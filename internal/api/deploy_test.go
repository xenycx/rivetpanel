package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeGHAPI serves the GitHub REST endpoints the deploy service uses.
type fakeGHAPI struct {
	mu       sync.Mutex
	srv      *httptest.Server
	files    map[string]string
	sha      string
	private  bool
	hooks    map[int64]map[string]string
	deleted  []int64
	nextHook int64
	failHook bool
	// hold, when set, blocks tarball downloads until closed; started receives
	// once per download that reached the handler.
	hold    chan struct{}
	started chan struct{}
}

func newFakeGHAPI(t *testing.T) *fakeGHAPI {
	f := &fakeGHAPI{files: map[string]string{"index.js": "v1", "src/a.js": "a", "bot/main.js": "m"}, sha: strings.Repeat("a", 40),
		hooks: map[int64]map[string]string{}, nextHook: 100}
	mux := http.NewServeMux()
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer gho_plaintexttoken" {
			http.Error(w, "unauthorized", 401)
			return false
		}
		return true
	}
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			json.NewEncoder(w).Encode([]map[string]any{{"full_name": "o/r", "private": true, "default_branch": "main"}})
		}
	})
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			json.NewEncoder(w).Encode(map[string]any{"full_name": "o/r", "private": f.private, "default_branch": "main"})
		}
	})
	mux.HandleFunc("/repos/o/r/branches", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			w.Write([]byte(`[{"name":"main"},{"name":"dev"}]`))
		}
	})
	mux.HandleFunc("/repos/o/r/commits/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/nope") {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"sha": f.sha})
	})
	mux.HandleFunc("/repos/o/r/tarball/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		hold, started := f.hold, f.started
		f.mu.Unlock()
		if started != nil {
			started <- struct{}{}
		}
		if hold != nil {
			<-hold
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "o-r-" + f.sha[:7] + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		for n, c := range f.files {
			tw.WriteHeader(&tar.Header{Name: "o-r-" + f.sha[:7] + "/" + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))})
			tw.Write([]byte(c))
		}
		tw.Close()
		gz.Close()
		w.Write(buf.Bytes())
	})
	mux.HandleFunc("/repos/o/r/hooks", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failHook {
			http.Error(w, "forbidden", 403)
			return
		}
		var in struct{ Config map[string]string }
		json.NewDecoder(r.Body).Decode(&in)
		f.nextHook++
		f.hooks[f.nextHook] = in.Config
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":%d}`, f.nextHook)
	})
	mux.HandleFunc("/repos/o/r/hooks/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var id int64
		fmt.Sscanf(r.URL.Path, "/repos/o/r/hooks/%d", &id)
		f.deleted = append(f.deleted, id)
		delete(f.hooks, id)
		w.WriteHeader(204)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (f *fakeGHAPI) onlyHook() (int64, map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, c := range f.hooks {
		return id, c
	}
	return 0, nil
}

type deployRig struct {
	e    *env
	c    *client
	gh   *fakeGHAPI
	svc  *service.DeployService
	ws   string
	botD string
}

func newDeployRig(t *testing.T) *deployRig {
	e, ghOAuth, oauthSvc := oauthEnv(t, true)
	_ = ghOAuth
	api := newFakeGHAPI(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	dep := &service.DeployService{Bots: e.bots, OAuth: oauthSvc, Files: fm, GH: &github.Client{API: api.srv.URL}, Keys: e.bots.Keys,
		PublicURL: "https://panel.example.com", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := t.Context()
	dep.Start(ctx)
	t.Cleanup(dep.Wait)
	e.bots.Coord = &service.Coordinator{}
	dep.Ops = &service.Operations{Store: e.db, Bots: e.bots}
	e.bots.BeforeDelete = dep.BeforeBotDelete
	e.bots.Files = fm
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: oauthSvc,
		Files: fm, Deploy: dep, Ops: dep.Ops, PublicURL: "https://panel.example.com", SecureCookies: true})

	// Sign up through the (fake) GitHub OAuth flow; the sealed token is then usable for deploys.
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	resp := callback(t, e, state, binder, "good")
	c := &client{e: e, cookie: sessionOf(resp)}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	c.csrf = me.CSRF
	return &deployRig{e: e, c: c, gh: api, svc: dep}
}

func (r *deployRig) waitDeploy(t *testing.T, base string, wantSHA string) map[string]any {
	t.Helper()
	for i := 0; i < 300; i++ {
		var g struct {
			Repo map[string]any
		}
		json.Unmarshal(r.c.mustStatus(200, "GET", base+"/github", nil), &g)
		if g.Repo["deploying"] == false && g.Repo["last_sha"] == wantSHA {
			return g.Repo
		}
		if e, _ := g.Repo["last_error"].(string); e != "" && g.Repo["deploying"] == false {
			t.Fatalf("deploy failed: %s", e)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("deploy did not finish")
	return nil
}

func TestGitHubDeployEndToEnd(t *testing.T) {
	r := newDeployRig(t)
	c := r.c

	// Repository and branch pickers.
	var repos struct{ Repos []github.Repo }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/me/github/repos", nil), &repos)
	var br struct{ Branches []string }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/me/github/branches?repo=o/r", nil), &br)
	if len(repos.Repos) != 1 || len(br.Branches) != 2 {
		t.Fatalf("%+v %+v", repos, br)
	}
	c.mustStatus(400, "GET", "/api/v1/me/github/branches?repo=../../etc", nil)

	// Validation before any bot is created.
	bad := func(g map[string]any) {
		t.Helper()
		c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs", "github": g})
	}
	bad(map[string]any{"full_name": "nope", "branch": "main"})
	bad(map[string]any{"full_name": "o/r", "branch": "--upload-pack=x"})
	bad(map[string]any{"full_name": "o/r", "branch": "main", "root_dir": "../.."})
	bad(map[string]any{"full_name": "o/r", "branch": "nope"}) // branch does not exist
	bad(map[string]any{"full_name": "x/unknown", "branch": "main"})
	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs", "template_id": "discordjs", "github": map[string]any{"full_name": "o/r", "branch": "main"}})
	var n int
	r.e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
	if n != 0 {
		t.Fatalf("failed creations left %d bots behind", n)
	}

	// Create from GitHub with auto-deploy: first deploy runs in the background.
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "gh bot", "runtime": "nodejs",
		"github": map[string]any{"full_name": "o/r", "branch": "main", "root_dir": "", "auto_deploy": true}}), &b)
	base := "/api/v1/bots/" + b.ID
	if b.SourceType != "github" {
		t.Fatalf("source_type = %q", b.SourceType)
	}
	repo := r.waitDeploy(t, base, strings.Repeat("a", 40))
	ws := filepath.Join(r.e.dataDir, b.ID)
	if got, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(got) != "v1" {
		t.Fatalf("index.js = %q", got)
	}
	hookID, cfg := r.gh.onlyHook()
	if hookID == 0 || cfg["url"] != "https://panel.example.com/api/v1/webhooks/github" || len(cfg["secret"]) < 32 || cfg["content_type"] != "json" || repo["hook_created"] != true {
		t.Fatalf("hook: %d %v %v", hookID, cfg, repo)
	}
	if strings.Contains(string(c.mustStatus(200, "GET", base+"/github", nil)), cfg["secret"]) {
		t.Fatal("the webhook secret was returned after creation")
	}

	// Push webhook: only valid signatures deploy; everything else is a uniform 401.
	push := func(secret, event, delivery, ref string) (int, string) {
		body, _ := json.Marshal(map[string]any{"ref": ref, "repository": map[string]any{"full_name": "o/r"}})
		anon := &client{e: r.e}
		resp, out := anon.req("POST", "/api/v1/webhooks/github", nil, func(req *http.Request) {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-GitHub-Event", event)
			req.Header.Set("X-GitHub-Delivery", delivery)
			if secret != "" {
				req.Header.Set("X-Hub-Signature-256", sign(secret, body))
			}
		})
		return resp.StatusCode, string(out)
	}
	if code, _ := push("wrong-secret", "push", "d0", "refs/heads/main"); code != 401 {
		t.Fatalf("bad signature: %d", code)
	}
	if code, _ := push("", "push", "d0", "refs/heads/main"); code != 401 {
		t.Fatalf("missing signature: %d", code)
	}
	if code, out := push(cfg["secret"], "ping", "d1", "refs/heads/main"); code != 202 || !strings.Contains(out, "pong") {
		t.Fatalf("ping: %d %s", code, out)
	}
	if code, out := push(cfg["secret"], "push", "d2", "refs/heads/dev"); code != 202 || !strings.Contains(out, "ignored") {
		t.Fatalf("other branch: %d %s", code, out)
	}
	if code, out := push(cfg["secret"], "issues", "d3", "refs/heads/main"); code != 202 || !strings.Contains(out, "ignored") {
		t.Fatalf("other event: %d %s", code, out)
	}

	// A real push: new commit, a changed file, a removed file, user data kept.
	os.WriteFile(filepath.Join(ws, "user-data.db"), []byte("keep"), 0o644)
	r.gh.mu.Lock()
	r.gh.sha = strings.Repeat("b", 40)
	r.gh.files = map[string]string{"index.js": "v2", "src/b.js": "b"}
	r.gh.mu.Unlock()
	if code, out := push(cfg["secret"], "push", "d4", "refs/heads/main"); code != 202 || !strings.Contains(out, "queued") {
		t.Fatalf("push: %d %s", code, out)
	}
	if code, out := push(cfg["secret"], "push", "d4", "refs/heads/main"); code != 202 || !strings.Contains(out, "ignored") {
		t.Fatalf("redelivery must not deploy twice: %d %s", code, out)
	}
	r.waitDeploy(t, base, strings.Repeat("b", 40))
	if got, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(got) != "v2" {
		t.Fatal("update not applied")
	}
	if _, err := os.Stat(filepath.Join(ws, "src/a.js")); err == nil {
		t.Fatal("file removed upstream survived")
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "user-data.db")); string(got) != "keep" {
		t.Fatal("untracked data was deleted")
	}

	// A running bot is restarted on new code (generation advances, runner notified).
	r.e.db.Exec(`UPDATE bots SET desired_state = 'running', observed_state = 'running', generation = 5, observed_generation = 5 WHERE id = ?`, b.ID)
	before := r.e.rec.count()
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	for i := 0; i < 200 && r.e.rec.count() == before; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	var cur botDTO
	json.Unmarshal(c.mustStatus(200, "GET", base, nil), &cur)
	if r.e.rec.count() == before || cur.Generation != 6 {
		t.Fatalf("running bot not restarted: notifies=%d gen=%d", r.e.rec.count()-before, cur.Generation)
	}
	r.e.db.Exec(`UPDATE bots SET desired_state = 'stopped', observed_state = 'stopped' WHERE id = ?`, b.ID)

	// Manual deploy of a different repo state failing is reported, not hidden.
	r.gh.mu.Lock()
	r.gh.files = map[string]string{}
	r.gh.sha = strings.Repeat("c", 40)
	r.gh.mu.Unlock()
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	var failed map[string]any
	for i := 0; i < 300; i++ {
		var g struct{ Repo map[string]any }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &g)
		if e, _ := g.Repo["last_error"].(string); e != "" && g.Repo["deploying"] == false {
			failed = g.Repo
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if failed == nil || !strings.Contains(failed["last_error"].(string), "empty") {
		t.Fatalf("failure not recorded: %v", failed)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(got) != "v2" {
		t.Fatal("a failed deploy changed the workspace")
	}

	// Other users cannot see or touch it; unlinking removes the GitHub webhook.
	stranger := r.e.user("st@x.io", "user")
	stranger.mustStatus(404, "GET", base+"/github", nil)
	stranger.mustStatus(404, "POST", base+"/github/deploy", nil)
	c.mustStatus(204, "DELETE", base+"/github", nil)
	r.gh.mu.Lock()
	gone := len(r.gh.deleted) == 1 && r.gh.deleted[0] == hookID
	r.gh.mu.Unlock()
	if !gone {
		t.Fatal("webhook not removed from GitHub on unlink")
	}
	if code, _ := push(cfg["secret"], "push", "d9", "refs/heads/main"); code != 401 {
		t.Fatalf("webhook of an unlinked bot must be refused: %d", code)
	}
}

func TestGitHubManualWebhookFallbackAndDeleteCleanup(t *testing.T) {
	r := newDeployRig(t)
	c := r.c
	r.gh.mu.Lock()
	r.gh.failHook = true // e.g. the token lacks webhook rights
	r.gh.mu.Unlock()
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs"}), &b)
	base := "/api/v1/bots/" + b.ID
	var g struct {
		Linked bool
		Repo   repoDTO
	}
	json.Unmarshal(c.mustStatus(200, "PUT", base+"/github", map[string]any{"full_name": "o/r", "branch": "main", "auto_deploy": true}), &g)
	if g.Repo.HookCreated || g.Repo.Secret == "" || g.Repo.WebhookURL == "" {
		t.Fatalf("manual setup values missing: %+v", g.Repo)
	}
	// The user pastes them into GitHub; deliveries then verify against that secret.
	body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"o/r"}}`)
	anon := &client{e: r.e}
	resp, _ := anon.req("POST", "/api/v1/webhooks/github", nil, func(req *http.Request) {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-Hub-Signature-256", sign(g.Repo.Secret, body))
	})
	if resp.StatusCode != 202 {
		t.Fatalf("manual webhook: %d", resp.StatusCode)
	}
	var again struct{ Repo repoDTO }
	json.Unmarshal(c.mustStatus(200, "PUT", base+"/github", map[string]any{"full_name": "o/r", "branch": "main", "auto_deploy": true}), &again)
	if again.Repo.Secret != g.Repo.Secret {
		t.Fatal("the webhook secret must stay stable so a pasted secret keeps working")
	}
	r.svc.Wait()

	// Auto-deploy needs a public URL; an unknown template is rejected.
	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "t", "runtime": "nodejs", "template_id": "nope"})
}

// waitOp polls a bot's newest operation until it is final.
func (r *deployRig) waitOp(t *testing.T, base string) opDTO {
	t.Helper()
	for i := 0; i < 300; i++ {
		var l struct{ Operations []opDTO }
		json.Unmarshal(r.c.mustStatus(200, "GET", base+"/operations", nil), &l)
		if len(l.Operations) > 0 && l.Operations[0].Status != "running" && l.Operations[0].Status != "queued" {
			return l.Operations[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return opDTO{}
}

func TestStaleDeployNeverOverridesNewerIntent(t *testing.T) {
	r := newDeployRig(t)
	c := r.c
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "gh", "runtime": "nodejs",
		"github": map[string]any{"full_name": "o/r", "branch": "main"}}), &b)
	base := "/api/v1/bots/" + b.ID
	r.waitDeploy(t, base, strings.Repeat("a", 40))
	if op := r.waitOp(t, base); op.Status != "succeeded" || op.Kind != "deploy" || op.Trigger != "initial" || deref(op.SourceRef) != strings.Repeat("a", 40) {
		t.Fatalf("initial deploy operation: %+v", op)
	}
	c.mustStatus(202, "POST", base+"/start", nil)

	// 1. Stop while the new commit downloads: the files update, but the bot
	//    must not be restarted by the finished deployment.
	r.gh.mu.Lock()
	r.gh.sha, r.gh.files["index.js"] = strings.Repeat("b", 40), "v2"
	r.gh.hold, r.gh.started = make(chan struct{}), make(chan struct{}, 4)
	hold := r.gh.hold
	r.gh.mu.Unlock()
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	<-r.gh.started
	c.mustStatus(202, "POST", base+"/stop", nil)
	gen := botGen(t, c, base)
	close(hold)
	r.waitDeploy(t, base, strings.Repeat("b", 40))
	if op := r.waitOp(t, base); op.Status != "succeeded" {
		t.Fatalf("second deploy: %+v", op)
	}
	var cur botDTO
	json.Unmarshal(c.mustStatus(200, "GET", base, nil), &cur)
	if cur.DesiredState != "stopped" || cur.Generation != gen {
		t.Fatalf("a deploy that finished after Stop restarted the bot: desired=%s gen=%d want %d", cur.DesiredState, cur.Generation, gen)
	}

	// 2. Unlink while downloading: no file may change afterwards.
	r.gh.mu.Lock()
	r.gh.sha, r.gh.files["index.js"] = strings.Repeat("c", 40), "v3"
	r.gh.hold = make(chan struct{})
	hold = r.gh.hold
	r.gh.mu.Unlock()
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	<-r.gh.started
	done := make(chan struct{})
	go func() { c.mustStatus(204, "DELETE", base+"/github", nil); close(done) }()
	time.Sleep(50 * time.Millisecond)
	close(hold)
	<-done
	op := r.waitOp(t, base)
	if op.Status != "cancelled" {
		t.Fatalf("deploy during unlink: %+v %s", op, deref(op.Message))
	}
	if got, _ := os.ReadFile(filepath.Join(r.e.dataDir, b.ID, "index.js")); string(got) != "v2" {
		t.Fatalf("a superseded deployment changed files: index.js = %q", got)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
