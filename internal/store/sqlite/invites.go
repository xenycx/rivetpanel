package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const inviteCols = `i.id, i.bot_id, b.name, i.permissions, i.created_by, COALESCE(u.email, ''), i.created_at_ms, i.expires_at_ms, i.used_by, i.used_at_ms`
const inviteFrom = ` FROM bot_invites i JOIN bots b ON b.id = i.bot_id LEFT JOIN users u ON u.id = i.created_by`

func scanInvite(row interface{ Scan(...any) error }) (domain.Invite, error) {
	var v domain.Invite
	err := row.Scan(&v.ID, &v.BotID, &v.BotName, &v.Permissions, &v.CreatedBy, &v.CreatorName, &v.CreatedAtMS, &v.ExpiresAtMS, &v.UsedBy, &v.UsedAtMS)
	return v, mapErr(err)
}

// InsertInvite stores a hashed invitation, keeping at most 20 open per bot.
func (db *DB) InsertInvite(ctx context.Context, v domain.Invite, hash []byte, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bot_invites WHERE bot_id = ? AND used_at_ms IS NULL AND expires_at_ms > ?`,
			v.BotID, nowMS).Scan(&open); err != nil {
			return err
		}
		if open >= 20 {
			return domain.Invalid("this bot has 20 open invitations; revoke one first")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO bot_invites (id, bot_id, token_hash, permissions, created_by, created_at_ms, expires_at_ms)
			VALUES (?,?,?,?,?,?,?)`, v.ID, v.BotID, hash, v.Permissions, v.CreatedBy, v.CreatedAtMS, v.ExpiresAtMS)
		return err
	})
}

// ListInvites returns a bot's invitations that are still open.
func (db *DB) ListInvites(ctx context.Context, botID string, nowMS int64) ([]domain.Invite, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+inviteCols+inviteFrom+` WHERE i.bot_id = ? AND i.used_at_ms IS NULL AND i.expires_at_ms > ?
		ORDER BY i.created_at_ms DESC LIMIT 50`, botID, nowMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Invite
	for rows.Next() {
		v, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InviteByHash returns an open invitation.
func (db *DB) InviteByHash(ctx context.Context, hash []byte, nowMS int64) (domain.Invite, error) {
	return scanInvite(db.QueryRowContext(ctx, `SELECT `+inviteCols+inviteFrom+` WHERE i.token_hash = ? AND i.used_at_ms IS NULL
		AND i.expires_at_ms > ? AND b.desired_state != 'deleted'`, hash, nowMS))
}

// AcceptInvite consumes an open invitation and grants its permissions, in one
// transaction, so a link works exactly once.
func (db *DB) AcceptInvite(ctx context.Context, hash []byte, userID string, nowMS int64) (domain.Invite, error) {
	var v domain.Invite
	err := db.tx(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteCols+inviteFrom+` WHERE i.token_hash = ? AND i.used_at_ms IS NULL
			AND i.expires_at_ms > ? AND b.desired_state != 'deleted'`, hash, nowMS))
		if err != nil {
			return err
		}
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM bots WHERE id = ?`, v.BotID).Scan(&owner); err != nil {
			return err
		}
		if owner == userID {
			return domain.Invalid("you already own this bot")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bot_invites SET used_by = ?, used_at_ms = ? WHERE id = ?`, userID, nowMS, v.ID); err != nil {
			return err
		}
		// An existing grant is widened, never narrowed, by an invitation.
		_, err = tx.ExecContext(ctx, `INSERT INTO bot_subusers (bot_id, user_id, permissions, invited_by, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?) ON CONFLICT(bot_id, user_id) DO UPDATE SET permissions = bot_subusers.permissions | excluded.permissions,
			updated_at_ms = excluded.updated_at_ms`, v.BotID, userID, v.Permissions, v.CreatedBy, nowMS, nowMS)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return v, domain.Invalid("this invitation link was already used, expired or revoked")
	}
	return v, err
}

// DeleteInvite revokes an open invitation.
func (db *DB) DeleteInvite(ctx context.Context, botID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM bot_invites WHERE id = ? AND bot_id = ? AND used_at_ms IS NULL`, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
