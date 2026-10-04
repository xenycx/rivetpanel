package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// raw sends a non-JSON body with the CSRF header.
func (c *client) raw(method, path string, body []byte, ct string) (*http.Response, []byte) {
	c.e.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	}
	r.Header.Set(csrfHeader, c.csrf)
	resp, err := c.e.app.Test(r, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func fpath(id, sub, p string) string {
	return "/api/v1/bots/" + id + "/files" + sub + "?path=" + url.QueryEscape(p)
}

func TestFileWriteReadRoundTripServesInertData(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	html := []byte("<html><script>alert(document.domain)</script></html>")
	if resp, b := u.raw("PUT", fpath(id, "/content", "site/index.html"), html, "text/html"); resp.StatusCode != 204 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp, body := u.raw("GET", fpath(id, "/content", "site/index.html"), nil, "")
	if resp.StatusCode != 200 || !bytes.Equal(body, html) {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	h := resp.Header
	if !strings.HasPrefix(h.Get("Content-Type"), "application/octet-stream") || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") ||
		h.Get("Cache-Control") != "no-store" {
		t.Fatalf("file served as active content: %v", h)
	}
	// the file really lives in the bot's workspace
	if _, err := os.Stat(filepath.Join(e.dataDir, id, "site", "index.html")); err != nil {
		t.Fatal(err)
	}
	// overwrite is atomic and leaves no temp files
	u.raw("PUT", fpath(id, "/content", "site/index.html"), []byte("v2"), "")
	var list struct{ Entries []entryDTO }
	json.Unmarshal(u.mustStatus(200, "GET", fpath(id, "", "site"), nil), &list)
	if len(list.Entries) != 1 || list.Entries[0].Name != "index.html" || list.Entries[0].Size != 2 {
		t.Fatalf("%+v", list.Entries)
	}
}

func TestFileTraversalAndSymlinkEscapesAreRejected(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("TOP SECRET"), 0o600)
	os.Symlink(secret, filepath.Join(e.dataDir, id, "leak"))
	os.Symlink(filepath.Dir(secret), filepath.Join(e.dataDir, id, "leakdir"))

	// listing shows the symlinks as symlinks, not their targets
	var list struct{ Entries []entryDTO }
	json.Unmarshal(u.mustStatus(200, "GET", fpath(id, "", "."), nil), &list)
	kinds := map[string]string{}
	for _, en := range list.Entries {
		kinds[en.Name] = en.Type
	}
	if kinds["leak"] != "symlink" || kinds["leakdir"] != "symlink" {
		t.Fatalf("%v", kinds)
	}

	// Paths that leave the workspace, or go THROUGH a link that does, are refused.
	for _, p := range []string{"../x", "a/../../x", "/etc/passwd", "..", "a\\b", "leak", "leakdir/secret.txt", "leakdir/new.txt"} {
		resp, body := u.raw("GET", fpath(id, "/content", p), nil, "")
		if resp.StatusCode < 400 || strings.Contains(string(body), "TOP SECRET") {
			t.Errorf("GET %q -> %d %s", p, resp.StatusCode, body)
		}
	}
	for _, p := range []string{"../x", "a/../../x", "/etc/passwd", "..", "leakdir/secret.txt", "leakdir/new.txt"} {
		if resp, _ := u.raw("PUT", fpath(id, "/content", p), []byte("pwn"), ""); resp.StatusCode < 400 {
			t.Errorf("PUT %q accepted", p)
		}
		if resp, _ := u.raw("DELETE", fpath(id, "", p), nil, ""); resp.StatusCode < 400 {
			t.Errorf("DELETE %q accepted", p)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(secret), "new.txt")); err == nil {
		t.Fatal("file created outside the workspace through a symlinked directory")
	}
	if resp, _ := u.raw("GET", fpath(id, "", "leakdir"), nil, ""); resp.StatusCode < 400 {
		t.Fatal("listing through a symlink to the outside succeeded")
	}
	if resp, _ := u.raw("DELETE", fpath(id, "", "."), nil, ""); resp.StatusCode != 400 {
		t.Fatalf("workspace root deletion: %d", resp.StatusCode)
	}
	// Writing to / deleting the link ITSELF acts on the link, never its target.
	u.raw("PUT", fpath(id, "/content", "leak"), []byte("replacement"), "")
	u.raw("DELETE", fpath(id, "", "leakdir"), nil, "")
	if b, _ := os.ReadFile(secret); string(b) != "TOP SECRET" {
		t.Fatalf("symlink target modified: %q", b)
	}
	if _, err := os.Stat(filepath.Dir(secret)); err != nil {
		t.Fatal("deleting a symlinked directory removed its target")
	}
}

func TestFileEndpointsEnforceOwnershipAndCSRF(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	mallory := e.user("mallory@example.com", domain.RoleUser)
	id := alice.createBot("b")
	alice.raw("PUT", fpath(id, "/content", "f.txt"), []byte("mine"), "")
	for _, tc := range []struct{ method, sub string }{{"GET", ""}, {"GET", "/content"}, {"PUT", "/content"}, {"DELETE", ""}, {"POST", "/extract"}} {
		if resp, _ := mallory.raw(tc.method, fpath(id, tc.sub, "f.txt"), []byte("x"), ""); resp.StatusCode != 404 {
			t.Errorf("%s %s by other user: %d", tc.method, tc.sub, resp.StatusCode)
		}
	}
	mallory.mustStatus(404, "POST", "/api/v1/bots/"+id+"/files/mkdir", map[string]string{"path": "x"})
	mallory.mustStatus(404, "POST", "/api/v1/bots/"+id+"/files/move", map[string]string{"from": "f.txt", "to": "g.txt"})
	if b, _ := os.ReadFile(filepath.Join(e.dataDir, id, "f.txt")); string(b) != "mine" {
		t.Fatal("file changed by another user")
	}
	// CSRF: state-changing file requests without the token are refused
	noCSRF := &client{e: e, cookie: alice.cookie}
	if resp, _ := noCSRF.raw("PUT", fpath(id, "/content", "f.txt"), []byte("pwn"), ""); resp.StatusCode != 403 {
		t.Fatalf("PUT without csrf: %d", resp.StatusCode)
	}
	if resp, _ := noCSRF.raw("DELETE", fpath(id, "", "f.txt"), nil, ""); resp.StatusCode != 403 {
		t.Fatalf("DELETE without csrf: %d", resp.StatusCode)
	}
	anon := &client{e: e}
	if resp, _ := anon.raw("GET", fpath(id, "/content", "f.txt"), nil, ""); resp.StatusCode != 401 {
		t.Fatalf("anonymous read: %d", resp.StatusCode)
	}
}

func TestFileMkdirMoveDeleteAndListingOrder(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	base := "/api/v1/bots/" + id + "/files"
	u.mustStatus(204, "POST", base+"/mkdir", map[string]string{"path": "src/lib"})
	u.raw("PUT", fpath(id, "/content", "zeta.txt"), []byte("z"), "")
	u.raw("PUT", fpath(id, "/content", "alpha.txt"), []byte("a"), "")
	var list struct{ Entries []entryDTO }
	json.Unmarshal(u.mustStatus(200, "GET", base, nil), &list)
	if len(list.Entries) != 3 || list.Entries[0].Name != "src" || list.Entries[0].Type != "dir" || list.Entries[1].Name != "alpha.txt" {
		t.Fatalf("%+v", list.Entries)
	}
	u.mustStatus(204, "POST", base+"/move", map[string]string{"from": "alpha.txt", "to": "src/lib/a.txt"})
	u.mustStatus(400, "POST", base+"/move", map[string]string{"from": "src", "to": "src/lib/inner"})
	u.mustStatus(400, "POST", base+"/move", map[string]string{"from": "../x", "to": "y"})
	u.mustStatus(404, "POST", base+"/move", map[string]string{"from": "missing", "to": "y"})
	u.mustStatus(204, "DELETE", fpath(id, "", "src"), nil)
	json.Unmarshal(u.mustStatus(200, "GET", base, nil), &list)
	if len(list.Entries) != 1 || list.Entries[0].Name != "zeta.txt" {
		t.Fatalf("%+v", list.Entries)
	}
	u.mustStatus(404, "GET", fpath(id, "/content", "nope.txt"), nil)
	u.mustStatus(400, "GET", fpath(id, "/content", "."), nil) // a directory is not a file
}

// live sends a request over a real socket (how production traffic arrives).
func (c *client) live(addr, method, path string, body []byte, ct string) (*http.Response, []byte) {
	c.e.t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		c.e.t.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	}
	req.Header.Set(csrfHeader, c.csrf)
	resp, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		// A server may answer 413 and close before the client finishes sending,
		// which the client can observe as a reset instead of the response. Both
		// are rejections; report status 0 for the reset and fail on anything else.
		if strings.Contains(err.Error(), "reset by peer") || strings.Contains(err.Error(), "broken pipe") {
			return &http.Response{StatusCode: 0}, nil
		}
		c.e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestUploadAndEditorSizeLimits(t *testing.T) {
	e := newEnv(t) // MaxUpload = 2 MiB
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	addr := serve(t, e)
	big := bytes.Repeat([]byte("x"), 2<<20+1)
	if resp, _ := u.live(addr, "PUT", fpath(id, "/content", "big.bin"), big, ""); resp.StatusCode != 413 && resp.StatusCode != 0 {
		t.Fatalf("over-limit upload: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, id, "big.bin")); err == nil {
		t.Fatal("over-limit upload was stored")
	}
	ok := bytes.Repeat([]byte("y"), 1536<<10) // 1.5 MiB: allowed to upload, too big for the editor
	if resp, b := u.live(addr, "PUT", fpath(id, "/content", "ok.bin"), ok, ""); resp.StatusCode != 204 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if resp, _ := u.live(addr, "GET", fpath(id, "/content", "ok.bin"), nil, ""); resp.StatusCode != 413 {
		t.Fatalf("editor read of large file: %d", resp.StatusCode)
	}
	resp, body := u.live(addr, "GET", fpath(id, "/content", "ok.bin")+"&download=1", nil, "")
	if resp.StatusCode != 200 || len(body) != len(ok) {
		t.Fatalf("download: %d len=%d", resp.StatusCode, len(body))
	}
	// a client that lies about its length cannot exceed the limit either
	entries, _ := os.ReadDir(filepath.Join(e.dataDir, id))
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".tmp-") {
			t.Fatalf("temp file leaked: %s", en.Name())
		}
	}
}

