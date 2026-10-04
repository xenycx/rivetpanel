package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
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

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/github"
	"github.com/xenycx/rivetpanel/internal/service"
)

func TestAddonsAPI(t *testing.T) {
	e := newEnv(t)
	addonDir := filepath.Join(t.TempDir(), "addons")
	e.bots.AddonData = addons.DataRoot{Dir: addonDir}
	e.bots.Coord = &service.Coordinator{}
	c := e.user("a@x.io", "user")
	other := e.user("b@x.io", "user")

	var kinds struct {
		Addons    []addons.Kind
		Available bool
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/addons", nil), &kinds)
	if !kinds.Available || len(kinds.Addons) != 4 {
		t.Fatalf("%+v", kinds)
	}

	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "go", "addons": []map[string]any{{"kind": "oracle"}}})
	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "go", "addons": []map[string]any{{"kind": "redis"}, {"kind": "redis"}}})
	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "go", "build_command": strings.Repeat("x", 5000)})
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "yag", "runtime": "go",
		"argv": []string{"./app", "-all"}, "build_command": "cd cmd/yagpdb\r\ngo build -o /workspace/app .\n",
		"addons": []map[string]any{{"kind": "postgres"}}, "env": map[string]string{"YAGPDB_PQPASSWORD": "${POSTGRES_PASSWORD}"}}), &b)
	base := "/api/v1/bots/" + b.ID
	if b.BuildCommand != "cd cmd/yagpdb\ngo build -o /workspace/app ." || len(b.Addons) != 1 || b.Addons[0] != "postgres" {
		t.Fatalf("%+v", b)
	}

	// The generated password is a system variable: hidden from the variable
	// list, not deletable there, shown through the add-on.
	var envs struct{ Env []struct{ Name string } }
	json.Unmarshal(c.mustStatus(200, "GET", base+"/env", nil), &envs)
	for _, v := range envs.Env {
		if strings.HasPrefix(v.Name, "RIVET_ADDON_") {
			t.Fatalf("password listed: %+v", envs)
		}
	}
	c.mustStatus(400, "DELETE", base+"/env/"+domain.AddonPasswordVar("postgres"), nil)
	c.mustStatus(400, "PUT", base+"/env", map[string]any{"vars": map[string]string{domain.AddonPasswordVar("postgres"): "x"}})
	var conn struct{ Variables map[string]string }
	json.Unmarshal(c.mustStatus(200, "POST", base+"/addons/postgres/reveal", nil), &conn)
	pwd := conn.Variables["POSTGRES_PASSWORD"]
	if len(pwd) < 24 || conn.Variables["DATABASE_URL"] != "postgres://bot:"+pwd+"@postgres:5432/bot?sslmode=disable" {
		t.Fatalf("%+v", conn)
	}

	// Other people see nothing; duplicates and unknown kinds are refused.
	other.mustStatus(404, "GET", base+"/addons", nil)
	other.mustStatus(404, "POST", base+"/addons", map[string]any{"kind": "redis"})
	c.mustStatus(400, "POST", base+"/addons", map[string]any{"kind": "postgres"})
	c.mustStatus(400, "POST", base+"/addons", map[string]any{"kind": "nope"})
	c.mustStatus(400, "POST", base+"/addons", map[string]any{"kind": "redis", "memory_bytes": 1 << 20})
	c.mustStatus(201, "POST", base+"/addons", map[string]any{"kind": "redis"})
	var list struct{ Addons []service.AddonView }
	json.Unmarshal(c.mustStatus(200, "GET", base+"/addons", nil), &list)
	if len(list.Addons) != 2 || list.Addons[0].Kind != "postgres" || list.Addons[1].Host != "redis" {
		t.Fatalf("%+v", list)
	}

	// Add-on memory counts toward the owner's budget.
	_, mem, err := e.db.OwnerUsage(t.Context(), b.OwnerID)
	if err != nil || mem != b.MemoryBytes+256<<20+128<<20 {
		t.Fatalf("usage %d %v", mem, err)
	}

	// Changes need a stopped bot.
	c.mustStatus(202, "POST", base+"/start", nil)
	c.mustStatus(409, "POST", base+"/addons", map[string]any{"kind": "mongodb"})
	c.mustStatus(409, "DELETE", base+"/addons/redis", nil)
	c.mustStatus(202, "POST", base+"/stop", nil)
	e.db.ExecContext(t.Context(), `UPDATE bots SET observed_state = 'stopped', observed_generation = generation WHERE id = ?`, b.ID)

	// Removing an add-on deletes its data and its password.
	dataDir := filepath.Join(addonDir, b.ID, "postgres")
	os.MkdirAll(dataDir, 0o700)
	c.mustStatus(204, "DELETE", base+"/addons/postgres", nil)
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatal("data kept")
	}
	c.mustStatus(404, "POST", base+"/addons/postgres/reveal", nil)
	if rows, _ := e.db.ListEnv(t.Context(), b.ID); len(rows) != 1 || rows[0].Name != "YAGPDB_PQPASSWORD" {
		t.Fatalf("env rows %+v", rows)
	}
	c.mustStatus(200, "PATCH", base, map[string]any{"build_command": ""})
	var nb botDTO
	json.Unmarshal(c.mustStatus(200, "GET", base, nil), &nb)
	if nb.BuildCommand != "" || len(nb.Addons) != 1 {
		t.Fatalf("%+v", nb)
	}
}

