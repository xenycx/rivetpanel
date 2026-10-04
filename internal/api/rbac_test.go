package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestSubUserPermissions(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	sub := e.user("sub@x.io", domain.RoleUser)
	stranger := e.user("stranger@x.io", domain.RoleUser)
	id := owner.createBot("b")
	bot := "/api/v1/bots/" + id

	// No grant: indistinguishable from nonexistent.
	sub.mustStatus(404, "GET", bot, nil)
	stranger.mustStatus(404, "GET", bot, nil)

	// Only the owner may share; unknown email is a validation error.
	sub.mustStatus(404, "PUT", bot+"/users", map[string]any{"email": "stranger@x.io", "permissions": 1})
	owner.mustStatus(400, "PUT", bot+"/users", map[string]any{"email": "nobody@x.io", "permissions": 1})
	owner.mustStatus(400, "PUT", bot+"/users", map[string]any{"email": "sub@x.io", "permissions": 0})
	owner.mustStatus(400, "PUT", bot+"/users", map[string]any{"email": "owner@x.io", "permissions": 1})
	owner.mustStatus(200, "PUT", bot+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermEditFiles})

	// The grant is visible, listed and scoped: files yes, env/power/config no.
	var got botDTO
	json.Unmarshal(sub.mustStatus(200, "GET", bot, nil), &got)
	if !got.Shared || got.Permissions != domain.PermEditFiles {
		t.Fatalf("view: %+v", got)
	}
	sub.mustStatus(200, "GET", bot+"/files?path=.", nil)
	sub.mustStatus(403, "GET", bot+"/env", nil)
	sub.mustStatus(403, "POST", bot+"/start", nil)
	sub.mustStatus(403, "PATCH", bot, map[string]any{"name": "x"})
	sub.mustStatus(403, "DELETE", bot, nil)
	sub.mustStatus(403, "GET", bot+"/users", nil)
	stranger.mustStatus(404, "GET", bot+"/files?path=.", nil)

	var list struct{ Bots []botDTO }
	json.Unmarshal(sub.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 1 || list.Bots[0].ID != id {
		t.Fatalf("shared bot missing from list: %+v", list.Bots)
	}
	json.Unmarshal(stranger.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 0 {
		t.Fatal("stranger sees the bot")
	}

	// Env permission unlocks env only; full admin implies all but not deletion.
	owner.mustStatus(200, "PUT", bot+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermManageEnv})
	sub.mustStatus(200, "GET", bot+"/env", nil)
	sub.mustStatus(403, "GET", bot+"/files?path=.", nil)
	owner.mustStatus(200, "PUT", bot+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermFullAdmin})
	sub.mustStatus(200, "GET", bot+"/files?path=.", nil)
	sub.mustStatus(200, "PATCH", bot, map[string]any{"name": "renamed"})
	sub.mustStatus(403, "DELETE", bot, nil)

	// Revocation: the owner can remove; a sub-user can leave; others cannot.
	stranger.mustStatus(404, "DELETE", bot+"/users/x", nil)
	var users struct{ Users []subUserDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", bot+"/users", nil), &users)
	if len(users.Users) != 1 {
		t.Fatal(users)
	}
	sub.mustStatus(204, "DELETE", bot+"/users/"+users.Users[0].UserID, nil)
	sub.mustStatus(404, "GET", bot, nil)
}

func TestAPIKeysLifecycle(t *testing.T) {
	e := newEnv(t)
	u := e.user("k@x.io", domain.RoleUser)
	other := e.user("o@x.io", domain.RoleUser)
	var made struct {
		Key  string
		Info apiKeyDTO
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/me/api-keys", map[string]any{"name": "laptop", "expires_in_days": 30}), &made)
	if len(made.Key) < 40 || made.Key[:4] != "bpk_" || made.Info.ExpiresAtMS == nil {
		t.Fatalf("%+v", made)
	}
	// The plaintext is never stored or listed again.
	body := string(u.mustStatus(200, "GET", "/api/v1/me/api-keys", nil))
	if strings.Contains(body, made.Key) || !strings.Contains(body, made.Info.Prefix) {
		t.Fatal("list leaks or omits key: " + body)
	}
	// It authenticates only for its own account.
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "k@x.io", made.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "o@x.io", made.Key); err == nil {
		t.Fatal("key accepted for another account")
	}
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "k@x.io", pw); err != nil {
		t.Fatal("password must also work:", err)
	}
	other.mustStatus(404, "DELETE", "/api/v1/me/api-keys/"+made.Info.ID, nil)
	u.mustStatus(204, "DELETE", "/api/v1/me/api-keys/"+made.Info.ID, nil)
	if _, err := e.auth.AuthenticateSFTP(context.Background(), "k@x.io", made.Key); err == nil {
		t.Fatal("deleted key still works")
	}
	u.mustStatus(400, "POST", "/api/v1/me/api-keys", map[string]any{"name": ""})
	body = string(u.mustStatus(200, "GET", "/api/v1/me/sftp", nil))
	if strings.TrimSpace(body) != `{"enabled":false}` {
		t.Fatal(body)
	}
}

func TestRevealEnv(t *testing.T) {
	e := newEnv(t)
	owner := e.user("o@x.io", domain.RoleUser)
	files := e.user("f@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "f@x.io", "permissions": domain.PermEditFiles})
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": "s3cr3t"}})
	// Listing stays masked; reveal is explicit, per variable and permission-gated.
	if strings.Contains(string(owner.mustStatus(200, "GET", base+"/env", nil)), "s3cr3t") {
		t.Fatal("listing leaked the value")
	}
	resp, body := owner.do("POST", base+"/env/DISCORD_TOKEN/reveal", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "s3cr3t") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s %v", resp.StatusCode, body, resp.Header)
	}
	owner.mustStatus(404, "POST", base+"/env/NOPE/reveal", nil)
	files.mustStatus(403, "POST", base+"/env/DISCORD_TOKEN/reveal", nil)
	if resp, _ := owner.req("POST", base+"/env/DISCORD_TOKEN/reveal", nil); resp.StatusCode != 403 {
		t.Fatalf("reveal without CSRF: %d", resp.StatusCode)
	}
}

func TestOwnershipTransfer(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	id := alice.createBot("b")
	base := "/api/v1/bots/" + id
	alice.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "bob@x.io", "permissions": domain.PermViewConsole})
	bob.mustStatus(403, "POST", base+"/transfer", map[string]any{"email": "bob@x.io"}) // only the owner may
	alice.mustStatus(400, "POST", base+"/transfer", map[string]any{"email": "nobody@x.io"})
	alice.mustStatus(200, "POST", base+"/transfer", map[string]any{"email": "bob@x.io", "keep_access": true})

	var b botDTO
	json.Unmarshal(bob.mustStatus(200, "GET", base, nil), &b)
	if b.Shared || b.Permissions != domain.PermAll {
		t.Fatalf("new owner view: %+v", b)
	}
	json.Unmarshal(alice.mustStatus(200, "GET", base, nil), &b)
	if !b.Shared || b.Permissions != domain.PermAll {
		t.Fatalf("previous owner kept access: %+v", b)
	}
	alice.mustStatus(403, "DELETE", base, nil) // full-admin sub-users still cannot delete
	bob.mustStatus(200, "POST", base+"/transfer", map[string]any{"email": "alice@x.io"})
	var n int
	e.db.QueryRow(`SELECT count(*) FROM bot_subusers WHERE bot_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("sub-user rows after transferring back without keeping access: %d", n)
	}
}
