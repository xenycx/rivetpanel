package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeMailgun records the messages the panel sends and answers the two
// endpoints it uses.
type fakeMailgun struct {
	srv   *httptest.Server
	sent  chan map[string]string
	key   string
	calls []string
}

func newFakeMailgun(t *testing.T) *fakeMailgun {
	f := &fakeMailgun{sent: make(chan map[string]string, 16)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if _, p, _ := r.BasicAuth(); p != "key-good" {
			w.WriteHeader(401)
			io.WriteString(w, `{"message":"Invalid private key"}`)
			return
		}
		f.key = "key-good"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v4/domains/mg.example.com":
			io.WriteString(w, `{"domain":{"name":"mg.example.com","state":"active","type":"custom"},"sending_dns_records":[{"valid":"valid"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v3/mg.example.com/messages":
			r.ParseMultipartForm(1 << 20)
			m := map[string]string{}
			for k, v := range r.MultipartForm.Value {
				m[k] = v[0]
			}
			f.sent <- m
			io.WriteString(w, `{"id":"<1@mg.example.com>","message":"Queued. Thank you."}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMailgun) next(t *testing.T) map[string]string {
	t.Helper()
	select {
	case m := <-f.sent:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no email was sent")
		return nil
	}
}

func (f *fakeMailgun) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-f.sent:
		t.Fatalf("unexpected email: %v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

func mailEnv(t *testing.T) (*env, *fakeMailgun, *service.MailService) {
	e := newEnv(t)
	fm := newFakeMailgun(t)
	o := &service.OAuthService{Store: e.db, Auth: e.auth, Keys: e.bots.Keys, Providers: map[string]oauth.Provider{}, States: oauth.NewStateStore()}
	st := &service.SettingsService{Store: e.db, Keys: e.bots.Keys, OAuth: o, Auth: e.auth, Env: service.EnvSettings{Production: true}}
	ms := &service.MailService{Settings: st, Recipients: e.db, Client: &mail.Client{BaseURL: fm.srv.URL}}
	rs := &service.PasswordResetService{Auth: e.auth, Store: e.db, Mail: ms, Settings: st}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, OAuth: o, Settings: st,
		Mail: ms, Resets: rs, MailPrefs: e.db, SecureCookies: true})
	return e, fm, ms
}

func configureMail(t *testing.T, admin *client, extra map[string]any) {
	t.Helper()
	body := map[string]any{"mailgun_api_key": "key-good", "mailgun_domain": "mg.example.com", "mailgun_region": "eu",
		"mail_from": "RivetPanel <noreply@mg.example.com>", "public_url": "https://panel.example.com"}
	for k, v := range extra {
		body[k] = v
	}
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", body)
}

func TestMailSettingsKeepTheKeySecret(t *testing.T) {
	e, _, _ := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	raw := admin.mustStatus(200, "GET", "/api/v1/admin/settings", nil)
	var v map[string]any
	json.Unmarshal(raw, &v)
	if v["mail_enabled"] != false {
		t.Fatalf("mail must start off: %s", raw)
	}
	configureMail(t, admin, nil)
	raw = admin.mustStatus(200, "GET", "/api/v1/admin/settings", nil)
	json.Unmarshal(raw, &v)
	if v["mail_enabled"] != true || v["mailgun_key_set"] != true || v["mailgun_region"] != "eu" || strings.Contains(string(raw), "key-good") {
		t.Fatalf("settings view: %s", raw)
	}
	// The key is sealed in the database, not stored as text.
	st, _ := e.db.Settings(context.Background())
	if s := st[service.SetMailKey]; len(s.Cipher) == 0 || s.Value != "" || strings.Contains(string(s.Cipher), "key-good") {
		t.Fatalf("key not sealed: %+v", s)
	}
	for _, bad := range []map[string]any{{"mailgun_domain": "https://x.example.com/path"}, {"mail_from": "a@b.co\r\nBcc: c@d.ef"}, {"mailgun_region": "asia"}, {"mailgun_api_key": "has space"}} {
		admin.mustStatus(400, "PUT", "/api/v1/admin/settings", bad)
	}
	// Blank key turns mail off again.
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"mailgun_api_key": ""})
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/settings", nil), &v)
	if v["mail_enabled"] != false {
		t.Fatal("mail still enabled without a key")
	}
	u := e.user("u@x.io", domain.RoleUser)
	if resp, _ := u.do("POST", "/api/v1/admin/settings/mail/test", map[string]string{"to": "u@x.io"}); resp.StatusCode != 403 {
		t.Fatalf("user sent a test email: %d", resp.StatusCode)
	}
}

