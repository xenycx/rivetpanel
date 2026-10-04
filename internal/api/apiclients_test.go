package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/console"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func clientEnv(t *testing.T) *env {
	e := newEnv(t)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true, ConsoleLimit: e.consoleLimit,
		Console: &console.Service{Src: e.src, Bus: e.bus, Opts: console.Options{AccessRecheck: 50 * time.Millisecond, StatusPoll: 20 * time.Millisecond}},
		Clients: &service.APIClientService{Store: e.db, Bots: e.bots}})
	return e
}

func mkClient(t *testing.T, c *client, body map[string]any) (string, apiClientDTO) {
	t.Helper()
	var out struct {
		Token string
		Info  apiClientDTO
	}
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/me/api-clients", body), &out)
	if !strings.HasPrefix(out.Token, service.APIClientPrefix) {
		t.Fatalf("token: %q", out.Token)
	}
	return out.Token, out.Info
}

func wantStatus(t *testing.T, e *env, tok string, want int, method, path string, body any) []byte {
	t.Helper()
	resp, b := bearer(e, tok, method, path, body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s with client: got %d want %d: %s", method, path, resp.StatusCode, want, b)
	}
	return b
}

// A client never carries a permission its creator lacks, follows the
// creator's current role, never acts as an administrator and cannot manage
// credentials or the account.
func TestAPIClientPermissionsNoEscalation(t *testing.T) {
	e := clientEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	op := e.user("op@x.io", domain.RoleUser)
	mine := op.createBot("mine")
	rid := admin.createRole("Ops", domain.PermBotsConsole, domain.PermBotsPower, domain.PermAPIKeys)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("op@x.io"), map[string]any{"role": rid})

	// Escalation is refused: a resource permission the role lacks, an
	// administration permission, an unknown name, nothing at all.
	for _, perms := range [][]string{{domain.PermBotsFiles}, {domain.PermUsersView}, {"root"}, {}} {
		op.mustStatus(400, "POST", "/api/v1/me/api-clients", map[string]any{"name": "x", "permissions": perms})
	}
	// Only what the role grants is offered.
	var l struct {
		Grantable []domain.PermissionInfo
	}
	json.Unmarshal(op.mustStatus(200, "GET", "/api/v1/me/api-clients", nil), &l)
	if len(l.Grantable) != 3 {
		t.Fatalf("grantable: %+v", l.Grantable)
	}

	tok, info := mkClient(t, op, map[string]any{"name": "ci", "permissions": []string{domain.PermBotsPower}, "expires_in_days": 30})
	if info.ExpiresAtMS == nil || info.BotIDs != nil {
		t.Fatalf("info: %+v", info)
	}
	// Hashed at rest.
	var n int
	e.db.QueryRow(`SELECT count(*) FROM api_clients WHERE token_hash = ?`, []byte(tok)).Scan(&n)
	if n != 0 {
		t.Fatal("raw token stored")
	}
	// The whole API works with the bearer token, no cookie or CSRF token.
	wantStatus(t, e, tok, 200, "GET", "/api/v1/bots/"+mine, nil)
	wantStatus(t, e, tok, 202, "POST", "/api/v1/bots/"+mine+"/start", nil)
	// Not carried: console (the role has it, the client does not).
	b := wantStatus(t, e, tok, 403, "GET", "/api/v1/bots/"+mine+"/console", nil)
	if !strings.Contains(string(b), "API client does not carry") {
		t.Fatalf("message: %s", b)
	}
	// The role loses power: so does the client, on the next request.
	admin.mustStatus(200, "PATCH", "/api/v1/admin/roles/"+rid, map[string]any{"name": "Ops", "permissions": []string{domain.PermBotsConsole, domain.PermAPIKeys}})
	wantStatus(t, e, tok, 403, "POST", "/api/v1/bots/"+mine+"/stop", nil)

	// Credentials and the account are session-only.
	wantStatus(t, e, tok, 403, "POST", "/api/v1/me/api-clients", map[string]any{"name": "y", "permissions": []string{domain.PermBotsPower}})
	wantStatus(t, e, tok, 403, "GET", "/api/v1/me/api-clients", nil)
	wantStatus(t, e, tok, 403, "POST", "/api/v1/me/tokens", map[string]any{"name": "y", "actions": []string{"read"}, "expires_in_days": 1})
	wantStatus(t, e, tok, 403, "POST", "/api/v1/me/password", map[string]any{"current_password": pw, "new_password": "another-long-password"})
	wantStatus(t, e, tok, 403, "POST", "/api/v1/auth/logout", nil)

	// An administrator's client is not an administrator.
	at, _ := mkClient(t, admin, map[string]any{"name": "audit", "permissions": []string{domain.PermUsersView, domain.PermUsersManage}})
	wantStatus(t, e, at, 200, "GET", "/api/v1/users", nil)
	// Not an administrator: another account's bot is not visible to it.
	wantStatus(t, e, at, 404, "GET", "/api/v1/bots/"+mine, nil)
	e.user("admin2@x.io", domain.RoleAdmin)
	wantStatus(t, e, at, 403, "PATCH", "/api/v1/users/"+e.userID("admin2@x.io"), map[string]any{"disabled": true})
	wantStatus(t, e, at, 403, "POST", "/api/v1/admin/roles", map[string]any{"name": "Sneaky", "permissions": []string{domain.PermUsersView}})
	if resp, _ := bearer(e, at, "GET", "/api/v1/auth/me", nil); resp.StatusCode != 200 {
		t.Fatalf("me: %d", resp.StatusCode)
	}
	var me struct {
		User        userDTO
		Permissions []string
	}
	json.Unmarshal(wantStatus(t, e, at, 200, "GET", "/api/v1/auth/me", nil), &me)
	if len(me.Permissions) != 2 {
		t.Fatalf("client permissions: %v", me.Permissions)
	}
}

