package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeIssuer is an in-process OpenID Connect provider: discovery and keys
// from go-oidc's oidctest, plus a token endpoint that checks the client
// secret and PKCE and returns the ID token the test prepared for the code.
type fakeIssuer struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	secret string
	mu     sync.Mutex
	grants map[string]fakeGrant
}

type fakeGrant struct {
	challenge string
	idToken   string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{t: t, key: key, secret: "issuer-test-secret", grants: map[string]fakeGrant{}}
	disc := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "k1", Algorithm: oidc.RS256}}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			f.token(w, r)
			return
		}
		disc.ServeHTTP(w, r)
	}))
	disc.SetIssuer(f.srv.URL)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, sec, _ := r.BasicAuth()
	if id == "" {
		id, sec = r.Form.Get("client_id"), r.Form.Get("client_secret")
	}
	if id != "panel-client" || sec != f.secret {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	g, ok := f.grants[r.Form.Get("code")]
	delete(f.grants, r.Form.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": g.idToken})
}

// claims are the ID token claims; nil fields take defaults.
type claims map[string]any

func (f *fakeIssuer) sign(key *rsa.PrivateKey, c claims) string {
	b, _ := json.Marshal(c)
	return oidctest.SignIDToken(key, "k1", oidc.RS256, string(b))
}

func oidcEnv(t *testing.T) (*env, *fakeIssuer) {
	e := newEnv(t)
	f := newFakeIssuer(t)
	keys := e.bots.Keys
	o := &service.OIDCService{Store: e.db, Auth: e.auth, Keys: keys, PublicURL: func() string { return "https://panel.test" },
		States: oauth.NewStateStore(), HTTP: f.srv.Client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true, OIDC: o})
	return e, f
}

// browser is a cookie jar for the redirect flow.
type browser struct {
	e       *env
	cookies map[string]string
}

func (b *browser) get(path string) *http.Response {
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range b.cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	resp, err := b.e.app.Test(r)
	if err != nil {
		b.e.t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		b.cookies[c.Name] = c.Value
	}
	return resp
}

