package agenthub_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/yamux"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// --- fixture ---

type fakeEnv struct{}

func (fakeEnv) DecryptEnv(context.Context, string) (map[string]string, error) {
	return map[string]string{"SECRET": "value"}, nil
}

type fixture struct {
	db     *sqlite.DB
	ca     *agentcert.Authority
	caDir  string
	hub    *agenthub.Hub
	addr   string
	caPool *x509.CertPool
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u1','u1@x','hash',1,1)`); err != nil {
		t.Fatal(err)
	}
	caDir := t.TempDir()
	ca, err := agentcert.LoadOrCreate(caDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, ca: ca, caDir: caDir, caPool: ca.Pool(), now: time.Now()}
	f.hub = &agenthub.Hub{Store: db, CA: ca, Env: fakeEnv{}, InstallID: "install-1",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return f.now }}
	serverCert, err := ca.IssueServer([]string{"127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.addr = ln.Addr().String()
	done := make(chan struct{})
	go func() { _ = f.hub.Serve(ctx, ln, serverCert); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	return f
}

// enrolledNode is a node that enrolled through the real enrollment path.
type enrolledNode struct {
	id     string
	serial string
	cert   tls.Certificate
	pub    ed25519.PublicKey
	key    ed25519.PrivateKey
}

func (f *fixture) enroll(t *testing.T, name string) enrolledNode {
	t.Helper()
	ctx := context.Background()
	nowMS := time.Now().UnixMilli()
	tokenHash := sha256.Sum256([]byte(uuid.NewString()))
	e := domain.AgentEnrollment{ID: uuid.NewString(), NodeID: uuid.NewString(), NodeName: name,
		LocationID: domain.LocalLocationID, Prefix: "rpa_test_" + name[:1], TokenHash: tokenHash[:],
		CreatedAtMS: nowMS, ExpiresAtMS: nowMS + time.Hour.Milliseconds()}
	if err := f.db.CreateAgentEnrollment(ctx, e); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	csr := csrFor(t, key)
	var certPEM []byte
	if _, err := f.db.EnrollAgent(ctx, tokenHash[:], nowMS, func(nodeID string) (string, int64, error) {
		c, serial, exp, err := f.ca.Issue(csr, nodeID)
		certPEM = c
		return serial, exp, err
	}); err != nil {
		t.Fatal(err)
	}
	leaf := parsePEMCert(t, certPEM)
	_, serial, err := agentcert.NodeIdentity(leaf)
	if err != nil {
		t.Fatal(err)
	}
	return enrolledNode{id: e.NodeID, serial: serial, pub: pub, key: key,
		cert: tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}}
}

func (f *fixture) createBot(t *testing.T, id, nodeID string) {
	t.Helper()
	err := f.db.CreateBot(context.Background(), domain.Bot{ID: id, OwnerID: "u1", NodeID: nodeID, Name: id, Runtime: "nodejs",
		ImageRef: "i", Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})
	if err != nil {
		t.Fatal(err)
	}
}

func csrFor(t *testing.T, key ed25519.PrivateKey) *x509.CertificateRequest {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "rivet-agent"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := agentcert.ParseCSR(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})))
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

func parsePEMCert(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	blk, _ := pem.Decode(b)
	if blk == nil {
		t.Fatal("no PEM certificate")
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// loadCAFiles reads a CA directory written by agentcert.LoadOrCreate so a test
// can sign certificates the Authority API would never issue.
func loadCAFiles(t *testing.T, dir string) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := pem.Decode(keyPEM)
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return parsePEMCert(t, certPEM), k.(ed25519.PrivateKey)
}

// signClient signs a node client certificate with an arbitrary CA, serial and
// validity window, mirroring the shape agentcert.Authority.Issue produces.
func signClient(t *testing.T, caDir, nodeID string, serial *big.Int, notBefore, notAfter time.Time, key ed25519.PrivateKey) tls.Certificate {
	t.Helper()
	caCert, caKey := loadCAFiles(t, caDir)
	identity, _ := url.Parse("spiffe://rivetpanel/node/" + nodeID)
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "rivet-agent:" + nodeID},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{identity}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, key.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func serialInt(t *testing.T, hexSerial string) *big.Int {
	t.Helper()
	b, err := hex.DecodeString(hexSerial)
	if err != nil {
		t.Fatal(err)
	}
	return new(big.Int).SetBytes(b)
}

// agentConn is a test agent connection: it speaks the agent side of the
// yamux/HTTP protocol with the given client certificate.
type agentConn struct {
	client *http.Client
	ys     *yamux.Session
}

func (f *fixture) dial(t *testing.T, cert tls.Certificate) (*agentConn, error) {
	t.Helper()
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", f.addr, &tls.Config{MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{cert}, RootCAs: f.caPool, ServerName: "127.0.0.1"})
	if err != nil {
		return nil, err
	}
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.ConnectionWriteTimeout = 5 * time.Second
	ys, err := yamux.Client(conn, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	t.Cleanup(func() { ys.Close() })
	return &agentConn{ys: ys, client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return ys.Open() }}}}, nil
}

