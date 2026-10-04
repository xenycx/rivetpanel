package api

import (
	"bytes"
	"context"
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

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) Notify(id string) { r.mu.Lock(); r.calls = append(r.calls, id); r.mu.Unlock() }
func (r *recorder) count() int       { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

type env struct {
	src     *wsSource
	bus     *events.Bus
	rec     *recorder
	t       *testing.T
	app     *fiber.App
	db      *sqlite.DB
	auth    *service.AuthService
	bots    *service.BotService
	dataDir string
	// consoleLimit counts live console sessions (serve waits for them).
	consoleLimit *console.Limiter
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	keys, err := secrets.LoadDir(filepath.Join(dir, "keys"), "k1", true)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "bots")
	wsm, err := filesystem.NewManager(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wsm.Close() })
	as := &service.AuthService{Store: db, Hasher: auth.NewHasher(2), TTL: 3600e9}
	rec := &recorder{}
	bs := &service.BotService{Notifier: rec, Store: db, Catalog: cat, Keys: keys, Workspaces: wsm, LocalNode: domain.LocalNodeID,
		Limits: service.Limits{MinMemoryBytes: 32 << 20, MaxMemoryBytes: 1 << 30, MinNanoCPUs: 50_000_000, MaxNanoCPUs: 2e9}}
	src, bus := newWSSource(), events.NewBus()
	bs.Bus = bus
	csvc := &console.Service{Src: src, Bus: bus, Opts: console.Options{AccessRecheck: 50 * time.Millisecond, StatusPoll: 20 * time.Millisecond}}
	limit := console.NewLimiter(0, 0, 2)
	app := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: db, Auth: as, Bots: bs, Catalog: cat, SecureCookies: true,
		Console: csvc, ConsoleLimit: limit, Nodes: db, Files: wsm, MaxUpload: 2 << 20,
		Clients: &service.APIClientService{Store: db, Bots: bs}})
	return &env{src, bus, rec, t, app, db, as, bs, data, limit}
}

type client struct {
	e      *env
	cookie string
	csrf   string
}

const pw = "correct-horse-battery"

func (e *env) user(email, role string) *client {
	e.t.Helper()
	if _, err := e.auth.CreateUser(context.Background(), email, pw, role); err != nil {
		e.t.Fatal(err)
	}
	c := &client{e: e}
	resp, body := c.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": pw})
	if resp.StatusCode != 200 {
		e.t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck.Value
		}
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(body, &out)
	c.csrf = out.CSRF
	return c
}

func (c *client) req(method, path string, body any, mods ...func(*http.Request)) (*http.Response, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	}
	for _, m := range mods {
		m(r)
	}
	resp, err := c.e.app.Test(r)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// do sends a request with the CSRF header attached.
func (c *client) do(method, path string, body any) (*http.Response, []byte) {
	return c.req(method, path, body, func(r *http.Request) {
		if c.csrf != "" {
			r.Header.Set(csrfHeader, c.csrf)
		}
	})
}

func (c *client) mustStatus(want int, method, path string, body any) []byte {
	c.e.t.Helper()
	resp, b := c.do(method, path, body)
	if resp.StatusCode != want {
		c.e.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, b)
	}
	return b
}

func (c *client) createBot(name string) string {
	c.e.t.Helper()
	var out struct{ ID string }
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": name, "runtime": "nodejs"}), &out)
	return out.ID
}

func TestLoginLogoutAndSessionCookie(t *testing.T) {
	e := newEnv(t)
	u := e.user("Alice@Example.com", domain.RoleUser)
	// email is normalized; login with different case works
	resp, _ := u.do("POST", "/api/v1/auth/login", map[string]string{"email": "ALICE@example.com", "password": pw})
	if resp.StatusCode != 200 {
		t.Fatal("case-insensitive login failed", resp.StatusCode)
	}
	var ck *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Value == "" {
		t.Fatalf("cookie flags: %+v", ck)
	}
	// only the hash is stored
	var n int
	e.db.QueryRow(`SELECT count(*) FROM sessions WHERE token_hash = ?`, []byte(ck.Value)).Scan(&n)
	if n != 0 {
		t.Fatal("raw token stored")
	}
	u.mustStatus(200, "GET", "/api/v1/auth/me", nil)
	u.mustStatus(204, "POST", "/api/v1/auth/logout", nil)
	if resp, _ := u.do("GET", "/api/v1/auth/me", nil); resp.StatusCode != 401 {
		t.Fatalf("session usable after logout: %d", resp.StatusCode)
	}
}

func TestBadCredentialsAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	e.user("a@example.com", domain.RoleUser)
	anon := &client{e: e}
	r1, b1 := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "wrong-password-1"})
	r2, b2 := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": "wrong-password-1"})
	if r1.StatusCode != 401 || r2.StatusCode != 401 || string(b1) != string(b2) {
		t.Fatalf("%d %s / %d %s", r1.StatusCode, b1, r2.StatusCode, b2)
	}
}

func TestDisabledUserRevokedAndCannotLogin(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	bob := e.user("bob@example.com", domain.RoleUser)
	var users struct{ Users []userDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/users", nil), &users)
	var bobID string
	for _, u := range users.Users {
		if u.Email == "bob@example.com" {
			bobID = u.ID
		}
	}
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+bobID, map[string]bool{"disabled": true})
	if resp, _ := bob.do("GET", "/api/v1/auth/me", nil); resp.StatusCode != 401 {
		t.Fatal("disabled user's session still valid")
	}
	anon := &client{e: e}
	if resp, _ := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "bob@example.com", "password": pw}); resp.StatusCode != 401 {
		t.Fatal("disabled user can log in")
	}
	// admin cannot disable self; non-admin cannot manage users
	admin.mustStatus(400, "PATCH", "/api/v1/users/"+users.Users[0].ID, map[string]bool{"disabled": true})
	carol := e.user("carol@example.com", domain.RoleUser)
	carol.mustStatus(403, "GET", "/api/v1/users", nil)
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	body := map[string]any{"name": "x", "runtime": "nodejs"}
	if resp, _ := u.req("POST", "/api/v1/bots", body); resp.StatusCode != 403 {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}
	if resp, _ := u.req("POST", "/api/v1/bots", body, func(r *http.Request) { r.Header.Set(csrfHeader, "forged") }); resp.StatusCode != 403 {
		t.Fatalf("forged csrf: %d", resp.StatusCode)
	}
	if resp, _ := u.req("POST", "/api/v1/bots", body, func(r *http.Request) {
		r.Header.Set(csrfHeader, u.csrf)
		r.Header.Set("Origin", "https://evil.example")
	}); resp.StatusCode != 403 {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}
	if resp, _ := u.req("POST", "/api/v1/bots", body, func(r *http.Request) {
		r.Header.Set(csrfHeader, u.csrf)
		r.Header.Set("Origin", "http://"+r.Host)
	}); resp.StatusCode != 201 {
		t.Fatalf("same origin rejected: %d", resp.StatusCode)
	}
	// another session's CSRF token does not work
	other := e.user("b@example.com", domain.RoleUser)
	if resp, _ := u.req("POST", "/api/v1/bots", body, func(r *http.Request) { r.Header.Set(csrfHeader, other.csrf) }); resp.StatusCode != 403 {
		t.Fatal("cross-session csrf accepted")
	}
	// GETs need no CSRF; unauthenticated requests are rejected
	u.req("GET", "/api/v1/bots", nil)
	anon := &client{e: e}
	if resp, _ := anon.do("GET", "/api/v1/bots", nil); resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	anon := &client{e: e}
	var last int
	for i := 0; i < 12; i++ {
		resp, _ := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "x@example.com", "password": "nope-nope-nope"})
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("last status %d", last)
	}
}

