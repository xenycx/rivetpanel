package api

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
)

type wsList struct {
	Workspaces []struct {
		ID       string
		Name     string
		Role     string
		Personal bool
		Bots     int
	}
}

func TestWorkspaceRolesAndMembership(t *testing.T) {
	e := newEnv(t)
	a := e.user("a@x.io", domain.RoleUser)
	b := e.user("b@x.io", domain.RoleUser)
	admin := e.user("root@x.io", domain.RoleAdmin)

	var created struct {
		Workspace struct{ ID, Role string }
	}
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/workspaces", map[string]string{"name": "Team"}), &created)
	team := created.Workspace.ID
	if created.Workspace.Role != domain.WorkspaceOwner {
		t.Fatalf("creator role = %q", created.Workspace.Role)
	}
	var list wsList
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/workspaces", nil), &list)
	if len(list.Workspaces) != 2 || !list.Workspaces[0].Personal || list.Workspaces[1].Name != "Team" {
		t.Fatalf("workspaces = %+v", list.Workspaces)
	}
	personal := list.Workspaces[0].ID

	var bot struct {
		ID          string
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "team-bot", "runtime": "nodejs", "workspace_id": team}), &bot)
	if bot.WorkspaceID != team {
		t.Fatalf("bot workspace = %q", bot.WorkspaceID)
	}
	// Bots default to the personal workspace.
	var own struct {
		ID          string
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(a.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "mine", "runtime": "nodejs"}), &own)
	if own.WorkspaceID != personal {
		t.Fatalf("default workspace = %q want %q", own.WorkspaceID, personal)
	}

	// Outsiders cannot see the workspace or its bots, nor create bots in it.
	b.mustStatus(404, "GET", "/api/v1/workspaces/"+team, nil)
	b.mustStatus(404, "GET", "/api/v1/bots/"+bot.ID, nil)
	b.mustStatus(404, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs", "workspace_id": team})

	// A developer operates bots but cannot reconfigure, delete or share them.
	a.mustStatus(200, "PUT", "/api/v1/workspaces/"+team+"/members", map[string]string{"email": "B@x.io", "role": "developer"})
	var bl struct {
		Bots []struct {
			ID     string
			Shared bool
		}
	}
	json.Unmarshal(b.mustStatus(200, "GET", "/api/v1/bots", nil), &bl)
	if len(bl.Bots) != 1 || bl.Bots[0].ID != bot.ID || bl.Bots[0].Shared {
		t.Fatalf("developer sees %+v", bl.Bots)
	}
	b.mustStatus(202, "POST", "/api/v1/bots/"+bot.ID+"/start", nil)
	b.mustStatus(403, "PATCH", "/api/v1/bots/"+bot.ID, map[string]any{"name": "renamed"})
	b.mustStatus(403, "DELETE", "/api/v1/bots/"+bot.ID, nil)
	b.mustStatus(403, "PUT", "/api/v1/workspaces/"+team+"/members", map[string]string{"email": "root@x.io", "role": "viewer"})
	b.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "dev-bot", "runtime": "nodejs", "workspace_id": team})

	// A viewer only looks.
	var detail struct {
		Members []struct {
			UserID string `json:"user_id"`
			Role   string
		}
	}
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/workspaces/"+team, nil), &detail)
	bID := ""
	for _, m := range detail.Members {
		if m.Role == "developer" {
			bID = m.UserID
		}
	}
	a.mustStatus(200, "PATCH", "/api/v1/workspaces/"+team+"/members/"+bID, map[string]string{"role": "viewer"})
	b.mustStatus(403, "POST", "/api/v1/bots/"+bot.ID+"/stop", nil)
	b.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "y", "runtime": "nodejs", "workspace_id": team})
	b.mustStatus(200, "GET", "/api/v1/bots/"+bot.ID, nil)

	// The owner cannot be removed or demoted; members may leave.
	a.mustStatus(400, "DELETE", "/api/v1/workspaces/"+team+"/members/"+currentID(t, a), nil)
	b.mustStatus(204, "DELETE", "/api/v1/workspaces/"+team+"/members/"+bID, nil)
	b.mustStatus(404, "GET", "/api/v1/bots/"+bot.ID, nil)

	// Administrators see every workspace, its bots and its owner.
	var all wsList
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces", nil), &all)
	if len(all.Workspaces) != 4 { // three personal + Team
		t.Fatalf("admin sees %d workspaces", len(all.Workspaces))
	}
	var aw struct {
		Bots []struct {
			ID         string
			OwnerEmail string `json:"owner_email"`
		}
		Operations []any
		Sites      []any
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+team, nil), &aw)
	if len(aw.Bots) != 2 || aw.Operations == nil || aw.Sites == nil {
		t.Fatalf("admin workspace view = %+v", aw)
	}
	var au struct {
		Workspaces []any
		Bots       []any
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/users/"+currentID(t, a), nil), &au)
	if len(au.Workspaces) != 2 || len(au.Bots) != 2 {
		t.Fatalf("admin user view = %+v", au)
	}
	a.mustStatus(403, "GET", "/api/v1/admin/workspaces", nil)
	a.mustStatus(403, "GET", "/api/v1/admin/users/"+currentID(t, a), nil)

	// A workspace with bots cannot be deleted; a personal one never.
	a.mustStatus(400, "DELETE", "/api/v1/workspaces/"+team, nil)
	a.mustStatus(400, "DELETE", "/api/v1/workspaces/"+personal, nil)
	a.mustStatus(200, "PUT", "/api/v1/bots/"+bot.ID+"/workspace", map[string]string{"workspace_id": personal})
	var dl struct{ Bots []struct{ ID string } }
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/bots", nil), &dl)
	for _, x := range dl.Bots {
		if x.ID != bot.ID && x.ID != own.ID {
			a.mustStatus(200, "PUT", "/api/v1/bots/"+x.ID+"/workspace", map[string]string{"workspace_id": personal})
		}
	}
	a.mustStatus(204, "DELETE", "/api/v1/workspaces/"+team, nil)
}

