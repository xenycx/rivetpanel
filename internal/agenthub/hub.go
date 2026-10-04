// Package agenthub is the control-plane end of rivet-agent connections. Agents
// dial the agent listener with their panel-issued client certificate; the hub
// verifies the certificate against the database on every handshake, then runs
// HTTP in both directions over yamux streams on that one connection.
package agenthub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// Store is the persistence the hub needs.
type Store interface {
	AuthorizeAgentCertificate(ctx context.Context, nodeID, serial string, nowMS int64) error
	GetNode(ctx context.Context, id string) (domain.Node, error)
	AgentConnected(ctx context.Context, nodeID string, protocol int, version, hostname, capsJSON string, nowMS int64) error
	AgentDisconnected(ctx context.Context, nodeID string, nowMS int64) error
	GetBot(ctx context.Context, id string) (domain.Bot, error)
	ListBotsByNode(ctx context.Context, nodeID string) ([]domain.Bot, error)
	Observe(ctx context.Context, o sqlite.Observation) (bool, error)
	MarkNodeObservedUnknown(ctx context.Context, nodeID string, nowMS int64) (int64, error)
	TouchNode(ctx context.Context, nodeID string, nowMS int64) error
	DeleteBotRow(ctx context.Context, id string) error
	GetBlueprintRevision(ctx context.Context, id string, rev int64) (domain.BlueprintRevision, error)
	SetInstallState(ctx context.Context, botID, state string, imageChoice *string, nowMS int64) error
	SetInstalledVersion(ctx context.Context, botID, version string) error
	RecordRotatedCertificate(ctx context.Context, nodeID, serial string, expiresAtMS, nowMS int64) error
	RetireSupersededAgentCertificates(ctx context.Context, nodeID, serial string, nowMS int64) (int, error)
}

// EnvSource decrypts a server's variables for the agent that runs it.
type EnvSource interface {
	DecryptEnv(ctx context.Context, botID string) (map[string]string, error)
}

// BuildRecorder records build operations (the runner.BuildRecorder contract).
type BuildRecorder interface {
	BuildStarted(ctx context.Context, botID string, generation int64) string
	BuildStage(ctx context.Context, id, stage string)
	BuildOutput(id string) io.WriteCloser
	BuildFinished(ctx context.Context, id, status, code, msg string)
}

// ErrOffline means the node's agent is not connected.
var ErrOffline = errors.New("the node's agent is not connected")

// Hub tracks connected agents.
type Hub struct {
	Store     Store
	CA        *agentcert.Authority
	Env       EnvSource
	Builds    BuildRecorder // optional
	InstallID string
	Log       *slog.Logger
	Now       func() time.Time
	// OnConnect runs after an agent connected (for example to resync).
	OnConnect func(nodeID string)
	// OnDisconnect runs after the current connection of a node ended.
	OnDisconnect func(nodeID string)

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	nodeID string
	serial string
	ys     *yamux.Session
	client *http.Client
	done   chan struct{}

	mu  sync.Mutex
	ops map[string]bool // build operations started through this session
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Hub) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// TLSConfig is the listener's configuration: TLS 1.3, a client certificate
// chained to the agent CA is required, and its serial must be a current,
// unrevoked certificate of an enabled agent node.
func (h *Hub) TLSConfig(server tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    h.CA.Pool(),
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no client certificate")
			}
			nodeID, serial, err := agentcert.NodeIdentity(cs.PeerCertificates[0])
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return h.Store.AuthorizeAgentCertificate(ctx, nodeID, serial, h.now().UnixMilli())
		},
	}
}

// Serve accepts agent connections on ln (TLS is applied here) until ctx ends.
func (h *Hub) Serve(ctx context.Context, ln net.Listener, server tls.Certificate) error {
	tl := tls.NewListener(ln, h.TLSConfig(server))
	go func() {
		<-ctx.Done()
		tl.Close()
		h.closeAll()
	}()
	for {
		c, err := tl.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go h.handle(ctx, c.(*tls.Conn))
	}
}

