package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func (e *env) userID(email string) string {
	e.t.Helper()
	u, err := e.db.GetUserByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (c *client) createRole(name string, perms ...string) string {
	c.e.t.Helper()
	if perms == nil {
		perms = []string{}
	}
	var r roleDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/admin/roles", map[string]any{"name": name, "permissions": perms}), &r)
	return r.ID
}

func (c *client) me() (out struct {
	User              userDTO  `json:"user"`
	Permissions       []string `json:"permissions"`
	EmailVerification struct {
		Verified     bool     `json:"verified"`
		Withheld     []string `json:"withheld"`
		Available    bool     `json:"available"`
		PendingEmail string   `json:"pending_email"`
	} `json:"email_verification"`
}) {
	c.e.t.Helper()
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &out)
	return out
}

// A custom role is enforced by the server on representative routes, owners
// included, and through sub-user grants.
func TestCustomRoleEnforcedOnServer(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	op := e.user("op@x.io", domain.RoleUser)
	owner := e.user("owner@x.io", domain.RoleUser)
	mine := op.createBot("mine")
	shared := owner.createBot("shared")
	owner.mustStatus(200, "PUT", "/api/v1/bots/"+shared+"/users", map[string]any{"email": "op@x.io", "permissions": domain.PermFullAdmin})

	// Before the role: the built-in user role allows everything on own bots.
	op.mustStatus(200, "GET", "/api/v1/bots/"+mine+"/files?path=.", nil)
	op.mustStatus(200, "GET", "/api/v1/bots/"+shared+"/env", nil)

	rid := admin.createRole("Operators", domain.PermBotsConsole, domain.PermBotsPower)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("op@x.io"), map[string]any{"role": rid})

	m := op.me()
	if m.User.RoleID != rid || m.User.RoleName != "Operators" || m.User.Role != domain.RoleUser {
		t.Fatalf("role not shown: %+v", m.User)
	}
	if !slices.Equal(m.Permissions, []string{domain.PermBotsConsole, domain.PermBotsPower}) {
		t.Fatalf("permissions: %v", m.Permissions)
	}

	// Own bot: files, env, delete and creation are refused; power is not.
	resp, body := op.do("GET", "/api/v1/bots/"+mine+"/files?path=.", nil)
	if resp.StatusCode != 403 || !strings.Contains(string(body), "bots.files") {
		t.Fatalf("files: %d %s", resp.StatusCode, body)
	}
	op.mustStatus(403, "GET", "/api/v1/bots/"+mine+"/env", nil)
	op.mustStatus(403, "PUT", "/api/v1/bots/"+mine+"/env", map[string]any{"vars": map[string]string{"A": "b"}})
	op.mustStatus(403, "DELETE", "/api/v1/bots/"+mine, nil)
	op.mustStatus(403, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs"})
	op.mustStatus(403, "POST", "/api/v1/workspaces", map[string]any{"name": "team"})
	op.mustStatus(403, "POST", "/api/v1/me/api-keys", map[string]any{"name": "k"})
	op.mustStatus(403, "PUT", "/api/v1/bots/"+mine+"/users", map[string]any{"email": "owner@x.io", "permissions": 1})
	op.mustStatus(202, "POST", "/api/v1/bots/"+mine+"/start", nil)

	// A full-access sub-user grant cannot bring back what the role removes.
	op.mustStatus(403, "GET", "/api/v1/bots/"+shared+"/env", nil)
	op.mustStatus(403, "GET", "/api/v1/bots/"+shared+"/files?path=.", nil)
	var got botDTO
	json.Unmarshal(op.mustStatus(200, "GET", "/api/v1/bots/"+shared, nil), &got)
	if got.Permissions&domain.PermEditFiles != 0 || got.Permissions&domain.PermManageEnv != 0 || got.Permissions&domain.PermPower == 0 {
		t.Fatalf("masked permissions: %d", got.Permissions)
	}

	// Administration stays closed.
	op.mustStatus(403, "GET", "/api/v1/users", nil)
	op.mustStatus(403, "GET", "/api/v1/nodes", nil)
	op.mustStatus(403, "GET", "/api/v1/admin/roles", nil)

	// Editing the role applies on the next request.
	admin.mustStatus(200, "PATCH", "/api/v1/admin/roles/"+rid, map[string]any{"name": "Operators",
		"permissions": []string{domain.PermBotsConsole, domain.PermBotsPower, domain.PermBotsFiles}})
	op.mustStatus(200, "GET", "/api/v1/bots/"+mine+"/files?path=.", nil)

	// Back to the built-in user role: everything as before.
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("op@x.io"), map[string]any{"role": "user"})
	op.mustStatus(200, "GET", "/api/v1/bots/"+mine+"/env", nil)
	op.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "again", "runtime": "nodejs"})
}

