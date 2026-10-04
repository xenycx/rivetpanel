package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// CreateEmailVerification stores a verification link for a user, replacing
// any earlier one, and drops expired links. It returns domain.ErrConflict
// when the user asked for one less than cooldownMS ago.
func (db *DB) CreateEmailVerification(ctx context.Context, id, userID, email string, hash []byte, nowMS, expiresMS, cooldownMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM email_verifications WHERE expires_at_ms <= ?`, nowMS); err != nil {
			return err
		}
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT max(created_at_ms) FROM email_verifications WHERE user_id = ?`, userID).Scan(&last); err != nil {
			return err
		}
		if last.Valid && nowMS-last.Int64 < cooldownMS {
			return domain.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO email_verifications (id, user_id, token_hash, email, created_at_ms, expires_at_ms) VALUES (?,?,?,?,?,?)`,
			id, userID, hash, email, nowMS, expiresMS)
		return err
	})
}

// PendingEmailVerification returns the address and creation time of the
// account's outstanding link (domain.ErrNotFound when there is none).
func (db *DB) PendingEmailVerification(ctx context.Context, userID string, nowMS int64) (string, int64, error) {
	var email string
	var at int64
	err := db.QueryRowContext(ctx, `SELECT email, created_at_ms FROM email_verifications WHERE user_id = ? AND expires_at_ms > ?
		ORDER BY created_at_ms DESC LIMIT 1`, userID, nowMS).Scan(&email, &at)
	return email, at, mapErr(err)
}

// ErrEmailTaken refuses confirming an address another account now uses.
var ErrEmailTaken = domain.Invalid("another account already uses that email address")

// ConsumeEmailVerification uses up a link (single use) and confirms its
// address in one transaction: the current address is marked verified; a
// different address (an email change) replaces the account's address, is
// marked verified, and pending password-reset links (sent to the old
// address) are dropped. It returns the account and its previous address.
// An unknown, used or expired link is domain.ErrNotFound.
func (db *DB) ConsumeEmailVerification(ctx context.Context, hash []byte, nowMS int64) (userID, oldEmail, newEmail string, err error) {
	err = db.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `DELETE FROM email_verifications WHERE token_hash = ? AND expires_at_ms > ? RETURNING user_id, email`,
			hash, nowMS).Scan(&userID, &newEmail); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		var dis int
		if err := tx.QueryRowContext(ctx, `SELECT email, disabled FROM users WHERE id = ?`, userID).Scan(&oldEmail, &dis); err != nil {
			return err
		}
		if dis == 1 {
			return domain.ErrNotFound
		}
		if !strings.EqualFold(oldEmail, newEmail) {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email = ? AND id != ?`, newEmail, userID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrEmailTaken
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = ?`, userID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, email_verified = 1, email_verified_at_ms = ?, updated_at_ms = ? WHERE id = ?`,
			newEmail, nowMS, nowMS, userID)
		return err
	})
	return userID, oldEmail, newEmail, err
}

// SetUserEmail changes an account's address (administrators). The new
// address is unverified; outstanding verification and reset links are
// dropped. A duplicate address is domain.ErrConflict.
func (db *DB) SetUserEmail(ctx context.Context, id, email string, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, email_verified = 0, email_verified_at_ms = NULL, updated_at_ms = ? WHERE id = ?`, email, nowMS, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM email_verifications WHERE user_id = ?`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = ?`, id)
		return err
	})
}

// SetEmailVerified marks an address verified or unverified (administrators).
func (db *DB) SetEmailVerified(ctx context.Context, id string, verified bool, nowMS int64) error {
	var at any
	if verified {
		at = nowMS
	}
	res, err := db.ExecContext(ctx, `UPDATE users SET email_verified = ?, email_verified_at_ms = ?, updated_at_ms = ? WHERE id = ?`, boolInt(verified), at, nowMS, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	if verified {
		_, err = db.ExecContext(ctx, `DELETE FROM email_verifications WHERE user_id = ? AND email = (SELECT email FROM users WHERE id = ?)`, id, id)
	}
	return err
}
