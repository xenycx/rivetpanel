package sqlite

import (
	"context"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const notificationCols = `id, user_id, category, title, body, link, created_at_ms, read_at_ms`

func scanNotification(row interface{ Scan(...any) error }) (domain.Notification, error) {
	var n domain.Notification
	err := row.Scan(&n.ID, &n.UserID, &n.Category, &n.Title, &n.Body, &n.Link, &n.CreatedAtMS, &n.ReadAtMS)
	return n, mapErr(err)
}

// InsertNotifications stores notifications and, in the same transaction,
// trims every recipient to its newest keep rows.
func (db *DB) InsertNotifications(ctx context.Context, ns []domain.Notification, keep int) error {
	if len(ns) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, n := range ns {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (`+notificationCols+`) VALUES (?,?,?,?,?,?,?,NULL)`,
			n.ID, n.UserID, n.Category, n.Title, n.Body, n.Link, n.CreatedAtMS); err != nil {
			return mapErr(err)
		}
		seen[n.UserID] = true
	}
	for uid := range seen {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notifications WHERE user_id = ? AND id NOT IN
			(SELECT id FROM notifications WHERE user_id = ? ORDER BY created_at_ms DESC, rowid DESC LIMIT ?)`, uid, uid, keep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListNotifications returns one account's notifications, newest first.
// beforeMS > 0 pages back; unreadOnly skips read ones.
func (db *DB) ListNotifications(ctx context.Context, userID string, beforeMS int64, unreadOnly bool, limit int) ([]domain.Notification, error) {
	q := `SELECT ` + notificationCols + ` FROM notifications WHERE user_id = ?`
	args := []any{userID}
	if beforeMS > 0 {
		q += ` AND created_at_ms < ?`
		args = append(args, beforeMS)
	}
	if unreadOnly {
		q += ` AND read_at_ms IS NULL`
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY created_at_ms DESC, rowid DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CountUnreadNotifications counts an account's unread notifications.
func (db *DB) CountUnreadNotifications(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE user_id = ? AND read_at_ms IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks the given notifications of one account read
// (ids nil = all of them) and returns how many changed.
func (db *DB) MarkNotificationsRead(ctx context.Context, userID string, ids []string, nowMS int64) (int, error) {
	q := `UPDATE notifications SET read_at_ms = ? WHERE user_id = ? AND read_at_ms IS NULL`
	args := []any{nowMS, userID}
	if ids != nil {
		if len(ids) == 0 {
			return 0, nil
		}
		q += ` AND id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteNotification removes one of an account's notifications.
func (db *DB) DeleteNotification(ctx context.Context, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteReadNotifications removes an account's read notifications.
func (db *DB) DeleteReadNotifications(ctx context.Context, userID string) (int, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM notifications WHERE user_id = ? AND read_at_ms IS NOT NULL`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneNotifications deletes notifications created before cutoffMS.
func (db *DB) PruneNotifications(ctx context.Context, cutoffMS int64) (int, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM notifications WHERE created_at_ms < ?`, cutoffMS)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// NotificationPrefs returns an account's stored choices (categories without
// a row use the defaults).
func (db *DB) NotificationPrefs(ctx context.Context, userID string) (map[string]domain.NotificationPref, error) {
	rows, err := db.QueryContext(ctx, `SELECT category, in_panel, email FROM notification_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.NotificationPref{}
	for rows.Next() {
		var p domain.NotificationPref
		if err := rows.Scan(&p.Category, &p.InPanel, &p.Email); err != nil {
			return nil, err
		}
		out[p.Category] = p
	}
	return out, rows.Err()
}

// SetNotificationPrefs stores an account's choices.
func (db *DB) SetNotificationPrefs(ctx context.Context, userID string, ps []domain.NotificationPref) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_prefs (user_id, category, in_panel, email) VALUES (?,?,?,?)
			ON CONFLICT (user_id, category) DO UPDATE SET in_panel = excluded.in_panel, email = excluded.email`,
			userID, p.Category, boolInt(p.InPanel), boolInt(p.Email)); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}
