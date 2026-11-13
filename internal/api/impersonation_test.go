package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// jar is one browser: it keeps every cookie the panel sets (the active
// session and the other signed-in accounts) and the CSRF token of the
// active session.
type jar struct {
	e       *env
	cookies map[string]string
	csrf    string
}

func newJar(e *env) *jar { return &jar{e: e, cookies: map[string]string{}} }

func (j *jar) send(method, path string, body any) (int, []byte) {
	j.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range j.cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if j.csrf != "" {
		r.Header.Set(csrfHeader, j.csrf)
	}
	resp, err := j.e.app.Test(r, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		j.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	for _, c := range resp.Cookies() {
		if c.Value == "" || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(j.cookies, c.Name)
		} else {
			j.cookies[c.Name] = c.Value
		}
	}
	var out struct {
		CSRF *string `json:"csrf_token"`
	}
	if json.Unmarshal(b, &out) == nil && out.CSRF != nil {
		j.csrf = *out.CSRF
	}
	return resp.StatusCode, b
}

func (j *jar) must(want int, method, path string, body any) []byte {
	j.e.t.Helper()
	code, b := j.send(method, path, body)
	if code != want {
		j.e.t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, b)
	}
	return b
}

func (j *jar) login(email string) {
	j.e.t.Helper()
	j.must(200, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": pw})
}

type meOut struct {
	User         userDTO `json:"user"`
	Impersonator *struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"impersonator"`
}

func (j *jar) me() meOut {
	j.e.t.Helper()
	var m meOut
	json.Unmarshal(j.must(200, "GET", "/api/v1/auth/me", nil), &m)
	return m
}

type accountsOut struct {
	Current  *userDTO     `json:"current"`
	Accounts []accountDTO `json:"accounts"`
}

func (j *jar) accounts() accountsOut {
	j.e.t.Helper()
	var a accountsOut
	json.Unmarshal(j.must(200, "GET", "/api/v1/auth/accounts", nil), &a)
	return a
}

func TestImpersonationStartsInOneStepAndReturns(t *testing.T) {
	e := auditEnv(t)
	ctx := context.Background()
	e.user("admin@x.io", domain.RoleAdmin)
	bob := e.user("bob@x.io", domain.RoleUser)
	bobBot := bob.createBot("bobs-bot")
	e.user("other-admin@x.io", domain.RoleAdmin)
	bobUser, _ := e.db.GetUserByEmail(ctx, "bob@x.io")
	otherAdmin, _ := e.db.GetUserByEmail(ctx, "other-admin@x.io")
	adminUser, _ := e.db.GetUserByEmail(ctx, "admin@x.io")

	j := newJar(e)
	j.login("admin@x.io")

	// Administrators cannot be impersonated, nor can the administrator itself.
	j.must(400, "POST", "/api/v1/admin/users/"+otherAdmin.ID+"/impersonate", nil)
	j.must(400, "POST", "/api/v1/admin/users/"+adminUser.ID+"/impersonate", nil)

	// One request: the browser now acts as Bob and sees Bob's apps.
	var started struct {
		User         userDTO `json:"user"`
		Impersonator struct{ Email string } `json:"impersonator"`
	}
	json.Unmarshal(j.must(200, "POST", "/api/v1/admin/users/"+bobUser.ID+"/impersonate", nil), &started)
	if started.User.ID != bobUser.ID || started.Impersonator.Email != "admin@x.io" {
		t.Fatalf("start: %+v", started)
	}
	if m := j.me(); m.User.ID != bobUser.ID || m.Impersonator == nil || m.Impersonator.Email != "admin@x.io" {
		t.Fatalf("me while impersonating: %+v", m)
	}
	j.must(200, "GET", "/api/v1/bots/"+bobBot, nil)

	// Credentials and identity stay with Bob; nested impersonation is refused.
	j.must(403, "POST", "/api/v1/me/password", map[string]string{"current_password": pw, "new_password": "something-else-long"})
	j.must(403, "PUT", "/api/v1/me/profile", map[string]string{"display_name": "Mallory"})
	j.must(403, "POST", "/api/v1/me/sessions/revoke-others", nil)
	j.must(403, "POST", "/api/v1/me/api-keys", map[string]string{"name": "k"})
	j.must(403, "POST", "/api/v1/auth/accounts/park", nil)
	j.must(403, "POST", "/api/v1/admin/users/"+otherAdmin.ID+"/impersonate", nil)

	// Bob sees the impersonation in his sessions, named after the administrator.
	var l sessionList
	json.Unmarshal(bob.mustStatus(200, "GET", "/api/v1/me/sessions", nil), &l)
	found := false
	for _, s := range l.Sessions {
		found = found || s.ImpersonatedBy == "admin@x.io"
	}
	if !found {
		t.Fatalf("sessions: %+v", l.Sessions)
	}

	// Changes made while impersonating name the administrator in the activity.
	j.must(200, "PATCH", "/api/v1/bots/"+bobBot, map[string]any{"name": "renamed-by-admin"})
	var label string
	e.db.QueryRow(`SELECT actor_label FROM audit_events WHERE action = 'bot.update' ORDER BY rowid DESC LIMIT 1`).Scan(&label)
	if label != "admin@x.io as bob@x.io" {
		t.Fatalf("audit actor = %q", label)
	}

	// Returning ends the impersonation session and restores the administrator.
	var back struct{ User userDTO }
	json.Unmarshal(j.must(200, "POST", "/api/v1/auth/impersonation/stop", nil), &back)
	if back.User.ID != adminUser.ID || j.me().Impersonator != nil {
		t.Fatalf("stop: %+v", back)
	}
	l = sessionList{}
	json.Unmarshal(bob.mustStatus(200, "GET", "/api/v1/me/sessions", nil), &l)
	for _, s := range l.Sessions {
		if s.ImpersonatedBy != "" {
			t.Fatalf("impersonation session left behind: %+v", s)
		}
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action IN ('admin.impersonate_start','admin.impersonate_stop') AND subject_user_id = ?`, bobUser.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("impersonation audit records = %d", n)
	}
}

