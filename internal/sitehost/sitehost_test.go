package sitehost

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/siterules"
)

const testRelease = "11111111-2222-3333-4444-555555555555"

type fakeRouter struct {
	dir   string
	route domain.SiteRoute
}

func (f *fakeRouter) Resolve(string) (domain.SiteRoute, bool)   { return f.route, true }
func (f *fakeRouter) ReleaseRoot(_, _ string) (*os.Root, error) { return os.OpenRoot(f.dir) }
func (f *fakeRouter) PublicPage(context.Context, string) (domain.PublicSitePage, error) {
	return domain.PublicSitePage{}, nil
}
func (f *fakeRouter) TLSAllowed(string) bool { return false }

func site(t *testing.T, files map[string]string, spa bool) *Handler {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := siterules.Publish(root); err != nil {
		t.Fatal(err)
	}
	return &Handler{Sites: &fakeRouter{dir: dir, route: domain.SiteRoute{SiteID: "s", Release: testRelease, SPA: spa, Mode: "files"}}}
}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCacheControl(t *testing.T) {
	cases := map[string]string{
		"index.html":                     "no-cache",
		"_app/immutable/chunks/x.js":     "immutable",
		"assets/index-B4x9kQ2a.js":       "immutable",
		"assets/index-D-xk3_9a.css":      "immutable",
		"assets/my-portfolio.png":        "max-age=3600",
		"assets/photo-20240101.jpg":      "max-age=3600",
		"css/style.css":                  "max-age=3600",
		"sub/assets/logo.a1b2c3d4e5.svg": "immutable",
	}
	for name, want := range cases {
		if got := cacheControl(name, strings.ToLower(filepath.Ext(name))); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestCompression(t *testing.T) {
	css := strings.Repeat("a{color:red}\n", 200)
	h := site(t, map[string]string{"index.html": "hi", "app.css": css, "pre.js": "plain", "pre.js.br": "brotli"}, false)

	w := get(h, "/app.css", "Accept-Encoding", "gzip, br")
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != css {
		t.Error("gzip body mismatch")
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content type %q", ct)
	}

	if w := get(h, "/pre.js", "Accept-Encoding", "gzip, br"); w.Header().Get("Content-Encoding") != "br" || w.Body.String() != "brotli" {
		t.Errorf("brotli sibling: %v %q", w.Header(), w.Body.String())
	}
	if w := get(h, "/pre.js", "Accept-Encoding", "br;q=0"); w.Header().Get("Content-Encoding") != "" || w.Body.String() != "plain" {
		t.Errorf("q=0 must refuse brotli: %v", w.Header())
	}
	if w := get(h, "/app.css", "Accept-Encoding", "gzip", "Range", "bytes=0-3"); w.Header().Get("Content-Encoding") != "" || w.Code != http.StatusPartialContent {
		t.Errorf("range requests are served uncompressed: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/.rivet/z/app.css.gz"); w.Code != http.StatusNotFound {
		t.Errorf("generated files must not be served directly: %d", w.Code)
	}
}

func TestRedirectsAndHeaders(t *testing.T) {
	h := site(t, map[string]string{
		"index.html": "home",
		"about.html": "about",
		"404.html":   "custom 404",
		"_redirects": "/old /about.html 301\n/blog/:slug /posts/:slug 302\n/app/* /index.html 200\n/about.html /index.html 302\n/gone /404.html 410\n",
		"_headers":   "/*\n  X-Frame-Options: DENY\n/about.html\n  Cache-Control: public, max-age=5\n",
	}, false)

	if w := get(h, "/old?x=1"); w.Code != 301 || w.Header().Get("Location") != "/about.html?x=1" {
		t.Errorf("redirect: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/blog/hi"); w.Code != 302 || w.Header().Get("Location") != "/posts/hi" {
		t.Errorf("placeholder: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/app/x/y"); w.Code != 200 || w.Body.String() != "home" {
		t.Errorf("rewrite: %d %q", w.Code, w.Body.String())
	}
	if w := get(h, "/about.html"); w.Code != 200 || w.Body.String() != "about" || w.Header().Get("Cache-Control") != "public, max-age=5" {
		t.Errorf("existing files shadow unforced rules and _headers apply: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w := get(h, "/gone"); w.Code != http.StatusGone || w.Body.String() != "custom 404" {
		t.Errorf("410: %d %q", w.Code, w.Body.String())
	}
	if w := get(h, "/nope"); w.Code != 404 || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers on 404: %d %v", w.Code, w.Header())
	}
	for _, p := range []string{"/_redirects", "/_headers", "/.rivet/rules.json"} {
		if w := get(h, p); w.Code != 404 {
			t.Errorf("%s served: %d", p, w.Code)
		}
	}
}

func TestSPAAndDirectories(t *testing.T) {
	h := site(t, map[string]string{"index.html": "spa", "docs/index.html": "docs"}, true)
	if w := get(h, "/some/route"); w.Code != 200 || w.Body.String() != "spa" {
		t.Errorf("spa fallback: %d %q", w.Code, w.Body.String())
	}
	if w := get(h, "/docs"); w.Code != 301 || w.Header().Get("Location") != "/docs/" {
		t.Errorf("dir redirect: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/missing.js"); w.Code != 404 {
		t.Errorf("assets don't fall back to the SPA: %d", w.Code)
	}
}