func TestCrossUserAccessRejected(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	mallory := e.user("mallory@example.com", domain.RoleUser)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	id := alice.createBot("alice-bot")
	p := "/api/v1/bots/" + id

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", p, nil},
		{"PATCH", p, map[string]any{"name": "pwn"}},
		{"DELETE", p, nil},
		{"GET", p + "/env", nil},
		{"PUT", p + "/env", map[string]any{"vars": map[string]string{"A": "b"}}},
		{"DELETE", p + "/env/A", nil},
	} {
		if resp, _ := mallory.do(tc.method, tc.path, tc.body); resp.StatusCode != 404 {
			t.Errorf("%s %s by other user: %d (want 404)", tc.method, tc.path, resp.StatusCode)
		}
	}
	var list struct{ Bots []botDTO }
	json.Unmarshal(mallory.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 0 {
		t.Fatal("bot list leaks other users' bots")
	}
	// unmodified
	var got botDTO
	json.Unmarshal(alice.mustStatus(200, "GET", p, nil), &got)
	if got.Name != "alice-bot" || got.Generation != 0 {
		t.Fatalf("bot modified: %+v", got)
	}
	// admin sees and can manage it
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 1 {
		t.Fatal("admin cannot list all bots")
	}
	admin.mustStatus(200, "GET", p, nil)
	// malformed ids are plain 404s
	alice.mustStatus(404, "GET", "/api/v1/bots/not-a-uuid", nil)
	alice.mustStatus(404, "GET", "/api/v1/bots/..%2f..%2fetc", nil)
}

func TestBotValidation(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	bad := map[string]map[string]any{
		"empty name":      {"name": "  ", "runtime": "nodejs"},
		"long name":       {"name": strings.Repeat("x", 65), "runtime": "nodejs"},
		"unknown runtime": {"name": "b", "runtime": "php"},
		"shell argv0":     {"name": "b", "runtime": "nodejs", "argv": []string{"sh", "-c", "id"}},
		"abs argv0 go":    {"name": "b", "runtime": "go", "argv": []string{"/bin/sh"}},
		"traversal go":    {"name": "b", "runtime": "go", "argv": []string{"../../bin/sh"}},
		"empty argv":      {"name": "b", "runtime": "nodejs", "argv": []string{}},
		"nul in argv":     {"name": "b", "runtime": "nodejs", "argv": []string{"node", "a\u0000b"}},
		"long arg":        {"name": "b", "runtime": "nodejs", "argv": []string{"node", strings.Repeat("a", 1025)}},
		"too many args":   {"name": "b", "runtime": "nodejs", "argv": append([]string{"node"}, make([]string, 40)...)},
		"mem too small":   {"name": "b", "runtime": "nodejs", "memory_bytes": 1024},
		"mem too big":     {"name": "b", "runtime": "nodejs", "memory_bytes": 1 << 40},
		"java heap floor": {"name": "b", "runtime": "java", "memory_bytes": 64 << 20},
		"cpu too big":     {"name": "b", "runtime": "nodejs", "nano_cpus": 64e9},
		"negative cpu":    {"name": "b", "runtime": "nodejs", "nano_cpus": -1},
		"pids too big":    {"name": "b", "runtime": "nodejs", "pids_limit": 100000},
	}
	for name, body := range bad {
		if resp, b := u.do("POST", "/api/v1/bots", body); resp.StatusCode != 400 {
			t.Errorf("%s: got %d %s", name, resp.StatusCode, b)
		}
	}
	// unknown JSON field (e.g. an attempt to smuggle an image or mount)
	u.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs", "image_ref": "evil:latest"})
	u.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs", "mounts": []string{"/:/host"}})
	// non-JSON content type
	if resp, _ := u.req("POST", "/api/v1/bots", nil, func(r *http.Request) { r.Header.Set(csrfHeader, u.csrf) }); resp.StatusCode == 201 {
		t.Fatal("empty body created a bot")
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bots created by invalid requests", n)
	}
	// valid creation uses catalog defaults, catalog image, and creates a workspace
	var b botDTO
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": " ok ", "runtime": "go"}), &b)
	if b.Name != "ok" || b.ImageRef != "alpine:3.23" || b.Argv[0] != "./app" || b.DesiredState != "stopped" || b.NodeID != domain.LocalNodeID {
		t.Fatalf("%+v", b)
	}
	if st, err := os.Stat(filepath.Join(e.dataDir, b.ID)); err != nil || !st.IsDir() {
		t.Fatal("workspace not created")
	}
}

