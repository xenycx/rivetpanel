package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// CreatePasswordReset stores a reset link for a user, replacing any earlier
// one, and drops expired links. It returns domain.ErrConflict when the user
// already asked for one less than cooldownMS ago.
func (db *DB) CreatePasswordReset(ctx context.Context, id, userID string, hash []byte, nowMS, expiresMS, cooldownMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE expires_at_ms <= ?`, nowMS); err != nil {
		return err
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(created_at_ms) FROM password_resets WHERE user_id = ?`, userID).Scan(&last); err != nil {
		return err
	}
	if last.Valid && nowMS-last.Int64 < cooldownMS {
		return domain.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO password_resets (id, user_id, token_hash, created_at_ms, expires_at_ms) VALUES (?,?,?,?,?)`,
		id, userID, hash, nowMS, expiresMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

// ConsumePasswordReset atomically uses up a reset link and returns its user.
// An unknown, used or expired link is domain.ErrNotFound.
func (db *DB) ConsumePasswordReset(ctx context.Context, hash []byte, nowMS int64) (string, error) {
	var userID string
	err := db.QueryRowContext(ctx, `DELETE FROM password_resets WHERE token_hash = ? AND expires_at_ms > ? RETURNING user_id`, hash, nowMS).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return userID, err
}

// UserEmailAlerts reports whether a user wants alert emails.
func (db *DB) UserEmailAlerts(ctx context.Context, userID string) (bool, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT email_alerts FROM users WHERE id = ?`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, domain.ErrNotFound
	}
	return v == 1, err
}

// SetUserEmailAlerts turns alert emails on or off for a user.
func (db *DB) SetUserEmailAlerts(ctx context.Context, userID string, on bool) error {
	res, err := db.ExecContext(ctx, `UPDATE users SET email_alerts = ? WHERE id = ?`, boolInt(on), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UserEmailNews reports whether a user wants optional news emails.
func (db *DB) UserEmailNews(ctx context.Context, userID string) (bool, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT email_news FROM users WHERE id = ?`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, domain.ErrNotFound
	}
	return v == 1, err
}

// SetUserEmailNews turns optional news emails on or off for a user.
func (db *DB) SetUserEmailNews(ctx context.Context, userID string, on bool) error {
	res, err := db.ExecContext(ctx, `UPDATE users SET email_news = ? WHERE id = ?`, boolInt(on), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListMailRecipients returns every enabled account (at most 5000).
func (db *DB) ListMailRecipients(ctx context.Context) ([]domain.MailRecipient, error) {
	rows, err := db.QueryContext(ctx, `SELECT email, role = 'admin', email_news = 1 FROM users WHERE disabled = 0 ORDER BY created_at_ms LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MailRecipient
	for rows.Next() {
		var r domain.MailRecipient
		if err := rows.Scan(&r.Email, &r.Admin, &r.News); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
