package noderoute_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentcert"
	"github.com/xenycx/rivetpanel/internal/agentclient"
	"github.com/xenycx/rivetpanel/internal/agenthub"
	"github.com/xenycx/rivetpanel/internal/agentnode"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

type noEnv struct{}

func (noEnv) DecryptEnv(context.Context, string) (map[string]string, error) { return nil, nil }

// harnessAddons reports one running redis add-on per server and echoes
// what it was asked for as its log output.
type harnessAddons struct{}

func (harnessAddons) AddonStates(context.Context, string) (map[string]runner.AddonStatus, error) {
	return map[string]runner.AddonStatus{"redis": {State: "running", Health: "healthy"}}, nil
}

func (harnessAddons) AddonLogs(_ context.Context, botID, kind string, lines int) (string, error) {
	return fmt.Sprintf("%s %s %d", botID, kind, lines), nil
}

type idleRunner struct{}

func (idleRunner) Notify(string)                       {}
func (idleRunner) Purge(context.Context, string) error { return nil }
func (idleRunner) Kill(context.Context, string) error  { return nil }
func (idleRunner) Status() runner.Status               { return runner.Status{} }

// remoteNode runs a real panel hub and a real enrolled rivet-agent serving
// the node API over mutual TLS, in process. It returns the panel's router
// and the node's own workspaces.
func remoteNode(t *testing.T) (*noderoute.Router, string, *filesystem.Manager) {
	t.Helper()
	r, nodeID, files, _ := remoteNodeDB(t)
	return r, nodeID, files
}

// remoteNodeDB is remoteNode that also returns the panel's database.
func remoteNodeDB(t *testing.T) (*noderoute.Router, string, *filesystem.Manager, *sqlite.DB) {
	t.Helper()
	return remoteNodeWith(t, nil)
}

