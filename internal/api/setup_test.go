package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/service"
)

func setupEnv(t *testing.T, env service.EnvSettings) (*env, *service.SettingsService, *service.OAuthService) {
	e := newEnv(t)
	o := &service.OAuthService{Store: e.db, Auth: e.auth, Keys: e.bots.Keys, Providers: map[string]oauth.Provider{}, States: oauth.NewStateStore()}
	st := &service.SettingsService{Store: e.db, Keys: e.bots.Keys, OAuth: o, Auth: e.auth, Env: env}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: o, Settings: st, SecureCookies: true})
	return e, st, o
}

func TestFirstRunSetupAndSettings(t *testing.T) {
	e, st, o := setupEnv(t, service.EnvSettings{Production: true})
	anon := &client{e: e}
	var status struct{ Needed bool }
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/setup/status", nil), &status)
	if !status.Needed {
		t.Fatal("a fresh installation must need setup")
	}
	anon.mustStatus(400, "POST", "/api/v1/setup/check", map[string]string{"code": "AAAA-BBBB-CCCC-DDDD"})
	code := st.SetupCode()
	anon.mustStatus(204, "POST", "/api/v1/setup/check", map[string]string{"code": code})

	gh := "Iv1.abc123"
	body := map[string]any{"code": code, "email": "Owner@Example.com", "password": pw,
		"settings": map[string]any{"public_url": "https://panel.example.com/", "github_client_id": gh, "github_client_secret": "s3cr3t-value"}}
	// A bad address is refused before anything is created.
	bad := map[string]any{"code": code, "email": "owner@example.com", "password": pw, "settings": map[string]any{"public_url": "http://panel.example.com"}}
	anon.mustStatus(400, "POST", "/api/v1/setup/complete", bad)

	resp, raw := anon.do("POST", "/api/v1/setup/complete", body)
	if resp.StatusCode != 200 {
		t.Fatalf("complete: %d %s", resp.StatusCode, raw)
	}
	admin := &client{e: e}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			admin.cookie = ck.Value
		}
	}
	var out struct {
		User struct{ Email, Role string }
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(raw, &out)
	admin.csrf = out.CSRF
	if admin.cookie == "" || out.User.Role != domain.RoleAdmin || out.User.Email != "owner@example.com" {
		t.Fatalf("setup session: %s", raw)
	}
	// Once done, setup is closed, even with the right code.
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/setup/status", nil), &status)
	if status.Needed {
		t.Fatal("setup still open")
	}
	anon.mustStatus(400, "POST", "/api/v1/setup/complete", body)

	// GitHub sign-in is live without a restart; the secret is never returned.
	if !o.Enabled("github") || o.CurrentPublicURL() != "https://panel.example.com" {
		t.Fatal("settings not applied")
	}
	var prov struct{ Providers []struct{ ID string } }
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/auth/providers", nil), &prov)
	if len(prov.Providers) != 1 || prov.Providers[0].ID != "github" {
		t.Fatalf("providers: %+v", prov)
	}
	raw = admin.mustStatus(200, "GET", "/api/v1/admin/settings", nil)
	var sv map[string]any
	json.Unmarshal(raw, &sv)
	if sv["github_client_id"] != gh || sv["github_secret_set"] != true || strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("settings view: %s", raw)
	}
	// Clearing the secret turns GitHub off again; a user who is not an
	// administrator cannot read or change settings.
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"github_client_secret": ""})
	if o.Enabled("github") {
		t.Fatal("github still enabled without a secret")
	}
	admin.mustStatus(201, "POST", "/api/v1/users", map[string]any{"email": "u@x.io", "password": pw, "role": "user"})
	u := &client{e: e}
	r2, b2 := u.do("POST", "/api/v1/auth/login", map[string]string{"email": "u@x.io", "password": pw})
	if r2.StatusCode != 200 {
		t.Fatalf("login %s", b2)
	}
	for _, ck := range r2.Cookies() {
		if ck.Name == sessionCookie {
			u.cookie = ck.Value
		}
	}
	if resp, _ := u.req("GET", "/api/v1/admin/settings", nil); resp.StatusCode != 403 {
		t.Fatalf("user reads settings: %d", resp.StatusCode)
	}
}

func TestEnvironmentSettingsAreLocked(t *testing.T) {
	e, st, o := setupEnv(t, service.EnvSettings{PublicURL: "https://env.example.com", DiscordID: "123", DiscordSecret: "abc", Production: true})
	if err := st.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !o.Enabled("discord") {
		t.Fatal("env provider not applied")
	}
	admin := e.user("a@x.io", domain.RoleAdmin)
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"public_url": "https://db.example.com"})
	var sv struct {
		PublicURL string          `json:"public_url"`
		Locked    map[string]bool `json:"locked"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/settings", nil), &sv)
	if sv.PublicURL != "https://env.example.com" || !sv.Locked["public_url"] || !sv.Locked["discord_client_id"] {
		t.Fatalf("env must win: %+v", sv)
	}
}

func TestAccountInvitationRegistration(t *testing.T) {
	e, _, _ := setupEnv(t, service.EnvSettings{AllowSignup: true, AllowSignupSet: true})
	admin := e.user("admin@x.io", domain.RoleAdmin)
	var created struct {
		Token, Path string
		Invite      struct{ Email, Role string }
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "new@x.io", "role": "user", "expires_in_days": 7}), &created)
	if !strings.HasPrefix(created.Token, "bpu_") || !strings.HasPrefix(created.Path, "/register#") {
		t.Fatalf("invite: %+v", created)
	}
	anon := &client{e: e}
	anon.mustStatus(200, "POST", "/api/v1/registration/preview", map[string]string{"token": created.Token})
	resp, body := anon.do("POST", "/api/v1/registration", map[string]string{"token": created.Token, "email": "new@x.io", "password": pw})
	if resp.StatusCode != 201 || !strings.Contains(string(body), `"email":"new@x.io"`) {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	anon.mustStatus(404, "POST", "/api/v1/registration", map[string]string{"token": created.Token, "email": "new@x.io", "password": pw})

	closed, _, _ := setupEnv(t, service.EnvSettings{AllowSignup: false, AllowSignupSet: true})
	a := closed.user("a@x.io", domain.RoleAdmin)
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"role": "user", "expires_in_days": 1}), &created)
	(&client{e: closed}).mustStatus(400, "POST", "/api/v1/registration", map[string]string{"token": created.Token, "email": "blocked@x.io", "password": pw})
}