func (h *Hub) handle(ctx context.Context, c *tls.Conn) {
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := c.HandshakeContext(hctx)
	cancel()
	if err != nil {
		h.log().Warn("agent handshake refused", "remote", c.RemoteAddr().String(), "err", err)
		c.Close()
		return
	}
	nodeID, serial, err := agentcert.NodeIdentity(c.ConnectionState().PeerCertificates[0])
	if err != nil {
		c.Close()
		return
	}
	// The first successful handshake with a certificate retires every older
	// certificate of the node (a completed rotation). Doing it here, not when
	// the renewal is issued, keeps an agent that crashed before persisting
	// its renewal able to reconnect with the previous certificate.
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	retired, err := h.Store.RetireSupersededAgentCertificates(rctx, nodeID, serial, h.now().UnixMilli())
	rcancel()
	if err != nil {
		// The certificate was authorized a moment ago; a failure here means
		// it was revoked concurrently or the database is unavailable.
		h.log().Warn("agent certificate bookkeeping failed", "node", nodeID, "err", err)
		c.Close()
		return
	}
	if retired > 0 {
		h.log().Info("superseded agent certificates revoked", "node", nodeID, "count", retired)
	}
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive, cfg.KeepAliveInterval = true, 15*time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.LogOutput = io.Discard
	ys, err := yamux.Server(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	s := &session{nodeID: nodeID, serial: serial, ys: ys, done: make(chan struct{}), ops: map[string]bool{}}
	s.client = &http.Client{Transport: &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return ys.Open() },
		MaxIdleConnsPerHost: 16, IdleConnTimeout: 60 * time.Second, ResponseHeaderTimeout: 0,
	}}
	h.register(s)
	srv := &http.Server{Handler: h.agentAPI(s), ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ys.CloseChan()
		srv.Close()
	}()
	_ = srv.Serve(ys) // agent-initiated streams
	h.unregister(s)
}

func (h *Hub) register(s *session) {
	h.mu.Lock()
	if h.sessions == nil {
		h.sessions = map[string]*session{}
	}
	old := h.sessions[s.nodeID]
	h.sessions[s.nodeID] = s
	h.mu.Unlock()
	if old != nil {
		old.ys.Close() // a reconnect replaces the previous connection
	}
	h.log().Info("agent connected", "node", s.nodeID)
}

func (h *Hub) unregister(s *session) {
	h.mu.Lock()
	current := h.sessions[s.nodeID] == s
	if current {
		delete(h.sessions, s.nodeID)
	}
	h.mu.Unlock()
	s.ys.Close()
	close(s.done)
	if current {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Store.AgentDisconnected(ctx, s.nodeID, h.now().UnixMilli())
		h.log().Info("agent disconnected", "node", s.nodeID)
		if h.OnDisconnect != nil {
			go h.OnDisconnect(s.nodeID)
		}
	}
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	list := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		list = append(list, s)
	}
	h.mu.Unlock()
	for _, s := range list {
		s.ys.Close()
	}
}

// Connected reports whether a node's agent is connected.
func (h *Hub) Connected(nodeID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[nodeID]
	return ok
}

// ConnectedNodes lists the nodes whose agents are connected now.
func (h *Hub) ConnectedNodes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		out = append(out, id)
	}
	return out
}

// Disconnect closes a node's connection (after a certificate revocation).
func (h *Hub) Disconnect(nodeID string) {
	h.mu.Lock()
	s := h.sessions[nodeID]
	h.mu.Unlock()
	if s != nil {
		s.ys.Close()
	}
}

// Client returns an HTTP client whose requests reach the node's agent.
func (h *Hub) Client(nodeID string) (*http.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[nodeID]
	if !ok {
		return nil, ErrOffline
	}
	return s.client, nil
}

// Do sends a request to the node's agent; path starts with /node/v1/.
func (h *Hub) Do(ctx context.Context, nodeID, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	c, err := h.Client(nodeID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(req)
}

// Call sends a JSON request and decodes a JSON answer (out may be nil).
func (h *Hub) Call(ctx context.Context, nodeID, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytesReader(b)
	}
	resp, err := h.Do(ctx, nodeID, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("agent answered %d: %s", resp.StatusCode, msg)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
	}
	return nil
}

