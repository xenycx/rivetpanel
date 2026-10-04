package api

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/service"
)

type verifyClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *verifyClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *verifyClock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func verifyEnv(t *testing.T) (*env, *fakeMailgun, *verifyClock) {
	e := newEnv(t)
	fm := newFakeMailgun(t)
	clk := &verifyClock{t: time.Now()}
	o := &service.OAuthService{Store: e.db, Auth: e.auth, Keys: e.bots.Keys, Providers: map[string]oauth.Provider{}, States: oauth.NewStateStore()}
	st := &service.SettingsService{Store: e.db, Keys: e.bots.Keys, OAuth: o, Auth: e.auth, Env: service.EnvSettings{Production: true}}
	ms := &service.MailService{Settings: st, Recipients: e.db, Client: &mail.Client{BaseURL: fm.srv.URL}}
	vs := &service.EmailVerificationService{Auth: e.auth, Mail: ms, Settings: st, Now: clk.Now}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: o, Settings: st,
		Mail: ms, Verify: vs, MailPrefs: e.db, Catalog: e.bots.Catalog, SecureCookies: true, Audit: &service.Audit{Store: e.db, Bots: e.bots}})
	return e, fm, clk
}

var verifyTokenRe = regexp.MustCompile(`/verify-email#(bpv_[A-Za-z0-9_-]+)`)

func verifyToken(t *testing.T, m map[string]string) string {
	t.Helper()
	got := verifyTokenRe.FindStringSubmatch(m["text"])
	if got == nil {
		t.Fatalf("no verification link in %q", m["text"])
	}
	return got[1]
}

