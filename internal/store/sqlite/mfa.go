package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// GetMFA returns a user's enrollment (enabled or pending).
func (db *DB) GetMFA(ctx context.Context, userID string) (domain.MFA, error) {
	var m domain.MFA
	err := db.QueryRowContext(ctx, `SELECT user_id, secret_cipher, secret_nonce, secret_key_id, enabled_at_ms, last_step, created_at_ms,
		(SELECT count(*) FROM mfa_recovery_codes r WHERE r.user_id = m.user_id AND r.used_at_ms IS NULL)
		FROM user_mfa m WHERE user_id = ?`, userID).
		Scan(&m.UserID, &m.Cipher, &m.Nonce, &m.KeyID, &m.EnabledAtMS, &m.LastStep, &m.CreatedAtMS, &m.RecoveryLeft)
	return m, mapErr(err)
}

// MFAEnabled reports whether two-step sign-in is on for a user.
func (db *DB) MFAEnabled(ctx context.Context, userID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_mfa WHERE user_id = ? AND enabled_at_ms IS NOT NULL`, userID).Scan(&n)
	return n > 0, err
}

// StartMFA stores a pending enrollment, replacing an earlier pending one. An
// enabled enrollment is never replaced (disable it first).
func (db *DB) StartMFA(ctx context.Context, m domain.MFA) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		var enabled sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT enabled_at_ms FROM user_mfa WHERE user_id = ?`, m.UserID).Scan(&enabled)
		if err == nil && enabled.Valid {
			return domain.ErrConflict
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_mfa (user_id, secret_cipher, secret_nonce, secret_key_id, created_at_ms)
			VALUES (?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET secret_cipher = excluded.secret_cipher,
			secret_nonce = excluded.secret_nonce, secret_key_id = excluded.secret_key_id, last_step = 0,
			created_at_ms = excluded.created_at_ms`, m.UserID, m.Cipher, m.Nonce, m.KeyID, m.CreatedAtMS)
		return err
	})
}

// UseTOTPStep records a verified step; it fails (false) if that step or a
// later one was already used, which makes each code single-use.
func (db *DB) UseTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE user_mfa SET last_step = ? WHERE user_id = ? AND last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// EnableMFA turns a pending enrollment on and replaces the recovery codes.
func (db *DB) EnableMFA(ctx context.Context, userID string, codeHashes [][]byte, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE user_mfa SET enabled_at_ms = ? WHERE user_id = ? AND enabled_at_ms IS NULL`, nowMS, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return domain.ErrNotFound
		}
		return replaceCodes(ctx, tx, userID, codeHashes)
	})
}

// ReplaceRecoveryCodes swaps every recovery code for a new set.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes [][]byte) error {
	return db.tx(ctx, func(tx *sql.Tx) error { return replaceCodes(ctx, tx, userID, codeHashes) })
}

func replaceCodes(ctx context.Context, tx *sql.Tx, userID string, hashes [][]byte) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			return err
		}
	}
	return nil
}

// UseRecoveryCode consumes an unused code; false when unknown or used.
func (db *DB) UseRecoveryCode(ctx context.Context, userID string, hash []byte, nowMS int64) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE mfa_recovery_codes SET used_at_ms = ? WHERE user_id = ? AND code_hash = ? AND used_at_ms IS NULL`,
		nowMS, userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeleteMFA removes the enrollment and every recovery code.
func (db *DB) DeleteMFA(ctx context.Context, userID string) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM user_mfa WHERE user_id = ?`, userID)
		return err
	})
}

// tx runs fn in one immediate write transaction.
func (db *DB) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return mapErr(err)
	}
	return tx.Commit()
}
