package agentclient_test

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
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/valyala/fasthttp"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agentclient"
	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// recordingStore records every serial the hub authorized at a handshake.
type recordingStore struct {
	*sqlite.DB
	mu      sync.Mutex
	serials []string
}

func (s *recordingStore) AuthorizeAgentCertificate(ctx context.Context, nodeID, serial string, nowMS int64) error {
	err := s.DB.AuthorizeAgentCertificate(ctx, nodeID, serial, nowMS)
	if err == nil {
		s.mu.Lock()
		s.serials = append(s.serials, serial)
		s.mu.Unlock()
	}
	return err
}

func (s *recordingStore) authorized() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.serials...)
}

type noEnv struct{}

func (noEnv) DecryptEnv(context.Context, string) (map[string]string, error) { return nil, nil }

func caFiles(t *testing.T, dir string) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	cb, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	kb, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	cblk, _ := pem.Decode(cb)
	cert, err := x509.ParseCertificate(cblk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	kblk, _ := pem.Decode(kb)
	key, err := x509.ParsePKCS8PrivateKey(kblk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key.(ed25519.PrivateKey)
}

func readIdentity(t *testing.T, dir string) (*x509.Certificate, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, agentclient.IdentityFile))
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		t.Fatalf("node.pem is not a usable certificate+key pair: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf, raw
}