func TestEmailVerificationLink(t *testing.T) {
	e, fm, clk := verifyEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	anon := &client{e: e}

	// Without email the panel says so instead of pretending to send.
	if m := u.me(); m.EmailVerification.Verified || m.User.EmailVerified || m.EmailVerification.Available {
		t.Fatalf("new account: %+v", m)
	}
	u.mustStatus(400, "POST", "/api/v1/me/email/verification", nil)
	configureMail(t, admin, nil)
	fm.none(t)

	u.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
	msg := fm.next(t)
	if msg["to"] != "u@x.io" {
		t.Fatalf("sent to %q", msg["to"])
	}
	tok := verifyToken(t, msg)
	// Resend is rate limited (one a minute).
	u.mustStatus(400, "POST", "/api/v1/me/email/verification", nil)
	if m := u.me(); m.EmailVerification.PendingEmail != "u@x.io" {
		t.Fatalf("pending: %+v", m.EmailVerification)
	}
	// Only the hash is stored.
	var stored []byte
	e.db.QueryRow(`SELECT token_hash FROM email_verifications`).Scan(&stored)
	if string(stored) == tok || string(stored) != string(auth.HashToken(tok)) {
		t.Fatal("token not stored as its hash")
	}

	anon.mustStatus(400, "POST", "/api/v1/auth/email/verify", map[string]string{"token": "bpv_wrong"})
	anon.mustStatus(200, "POST", "/api/v1/auth/email/verify", map[string]string{"token": tok})
	if m := u.me(); !m.EmailVerification.Verified || !m.User.EmailVerified {
		t.Fatalf("not verified: %+v", m)
	}
	// Single use.
	anon.mustStatus(400, "POST", "/api/v1/auth/email/verify", map[string]string{"token": tok})
	u.mustStatus(400, "POST", "/api/v1/me/email/verification", nil) // already verified

	// Links expire.
	v := e.user("v@x.io", domain.RoleUser)
	v.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
	old := verifyToken(t, fm.next(t))
	clk.Advance(25 * time.Hour)
	anon.mustStatus(400, "POST", "/api/v1/auth/email/verify", map[string]string{"token": old})
	// A newer link replaces the older one.
	v.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
	first := verifyToken(t, fm.next(t))
	clk.Advance(2 * time.Minute)
	v.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
	second := verifyToken(t, fm.next(t))
	anon.mustStatus(400, "POST", "/api/v1/auth/email/verify", map[string]string{"token": first})
	anon.mustStatus(200, "POST", "/api/v1/auth/email/verify", map[string]string{"token": second})

	var n int
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'account.email_verified'`).Scan(&n)
	if n != 2 {
		t.Fatalf("verifications recorded: %d", n)
	}
}

func TestUnverifiedPolicyWithholdsPermissions(t *testing.T) {
	e, fm, _ := verifyEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	configureMail(t, admin, nil)

	// Off by default: unverified accounts work as before.
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "one", "runtime": "nodejs"})

	admin.mustStatus(400, "PUT", "/api/v1/admin/settings", map[string]any{"unverified_restrict": []string{"nope"}})
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"unverified_restrict": []string{domain.PermBotsCreate, domain.PermAPIKeys}})
	resp, body := u.do("POST", "/api/v1/bots", map[string]any{"name": "two", "runtime": "nodejs"})
	if resp.StatusCode != 403 || !strings.Contains(string(body), "verify your email") {
		t.Fatalf("unverified create: %d %s", resp.StatusCode, body)
	}
	u.mustStatus(403, "POST", "/api/v1/me/api-keys", map[string]any{"name": "k"})
	if m := u.me(); len(m.EmailVerification.Withheld) != 2 {
		t.Fatalf("withheld: %+v", m.EmailVerification)
	}
	// Administrators are never restricted; what is not listed still works.
	admin.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "adm", "runtime": "nodejs"})
	u.mustStatus(200, "GET", "/api/v1/bots", nil)

	// Verifying lifts the restriction.
	u.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
	(&client{e: e}).mustStatus(200, "POST", "/api/v1/auth/email/verify", map[string]string{"token": verifyToken(t, fm.next(t))})
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "two", "runtime": "nodejs"})

	// An account manager can mark an address verified (or not) by hand.
	w := e.user("w@x.io", domain.RoleUser)
	w.mustStatus(403, "POST", "/api/v1/bots", map[string]any{"name": "w", "runtime": "nodejs"})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("w@x.io"), map[string]any{"email_verified": true})
	w.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "w", "runtime": "nodejs"})

	// Turning the policy off restores everything.
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("w@x.io"), map[string]any{"email_verified": false})
	// Only what the role grants is reported as withheld.
	rid := admin.createRole("Creators", domain.PermBotsCreate, domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("w@x.io"), map[string]any{"role": rid})
	if m := w.me(); len(m.EmailVerification.Withheld) != 1 || m.EmailVerification.Withheld[0] != domain.PermBotsCreate {
		t.Fatalf("withheld for a custom role: %+v", m.EmailVerification)
	}
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("w@x.io"), map[string]any{"role": "user"})
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"unverified_restrict": []string{}})
	w.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "w2", "runtime": "nodejs"})
}

func TestEmailChangeNeedsVerification(t *testing.T) {
	e, fm, _ := verifyEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	e.user("taken@x.io", domain.RoleUser)
	configureMail(t, admin, nil)

	u.mustStatus(400, "POST", "/api/v1/me/email", map[string]string{"email": "new@x.io", "current_password": "wrong-password-1"})
	u.mustStatus(400, "POST", "/api/v1/me/email", map[string]string{"email": "taken@x.io", "current_password": pw})
	u.mustStatus(400, "POST", "/api/v1/me/email", map[string]string{"email": "u@x.io", "current_password": pw})
	u.mustStatus(202, "POST", "/api/v1/me/email", map[string]string{"email": "New@x.io", "current_password": pw})
	var link, notice map[string]string
	for range 2 {
		m := fm.next(t)
		if m["to"] == "new@x.io" {
			link = m
		} else if m["to"] == "u@x.io" {
			notice = m
		}
	}
	if link == nil || notice == nil || !strings.Contains(notice["text"], "new@x.io") {
		t.Fatalf("emails: link=%v notice=%v", link, notice)
	}
	// Nothing changes until the link is used.
	if m := u.me(); m.User.Email != "u@x.io" || m.EmailVerification.PendingEmail != "new@x.io" {
		t.Fatalf("changed early: %+v", m)
	}
	(&client{e: e}).mustStatus(200, "POST", "/api/v1/auth/email/verify", map[string]string{"token": verifyToken(t, link)})
	if m := u.me(); m.User.Email != "new@x.io" || !m.User.EmailVerified {
		t.Fatalf("not changed: %+v", m.User)
	}
	if old := fm.next(t); old["to"] != "u@x.io" {
		t.Fatalf("old address not told: %v", old)
	}
	anon := &client{e: e}
	anon.mustStatus(401, "POST", "/api/v1/auth/login", map[string]string{"email": "u@x.io", "password": pw})
	anon.mustStatus(200, "POST", "/api/v1/auth/login", map[string]string{"email": "new@x.io", "password": pw})

	// An administrator's change makes the address unverified again.
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("new@x.io"), map[string]any{"email": "other@x.io"})
	got, err := e.db.GetUserByID(context.Background(), e.userID("other@x.io"))
	if err != nil || got.EmailVerified {
		t.Fatalf("admin change: %+v %v", got, err)
	}
	admin.mustStatus(400, "PATCH", "/api/v1/users/"+got.ID, map[string]any{"email": "taken@x.io"})
}

// Resends are limited to one a minute (store) and five an hour per account
// (route), so the panel cannot be used to flood a mailbox.
func TestVerificationResendRateLimited(t *testing.T) {
	e, fm, clk := verifyEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	configureMail(t, admin, nil)
	for i := range 5 {
		u.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
		fm.next(t)
		if i < 4 {
			clk.Advance(2 * time.Minute)
		}
	}
	clk.Advance(2 * time.Minute)
	u.mustStatus(429, "POST", "/api/v1/me/email/verification", nil)
	u.mustStatus(429, "POST", "/api/v1/me/email", map[string]string{"email": "n@x.io", "current_password": pw})
	fm.none(t)
	// Another account is not affected.
	v := e.user("v@x.io", domain.RoleUser)
	v.mustStatus(202, "POST", "/api/v1/me/email/verification", nil)
}
