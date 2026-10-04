package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const accountInviteCols = `id, COALESCE(email,''), role, created_by, created_at_ms, expires_at_ms`

func scanAccountInvite(row interface{ Scan(...any) error }) (domain.AccountInvite, error) {
	var v domain.AccountInvite
	err := row.Scan(&v.ID, &v.Email, &v.Role, &v.CreatedBy, &v.CreatedAtMS, &v.ExpiresAtMS)
	return v, mapErr(err)
}

func (db *DB) InsertAccountInvite(ctx context.Context, v domain.AccountInvite, hash []byte) error {
	_, err := db.ExecContext(ctx, `INSERT INTO account_invites (id,token_hash,email,role,created_by,created_at_ms,expires_at_ms) VALUES (?,?,?,?,?,?,?)`,
		v.ID, hash, nullString(v.Email), v.Role, v.CreatedBy, v.CreatedAtMS, v.ExpiresAtMS)
	return mapErr(err)
}

func (db *DB) ListAccountInvites(ctx context.Context, nowMS int64) ([]domain.AccountInvite, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+accountInviteCols+` FROM account_invites WHERE used_at_ms IS NULL AND expires_at_ms > ? ORDER BY created_at_ms DESC LIMIT 100`, nowMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AccountInvite
	for rows.Next() {
		v, err := scanAccountInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (db *DB) AccountInviteByHash(ctx context.Context, hash []byte, nowMS int64) (domain.AccountInvite, error) {
	return scanAccountInvite(db.QueryRowContext(ctx, `SELECT `+accountInviteCols+` FROM account_invites WHERE token_hash=? AND used_at_ms IS NULL AND expires_at_ms>?`, hash, nowMS))
}

func (db *DB) UseAccountInvite(ctx context.Context, hash []byte, u domain.User, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var email sql.NullString
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT email,role FROM account_invites WHERE token_hash=? AND used_at_ms IS NULL AND expires_at_ms>?`, hash, nowMS).Scan(&email, &role); err != nil {
		return mapErr(err)
	}
	if email.Valid && email.String != u.Email {
		return domain.Invalid("this invitation was sent to a different email address")
	}
	u.Role = role
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`, u.ID, u.Email, u.DisplayName, u.AvatarJPEG, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAtMS, u.UpdatedAtMS, boolInt(u.EmailVerified)); err != nil {
		return mapErr(err)
	}
	if err := createPersonalWorkspace(ctx, tx, u); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE account_invites SET used_by=?,used_at_ms=? WHERE token_hash=? AND used_at_ms IS NULL`, u.ID, nowMS, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.ErrConflict
	}
	return tx.Commit()
}

func (db *DB) DeleteAccountInvite(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM account_invites WHERE id=? AND used_at_ms IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