// TestAgentRotatesNearExpiryCertificateAndReconnects enrolls a node whose
// certificate has less than a third of its lifetime left. The agent must
// request a renewal over the authenticated connection, atomically replace
// node.pem (certificate and new key together), drop the connection and
// reconnect with the renewed certificate.
func TestAgentRotatesNearExpiryCertificateAndReconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	caDir := t.TempDir()
	ca, err := agentcert.LoadOrCreate(caDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{DB: db}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := &agenthub.Hub{Store: store, CA: ca, Env: noEnv{}, InstallID: "install-1", Log: quiet}
	serverCert, err := ca.IssueServer([]string{"127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hubDone := make(chan struct{})
	go func() { _ = hub.Serve(ctx, ln, serverCert); close(hubDone) }()

	// Enroll a node with a hand-signed, nearly expired (day 29 of 30)
	// certificate from the real CA, recorded through the enrollment path.
	now := time.Now()
	nodeID := uuid.NewString()
	tokenHash := sha256.Sum256([]byte(nodeID))
	if err := db.CreateAgentEnrollment(ctx, domain.AgentEnrollment{ID: uuid.NewString(), NodeID: nodeID, NodeName: "edge",
		LocationID: domain.LocalLocationID, Prefix: "rpa_test_e", TokenHash: tokenHash[:],
		CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	_, nodeKey, _ := ed25519.GenerateKey(rand.Reader)
	caCert, caKey := caFiles(t, caDir)
	identity, _ := url.Parse("spiffe://rivetpanel/node/" + nodeID)
	oldSerial := big.NewInt(0).SetBytes([]byte("old-serial-0001"))
	tpl := &x509.Certificate{SerialNumber: oldSerial, Subject: pkix.Name{CommonName: "rivet-agent:" + nodeID},
		NotBefore: now.Add(-29 * 24 * time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs: []*url.URL{identity}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, nodeKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	oldSerialHex := hex.EncodeToString(oldSerial.Bytes())
	if _, err := db.EnrollAgent(ctx, tokenHash[:], now.UnixMilli(), func(string) (string, int64, error) {
		return oldSerialHex, tpl.NotAfter.UnixMilli(), nil
	}); err != nil {
		t.Fatal(err)
	}

	// Write the agent's state directory as Enroll would.
	dir := t.TempDir()
	keyDER, _ := x509.MarshalPKCS8PrivateKey(nodeKey)
	oldIdentity := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
	if err := os.WriteFile(filepath.Join(dir, agentclient.IdentityFile), oldIdentity, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentclient.CAFile), ca.CertificatePEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := agentclient.Config{PanelURL: "https://panel.invalid", Connect: ln.Addr().String(), ServerName: "127.0.0.1", NodeID: nodeID}

	agent := agentclient.New(dir, cfg, func(c *fasthttp.RequestCtx) { c.SetStatusCode(fasthttp.StatusNoContent) }, quiet)
	agent.Hello = func() agentproto.Hello { return agentproto.Hello{Protocol: agentproto.Version, Hostname: "edge"} }
	var welcomes sync.WaitGroup
	welcomes.Add(2) // the original connection and the reconnect after renewal
	var once [2]sync.Once
	var count int
	var cmu sync.Mutex
	agent.OnWelcome = func(w agentproto.Welcome) {
		cmu.Lock()
		i := count
		count++
		cmu.Unlock()
		if i < 2 {
			once[i].Do(welcomes.Done)
		}
	}
	runDone := make(chan error, 1)
	go func() { runDone <- agent.Run(ctx) }()

	waited := make(chan struct{})
	go func() { welcomes.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatalf("agent did not reconnect after renewal; authorized serials: %v", store.authorized())
	}

	// node.pem now holds a renewed certificate and a NEW private key.
	leaf, newIdentity := readIdentity(t, dir)
	if bytes.Equal(newIdentity, oldIdentity) {
		t.Fatal("node.pem was not replaced")
	}
	gotNode, newSerial, err := agentcert.NodeIdentity(leaf)
	if err != nil || gotNode != nodeID {
		t.Fatalf("renewed identity = %s, %v", gotNode, err)
	}
	if newSerial == oldSerialHex {
		t.Fatal("renewal reused the old serial")
	}
	if pub, ok := leaf.PublicKey.(ed25519.PublicKey); !ok || pub.Equal(nodeKey.Public()) {
		t.Fatal("renewal did not rotate the private key")
	}
	if !leaf.NotAfter.After(now.Add(25 * 24 * time.Hour)) {
		t.Fatalf("renewed certificate expires %s; want a fresh 30-day lifetime", leaf.NotAfter)
	}
	if info, err := os.Stat(filepath.Join(dir, agentclient.IdentityFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("node.pem mode = %v, %v", info, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}

	// The panel recorded the renewal as the node's current certificate, and
	// the reconnect authenticated with it.
	st, err := db.GetAgentState(ctx, nodeID)
	if err != nil || st.CertificateSerial == nil || *st.CertificateSerial != newSerial {
		t.Fatalf("panel current serial = %+v, %v; want %s", st.CertificateSerial, err, newSerial)
	}
	auth := store.authorized()
	if len(auth) < 2 || auth[0] != oldSerialHex || auth[len(auth)-1] != newSerial {
		t.Fatalf("authorized serials = %v; want first %s then %s", auth, oldSerialHex, newSerial)
	}
	if !hub.Connected(nodeID) || !agent.Connected() {
		t.Fatal("agent not connected after renewal")
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not stop")
	}
	<-hubDone

	// Reconnecting with the renewal retired the superseded certificate: the
	// panel refuses it from now on even though it has not expired.
	if err := db.AuthorizeAgentCertificate(context.Background(), nodeID, oldSerialHex, time.Now().UnixMilli()); err == nil {
		t.Fatal("superseded certificate is still accepted after the agent reconnected with its renewal")
	}
}

// TestAgentKeepsCertificateWhenFarFromExpiry checks that a fresh certificate
// is not renewed on connect.
func TestAgentKeepsCertificateWhenFarFromExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	ca, err := agentcert.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := &agenthub.Hub{Store: db, CA: ca, Env: noEnv{}, Log: quiet}
	serverCert, _ := ca.IssueServer([]string{"127.0.0.1"}, time.Hour)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hubDone := make(chan struct{})
	go func() { _ = hub.Serve(ctx, ln, serverCert); close(hubDone) }()

	now := time.Now()
	nodeID := uuid.NewString()
	tokenHash := sha256.Sum256([]byte(nodeID))
	if err := db.CreateAgentEnrollment(ctx, domain.AgentEnrollment{ID: uuid.NewString(), NodeID: nodeID, NodeName: "fresh",
		LocationID: domain.LocalLocationID, Prefix: "rpa_test_f", TokenHash: tokenHash[:],
		CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	_, nodeKey, _ := ed25519.GenerateKey(rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "rivet-agent"}}, nodeKey)
	csr, err := agentcert.ParseCSR(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})))
	if err != nil {
		t.Fatal(err)
	}
	var certPEM []byte
	if _, err := db.EnrollAgent(ctx, tokenHash[:], now.UnixMilli(), func(id string) (string, int64, error) {
		c, serial, exp, err := ca.Issue(csr, id)
		certPEM = c
		return serial, exp, err
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyDER, _ := x509.MarshalPKCS8PrivateKey(nodeKey)
	identity := append(append([]byte(nil), certPEM...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
	if err := os.WriteFile(filepath.Join(dir, agentclient.IdentityFile), identity, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentclient.CAFile), ca.CertificatePEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := agentclient.New(dir, agentclient.Config{Connect: ln.Addr().String(), ServerName: "127.0.0.1", NodeID: nodeID},
		func(c *fasthttp.RequestCtx) {}, quiet)
	agent.Hello = func() agentproto.Hello { return agentproto.Hello{Protocol: agentproto.Version} }
	welcomed := make(chan struct{}, 4)
	agent.OnWelcome = func(agentproto.Welcome) { welcomed <- struct{}{} }
	runDone := make(chan error, 1)
	go func() { runDone <- agent.Run(ctx) }()
	select {
	case <-welcomed:
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not connect")
	}
	time.Sleep(500 * time.Millisecond) // give a (wrong) renewal time to happen
	_, after := readIdentity(t, dir)
	if !bytes.Equal(after, identity) {
		t.Fatal("fresh certificate was renewed")
	}
	if certs, err := db.ListAgentCertificates(ctx, nodeID); err != nil || len(certs) != 1 {
		t.Fatalf("certificates = %d, %v; want 1", len(certs), err)
	}
	if !hub.Connected(nodeID) {
		t.Fatal("hub lost the connection")
	}
	cancel()
	<-runDone
	<-hubDone
}
