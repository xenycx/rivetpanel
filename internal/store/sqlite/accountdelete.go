package sqlite

import (
	"context"
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// AccountHoldings lists what stands between an account and its deletion:
// the bots (deleted ones still being removed included) and sites it owns,
// and the names of team workspaces it owns that hold other accounts' bots or
// sites (deleting the owner would delete those workspaces).
func (db *DB) AccountHoldings(ctx context.Context, userID string) (bots, sites, shared []string, err error) {
	if bots, err = db.strings(ctx, `SELECT id FROM bots WHERE owner_id = ? ORDER BY created_at_ms`, userID); err != nil {
		return
	}
	if sites, err = db.strings(ctx, `SELECT id FROM sites WHERE owner_id = ? ORDER BY created_at_ms`, userID); err != nil {
		return
	}
	shared, err = db.strings(ctx, `SELECT w.name FROM workspaces w WHERE w.owner_id = ?1 AND w.personal = 0
		AND (EXISTS (SELECT 1 FROM bots b WHERE b.workspace_id = w.id AND b.owner_id != ?1)
			OR EXISTS (SELECT 1 FROM sites s WHERE s.workspace_id = w.id AND s.owner_id != ?1))
		ORDER BY w.name`, userID)
	return
}

func (db *DB) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteUserAccount removes an account row once it owns no bots. In the same
// transaction it removes repository links made with its GitHub token (on
// other accounts' bots) and any sites it still owns; everything else that
// belongs to the account (sessions, keys, tokens, workspaces it owns,
// memberships, notifications, tickets, quota) goes by ON DELETE CASCADE, and
// references to it in history (operations, backups, releases) become NULL.
// The audit log keeps its events.
func (db *DB) DeleteUserAccount(ctx context.Context, userID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bots, shared int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM bots WHERE owner_id = ?1),
		(SELECT count(*) FROM workspaces w WHERE w.owner_id = ?1 AND w.personal = 0
			AND (EXISTS (SELECT 1 FROM bots b WHERE b.workspace_id = w.id AND b.owner_id != ?1)
				OR EXISTS (SELECT 1 FROM sites s WHERE s.workspace_id = w.id AND s.owner_id != ?1)))`, userID).Scan(&bots, &shared); err != nil {
		return err
	}
	if bots > 0 {
		return domain.Invalid(fmt.Sprintf("%d of the account's apps are still being removed (a remote node may be offline); the account stays disabled, try again once they are gone", bots))
	}
	if shared > 0 {
		return domain.Invalid("a workspace this account owns now holds other accounts' bots or sites; move those out first")
	}
	for _, q := range []string{
		`DELETE FROM github_repos WHERE token_user_id = ?`,
		`DELETE FROM sites WHERE owner_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return mapErr(err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}
