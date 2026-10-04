package api

import (
	"encoding/json"
	"testing"

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