func TestUpdateBumpsGenerationAndRequiresStopped(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	var b botDTO
	json.Unmarshal(u.mustStatus(200, "PATCH", "/api/v1/bots/"+id, map[string]any{"name": "renamed", "memory_bytes": 128 << 20}), &b)
	if b.Name != "renamed" || b.MemoryBytes != 128<<20 || b.Generation != 1 {
		t.Fatalf("%+v", b)
	}
	u.mustStatus(400, "PATCH", "/api/v1/bots/"+id, map[string]any{"argv": []string{"bash"}})
	u.mustStatus(400, "PATCH", "/api/v1/bots/"+id, map[string]any{"runtime": "cobol"})
	// Selecting another runtime switches the image and resets the command to its
	// default, since the old one rarely fits (the stopped bot may change runtime).
	var sw botDTO
	json.Unmarshal(u.mustStatus(200, "PATCH", "/api/v1/bots/"+id, map[string]any{"runtime": "go"}), &sw)
	if sw.Runtime != "go" || sw.ImageRef != "alpine:3.23" || len(sw.Argv) != 1 || sw.Argv[0] != "./app" || sw.Generation != 2 {
		t.Fatalf("runtime switch: %+v", sw)
	}
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+id, map[string]any{"runtime": "nodejs"})
	// running / starting bots cannot be edited
	for _, st := range []string{"running", "starting", "building", "stopping"} {
		e.db.Exec(`UPDATE bots SET observed_state = ? WHERE id = ?`, st, id)
		u.mustStatus(409, "PATCH", "/api/v1/bots/"+id, map[string]any{"name": "x"})
		u.mustStatus(409, "PUT", "/api/v1/bots/"+id+"/env", map[string]any{"vars": map[string]string{"A": "1"}})
	}
	e.db.Exec(`UPDATE bots SET observed_state = 'stopped', desired_state = 'running' WHERE id = ?`, id)
	u.mustStatus(409, "PATCH", "/api/v1/bots/"+id, map[string]any{"name": "x"})
	u.mustStatus(409, "DELETE", "/api/v1/bots/"+id+"/env/A", nil)
	json.Unmarshal(func() []byte {
		e.db.Exec(`UPDATE bots SET desired_state='stopped' WHERE id=?`, id)
		return u.mustStatus(200, "GET", "/api/v1/bots/"+id, nil)
	}(), &b)
	if b.Generation != 3 || b.Name != "renamed" {
		t.Fatalf("rejected edits leaked: %+v", b)
	}
}

func TestEnvEncryptedMaskedAndGenerationBump(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	const secret = "MTIzNDU2.super-secret-discord-token"
	u.mustStatus(200, "PUT", "/api/v1/bots/"+id+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": secret, "MODE": "prod"}})

	body := u.mustStatus(200, "GET", "/api/v1/bots/"+id+"/env", nil)
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "prod") || !strings.Contains(string(body), maskedValue) {
		t.Fatalf("env not masked: %s", body)
	}
	// never in the bot resource either
	if b := u.mustStatus(200, "GET", "/api/v1/bots/"+id, nil); strings.Contains(string(b), secret) {
		t.Fatal("secret leaked in bot JSON")
	}
	// ciphertext at rest
	var ct, nonce []byte
	var keyID string
	e.db.QueryRow(`SELECT ciphertext, nonce, key_id FROM bot_env_vars WHERE bot_id=? AND name='DISCORD_TOKEN'`, id).Scan(&ct, &nonce, &keyID)
	if bytes.Contains(ct, []byte(secret)) || len(nonce) != 12 || keyID != "k1" {
		t.Fatal("secret not encrypted at rest")
	}
	// runner-side decryption
	plain, err := e.bots.DecryptEnv(context.Background(), id)
	if err != nil || plain["DISCORD_TOKEN"] != secret || plain["MODE"] != "prod" {
		t.Fatal(err, plain)
	}
	// generation advanced once per env change
	var gen int64
	e.db.QueryRow(`SELECT generation FROM bots WHERE id=?`, id).Scan(&gen)
	if gen != 1 {
		t.Fatalf("generation = %d", gen)
	}
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+id+"/env/MODE", nil)
	u.mustStatus(404, "DELETE", "/api/v1/bots/"+id+"/env/MODE", nil)
	e.db.QueryRow(`SELECT generation FROM bots WHERE id=?`, id).Scan(&gen)
	if gen != 2 {
		t.Fatalf("generation after delete = %d (failed delete must not bump)", gen)
	}
	// a ciphertext moved to another bot/name must not decrypt
	other := u.createBot("other")
	e.db.Exec(`INSERT INTO bot_env_vars SELECT ?, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms FROM bot_env_vars WHERE bot_id=?`, other, id)
	if _, err := e.bots.DecryptEnv(context.Background(), other); err == nil {
		t.Fatal("ciphertext replay across bots accepted")
	}
}

