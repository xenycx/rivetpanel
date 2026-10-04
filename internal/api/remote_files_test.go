package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/pkgmgr"
)

// fakeNodeFiles stands in for noderoute.Router's single-file access to a
// connected rivet-agent: every operation runs against the node's own
// workspaces with the same filesystem rules the agent uses. (The real wire
// path is covered by internal/noderoute's in-process hub+agent tests and by
// TestRemoteAgentNode.)
type fakeNodeFiles struct {
	mu     sync.Mutex
	files  *filesystem.Manager
	online bool
	ops    []string
	// beforeWrite runs between a caller's read and its conditional write.
	beforeWrite func()
	// failCommits makes that many transaction commits fail (a lost answer).
	failCommits int
	// patchErr, when set, makes the next patch fail before anything changes.
	patchErr error
	txs      map[string]fakeTx
}

type fakeTx struct {
	w *filesystem.Workspace
	c *filesystem.Commit
}

func newFakeNodeFiles(t *testing.T) *fakeNodeFiles {
	t.Helper()
	m, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return &fakeNodeFiles{files: m, online: true, txs: map[string]fakeTx{}}
}

func (f *fakeNodeFiles) record(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
	if !f.online {
		return domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return nil
}

func (f *fakeNodeFiles) open(botID string) (*filesystem.Workspace, error) {
	if _, err := f.files.Path(botID); err != nil {
		_ = f.files.Create(botID)
	}
	return f.files.Open(botID)
}

func (f *fakeNodeFiles) Online(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online
}

func (f *fakeNodeFiles) ListDir(_ context.Context, _, botID, dir string) ([]filesystem.Entry, error) {
	if err := f.record("list " + dir); err != nil {
		return nil, err
	}
	w, err := f.open(botID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return w.List(dir)
}

func (f *fakeNodeFiles) ReadFileRevision(_ context.Context, _, botID, p string, max int64) ([]byte, string, error) {
	if err := f.record("read " + p); err != nil {
		return nil, "", err
	}
	w, err := f.open(botID)
	if err != nil {
		return nil, "", err
	}
	defer w.Close()
	b, err := w.Read(p, max)
	if err != nil {
		return nil, "", err
	}
	rev, err := w.Revision(p)
	return b, rev, err
}

func (f *fakeNodeFiles) WriteFile(_ context.Context, _, botID, p string, data []byte, ifMatch string, createOnly bool) (string, error) {
	if err := f.record("write " + p); err != nil {
		return "", err
	}
	if f.beforeWrite != nil {
		f.beforeWrite()
		f.beforeWrite = nil
	}
	w, err := f.open(botID)
	if err != nil {
		return "", err
	}
	defer w.Close()
	cur, err := w.RevisionOrMissing(p)
	if err != nil {
		return "", err
	}
	if (createOnly && cur != "") || (ifMatch != "" && cur != ifMatch) {
		return "", fmt.Errorf("precondition: %w", domain.ErrConflict)
	}
	if err := w.Write(p, bytes.NewReader(data), 1<<20); err != nil {
		return "", err
	}
	return w.Revision(p)
}

func (f *fakeNodeFiles) WriteFileFrom(ctx context.Context, nodeID, botID, p string, src io.Reader, size int64, ifMatch string, createOnly bool) (string, error) {
	data, err := io.ReadAll(io.LimitReader(src, size+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) != size {
		return "", fmt.Errorf("body is %d bytes, declared %d", len(data), size)
	}
	return f.WriteFile(ctx, nodeID, botID, p, data, ifMatch, createOnly)
}

func (f *fakeNodeFiles) BeginPatch(_ context.Context, _, botID string, files []filesystem.PatchFile, maxFile, maxTotal int64) (string, map[string]string, error) {
	if err := f.record("patch"); err != nil {
		return "", nil, err
	}
	f.mu.Lock()
	perr := f.patchErr
	f.patchErr = nil
	f.mu.Unlock()
	if perr != nil {
		return "", nil, perr
	}
	w, err := f.open(botID)
	if err != nil {
		return "", nil, err
	}
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].Path
	}
	c, err := w.ApplyPatch(files, maxFile, maxTotal)
	if err != nil {
		w.Close()
		return "", nil, err
	}
	revs := map[string]string{}
	for _, p := range paths {
		if r, err := w.Revision(p); err == nil {
			revs[p] = r
		}
	}
	id := uuid.NewString()
	f.mu.Lock()
	f.txs[id] = fakeTx{w, c}
	f.mu.Unlock()
	return id, revs, nil
}

func (f *fakeNodeFiles) CreateWorkspace(_ context.Context, _, botID string) error {
	if err := f.record("mkdir"); err != nil {
		return err
	}
	if _, err := f.files.Path(botID); err == nil {
		return nil
	}
	return f.files.Create(botID)
}

func (f *fakeNodeFiles) CompleteTransaction(_ context.Context, _, _, tx string, commit bool) error {
	f.mu.Lock()
	if commit && f.failCommits > 0 {
		f.failCommits--
		f.mu.Unlock()
		return errors.New("agent did not answer")
	}
	t, ok := f.txs[tx]
	delete(f.txs, tx)
	f.ops = append(f.ops, fmt.Sprintf("complete %v", commit))
	f.mu.Unlock()
	if !ok {
		return domain.ErrNotFound
	}
	defer t.w.Close()
	if commit {
		return t.c.Finish()
	}
	return t.c.Rollback()
}

func (f *fakeNodeFiles) write(t *testing.T, botID, p, content string) {
	t.Helper()
	w, err := f.open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(p, strings.NewReader(content), 4<<20); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeNodeFiles) read(t *testing.T, botID, p string) (string, bool) {
	t.Helper()
	w, err := f.open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, err := w.Read(p, 4<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// placeOnRemoteNode moves a bot to an agent node backed by node.
func placeOnRemoteNode(t *testing.T, e *env, botID string, node *fakeNodeFiles) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT OR IGNORE INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Files agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE bots SET node_id = ? WHERE id = ?`, remoteDeployNode, botID); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	e.bots.NodeFiles = node
}

// panelDecoy writes a file to the panel's own disk for a remote bot; it must
// never be read, changed or sent anywhere.
func panelDecoy(t *testing.T, fm *filesystem.Manager, botID, p string) {
	t.Helper()
	if _, err := fm.Path(botID); err != nil {
		_ = fm.Create(botID)
	}
	w, err := fm.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(p, strings.NewReader("PANEL-LOCAL"), 1<<20); err != nil {
		t.Fatal(err)
	}
}

func panelFile(t *testing.T, fm *filesystem.Manager, botID, p string) string {
	t.Helper()
	w, err := fm.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, _ := w.Read(p, 1<<20)
	return string(b)
}

func TestRemotePackageManager(t *testing.T) {
	e := newEnv(t)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"objects":[{"package":{"name":"discord.js","version":"14.16.3","description":"lib"}}]}`))
	}))
	defer reg.Close()
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Files: fm,
		Registry: &pkgmgr.Registry{NPM: reg.URL}, SecureCookies: true})
	owner := e.user("own@x.io", domain.RoleUser)
	viewer := e.user("vw@x.io", domain.RoleUser)
	id := owner.createBot("remote-pkgs") // nodejs
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "vw@x.io", "permissions": domain.PermViewConsole})
	node := newFakeNodeFiles(t)
	placeOnRemoteNode(t, e, id, node)
	panelDecoy(t, fm, id, "package.json")

	type resp struct {
		Supported bool
		File      string
		Exists    bool
		Deps      []pkgmgr.Dependency
	}
	// The node has no manifest yet: the panel's decoy is not consulted.
	var r resp
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/packages", nil), &r)
	if !r.Supported || r.File != "package.json" || r.Exists {
		t.Fatalf("empty remote project: %+v", r)
	}
	viewer.mustStatus(403, "GET", base+"/packages", nil)

	// Adding creates package.json on the node (create-only write).
	json.Unmarshal(owner.mustStatus(200, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{
		{"action": "add", "name": "discord.js", "spec": "^14.0.0"}}}), &r)
	if !r.Exists || len(r.Deps) != 1 {
		t.Fatalf("after add: %+v", r)
	}
	got, ok := node.read(t, id, "package.json")
	if !ok || !strings.Contains(got, `"discord.js": "^14.0.0"`) || !strings.Contains(got, `"private": true`) {
		t.Fatalf("node manifest = %q", got)
	}
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/packages", nil), &r)
	if !r.Exists || len(r.Deps) != 1 || r.Deps[0].Name != "discord.js" {
		t.Fatalf("list from node: %+v", r)
	}

	// An update is written only if the node's file is still what was read.
	json.Unmarshal(owner.mustStatus(200, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{
		{"action": "update", "name": "discord.js", "spec": "^14.16.0"}}}), &r)
	if got, _ := node.read(t, id, "package.json"); !strings.Contains(got, `"^14.16.0"`) {
		t.Fatalf("update not written: %q", got)
	}
	node.beforeWrite = func() {
		time.Sleep(5 * time.Millisecond)
		node.write(t, id, "package.json", "{\n  \"name\": \"edited-elsewhere\"\n}\n")
	}
	out := string(owner.mustStatus(409, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{
		{"action": "add", "name": "zod", "spec": "^3.0.0"}}}))
	if !strings.Contains(out, "changed while it was being edited") {
		t.Fatalf("conflict message: %s", out)
	}
	if got, _ := node.read(t, id, "package.json"); strings.Contains(got, "zod") || !strings.Contains(got, "edited-elsewhere") {
		t.Fatalf("a conflicting edit overwrote the node's file: %q", got)
	}

	// Registry search only needs the ecosystem.
	owner.mustStatus(200, "GET", base+"/packages/search?q=discord", nil)

	// The manifest size bound applies to remote reads too.
	node.write(t, id, "package.json", `{"x":"`+strings.Repeat("a", pkgmgr.MaxManifestBytes)+`"}`)
	owner.mustStatus(413, "GET", base+"/packages", nil)
	node.write(t, id, "package.json", "{}\n")

	// Offline: refused with a readable message; nothing falls back to the panel.
	node.mu.Lock()
	node.online = false
	node.mu.Unlock()
	for _, m := range []string{"GET", "PUT"} {
		out := string(owner.mustStatus(400, m, base+"/packages", map[string]any{"ops": []map[string]string{{"action": "add", "name": "zod", "spec": "^3.0.0"}}}))
		if !strings.Contains(out, "offline") {
			t.Fatalf("offline %s: %s", m, out)
		}
	}
	if got := panelFile(t, fm, id, "package.json"); got != "PANEL-LOCAL" {
		t.Fatalf("the panel's local copy changed: %q", got)
	}
}

