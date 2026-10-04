package sqlite

import (
	"context"
	"github.com/xenycx/rivetpanel/internal/domain"
	"time"
)

func (db *DB) UpsertBotWidgets(ctx context.Context, widgets []domain.BotWidget) error {
	if len(widgets) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range widgets {
		_, err = tx.ExecContext(ctx, `INSERT INTO bot_widgets (bot_id,widget_key,kind,title,group_name,span,min_height,position,payload_json,updated_at_ms,expires_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(bot_id,widget_key) DO UPDATE SET kind=excluded.kind,title=excluded.title,group_name=excluded.group_name,span=excluded.span,min_height=excluded.min_height,position=excluded.position,payload_json=excluded.payload_json,updated_at_ms=excluded.updated_at_ms,expires_at_ms=excluded.expires_at_ms`, w.BotID, w.Key, w.Kind, w.Title, w.Group, w.Span, w.MinHeight, w.Position, w.PayloadJSON, w.UpdatedAtMS, w.ExpiresAtMS)
		if err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}

func (db *DB) ListBotWidgets(ctx context.Context, botID string) ([]domain.BotWidget, error) {
	// Expired widgets disappear automatically. A generous bound protects the
	// response while allowing grouped dashboards to grow beyond the old 24 cap.
	rows, err := db.QueryContext(ctx, `SELECT bot_id,widget_key,kind,title,group_name,span,min_height,position,payload_json,updated_at_ms,expires_at_ms FROM bot_widgets WHERE bot_id=? AND (expires_at_ms IS NULL OR expires_at_ms>?) ORDER BY group_name,position,widget_key LIMIT 240`, botID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotWidget
	for rows.Next() {
		var w domain.BotWidget
		if err := rows.Scan(&w.BotID, &w.Key, &w.Kind, &w.Title, &w.Group, &w.Span, &w.MinHeight, &w.Position, &w.PayloadJSON, &w.UpdatedAtMS, &w.ExpiresAtMS); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteBotWidgets removes selected published widgets. An empty key list is a no-op.
func (db *DB) DeleteBotWidgets(ctx context.Context, botID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err = tx.ExecContext(ctx, `DELETE FROM bot_widgets WHERE bot_id=? AND widget_key=?`, botID, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}
