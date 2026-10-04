package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeGitHub serves the three GitHub endpoints the provider calls.
type fakeGitHub struct {
	srv    *httptest.Server
	id     int64
	login  string
	email  string // primary verified email; "" means none
	verify string // last code_verifier seen
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{id: 1001, login: "octo", email: "octo@example.com"}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.verify = r.Form.Get("code_verifier")
		if r.Form.Get("code") != "good" || r.Form.Get("client_secret") != "sekret" {
			w.Write([]byte(`{"error":"bad_verification_code"}`)) // GitHub answers 200
			return
		}
		w.Write([]byte(`{"access_token":"gho_plaintexttoken","scope":"read:user,user:email"}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_plaintexttoken" {
			http.Error(w, "no", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": f.id, "login": f.login, "avatar_url": "https://a/x.png"})
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{{"email": "noise@example.com", "primary": false, "verified": true}}
		if f.email != "" {
			out = append(out, map[string]any{"email": f.email, "primary": true, "verified": true})
		}
		json.NewEncoder(w).Encode(out)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func oauthEnv(t *testing.T, allowSignup bool) (*env, *fakeGitHub, *service.OAuthService) {
	e := newEnv(t)
	gh := newFakeGitHub(t)
	keys, err := secrets.LoadDir(t.TempDir(), "k1", true)
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.OAuthService{
		Store: e.db, Auth: e.auth, Keys: keys, PublicURL: "https://panel.example.com", AllowSignup: allowSignup,
		States: oauth.NewStateStore(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Providers: map[string]oauth.Provider{"github": &oauth.GitHub{
			ClientID: "cid", Secret: "sekret", AuthBase: gh.srv.URL + "/authorize", TokenURL: gh.srv.URL + "/token", APIBase: gh.srv.URL,
		}},
	}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: svc, SecureCookies: true})
	return e, gh, svc
}

// begin runs the browser's first hop and returns the provider state and binder cookie.
func begin(t *testing.T, e *env, path string) (state, binder string) {
	t.Helper()
	c := &client{e: e}
	resp, _ := c.req("GET", path, nil)
	return parseBegin(t, resp.StatusCode, resp.Header.Get("Location"), resp.Cookies())
}

func parseBegin(t *testing.T, status int, loc string, cookies []*http.Cookie) (string, string) {
	t.Helper()
	if status != 302 {
		t.Fatalf("begin: status %d", status)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("client_id") != "cid" ||
		q.Get("redirect_uri") != "https://panel.example.com/api/v1/auth/github/callback" {
		t.Fatalf("authorize URL: %s", loc)
	}
	for _, ck := range cookies {
		if ck.Name == oauthCookie {
			if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/api/v1/auth" {
				t.Fatalf("state cookie flags: %+v", ck)
			}
			return q.Get("state"), ck.Value
		}
	}
	t.Fatal("no state cookie")
	return "", ""
}

func callback(t *testing.T, e *env, state, binder, code string) *http.Response {
	t.Helper()
	c := &client{e: e}
	resp, _ := c.req("GET", "/api/v1/auth/github/callback?code="+code+"&state="+url.QueryEscape(state), nil, func(r *http.Request) {
		if binder != "" {
			r.AddCookie(&http.Cookie{Name: oauthCookie, Value: binder})
		}
	})
	return resp
}

func sessionOf(resp *http.Response) string {
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			return ck.Value
		}
	}
	return ""
}

func TestOAuthDisabledWhenNotConfigured(t *testing.T) {
	e := newEnv(t)
	c := &client{e: e}
	if resp, _ := c.req("GET", "/api/v1/auth/github/login", nil); resp.StatusCode != 404 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_, body := c.req("GET", "/api/v1/auth/providers", nil)
	if strings.TrimSpace(string(body)) != `{"oidc":[],"passkeys":false,"providers":[]}` {
		t.Fatalf("providers: %s", body)
	}
}

func TestOAuthSignupDisabledByDefault(t *testing.T) {
	e, _, _ := oauthEnv(t, false)
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	resp := callback(t, e, state, binder, "good")
	if loc := resp.Header.Get("Location"); resp.StatusCode != 302 || loc != "/login?error=signup_disabled" || sessionOf(resp) != "" {
		t.Fatalf("got %d %q", resp.StatusCode, loc)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	if n != 0 {
		t.Fatal("user created although signup is disabled")
	}
}

func TestOAuthSignupLoginAndTokenSealing(t *testing.T) {
	e, gh, svc := oauthEnv(t, true)
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	resp := callback(t, e, state, binder, "good")
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/login?complete=1" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	tok := sessionOf(resp)
	if tok == "" {
		t.Fatal("no session cookie")
	}
	if gh.verify == "" {
		t.Fatal("PKCE verifier not sent to the token endpoint")
	}
	c := &client{e: e, cookie: tok}
	var me struct {
		User struct {
			Email, Role   string
			EmailVerified bool `json:"email_verified"`
		}
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	// The provider-verified address counts as verified.
	if me.User.Email != "octo@example.com" || me.User.Role != "user" || !me.User.EmailVerified {
		t.Fatalf("me: %+v", me)
	}

	// The access token is stored sealed, never in plaintext, and is recoverable.
	var raw []byte
	var uid string
	e.db.QueryRow(`SELECT user_id, token_ciphertext FROM oauth_accounts WHERE provider='github'`).Scan(&uid, &raw)
	if len(raw) == 0 || strings.Contains(string(raw), "gho_plaintexttoken") {
		t.Fatal("token not sealed")
	}
	if got, err := svc.GitHubToken(context.Background(), uid); err != nil || got != "gho_plaintexttoken" {
		t.Fatalf("GitHubToken = %q, %v", got, err)
	}

	// Signing in again reuses the same account.
	state, binder = begin(t, e, "/api/v1/auth/github/login")
	resp = callback(t, e, state, binder, "good")
	if resp.Header.Get("Location") != "/login?complete=1" || sessionOf(resp) == "" {
		t.Fatal("second login failed")
	}
	var users int
	e.db.QueryRow(`SELECT count(*) FROM users`).Scan(&users)
	if users != 1 {
		t.Fatalf("users = %d", users)
	}
}

func TestOAuthStateAndBinderValidation(t *testing.T) {
	e, _, _ := oauthEnv(t, true)
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	for name, tc := range map[string][2]string{
		"no cookie":    {state, ""},
		"wrong binder": {state, "AAAA"},
		"unknown":      {"nope", binder},
	} {
		resp := callback(t, e, tc[0], tc[1], "good")
		if resp.Header.Get("Location") != "/login?error=state_invalid" || sessionOf(resp) != "" {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// Failed attempts must not consume the real flow.
	if resp := callback(t, e, state, binder, "good"); resp.Header.Get("Location") != "/login?complete=1" {
		t.Fatalf("valid flow burned by bad attempts: %q", resp.Header.Get("Location"))
	}
	// Single use.
	if resp := callback(t, e, state, binder, "good"); resp.Header.Get("Location") != "/login?error=state_invalid" {
		t.Fatal("state replay accepted")
	}
	// Provider rejects the code.
	state, binder = begin(t, e, "/api/v1/auth/github/login")
	if resp := callback(t, e, state, binder, "bad"); resp.Header.Get("Location") != "/login?error=exchange_failed" {
		t.Fatalf("bad code: %q", resp.Header.Get("Location"))
	}
	// User denied at the provider.
	c := &client{e: e}
	resp, _ := c.req("GET", "/api/v1/auth/github/callback?error=access_denied&state=x", nil)
	if resp.Header.Get("Location") != "/login?error=denied" {
		t.Fatalf("denied: %q", resp.Header.Get("Location"))
	}
}

func TestOAuthNeverMergesByEmail(t *testing.T) {
	e, gh, _ := oauthEnv(t, true)
	victim := e.user("octo@example.com", domain.RoleUser)
	_ = victim
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	resp := callback(t, e, state, binder, "good")
	if resp.Header.Get("Location") != "/login?error=email_in_use" || sessionOf(resp) != "" {
		t.Fatalf("got %q", resp.Header.Get("Location"))
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM oauth_accounts`).Scan(&n)
	if n != 0 {
		t.Fatal("identity linked by email match")
	}
	// No verified email at all: signup is refused.
	gh.email = ""
	state, binder = begin(t, e, "/api/v1/auth/github/login")
	if resp := callback(t, e, state, binder, "good"); resp.Header.Get("Location") != "/login?error=email_unverified" {
		t.Fatalf("got %q", resp.Header.Get("Location"))
	}
}

func TestOAuthLinkFlowAndLastMethodRule(t *testing.T) {
	e, gh, _ := oauthEnv(t, false)
	alice := e.user("alice@example.com", domain.RoleUser)

	// Linking requires a session and CSRF.
	anon := &client{e: e}
	if resp, _ := anon.req("POST", "/api/v1/me/connections/github/start", nil); resp.StatusCode != 401 {
		t.Fatalf("anon start: %d", resp.StatusCode)
	}
	if resp, _ := alice.req("POST", "/api/v1/me/connections/github/start", nil); resp.StatusCode != 403 {
		t.Fatalf("start without CSRF: %d", resp.StatusCode)
	}
	resp, body := alice.do("POST", "/api/v1/me/connections/github/start", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	var st struct{ URL string }
	json.Unmarshal(body, &st)
	state, binder := parseBegin(t, 302, st.URL, resp.Cookies())

	// The callback arrives cross-site: the Strict session cookie is absent.
	res := callback(t, e, state, binder, "good")
	if res.Header.Get("Location") != "/settings/connected-accounts?linked=github" || sessionOf(res) != "" {
		t.Fatalf("link callback: %q", res.Header.Get("Location"))
	}
	var cons struct{ Connections []connectionDTO }
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/me/connections", nil), &cons)
	var got connectionDTO
	for _, c := range cons.Connections {
		if c.Provider == "github" {
			got = c
		}
	}
	if !got.Linked || got.Username != "octo" || !got.CanDisconnect || !got.Configured {
		t.Fatalf("connection: %+v", got)
	}

	// Someone else cannot claim the same GitHub identity.
	bob := e.user("bob@example.com", domain.RoleUser)
	resp, body = bob.do("POST", "/api/v1/me/connections/github/start", nil)
	json.Unmarshal(body, &st)
	state, binder = parseBegin(t, 302, st.URL, resp.Cookies())
	if res := callback(t, e, state, binder, "good"); res.Header.Get("Location") != "/settings/connected-accounts?error=already_linked" {
		t.Fatalf("stolen identity: %q", res.Header.Get("Location"))
	}

	// A link flow started by Alice cannot be completed into another browser's session:
	// without her binder cookie it is rejected.
	resp, body = alice.do("POST", "/api/v1/me/connections/github/start", nil)
	json.Unmarshal(body, &st)
	state, _ = parseBegin(t, 302, st.URL, resp.Cookies())
	gh.id = 2002
	if res := callback(t, e, state, "", "good"); res.Header.Get("Location") != "/login?error=state_invalid" {
		t.Fatalf("cross-browser link: %q", res.Header.Get("Location"))
	}

	// Alice has a password, so she may disconnect her only provider.
	alice.mustStatus(204, "DELETE", "/api/v1/me/connections/github", nil)
	alice.mustStatus(404, "DELETE", "/api/v1/me/connections/github", nil)
	alice.mustStatus(404, "DELETE", "/api/v1/me/connections/gitlab", nil)
}

func TestOAuthOnlyUserCannotDisconnectLastProvider(t *testing.T) {
	e, _, _ := oauthEnv(t, true)
	state, binder := begin(t, e, "/api/v1/auth/github/login")
	resp := callback(t, e, state, binder, "good")
	c := &client{e: e, cookie: sessionOf(resp)}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	c.csrf = me.CSRF

	var cons struct{ Connections []connectionDTO }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/me/connections", nil), &cons)
	for _, x := range cons.Connections {
		if x.Provider == "github" && (!x.Linked || x.CanDisconnect) {
			t.Fatalf("OAuth-only user must not be able to disconnect: %+v", x)
		}
	}
	body := c.mustStatus(400, "DELETE", "/api/v1/me/connections/github", nil)
	if !strings.Contains(string(body), "only remaining sign-in method") {
		t.Fatalf("body: %s", body)
	}
	// And a passwordless account cannot sign in through the password form.
	if resp, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"email": "octo@example.com", "password": ""}); resp.StatusCode != 401 {
		t.Fatalf("empty password login: %d", resp.StatusCode)
	}
}
