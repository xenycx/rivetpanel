package sqlite

import (
	"context"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// EnsureLocalNode idempotently registers the local node under a stable ID.
// It never overwrites an existing row (e.g. an administrator-disabled node).
func (db *DB) EnsureLocalNode(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := db.ExecContext(ctx, `INSERT INTO nodes
		(id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, 'local', NULL, 1, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		domain.LocalNodeID, domain.LocalLocationID, domain.LocalNodeName, now, now)
	return err
}

// GetNode returns a node by ID; sql.ErrNoRows if absent.
func (db *DB) GetNode(ctx context.Context, id string) (domain.Node, error) {
	var n domain.Node
	var enabled int
	err := db.QueryRowContext(ctx,
		`SELECT id, location_id, name, transport, endpoint, enabled, last_seen_at_ms, draining, public_address FROM nodes WHERE id = ?`, id).
		Scan(&n.ID, &n.LocationID, &n.Name, &n.Transport, &n.Endpoint, &enabled, &n.LastSeenMS, &n.Draining, &n.PublicAddress)
	n.Enabled = enabled == 1
	return n, mapErr(err)
}