func TestImpersonationEndsWhenAdministratorLosesRole(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user("admin@x.io", domain.RoleAdmin)
	root := e.user("root@x.io", domain.RoleAdmin)
	e.user("bob@x.io", domain.RoleUser)
	bobUser, _ := e.db.GetUserByEmail(ctx, "bob@x.io")
	adminUser, _ := e.db.GetUserByEmail(ctx, "admin@x.io")

	j := newJar(e)
	j.login("admin@x.io")
	j.must(200, "POST", "/api/v1/admin/users/"+bobUser.ID+"/impersonate", nil)
	root.mustStatus(204, "PATCH", "/api/v1/users/"+adminUser.ID, map[string]any{"role": "user"})
	j.must(401, "GET", "/api/v1/auth/me", nil)

	// One impersonation per administrator: starting another (here from a
	// second browser) ends the first.
	j2, j3 := newJar(e), newJar(e)
	j2.login("root@x.io")
	j3.login("root@x.io")
	j2.must(200, "POST", "/api/v1/admin/users/"+bobUser.ID+"/impersonate", nil)
	j3.must(200, "POST", "/api/v1/admin/users/"+bobUser.ID+"/impersonate", nil)
	j2.must(401, "GET", "/api/v1/auth/me", nil)
	j3.must(200, "GET", "/api/v1/auth/me", nil)
	// While acting as Bob, starting an impersonation is an administrator action Bob lacks.
	j3.must(403, "POST", "/api/v1/admin/users/"+bobUser.ID+"/impersonate", nil)
	// Signing out while impersonating returns to the administrator.
	var back struct{ User userDTO }
	json.Unmarshal(j3.must(200, "POST", "/api/v1/auth/logout", nil), &back)
	if back.User.Email != "root@x.io" {
		t.Fatalf("logout while impersonating: %+v", back)
	}
}

func TestAccountSwitcher(t *testing.T) {
	e := newEnv(t)
	e.user("a@x.io", domain.RoleUser)
	e.user("b@x.io", domain.RoleUser)
	e.user("c@x.io", domain.RoleUser)

	j := newJar(e)
	j.login("a@x.io")
	// Add another account: park the active one, sign in to the next.
	j.must(204, "POST", "/api/v1/auth/accounts/park", nil)
	if a := j.accounts(); a.Current != nil || len(a.Accounts) != 1 || a.Accounts[0].User.Email != "a@x.io" {
		t.Fatalf("after park: %+v", a)
	}
	j.login("b@x.io")
	j.must(204, "POST", "/api/v1/auth/accounts/park", nil)
	j.login("c@x.io")
	a := j.accounts()
	if a.Current == nil || a.Current.Email != "c@x.io" || len(a.Accounts) != 2 {
		t.Fatalf("accounts: %+v", a)
	}

	// Switching swaps the active account with the chosen one.
	var target string
	for _, x := range a.Accounts {
		if x.User.Email == "a@x.io" {
			target = x.SessionID
		}
	}
	j.must(200, "POST", "/api/v1/auth/accounts/switch", map[string]string{"session_id": target})
	if m := j.me(); m.User.Email != "a@x.io" {
		t.Fatalf("after switch: %+v", m)
	}
	j.must(200, "GET", "/api/v1/bots", nil) // the new CSRF token is in place for changes too
	j.must(400, "POST", "/api/v1/auth/accounts/switch", map[string]string{"session_id": "nope"})

	// Signing out of the active account moves on to the next one.
	var next struct{ User userDTO }
	json.Unmarshal(j.must(200, "POST", "/api/v1/auth/logout", nil), &next)
	if next.User.Email == "" || next.User.Email == "a@x.io" {
		t.Fatalf("logout moved to %+v", next)
	}
	if got := len(j.accounts().Accounts); got != 1 {
		t.Fatalf("stashed after logout = %d", got)
	}

	// Signing out of one stashed account, then of everything.
	j.must(204, "POST", "/api/v1/auth/logout?all=1", nil)
	if a := j.accounts(); a.Current != nil || len(a.Accounts) != 0 {
		t.Fatalf("after sign out of all: %+v", a)
	}
	if _, ok := j.cookies[accountsCookie]; ok {
		t.Fatal("accounts cookie kept after signing out of everything")
	}

	// Signing in to an account that is already stashed keeps one entry.
	j.login("a@x.io")
	j.must(204, "POST", "/api/v1/auth/accounts/park", nil)
	j.login("b@x.io")
	j.must(204, "POST", "/api/v1/auth/accounts/park", nil)
	j.login("a@x.io")
	a = j.accounts()
	if len(a.Accounts) != 1 || a.Accounts[0].User.Email != "b@x.io" {
		t.Fatalf("dedupe: %+v", a)
	}
	j.must(204, "DELETE", "/api/v1/auth/accounts/"+a.Accounts[0].SessionID, nil)
	if len(j.accounts().Accounts) != 0 || !strings.Contains(string(j.must(200, "GET", "/api/v1/auth/me", nil)), "a@x.io") {
		t.Fatal("forget did not drop the account")
	}
}
