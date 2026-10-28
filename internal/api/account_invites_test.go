package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/service"
)

// inviteEnv is a panel with registration open and auditing on.
func inviteEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	o := &service.OAuthService{Store: e.db, Auth: e.auth, Keys: e.bots.Keys, Providers: map[string]oauth.Provider{}, States: oauth.NewStateStore()}
	st := &service.SettingsService{Store: e.db, Keys: e.bots.Keys, OAuth: o, Auth: e.auth, Env: service.EnvSettings{AllowSignup: true, AllowSignupSet: true}}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		OAuth: o, Settings: st, Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true})
	return e
}

type inviteCreated struct {
	Token, Path string
	Invite      accountInviteDTO `json:"invite"`
}

func (c *client) invite(body map[string]any) inviteCreated {
	c.e.t.Helper()
	var out inviteCreated
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/admin/account-invites", body), &out)
	return out
}

// register uses an invitation link and returns the signed-in new account.
func register(t *testing.T, e *env, token, email string) *client {
	t.Helper()
	anon := &client{e: e}
	resp, body := anon.do("POST", "/api/v1/registration", map[string]string{"token": token, "email": email, "password": pw})
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	c := &client{e: e}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck.Value
		}
	}
	var reg struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(body, &reg)
	c.csrf = reg.CSRF
	return c
}

// An invitation carries the account's whole setup: a custom role, limits for
// the personal workspace, the node it is pinned to and a team workspace to
// join. Everything lands when the link is used.
func TestInviteSetupApplied(t *testing.T) {
	e := inviteEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	team := teamWS(admin, "Team")
	limited := admin.createRole("Limited", domain.PermBotsCreate, domain.PermBotsConsole)

	created := admin.invite(map[string]any{"email": "new@x.io", "role": limited, "expires_in_days": 30,
		"quota":   map[string]any{"max_bots": 1, "memory_bytes": 1 << 30},
		"node_id": domain.LocalNodeID, "workspace_id": team, "workspace_role": domain.WorkspaceDeveloper})
	v := created.Invite
	if v.Role != domain.RoleUser || v.RoleID != limited || v.RoleName != "Limited" || v.CreatedByEmail != "admin@x.io" ||
		v.Quota == nil || v.Quota.MaxBots == nil || *v.Quota.MaxBots != 1 || v.Quota.MemoryBytes == nil || *v.Quota.MemoryBytes != 1<<30 || v.Quota.MaxSites != nil ||
		v.NodeID != domain.LocalNodeID || v.NodeName == "" || v.WorkspaceID != team || v.WorkspaceName != "Team" || v.WorkspaceRole != domain.WorkspaceDeveloper {
		t.Fatalf("created invite: %+v", v)
	}

	// The public preview names the role and workspace, never limits or node.
	anon := &client{e: e}
	body := anon.mustStatus(200, "POST", "/api/v1/registration/preview", map[string]string{"token": created.Token})
	var preview map[string]any
	json.Unmarshal(body, &preview)
	if preview["role_name"] != "Limited" || preview["workspace_name"] != "Team" || preview["email"] != "new@x.io" {
		t.Fatalf("preview: %s", body)
	}
	for _, k := range []string{"quota", "node_id", "node_name", "workspace_id", "role_id", "created_by_email"} {
		if _, ok := preview[k]; ok {
			t.Fatalf("preview leaks %s: %s", k, body)
		}
	}

	invitee := register(t, e, created.Token, "new@x.io")
	me := invitee.me()
	if me.User.RoleID != limited || me.User.Role != domain.RoleUser || len(me.Permissions) != 2 {
		t.Fatalf("invitee: %+v", me)
	}
	personal := personalWS(t, e, "new@x.io")
	var limits struct {
		Quota quotaDTO `json:"quota"`
	}
	json.Unmarshal(invitee.mustStatus(200, "GET", "/api/v1/workspaces/"+personal+"/limits", nil), &limits)
	if limits.Quota.MaxBots == nil || *limits.Quota.MaxBots != 1 || limits.Quota.MemoryBytes == nil || *limits.Quota.MemoryBytes != 1<<30 || limits.Quota.MaxApps != nil {
		t.Fatalf("limits: %+v", limits.Quota)
	}
	var ws struct{ Workspaces []workspaceDTO }
	json.Unmarshal(invitee.mustStatus(200, "GET", "/api/v1/workspaces", nil), &ws)
	joined := false
	for _, w := range ws.Workspaces {
		if w.ID == team {
			joined = w.Role == domain.WorkspaceDeveloper
		}
	}
	if !joined || len(ws.Workspaces) != 2 {
		t.Fatalf("workspaces: %+v", ws.Workspaces)
	}
	var nodeID, updatedBy string
	e.db.QueryRow(`SELECT COALESCE(node_id, '') FROM workspaces WHERE id = ?`, personal).Scan(&nodeID)
	e.db.QueryRow(`SELECT COALESCE(updated_by, '') FROM workspace_quotas WHERE workspace_id = ?`, personal).Scan(&updatedBy)
	if nodeID != domain.LocalNodeID || updatedBy != e.userID("admin@x.io") {
		t.Fatalf("personal workspace node %q, quota by %q", nodeID, updatedBy)
	}
	// The limit is enforced right away.
	invitee.createBot("one")
	expect400(t, invitee, "POST", "/api/v1/bots", map[string]any{"name": "two", "runtime": "nodejs"}, "bot")
	// A used link is gone.
	anon.mustStatus(404, "POST", "/api/v1/registration/preview", map[string]string{"token": created.Token})
}

