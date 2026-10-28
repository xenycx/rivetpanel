package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// A workspace pin follows its node: set, read back on every workspace
// view, cleared explicitly, and cleared by the database when the node is
// removed (migration 0059: ON DELETE SET NULL).
func TestWorkspaceNodeUnpinsOnNodeDelete(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	seed(t, db)
	const node = "22222222-2222-4222-8222-222222222222"
	if _, err := db.ExecContext(ctx, `INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Edge', 'agent', NULL, 1, 1, 1)`, node, domain.LocalLocationID); err != nil {
		t.Fatal(err)
	}
	w, err := db.PersonalWorkspace(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if w.NodeID != "" {
		t.Fatalf("fresh workspace pinned to %q", w.NodeID)
	}
	if err := db.SetWorkspaceNode(ctx, "no-such-workspace", node, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown workspace: %v", err)
	}
	if err := db.SetWorkspaceNode(ctx, w.ID, node, 2); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (domain.Workspace, error){
		func() (domain.Workspace, error) { return db.GetWorkspace(ctx, w.ID) },
		func() (domain.Workspace, error) { return db.PersonalWorkspace(ctx, "u1") },
		func() (domain.Workspace, error) {
			s, err := db.WorkspaceSummaryFor(ctx, w.ID, "u1")
			return s.Workspace, err
		},
		func() (domain.Workspace, error) {
			all, err := db.ListAllWorkspaces(ctx, "u1")
			if err != nil || len(all) == 0 {
				return domain.Workspace{}, err
			}
			return all[0].Workspace, nil
		},
	} {
		got, err := read()
		if err != nil || got.NodeID != node {
			t.Fatalf("pin not read back: %q %v", got.NodeID, err)
		}
	}
	if err := db.SetWorkspaceNode(ctx, w.ID, "", 3); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetWorkspace(ctx, w.ID); got.NodeID != "" {
		t.Fatalf("pin not cleared: %q", got.NodeID)
	}
	if err := db.SetWorkspaceNode(ctx, w.ID, node, 4); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAgentNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetWorkspace(ctx, w.ID); err != nil || got.NodeID != "" {
		t.Fatalf("pin survived node deletion: %q %v", got.NodeID, err)
	}
}

// personalWorkspaceID returns (creating it when absent) a user's personal
// workspace with SQL that works on every schema since workspaces exist, for
// fixtures held at a historical version where the current store helpers
// select newer columns.
func personalWorkspaceID(t *testing.T, db *DB, userID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE owner_id = ? AND personal = 1`, userID).Scan(&id)
	if err == nil {
		return id
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("personal workspace of %s: %v", userID, err)
	}
	id = uuid.NewString()
	for _, q := range []string{
		`INSERT INTO workspaces (id, name, owner_id, personal, created_at_ms, updated_at_ms) VALUES (?, 'Personal', ?, 1, 1, 1)`,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at_ms, updated_at_ms) VALUES (?, ?, 'owner', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q, id, userID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return id
}
