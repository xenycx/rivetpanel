package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const apiClientCols = `c.id, c.user_id, c.name, c.prefix, c.permissions_json, c.bot_ids_json, c.workspace_ids_json,
	c.created_at_ms, c.last_used_at_ms, c.expires_at_ms, u.email`

func scanAPIClient(row interface{ Scan(...any) error }) (domain.APIClient, error) {
	var c domain.APIClient
	var perms string
	var bots, wss sql.NullString
	if err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Prefix, &perms, &bots, &wss, &c.CreatedAtMS, &c.LastUsedAtMS, &c.ExpiresAtMS, &c.OwnerEmail); err != nil {
		return c, mapErr(err)
	}
	c.Permissions = decodePermissions(perms)
	c.BotIDs = decodeIDList(bots)
	c.WorkspaceIDs = decodeIDList(wss)
	return c, nil
}

// decodeIDList maps NULL to nil (not limited) and an array to a non-nil slice.
func decodeIDList(v sql.NullString) []string {
	if !v.Valid {
		return nil
	}
	out := []string{}
	_ = json.Unmarshal([]byte(v.String), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func encodeIDList(ids []string) *string {
	if ids == nil {
		return nil
	}
	b, _ := json.Marshal(ids)
	s := string(b)
	return &s
}

// InsertAPIClient stores a hashed API client.
func (db *DB) InsertAPIClient(ctx context.Context, c domain.APIClient) error {
	_, err := db.ExecContext(ctx, `INSERT INTO api_clients (id, user_id, name, prefix, token_hash, permissions_json,
		bot_ids_json, workspace_ids_json, created_at_ms, expires_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.UserID, c.Name, c.Prefix, c.TokenHash, encodePermissions(c.Permissions),
		encodeIDList(c.BotIDs), encodeIDList(c.WorkspaceIDs), c.CreatedAtMS, c.ExpiresAtMS)
	return mapErr(err)
}

// ListAPIClients returns one account's clients (userID != "") or every
// client, newest first.
func (db *DB) ListAPIClients(ctx context.Context, userID string) ([]domain.APIClient, error) {
	q := `SELECT ` + apiClientCols + ` FROM api_clients c JOIN users u ON u.id = c.user_id`
	args := []any{}
	if userID != "" {
		q += ` WHERE c.user_id = ?`
		args = append(args, userID)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY c.created_at_ms DESC LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIClient
	for rows.Next() {
		c, err := scanAPIClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetAPIClient returns one client by id.
func (db *DB) GetAPIClient(ctx context.Context, id string) (domain.APIClient, error) {
	return scanAPIClient(db.QueryRowContext(ctx, `SELECT `+apiClientCols+` FROM api_clients c JOIN users u ON u.id = c.user_id WHERE c.id = ?`, id))
}

// APIClientByHash resolves an unexpired client and its enabled owner.
func (db *DB) APIClientByHash(ctx context.Context, hash []byte, nowMS int64) (domain.APIClient, domain.User, error) {
	c, err := scanAPIClient(db.QueryRowContext(ctx, `SELECT `+apiClientCols+` FROM api_clients c JOIN users u ON u.id = c.user_id
		WHERE c.token_hash = ? AND (c.expires_at_ms IS NULL OR c.expires_at_ms > ?)`, hash, nowMS))
	if err != nil {
		return c, domain.User{}, err
	}
	u, err := db.GetUserByID(ctx, c.UserID)
	if err != nil {
		return c, u, err
	}
	if u.Disabled {
		return c, u, domain.ErrNotFound
	}
	return c, u, nil
}

// TouchAPIClient records use (callers throttle).
func (db *DB) TouchAPIClient(ctx context.Context, id string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE api_clients SET last_used_at_ms = ? WHERE id = ?`, nowMS, id)
	return err
}

// DeleteAPIClient revokes a client; userID != "" limits it to that owner.
func (db *DB) DeleteAPIClient(ctx context.Context, userID, id string) error {
	q, args := `DELETE FROM api_clients WHERE id = ?`, []any{id}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