func TestAdminTestEmail(t *testing.T) {
	e, fm, _ := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	admin.mustStatus(400, "POST", "/api/v1/admin/settings/mail/test", map[string]string{"to": "a@x.io"}) // not configured
	configureMail(t, admin, nil)
	raw := admin.mustStatus(200, "POST", "/api/v1/admin/settings/mail/test", map[string]string{"to": "dest@example.com"})
	var res service.TestResult
	json.Unmarshal(raw, &res)
	if res.MessageID == "" || res.State != "active" || !res.Verified || res.Sandbox {
		t.Fatalf("result: %s", raw)
	}
	m := fm.next(t)
	if m["to"] != "dest@example.com" || m["from"] != "RivetPanel <noreply@mg.example.com>" || !strings.Contains(m["subject"], "test") {
		t.Fatalf("message: %v", m)
	}
	// A wrong key is explained, not leaked.
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"mailgun_api_key": "key-wrong"})
	resp, body := admin.do("POST", "/api/v1/admin/settings/mail/test", map[string]string{"to": "dest@example.com"})
	if resp.StatusCode != 400 || !strings.Contains(string(body), "rejected the API key") || strings.Contains(string(body), "key-wrong") {
		t.Fatalf("bad key: %d %s", resp.StatusCode, body)
	}
}

var resetLink = regexp.MustCompile(`https://panel\.example\.com/reset#(bpr_[A-Za-z0-9_-]+)`)

func TestPasswordResetByEmail(t *testing.T) {
	e, fm, _ := mailEnv(t)
	anon := &client{e: e}
	admin := e.user("a@x.io", domain.RoleAdmin)
	user := e.user("u@x.io", domain.RoleUser)

	var av struct{ Available bool }
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/auth/password-reset", nil), &av)
	if av.Available {
		t.Fatal("reset offered before email is set up")
	}
	configureMail(t, admin, nil)
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/auth/password-reset", nil), &av)
	if !av.Available {
		t.Fatal("reset not offered once email is set up")
	}

	// Unknown and malformed addresses get the same answer and no email.
	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/request", map[string]string{"email": "nobody@x.io"})
	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/request", map[string]string{"email": "not an email"})
	fm.none(t)

	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/request", map[string]string{"email": "U@X.io"})
	m := fm.next(t)
	match := resetLink.FindStringSubmatch(m["text"])
	if m["to"] != "u@x.io" || match == nil || !strings.Contains(m["html"], "Choose a new password") {
		t.Fatalf("reset email: %v", m)
	}
	tok := match[1]
	// Asking again right away does not send a second email.
	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/request", map[string]string{"email": "u@x.io"})
	fm.none(t)

	// A weak password does not use the link up; a wrong token does nothing.
	anon.mustStatus(400, "POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": tok, "password": "short"})
	anon.mustStatus(400, "POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": "bpr_nope", "password": "a-new-strong-password"})
	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": tok, "password": "a-new-strong-password"})
	if n := fm.next(t); n["to"] != "u@x.io" || !strings.Contains(n["text"], "reset link") {
		t.Fatalf("expected a security notice, got %v", n)
	}
	// The link works once.
	anon.mustStatus(400, "POST", "/api/v1/auth/password-reset/confirm", map[string]string{"token": tok, "password": "another-strong-password"})
	// Every old session is gone, the old password no longer works, the new one does.
	if resp, _ := user.do("GET", "/api/v1/auth/me", nil); resp.StatusCode != 401 {
		t.Fatalf("old session survived the reset: %d", resp.StatusCode)
	}
	anon.mustStatus(401, "POST", "/api/v1/auth/login", map[string]string{"email": "u@x.io", "password": pw})
	anon.mustStatus(200, "POST", "/api/v1/auth/login", map[string]string{"email": "u@x.io", "password": "a-new-strong-password"})
}

