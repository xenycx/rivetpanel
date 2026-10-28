package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const quotaCols = `user_id, max_apps, max_bots, max_games, max_web_apps, max_sites, memory_bytes, updated_by, created_at_ms, updated_at_ms`

// GetUserQuota returns an account's quota, or domain.ErrNotFound when it has
// none (the panel defaults apply).
func (db *DB) GetUserQuota(ctx context.Context, userID string) (domain.UserQuota, error) {
	var q domain.UserQuota
	err := db.QueryRowContext(ctx, `SELECT `+quotaCols+` FROM user_quotas WHERE user_id = ?`, userID).
		Scan(&q.UserID, &q.MaxApps, &q.MaxBots, &q.MaxGames, &q.MaxWebApps, &q.MaxSites, &q.MemoryBytes, &q.UpdatedBy, &q.CreatedAtMS, &q.UpdatedAtMS)
	return q, mapErr(err)
}

// SetUserQuota stores an account's quota, replacing every field.
func (db *DB) SetUserQuota(ctx context.Context, q domain.UserQuota) error {
	_, err := db.ExecContext(ctx, `INSERT INTO user_quotas (`+quotaCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET max_apps = excluded.max_apps, max_bots = excluded.max_bots,
			max_games = excluded.max_games, max_web_apps = excluded.max_web_apps, max_sites = excluded.max_sites,
			memory_bytes = excluded.memory_bytes, updated_by = excluded.updated_by, updated_at_ms = excluded.updated_at_ms`,
		q.UserID, q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes, q.UpdatedBy, q.CreatedAtMS, q.UpdatedAtMS)
	return mapErr(err)
}

// DeleteUserQuota removes an account's quota (the panel defaults apply again).
func (db *DB) DeleteUserQuota(ctx context.Context, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM user_quotas WHERE user_id = ?`, userID)
	return err
}
