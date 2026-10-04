package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// EnvOverrides returns every environment override stored from the panel.
func (db *DB) EnvOverrides(ctx context.Context) (map[string]domain.Setting, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, value, secret_cipher, secret_nonce, secret_key_id FROM env_overrides`)
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

// EnvOverrideRemoved marks an override for deletion in PutEnvOverrides.
func EnvOverrideRemoved(name string) domain.Setting { return domain.Setting{Key: name} }

// PutEnvOverrides writes overrides in one transaction. Unlike panel settings an
// empty value is a real override ("this variable is unset"), so removal is
// explicit: pass removed names in remove.
func (db *DB) PutEnvOverrides(ctx context.Context, set []domain.Setting, remove []string, by string, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		for _, name := range remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM env_overrides WHERE name = ?`, name); err != nil {
				return err
			}
		}
		for _, s := range set {
			var v, kid any
			if s.Cipher == nil {
				v = s.Value
			} else {
				kid = s.KeyID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO env_overrides (name, value, secret_cipher, secret_nonce, secret_key_id, updated_at_ms, updated_by)
				VALUES (?,?,?,?,?,?,?) ON CONFLICT(name) DO UPDATE SET value = excluded.value, secret_cipher = excluded.secret_cipher,
				secret_nonce = excluded.secret_nonce, secret_key_id = excluded.secret_key_id, updated_at_ms = excluded.updated_at_ms,
				updated_by = excluded.updated_by`,
				s.Key, v, s.Cipher, s.Nonce, kid, nowMS, by); err != nil {
				return err
			}
		}
		return nil
	})
}