func currentID(t *testing.T, c *client) string {
	t.Helper()
	var me struct{ User struct{ ID string } }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	return me.User.ID
}

// Renaming needs the workspace admin role and the workspaces.rename
// permission; personal workspaces are renamed like team ones and the new
// name shows wherever workspaces are listed.
func TestWorkspaceRenamePermission(t *testing.T) {
	e := newEnv(t)
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	other := e.user("o@x.io", domain.RoleUser)
	team := teamWS(u, "Team")
	u.mustStatus(200, "PUT", "/api/v1/workspaces/"+team+"/members", map[string]string{"email": "o@x.io", "role": "developer"})

	// A custom role without the permission: workspace owner, still refused.
	rid := admin.createRole("Builders", domain.PermWorkspacesCreate)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("u@x.io"), map[string]any{"role": rid})
	resp, body := u.do("PATCH", "/api/v1/workspaces/"+team, map[string]any{"name": "Renamed"})
	if resp.StatusCode != 403 || !strings.Contains(string(body), domain.PermWorkspacesRename) {
		t.Fatalf("rename without permission: %d %s", resp.StatusCode, body)
	}
	admin.mustStatus(200, "PATCH", "/api/v1/admin/roles/"+rid, map[string]any{"name": "Builders",
		"permissions": []string{domain.PermWorkspacesCreate, domain.PermWorkspacesRename}})
	var renamed struct{ Workspace struct{ Name string } }
	json.Unmarshal(u.mustStatus(200, "PATCH", "/api/v1/workspaces/"+team, map[string]any{"name": "Renamed"}), &renamed)
	if renamed.Workspace.Name != "Renamed" {
		t.Fatalf("renamed to %q", renamed.Workspace.Name)
	}
	// The permission does not lift the workspace admin role requirement.
	other.mustStatus(403, "PATCH", "/api/v1/workspaces/"+team, map[string]any{"name": "Nope"})

	// The built-in user role renames its own personal workspace.
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("u@x.io"), map[string]any{"role": "user"})
	personal := personalWS(t, e, "u@x.io")
	u.mustStatus(400, "PATCH", "/api/v1/workspaces/"+personal, map[string]any{"name": "   "})
	u.mustStatus(400, "PATCH", "/api/v1/workspaces/"+personal, map[string]any{"name": strings.Repeat("x", 65)})
	u.mustStatus(200, "PATCH", "/api/v1/workspaces/"+personal, map[string]any{"name": " Alice's lab "})
	var list wsList
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/workspaces", nil), &list)
	if len(list.Workspaces) != 2 || !list.Workspaces[0].Personal || list.Workspaces[0].Name != "Alice's lab" {
		t.Fatalf("workspaces = %+v", list.Workspaces)
	}
	// Other accounts never rename someone's personal workspace.
	other.mustStatus(404, "PATCH", "/api/v1/workspaces/"+personal, map[string]any{"name": "Mine"})
}

