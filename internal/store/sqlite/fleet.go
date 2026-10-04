package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SetBotTags replaces a bot's tags.
func (db *DB) SetBotTags(ctx context.Context, botID string, tags []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_tags WHERE bot_id = ?`, botID); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_tags (bot_id, tag) VALUES (?, ?)`, botID, t); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}

// TagsAndFavorites returns tags per bot and the user's favorites, for a list.
func (db *DB) TagsAndFavorites(ctx context.Context, userID string) (map[string][]string, map[string]bool, error) {
	tags := map[string][]string{}
	rows, err := db.QueryContext(ctx, `SELECT bot_id, tag FROM bot_tags ORDER BY tag`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var b, t string
		if err := rows.Scan(&b, &t); err != nil {
			rows.Close()
			return nil, nil, err
		}
		tags[b] = append(tags[b], t)
	}
	rows.Close()
	favs := map[string]bool{}
	rows, err = db.QueryContext(ctx, `SELECT bot_id FROM user_bot_favorites WHERE user_id = ?`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, nil, err
		}
		favs[b] = true
	}
	return tags, favs, rows.Err()
}

// SetFavorite marks or unmarks a bot as a favorite of the user.
func (db *DB) SetFavorite(ctx context.Context, userID, botID string, on bool, nowMS int64) error {
	if !on {
		_, err := db.ExecContext(ctx, `DELETE FROM user_bot_favorites WHERE user_id = ? AND bot_id = ?`, userID, botID)
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO user_bot_favorites (user_id, bot_id, created_at_ms) VALUES (?,?,?)
		ON CONFLICT(user_id, bot_id) DO NOTHING`, userID, botID, nowMS)
	return mapErr(err)
}

var _ = domain.ErrNotFound