func TestAIOperatorRemoteBotFiles(t *testing.T) {
	fp := &fakeProvider{}
	fp.steps = []func(http.ResponseWriter){
		toolStep("c1", "list_files", map[string]any{"path": "."}),
		toolStep("c2", "read_file", map[string]any{"path": "index.js"}),
		toolStep("c3", "search_files", map[string]any{"query": "login"}),
		toolStep("c4", "read_file", map[string]any{"path": ".env"}),
		toolStep("c5", "read_file", map[string]any{"path": "../../etc/passwd"}),
		toolStep("c6", "propose_file_change", map[string]any{"path": "index.js", "content": "login(process.env.DISCORD_TOKEN)\n", "summary": "Use the environment"}),
		textStep("Done."),
	}
	e, _, admin, bot, conv := aiEnv(t, fp)
	node := newFakeNodeFiles(t)
	placeOnRemoteNode(t, e, bot, node)
	fm := e.bots.Workspaces.(*filesystem.Manager)
	panelDecoy(t, fm, bot, "index.js")
	panelDecoy(t, fm, bot, "panel-only.js")
	node.write(t, bot, "index.js", "login('tok-very-secret-value')\n")
	node.write(t, bot, ".env", "DISCORD_TOKEN=tok-very-secret-value\n")

	run := startRun(t, admin, conv, "approval", "fix login")
	waitRun(t, admin, run, "waiting_approval")
	raw, _ := json.Marshal(fp.body(5))
	s := string(raw)
	// Listing, reading and searching come from the node, redacted.
	if strings.Contains(s, "PANEL-LOCAL") || strings.Contains(s, "panel-only.js") {
		t.Fatalf("panel-local files reached the model: %s", s)
	}
	if strings.Contains(s, "tok-very-secret-value") || !strings.Contains(s, "login('[REDACTED]')") {
		t.Fatalf("remote contents not read or not redacted: %s", s)
	}
	if !strings.Contains(s, "index.js:1:") {
		t.Fatalf("remote search found nothing: %s", s)
	}
	if !strings.Contains(s, "Permission is required") {
		t.Fatalf("protected .env read was not refused: %s", s)
	}
	// Containment is enforced by the node's filesystem layer.
	if r := toolResults(fp, 5); len(r) != 5 || !strings.Contains(fmt.Sprint(r[4]["content"]), "invalid path") {
		t.Fatalf("escaping read: %v", r)
	}
	node.mu.Lock()
	for _, op := range node.ops {
		if strings.Contains(op, ".env") {
			t.Fatalf("the panel asked the node for a protected path: %s", op)
		}
	}
	node.mu.Unlock()

	admin.mustStatus(204, "POST", "/api/v1/ai/tool-calls/"+pendingCall(t, e, run)+"/decision", map[string]any{"approve": true})
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	if got, _ := node.read(t, bot, "index.js"); got != "login(process.env.DISCORD_TOKEN)\n" {
		t.Fatalf("node file after apply: %q", got)
	}
	if got := panelFile(t, fm, bot, "index.js"); got != "PANEL-LOCAL" {
		t.Fatalf("the panel's disk was written: %q", got)
	}
	runs := conversationRuns(t, admin, conv)
	if len(runs[0].ChangeSets) != 1 || runs[0].ChangeSets[0].Status != "applied" {
		t.Fatalf("change sets: %+v", runs[0].ChangeSets)
	}
	change := runs[0].ChangeSets[0].ID

	// Undo goes through the node's transaction too.
	admin.mustStatus(204, "POST", "/api/v1/ai/change-sets/"+change+"/revert", nil)
	if got, _ := node.read(t, bot, "index.js"); got != "login('tok-very-secret-value')\n" {
		t.Fatalf("undo on node: %q", got)
	}

	// Offline: file tools are refused; nothing falls back to the panel.
	fp.mu.Lock()
	fp.steps = append(fp.steps, toolStep("c7", "read_file", map[string]any{"path": "index.js"}), textStep("offline"))
	fp.mu.Unlock()
	node.mu.Lock()
	node.online = false
	node.mu.Unlock()
	run2 := startRun(t, admin, conv, "approval", "read it again")
	waitRun(t, admin, run2, "completed", "failed")
	if out := lastToolResult(fp, 8); !strings.Contains(out, "offline") || strings.Contains(out, "PANEL-LOCAL") {
		t.Fatalf("offline read: %q", out)
	}
}