// A delegated account manager hands out only what it holds: no limits, no
// node choice without nodes.manage, no role broader than its own, no team
// workspace it does not administer, and a link no longer than 30 days.
func TestInviteDelegation(t *testing.T) {
	e := inviteEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	mgr := e.user("manager@x.io", domain.RoleUser)
	managers := admin.createRole("Account managers", append([]string{domain.PermUsersManage}, domain.DefaultUserPermissions()...)...)
	ops := admin.createRole("Node operators", domain.PermNodesManage)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("manager@x.io"), map[string]any{"role": managers})
	adminTeam := teamWS(admin, "Admin team")
	mgrTeam := teamWS(mgr, "Manager team")

	post := func(c *client, body map[string]any) (int, string) {
		base := map[string]any{"role": "user", "expires_in_days": 3}
		for k, v := range body {
			base[k] = v
		}
		resp, b := c.do("POST", "/api/v1/admin/account-invites", base)
		return resp.StatusCode, string(b)
	}
	if code, b := post(mgr, map[string]any{"quota": map[string]any{"max_bots": 1}}); code != 403 {
		t.Fatalf("limits by a delegate: %d %s", code, b)
	}
	if code, b := post(mgr, map[string]any{"node_id": domain.LocalNodeID}); code != 403 {
		t.Fatalf("node by a delegate: %d %s", code, b)
	}
	if code, b := post(mgr, map[string]any{"role": ops}); code != 400 || !strings.Contains(b, domain.PermNodesManage) {
		t.Fatalf("broader role by a delegate: %d %s", code, b)
	}
	if code, b := post(mgr, map[string]any{"role": "admin"}); code != 403 {
		t.Fatalf("admin role by a delegate: %d %s", code, b)
	}
	if code, b := post(mgr, map[string]any{"workspace_id": adminTeam, "workspace_role": "developer"}); code != 403 && code != 404 {
		t.Fatalf("foreign workspace by a delegate: %d %s", code, b)
	}
	if code, b := post(mgr, map[string]any{"expires_in_days": 31}); code != 400 {
		t.Fatalf("31 days: %d %s", code, b)
	}
	if code, b := post(admin, map[string]any{"expires_in_days": 31}); code != 400 {
		t.Fatalf("31 days by an administrator: %d %s", code, b)
	}
	if code, b := post(admin, map[string]any{"role": "nope"}); code != 400 || !strings.Contains(b, "unknown role") {
		t.Fatalf("unknown role: %d %s", code, b)
	}
	if code, b := post(admin, map[string]any{"workspace_id": adminTeam, "workspace_role": "owner"}); code != 400 {
		t.Fatalf("owner role: %d %s", code, b)
	}
	if code, b := post(admin, map[string]any{"workspace_id": personalWS(t, e, "admin@x.io"), "workspace_role": "viewer"}); code != 400 {
		t.Fatalf("personal workspace: %d %s", code, b)
	}
	if code, b := post(admin, map[string]any{"node_id": "11111111-1111-4111-8111-111111111111"}); code != 400 {
		t.Fatalf("unknown node: %d %s", code, b)
	}
	// Within its reach it works: its own team workspace, 30 days, the user role.
	v := mgr.invite(map[string]any{"role": "user", "expires_in_days": 30, "workspace_id": mgrTeam, "workspace_role": "viewer"}).Invite
	if v.WorkspaceID != mgrTeam || v.WorkspaceRole != "viewer" || v.RoleName != "User" || v.CreatedByEmail != "manager@x.io" || v.Quota != nil || v.NodeID != "" {
		t.Fatalf("delegate invite: %+v", v)
	}
}