// remoteNodeWith is remoteNodeDB with the agent's services adjusted by opt
// (for example a diagnostic runner or telemetry) before the agent starts.
func remoteNodeWith(t *testing.T, opt func(*agentnode.Deps)) (*noderoute.Router, string, *filesystem.Manager, *sqlite.DB) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	ca, err := agentcert.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := &agenthub.Hub{Store: db, CA: ca, Env: noEnv{}, InstallID: "install-1", Log: quiet}
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

	now := time.Now()
	nodeID := uuid.NewString()
	tokenHash := sha256.Sum256([]byte(nodeID))
	if err := db.CreateAgentEnrollment(ctx, domain.AgentEnrollment{ID: uuid.NewString(), NodeID: nodeID, NodeName: "edge",
		LocationID: domain.LocalLocationID, Prefix: "rpa_test_n", TokenHash: tokenHash[:],
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
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deps := agentnode.Deps{Runner: idleRunner{}, Addons: harnessAddons{}, Files: files, MaxUpload: 4 << 20,
		InstallID: func() string { return "install-1" }, Log: quiet, TransactionTTL: time.Minute}
	if opt != nil {
		opt(&deps)
	}
	node := agentnode.App(deps)
	agent := agentclient.New(dir, agentclient.Config{Connect: ln.Addr().String(), ServerName: "127.0.0.1", NodeID: nodeID}, node.Handler(), quiet)
	agent.Hello = func() agentproto.Hello { return agentproto.Hello{Protocol: agentproto.Version, Hostname: "edge"} }
	welcomed := make(chan struct{}, 4)
	agent.OnWelcome = func(agentproto.Welcome) { welcomed <- struct{}{} }
	runDone := make(chan error, 1)
	go func() { runDone <- agent.Run(ctx) }()
	select {
	case <-welcomed:
	case <-time.After(15 * time.Second):
		t.Fatal("agent did not connect")
	}
	t.Cleanup(func() {
		cancel()
		<-runDone
		<-hubDone
		files.Close()
		db.Close()
	})
	return &noderoute.Router{LocalNode: domain.LocalNodeID, Hub: hub, Bots: db}, nodeID, files, db
}

func nodeFile(t *testing.T, files *filesystem.Manager, botID, p string) (string, bool) {
	t.Helper()
	w, err := files.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, err := w.Read(p, 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// TestRemoteSingleFileAccess drives the package manager's and AI tools'
// remote file calls through a real hub and agent.
func TestRemoteSingleFileAccess(t *testing.T) {
	r, nodeID, files := remoteNode(t)
	ctx := t.Context()
	bot := uuid.NewString()
	if !r.Online(nodeID) {
		t.Fatal("node not online")
	}

	// Creating requires absence; the node reports the revision it wrote.
	rev1, err := r.WriteFile(ctx, nodeID, bot, "package.json", []byte(`{"name":"a"}`), "", true)
	if err != nil || rev1 == "" {
		t.Fatalf("create: %q %v", rev1, err)
	}
	if _, err := r.WriteFile(ctx, nodeID, bot, "package.json", []byte("x"), "", true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("create over existing: %v", err)
	}
	data, rev, err := r.ReadFileRevision(ctx, nodeID, bot, "package.json", 1<<20)
	if err != nil || string(data) != `{"name":"a"}` || rev != rev1 {
		t.Fatalf("read: %q %q %v (want rev %q)", data, rev, err, rev1)
	}

	// A write based on a stale revision changes nothing.
	time.Sleep(10 * time.Millisecond)
	rev2, err := r.WriteFile(ctx, nodeID, bot, "package.json", []byte(`{"name":"b"}`), rev1, false)
	if err != nil || rev2 == rev1 {
		t.Fatalf("conditional write: %q %v", rev2, err)
	}
	if _, err := r.WriteFile(ctx, nodeID, bot, "package.json", []byte(`{"name":"lost"}`), rev1, false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if got, _ := nodeFile(t, files, bot, "package.json"); got != `{"name":"b"}` {
		t.Fatalf("node file = %q", got)
	}

	// Listing, missing files, size bound and containment.
	es, err := r.ListDir(ctx, nodeID, bot, ".")
	if err != nil || len(es) != 1 || es[0].Name != "package.json" || es[0].IsDir {
		t.Fatalf("list: %+v %v", es, err)
	}
	if _, _, err := r.ReadFileRevision(ctx, nodeID, bot, "missing.txt", 1<<20); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := r.ReadFileRevision(ctx, nodeID, bot, "package.json", 4); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("over max: %v", err)
	}
	for _, bad := range []string{"../escape.txt", "/etc/passwd", "a/../../x"} {
		if _, _, err := r.ReadFileRevision(ctx, nodeID, bot, bad, 1<<20); err == nil {
			t.Fatalf("read %s was allowed", bad)
		}
		if _, err := r.WriteFile(ctx, nodeID, bot, bad, []byte("x"), "", false); err == nil {
			t.Fatalf("write %s was allowed", bad)
		}
	}

	// Offline nodes are refused with a readable message.
	other := uuid.NewString()
	if _, err := r.ListDir(ctx, other, bot, "."); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline list: %v", err)
	}
	if _, err := r.WriteFile(ctx, other, bot, "a", []byte("x"), "", false); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline write: %v", err)
	}
	// The local node is never routed to an agent.
	if _, err := r.ListDir(ctx, domain.LocalNodeID, bot, "."); !errors.Is(err, noderoute.ErrNoRunner) {
		t.Fatalf("local node: %v", err)
	}
}

// TestRemotePatchTransaction checks the AI patch lifecycle on a real agent:
// revision-checked, reversible until the panel commits or rolls back.
func TestRemotePatchTransaction(t *testing.T) {
	r, nodeID, files := remoteNode(t)
	ctx := t.Context()
	bot := uuid.NewString()
	rev, err := r.WriteFile(ctx, nodeID, bot, "index.js", []byte("v1"), "", true)
	if err != nil {
		t.Fatal(err)
	}

	// Rollback restores the previous contents.
	tx, revs, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "index.js", BeforeRevision: rev, After: []byte("v2"), Mode: 0o640}}, 1<<20, 8<<20)
	if err != nil || tx == "" || revs["index.js"] == "" {
		t.Fatalf("begin: %q %v %v", tx, revs, err)
	}
	if got, _ := nodeFile(t, files, bot, "index.js"); got != "v2" {
		t.Fatalf("swapped file = %q", got)
	}
	// Another patch waits for the open transaction.
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "new.js", After: []byte("n")}}, 1<<20, 8<<20); err == nil || errors.Is(err, filesystem.ErrPatchConflict) {
		t.Fatalf("concurrent patch: %v", err)
	}
	if err := r.CompleteTransaction(ctx, nodeID, bot, tx, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := nodeFile(t, files, bot, "index.js"); got != "v1" {
		t.Fatalf("after rollback = %q", got)
	}

	// Commit keeps the change; the reported revision is the file's revision.
	tx, revs, err = r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{
		{Path: "index.js", BeforeRevision: rev, After: []byte("v3")},
		{Path: "src/new.js", After: []byte("n")}}, 1<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CompleteTransaction(ctx, nodeID, bot, tx, true); err != nil {
		t.Fatal(err)
	}
	if err := r.CompleteTransaction(ctx, nodeID, bot, tx, true); err != nil {
		t.Fatalf("repeated commit (lost answer) must succeed: %v", err)
	}
	_, cur, err := r.ReadFileRevision(ctx, nodeID, bot, "index.js", 1<<20)
	if err != nil || cur != revs["index.js"] {
		t.Fatalf("revision %q, patch reported %q (%v)", cur, revs["index.js"], err)
	}
	if got, _ := nodeFile(t, files, bot, "src/new.js"); got != "n" {
		t.Fatalf("new file = %q", got)
	}

	// A stale snapshot, a file that appeared, size limits and escaping paths
	// are refused before anything changes.
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "index.js", BeforeRevision: rev, After: []byte("x")}}, 1<<20, 8<<20); !errors.Is(err, filesystem.ErrPatchConflict) {
		t.Fatalf("stale: %v", err)
	}
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "src/new.js", After: []byte("x")}}, 1<<20, 8<<20); !errors.Is(err, filesystem.ErrPatchConflict) {
		t.Fatalf("appeared: %v", err)
	}
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "big.txt", After: []byte("12345")}}, 4, 8<<20); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "x", After: []byte("x")}}, agentproto.MaxPatchFile+1, 8<<20); err == nil {
		t.Fatal("limits above the node ceiling were accepted")
	}
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "../escape", After: []byte("x")}}, 1<<20, 8<<20); err == nil {
		t.Fatal("escaping patch path accepted")
	}
	if got, _ := nodeFile(t, files, bot, "index.js"); got != "v3" {
		t.Fatalf("refused patches changed the file: %q", got)
	}

	// Delete through a patch, then roll it back.
	_, cur, _ = r.ReadFileRevision(ctx, nodeID, bot, "index.js", 1<<20)
	tx, _, err = r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "index.js", BeforeRevision: cur}}, 1<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeFile(t, files, bot, "index.js"); ok {
		t.Fatal("deleted file still present while swapped")
	}
	if err := r.CompleteTransaction(ctx, nodeID, bot, tx, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := nodeFile(t, files, bot, "index.js"); got != "v3" {
		t.Fatalf("delete rollback = %q", got)
	}

	if _, _, err := r.BeginPatch(ctx, uuid.NewString(), bot, []filesystem.PatchFile{{Path: "a", After: []byte("x")}}, 1<<20, 8<<20); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline patch: %v", err)
	}
}
