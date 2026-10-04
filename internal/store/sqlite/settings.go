package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Settings returns every stored panel setting.
func (db *DB) Settings(ctx context.Context) (map[string]domain.Setting, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value, secret_cipher, secret_nonce, secret_key_id FROM panel_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.Setting{}
	for rows.Next() {
		var s domain.Setting
		var v, kid sql.NullString
		if err := rows.Scan(&s.Key, &v, &s.Cipher, &s.Nonce, &kid); err != nil {
			return nil, err
		}
		s.Value, s.KeyID = v.String, kid.String
		out[s.Key] = s
	}
	return out, rows.Err()
}

// PutSettings writes settings in one transaction. A setting with an empty
// Value and no Cipher is deleted.
func (db *DB) PutSettings(ctx context.Context, set []domain.Setting, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		for _, s := range set {
			if s.Value == "" && s.Cipher == nil {
				if _, err := tx.ExecContext(ctx, `DELETE FROM panel_settings WHERE key = ?`, s.Key); err != nil {
					return err
				}
				continue
			}
			var v, kid any
			if s.Value != "" {
				v = s.Value
			}
			if s.Cipher != nil {
				kid = s.KeyID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO panel_settings (key, value, secret_cipher, secret_nonce, secret_key_id, updated_at_ms)
				VALUES (?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, secret_cipher = excluded.secret_cipher,
				secret_nonce = excluded.secret_nonce, secret_key_id = excluded.secret_key_id, updated_at_ms = excluded.updated_at_ms`,
				s.Key, v, s.Cipher, s.Nonce, kid, nowMS); err != nil {
				return err
			}
		}
		return nil
	})
}

// UserCount counts accounts (the setup wizard runs only while it is zero).
func (db *DB) UserCount(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}
