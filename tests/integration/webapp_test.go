//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/sitehost"
)

// A static site's build runs once in a builder and leaves its output in the
// unit's workspace; nothing is started.
func TestWebStaticBuild(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs", func(in *service.CreateBotInput) {
		in.Kind = domain.KindWeb
		in.BuildCommand = "mkdir -p dist\necho \"<h1>$VITE_TITLE</h1>\" > dist/index.html\necho \"secret=$API_KEY\" > dist/leak.txt"
		in.Env = map[string]string{"VITE_TITLE": "built", "API_KEY": "s3cret"}
	})
	var out bytes.Buffer
	if err := s.rn.BuildOnce(s.ctx, b.ID, &out); err != nil {
		t.Fatalf("build: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(filepath.Join(s.dir, "bots", b.ID, "dist", "index.html"))
	if err != nil || strings.TrimSpace(string(got)) != "<h1>built</h1>" {
		t.Fatalf("output = %q %v", got, err)
	}
	if leak, _ := os.ReadFile(filepath.Join(s.dir, "bots", b.ID, "dist", "leak.txt")); strings.TrimSpace(string(leak)) != "secret=" {
		t.Fatalf("a secret reached the builder: %q", leak)
	}
	if s.get(b.ID).ObservedState != "stopped" || len(s.containers(b.ID)) != 0 {
		t.Fatal("a static build must leave nothing running")
	}

	// A failing build reports its output.
	s.db.ExecContext(s.ctx, `UPDATE bots SET build_command = 'echo nope >&2; exit 3' WHERE id = ?`, b.ID)
	err = s.rn.BuildOnce(s.ctx, b.ID, io.Discard)
	var be *runner.BuildError
	if !asBuildError(err, &be) || !strings.Contains(be.Msg, "exit code 3") || !strings.Contains(be.Msg, "nope") {
		t.Fatalf("failed build = %v", err)
	}
}

func asBuildError(err error, out **runner.BuildError) bool {
	be, ok := err.(*runner.BuildError)
	*out = be
	return ok
}

// An app runs on the apps network, keeps /data across generations, runs its
// release command first, and is reachable through the sites listener proxy.
func TestWebAppThroughProxy(t *testing.T) {
	apps := "rivetpanel-it-" + uuid.NewString()[:8]
	s := newStack(t, func(o *runner.Options) { o.AppsNetwork = apps })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.removeAllContainers()
		_ = s.sdk.NetworkRemove(ctx, apps)
	})
	b := s.createBot("nodejs", func(in *service.CreateBotInput) {
		in.Kind = domain.KindWeb
		in.Argv = []string{"node", "server.js"}
		in.ReleaseCommand = "echo migrated >> /data/migrations.log"
		in.Env = map[string]string{"PORT": "3000", "GREETING": "hello"}
	})
	s.write(b, "server.js", `
const http = require('http'), fs = require('fs');
const f = process.env.DATA_DIR + '/count';
http.createServer((req, res) => {
  let n = 0; try { n = parseInt(fs.readFileSync(f, 'utf8')) || 0; } catch (e) {}
  fs.writeFileSync(f, String(++n));
  const migrations = fs.readFileSync('/data/migrations.log', 'utf8').trim().split('\n').length;
  res.setHeader('Content-Type', 'text/plain');
  res.end(process.env.GREETING + ' ' + n + ' host=' + req.headers.host + ' proto=' + req.headers['x-forwarded-proto'] + ' migrations=' + migrations);
}).listen(process.env.PORT, process.env.HOST);
`)
	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "running", 3*time.Minute)

	nw, err := s.sdk.NetworkInspect(s.ctx, apps, network.InspectOptions{})
	if err != nil || nw.Internal {
		t.Fatalf("apps network = %+v %v", nw, err)
	}
	h := &sitehost.Handler{
		Sites: routeTo(domain.SiteRoute{SiteID: "s", Mode: domain.SiteModeApp, AppID: b.ID, ContainerID: *b.ContainerID, AppState: "running", AppPort: 3000}),
		Proxy: &sitehost.Proxy{Apps: s.dk, Network: apps},
	}
	front := httptest.NewServer(h)
	defer front.Close()
	get := func() string {
		t.Helper()
		var body string
		s.waitFor("app answers through the proxy", 30*time.Second, func() bool {
			req, _ := http.NewRequest("GET", front.URL+"/", nil)
			req.Host = "app.example.test"
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return false
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			body = string(b)
			return res.StatusCode == 200
		})
		return body
	}
	if got := get(); got != "hello 1 host=app.example.test proto=http migrations=1" {
		t.Fatalf("first response = %q", got)
	}

	// A restart is a new container (new address) with the same /data; the
	// release command runs again for the new generation.
	if _, err := s.bots.Restart(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("new container", 2*time.Minute, func() bool {
		n := s.get(b.ID)
		return n.ObservedState == "running" && n.ContainerID != nil && *n.ContainerID != *b.ContainerID
	})
	nb := s.get(b.ID)
	h.Sites = routeTo(domain.SiteRoute{SiteID: "s", Mode: domain.SiteModeApp, AppID: b.ID, ContainerID: *nb.ContainerID, AppState: "running", AppPort: 3000})
	if got := get(); got != "hello 2 host=app.example.test proto=http migrations=2" {
		t.Fatalf("after restart = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "bots", b.ID, ".rivet-data", "count")); err != nil {
		t.Fatalf("data is not kept in the workspace: %v", err)
	}
	ip, err := s.dk.ContainerIP(s.ctx, *nb.ContainerID, apps)
	if err != nil || net.ParseIP(ip) == nil {
		t.Fatalf("container ip = %q %v", ip, err)
	}
}

type staticRoute struct{ r domain.SiteRoute }

func routeTo(r domain.SiteRoute) *staticRoute                    { return &staticRoute{r} }
func (s *staticRoute) Resolve(string) (domain.SiteRoute, bool)   { return s.r, true }
func (s *staticRoute) ReleaseRoot(_, _ string) (*os.Root, error) { return nil, os.ErrNotExist }
func (s *staticRoute) TLSAllowed(string) bool                    { return false }
func (s *staticRoute) PublicPage(context.Context, string) (domain.PublicSitePage, error) {
	return domain.PublicSitePage{}, nil
}

// PHP apps run FrankenPHP from a capability-free copy made by the build, so
// they start under no-new-privileges as an unprivileged user.
func TestWebPHPApp(t *testing.T) {
	apps := "rivetpanel-it-" + uuid.NewString()[:8]
	s := newStack(t, func(o *runner.Options) { o.AppsNetwork = apps })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.removeAllContainers()
		_ = s.sdk.NetworkRemove(ctx, apps)
	})
	b := s.createBot("php", func(in *service.CreateBotInput) {
		in.Kind = domain.KindWeb
		in.Env = map[string]string{"PORT": "8080"}
	})
	s.write(b, "index.php", `<?php file_put_contents(getenv('DATA_DIR').'/hits', 'x', FILE_APPEND); echo 'php '.PHP_MAJOR_VERSION.' hits='.strlen(file_get_contents('/data/hits'));`)
	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "running", 5*time.Minute)
	h := &sitehost.Handler{
		Sites: routeTo(domain.SiteRoute{SiteID: "s", Mode: domain.SiteModeApp, AppID: b.ID, ContainerID: *b.ContainerID, AppState: "running", AppPort: 8080}),
		Proxy: &sitehost.Proxy{Apps: s.dk, Network: apps},
	}
	front := httptest.NewServer(h)
	defer front.Close()
	var body string
	s.waitFor("php answers", time.Minute, func() bool {
		res, err := http.Get(front.URL + "/")
		if err != nil {
			return false
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		body = string(raw)
		return res.StatusCode == 200
	})
	if body != "php 8 hits=1" {
		t.Fatalf("php = %q (logs: %s)", body, s.logs(*b.ContainerID))
	}
}