// A client limited to some bots or workspaces gets 403 outside them.
func TestAPIClientResourceScope(t *testing.T) {
	e := clientEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	a, b := owner.createBot("a"), owner.createBot("b")
	var wsr, otherr struct{ Workspace struct{ ID string } }
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/workspaces", map[string]any{"name": "Team"}), &wsr)
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/workspaces", map[string]any{"name": "Other"}), &otherr)
	ws, other := wsr.Workspace, otherr.Workspace
	inTeam := owner.createBotIn("in-team", ws.ID)

	all := []string{domain.PermBotsConsole, domain.PermBotsPower, domain.PermBotsCreate, domain.PermSitesCreate}
	// Scope must be reachable by the creator.
	stranger := e.user("stranger@x.io", domain.RoleUser)
	stranger.mustStatus(400, "POST", "/api/v1/me/api-clients", map[string]any{"name": "x", "permissions": all, "bot_ids": []string{a}})
	stranger.mustStatus(400, "POST", "/api/v1/me/api-clients", map[string]any{"name": "x", "permissions": all, "workspace_ids": []string{ws.ID}})
	owner.mustStatus(400, "POST", "/api/v1/me/api-clients", map[string]any{"name": "x", "permissions": all, "bot_ids": []string{}})

	botTok, _ := mkClient(t, owner, map[string]any{"name": "a-only", "permissions": all, "bot_ids": []string{a}})
	var bl struct{ Bots []struct{ ID string } }
	json.Unmarshal(wantStatus(t, e, botTok, 200, "GET", "/api/v1/bots", nil), &bl)
	if len(bl.Bots) != 1 || bl.Bots[0].ID != a {
		t.Fatalf("list: %+v", bl)
	}
	wantStatus(t, e, botTok, 202, "POST", "/api/v1/bots/"+a+"/start", nil)
	wantStatus(t, e, botTok, 403, "GET", "/api/v1/bots/"+b, nil)
	wantStatus(t, e, botTok, 403, "POST", "/api/v1/bots/"+b+"/start", nil)
	var br struct{ Results []struct{ OK bool } }
	json.Unmarshal(wantStatus(t, e, botTok, 202, "POST", "/api/v1/bots/batch", map[string]any{"ids": []string{b}, "action": "stop"}), &br)
	if len(br.Results) != 1 || br.Results[0].OK {
		t.Fatalf("batch outside the scope: %+v", br)
	}
	wantStatus(t, e, botTok, 403, "GET", "/api/v1/workspaces/"+ws.ID, nil)
	wantStatus(t, e, botTok, 403, "POST", "/api/v1/workspaces", map[string]any{"name": "New"})
	wantStatus(t, e, botTok, 403, "POST", "/api/v1/bots", map[string]any{"name": "n", "runtime": "nodejs"})
	wantStatus(t, e, botTok, 403, "GET", "/api/v1/sites", nil)
	wantStatus(t, e, botTok, 403, "GET", "/api/v1/operations", nil)

	wsTok, _ := mkClient(t, owner, map[string]any{"name": "team", "permissions": all, "workspace_ids": []string{ws.ID}})
	wantStatus(t, e, wsTok, 200, "GET", "/api/v1/bots/"+inTeam, nil)
	wantStatus(t, e, wsTok, 403, "GET", "/api/v1/bots/"+a, nil)
	wantStatus(t, e, wsTok, 200, "GET", "/api/v1/workspaces/"+ws.ID, nil)
	wantStatus(t, e, wsTok, 403, "GET", "/api/v1/workspaces/"+other.ID, nil)
	// Creating inside the scope works; in the personal workspace it does not.
	wantStatus(t, e, wsTok, 201, "POST", "/api/v1/bots", map[string]any{"name": "n", "runtime": "nodejs", "workspace_id": ws.ID})
	wantStatus(t, e, wsTok, 403, "POST", "/api/v1/bots", map[string]any{"name": "n2", "runtime": "nodejs"})
	// Moving a bot out of the scope is refused.
	wantStatus(t, e, wsTok, 403, "PUT", "/api/v1/bots/"+inTeam+"/workspace", map[string]any{"workspace_id": other.ID})
	var wl struct{ Workspaces []struct{ ID string } }
	json.Unmarshal(wantStatus(t, e, wsTok, 200, "GET", "/api/v1/workspaces", nil), &wl)
	if len(wl.Workspaces) != 1 || wl.Workspaces[0].ID != ws.ID {
		t.Fatalf("workspaces: %+v", wl)
	}

	// Refusals are recorded with the client's name.
	var evs auditList
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/activity/changes", nil), &evs)
	found := false
	for _, ev := range evs.Events {
		if ev.Outcome == "denied" && ev.Actor != nil && strings.Contains(*ev.Actor, "via API client a-only") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no refusal recorded: %+v", evs.Events)
	}
}