// Invitation rows written before the setup columns existed still register
// with the built-in role and nothing else.
func TestInviteLegacyRow(t *testing.T) {
	e := inviteEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	const token = "bpu_legacy-token-from-an-older-release"
	if _, err := e.db.Exec(`INSERT INTO account_invites (id, token_hash, email, role, created_by, created_at_ms, expires_at_ms)
		VALUES ('legacy', ?, NULL, 'user', ?, 1, ?)`, auth.HashToken(token), e.userID("admin@x.io"), 1<<60); err != nil {
		t.Fatal(err)
	}
	anon := &client{e: e}
	var preview map[string]any
	json.Unmarshal(anon.mustStatus(200, "POST", "/api/v1/registration/preview", map[string]string{"token": token}), &preview)
	if preview["role_name"] != "User" || preview["workspace_name"] != "" || preview["email"] != "" {
		t.Fatalf("legacy preview: %v", preview)
	}
	var list struct{ Invites []accountInviteDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/account-invites", nil), &list)
	if len(list.Invites) != 1 || list.Invites[0].ID != "legacy" || list.Invites[0].Quota != nil || list.Invites[0].RoleName != "User" || list.Invites[0].CreatedByEmail != "admin@x.io" {
		t.Fatalf("legacy list: %+v", list.Invites)
	}
	invitee := register(t, e, token, "old@x.io")
	me := invitee.me()
	if me.User.Role != domain.RoleUser || me.User.RoleID != "" || len(me.Permissions) != len(domain.DefaultUserPermissions()) {
		t.Fatalf("legacy invitee: %+v", me)
	}
	personal := personalWS(t, e, "old@x.io")
	var nodeID string
	var quotas int
	e.db.QueryRow(`SELECT COALESCE(node_id, '') FROM workspaces WHERE id = ?`, personal).Scan(&nodeID)
	e.db.QueryRow(`SELECT count(*) FROM workspace_quotas WHERE workspace_id = ?`, personal).Scan(&quotas)
	if nodeID != "" || quotas != 0 {
		t.Fatalf("legacy setup applied: node %q quotas %d", nodeID, quotas)
	}
}

// Pending invitations list their setup and creator; creating, revoking and
// registering are audited.
func TestInvitesListedAndAudited(t *testing.T) {
	e := inviteEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	team := teamWS(admin, "Team")
	limited := admin.createRole("Limited", domain.PermBotsConsole)
	first := admin.invite(map[string]any{"email": "new@x.io", "role": limited, "expires_in_days": 7,
		"quota": map[string]any{"max_bots": 1}, "workspace_id": team, "workspace_role": "developer"})
	second := admin.invite(map[string]any{"role": "admin", "expires_in_days": 1})

	var list struct{ Invites []accountInviteDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/account-invites", nil), &list)
	if len(list.Invites) != 2 {
		t.Fatalf("list: %+v", list.Invites)
	}
	for _, v := range list.Invites {
		switch v.ID {
		case first.Invite.ID:
			if v.Email != "new@x.io" || v.RoleID != limited || v.RoleName != "Limited" || v.CreatedByEmail != "admin@x.io" ||
				v.Quota == nil || *v.Quota.MaxBots != 1 || v.WorkspaceID != team || v.WorkspaceName != "Team" || v.WorkspaceRole != "developer" || v.NodeID != "" {
				t.Fatalf("first: %+v", v)
			}
		case second.Invite.ID:
			if v.Role != "admin" || v.RoleName != "Administrator" || v.Quota != nil || v.WorkspaceID != "" || v.CreatedByEmail != "admin@x.io" {
				t.Fatalf("second: %+v", v)
			}
		default:
			t.Fatalf("unexpected invite %+v", v)
		}
	}
	admin.mustStatus(204, "DELETE", "/api/v1/admin/account-invites/"+second.Invite.ID, nil)
	admin.mustStatus(404, "DELETE", "/api/v1/admin/account-invites/"+second.Invite.ID, nil)
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/account-invites", nil), &list)
	if len(list.Invites) != 1 || list.Invites[0].ID != first.Invite.ID {
		t.Fatalf("after revoke: %+v", list.Invites)
	}
	register(t, e, first.Token, "new@x.io")

	rows, err := e.db.Query(`SELECT action, outcome, COALESCE(target, ''), COALESCE(actor_label, '') FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, o, tg, actor string
		rows.Scan(&a, &o, &tg, &actor)
		got = append(got, a+"|"+o+"|"+tg+"|"+actor)
	}
	for _, want := range []string{
		"admin.invite_create|ok|new@x.io role=Limited limits=bots=1 node=default workspace=Team/developer|admin@x.io",
		"admin.invite_create|ok|link role=Administrator limits=none node=default workspace=none|admin@x.io",
		"admin.invite_revoke|ok|" + second.Invite.ID + "|admin@x.io",
		"admin.invite_revoke|denied|" + second.Invite.ID + "|admin@x.io",
		"account.register|ok|" + first.Invite.ID + "|new@x.io",
	} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	if _, err := e.db.GetUserByEmail(context.Background(), "new@x.io"); err != nil {
		t.Fatal(err)
	}
}