// Built-in roles keep their behaviour and are listed as system roles.
func TestBuiltInRolesUnchanged(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	if got := u.me().Permissions; !slices.Equal(got, domain.DefaultUserPermissions()) {
		t.Fatalf("user permissions: %v", got)
	}
	if got := admin.me().Permissions; !slices.Equal(got, domain.AllPermissions()) {
		t.Fatalf("admin permissions: %v", got)
	}
	var roles struct{ Roles []roleDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/roles", nil), &roles)
	if len(roles.Roles) != 2 || !roles.Roles[0].System || roles.Roles[0].ID != "admin" || roles.Roles[1].ID != "user" ||
		roles.Roles[0].Users != 1 || roles.Roles[1].Users != 1 {
		t.Fatalf("system roles: %+v", roles.Roles)
	}
	admin.mustStatus(400, "PATCH", "/api/v1/admin/roles/admin", map[string]any{"name": "x", "permissions": []string{}})
	admin.mustStatus(400, "DELETE", "/api/v1/admin/roles/user", nil)
	admin.mustStatus(400, "POST", "/api/v1/admin/roles", map[string]any{"name": "bad", "permissions": []string{"nope"}})
	admin.mustStatus(400, "POST", "/api/v1/admin/roles", map[string]any{"name": "user", "permissions": []string{}}) // duplicate (case-insensitive)

	// The existing role switch still works, and a user still cannot manage accounts.
	uid := e.userID("u@x.io")
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+uid, map[string]any{"role": "admin"})
	u.mustStatus(200, "GET", "/api/v1/users", nil)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+uid, map[string]any{"role": "user"})
	u.mustStatus(403, "GET", "/api/v1/users", nil)
	// The last administrator cannot be given a custom role either.
	rid := admin.createRole("Viewer")
	admin.mustStatus(400, "PATCH", "/api/v1/users/"+e.userID("admin@x.io"), map[string]any{"role": rid})
}

// Delegated administration: a custom role opens exactly its parts of the
// administration area and can never grant more than it holds.
func TestDelegatedAdministration(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	sup := e.user("support@x.io", domain.RoleUser)
	plain := e.user("plain@x.io", domain.RoleUser)
	support := admin.createRole("Support", domain.PermUsersView, domain.PermUsersManage, domain.PermBotsConsole)
	broader := admin.createRole("Developers", domain.PermBotsConsole, domain.PermBotsFiles)
	narrow := admin.createRole("Console only", domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("support@x.io"), map[string]any{"role": support})
	plainID, adminID := e.userID("plain@x.io"), e.userID("admin@x.io")

	sup.mustStatus(200, "GET", "/api/v1/users", nil)
	sup.mustStatus(200, "GET", "/api/v1/admin/users/"+plainID, nil)
	sup.mustStatus(200, "GET", "/api/v1/admin/roles", nil)
	sup.mustStatus(403, "GET", "/api/v1/nodes", nil)
	sup.mustStatus(403, "GET", "/api/v1/admin/workspaces", nil)
	sup.mustStatus(403, "POST", "/api/v1/admin/roles", map[string]any{"name": "x", "permissions": []string{}})

	// No escalation: no administrators, no roles with permissions it lacks,
	// not its own role, never touching an administrator.
	sup.mustStatus(403, "PATCH", "/api/v1/users/"+plainID, map[string]any{"role": "admin"})
	sup.mustStatus(400, "PATCH", "/api/v1/users/"+plainID, map[string]any{"role": broader})
	sup.mustStatus(400, "PATCH", "/api/v1/users/"+plainID, map[string]any{"role": "user"}) // holds resource permissions it lacks
	sup.mustStatus(400, "PATCH", "/api/v1/users/"+e.userID("support@x.io"), map[string]any{"role": narrow})
	sup.mustStatus(403, "PATCH", "/api/v1/users/"+adminID, map[string]any{"disabled": true})
	sup.mustStatus(403, "PATCH", "/api/v1/users/"+adminID, map[string]any{"role": narrow})
	sup.mustStatus(403, "POST", "/api/v1/users", map[string]any{"email": "new@x.io", "password": pw, "role": "admin"})
	sup.mustStatus(403, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "", "role": "admin", "expires_in_days": 3})
	sup.mustStatus(400, "POST", "/api/v1/admin/account-invites", map[string]any{"email": "", "role": "user", "expires_in_days": 3}) // user role holds permissions it lacks

	// Within its permissions it works.
	sup.mustStatus(204, "PATCH", "/api/v1/users/"+plainID, map[string]any{"role": narrow})
	if r := plain.me(); r.User.RoleID != narrow || len(r.Permissions) != 1 {
		t.Fatalf("assigned by a delegate: %+v", r)
	}
	sup.mustStatus(204, "PATCH", "/api/v1/users/"+plainID, map[string]any{"disabled": true})
	sup.mustStatus(201, "POST", "/api/v1/users", map[string]any{"email": "new@x.io", "password": pw, "role": narrow})
	var users struct{ Users []userDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/users", nil), &users)
	for _, u := range users.Users {
		if u.Email == "new@x.io" && u.RoleID != narrow {
			t.Fatalf("created without the role: %+v", u)
		}
	}
}

