package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const quotaCols = `workspace_id, max_apps, max_bots, max_games, max_web_apps, max_sites, memory_bytes, nano_cpus, updated_by, created_at_ms, updated_at_ms`

type scanner interface{ Scan(dest ...any) error }

func scanQuota(r scanner) (domain.WorkspaceQuota, error) {
	var q domain.WorkspaceQuota
	err := r.Scan(&q.WorkspaceID, &q.MaxApps, &q.MaxBots, &q.MaxGames, &q.MaxWebApps, &q.MaxSites, &q.MemoryBytes, &q.NanoCPUs,
		&q.UpdatedBy, &q.CreatedAtMS, &q.UpdatedAtMS)
	return q, err
}

// GetWorkspaceQuota returns a workspace's limits, or domain.ErrNotFound when
// it has none (the panel defaults apply).
func (db *DB) GetWorkspaceQuota(ctx context.Context, workspaceID string) (domain.WorkspaceQuota, error) {
	q, err := scanQuota(db.QueryRowContext(ctx, `SELECT `+quotaCols+` FROM workspace_quotas WHERE workspace_id = ?`, workspaceID))
	return q, mapErr(err)
}

// ListWorkspaceQuotas returns every workspace's limits by workspace id.
func (db *DB) ListWorkspaceQuotas(ctx context.Context) (map[string]domain.WorkspaceQuota, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+quotaCols+` FROM workspace_quotas`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.WorkspaceQuota{}
	for rows.Next() {
		q, err := scanQuota(rows)
		if err != nil {
			return nil, err
		}
		out[q.WorkspaceID] = q
	}
	return out, rows.Err()
}

// SetWorkspaceQuota stores a workspace's limits, replacing every field.
func (db *DB) SetWorkspaceQuota(ctx context.Context, q domain.WorkspaceQuota) error {
	_, err := db.ExecContext(ctx, `INSERT INTO workspace_quotas (`+quotaCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(workspace_id) DO UPDATE SET max_apps = excluded.max_apps, max_bots = excluded.max_bots,
			max_games = excluded.max_games, max_web_apps = excluded.max_web_apps, max_sites = excluded.max_sites,
			memory_bytes = excluded.memory_bytes, nano_cpus = excluded.nano_cpus, updated_by = excluded.updated_by,
			updated_at_ms = excluded.updated_at_ms`,
		q.WorkspaceID, q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes, q.NanoCPUs, q.UpdatedBy, q.CreatedAtMS, q.UpdatedAtMS)
	return mapErr(err)
}

// DeleteWorkspaceQuota removes a workspace's limits (the panel defaults apply again).
func (db *DB) DeleteWorkspaceQuota(ctx context.Context, workspaceID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM workspace_quotas WHERE workspace_id = ?`, workspaceID)
	return err
}