func adminDeletePath(ws string) string { return "/api/v1/admin/workspaces/" + ws + "/delete" }

// An administrator deletes a team workspace with its apps (add-ons
// included), sites and members after typing its name; the audit log records
// what went.
func TestAdminWorkspaceDeleteCascade(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	e.bots.AddonData = addons.DataRoot{Dir: filepath.Join(t.TempDir(), "addons")}
	admin := e.user("root@x.io", domain.RoleAdmin)
	owner := e.user("lead@x.io", domain.RoleUser)
	e.user("dev@x.io", domain.RoleUser)
	ws := teamWS(owner, "Studio")
	owner.mustStatus(200, "PUT", "/api/v1/workspaces/"+ws+"/members", map[string]string{"email": "dev@x.io", "role": "developer"})
	one := owner.createBotIn("one", ws)
	var two botDTO
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "two", "runtime": "nodejs", "workspace_id": ws,
		"addons": []map[string]any{{"kind": "redis"}}}), &two)
	if len(two.Addons) != 1 {
		t.Fatalf("add-on not attached: %+v", two)
	}
	var site siteDTO
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/sites", map[string]any{"name": "Docs", "workspace_id": ws}), &site)
	keep := owner.createBot("personal-bot")

	owner.mustStatus(403, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio"})
	expect400(t, admin, "POST", adminDeletePath(ws), map[string]any{"confirm": "studio"}, "type the workspace's name exactly")
	expect400(t, admin, "POST", adminDeletePath(personalWS(t, e, "lead@x.io")), map[string]any{"confirm": "Personal"}, "delete the account instead")
	admin.mustStatus(404, "POST", adminDeletePath("11111111-1111-4111-8111-111111111111"), map[string]any{"confirm": "x"})
	// Nothing changed yet.
	owner.mustStatus(200, "GET", "/api/v1/bots/"+one, nil)
	owner.mustStatus(200, "GET", "/api/v1/sites/"+site.ID, nil)

	admin.mustStatus(204, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio"})
	for _, p := range []string{"/api/v1/bots/" + one, "/api/v1/bots/" + two.ID, "/api/v1/sites/" + site.ID, "/api/v1/workspaces/" + ws} {
		owner.mustStatus(404, "GET", p, nil)
		admin.mustStatus(404, "GET", p, nil)
	}
	admin.mustStatus(404, "GET", "/api/v1/admin/workspaces/"+ws, nil)
	admin.mustStatus(404, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio"})
	owner.mustStatus(200, "GET", "/api/v1/bots/"+keep, nil)
	var list wsList
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/workspaces", nil), &list)
	if len(list.Workspaces) != 1 || !list.Workspaces[0].Personal {
		t.Fatalf("owner keeps %+v", list.Workspaces)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM bots WHERE workspace_id = ?`, ws).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bot rows linger", n)
	}

	var target string
	e.db.QueryRow(`SELECT COALESCE(target, '') FROM audit_events WHERE action = 'admin.workspace_delete' AND outcome = 'ok'`).Scan(&target)
	if want := fmt.Sprintf("%s %q apps=2 sites=1 members=2", ws, "Studio"); target != want {
		t.Fatalf("audit target %q want %q", target, want)
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'admin.workspace_delete' AND outcome != 'ok'`).Scan(&n)
	if n < 3 {
		t.Fatalf("refusals recorded: %d", n)
	}
}

// A custom role holding only workspaces.manage administers workspaces
// (list, rename, members, delete) but sets no limits and opens no app.
func TestWorkspacesManageDelegated(t *testing.T) {
	e := newEnv(t)
	admin := e.user("root@x.io", domain.RoleAdmin)
	owner := e.user("lead@x.io", domain.RoleUser)
	mgr := e.user("mgr@x.io", domain.RoleUser)
	ws := teamWS(owner, "Studio")
	bot := owner.createBotIn("one", ws)
	mgr.mustStatus(403, "GET", "/api/v1/admin/workspaces", nil)
	mgr.mustStatus(403, "PATCH", "/api/v1/admin/workspaces/"+ws, map[string]any{"name": "Nope"})
	mgr.mustStatus(403, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio"})

	rid := admin.createRole("Workspace managers", domain.PermWorkspacesManage)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("mgr@x.io"), map[string]any{"role": rid})
	var all wsList
	json.Unmarshal(mgr.mustStatus(200, "GET", "/api/v1/admin/workspaces", nil), &all)
	if len(all.Workspaces) != 4 {
		t.Fatalf("manager sees %d workspaces", len(all.Workspaces))
	}
	var detail struct {
		Bots []struct{ ID string }
	}
	json.Unmarshal(mgr.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+ws, nil), &detail)
	if len(detail.Bots) != 1 || detail.Bots[0].ID != bot {
		t.Fatalf("manager's view %+v", detail)
	}
	mgr.mustStatus(200, "GET", quotaPath(ws), nil)
	mgr.mustStatus(403, "PUT", quotaPath(ws), map[string]any{"max_bots": 1})
	mgr.mustStatus(403, "DELETE", quotaPath(ws), nil)
	// Managing a workspace does not open its apps.
	mgr.mustStatus(404, "GET", "/api/v1/bots/"+bot, nil)

	var renamed struct{ Workspace struct{ Name string } }
	json.Unmarshal(mgr.mustStatus(200, "PATCH", "/api/v1/admin/workspaces/"+ws, map[string]any{"name": "Studio 2"}), &renamed)
	if renamed.Workspace.Name != "Studio 2" {
		t.Fatalf("renamed to %q", renamed.Workspace.Name)
	}
	mgr.mustStatus(200, "PATCH", "/api/v1/admin/workspaces/"+personalWS(t, e, "lead@x.io"), map[string]any{"name": "Lead's own"})
	mgr.mustStatus(200, "PUT", "/api/v1/workspaces/"+ws+"/members", map[string]string{"email": "root@x.io", "role": "viewer"})
	expect400(t, mgr, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio"}, "type the workspace's name exactly")
	mgr.mustStatus(204, "POST", adminDeletePath(ws), map[string]any{"confirm": "Studio 2"})
	owner.mustStatus(404, "GET", "/api/v1/workspaces/"+ws, nil)
	owner.mustStatus(404, "GET", "/api/v1/bots/"+bot, nil)
	var list wsList
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/workspaces", nil), &list)
	if len(list.Workspaces) != 1 || list.Workspaces[0].Name != "Lead's own" {
		t.Fatalf("owner keeps %+v", list.Workspaces)
	}
}

// A workspace with an app on an offline remote node is left whole: nothing
// is deleted until the node is back (or gone).
func TestCascadeRefusesOfflineNode(t *testing.T) {
	e := newEnv(t)
	admin := e.user("root@x.io", domain.RoleAdmin)
	ws := teamWS(admin, "Edge")
	local := admin.createBotIn("local-bot", ws)
	remote := admin.createBotIn("edge-bot", ws)
	const node = "33333333-3333-4333-8333-333333333333"
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Edge agent', 'agent', NULL, 1, ?, ?)`, node, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE bots SET node_id = ? WHERE id = ?`, node, remote); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(id string) bool { return id == node }

	expect400(t, admin, "POST", adminDeletePath(ws), map[string]any{"confirm": "Edge"}, `1 apps run on node \"Edge agent\", which is offline`)
	var detail struct{ Bots []struct{ ID string } }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+ws, nil), &detail)
	if len(detail.Bots) != 2 {
		t.Fatalf("refusal removed apps: %+v", detail.Bots)
	}
	admin.mustStatus(200, "GET", "/api/v1/bots/"+local, nil)

	// With the node gone from the panel the deletion goes through.
	e.bots.RemoteNode = nil
	admin.mustStatus(204, "POST", adminDeletePath(ws), map[string]any{"confirm": "Edge"})
	admin.mustStatus(404, "GET", "/api/v1/bots/"+local, nil)
	admin.mustStatus(404, "GET", "/api/v1/bots/"+remote, nil)
}
