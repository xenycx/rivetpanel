package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/sitehost"
)

// fakeBuilder "builds" by writing output files into the unit's workspace.
type fakeBuilder struct {
	mu    sync.Mutex
	files *filesystem.Manager
	fail  string
	runs  int
}

func (b *fakeBuilder) BuildOnce(_ context.Context, botID string, out io.Writer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runs++
	fmt.Fprintln(out, "vite v5 building for production...")
	if b.fail != "" {
		return &runner.BuildError{Msg: b.fail}
	}
	for name, body := range map[string]string{"dist/index.html": "<h1>built</h1>", "dist/assets/index-B4x9kQ2a.js": "console.log(1)"} {
		if err := b.files.WriteFile(botID, name, strings.NewReader(body), 1<<20); err != nil {
			return err
		}
	}
	return nil
}

func TestBuiltStaticSiteFromGitHub(t *testing.T) {
	r := newDeployRig(t)
	c, e := r.c, r.e
	r.gh.mu.Lock()
	r.gh.files = map[string]string{"package.json": `{"devDependencies":{"vite":"5"},"scripts":{"build":"vite build"}}`, "src/main.js": "x",
		".rivet-data/evil.db": "planted"}
	r.gh.mu.Unlock()
	fm := r.svc.Files
	builder := &fakeBuilder{files: fm}
	sites := &service.SiteService{Store: e.db, Bots: e.bots, Dir: filepath.Join(t.TempDir(), "sites"), BaseURL: "http://sites.test",
		PanelHost: "panel.example.com", MaxBytes: 8 << 20, Deploys: r.svc, Builder: builder, OAuth: r.svc.OAuth}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); sites.Wait() })
	if err := sites.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r.svc.Web = sites
	e.app = New(Deps{Log: r.svc.Log, DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog, OAuth: r.svc.OAuth, Files: fm,
		Deploy: r.svc, Ops: r.svc.Ops, PublicURL: "https://panel.example.com", SecureCookies: true, Nodes: e.db, Sites: sites})
	host := &sitehost.Handler{Sites: sites}

	var info struct {
		Builds bool `json:"builds_enabled"`
		Apps   bool `json:"apps_enabled"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/sites-info", nil), &info)
	if !info.Builds {
		t.Fatalf("info = %+v", info)
	}
	var a struct {
		Plan struct {
			Kind, Framework string
			OutputDir       string `json:"output_dir"`
		}
	}
	json.Unmarshal(c.mustStatus(200, "POST", "/api/v1/sites/analyze", map[string]any{"repo": "o/r"}), &a)
	if a.Plan.Kind != "static_build" || a.Plan.Framework != "vite" || a.Plan.OutputDir != "dist" {
		t.Fatalf("analysis = %+v", a.Plan)
	}
	c.mustStatus(400, "POST", "/api/v1/sites/from-repo", map[string]any{"name": "X", "kind": "vm", "runtime": "nodejs",
		"repo": map[string]any{"full_name": "o/r", "branch": "main"}})
	c.mustStatus(400, "POST", "/api/v1/sites/from-repo", map[string]any{"name": "X", "kind": "static_build", "runtime": "nodejs",
		"output_dir": ".git", "build_command": "x", "repo": map[string]any{"full_name": "o/r", "branch": "main"}})

	var site struct {
		ID    string
		Slug  string
		AppID string `json:"app_id"`
	}
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/sites/from-repo", map[string]any{"name": "Map", "slug": "map", "kind": "static_build",
		"framework": "vite", "runtime": "nodejs", "build_command": "npm run build", "output_dir": "dist", "spa": true,
		"repo": map[string]any{"full_name": "o/r", "branch": "main"}}), &site)
	if site.AppID == "" {
		t.Fatalf("site = %+v", site)
	}
	release := func() (string, map[string]any) {
		var d struct {
			Site struct {
				CurrentRelease *string `json:"current_release"`
			}
			Releases []map[string]any
		}
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/sites/"+site.ID, nil), &d)
		if d.Site.CurrentRelease == nil || len(d.Releases) == 0 {
			return "", nil
		}
		return *d.Site.CurrentRelease, d.Releases[0]
	}
	var cur string
	var rel map[string]any
	for i := 0; i < 300 && cur == ""; i++ {
		time.Sleep(20 * time.Millisecond)
		cur, rel = release()
	}
	if cur == "" {
		t.Fatal("the build was never published")
	}
	if rel["source_ref"] != r.gh.sha || rel["files"] != float64(2) {
		t.Fatalf("release = %v", rel)
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "map.sites.test"
		rec := httptest.NewRecorder()
		host.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/some/route"); rec.Code != 200 || rec.Body.String() != "<h1>built</h1>" {
		t.Fatalf("spa: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/assets/index-B4x9kQ2a.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("hashed asset cache: %v", rec.Header())
	}
	if ok, _ := fm.Exists(site.AppID, ".rivet-data/evil.db"); ok {
		t.Fatal("a repository wrote into the app's data directory")
	}

	// The unit is managed through the site only.
	var bots struct{ Bots []map[string]any }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/bots", nil), &bots)
	if len(bots.Bots) != 0 {
		t.Fatalf("the site's unit is listed as a bot: %v", bots.Bots)
	}
	c.mustStatus(400, "DELETE", "/api/v1/bots/"+site.AppID, nil)
	c.mustStatus(400, "DELETE", "/api/v1/bots/"+site.AppID+"/github", nil)
	var by struct {
		SiteID string `json:"site_id"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/sites/by-app/"+site.AppID, nil), &by)
	if by.SiteID != site.ID {
		t.Fatalf("by-app = %+v", by)
	}

	// A failed build keeps the live release and reports why.
	builder.mu.Lock()
	builder.fail = "build failed (exit code 1): boom"
	builder.mu.Unlock()
	c.mustStatus(202, "POST", "/api/v1/sites/"+site.ID+"/deploy", nil)
	var lastErr string
	for i := 0; i < 300 && lastErr == ""; i++ {
		time.Sleep(20 * time.Millisecond)
		var g struct{ Repo map[string]any }
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/bots/"+site.AppID+"/github", nil), &g)
		lastErr, _ = g.Repo["last_error"].(string)
	}
	if !strings.Contains(lastErr, "boom") {
		t.Fatalf("last error = %q", lastErr)
	}
	if now, _ := release(); now != cur {
		t.Fatal("a failed build replaced the live release")
	}
	if rec := get("/"); rec.Body.String() != "<h1>built</h1>" {
		t.Fatal("the site stopped serving after a failed build")
	}

	c.mustStatus(204, "DELETE", "/api/v1/sites/"+site.ID, nil)
	if b, err := e.db.GetBot(context.Background(), site.AppID); err == nil && b.DesiredState != "deleted" {
		t.Fatalf("the site's unit survived: %+v", b)
	}
	c.mustStatus(404, "GET", "/api/v1/sites/"+site.ID, nil)
}
