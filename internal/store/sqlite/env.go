package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// ListEnv returns a bot's encrypted variables ordered by name.
func (db *DB) ListEnv(ctx context.Context, botID string) ([]domain.EnvVar, error) {
	rows, err := db.QueryContext(ctx, `SELECT bot_id, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms
		FROM bot_env_vars WHERE bot_id = ? ORDER BY name`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.EnvVar
	for rows.Next() {
		var e domain.EnvVar
		if err := rows.Scan(&e.BotID, &e.Name, &e.Ciphertext, &e.Nonce, &e.KeyID, &e.CreatedAtMS, &e.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// bumpStopped advances the bot's generation in tx only if it is stopped.
func (db *DB) bumpStopped(ctx context.Context, exec func(string, ...any) (int64, error), id string, nowMS int64) error {
	n, err := exec(`UPDATE bots SET generation = generation + 1, updated_at_ms = ? WHERE id = ? AND `+stoppedPredicate, nowMS, id)
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	if _, err := db.GetBot(ctx, id); err != nil {
		return err
	}
	return domain.ErrNotStopped
}

// UpsertEnv stores variables and advances the bot generation in one
// transaction; it fails with domain.ErrNotStopped unless the bot is stopped.
func (db *DB) UpsertEnv(ctx context.Context, botID string, vars []domain.EnvVar, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, a ...any) (int64, error) {
		r, err := tx.ExecContext(ctx, q, a...)
		if err != nil {
			return 0, err
		}
		return r.RowsAffected()
	}
	if err := db.bumpStopped(ctx, exec, botID, nowMS); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_env_vars (bot_id, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(bot_id, name) DO UPDATE SET ciphertext = excluded.ciphertext, nonce = excluded.nonce,
				key_id = excluded.key_id, updated_at_ms = excluded.updated_at_ms`,
			botID, v.Name, v.Ciphertext, v.Nonce, v.KeyID, nowMS, nowMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteEnv removes a variable; domain.ErrNotFound if it does not exist.
func (db *DB) DeleteEnv(ctx context.Context, botID, name string, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, a ...any) (int64, error) {
		r, err := tx.ExecContext(ctx, q, a...)
		if err != nil {
			return 0, err
		}
		return r.RowsAffected()
	}
	if err := db.bumpStopped(ctx, exec, botID, nowMS); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM bot_env_vars WHERE bot_id = ? AND name = ?`, botID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// WalkEnv calls fn for every stored environment row (ciphertext only), in
// primary-key order, without holding the whole table in memory.
func (db *DB) WalkEnv(ctx context.Context, fn func(domain.EnvVar) error) error {
	rows, err := db.QueryContext(ctx, `SELECT bot_id, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms FROM bot_env_vars ORDER BY bot_id, name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e domain.EnvVar
		if err := rows.Scan(&e.BotID, &e.Name, &e.Ciphertext, &e.Nonce, &e.KeyID, &e.CreatedAtMS, &e.UpdatedAtMS); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ReplaceEnvAll makes a bot's user-managed variables exactly vars in one
// transaction and advances the generation (stopped bots only). Names starting
// with RIVET_ are system-managed and left untouched.
func (db *DB) ReplaceEnvAll(ctx context.Context, botID string, vars []domain.EnvVar, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, a ...any) (int64, error) {
		r, err := tx.ExecContext(ctx, q, a...)
		if err != nil {
			return 0, err
		}
		return r.RowsAffected()
	}
	if err := db.bumpStopped(ctx, exec, botID, nowMS); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_env_vars WHERE bot_id = ? AND name NOT LIKE 'RIVET\_%' ESCAPE '\'`, botID); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_env_vars (bot_id, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?,?)`, botID, v.Name, v.Ciphertext, v.Nonce, v.KeyID, nowMS, nowMS); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}
