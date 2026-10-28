package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// botMemory is a bot's memory reservation: its own limit plus its add-ons'.
const botMemory = `(memory_bytes + (SELECT COALESCE(SUM(a.memory_bytes), 0) FROM bot_addons a WHERE a.bot_id = bots.id))`

// OwnerUsage counts a user's bots (not deleted) and the sum of their memory
// limits, add-ons included.
func (db *DB) OwnerUsage(ctx context.Context, ownerID string) (bots int, memory int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(`+botMemory+`), 0) FROM bots
		WHERE owner_id = ? AND desired_state != 'deleted'`, ownerID).Scan(&bots, &memory)
	return
}

// NodeReserved sums the memory limits (add-ons included) of bots wanted
// running on a node.
func (db *DB) NodeReserved(ctx context.Context, nodeID string) (running int, memory int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(`+botMemory+`), 0) FROM bots
		WHERE node_id = ? AND desired_state = 'running'`, nodeID).Scan(&running, &memory)
	return
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// WorkspaceUsage is what a workspace holds against its limits: apps not
// deleted by kind (stopped ones count, so stopped apps cannot later all be
// started past a limit), their memory limits with add-ons, their CPU limits,
// and its sites.
func (db *DB) WorkspaceUsage(ctx context.Context, workspaceID string) (domain.WorkspaceUsage, error) {
	return workspaceUsage(ctx, db, workspaceID)
}

func workspaceUsage(ctx context.Context, db rowQuerier, workspaceID string) (u domain.WorkspaceUsage, err error) {
	err = db.QueryRowContext(ctx, `SELECT count(*),
		COALESCE(SUM(kind = 'bot'), 0), COALESCE(SUM(kind = 'game'), 0), COALESCE(SUM(kind = 'web'), 0),
		COALESCE(SUM(`+botMemory+`), 0), COALESCE(SUM(nano_cpus), 0), (SELECT count(*) FROM sites WHERE workspace_id = ?1)
		FROM bots WHERE workspace_id = ?1 AND desired_state != 'deleted'`, workspaceID).
		Scan(&u.Apps, &u.Bots, &u.Games, &u.Web, &u.Memory, &u.CPU, &u.Sites)
	return
}
