package sqlite

import (
	"context"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func (db *DB) InsertAudit(ctx context.Context, e domain.AuditEvent) error {
	_, err := db.ExecContext(ctx, `INSERT INTO audit_events (at_ms, actor_id, actor_label, bot_id, bot_name, site_id, site_name, subject_user_id,
		action, target, outcome, ip) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.AtMS, e.ActorID, e.ActorLabel, e.BotID, e.BotName, e.SiteID, e.SiteName, e.SubjectUserID, e.Action, e.Target, e.Outcome, e.IP)
	return err
}

// AuditFilter selects events; zero fields do not filter.
type AuditFilter struct {
	BotID     string
	SiteID    string
	AccountOf string // events by or about this user
	BotsOf    string // events on bots this user can currently access
	BeforeID  int64
	Limit     int
}

func (db *DB) ListAudit(ctx context.Context, f AuditFilter) ([]domain.AuditEvent, error) {
	var where []string
	var args []any
	if f.BotID != "" {
		where, args = append(where, "bot_id = ?"), append(args, f.BotID)
	}
	if f.SiteID != "" {
		where, args = append(where, "site_id = ?"), append(args, f.SiteID)
	}
	if f.AccountOf != "" {
		where = append(where, "(actor_id = ? AND bot_id IS NULL OR subject_user_id = ?)")
		args = append(args, f.AccountOf, f.AccountOf)
	}
	if f.BotsOf != "" {
		where = append(where, `bot_id IN (SELECT id FROM bots WHERE owner_id = ? AND desired_state != 'deleted'
			UNION SELECT bot_id FROM bot_subusers WHERE user_id = ?
			UNION SELECT b.id FROM bots b JOIN workspace_members m ON m.workspace_id = b.workspace_id
				WHERE m.user_id = ? AND b.desired_state != 'deleted')`)
		args = append(args, f.BotsOf, f.BotsOf, f.BotsOf)
	}
	if f.BeforeID > 0 {
		where, args = append(where, "id < ?"), append(args, f.BeforeID)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := `SELECT id, at_ms, actor_id, actor_label, bot_id, bot_name, site_id, site_name, subject_user_id, action, target, outcome, ip FROM audit_events`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := db.QueryContext(ctx, q+" ORDER BY id DESC LIMIT ?", append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ID, &e.AtMS, &e.ActorID, &e.ActorLabel, &e.BotID, &e.BotName, &e.SiteID, &e.SiteName, &e.SubjectUserID, &e.Action, &e.Target, &e.Outcome, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneAudit deletes events older than beforeMS, and the oldest beyond
// maxRows, in bounded batches. It returns how many rows it removed.
func (db *DB) PruneAudit(ctx context.Context, beforeMS int64, maxRows, batch int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM audit_events WHERE id IN (
		SELECT id FROM audit_events WHERE at_ms < ? OR id <= (SELECT COALESCE(max(id), 0) - ? FROM audit_events)
		ORDER BY id LIMIT ?)`, beforeMS, maxRows, batch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
