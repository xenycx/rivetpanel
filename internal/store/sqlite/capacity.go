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

// AccountUsage is what an account holds against its quota: bots not deleted
// by kind (stopped ones count, so stopped apps cannot later all be started
// past a limit), their memory limits with add-ons, and its sites.
func (db *DB) AccountUsage(ctx context.Context, ownerID string) (domain.OwnerUsage, error) {
	return ownerUsage(ctx, db, ownerID)
}

func ownerUsage(ctx context.Context, db rowQuerier, ownerID string) (u domain.OwnerUsage, err error) {
	err = db.QueryRowContext(ctx, `SELECT count(*),
		COALESCE(SUM(kind = 'bot'), 0), COALESCE(SUM(kind = 'game'), 0), COALESCE(SUM(kind = 'web'), 0),
		COALESCE(SUM(`+botMemory+`), 0), (SELECT count(*) FROM sites WHERE owner_id = ?1)
		FROM bots WHERE owner_id = ?1 AND desired_state != 'deleted'`, ownerID).
		Scan(&u.Apps, &u.Bots, &u.Games, &u.Web, &u.Memory, &u.Sites)
	return
}
