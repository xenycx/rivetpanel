package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakePushNode stands in for a connected rivet-agent's push surface: the
// selection and every read come from the node's own workspaces, using the
// same filesystem rules the agent runs.
type fakePushNode struct {
	mu     sync.Mutex
	files  *filesystem.Manager
	online bool
	reads  []string
	sets   int
}

func (f *fakePushNode) Online(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online
}

func (f *fakePushNode) PushSet(_ context.Context, _ string, botID string, lim filesystem.PushLimits) (filesystem.PushSet, error) {
	if !f.Online("") {
		return filesystem.PushSet{}, domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	f.mu.Lock()
	f.sets++
	f.mu.Unlock()
	w, err := f.files.Open(botID)
	if err != nil {
		return filesystem.PushSet{}, err
	}
	defer w.Close()
	return w.PushSet(lim)
}

func (f *fakePushNode) ReadFile(_ context.Context, _ string, botID, p string, max int64) ([]byte, error) {
	f.mu.Lock()
	f.reads = append(f.reads, p)
	f.mu.Unlock()
	w, err := f.files.Open(botID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return w.Read(p, max)
}

func (f *fakePushNode) write(t *testing.T, botID string, files map[string]string) {
	t.Helper()
	if _, err := f.files.Path(botID); err != nil {
		if err := f.files.Create(botID); err != nil {
			t.Fatal(err)
		}
	}
	w, err := f.files.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for p, content := range files {
		if err := w.Write(p, strings.NewReader(content), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemotePublishAndPushToGitHub(t *testing.T) {
	e, _, oauthSvc := oauthEnv(t, true)
	git := newGitFake(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	nodeFiles, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nodeFiles.Close() })
	node := &fakePushNode{files: nodeFiles, online: true}
	dep := &service.DeployService{Bots: e.bots, OAuth: oauthSvc, Files: fm, GH: &github.Client{API: git.srv.URL}, Keys: e.bots.Keys,
		PublicURL: "https://panel.example.com", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), RemotePush: node}
	dep.Start(t.Context())
	t.Cleanup(dep.Wait)
	e.bots.Coord = &service.Coordinator{}
	dep.Ops = &service.Operations{Store: e.db, Bots: e.bots}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: oauthSvc,
		Files: fm, Deploy: dep, Ops: dep.Ops, PublicURL: "https://panel.example.com", SecureCookies: true})
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	c := &client{e: e, cookie: sessionOf(callback(t, e, state, binder, "good"))}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	c.csrf = me.CSRF
	e.db.Exec(`UPDATE oauth_accounts SET scopes = 'read:user user:email repo'`)

	id := c.createBot("remote-pub")
	base := "/api/v1/bots/" + id
	// A decoy on the panel's disk: a remote server must never be read from here.
	if w, err := fm.Open(id); err == nil {
		w.Write("index.js", strings.NewReader("PANEL-LOCAL"), 1<<20)
		w.Write("panel-only.txt", strings.NewReader("PANEL-LOCAL"), 1<<20)
		w.Close()
	}
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Push agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE bots SET node_id = ? WHERE id = ?`, remoteDeployNode, id); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	node.write(t, id, map[string]string{"index.js": "remote v1", ".env": "TOKEN=secret", ".env.example": "TOKEN=",
		"node_modules/x/index.js": "dep", ".gitignore": "data/\n", "data/db.json": "{}", "src/util.js": "u"})

	// Offline: preview, publish and push are refused and nothing is created.
	node.online = false
	if out := string(c.mustStatus(400, "GET", base+"/github/push-plan", nil)); !strings.Contains(out, "offline") {
		t.Fatalf("offline plan: %s", out)
	}
	if out := string(c.mustStatus(400, "POST", base+"/github/publish", map[string]any{"name": "rpub"})); !strings.Contains(out, "offline") {
		t.Fatalf("offline publish: %s", out)
	}
	if git.repos["o/rpub"] {
		t.Fatal("a repository was created for an offline node")
	}
	node.online = true

	// The plan is the node's selection with the shared exclusions.
	var plan struct {
		Files  int
		Sample []struct{ Path string }
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/github/push-plan", nil), &plan)
	var paths []string
	for _, s := range plan.Sample {
		paths = append(paths, s.Path)
	}
	if got := strings.Join(paths, ","); got != ".env.example,.gitignore,index.js,src/util.js" {
		t.Fatalf("remote plan = %s", got)
	}

	c.mustStatus(202, "POST", base+"/github/publish", map[string]any{"name": "rpub", "private": true})
	if op := waitPublish(t, c, base, 1); op["status"] != "succeeded" {
		t.Fatalf("remote publish op = %v", op)
	}
	got := git.files("o/rpub", "main")
	if len(got) != 4 || got["index.js|100644"] != "remote v1" || got["src/util.js|100644"] != "u" {
		t.Fatalf("pushed files = %v", got)
	}
	for k, v := range got {
		if strings.HasPrefix(k, ".env|") || strings.HasPrefix(k, "data/") || strings.HasPrefix(k, "node_modules/") ||
			strings.HasPrefix(k, "panel-only") || v == "PANEL-LOCAL" {
			t.Fatalf("pushed an excluded or panel-local file: %s", k)
		}
	}
	node.mu.Lock()
	for _, p := range node.reads {
		if p == ".env" || strings.HasPrefix(p, "data/") || strings.HasPrefix(p, "node_modules/") {
			t.Fatalf("the panel read an excluded file from the node: %s", p)
		}
	}
	node.mu.Unlock()
	var link struct {
		Linked bool
		Repo   struct {
			LastSHA string `json:"last_sha"`
		}
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &link)
	if !link.Linked || link.Repo.LastSHA != git.refs["o/rpub main"] {
		t.Fatalf("link = %+v (head %s)", link, git.refs["o/rpub main"])
	}

	// A push to the linked repository sends what changed on the node.
	node.write(t, id, map[string]string{"index.js": "remote v2"})
	before := git.blobCreates
	c.mustStatus(202, "POST", base+"/github/push", map[string]string{"message": "remote tweak"})
	if op := waitPublish(t, c, base, 2); op["status"] != "succeeded" {
		t.Fatalf("remote push op = %v", op)
	}
	if git.blobCreates-before != 1 || git.files("o/rpub", "main")["index.js|100644"] != "remote v2" {
		t.Fatalf("blobs = %d files = %v", git.blobCreates-before, git.files("o/rpub", "main"))
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &link)
	if link.Repo.LastSHA != git.refs["o/rpub main"] {
		t.Fatalf("last_sha %s, head %s", link.Repo.LastSHA, git.refs["o/rpub main"])
	}

	// Offline push is refused before anything is queued.
	node.online = false
	if out := string(c.mustStatus(400, "POST", base+"/github/push", nil)); !strings.Contains(out, "offline") {
		t.Fatalf("offline push: %s", out)
	}
	node.online = true

	// The node enforces the same limits, with a message users can act on.
	dep.PushLimits = filesystem.PushLimits{MaxFiles: 2, MaxBytes: 1 << 20, MaxFile: 1 << 20}
	if out := string(c.mustStatus(400, "GET", base+"/github/push-plan", nil)); !strings.Contains(out, "more than 2 files") {
		t.Fatalf("limited plan: %s", out)
	}
	dep.PushLimits = filesystem.PushLimits{}

	// Remote servers are read from the node only: the panel's decoy never
	// appeared in any commit.
	for sha, b := range git.blobs {
		if string(b) == "PANEL-LOCAL" {
			t.Fatalf("panel-local content was uploaded as blob %s", sha)
		}
	}
	if node.sets < 3 {
		t.Fatalf("node selections = %d", node.sets)
	}
}