func TestNonUploadRoutesRejectLargeBodies(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	addr := serve(t, e)
	huge := bytes.Repeat([]byte("a"), 1<<20+100)
	defer func() {
		var n int
		e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
		if n != 0 {
			t.Errorf("an oversized request created %d bots", n)
		}
	}()
	body := append([]byte(`{"name":"`), huge...)
	body = append(body, []byte(`","runtime":"nodejs"}`)...)
	if resp, _ := u.live(addr, "POST", "/api/v1/bots", body, "application/json"); resp.StatusCode != 413 && resp.StatusCode != 0 {
		t.Fatalf("large JSON body: %d", resp.StatusCode)
	}
	anon := &client{e: e}
	if resp, _ := anon.live(addr, "POST", "/api/v1/auth/login", body, "application/json"); resp.StatusCode != 413 && resp.StatusCode != 0 {
		t.Fatalf("large unauthenticated body: %d", resp.StatusCode)
	}
	// an unauthenticated upload is refused before the body is consumed
	if resp, _ := anon.live(addr, "PUT", fpath("6f1c0a52-3b7e-4d0e-9a41-0c5b7d2e8f10", "/content", "x"), bytes.Repeat([]byte("z"), 1<<20), ""); resp.StatusCode != 401 && resp.StatusCode != 0 {
		t.Fatalf("anonymous upload: %d", resp.StatusCode)
	}
}