// Role changes are recorded with names, and a role in use cannot be deleted.
func TestRoleChangesAreAudited(t *testing.T) {
	e := newEnv(t)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true})
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	rid := admin.createRole("Helpers", domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("u@x.io"), map[string]any{"role": rid})
	admin.mustStatus(400, "DELETE", "/api/v1/admin/roles/"+rid, nil) // still assigned
	u.mustStatus(403, "POST", "/api/v1/admin/roles", map[string]any{"name": "Mine", "permissions": []string{domain.PermUsersManage}})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("u@x.io"), map[string]any{"role": "user"})
	admin.mustStatus(204, "DELETE", "/api/v1/admin/roles/"+rid, nil)

	rows, err := e.db.Query(`SELECT action, outcome, COALESCE(target, '') FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, o, tg string
		rows.Scan(&a, &o, &tg)
		got = append(got, a+"|"+o+"|"+tg)
	}
	for _, want := range []string{
		"admin.role_create|ok|Helpers: bots.console",
		"admin.user_role|ok|Helpers",
		"admin.role_delete|failed|",
		"admin.role_create|denied|Mine",
		"admin.user_role|ok|User",
		"admin.role_delete|ok|Helpers",
	} {
		found := false
		for _, g := range got {
			if g == want || (strings.HasSuffix(want, "|") && strings.HasPrefix(g, want)) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

// Administration permissions open exactly their routes, and a delegated
// role manager can neither grant what it lacks nor change its own role.
func TestDelegatedRoleManagersAndNodes(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	ops := e.user("ops@x.io", domain.RoleUser)
	rm := e.user("roles@x.io", domain.RoleUser)
	nodeRole := admin.createRole("Node operators", domain.PermNodesManage)
	roleMgr := admin.createRole("Role managers", domain.PermRolesManage, domain.PermBotsConsole, domain.PermBotsPower)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": nodeRole})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("roles@x.io"), map[string]any{"role": roleMgr})

	ops.mustStatus(200, "GET", "/api/v1/nodes", nil)
	ops.mustStatus(403, "GET", "/api/v1/users", nil)
	ops.mustStatus(403, "GET", "/api/v1/admin/roles", nil)
	ops.mustStatus(403, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs"})
	if m := ops.me(); !slices.Equal(m.Permissions, []string{domain.PermNodesManage}) {
		t.Fatalf("node operator permissions: %v", m.Permissions)
	}

	rm.mustStatus(403, "GET", "/api/v1/nodes", nil)
	rm.mustStatus(200, "GET", "/api/v1/admin/roles", nil)
	narrow := rm.createRole("Console", domain.PermBotsConsole)
	rm.mustStatus(400, "POST", "/api/v1/admin/roles", map[string]any{"name": "Wider", "permissions": []string{domain.PermBotsFiles}})
	rm.mustStatus(400, "PATCH", "/api/v1/admin/roles/"+narrow, map[string]any{"name": "Console", "permissions": []string{domain.PermUsersManage}})
	rm.mustStatus(200, "PATCH", "/api/v1/admin/roles/"+narrow, map[string]any{"name": "Console", "permissions": []string{domain.PermBotsConsole, domain.PermBotsPower}})
	rm.mustStatus(400, "PATCH", "/api/v1/admin/roles/"+roleMgr, map[string]any{"name": "Role managers", "permissions": []string{domain.PermRolesManage}})
	rm.mustStatus(403, "DELETE", "/api/v1/admin/roles/"+nodeRole, nil) // holds a permission it lacks
	rm.mustStatus(400, "DELETE", "/api/v1/admin/roles/user", nil)
	rm.mustStatus(204, "DELETE", "/api/v1/admin/roles/"+narrow, nil)
	// Role managers cannot assign roles without users.manage.
	rm.mustStatus(403, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": "user"})
}

// A delegated account manager cannot act on an account holding an
// administration permission it lacks: changing its address (then resetting
// the password) would otherwise take that account over.
func TestDelegatedManagerCannotTakeOverBroaderAccounts(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	sup := e.user("support@x.io", domain.RoleUser)
	e.user("ops@x.io", domain.RoleUser)
	e.user("peer@x.io", domain.RoleUser)
	support := admin.createRole("Support", domain.PermUsersView, domain.PermUsersManage, domain.PermBotsConsole)
	nodeOps := admin.createRole("Node operators", domain.PermNodesManage, domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("support@x.io"), map[string]any{"role": support})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": nodeOps})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("peer@x.io"), map[string]any{"role": support})
	opsID, peerID := e.userID("ops@x.io"), e.userID("peer@x.io")

	for _, body := range []map[string]any{
		{"email": "mine@x.io"},
		{"email_verified": false},
		{"disabled": true},
		{"role": support},
	} {
		sup.mustStatus(403, "PATCH", "/api/v1/users/"+opsID, body)
	}
	if u, _ := e.db.GetUserByID(context.Background(), opsID); u.Email != "ops@x.io" || u.Disabled || u.RoleID != nodeOps {
		t.Fatalf("node operator changed by a delegate: %+v", u)
	}
	// An account with the same administration permissions is within reach.
	sup.mustStatus(204, "PATCH", "/api/v1/users/"+peerID, map[string]any{"disabled": true})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+opsID, map[string]any{"email": "ops2@x.io"})
}