func TestEnvValidation(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	p := "/api/v1/bots/" + id + "/env"
	for name, vars := range map[string]map[string]string{
		"empty":      {},
		"bad name":   {"1BAD": "x"},
		"dash":       {"A-B": "x"},
		"space":      {"A B": "x"},
		"equals":     {"A=B": "x"},
		"path":       {"PATH": "/tmp"},
		"ld preload": {"LD_PRELOAD": "/x.so"},
		"reserved":   {"RIVET_KEY": "x"},
		"nul":        {"A": "a\u0000b"},
		"huge":       {"A": strings.Repeat("x", 40000)},
	} {
		if resp, b := u.do("PUT", p, map[string]any{"vars": vars}); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
	many := map[string]string{}
	for i := 0; i < 129; i++ {
		many["V"+string(rune('A'+i%26))+strings.Repeat("x", i/26)] = "1"
	}
	u.mustStatus(400, "PUT", p, map[string]any{"vars": many})
	var gen int64
	e.db.QueryRow(`SELECT generation FROM bots WHERE id=?`, id).Scan(&gen)
	if gen != 0 {
		t.Fatal("rejected input advanced generation")
	}
}

func TestDeleteRemovesWorkspaceThenRow(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	u.mustStatus(200, "PUT", "/api/v1/bots/"+id+"/env", map[string]any{"vars": map[string]string{"A": "1"}})
	os.WriteFile(filepath.Join(e.dataDir, id, "index.js"), []byte("x"), 0o640)
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+id, nil)
	if _, err := os.Stat(filepath.Join(e.dataDir, id)); !os.IsNotExist(err) {
		t.Fatal("workspace remains")
	}
	var n int
	e.db.QueryRow(`SELECT (SELECT count(*) FROM bots) + (SELECT count(*) FROM bot_env_vars)`).Scan(&n)
	if n != 0 {
		t.Fatal("rows remain")
	}
	u.mustStatus(404, "GET", "/api/v1/bots/"+id, nil)
}

func TestDeleteFailureKeepsRowMarkedDeleted(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	// Make cleanup fail: the data root itself is read-only, so the workspace
	// entry cannot be removed (read-only trees inside it are repaired).
	ws := filepath.Join(e.dataDir, id)
	os.MkdirAll(filepath.Join(ws, "sub"), 0o750)
	os.WriteFile(filepath.Join(ws, "sub", "f"), []byte("x"), 0o640)
	os.Chmod(e.dataDir, 0o500)
	defer os.Chmod(e.dataDir, 0o750)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if resp, _ := u.do("DELETE", "/api/v1/bots/"+id, nil); resp.StatusCode < 500 {
		t.Fatalf("expected failure, got %d", resp.StatusCode)
	}
	var state string
	if err := e.db.QueryRow(`SELECT desired_state FROM bots WHERE id=?`, id).Scan(&state); err != nil || state != "deleted" {
		t.Fatalf("row must be preserved as deleted: %v %q", err, state)
	}
	u.mustStatus(404, "GET", "/api/v1/bots/"+id, nil) // hidden while being deleted
	os.Chmod(e.dataDir, 0o750)
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+id, nil) // retry succeeds
}

func TestRuntimesEndpoint(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	var out struct{ Runtimes []map[string]any }
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/runtimes", nil), &out)
	if len(out.Runtimes) != 6 {
		t.Fatalf("%d runtimes", len(out.Runtimes))
	}
}

