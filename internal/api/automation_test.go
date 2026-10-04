package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func automationEnv(t *testing.T) *env {
	e := newEnv(t)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, SecureCookies: true,
		Tokens: &service.TokenService{Store: e.db, Bots: e.bots}})
	return e
}

func bearer(e *env, tok, method, path string, body any, hdr ...string) (*http.Response, []byte) {
	c := &client{e: e}
	return c.req(method, path, body, func(r *http.Request) {
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	})
}

func mkToken(t *testing.T, c *client, body map[string]any) (string, string) {
	t.Helper()
	var out struct {
		Token string
		Info  tokenDTO
	}
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/me/tokens", body), &out)
	return out.Token, out.Info.ID
}

func TestAutomationTokens(t *testing.T) {
	e := automationEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	other := e.user("other@x.io", domain.RoleUser)
	a, b := owner.createBot("a"), owner.createBot("b")
	theirs := other.createBot("theirs")

	// Validation.
	owner.mustStatus(400, "POST", "/api/v1/me/tokens", map[string]any{"name": "x", "actions": []string{"shell"}, "expires_in_days": 30})
	owner.mustStatus(400, "POST", "/api/v1/me/tokens", map[string]any{"name": "x", "actions": []string{"read"}, "expires_in_days": 0})
	owner.mustStatus(400, "POST", "/api/v1/me/tokens", map[string]any{"name": "x", "actions": []string{"read"}, "expires_in_days": 30, "bot_ids": []string{theirs}})

	// A read-only token scoped to bot a.
	ro, roID := mkToken(t, owner, map[string]any{"name": "status", "actions": []string{"read"}, "bot_ids": []string{a}, "expires_in_days": 30})
	if resp, _ := bearer(e, "", "GET", "/api/v1/automation/bots", nil); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := bearer(e, "bpa_nope", "GET", "/api/v1/automation/bots", nil); resp.StatusCode != 401 {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	resp, body := bearer(e, ro, "GET", "/api/v1/automation/bots", nil)
	var l struct{ Bots []autoBotDTO }
	json.Unmarshal(body, &l)
	if resp.StatusCode != 200 || len(l.Bots) != 1 || l.Bots[0].ID != a || resp.Header.Get("X-Request-ID") == "" {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(e, ro, "GET", "/api/v1/automation/bots/"+b, nil); resp.StatusCode != 404 {
		t.Fatalf("bot outside the token: %d", resp.StatusCode)
	}
	if resp, _ := bearer(e, ro, "POST", "/api/v1/automation/bots/"+a+"/start", nil); resp.StatusCode != 403 {
		t.Fatalf("power without the action: %d", resp.StatusCode)
	}
	// The session cookie does not authenticate the automation API.
	if resp, _ := owner.do("GET", "/api/v1/automation/bots", nil); resp.StatusCode != 401 {
		t.Fatalf("cookie accepted: %d", resp.StatusCode)
	}
	// SFTP keys never gain HTTP access.
	var key struct{ Key string }
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/me/api-keys", map[string]any{"name": "sftp"}), &key)
	if resp, _ := bearer(e, key.Key, "GET", "/api/v1/automation/bots", nil); resp.StatusCode != 401 {
		t.Fatalf("sftp key accepted: %d", resp.StatusCode)
	}

	// A power token for all of the owner's bots; idempotent retries.
	pw, _ := mkToken(t, owner, map[string]any{"name": "ci", "actions": []string{"read", "power"}, "expires_in_days": 7})
	before := e.rec.count()
	r1, b1 := bearer(e, pw, "POST", "/api/v1/automation/bots/"+b+"/restart", nil, "Idempotency-Key", "run-42")
	r2, b2 := bearer(e, pw, "POST", "/api/v1/automation/bots/"+b+"/restart", nil, "Idempotency-Key", "run-42")
	if r1.StatusCode != 202 || r2.StatusCode != 202 || string(b1) != string(b2) || r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("idempotency: %d %d %q", r1.StatusCode, r2.StatusCode, r2.Header.Get("Idempotent-Replayed"))
	}
	if e.rec.count() != before+1 {
		t.Fatalf("retried request ran twice: %d", e.rec.count()-before)
	}
	// Another user's bot is invisible even to an unscoped token.
	if resp, _ := bearer(e, pw, "POST", "/api/v1/automation/bots/"+theirs+"/start", nil); resp.StatusCode != 404 {
		t.Fatalf("other user's bot: %d", resp.StatusCode)
	}
	// Tokens follow the owner's current permissions: a sub-user token loses
	// power when the grant does.
	owner.mustStatus(200, "PUT", "/api/v1/bots/"+a+"/users", map[string]any{"email": "other@x.io", "permissions": domain.PermPower})
	sub, _ := mkToken(t, other, map[string]any{"name": "sub", "actions": []string{"power"}, "bot_ids": []string{a}, "expires_in_days": 7})
	if resp, _ := bearer(e, sub, "POST", "/api/v1/automation/bots/"+a+"/stop", nil); resp.StatusCode != 202 {
		t.Fatalf("sub-user power: %d", resp.StatusCode)
	}
	owner.mustStatus(200, "PUT", "/api/v1/bots/"+a+"/users", map[string]any{"email": "other@x.io", "permissions": domain.PermViewConsole})
	if resp, _ := bearer(e, sub, "POST", "/api/v1/automation/bots/"+a+"/stop", nil); resp.StatusCode != 403 {
		t.Fatalf("revoked grant: %d", resp.StatusCode)
	}

	// Revocation is immediate.
	owner.mustStatus(204, "DELETE", "/api/v1/me/tokens/"+roID, nil)
	if resp, _ := bearer(e, ro, "GET", "/api/v1/automation/bots", nil); resp.StatusCode != 401 {
		t.Fatalf("revoked token: %d", resp.StatusCode)
	}
	var tl struct{ Tokens []tokenDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/me/tokens", nil), &tl)
	if len(tl.Tokens) != 1 || tl.Tokens[0].LastUsedAtMS == nil {
		t.Fatalf("tokens: %+v", tl.Tokens)
	}
	if resp, body := bearer(e, "", "GET", "/api/v1/automation/openapi.yaml", nil); resp.StatusCode != 200 || len(body) < 1000 {
		t.Fatalf("openapi: %d", resp.StatusCode)
	}
}