func TestPasswordResetNeedsPublicAddressAndActiveAccount(t *testing.T) {
	e, fm, _ := mailEnv(t)
	anon := &client{e: e}
	admin := e.user("a@x.io", domain.RoleAdmin)
	e.user("u@x.io", domain.RoleUser)
	configureMail(t, admin, nil)
	// Disabled accounts get no link.
	var users struct{ Users []struct{ ID, Email string } }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/users", nil), &users)
	for _, u := range users.Users {
		if u.Email == "u@x.io" {
			admin.mustStatus(204, "PATCH", "/api/v1/users/"+u.ID, map[string]any{"disabled": true})
		}
	}
	anon.mustStatus(204, "POST", "/api/v1/auth/password-reset/request", map[string]string{"email": "u@x.io"})
	fm.none(t)
	// Without the panel address the link could not be built.
	admin.mustStatus(200, "PUT", "/api/v1/admin/settings", map[string]any{"public_url": ""})
	var av struct{ Available bool }
	json.Unmarshal(anon.mustStatus(200, "GET", "/api/v1/auth/password-reset", nil), &av)
	if av.Available {
		t.Fatal("reset offered without a panel address")
	}
}

func TestInvitationEmail(t *testing.T) {
	e, fm, _ := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	// Before email is set up the invite is still created and the link returned.
	var out struct {
		Token      string
		Emailed    bool   `json:"emailed"`
		EmailError string `json:"email_error"`
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "new@x.io", "role": "user", "expires_in_days": 3, "send_email": true}), &out)
	if out.Token == "" || out.Emailed || out.EmailError == "" {
		t.Fatalf("invite without mail: %+v", out)
	}
	configureMail(t, admin, nil)
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "new@x.io", "role": "user", "expires_in_days": 3, "send_email": true}), &out)
	m := fm.next(t)
	if !out.Emailed || m["to"] != "new@x.io" || !strings.Contains(m["text"], "https://panel.example.com/register#"+out.Token) {
		t.Fatalf("invite email: %+v %v", out, m)
	}
	// An invite without an address cannot be mailed but still works.
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"role": "user", "expires_in_days": 3, "send_email": true}), &out)
	if out.Token == "" || out.Emailed || out.EmailError == "" {
		t.Fatalf("invite without address: %+v", out)
	}
	fm.none(t)
	// No email unless asked.
	admin.mustStatus(201, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "q@x.io", "role": "user", "expires_in_days": 3})
	fm.none(t)
}

