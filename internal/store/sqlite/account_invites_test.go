package sqlite

import (
	"bytes"
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// An invitation whose role, node or workspace vanished (or no longer
// qualifies) before the link is used still registers the account; only the
// setup with a live target is applied.
func TestUseAccountInviteSetupSkipsVanishedTargets(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	if err := db.EnsureLocalNode(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(ctx, domain.User{ID: "admin", Email: "admin@x.io", Role: domain.RoleAdmin, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRole(ctx, domain.Role{ID: "r1", Name: "Limited", Permissions: []string{domain.PermBotsConsole}, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []struct {
		id      string
		enabled int
	}{{"n1", 1}, {"n2", 0}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
			VALUES (?, ?, ?, 'agent', NULL, ?, 1, 1)`, n.id, domain.LocalLocationID, n.id, n.enabled); err != nil {
			t.Fatal(err)
		}
	}
	team := domain.Workspace{ID: "11111111-1111-4111-8111-111111111111", Name: "Team", OwnerID: "admin", CreatedAtMS: 1, UpdatedAtMS: 1}
	if err := db.CreateWorkspace(ctx, team); err != nil {
		t.Fatal(err)
	}
	h1, h2 := bytes.Repeat([]byte("1"), 32), bytes.Repeat([]byte("2"), 32)
	one := int64(1)
	vanished := domain.AccountInvite{ID: "i1", Role: domain.RoleUser, RoleID: "r1", Quota: &domain.WorkspaceQuota{MaxBots: &one},
		NodeID: "n1", WorkspaceID: team.ID, WorkspaceRole: domain.WorkspaceDeveloper, CreatedBy: "admin", CreatedAtMS: 1, ExpiresAtMS: 1 << 60}
	if err := db.InsertAccountInvite(ctx, vanished, h1); err != nil {
		t.Fatal(err)
	}
	// Setup whose target still exists but no longer qualifies: a disabled
	// node and a personal workspace.
	adminPersonal, err := db.PersonalWorkspace(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	stale := domain.AccountInvite{ID: "i2", Role: domain.RoleUser, NodeID: "n2", WorkspaceID: adminPersonal.ID, WorkspaceRole: domain.WorkspaceViewer,
		CreatedBy: "admin", CreatedAtMS: 1, ExpiresAtMS: 1 << 60}
	if err := db.InsertAccountInvite(ctx, stale, h2); err != nil {
		t.Fatal(err)
	}

	// Everything the first invitation points at goes away.
	for _, q := range []string{`DELETE FROM roles WHERE id = 'r1'`, `DELETE FROM nodes WHERE id = 'n1'`, `DELETE FROM workspaces WHERE id = '` + team.ID + `'`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.AccountInviteByHash(ctx, h1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.RoleID != "" || got.NodeID != "" || got.WorkspaceID != "" || got.RoleName != "User" || got.Quota == nil || *got.Quota.MaxBots != 1 {
		t.Fatalf("after deletions: %+v", got)
	}

	u := domain.User{ID: "new", Email: "new@x.io", Role: domain.RoleUser, CreatedAtMS: 2, UpdatedAtMS: 2}
	if err := db.UseAccountInvite(ctx, h1, u, 2); err != nil {
		t.Fatalf("register with vanished targets: %v", err)
	}
	stored, err := db.GetUserByID(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := db.PersonalWorkspace(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	var nodeID, updatedBy string
	var members int
	db.QueryRowContext(ctx, `SELECT COALESCE(node_id, '') FROM workspaces WHERE id = ?`, personal.ID).Scan(&nodeID)
	db.QueryRowContext(ctx, `SELECT COALESCE(updated_by, '') FROM workspace_quotas WHERE workspace_id = ?`, personal.ID).Scan(&updatedBy)
	db.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE user_id = 'new'`).Scan(&members)
	if stored.Role != domain.RoleUser || stored.RoleID != "" || nodeID != "" || members != 1 {
		t.Fatalf("setup applied for vanished targets: role_id %q node %q members %d", stored.RoleID, nodeID, members)
	}
	// Limits have no target to vanish and are applied, in the inviter's name.
	q, err := db.GetWorkspaceQuota(ctx, personal.ID)
	if err != nil || q.MaxBots == nil || *q.MaxBots != 1 || updatedBy != "admin" {
		t.Fatalf("quota %+v (%v), updated_by %q", q, err, updatedBy)
	}

	u2 := domain.User{ID: "new2", Email: "new2@x.io", Role: domain.RoleUser, CreatedAtMS: 3, UpdatedAtMS: 3}
	if err := db.UseAccountInvite(ctx, h2, u2, 3); err != nil {
		t.Fatalf("register with stale targets: %v", err)
	}
	personal2, _ := db.PersonalWorkspace(ctx, "new2")
	db.QueryRowContext(ctx, `SELECT COALESCE(node_id, '') FROM workspaces WHERE id = ?`, personal2.ID).Scan(&nodeID)
	db.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE user_id = 'new2'`).Scan(&members)
	if nodeID != "" || members != 1 {
		t.Fatalf("stale setup applied: node %q members %d", nodeID, members)
	}
}
