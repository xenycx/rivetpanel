package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/valyala/fasthttp"

	"github.com/xenycx/rivetpanel/internal/agentproto"
)

// File names inside the agent's state directory. node.pem holds the node
// certificate and its private key together (mode 0600), so a renewal replaces
// both with one atomic rename.
const (
	ConfigFile   = "agent.json"
	IdentityFile = "node.pem"
	CAFile       = "ca.crt"
)

// Config is the agent's persisted connection settings.
type Config struct {
	PanelURL   string `json:"panel_url"`
	Connect    string `json:"connect"`     // host:port of the panel's agent listener
	ServerName string `json:"server_name"` // name checked in the listener's certificate
	NodeID     string `json:"node_id"`
	InstallID  string `json:"install_id,omitempty"` // learned on the first connection
}

// LoadConfig reads dir/agent.json.
func LoadConfig(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	if c.Connect == "" || c.NodeID == "" {
		return c, fmt.Errorf("%s is incomplete; enroll the agent again", ConfigFile)
	}
	return c, nil
}

// SaveConfig writes dir/agent.json atomically.
func SaveConfig(dir string, c Config) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(filepath.Join(dir, ConfigFile), b, 0o600)
}

// writeAtomic writes a file through a temporary file and rename, so a crash
// never leaves a half-written key or certificate.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newCSR() (keyPEM, csrPEM []byte, err error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "rivet-agent"}}, key)
	if err != nil {
		return nil, nil, err
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// EnrollResult is the panel's answer to an enrollment.
type EnrollResult struct {
	NodeID       string `json:"node_id"`
	Certificate  string `json:"certificate"`
	CA           string `json:"ca"`
	Serial       string `json:"serial"`
	ExpiresAtMS  int64  `json:"expires_at_ms"`
	AgentAddress string `json:"agent_address"`
}

// Enroll exchanges a one-use token for a node certificate and writes the
// agent's state directory. The private key never leaves this machine.
func Enroll(ctx context.Context, hc *http.Client, panelURL, token, dir, connectOverride string) (Config, error) {
	panelURL = strings.TrimRight(panelURL, "/")
	if !strings.HasPrefix(panelURL, "https://") && !strings.HasPrefix(panelURL, "http://") {
		return Config{}, errors.New("the panel address must start with https://")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Config{}, err
	}
	keyPEM, csrPEM, err := newCSR()
	if err != nil {
		return Config{}, err
	}
	body, _ := json.Marshal(map[string]string{"token": strings.TrimSpace(token), "csr": string(csrPEM)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, panelURL+"/api/agent/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("contact the panel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return Config{}, errors.New("the panel refused the token (wrong, expired, revoked or already used)")
		case http.StatusNotFound:
			return Config{}, errors.New("the panel does not accept agents (turn on the agents module)")
		}
		return Config{}, fmt.Errorf("enrollment failed (%d): %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	var r EnrollResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Config{}, err
	}
	connect := r.AgentAddress
	if connectOverride != "" {
		connect = connectOverride
	}
	if connect == "" {
		return Config{}, errors.New("the panel did not say where agents connect; pass --connect host:port")
	}
	host, _, err := net.SplitHostPort(connect)
	if err != nil {
		return Config{}, fmt.Errorf("connect address %q: %w", connect, err)
	}
	if err := writeAtomic(filepath.Join(dir, IdentityFile), append([]byte(r.Certificate), keyPEM...), 0o600); err != nil {
		return Config{}, err
	}
	if err := writeAtomic(filepath.Join(dir, CAFile), []byte(r.CA), 0o644); err != nil {
		return Config{}, err
	}
	c := Config{PanelURL: panelURL, Connect: connect, ServerName: host, NodeID: r.NodeID}
	return c, SaveConfig(dir, c)
}

// Agent keeps the connection to the panel and serves the node API on it.
type Agent struct {
	Dir     string
	Config  Config
	Handler fasthttp.RequestHandler // the node API (agentnode.App(...).Handler())
	Hello   func() agentproto.Hello
	// OnWelcome runs after every successful hello.
	OnWelcome func(agentproto.Welcome)
	Log       *slog.Logger

	Remote *Remote

	mu     sync.Mutex
	client *http.Client
	ys     *yamux.Session
}

// New prepares an agent for dir.
func New(dir string, cfg Config, handler fasthttp.RequestHandler, log *slog.Logger) *Agent {
	a := &Agent{Dir: dir, Config: cfg, Handler: handler, Log: log}
	a.Remote = &Remote{Client: a.currentClient}
	return a
}

func (a *Agent) currentClient() (*http.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil, ErrOffline
	}
	return a.client, nil
}

// Connected reports whether the agent is connected right now.
func (a *Agent) Connected() bool {
	_, err := a.currentClient()
	return err == nil
}

func (a *Agent) tlsConfig() (*tls.Config, *x509.Certificate, error) {
	id := filepath.Join(a.Dir, IdentityFile)
	pair, err := tls.LoadX509KeyPair(id, id)
	if err != nil {
		return nil, nil, fmt.Errorf("node certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(a.Dir, CAFile))
	if err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, nil, errors.New("ca.crt holds no certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: pool, ServerName: a.Config.ServerName}, leaf, nil
}

// Run connects and reconnects until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	wait := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > time.Minute {
			wait = time.Second // a long session resets the backoff
		}
		a.Log.Warn("panel connection ended", "err", err, "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
	return nil
}

func (a *Agent) session(ctx context.Context) error {
	cfg, leaf, err := a.tlsConfig()
	if err != nil {
		return err
	}
	if time.Now().After(leaf.NotAfter) {
		return errors.New("the node certificate expired; enroll the agent again with a new token")
	}
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", a.Config.Connect, cfg)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", a.Config.Connect, err)
	}
	ycfg := yamux.DefaultConfig()
	ycfg.EnableKeepAlive, ycfg.KeepAliveInterval = true, 15*time.Second
	ycfg.ConnectionWriteTimeout = 30 * time.Second
	ycfg.LogOutput = io.Discard
	ys, err := yamux.Client(conn, ycfg)
	if err != nil {
		conn.Close()
		return err
	}
	defer ys.Close()
	client := &http.Client{Transport: &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return ys.Open() },
		MaxIdleConnsPerHost: 16, IdleConnTimeout: 60 * time.Second,
	}}
	srv := &fasthttp.Server{Handler: a.Handler, StreamRequestBody: true, MaxRequestBodySize: 4 << 30,
		ReadTimeout: 0, WriteTimeout: 0, IdleTimeout: 2 * time.Minute, NoDefaultServerHeader: true}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ys) }()

	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	welcome, err := (&Remote{Client: func() (*http.Client, error) { return client, nil }}).Hello(hctx, a.Hello())
	cancel()
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if welcome.NodeID != a.Config.NodeID {
		return fmt.Errorf("the panel identified this agent as node %s, expected %s", welcome.NodeID, a.Config.NodeID)
	}
	a.mu.Lock()
	a.client, a.ys = client, ys
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.ys == ys {
			a.client, a.ys = nil, nil
		}
		a.mu.Unlock()
	}()
	a.Log.Info("connected to the panel", "address", a.Config.Connect, "node", welcome.NodeName)
	if a.OnWelcome != nil {
		a.OnWelcome(welcome)
	}
	go a.maybeRotate(ctx, leaf, ys)

	select {
	case <-ctx.Done():
		return nil
	case <-ys.CloseChan():
		return errors.New("connection closed")
	case err := <-served:
		return err
	}
}

// maybeRotate renews the certificate when a third of its lifetime is left,
// then reconnects so the new certificate is used (and proven) at once.
func (a *Agent) maybeRotate(ctx context.Context, leaf *x509.Certificate, ys *yamux.Session) {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	if time.Until(leaf.NotAfter) > life/3 {
		return
	}
	keyPEM, csrPEM, err := newCSR()
	if err != nil {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := a.Remote.Rotate(rctx, string(csrPEM))
	if err != nil {
		a.Log.Warn("certificate renewal failed; retrying on the next connection", "err", err)
		return
	}
	// Certificate and key are replaced together by one rename.
	if err := writeAtomic(filepath.Join(a.Dir, IdentityFile), append([]byte(r.Certificate), keyPEM...), 0o600); err != nil {
		a.Log.Warn("could not save the renewed certificate", "err", err)
		return
	}
	a.Log.Info("node certificate renewed", "serial", r.Serial, "expires", time.UnixMilli(r.ExpiresAtMS).UTC().Format(time.RFC3339))
	ys.Close()
}