func TestAPIClientExpiryAndRevocation(t *testing.T) {
	e := clientEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	owner := e.user("owner@x.io", domain.RoleUser)
	mgr := e.user("mgr@x.io", domain.RoleUser)
	id := owner.createBot("a")
	tok, info := mkClient(t, owner, map[string]any{"name": "ci", "permissions": []string{domain.PermBotsPower}})
	if info.ExpiresAtMS != nil {
		t.Fatalf("no expiry requested: %+v", info)
	}
	if resp, _ := bearer(e, "rvc_nope", "GET", "/api/v1/bots", nil); resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	wantStatus(t, e, tok, 200, "GET", "/api/v1/bots/"+id, nil)
	var l struct{ Clients []apiClientDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/me/api-clients", nil), &l)
	if len(l.Clients) != 1 || l.Clients[0].LastUsedAtMS == nil {
		t.Fatalf("last used: %+v", l.Clients)
	}
	owner.mustStatus(400, "POST", "/api/v1/me/api-clients", map[string]any{"name": "x", "permissions": []string{domain.PermBotsPower}, "expires_in_days": 400})

	// Expiry.
	exp, expInfo := mkClient(t, owner, map[string]any{"name": "short", "permissions": []string{domain.PermBotsPower}, "expires_in_days": 1})
	wantStatus(t, e, exp, 200, "GET", "/api/v1/bots", nil)
	if _, err := e.db.Exec(`UPDATE api_clients SET expires_at_ms = created_at_ms + 1 WHERE id = ?`, expInfo.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // past the (now) expiry
	wantStatus(t, e, exp, 401, "GET", "/api/v1/bots", nil)

	// Another account cannot revoke it; a delegated manager cannot revoke an
	// administrator's client; the owner and administrators can.
	mgr.mustStatus(404, "DELETE", "/api/v1/me/api-clients/"+info.ID, nil)
	at, atInfo := mkClient(t, admin, map[string]any{"name": "admin-ci", "permissions": []string{domain.PermBotsPower}})
	rid := admin.createRole("Managers", domain.PermUsersView, domain.PermUsersManage)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("mgr@x.io"), map[string]any{"role": rid})
	mgr.mustStatus(403, "DELETE", "/api/v1/admin/api-clients/"+atInfo.ID, nil)
	var all struct{ Clients []apiClientDTO }
	json.Unmarshal(mgr.mustStatus(200, "GET", "/api/v1/admin/api-clients", nil), &all)
	if len(all.Clients) != 3 {
		t.Fatalf("admin list: %d", len(all.Clients))
	}
	mgr.mustStatus(204, "DELETE", "/api/v1/admin/api-clients/"+info.ID, nil)
	wantStatus(t, e, tok, 401, "GET", "/api/v1/bots", nil)
	admin.mustStatus(204, "DELETE", "/api/v1/me/api-clients/"+atInfo.ID, nil)
	wantStatus(t, e, at, 401, "GET", "/api/v1/bots", nil)

	// A disabled creator's clients stop working.
	t2, _ := mkClient(t, owner, map[string]any{"name": "again", "permissions": []string{domain.PermBotsPower}})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("owner@x.io"), map[string]any{"disabled": true})
	wantStatus(t, e, t2, 401, "GET", "/api/v1/bots", nil)
}

func (c *client) createBotIn(name, wsID string) string {
	c.e.t.Helper()
	var out struct{ ID string }
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": name, "runtime": "nodejs", "workspace_id": wsID}), &out)
	return out.ID
}