// TestAIOperatorRemoteUnconfirmedCommitRollsBack: when the node never
// confirms the commit, the change is rolled back on the node and recorded as
// not applied.
func TestAIOperatorRemoteUnconfirmedCommitRollsBack(t *testing.T) {
	fp := &fakeProvider{}
	fp.steps = []func(http.ResponseWriter){
		toolStep("c1", "propose_file_change", map[string]any{"path": "index.js", "content": "new()\n", "summary": "x"}),
		textStep("Done."),
	}
	e, _, admin, bot, conv := aiEnv(t, fp)
	node := newFakeNodeFiles(t)
	placeOnRemoteNode(t, e, bot, node)
	node.write(t, bot, "index.js", "old()\n")
	node.failCommits = 2

	run := startRun(t, admin, conv, "approval", "change it")
	waitRun(t, admin, run, "waiting_approval")
	admin.mustStatus(204, "POST", "/api/v1/ai/tool-calls/"+pendingCall(t, e, run)+"/decision", map[string]any{"approve": true})
	waitRun(t, admin, run, "completed", "failed")
	if got, _ := node.read(t, bot, "index.js"); got != "old()\n" {
		t.Fatalf("unconfirmed change kept on the node: %q", got)
	}
	runs := conversationRuns(t, admin, conv)
	if len(runs[0].ChangeSets) != 1 || runs[0].ChangeSets[0].Status != "conflicted" {
		t.Fatalf("change set after rollback: %+v", runs[0].ChangeSets)
	}
	if out := lastToolResult(fp, 1); !strings.Contains(out, "rolled back") {
		t.Fatalf("tool result: %q", out)
	}
}

func pendingCall(t *testing.T, e *env, run string) string {
	t.Helper()
	var id string
	e.db.QueryRow(`SELECT id FROM ai_tool_calls WHERE run_id=? AND approval_state='pending'`, run).Scan(&id)
	if id == "" {
		t.Fatal("no pending approval")
	}
	return id
}