// Notify asks the node's agent to reconcile a server. It is best effort: the
// agent also resynchronizes periodically and after every reconnect.
func (h *Hub) Notify(nodeID, botID string) {
	if !h.Connected(nodeID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.Call(ctx, nodeID, http.MethodPost, "/node/v1/notify", agentproto.Notify{BotID: botID}, nil); err != nil {
			h.log().Debug("notify agent", "node", nodeID, "bot", botID, "err", err)
		}
	}()
}

// --- agent → panel API ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	return dec.Decode(v)
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, domain.ErrConflict):
		http.Error(w, "conflict", http.StatusConflict)
	default:
		var v *domain.ValidationError
		if errors.As(err, &v) {
			http.Error(w, v.Msg, http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Hub) agentAPI(s *session) http.Handler {
	mux := http.NewServeMux()
	// botOnNode loads a server and refuses servers of other nodes.
	botOnNode := func(ctx context.Context, id string) (domain.Bot, error) {
		b, err := h.Store.GetBot(ctx, id)
		if err != nil {
			return b, err
		}
		if b.NodeID != s.nodeID {
			return domain.Bot{}, domain.ErrNotFound
		}
		return b, nil
	}
	mux.HandleFunc("POST /agent/v1/hello", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.Hello
		if err := readJSON(r, &in); err != nil {
			http.Error(w, "bad hello", http.StatusBadRequest)
			return
		}
		if in.Protocol != agentproto.Version {
			http.Error(w, fmt.Sprintf("protocol %d is not supported (this panel speaks %d); update rivet-agent", in.Protocol, agentproto.Version), http.StatusConflict)
			return
		}
		node, err := h.Store.GetNode(r.Context(), s.nodeID)
		if err != nil {
			fail(w, err)
			return
		}
		caps, _ := json.Marshal(in.Capabilities)
		if err := h.Store.AgentConnected(r.Context(), s.nodeID, in.Protocol, clip(in.AgentVersion, 64), clip(in.Hostname, 253), string(caps), h.now().UnixMilli()); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, agentproto.Welcome{NodeID: node.ID, NodeName: node.Name, InstallID: h.InstallID})
		if h.OnConnect != nil {
			go h.OnConnect(s.nodeID)
		}
	})
	mux.HandleFunc("GET /agent/v1/bots", func(w http.ResponseWriter, r *http.Request) {
		list, err := h.Store.ListBotsByNode(r.Context(), s.nodeID)
		if err != nil {
			fail(w, err)
			return
		}
		if list == nil {
			list = []domain.Bot{}
		}
		writeJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /agent/v1/bots/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, err := botOnNode(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, b)
	})
	mux.HandleFunc("DELETE /agent/v1/bots/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := botOnNode(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		if err := h.Store.DeleteBotRow(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/observe", func(w http.ResponseWriter, r *http.Request) {
		var o sqlite.Observation
		if err := readJSON(r, &o); err != nil {
			http.Error(w, "bad observation", http.StatusBadRequest)
			return
		}
		if _, err := botOnNode(r.Context(), o.BotID); err != nil {
			fail(w, err)
			return
		}
		ok, err := h.Store.Observe(r.Context(), o)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, agentproto.ObserveResult{Updated: ok})
	})
	mux.HandleFunc("POST /agent/v1/node/unknown", func(w http.ResponseWriter, r *http.Request) {
		n, err := h.Store.MarkNodeObservedUnknown(r.Context(), s.nodeID, h.now().UnixMilli())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"count": n})
	})
	mux.HandleFunc("POST /agent/v1/node/touch", func(w http.ResponseWriter, r *http.Request) {
		if err := h.Store.TouchNode(r.Context(), s.nodeID, h.now().UnixMilli()); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /agent/v1/bots/{id}/env", func(w http.ResponseWriter, r *http.Request) {
		if _, err := botOnNode(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		env, err := h.Env.DecryptEnv(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		if env == nil {
			env = map[string]string{}
		}
		writeJSON(w, http.StatusOK, env)
	})
	mux.HandleFunc("GET /agent/v1/blueprints/{id}/{rev}", func(w http.ResponseWriter, r *http.Request) {
		rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
		if err != nil {
			http.Error(w, "bad revision", http.StatusBadRequest)
			return
		}
		out, err := h.Store.GetBlueprintRevision(r.Context(), r.PathValue("id"), rev)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /agent/v1/bots/{id}/install-state", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.InstallState
		if err := readJSON(r, &in); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := botOnNode(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		if err := h.Store.SetInstallState(r.Context(), r.PathValue("id"), in.State, in.ImageChoice, h.now().UnixMilli()); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/bots/{id}/installed-version", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.InstalledVersion
		if err := readJSON(r, &in); err != nil || len(in.Version) > 64 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := botOnNode(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		if err := h.Store.SetInstalledVersion(r.Context(), r.PathValue("id"), in.Version); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/builds", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.BuildStart
		if err := readJSON(r, &in); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := botOnNode(r.Context(), in.BotID); err != nil {
			fail(w, err)
			return
		}
		id := ""
		if h.Builds != nil {
			id = h.Builds.BuildStarted(r.Context(), in.BotID, in.Generation)
		}
		if id != "" {
			s.mu.Lock()
			s.ops[id] = true
			s.mu.Unlock()
		}
		writeJSON(w, http.StatusOK, agentproto.BuildStarted{ID: id})
	})
	ownOp := func(id string) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return h.Builds != nil && s.ops[id]
	}
	mux.HandleFunc("POST /agent/v1/builds/{op}/stage", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.BuildStage
		if err := readJSON(r, &in); err != nil || !ownOp(r.PathValue("op")) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.Builds.BuildStage(r.Context(), r.PathValue("op"), clip(in.Stage, 64))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/builds/{op}/output", func(w http.ResponseWriter, r *http.Request) {
		if !ownOp(r.PathValue("op")) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		out := h.Builds.BuildOutput(r.PathValue("op"))
		if out == nil {
			_, _ = io.Copy(io.Discard, r.Body)
		} else {
			_, _ = io.Copy(out, io.LimitReader(r.Body, 64<<20))
			out.Close()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/builds/{op}/finish", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.BuildFinish
		if err := readJSON(r, &in); err != nil || !ownOp(r.PathValue("op")) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.Builds.BuildFinished(r.Context(), r.PathValue("op"), in.Status, clip(in.Code, 64), clip(in.Msg, 2000))
		s.mu.Lock()
		delete(s.ops, r.PathValue("op"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agent/v1/rotate", func(w http.ResponseWriter, r *http.Request) {
		var in agentproto.Rotate
		if err := readJSON(r, &in); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		csr, err := agentcert.ParseCSR(in.CSR)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		certPEM, serial, expires, err := h.CA.Issue(csr, s.nodeID)
		if err != nil {
			fail(w, err)
			return
		}
		if err := h.Store.RecordRotatedCertificate(r.Context(), s.nodeID, serial, expires, h.now().UnixMilli()); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, agentproto.Rotated{Certificate: string(certPEM), CA: string(h.CA.CertificatePEM()), Serial: serial, ExpiresAtMS: expires})
	})
	return mux
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// PeerNode is exported for tests: the node identity of a verified peer.
func PeerNode(c *x509.Certificate) (string, error) {
	id, _, err := agentcert.NodeIdentity(c)
	return id, err
}

// Forward sends a prepared request to the node's agent. Only the path,
// query, method, headers and body of req are used.
func (h *Hub) Forward(nodeID string, req *http.Request) (*http.Response, error) {
	c, err := h.Client(nodeID)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme, req.URL.Host, req.Host = "http", "agent", "agent"
	req.RequestURI = ""
	return c.Do(req)
}