// signIn runs the browser flow. mk builds the ID token from the nonce the
// panel sent; it returns the final redirect location.
func (f *fakeIssuer) signIn(b *browser, start string, mk func(nonce string) string) string {
	f.t.Helper()
	var loc string
	if strings.HasPrefix(start, "http") {
		loc = start
	} else {
		resp := b.get(start)
		if resp.StatusCode != 302 {
			f.t.Fatalf("login: %d", resp.StatusCode)
		}
		loc = resp.Header.Get("Location")
	}
	u, err := url.Parse(loc)
	if err != nil || !strings.HasPrefix(loc, f.srv.URL+"/auth") {
		f.t.Fatalf("authorize redirect: %q", loc)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" || q.Get("redirect_uri") != "https://panel.test/api/v1/auth/oidc/corp/callback" {
		f.t.Fatalf("authorize parameters: %v", q)
	}
	code := oauth.RandString(12)
	f.mu.Lock()
	f.grants[code] = fakeGrant{challenge: q.Get("code_challenge"), idToken: mk(q.Get("nonce"))}
	f.mu.Unlock()
	resp := b.get("/api/v1/auth/oidc/corp/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=" + code)
	if resp.StatusCode != 302 {
		f.t.Fatalf("callback: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func (f *fakeIssuer) claims(nonce, sub, email string, verified any) claims {
	return claims{"iss": f.srv.URL, "aud": "panel-client", "sub": sub, "nonce": nonce, "email": email, "email_verified": verified,
		"name": "Test Person", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
}

func TestOIDCSignIn(t *testing.T) {
	e, f := oidcEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	e.user("bob@x.io", domain.RoleUser)
	sec := f.secret
	body := map[string]any{"slug": "corp", "name": "Corp SSO", "issuer": f.srv.URL, "client_id": "panel-client", "client_secret": sec,
		"scopes": "email profile", "enabled": true, "allow_signup": true, "link_by_email": true}
	// Discovery is checked when saving.
	bad := map[string]any{}
	for k, v := range body {
		bad[k] = v
	}
	bad["issuer"] = f.srv.URL + "/nope"
	admin.mustStatus(400, "POST", "/api/v1/admin/oidc-providers", bad)
	bad["issuer"] = "http://idp.example.com"
	admin.mustStatus(400, "POST", "/api/v1/admin/oidc-providers", bad)
	bad["issuer"], bad["default_role_id"] = f.srv.URL, "admin"
	admin.mustStatus(400, "POST", "/api/v1/admin/oidc-providers", bad)
	var p oidcProviderDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/oidc-providers", body), &p)
	if !p.SecretSet || p.Scopes != "openid email profile" || p.RedirectURI != "https://panel.test/api/v1/auth/oidc/corp/callback" {
		t.Fatalf("provider: %+v", p)
	}
	// The secret is sealed, never stored or returned in plain text.
	var raw []byte
	e.db.QueryRow(`SELECT secret_ciphertext FROM oidc_providers WHERE id = ?`, p.ID).Scan(&raw)
	if len(raw) == 0 || bytes.Contains(raw, []byte(sec)) || strings.Contains(string(admin.mustStatus(200, "GET", "/api/v1/admin/oidc-providers", nil)), sec) {
		t.Fatal("client secret not sealed")
	}
	var pub struct {
		OIDC []struct{ Slug, Name string }
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/auth/providers", nil), &pub)
	if len(pub.OIDC) != 1 || pub.OIDC[0].Slug != "corp" {
		t.Fatalf("public providers: %+v", pub)
	}

	newBrowser := func() *browser { return &browser{e: e, cookies: map[string]string{}} }
	ok := func(nonce string) string { return f.sign(f.key, f.claims(nonce, "sub-alice", "alice@corp.io", true)) }

	// Happy path: a new account, verified because the provider said so.
	b := newBrowser()
	if loc := f.signIn(b, "/api/v1/auth/oidc/corp/login", ok); loc != "/login?complete=1" || b.cookies[sessionCookie] == "" {
		t.Fatalf("sign-in: %q", loc)
	}
	alice, err := e.db.GetUserByEmail(context.Background(), "alice@corp.io")
	if err != nil || !alice.EmailVerified || alice.PasswordHash != "" || alice.DisplayName != "Test Person" || alice.Role != domain.RoleUser {
		t.Fatalf("account: %+v %v", alice, err)
	}
	// The same subject signs in to the same account again.
	b2 := newBrowser()
	if loc := f.signIn(b2, "/api/v1/auth/oidc/corp/login", ok); loc != "/login?complete=1" {
		t.Fatalf("second sign-in: %q", loc)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := []struct {
		name string
		mk   func(nonce string) string
		want string
	}{
		{"bad signature", func(n string) string { return f.sign(other, f.claims(n, "sub-alice", "alice@corp.io", true)) }, "token_invalid"},
		{"wrong audience", func(n string) string {
			c := f.claims(n, "sub-alice", "alice@corp.io", true)
			c["aud"] = "someone-else"
			return f.sign(f.key, c)
		}, "token_invalid"},
		{"wrong issuer", func(n string) string {
			c := f.claims(n, "sub-alice", "alice@corp.io", true)
			c["iss"] = "https://evil.example"
			return f.sign(f.key, c)
		}, "token_invalid"},
		{"expired", func(n string) string {
			c := f.claims(n, "sub-alice", "alice@corp.io", true)
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			return f.sign(f.key, c)
		}, "token_invalid"},
		{"nonce mismatch", func(n string) string { return f.sign(f.key, f.claims(n+"x", "sub-alice", "alice@corp.io", true)) }, "token_invalid"},
		// An existing password account is not linked on an unverified claim.
		{"unverified email of an existing account", func(n string) string { return f.sign(f.key, f.claims(n, "sub-bob", "bob@x.io", false)) }, "link_required"},
		{"unverified email as a string", func(n string) string { return f.sign(f.key, f.claims(n, "sub-bob", "bob@x.io", "false")) }, "link_required"},
		// Administrators are never linked automatically.
		{"administrator by verified email", func(n string) string { return f.sign(f.key, f.claims(n, "sub-adm", "admin@x.io", true)) }, "link_required"},
	}
	for _, tc := range cases {
		if loc := f.signIn(newBrowser(), "/api/v1/auth/oidc/corp/login", tc.mk); loc != "/login?error="+tc.want {
			t.Fatalf("%s: %q", tc.name, loc)
		}
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM oidc_identities WHERE subject IN ('sub-bob', 'sub-adm')`).Scan(&n)
	if n != 0 {
		t.Fatal("identity linked on a refused sign-in")
	}

	// State: a wrong state or a missing/foreign binder cookie is refused.
	b3 := newBrowser()
	resp := b3.get("/api/v1/auth/oidc/corp/login")
	q, _ := url.Parse(resp.Header.Get("Location"))
	st := q.Query().Get("state")
	if r := b3.get("/api/v1/auth/oidc/corp/callback?state=wrong&code=x"); r.Header.Get("Location") != "/login?error=state_invalid" {
		t.Fatalf("wrong state: %q", r.Header.Get("Location"))
	}
	if r := newBrowser().get("/api/v1/auth/oidc/corp/callback?state=" + st + "&code=x"); r.Header.Get("Location") != "/login?error=state_invalid" {
		t.Fatalf("missing binder: %q", r.Header.Get("Location"))
	}

	// A verified address links to the existing account.
	bb := newBrowser()
	if loc := f.signIn(bb, "/api/v1/auth/oidc/corp/login", func(n string) string { return f.sign(f.key, f.claims(n, "sub-bob", "bob@x.io", true)) }); loc != "/login?complete=1" {
		t.Fatalf("verified link: %q", loc)
	}
	var owner string
	e.db.QueryRow(`SELECT user_id FROM oidc_identities WHERE subject = 'sub-bob'`).Scan(&owner)
	if owner != e.userID("bob@x.io") {
		t.Fatal("not linked to the existing account")
	}

	// Sign-up off: an unknown identity is refused.
	body["allow_signup"] = false
	delete(body, "client_secret") // keep the stored secret
	admin.mustStatus(200, "PATCH", "/api/v1/admin/oidc-providers/"+p.ID, body)
	if loc := f.signIn(newBrowser(), "/api/v1/auth/oidc/corp/login", func(n string) string { return f.sign(f.key, f.claims(n, "sub-new", "new@corp.io", true)) }); loc != "/login?error=signup_disabled" {
		t.Fatalf("signup disabled: %q", loc)
	}

	// Sign in first, then link: the identity joins the signed-in account,
	// whatever address the provider reports.
	carol := e.user("carol@x.io", domain.RoleUser)
	linkStart := func(c *client) (*browser, string) {
		resp, body := c.do("POST", "/api/v1/me/identities/corp/start", nil)
		var st struct{ URL string }
		json.Unmarshal(body, &st)
		if resp.StatusCode != 200 || st.URL == "" {
			t.Fatalf("link start: %d %s", resp.StatusCode, body)
		}
		br := &browser{e: e, cookies: map[string]string{}}
		for _, ck := range resp.Cookies() { // the binder cookie is set on the start request
			br.cookies[ck.Name] = ck.Value
		}
		return br, st.URL
	}
	cb, start2URL := linkStart(carol)
	if loc := f.signIn(cb, start2URL, func(n string) string { return f.sign(f.key, f.claims(n, "sub-carol", "carol@elsewhere.io", false)) }); loc != "/settings/connected-accounts?linked=corp" {
		t.Fatalf("link: %q", loc)
	}
	var ids struct {
		Identities []identityDTO
	}
	json.Unmarshal(carol.mustStatus(200, "GET", "/api/v1/me/identities", nil), &ids)
	if len(ids.Identities) != 1 || ids.Identities[0].Slug != "corp" || !ids.Identities[0].CanUnlink {
		t.Fatalf("identities: %+v", ids)
	}
	// The same subject cannot be linked to a second account.
	db3, start3URL := linkStart(e.user("dave@x.io", domain.RoleUser))
	if loc := f.signIn(db3, start3URL, func(n string) string { return f.sign(f.key, f.claims(n, "sub-carol", "carol@elsewhere.io", true)) }); loc != "/settings/connected-accounts?error=already_linked" {
		t.Fatalf("second link: %q", loc)
	}
	carol.mustStatus(204, "DELETE", "/api/v1/me/identities/"+p.ID, nil)

	// An account whose only sign-in method is the provider cannot unlink it.
	var aliceIDs struct{ Identities []identityDTO }
	aliceClient := &client{e: e, cookie: b.cookies[sessionCookie]}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(aliceClient.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	aliceClient.csrf = me.CSRF
	json.Unmarshal(aliceClient.mustStatus(200, "GET", "/api/v1/me/identities", nil), &aliceIDs)
	if len(aliceIDs.Identities) != 1 || aliceIDs.Identities[0].CanUnlink {
		t.Fatalf("alice identities: %+v", aliceIDs)
	}
	aliceClient.mustStatus(400, "DELETE", "/api/v1/me/identities/"+p.ID, nil)

	// A disabled provider is not offered and its routes are gone.
	body["enabled"] = false
	admin.mustStatus(200, "PATCH", "/api/v1/admin/oidc-providers/"+p.ID, body)
	if r := newBrowser().get("/api/v1/auth/oidc/corp/login"); r.StatusCode != 404 {
		t.Fatalf("disabled provider: %d", r.StatusCode)
	}
	admin.mustStatus(204, "DELETE", "/api/v1/admin/oidc-providers/"+p.ID, nil)
}

// A delegated settings manager cannot make a provider hand out more than it
// holds, and cannot delete a role a provider uses.
func TestOIDCProviderNoEscalation(t *testing.T) {
	e, f := oidcEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	mgr := e.user("mgr@x.io", domain.RoleUser)
	rid := admin.createRole("Settings", domain.PermSettingsManage, domain.PermBotsConsole)
	wide := admin.createRole("Wide", domain.PermBotsConsole, domain.PermUsersManage)
	narrow := admin.createRole("Narrow", domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("mgr@x.io"), map[string]any{"role": rid})
	body := map[string]any{"slug": "corp", "name": "Corp", "issuer": f.srv.URL, "client_id": "panel-client", "enabled": true, "allow_signup": true}
	for _, role := range []string{wide, "", "user", "admin"} {
		body["default_role_id"] = role
		mgr.mustStatus(400, "POST", "/api/v1/admin/oidc-providers", body)
	}
	body["default_role_id"] = narrow
	var p oidcProviderDTO
	json.Unmarshal(mgr.mustStatus(201, "POST", "/api/v1/admin/oidc-providers", body), &p)
	admin.mustStatus(400, "DELETE", "/api/v1/admin/roles/"+narrow, nil)
	// A provider an administrator widened is no longer editable by the delegate.
	body["default_role_id"] = wide
	admin.mustStatus(200, "PATCH", "/api/v1/admin/oidc-providers/"+p.ID, body)
	body["default_role_id"] = narrow
	mgr.mustStatus(403, "PATCH", "/api/v1/admin/oidc-providers/"+p.ID, body)

	// New accounts get the default role.
	b := &browser{e: e, cookies: map[string]string{}}
	f.secret = ""
	loc := f.signIn(b, "/api/v1/auth/oidc/corp/login", func(n string) string { return f.sign(f.key, f.claims(n, "s1", "new@corp.io", true)) })
	if loc != "/login?complete=1" {
		t.Fatalf("sign-in: %q", loc)
	}
	u, _ := e.db.GetUserByEmail(context.Background(), "new@corp.io")
	if u.RoleID != wide {
		t.Fatalf("role: %q", u.RoleID)
	}
}
