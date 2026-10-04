package api

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/service"
)

// gitFake is an in-memory GitHub with the repository and Git Data endpoints
// that publishing uses.
type gitFake struct {
	mu          sync.Mutex
	srv         *httptest.Server
	blobs       map[string][]byte
	trees       map[string][]github.TreeEntry
	commits     map[string][2]string // sha -> tree, parent
	refs        map[string]string    // "repo branch" -> commit
	repos       map[string]bool
	blobCreates int
	moveBranch  func() // runs before a ref update (simulates a concurrent push)
}

func hashOf(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		io.WriteString(h, p+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newGitFake(t *testing.T) *gitFake {
	g := &gitFake{blobs: map[string][]byte{}, trees: map[string][]github.TreeEntry{}, commits: map[string][2]string{},
		refs: map[string]string{}, repos: map[string]bool{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gitFake) putTree(entries []github.TreeEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	b, _ := json.Marshal(entries)
	sha := hashOf("tree", string(b))
	g.trees[sha] = entries
	return sha
}

func (g *gitFake) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer gho_plaintexttoken" {
		http.Error(w, "unauthorized", 401)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p := r.URL.Path
	reply := func(code int, v any) { w.WriteHeader(code); json.NewEncoder(w).Encode(v) }
	var in map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&in)
	str := func(k string) string { var s string; json.Unmarshal(in[k], &s); return s }
	switch {
	case p == "/user":
		reply(200, map[string]string{"login": "o"})
	case p == "/user/orgs":
		reply(200, []any{})
	case p == "/user/repos" && r.Method == "POST":
		full := "o/" + str("name")
		if g.repos[full] {
			reply(422, map[string]string{"message": "name already exists"})
			return
		}
		g.repos[full] = true
		readme := hashOf("blob", "readme")
		g.blobs[readme] = []byte("# readme")
		tree := g.putTree([]github.TreeEntry{{Path: "README.md", Mode: "100644", Type: "blob", SHA: readme}})
		c := hashOf("commit", tree)
		g.commits[c] = [2]string{tree, ""}
		g.refs[full+" main"] = c
		reply(201, map[string]any{"full_name": full, "default_branch": "main", "private": true, "html_url": "https://github.com/" + full})
	case strings.HasPrefix(p, "/repos/o/"):
		rest := strings.SplitN(strings.TrimPrefix(p, "/repos/o/"), "/", 2)
		full, sub := "o/"+rest[0], ""
		if len(rest) == 2 {
			sub = rest[1]
		}
		if !g.repos[full] {
			http.NotFound(w, r)
			return
		}
		switch {
		case sub == "":
			reply(200, map[string]any{"full_name": full, "default_branch": "main", "private": true})
		case strings.HasPrefix(sub, "commits/"):
			if c, ok := g.refs[full+" "+strings.TrimPrefix(sub, "commits/")]; ok {
				reply(200, map[string]string{"sha": c})
			} else {
				http.NotFound(w, r)
			}
		case strings.HasPrefix(sub, "git/ref/heads/"):
			if c, ok := g.refs[full+" "+strings.TrimPrefix(sub, "git/ref/heads/")]; ok {
				reply(200, map[string]any{"object": map[string]string{"sha": c}})
			} else {
				http.NotFound(w, r)
			}
		case strings.HasPrefix(sub, "git/commits/") && r.Method == "GET":
			c := g.commits[strings.TrimPrefix(sub, "git/commits/")]
			reply(200, map[string]any{"tree": map[string]string{"sha": c[0]}})
		case strings.HasPrefix(sub, "git/trees/") && r.Method == "GET":
			sha := strings.TrimSuffix(strings.TrimPrefix(sub, "git/trees/"), "?recursive=1")
			reply(200, map[string]any{"tree": g.trees[sha], "truncated": false})
		case sub == "git/blobs":
			b, _ := base64.StdEncoding.DecodeString(str("content"))
			sha := github.BlobSHA(b)
			g.blobs[sha] = b
			g.blobCreates++
			reply(201, map[string]string{"sha": sha})
		case sub == "git/trees":
			var entries []github.TreeEntry
			json.Unmarshal(in["tree"], &entries)
			for _, e := range entries {
				if _, ok := g.blobs[e.SHA]; !ok {
					reply(422, map[string]string{"message": "unknown blob " + e.SHA})
					return
				}
			}
			reply(201, map[string]string{"sha": g.putTree(entries)})
		case sub == "git/commits":
			var parents []string
			json.Unmarshal(in["parents"], &parents)
			parent := ""
			if len(parents) > 0 {
				parent = parents[0]
			}
			c := hashOf("commit", str("tree"), parent, str("message"))
			g.commits[c] = [2]string{str("tree"), parent}
			reply(201, map[string]string{"sha": c})
		case strings.HasPrefix(sub, "git/refs/heads/") && r.Method == "PATCH":
			if g.moveBranch != nil {
				g.moveBranch()
			}
			key := full + " " + strings.TrimPrefix(sub, "git/refs/heads/")
			next := str("sha")
			if g.commits[next][1] != g.refs[key] { // not a fast-forward
				reply(422, map[string]string{"message": "Update is not a fast forward"})
				return
			}
			g.refs[key] = next
			reply(200, map[string]any{})
		case sub == "hooks":
			reply(201, map[string]int{"id": 7})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// files returns path -> content of a branch head.
func (g *gitFake) files(full, branch string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	for _, e := range g.trees[g.commits[g.refs[full+" "+branch]][0]] {
		out[e.Path+"|"+e.Mode] = string(g.blobs[e.SHA])
	}
	return out
}

func TestPublishAndPushToGitHub(t *testing.T) {
	e, _, oauthSvc := oauthEnv(t, true)
	git := newGitFake(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	dep := &service.DeployService{Bots: e.bots, OAuth: oauthSvc, Files: fm, GH: &github.Client{API: git.srv.URL}, Keys: e.bots.Keys,
		PublicURL: "https://panel.example.com", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
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
	id := c.createBot("pub")
	base := "/api/v1/bots/" + id

	w, err := fm.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	for p, content := range map[string]string{"index.js": "console.log(1)", ".env": "TOKEN=secret", ".env.example": "TOKEN=",
		"node_modules/x/index.js": "dep", ".gitignore": "data/\n*.log\n!keep.log\n", "data/db.json": "{}", "src/util.js": "u",
		"debug.log": "noise", "keep.log": "kept", "run.sh": "#!/bin/sh"} {
		if err := w.Write(p, strings.NewReader(content), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := w.OpenWrite("run.sh", 0)
	f.Chmod(0o755)
	f.Close()
	w.Close()

	// Without the repo scope nothing is created.
	c.mustStatus(400, "POST", base+"/github/publish", map[string]any{"name": "pub"})
	e.db.Exec(`UPDATE oauth_accounts SET scopes = 'read:user user:email repo'`)

	var plan struct {
		Files   int
		Ignored int
		Sample  []struct{ Path string }
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/github/push-plan", nil), &plan)
	var paths []string
	for _, s := range plan.Sample {
		paths = append(paths, s.Path)
	}
	want := ".env.example,.gitignore,index.js,keep.log,run.sh,src/util.js"
	if strings.Join(paths, ",") != want {
		t.Fatalf("push plan = %v, want %s", paths, want)
	}

	var pub struct {
		Repo struct {
			FullName string `json:"full_name"`
		}
	}
	json.Unmarshal(c.mustStatus(202, "POST", base+"/github/publish", map[string]any{"name": "pub", "private": true}), &pub)
	if pub.Repo.FullName != "o/pub" {
		t.Fatalf("repo = %+v", pub)
	}
	op := waitPublish(t, c, base, 1)
	if op["status"] != "succeeded" {
		t.Fatalf("publish op = %v", op)
	}
	got := git.files("o/pub", "main")
	if _, readme := got["README.md|100644"]; len(got) != 6 || readme || got["run.sh|100755"] != "#!/bin/sh" || got["index.js|100644"] != "console.log(1)" {
		t.Fatalf("pushed files = %v", got)
	}
	for k := range got {
		if strings.HasPrefix(k, ".env|") || strings.HasPrefix(k, "data/") || strings.HasPrefix(k, "node_modules/") {
			t.Fatalf("pushed an excluded file: %s", k)
		}
	}
	var link struct {
		Linked bool
		Repo   struct {
			FullName string `json:"full_name"`
			LastSHA  string `json:"last_sha"`
		}
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &link)
	if !link.Linked || link.Repo.FullName != "o/pub" || link.Repo.LastSHA != git.refs["o/pub main"] {
		t.Fatalf("link = %+v", link)
	}
	c.mustStatus(400, "POST", base+"/github/publish", map[string]any{"name": "again"})

	// A later push uploads only what changed.
	w, _ = fm.Open(id)
	w.Write("index.js", strings.NewReader("console.log(2)"), 1<<20)
	w.Close()
	before := git.blobCreates
	c.mustStatus(202, "POST", base+"/github/push", map[string]string{"message": "tweak"})
	if op := waitPublish(t, c, base, 2); op["status"] != "succeeded" {
		t.Fatalf("push op = %v", op)
	}
	if git.blobCreates-before != 1 || git.files("o/pub", "main")["index.js|100644"] != "console.log(2)" {
		t.Fatalf("blobs uploaded = %d files = %v", git.blobCreates-before, git.files("o/pub", "main"))
	}

	// Pushing unchanged files makes no commit.
	head := git.refs["o/pub main"]
	c.mustStatus(202, "POST", base+"/github/push", nil)
	op = waitPublish(t, c, base, 3)
	if op["status"] != "succeeded" || !strings.Contains(fmt.Sprint(op["message"]), "nothing to push") || git.refs["o/pub main"] != head {
		t.Fatalf("no-op push = %v", op)
	}

	// A branch that moved meanwhile is never overwritten.
	w, _ = fm.Open(id)
	w.Write("index.js", strings.NewReader("console.log(3)"), 1<<20)
	w.Close()
	git.moveBranch = func() {
		git.refs["o/pub main"] = hashOf("someone else")
		git.moveBranch = nil
	}
	c.mustStatus(202, "POST", base+"/github/push", nil)
	op = waitPublish(t, c, base, 4)
	if op["status"] != "failed" || !strings.Contains(fmt.Sprint(op["message"]), "branch changed") {
		t.Fatalf("conflicting push = %v", op)
	}
}

// waitPublish waits until n publish operations exist and the newest is final.
func waitPublish(t *testing.T, c *client, base string, n int) map[string]any {
	t.Helper()
	for i := 0; i < 400; i++ {
		var ops struct{ Operations []map[string]any }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/operations?kind=publish", nil), &ops)
		if len(ops.Operations) >= n {
			if s := ops.Operations[0]["status"]; s != "queued" && s != "running" {
				return ops.Operations[0]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("publish did not finish")
	return nil
}