func TestLifecycleIntentIsIdempotentAndAsync(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	p := "/api/v1/bots/" + id
	type accepted struct {
		BotID      string `json:"bot_id"`
		Generation int64  `json:"generation"`
		Desired    string `json:"desired_state"`
	}
	call := func(action string) accepted {
		var a accepted
		json.Unmarshal(u.mustStatus(202, "POST", p+"/"+action, nil), &a)
		return a
	}
	a := call("start")
	if a.BotID != id || a.Generation != 1 || a.Desired != "running" || e.rec.count() != 1 {
		t.Fatalf("%+v notified=%d", a, e.rec.count())
	}
	// repeated Start returns the same generation and does not wake the runner again
	if a = call("start"); a.Generation != 1 || e.rec.count() != 1 {
		t.Fatalf("repeat start: %+v notified=%d", a, e.rec.count())
	}
	if a = call("restart"); a.Generation != 2 || a.Desired != "running" {
		t.Fatalf("restart: %+v", a)
	}
	if a = call("stop"); a.Generation != 3 || a.Desired != "stopped" {
		t.Fatalf("stop: %+v", a)
	}
	if a = call("stop"); a.Generation != 3 {
		t.Fatal("repeat stop must be idempotent")
	}
	// configuration is editable again only once the runner observed the stop
	var b botDTO
	json.Unmarshal(u.mustStatus(200, "GET", p, nil), &b)
	if b.DesiredState != "stopped" {
		t.Fatalf("%+v", b)
	}
}

func TestLifecycleAuthorization(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	mallory := e.user("mallory@example.com", domain.RoleUser)
	id := alice.createBot("b")
	for _, action := range []string{"start", "stop", "restart"} {
		mallory.mustStatus(404, "POST", "/api/v1/bots/"+id+"/"+action, nil)
	}
	if e.rec.count() != 0 {
		t.Fatal("runner notified for an unauthorized request")
	}
	anon := &client{e: e}
	if resp, _ := anon.do("POST", "/api/v1/bots/"+id+"/start", nil); resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := alice.req("POST", "/api/v1/bots/"+id+"/start", nil); resp.StatusCode != 403 {
		t.Fatalf("start without csrf: %d", resp.StatusCode)
	}
	var b botDTO
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/bots/"+id, nil), &b)
	if b.DesiredState != "stopped" || b.Generation != 0 {
		t.Fatalf("state changed by rejected requests: %+v", b)
	}
}

func TestLifecycleRejectedWithoutRunnerOrNode(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	e.db.Exec(`UPDATE nodes SET enabled = 0`)
	u.mustStatus(400, "POST", "/api/v1/bots/"+id+"/start", nil)
	e.db.Exec(`UPDATE nodes SET enabled = 1`)
	u.mustStatus(202, "POST", "/api/v1/bots/"+id+"/start", nil)

	e.bots.Notifier = nil // runner disabled
	u.mustStatus(503, "POST", "/api/v1/bots/"+id+"/stop", nil)
}

type fakePurger struct {
	e     *env
	calls int
	err   error
}

func (p *fakePurger) Purge(ctx context.Context, id string) error {
	p.calls++
	if p.err != nil {
		return p.err
	}
	p.e.bots.Workspaces.Remove(id)
	return p.e.db.DeleteBotRow(ctx, id)
}

func TestDeleteWithRunnerPurgesAndRetriesOnFailure(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	u.mustStatus(202, "POST", "/api/v1/bots/"+id+"/start", nil)
	e.db.Exec(`UPDATE bots SET container_id = 'c1', observed_state = 'running' WHERE id = ?`, id)

	p := &fakePurger{e: e, err: context.DeadlineExceeded}
	e.bots.Purger = p
	if resp, _ := u.do("DELETE", "/api/v1/bots/"+id, nil); resp.StatusCode < 500 {
		t.Fatal("expected failure")
	}
	var state string
	e.db.QueryRow(`SELECT desired_state FROM bots WHERE id=?`, id).Scan(&state)
	if state != "deleted" {
		t.Fatalf("row must remain marked deleted, got %q", state)
	}
	u.mustStatus(404, "GET", "/api/v1/bots/"+id, nil)
	p.err = nil
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+id, nil)
	if p.calls != 2 {
		t.Fatal(p.calls)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
	if n != 0 {
		t.Fatal("row remains")
	}
}

func TestDeleteWithContainerAndNoRunnerIsRefused(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@example.com", domain.RoleUser)
	id := u.createBot("b")
	e.db.Exec(`UPDATE bots SET container_id = 'c1' WHERE id = ?`, id)
	u.mustStatus(503, "DELETE", "/api/v1/bots/"+id, nil)
	var state string
	e.db.QueryRow(`SELECT desired_state FROM bots WHERE id=?`, id).Scan(&state)
	if state != "stopped" {
		t.Fatal("bot marked deleted although it cannot be torn down")
	}
}

func nil2ctx() context.Context { return context.Background() }