// publicGH serves one public repository and fails any authenticated call,
// proving the panel reads it anonymously.
type publicGH struct {
	mu   sync.Mutex
	srv  *httptest.Server
	sha  string
	auth int
}

func newPublicGH(t *testing.T) *publicGH {
	f := &publicGH{sha: strings.Repeat("b", 40)}
	files := map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"ioredis":"5"}}`,
		"index.js": "process.env.DISCORD_TOKEN", "sub/x.txt": "x"}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			f.auth++
		}
		switch p := r.URL.Path; {
		case p == "/repos/pub/bot":
			w.Write([]byte(`{"full_name":"pub/bot","private":false,"default_branch":"main","description":"A bot","language":"JavaScript","stargazers_count":3}`))
		case p == "/repos/pub/bot/branches":
			w.Write([]byte(`[{"name":"main"},{"name":"dev"}]`))
		case strings.HasPrefix(p, "/repos/pub/bot/commits/"):
			json.NewEncoder(w).Encode(map[string]string{"sha": f.sha})
		case strings.HasPrefix(p, "/repos/pub/bot/tarball/"):
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for n, c := range files {
				tw.WriteHeader(&tar.Header{Name: "pub-bot-x/" + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))})
				tw.Write([]byte(c))
			}
			tw.Close()
			gz.Close()
			w.Write(buf.Bytes())
		case p == "/repos/pub/limited":
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, "rate limited", 403)
		default:
			http.NotFound(w, r)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestPublicRepositoryWithoutGitHubConnection(t *testing.T) {
	e := newEnv(t)
	gh := newPublicGH(t)
	oauthSvc := &service.OAuthService{Store: e.db, Keys: e.bots.Keys}
	dep := &service.DeployService{Bots: e.bots, OAuth: oauthSvc, Files: nil, GH: &github.Client{API: gh.srv.URL}, Keys: e.bots.Keys,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PollInterval: time.Hour}
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	dep.Files = fm
	e.bots.Files = dep.Files
	e.bots.Coord = &service.Coordinator{}
	dep.Ops = &service.Operations{Store: e.db, Bots: e.bots}
	dep.Start(t.Context())
	t.Cleanup(dep.Wait)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: oauthSvc,
		Files: dep.Files, Deploy: dep, Ops: dep.Ops, Catalog: e.bots.Catalog, SecureCookies: true})
	c := e.user("p@x.io", "user")

	var lk service.RepoLookup
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/github/lookup?repo=https://github.com/pub/bot/tree/dev/sub", nil), &lk)
	if lk.Repo.FullName != "pub/bot" || lk.Branch != "dev" || lk.RootDir != "sub" || len(lk.Branches) != 2 || lk.Connected {
		t.Fatalf("%+v", lk)
	}
	c.mustStatus(400, "GET", "/api/v1/github/lookup?repo=pub/missing", nil)
	c.mustStatus(400, "GET", "/api/v1/github/lookup?repo=a/..", nil)
	_, body := c.do("GET", "/api/v1/github/lookup?repo=pub/limited", nil)
	if !strings.Contains(string(body), "rate limit") {
		t.Fatalf("rate limit not explained: %s", body)
	}

	var an service.Analysis
	json.Unmarshal(c.mustStatus(200, "POST", "/api/v1/github/analyze", map[string]any{"repo": "pub/bot", "ai": true}), &an)
	if an.Plan.Runtime != "nodejs" || an.Plan.Source != "detected" || an.AI.Used || len(an.Plan.Addons) != 1 || an.Plan.Addons[0] != "redis" {
		t.Fatalf("%+v", an)
	}

	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "pub", "runtime": "nodejs",
		"github": map[string]any{"full_name": "pub/bot", "branch": "main", "auto_deploy": true, "start_after_deploy": true}}), &b)
	base := "/api/v1/bots/" + b.ID
	var repo map[string]any
	for i := 0; i < 300; i++ {
		var g struct{ Repo map[string]any }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &g)
		if repo = g.Repo; repo["last_sha"] == gh.sha && repo["deploying"] == false {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if repo["last_sha"] != gh.sha || repo["polling"] != true || repo["hook_created"] != false || repo["secret"] != nil {
		t.Fatalf("%+v", repo)
	}
	// The first deployment started the bot.
	var nb botDTO
	json.Unmarshal(c.mustStatus(200, "GET", base, nil), &nb)
	if nb.DesiredState != domain.DesiredRunning {
		t.Fatalf("desired %s", nb.DesiredState)
	}

	// Polling queues a deployment once per new branch head.
	if n := dep.PollOnce(t.Context()); n != 0 {
		t.Fatalf("polled %d without a change", n)
	}
	gh.mu.Lock()
	gh.sha = strings.Repeat("c", 40)
	gh.mu.Unlock()
	if n := dep.PollOnce(t.Context()); n != 1 {
		t.Fatalf("polled %d", n)
	}
	if n := dep.PollOnce(t.Context()); n != 0 {
		t.Fatalf("queued the same head twice (%d)", n)
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.auth != 0 {
		t.Fatalf("%d authenticated calls for a public repository", gh.auth)
	}
}
