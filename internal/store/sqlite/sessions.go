package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// MaxSessionsPerUser bounds stored sessions; the oldest are dropped.
const MaxSessionsPerUser = 20

// CreateSession stores a session (only the token hash) and drops the user's
// oldest sessions beyond the cap, in one transaction.
func (db *DB) CreateSession(ctx context.Context, tokenHash []byte, s domain.Session) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at_ms, expires_at_ms, id, last_seen_at_ms, device, auth_at_ms)
		VALUES (?,?,?,?,?,?,?,?)`, tokenHash, s.UserID, s.CreatedAtMS, s.ExpiresAtMS, s.ID, s.CreatedAtMS, nullStr(s.Device), s.AuthAtMS); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash NOT IN
		(SELECT token_hash FROM sessions WHERE user_id = ? ORDER BY created_at_ms DESC, rowid DESC LIMIT ?)`, s.UserID, s.UserID, MaxSessionsPerUser); err != nil {
		return err
	}
	return tx.Commit()
}

const sessionCols = `s.id, s.user_id, s.created_at_ms, s.expires_at_ms, COALESCE(s.last_seen_at_ms, s.created_at_ms),
	COALESCE(s.auth_at_ms, s.created_at_ms), COALESCE(s.device, '')`

func scanSession(row interface{ Scan(...any) error }) (domain.Session, error) {
	var s domain.Session
	var id *string
	err := row.Scan(&id, &s.UserID, &s.CreatedAtMS, &s.ExpiresAtMS, &s.LastSeenAtMS, &s.AuthAtMS, &s.Device)
	if id != nil {
		s.ID = *id
	}
	return s, mapErr(err)
}

// GetSession returns the user and session for an unexpired session whose user
// is not disabled; otherwise domain.ErrNotFound.
func (db *DB) GetSession(ctx context.Context, tokenHash []byte, nowMS int64) (domain.User, domain.Session, error) {
	row := db.QueryRowContext(ctx, `SELECT `+userSelect("u")+`, `+sessionCols+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at_ms > ? AND u.disabled = 0`, tokenHash, nowMS)
	var u domain.User
	var s domain.Session
	var id *string
	dest, done := userScanDest(&u)
	err := row.Scan(append(dest, &id, &s.UserID, &s.CreatedAtMS, &s.ExpiresAtMS, &s.LastSeenAtMS, &s.AuthAtMS, &s.Device)...)
	done()
	if id != nil {
		s.ID = *id
	}
	return u, s, mapErr(err)
}

// GetSessionUser is GetSession without the session details.
func (db *DB) GetSessionUser(ctx context.Context, tokenHash []byte, nowMS int64) (domain.User, error) {
	u, _, err := db.GetSession(ctx, tokenHash, nowMS)
	return u, err
}

// TouchSession records activity; callers throttle it.
func (db *DB) TouchSession(ctx context.Context, tokenHash []byte, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET last_seen_at_ms = ? WHERE token_hash = ?`, nowMS, tokenHash)
	return err
}

// ListSessions returns a user's unexpired sessions, most recently used first.
func (db *DB) ListSessions(ctx context.Context, userID string, nowMS int64) ([]domain.Session, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions s WHERE s.user_id = ? AND s.expires_at_ms > ?
		ORDER BY COALESCE(s.last_seen_at_ms, s.created_at_ms) DESC LIMIT 100`, userID, nowMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSessionByID revokes one of a user's sessions by its public ID.
func (db *DB) DeleteSessionByID(ctx context.Context, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteOtherSessions revokes every session of the user except keep.
func (db *DB) DeleteOtherSessions(ctx context.Context, userID string, keep []byte) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetPassword replaces a user's password hash and, in the same transaction,
// revokes every session except keep (nil revokes all).
func (db *DB) SetPassword(ctx context.Context, userID, hash string, keep []byte, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at_ms = ? WHERE id = ?`, hash, nowMS, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	// IS NOT, unlike !=, is true when keep is NULL, so a nil keep revokes every
	// session as documented.
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash IS NOT ?`, userID, keep); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSessionAuthenticated records a fresh proof of identity in a session.
func (db *DB) MarkSessionAuthenticated(ctx context.Context, tokenHash []byte, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET auth_at_ms = ? WHERE token_hash = ?`, nowMS, tokenHash)
	return err
}

func (db *DB) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PruneSessions removes up to limit expired sessions and returns how many.
func (db *DB) PruneSessions(ctx context.Context, nowMS int64, limit int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash IN
		(SELECT token_hash FROM sessions WHERE expires_at_ms <= ? LIMIT ?)`, nowMS, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