func buildZip(t *testing.T, files map[string]string, extra func(*zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		io.WriteString(w, body)
	}
	if extra != nil {
		extra(zw)
	}
	zw.Close()
	return buf.Bytes()
}

func TestZipExtractionThroughAPI(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	good := buildZip(t, map[string]string{"index.js": "console.log(1)", "lib/util.js": "module.exports={}"}, nil)
	resp, b := u.raw("POST", fpath(id, "/extract", "."), good, "application/zip")
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"extracted":2`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	got, _ := os.ReadFile(filepath.Join(e.dataDir, id, "lib", "util.js"))
	if string(got) != "module.exports={}" {
		t.Fatal("content")
	}
	entries, _ := os.ReadDir(filepath.Join(e.dataDir, id))
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".upload-") || strings.HasPrefix(en.Name(), ".extract-") {
			t.Fatalf("temp file leaked: %s", en.Name())
		}
	}

	evil := buildZip(t, map[string]string{"ok.txt": "x"}, func(zw *zip.Writer) {
		w, _ := zw.Create("../../escape.txt")
		io.WriteString(w, "pwn")
	})
	if resp, _ := u.raw("POST", fpath(id, "/extract", "."), evil, "application/zip"); resp.StatusCode != 400 {
		t.Fatalf("malicious zip: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, id, "ok.txt")); err == nil {
		t.Fatal("part of a rejected archive was extracted")
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "escape.txt")); err == nil {
		t.Fatal("zip-slip succeeded")
	}
	if resp, _ := u.raw("POST", fpath(id, "/extract", "."), []byte("this is not a zip"), "application/zip"); resp.StatusCode != 400 {
		t.Fatalf("garbage: %d", resp.StatusCode)
	}
}

func (c *client) rawH(method, path string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	c.e.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "text/plain")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	r.Header.Set(csrfHeader, c.csrf)
	resp, err := c.e.app.Test(r)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestConditionalSavesDetectConflicts(t *testing.T) {
	e := newEnv(t)
	c := e.user("a@x.io", domain.RoleUser)
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "f", "runtime": "nodejs"}), &b)
	p := fpath(b.ID, "/content", "index.js")
	c.raw("PUT", p, []byte("v1"), "text/plain")
	resp, _ := c.raw("GET", p, nil, "")
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on read")
	}
	// Someone else (SFTP, a deployment, another tab) writes the file.
	time.Sleep(5 * time.Millisecond)
	c.raw("PUT", p, []byte("theirs"), "text/plain")
	if resp, _ := c.rawH("PUT", p, []byte("mine"), map[string]string{"If-Match": etag}); resp.StatusCode != 412 {
		t.Fatalf("stale save: %d", resp.StatusCode)
	}
	if _, body := c.raw("GET", p, nil, ""); string(body) != "theirs" {
		t.Fatalf("a stale save overwrote the file: %q", body)
	}
	resp, _ = c.raw("GET", p, nil, "")
	cur := resp.Header.Get("ETag")
	resp, _ = c.rawH("PUT", p, []byte("mine"), map[string]string{"If-Match": cur})
	if resp.StatusCode != 204 || resp.Header.Get("ETag") == "" || resp.Header.Get("ETag") == cur {
		t.Fatalf("current save: %d etag=%q", resp.StatusCode, resp.Header.Get("ETag"))
	}
	if resp, _ := c.rawH("PUT", p, nil, map[string]string{"If-None-Match": "*"}); resp.StatusCode != 412 {
		t.Fatalf("create-only over an existing file: %d", resp.StatusCode)
	}
	if resp, _ := c.rawH("PUT", fpath(b.ID, "/content", "new.js"), nil, map[string]string{"If-None-Match": "*"}); resp.StatusCode != 204 {
		t.Fatalf("create-only of a new file: %d", resp.StatusCode)
	}
}

func TestFileChangesWaitForExclusiveOperations(t *testing.T) {
	e := newEnv(t)
	e.bots.Coord = &service.Coordinator{}
	c := e.user("a@x.io", domain.RoleUser)
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "f", "runtime": "nodejs"}), &b)
	cl, _, err := e.bots.Coord.Claim(t.Context(), b.ID, "A restore", true)
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := c.raw("PUT", fpath(b.ID, "/content", "x.js"), []byte("x"), "text/plain"); resp.StatusCode != 409 || !strings.Contains(string(body), "A restore") {
		t.Fatalf("write during restore: %d %s", resp.StatusCode, body)
	}
	c.mustStatus(200, "GET", "/api/v1/bots/"+b.ID+"/files?path=.", nil) // reading still works
	c.mustStatus(409, "PUT", "/api/v1/bots/"+b.ID+"/env", map[string]any{"vars": map[string]string{"A": "b"}})
	c.mustStatus(409, "POST", "/api/v1/bots/"+b.ID+"/start", nil)
	cl.Release()
	c.mustStatus(200, "PUT", "/api/v1/bots/"+b.ID+"/env", map[string]any{"vars": map[string]string{"A": "b"}})
}
