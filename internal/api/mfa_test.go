package api

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func mfaEnv(t *testing.T) (*env, *atomicClock) {
	e := newEnv(t)
	clk := &atomicClock{}
	clk.set(time.Unix(1_800_000_000, 0))
	e.auth.Now = clk.now
	m := &service.MFAService{Store: e.db, Keys: e.bots.Keys, Auth: e.auth, Now: clk.now}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, MFA: m, SecureCookies: true})
	return e, clk
}

// passwordStep signs in with a password and returns the second-step cookie.
func passwordStep(t *testing.T, e *env, email string) *http.Cookie {
	t.Helper()
	c := &client{e: e}
	resp, body := c.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": pw})
	var out struct {
		MFARequired bool `json:"mfa_required"`
	}
	json.Unmarshal(body, &out)
	if resp.StatusCode != 200 || !out.MFARequired {
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Fatal("a session was issued before the second step")
		}
		if ck.Name == mfaCookie {
			return ck
		}
	}
	t.Fatal("no second-step cookie")
	return nil
}

func secondStep(e *env, ticket *http.Cookie, code string) (*http.Response, []byte) {
	c := &client{e: e}
	return c.req("POST", "/api/v1/auth/mfa", map[string]string{"code": code}, func(r *http.Request) { r.AddCookie(ticket) })
}

func TestTwoStepSignIn(t *testing.T) {
	e, clk := mfaEnv(t)
	u := e.user("u@x.io", domain.RoleUser)

	// Setup needs the current password.
	u.mustStatus(400, "POST", "/api/v1/me/mfa/setup", map[string]string{"password": "wrong-password-123"})
	var setup struct{ Key, URI string }
	json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/me/mfa/setup", map[string]string{"password": pw}), &setup)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Key)
	if err != nil || len(secret) != 20 {
		t.Fatalf("key %q: %v", setup.Key, err)
	}
	code := func() string { return auth.TOTPCode(secret, auth.TOTPStep(clk.now())) }

	// Not on until confirmed: password sign-in still works directly.
	e.user2Login(t, "u@x.io")
	u.mustStatus(400, "POST", "/api/v1/me/mfa/enable", map[string]string{"code": "000000"})
	var en struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/me/mfa/enable", map[string]string{"code": code()}), &en)
	if len(en.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes: %v", en.RecoveryCodes)
	}

	// Password sign-in now stops at the second step.
	clk.add(31 * time.Second)
	ticket := passwordStep(t, e, "u@x.io")
	if resp, _ := secondStep(e, ticket, "123456"); resp.StatusCode != 400 {
		t.Fatalf("wrong code: %d", resp.StatusCode)
	}
	resp, body := secondStep(e, ticket, code())
	if resp.StatusCode != 200 {
		t.Fatalf("right code: %d %s", resp.StatusCode, body)
	}
	// The same code cannot be used again (replay), even with a new ticket.
	ticket = passwordStep(t, e, "u@x.io")
	if resp, _ := secondStep(e, ticket, code()); resp.StatusCode != 400 {
		t.Fatal("replayed code accepted")
	}
	// A recovery code works once.
	if resp, body := secondStep(e, ticket, en.RecoveryCodes[0]); resp.StatusCode != 200 {
		t.Fatalf("recovery code: %d %s", resp.StatusCode, body)
	}
	ticket = passwordStep(t, e, "u@x.io")
	if resp, _ := secondStep(e, ticket, en.RecoveryCodes[0]); resp.StatusCode != 400 {
		t.Fatal("recovery code reused")
	}
	// Five wrong attempts use up a ticket.
	for i := 0; i < 5; i++ {
		secondStep(e, ticket, "000000")
	}
	if resp, _ := secondStep(e, ticket, en.RecoveryCodes[1]); resp.StatusCode == 200 { // 400, or 429 from the IP limit
		t.Fatal("ticket survived its attempt limit")
	}

	// SFTP with the account password would skip the second step: refused.
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "u@x.io", pw); err == nil {
		t.Fatal("password SFTP allowed with two-step sign-in")
	}

	var st struct {
		Enabled      bool `json:"enabled"`
		RecoveryLeft int  `json:"recovery_left"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/me/mfa", nil), &st)
	if !st.Enabled || st.RecoveryLeft != 9 {
		t.Fatalf("status: %+v", st)
	}
	// Turning it off needs a code.
	u.mustStatus(400, "POST", "/api/v1/me/mfa/disable", map[string]string{"code": "000000"})
	clk.add(31 * time.Second)
	u.mustStatus(204, "POST", "/api/v1/me/mfa/disable", map[string]string{"code": code()})
	e.user2Login(t, "u@x.io")
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "u@x.io", pw); err != nil {
		t.Fatal("password SFTP still refused after disabling")
	}
}

// user2Login asserts a password sign-in completes in one step.
func (e *env) user2Login(t *testing.T, email string) {
	t.Helper()
	c := &client{e: e}
	resp, body := c.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": pw})
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			return
		}
	}
	t.Fatalf("no session: %s", body)
}
