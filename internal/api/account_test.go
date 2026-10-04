package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type sessionList struct {
	Sessions []sessionDTO `json:"sessions"`
}

// loginAgain signs the same account in from another "browser".
func (e *env) loginAgain(email, password, ua string) *client {
	e.t.Helper()
	c := &client{e: e}
	resp, body := c.req("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password}, func(r *http.Request) {
		r.Header.Set("User-Agent", ua)
	})
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

func TestSessionsAndPasswordChange(t *testing.T) {
	e := newEnv(t)
	a := e.user("a@x.io", domain.RoleUser)
	b := e.loginAgain("a@x.io", pw, "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")

	var l sessionList
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/me/sessions", nil), &l)
	if len(l.Sessions) != 2 {
		t.Fatalf("sessions = %d", len(l.Sessions))
	}
	var current, other sessionDTO
	for _, s := range l.Sessions {
		if s.Current {
			current = s
		} else {
			other = s
		}
	}
	if current.ID == "" || other.Device != "Firefox on Linux" {
		t.Fatalf("listing: %+v", l.Sessions)
	}

	// Revoking one session signs that browser out.
	a.mustStatus(204, "DELETE", "/api/v1/me/sessions/"+other.ID, nil)
	b.mustStatus(401, "GET", "/api/v1/auth/me", nil)
	a.mustStatus(404, "DELETE", "/api/v1/me/sessions/"+other.ID, nil)

	// Changing the password needs the current one and signs out other sessions.
	c := e.loginAgain("a@x.io", pw, "curl/8")
	a.mustStatus(400, "POST", "/api/v1/me/password", map[string]string{"current_password": "wrong-password-xx", "new_password": "a-brand-new-passphrase"})
	a.mustStatus(400, "POST", "/api/v1/me/password", map[string]string{"current_password": pw, "new_password": "short"})
	a.mustStatus(204, "POST", "/api/v1/me/password", map[string]string{"current_password": pw, "new_password": "a-brand-new-passphrase"})
	c.mustStatus(401, "GET", "/api/v1/auth/me", nil)
	a.mustStatus(200, "GET", "/api/v1/auth/me", nil) // the session that changed it stays
	e.loginAgain("a@x.io", "a-brand-new-passphrase", "")

	// Revoke all others.
	d := e.loginAgain("a@x.io", "a-brand-new-passphrase", "")
	var r struct{ Revoked int }
	json.Unmarshal(a.mustStatus(200, "POST", "/api/v1/me/sessions/revoke-others", nil), &r)
	if r.Revoked < 2 {
		t.Fatalf("revoked = %d", r.Revoked)
	}
	d.mustStatus(401, "GET", "/api/v1/auth/me", nil)
}

func TestSessionCapDropsOldest(t *testing.T) {
	e := newEnv(t)
	a := e.user("a@x.io", domain.RoleUser)
	u, err := e.db.GetUserByEmail(context.Background(), "a@x.io")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		if _, err := e.auth.IssueSession(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	a.mustStatus(401, "GET", "/api/v1/auth/me", nil) // the oldest (first) session was dropped
	var n int
	e.db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&n)
	if n != 20 {
		t.Fatalf("sessions = %d, want the cap of 20", n)
	}
}

func TestSFTPLoginCheckFollowsCredentialChanges(t *testing.T) {
	e := newEnv(t)
	a := e.user("a@x.io", domain.RoleUser)
	admin := e.user("root@x.io", domain.RoleAdmin)
	ctx := context.Background()

	// Password login: invalid after the password changes.
	_, check, err := e.auth.LoginSFTP(ctx, "a@x.io", pw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := check(ctx); err != nil {
		t.Fatalf("fresh login: %v", err)
	}
	a.mustStatus(204, "POST", "/api/v1/me/password", map[string]string{"current_password": pw, "new_password": "another-long-passphrase"})
	if _, err := check(ctx); err == nil {
		t.Fatal("password login still valid after the password changed")
	}

	// Key login: invalid after the key is deleted, and after the account is disabled.
	var k struct{ ID, Key string }
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/me/api-keys", map[string]any{"name": "laptop", "expires_in_days": 1}), &k)
	_, kcheck, err := e.auth.LoginSFTP(ctx, "a@x.io", k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kcheck(ctx); err != nil {
		t.Fatal(err)
	}
	var list struct{ Keys []struct{ ID string } }
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/me/api-keys", nil), &list)
	a.mustStatus(204, "DELETE", "/api/v1/me/api-keys/"+list.Keys[0].ID, nil)
	if _, err := kcheck(ctx); err == nil {
		t.Fatal("key login still valid after the key was deleted")
	}
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/me/api-keys", map[string]any{"name": "laptop2", "expires_in_days": 1}), &k)
	_, kcheck, _ = e.auth.LoginSFTP(ctx, "a@x.io", k.Key)
	u, _ := e.db.GetUserByEmail(ctx, "a@x.io")
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+u.ID, map[string]any{"disabled": true})
	if _, err := kcheck(ctx); err == nil {
		t.Fatal("key login still valid after the account was disabled")
	}
}

func TestLastAdministratorIsProtected(t *testing.T) {
	e := newEnv(t)
	root := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	ctx := context.Background()
	rootU, _ := e.db.GetUserByEmail(ctx, "root@x.io")
	userU, _ := e.db.GetUserByEmail(ctx, "u@x.io")

	root.mustStatus(400, "PATCH", "/api/v1/users/"+rootU.ID, map[string]any{"role": "user"})
	u.mustStatus(403, "PATCH", "/api/v1/users/"+userU.ID, map[string]any{"role": "admin"})
	root.mustStatus(204, "PATCH", "/api/v1/users/"+userU.ID, map[string]any{"role": "admin"})
	root.mustStatus(204, "PATCH", "/api/v1/users/"+rootU.ID, map[string]any{"role": "user"}) // another admin exists now
	// The remaining administrator cannot be disabled or demoted.
	u.mustStatus(400, "PATCH", "/api/v1/users/"+userU.ID, map[string]any{"role": "user"})

	// Offboarding stops the account's bots when asked.
	id := root.createBot("b")
	root.mustStatus(202, "POST", "/api/v1/bots/"+id+"/start", nil)
	u.mustStatus(204, "PATCH", "/api/v1/users/"+rootU.ID, map[string]any{"disabled": true, "stop_bots": true})
	var desired string
	e.db.QueryRow(`SELECT desired_state FROM bots WHERE id = ?`, id).Scan(&desired)
	if desired != "stopped" {
		t.Fatalf("offboarding left the bot %s", desired)
	}
}
