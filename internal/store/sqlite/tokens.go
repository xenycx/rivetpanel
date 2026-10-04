package sqlite

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const tokenCols = `id, user_id, name, prefix, actions, bot_ids, created_at_ms, last_used_at_ms, expires_at_ms`

func scanToken(row interface{ Scan(...any) error }) (domain.AutomationToken, error) {
	var t domain.AutomationToken
	var actions string
	var bots *string
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &actions, &bots, &t.CreatedAtMS, &t.LastUsedAtMS, &t.ExpiresAtMS); err != nil {
		return t, mapErr(err)
	}
	t.Actions = strings.Split(actions, ",")
	if bots != nil {
		_ = json.Unmarshal([]byte(*bots), &t.BotIDs)
		if t.BotIDs == nil {
			t.BotIDs = []string{}
		}
	}
	return t, nil
}

// InsertToken stores a hashed automation token.
func (db *DB) InsertToken(ctx context.Context, t domain.AutomationToken) error {
	var bots *string
	if t.BotIDs != nil {
		b, _ := json.Marshal(t.BotIDs)
		s := string(b)
		bots = &s
	}
	_, err := db.ExecContext(ctx, `INSERT INTO automation_tokens (id, user_id, name, prefix, token_hash, actions, bot_ids, created_at_ms, expires_at_ms)
		VALUES (?,?,?,?,?,?,?,?,?)`, t.ID, t.UserID, t.Name, t.Prefix, t.TokenHash, strings.Join(t.Actions, ","), bots, t.CreatedAtMS, t.ExpiresAtMS)
	return mapErr(err)
}

// ListTokens returns a user's tokens, newest first.
func (db *DB) ListTokens(ctx context.Context, userID string) ([]domain.AutomationToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+tokenCols+` FROM automation_tokens WHERE user_id = ? ORDER BY created_at_ms DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AutomationToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TokenByHash resolves an unexpired token and its enabled owner.
func (db *DB) TokenByHash(ctx context.Context, hash []byte, nowMS int64) (domain.AutomationToken, domain.User, error) {
	t, err := scanToken(db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM automation_tokens WHERE token_hash = ? AND expires_at_ms > ?`, hash, nowMS))
	if err != nil {
		return t, domain.User{}, err
	}
	u, err := db.GetUserByID(ctx, t.UserID)
	if err != nil {
		return t, u, err
	}
	if u.Disabled {
		return t, u, domain.ErrNotFound
	}
	return t, u, nil
}

// TouchToken records use (callers throttle).
func (db *DB) TouchToken(ctx context.Context, id string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE automation_tokens SET last_used_at_ms = ? WHERE id = ?`, nowMS, id)
	return err
}

// DeleteToken revokes one of a user's tokens.
func (db *DB) DeleteToken(ctx context.Context, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM automation_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
