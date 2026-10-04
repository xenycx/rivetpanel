package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SetSubUser grants or updates a user's permissions on a bot. The owner cannot
// be a sub-user.
func (db *DB) SetSubUser(ctx context.Context, s domain.SubUser) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerID string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM bots WHERE id = ?`, s.BotID).Scan(&ownerID); err != nil {
		return mapErr(err)
	}
	if ownerID == s.UserID {
		return domain.Invalid("the owner already has full access")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bot_subusers
		(bot_id, user_id, permissions, invited_by, created_at_ms, updated_at_ms) VALUES (?,?,?,?,?,?)
		ON CONFLICT(bot_id, user_id) DO UPDATE SET permissions = excluded.permissions,
			updated_at_ms = excluded.updated_at_ms`,
		s.BotID, s.UserID, s.Permissions, s.InvitedBy, s.CreatedAtMS, s.UpdatedAtMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

func (db *DB) RemoveSubUser(ctx context.Context, botID, userID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM bot_subusers WHERE bot_id = ? AND user_id = ?`, botID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetSubUserPermissions returns the mask, or domain.ErrNotFound.
func (db *DB) GetSubUserPermissions(ctx context.Context, botID, userID string) (int, error) {
	var p int
	err := db.QueryRowContext(ctx, `SELECT permissions FROM bot_subusers WHERE bot_id = ? AND user_id = ?`, botID, userID).Scan(&p)
	return p, mapErr(err)
}

func (db *DB) ListSubUsers(ctx context.Context, botID string) ([]domain.SubUser, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.bot_id, s.user_id, u.email, s.permissions, s.invited_by, s.created_at_ms, s.updated_at_ms
		FROM bot_subusers s JOIN users u ON u.id = s.user_id WHERE s.bot_id = ? ORDER BY u.email LIMIT 200`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SubUser
	for rows.Next() {
		var s domain.SubUser
		if err := rows.Scan(&s.BotID, &s.UserID, &s.Email, &s.Permissions, &s.InvitedBy, &s.CreatedAtMS, &s.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// InsertAPIKey stores a hashed key.
func (db *DB) InsertAPIKey(ctx context.Context, k domain.APIKey) error {
	_, err := db.ExecContext(ctx, `INSERT INTO api_keys (id, user_id, name, prefix, token_hash, scope, created_at_ms, expires_at_ms)
		VALUES (?,?,?,?,?,?,?,?)`, k.ID, k.UserID, k.Name, k.Prefix, k.TokenHash, k.Scope, k.CreatedAtMS, k.ExpiresAtMS)
	return mapErr(err)
}

// GetUserByAPIKey resolves an unexpired key to an enabled user and records use.
func (db *DB) GetUserByAPIKey(ctx context.Context, hash []byte, scope string, nowMS int64) (domain.User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userSelect("u")+`
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.token_hash = ? AND k.scope = ? AND u.disabled = 0
		  AND (k.expires_at_ms IS NULL OR k.expires_at_ms > ?)`, hash, scope, nowMS))
	if err != nil {
		return u, err
	}
	_, err = db.ExecContext(ctx, `UPDATE api_keys SET last_used_at_ms = ? WHERE token_hash = ?`, nowMS, hash)
	return u, err
}

// APIKeyValid reports whether an unexpired key with this hash exists for the
// user, without recording use (for periodic revalidation).
func (db *DB) APIKeyValid(ctx context.Context, hash []byte, userID, scope string, nowMS int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM api_keys WHERE token_hash = ? AND user_id = ? AND scope = ?
		AND (expires_at_ms IS NULL OR expires_at_ms > ?)`, hash, userID, scope, nowMS).Scan(&n)
	return n > 0, err
}

func (db *DB) ListAPIKeys(ctx context.Context, userID string) ([]domain.APIKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, name, prefix, scope, created_at_ms, last_used_at_ms, expires_at_ms
		FROM api_keys WHERE user_id = ? ORDER BY created_at_ms LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIKey
	for rows.Next() {
		var k domain.APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.Scope, &k.CreatedAtMS, &k.LastUsedAtMS, &k.ExpiresAtMS); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (db *DB) DeleteAPIKey(ctx context.Context, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// TransferBot makes newOwner the owner of a bot in one transaction and moves
// it into their personal workspace. The new
// owner's own sub-user grant becomes redundant and is removed; the previous
// owner optionally keeps full access as a sub-user. The GitHub link is
// removed because its token belongs to the previous owner; it reports whether
// one was removed.
func (db *DB) TransferBot(ctx context.Context, botID, newOwner string, keepPrevious bool, nowMS int64) (hadRepo bool, err error) {
	if _, err := db.PersonalWorkspace(ctx, newOwner); err != nil {
		return false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var prev, desired string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id, desired_state FROM bots WHERE id = ?`, botID).Scan(&prev, &desired); err != nil {
		return false, mapErr(err)
	}
	if desired == domain.DesiredDeleted {
		return false, domain.ErrNotFound
	}
	if prev == newOwner {
		return false, domain.Invalid("that account already owns the bot")
	}
	// The bot moves into the new owner's personal workspace: staying in the
	// previous owner's workspace would keep their access through membership.
	if _, err := tx.ExecContext(ctx, `UPDATE bots SET owner_id = ?1, updated_at_ms = ?2,
		workspace_id = (SELECT id FROM workspaces WHERE owner_id = ?1 AND personal = 1) WHERE id = ?3`, newOwner, nowMS, botID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_subusers WHERE bot_id = ? AND user_id = ?`, botID, newOwner); err != nil {
		return false, err
	}
	if keepPrevious {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_subusers (bot_id, user_id, permissions, invited_by, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?) ON CONFLICT(bot_id, user_id) DO UPDATE SET permissions = excluded.permissions, updated_at_ms = excluded.updated_at_ms`,
			botID, prev, domain.PermAll, newOwner, nowMS, nowMS); err != nil {
			return false, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM github_repos WHERE bot_id = ?`, botID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, tx.Commit()
}