func (a *agentConn) call(method, path string, in any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://panel"+path, body)
	if err != nil {
		return 0, nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func (a *agentConn) hello() (agentproto.Welcome, error) {
	var w agentproto.Welcome
	code, body, err := a.call(http.MethodPost, "/agent/v1/hello", agentproto.Hello{Protocol: agentproto.Version, Hostname: "test"})
	if err != nil {
		return w, err
	}
	if code != http.StatusOK {
		return w, fmt.Errorf("hello answered %d: %s", code, body)
	}
	return w, json.Unmarshal(body, &w)
}

// connect dials and says hello; a nil error means the hub accepted the agent.
func (f *fixture) connect(t *testing.T, cert tls.Certificate) (*agentConn, error) {
	t.Helper()
	a, err := f.dial(t, cert)
	if err != nil {
		return nil, err
	}
	if _, err := a.hello(); err != nil {
		return nil, err
	}
	return a, nil
}

func (f *fixture) mustBeRefused(t *testing.T, what string, cert tls.Certificate, nodeID string) {
	t.Helper()
	if _, err := f.connect(t, cert); err == nil {
		t.Fatalf("%s: hub accepted the connection", what)
	}
	if f.hub.Connected(nodeID) {
		t.Fatalf("%s: hub registered a session for node %s", what, nodeID)
	}
	var connected int
	if err := f.db.QueryRowContext(context.Background(), `SELECT connected FROM node_agent_state WHERE node_id = ?`, nodeID).Scan(&connected); err == nil && connected != 0 {
		t.Fatalf("%s: node recorded as connected", what)
	}
}

// --- tests ---

func TestHubAcceptsValidCertificateForItsNode(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	a, err := f.connect(t, n.cert)
	if err != nil {
		t.Fatalf("valid certificate refused: %v", err)
	}
	w, err := a.hello()
	if err != nil || w.NodeID != n.id || w.NodeName != "alpha" || w.InstallID != "install-1" {
		t.Fatalf("welcome = %+v, %v", w, err)
	}
	if !f.hub.Connected(n.id) {
		t.Fatal("hub does not report the node connected")
	}
}

// TestHubRefusesPreviousProtocol: an agent one protocol behind (for example
// a protocol 7 agent without the port probe) is refused at hello with a
// message that names both versions, and is never recorded as connected.
func TestHubRefusesPreviousProtocol(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	a, err := f.dial(t, n.cert)
	if err != nil {
		t.Fatal(err)
	}
	code, body, err := a.call(http.MethodPost, "/agent/v1/hello", agentproto.Hello{Protocol: agentproto.Version - 1, Hostname: "old"})
	want := fmt.Sprintf("protocol %d is not supported (this panel speaks %d); update rivet-agent", agentproto.Version-1, agentproto.Version)
	if err != nil || code != http.StatusConflict || !strings.Contains(string(body), want) {
		t.Fatalf("old hello = %d %q, %v", code, body, err)
	}
	// The transport session exists until the agent hangs up after the
	// refusal, but the node is never recorded as connected.
	var connected int
	if err := f.db.QueryRowContext(context.Background(), `SELECT connected FROM node_agent_state WHERE node_id = ?`, n.id).Scan(&connected); err == nil && connected != 0 {
		t.Fatal("node recorded as connected with an older protocol")
	}
}

func TestHubRefusesCertificateFromForeignCA(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	// A different CA signs a certificate that copies the legitimate node
	// identity AND its recorded serial, so only chain verification can catch it.
	foreignDir := t.TempDir()
	if _, err := agentcert.LoadOrCreate(foreignDir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	forged := signClient(t, foreignDir, n.id, serialInt(t, n.serial), now.Add(-time.Minute), now.Add(24*time.Hour), n.key)
	f.mustBeRefused(t, "foreign CA", forged, n.id)

	// A self-signed certificate is refused too.
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	identity, _ := url.Parse("spiffe://rivetpanel/node/" + n.id)
	tpl := &x509.Certificate{SerialNumber: serialInt(t, n.serial), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{identity}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	f.mustBeRefused(t, "self-signed", tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, n.id)

	// The genuine certificate still works afterwards.
	if _, err := f.connect(t, n.cert); err != nil {
		t.Fatalf("valid certificate refused after forgery attempts: %v", err)
	}
}

func TestHubRefusesExpiredCertificates(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")

	// X.509 expiry: signed by the real CA, recorded in SQLite with a future
	// expiry, but NotAfter has passed. TLS chain verification must refuse it.
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	expired := signClient(t, f.caDir, n.id, serial, now.Add(-48*time.Hour), now.Add(-time.Hour), key)
	if err := f.db.RecordRotatedCertificate(context.Background(), n.id, hex.EncodeToString(serial.Bytes()),
		now.Add(24*time.Hour).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.mustBeRefused(t, "x509-expired", expired, n.id)

	// Database expiry: the node's genuine certificate, with the hub clock moved
	// past the expiry recorded in SQLite (agentcert issues 30-day certificates).
	f.now = time.Now().Add(31 * 24 * time.Hour)
	f.mustBeRefused(t, "db-expired", n.cert, n.id)
}

func TestHubRefusesRevokedCertificate(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	if _, err := f.connect(t, n.cert); err != nil {
		t.Fatalf("valid certificate refused before revocation: %v", err)
	}
	f.hub.Disconnect(n.id)
	waitFor(t, func() bool { return !f.hub.Connected(n.id) })
	if c, err := f.db.RevokeAgentCertificates(context.Background(), n.id, "test", time.Now().UnixMilli()); err != nil || c != 1 {
		t.Fatalf("revoke = %d, %v", c, err)
	}
	f.mustBeRefused(t, "revoked", n.cert, n.id)
}

func TestHubRefusesUnknownSerialAndDisabledNode(t *testing.T) {
	f := newFixture(t)
	n := f.enroll(t, "alpha")
	// Signed by the real CA but never recorded in SQLite.
	now := time.Now()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	unknown := signClient(t, f.caDir, n.id, big.NewInt(424242), now.Add(-time.Minute), now.Add(time.Hour), key)
	f.mustBeRefused(t, "unknown serial", unknown, n.id)

	disabled := false
	if err := f.db.UpdateNode(context.Background(), n.id, sqlite.NodeUpdate{Enabled: &disabled}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.mustBeRefused(t, "disabled node", n.cert, n.id)
}

func TestHubRefusesCertificateClaimingAnotherNode(t *testing.T) {
	f := newFixture(t)
	a := f.enroll(t, "alpha")
	b := f.enroll(t, "bravo")
	// Real CA, node A's recorded serial, but the identity of node B: the
	// serial-to-node binding in SQLite must refuse it.
	now := time.Now()
	swapped := signClient(t, f.caDir, b.id, serialInt(t, a.serial), now.Add(-time.Minute), now.Add(time.Hour), a.key)
	f.mustBeRefused(t, "serial of another node", swapped, b.id)
	if f.hub.Connected(a.id) {
		t.Fatal("node A registered by a certificate claiming node B")
	}
}

func TestHubRefusesCrossNodeBotAccess(t *testing.T) {
	f := newFixture(t)
	a := f.enroll(t, "alpha")
	b := f.enroll(t, "bravo")
	f.createBot(t, "bot-a", a.id)
	f.createBot(t, "bot-b", b.id)
	f.createBot(t, "bot-local", domain.LocalNodeID)

	connA, err := f.connect(t, a.cert)
	if err != nil {
		t.Fatal(err)
	}
	// Own server is reachable.
	if code, body, err := connA.call(http.MethodGet, "/agent/v1/bots/bot-a", nil); err != nil || code != http.StatusOK {
		t.Fatalf("own bot: %d %s %v", code, body, err)
	}
	// The list holds only node A's servers.
	code, body, err := connA.call(http.MethodGet, "/agent/v1/bots", nil)
	if err != nil || code != http.StatusOK {
		t.Fatalf("list: %d %s %v", code, body, err)
	}
	var list []domain.Bot
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "bot-a" {
		t.Fatalf("node A listed %+v", list)
	}

	for _, other := range []string{"bot-b", "bot-local", "does-not-exist"} {
		reqs := []struct {
			method, path string
			in           any
		}{
			{http.MethodGet, "/agent/v1/bots/" + other, nil},
			{http.MethodGet, "/agent/v1/bots/" + other + "/env", nil},
			{http.MethodPost, "/agent/v1/observe", sqlite.Observation{BotID: other}},
			{http.MethodPost, "/agent/v1/bots/" + other + "/install-state", agentproto.InstallState{State: "installed"}},
			{http.MethodPost, "/agent/v1/bots/" + other + "/installed-version", agentproto.InstalledVersion{Version: "1"}},
			{http.MethodPost, "/agent/v1/builds", agentproto.BuildStart{BotID: other, Generation: 1}},
			{http.MethodDelete, "/agent/v1/bots/" + other, nil},
		}
		for _, r := range reqs {
			code, body, err := connA.call(r.method, r.path, r.in)
			if err != nil {
				t.Fatalf("%s %s: %v", r.method, r.path, err)
			}
			if code != http.StatusNotFound {
				t.Errorf("node A %s %s = %d %s; want 404", r.method, r.path, code, body)
			}
		}
	}
	// Refused deletes left the other nodes' rows untouched.
	for _, id := range []string{"bot-b", "bot-local"} {
		if _, err := f.db.GetBot(context.Background(), id); err != nil {
			t.Fatalf("%s after refused delete: %v", id, err)
		}
	}
	// Node B can reach its own server over its own connection.
	connB, err := f.connect(t, b.cert)
	if err != nil {
		t.Fatal(err)
	}
	if code, body, err := connB.call(http.MethodGet, "/agent/v1/bots/bot-b/env", nil); err != nil || code != http.StatusOK {
		t.Fatalf("node B own env: %d %s %v", code, body, err)
	}
	// And the panel→agent direction is addressed by node, not by caller.
	if _, err := f.hub.Client(a.id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.hub.Client(uuid.NewString()); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("unknown node client: %v", err)
	}
}

func TestHubRotationBindsNewCertificateToCallingNode(t *testing.T) {
	f := newFixture(t)
	a := f.enroll(t, "alpha")
	conn, err := f.connect(t, a.cert)
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "rivet-agent"},
		URIs:    []*url.URL{{Scheme: "spiffe", Host: "rivetpanel", Path: "/node/" + uuid.NewString()}}}, key)
	if err != nil {
		t.Fatal(err)
	}
	code, body, err := conn.call(http.MethodPost, "/agent/v1/rotate",
		agentproto.Rotate{CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	if err != nil || code != http.StatusOK {
		t.Fatalf("rotate: %d %s %v", code, body, err)
	}
	var r agentproto.Rotated
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	leaf := parsePEMCert(t, []byte(r.Certificate))
	node, serial, err := agentcert.NodeIdentity(leaf)
	if err != nil || node != a.id || serial != r.Serial {
		t.Fatalf("rotated identity = %s/%s, %v (want node %s)", node, serial, err, a.id)
	}
	st, err := f.db.GetAgentState(context.Background(), a.id)
	if err != nil || st.CertificateSerial == nil || *st.CertificateSerial != r.Serial {
		t.Fatalf("current serial not updated: %+v %v", st, err)
	}
	// The rotated certificate connects.
	rotated := tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
	if _, err := f.connect(t, rotated); err != nil {
		t.Fatalf("rotated certificate refused: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
