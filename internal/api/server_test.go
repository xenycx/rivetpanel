package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

type fakeDB struct{ err error }

func (f fakeDB) PingContext(context.Context) error { return f.err }

func do(t *testing.T, app *fiber.App, method, path string) (int, string, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

func newApp(dbErr error) *fiber.App {
	ui := fstest.MapFS{
		"index.html":            {Data: []byte("<html>shell</html>")},
		"_app/immutable/app.js": {Data: []byte("x")},
	}
	return New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{dbErr}, UI: ui})
}

func TestHealth(t *testing.T) {
	app := newApp(nil)
	if code, _, body := do(t, app, "GET", "/api/v1/healthz"); code != 200 || !strings.Contains(body, "ok") {
		t.Fatal(code, body)
	}
	if code, _, _ := do(t, app, "GET", "/api/v1/readyz"); code != 200 {
		t.Fatal(code)
	}
}

func TestMetricsDisabledAndBearerProtected(t *testing.T) {
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}})
	if code, _, _ := do(t, app, "GET", "/metrics"); code != 404 {
		t.Fatalf("disabled metrics = %d", code)
	}
	const token = "this-is-a-long-metrics-token"
	app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, MetricsToken: token})
	if code, _, _ := do(t, app, "GET", "/metrics"); code != 401 {
		t.Fatalf("anonymous metrics = %d", code)
	}
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "rivetpanel_database_up 1") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %d %s", resp.StatusCode, body)
	}
}

func TestReadinessReflectsDatabase(t *testing.T) {
	app := newApp(errors.New("boom"))
	code, _, body := do(t, app, "GET", "/api/v1/readyz")
	if code != 503 || strings.Contains(body, "boom") {
		t.Fatal(code, body)
	}
	// liveness is independent of the database
	if code, _, _ := do(t, app, "GET", "/api/v1/healthz"); code != 200 {
		t.Fatal(code)
	}
}

func TestUnknownAPIIsJSON404NotHTML(t *testing.T) {
	app := newApp(nil)
	for _, p := range []string{"/api/v1/nope", "/api/v1/", "/api", "/api/other"} {
		code, ct, body := do(t, app, "GET", p)
		if code != 404 || !strings.HasPrefix(ct, "application/json") || strings.Contains(body, "<html") {
			t.Errorf("%s: %d %s %s", p, code, ct, body)
		}
	}
	if code, _, _ := do(t, app, "POST", "/api/v1/nope"); code != 404 {
		t.Fatal(code)
	}
}

func TestStaticAndFallback(t *testing.T) {
	app := newApp(nil)
	if code, ct, body := do(t, app, "GET", "/"); code != 200 || !strings.HasPrefix(ct, "text/html") || !strings.Contains(body, "shell") {
		t.Fatal(code, ct, body)
	}
	if code, _, body := do(t, app, "GET", "/bots/123"); code != 200 || !strings.Contains(body, "shell") {
		t.Fatal("spa fallback", code, body)
	}
	if code, _, _ := do(t, app, "GET", "/missing.js"); code != 404 {
		t.Fatal("missing asset must 404", code)
	}
	if code, _, _ := do(t, app, "GET", "/_app/immutable/app.js"); code != 200 {
		t.Fatal(code)
	}
	if code, _, _ := do(t, app, "POST", "/"); code != 405 {
		t.Fatal(code)
	}
	if _, _, body := do(t, app, "GET", "/../../etc/passwd"); !strings.Contains(body, "shell") {
		t.Fatal("traversal path must resolve inside the UI FS, got:", body)
	}
}

func TestShellOnlyBuild(t *testing.T) {
	ui := fstest.MapFS{"200.html": {Data: []byte("<html>spa200</html>")}}
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, UI: ui})
	for _, p := range []string{"/", "/bots/1"} {
		if code, _, body := do(t, app, "GET", p); code != 200 || !strings.Contains(body, "spa200") {
			t.Fatal(p, code, body)
		}
	}
}

func TestEmptyUI(t *testing.T) {
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, UI: fstest.MapFS{".gitkeep": {}}})
	if code, _, _ := do(t, app, "GET", "/"); code != 404 {
		t.Fatal(code)
	}
}

func TestReadinessIncludesExtraChecksWithoutLeakingDetail(t *testing.T) {
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{},
		Checks: []Check{{Name: "docker", Fn: func(context.Context) error { return errors.New("dial unix /var/run/docker.sock: denied") }}}})
	code, _, body := do(t, app, "GET", "/api/v1/readyz")
	if code != 503 || !strings.Contains(body, `"docker":"unavailable"`) || !strings.Contains(body, `"database":"ok"`) || strings.Contains(body, "sock") {
		t.Fatal(code, body)
	}
	app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{},
		Checks: []Check{{Name: "docker", Fn: func(context.Context) error { return nil }}}})
	if code, _, body := do(t, app, "GET", "/api/v1/readyz"); code != 200 || !strings.Contains(body, `"docker":"ok"`) {
		t.Fatal(code, body)
	}
}

func TestStaticAssetTypes(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>")},
		"manifest.webmanifest": {Data: []byte("{}")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		"favicon.ico":          {Data: []byte{0, 0, 1, 0}},
	}
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, UI: ui})
	for path, want := range map[string]string{"/manifest.webmanifest": "application/manifest+json", "/favicon.svg": "image/svg+xml", "/favicon.ico": "image/x-icon"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("%s: %v %d %q", path, err, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}