func TestSecurityNoticesAndAlertSwitch(t *testing.T) {
	e, fm, ms := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	configureMail(t, admin, nil)
	u := e.user("u@x.io", domain.RoleUser)

	u.mustStatus(204, "POST", "/api/v1/me/password", map[string]string{"current_password": pw, "new_password": "a-new-strong-password"})
	if m := fm.next(t); m["to"] != "u@x.io" || !strings.Contains(m["text"], "password") {
		t.Fatalf("password notice: %v", m)
	}

	var me struct {
		EmailAlerts bool `json:"email_alerts"`
		Features    struct{ Mail bool }
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	if !me.EmailAlerts || !me.Features.Mail {
		t.Fatalf("me: %+v", me)
	}
	u.mustStatus(200, "PUT", "/api/v1/me/email-alerts", map[string]bool{"enabled": false})
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	if me.EmailAlerts {
		t.Fatal("switch not saved")
	}

	// Alerts follow the switch; an alert email carries the title and message.
	alerts := &service.AlertService{Store: e.db, Keys: e.bots.Keys, Mail: ms, Users: e.db}
	owner, _ := e.db.GetUserByEmail(context.Background(), "u@x.io")
	if err := alerts.SendErr(context.Background(), owner.ID, "⚠️ bot needs attention", "it crashed"); err == nil {
		t.Fatal("alert reported delivered with the switch off")
	}
	fm.none(t)
	u.mustStatus(200, "PUT", "/api/v1/me/email-alerts", map[string]bool{"enabled": true})
	if err := alerts.SendErr(context.Background(), owner.ID, "⚠️ bot needs attention", "it crashed"); err != nil {
		t.Fatal(err)
	}
	if m := fm.next(t); m["to"] != "u@x.io" || m["subject"] != "bot needs attention" || !strings.Contains(m["text"], "it crashed") {
		t.Fatalf("alert email: %v", m)
	}
}

func TestAnnouncements(t *testing.T) {
	e, fm, ms := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	u1 := e.user("u1@x.io", domain.RoleUser)
	e.user("u2@x.io", domain.RoleUser)
	configureMail(t, admin, nil)
	u1.mustStatus(200, "PUT", "/api/v1/me/email-news", map[string]bool{"enabled": false})

	var aud struct{ Counts map[string]int }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/mail/audience", nil), &aud)
	if aud.Counts["all_notice"] != 3 || aud.Counts["all_news"] != 2 || aud.Counts["admins_notice"] != 1 {
		t.Fatalf("audience: %v", aud.Counts)
	}
	html := `<h2>Terms</h2><p>We changed things.</p><script>alert(1)</script><a href="javascript:x()" onclick="y()">go</a>`
	body := func(m map[string]any) map[string]any {
		out := map[string]any{"subject": "Policy update", "html": html, "audience": "all", "kind": "notice"}
		for k, v := range m {
			out[k] = v
		}
		return out
	}

	// Only administrators can use it.
	if resp, _ := u1.do("POST", "/api/v1/admin/mail/announcements", body(nil)); resp.StatusCode != 403 {
		t.Fatalf("user sent an announcement: %d", resp.StatusCode)
	}
	if resp, _ := u1.do("GET", "/api/v1/admin/mail/audience", nil); resp.StatusCode != 403 {
		t.Fatal("user read the audience")
	}
	// Bad input is refused before anything is sent.
	for _, bad := range []map[string]any{{"subject": ""}, {"html": "<p> </p>"}, {"audience": "everyone"}, {"kind": "spam"}} {
		admin.mustStatus(400, "POST", "/api/v1/admin/mail/announcements", body(bad))
	}
	fm.none(t)

	// A test goes to the sender only, marked as a test.
	var res service.BroadcastResult
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/mail/announcements", body(map[string]any{"test": true})), &res)
	m := fm.next(t)
	if res.Sent != 1 || m["to"] != "a@x.io" || m["subject"] != "[Test] Policy update" || strings.Contains(m["html"], "<script") || strings.Contains(m["html"], "onclick") || !strings.Contains(m["text"], "We changed things.") {
		t.Fatalf("test send: %+v %v", res, m)
	}

	// A real notice reaches everyone, in one batch where nobody sees the others.
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/mail/announcements", body(nil)), &res)
	m = fm.next(t)
	if res.Recipients != 3 || res.Sent != 3 || res.Failed != 0 || m["subject"] != "Policy update" ||
		m["to"] != "a@x.io,u1@x.io,u2@x.io" || !strings.Contains(m["recipient-variables"], "u2@x.io") || !strings.Contains(m["html"], "<h2>Terms</h2>") {
		t.Fatalf("notice: %+v %v", res, m)
	}
	// A second one right away is refused (double click), then allowed.
	admin.mustStatus(400, "POST", "/api/v1/admin/mail/announcements", body(nil))
	ms.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/mail/announcements", body(map[string]any{"kind": "news"})), &res)
	m = fm.next(t)
	if res.Recipients != 2 || res.Skipped != 1 || strings.Contains(m["to"], "u1@x.io") || !strings.Contains(m["text"], "turn news emails off") {
		t.Fatalf("news: %+v %v", res, m)
	}
	ms.Now = func() time.Time { return time.Now().Add(4 * time.Minute) }
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/mail/announcements", body(map[string]any{"audience": "admins"})), &res)
	if m = fm.next(t); res.Recipients != 1 || m["to"] != "a@x.io" {
		t.Fatalf("admins: %+v %v", res, m)
	}
	var me struct {
		EmailNews bool `json:"email_news"`
	}
	json.Unmarshal(u1.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	if me.EmailNews {
		t.Fatal("news switch not saved")
	}
}
