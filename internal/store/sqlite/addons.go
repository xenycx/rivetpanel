package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// ListBotAddons returns a bot's add-ons ordered by kind.
func (db *DB) ListBotAddons(ctx context.Context, botID string) ([]domain.BotAddon, error) {
	rows, err := db.QueryContext(ctx, `SELECT bot_id, kind, memory_bytes, created_at_ms, updated_at_ms
		FROM bot_addons WHERE bot_id = ? ORDER BY kind`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotAddon
	for rows.Next() {
		var a domain.BotAddon
		if err := rows.Scan(&a.BotID, &a.Kind, &a.MemoryBytes, &a.CreatedAtMS, &a.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) addonTx(ctx context.Context, botID string, nowMS int64, fn func(exec func(string, ...any) (int64, error)) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, a ...any) (int64, error) {
		r, err := tx.ExecContext(ctx, q, a...)
		if err != nil {
			return 0, mapErr(err)
		}
		return r.RowsAffected()
	}
	if err := db.bumpStopped(ctx, exec, botID, nowMS); err != nil {
		return err
	}
	if err := fn(exec); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateBotAddon adds an add-on and its sealed password variable (nil when
// the kind has none) and advances the generation, in one transaction. The bot
// must be stopped; an existing add-on of the kind returns domain.ErrConflict.
func (db *DB) CreateBotAddon(ctx context.Context, a domain.BotAddon, password *domain.EnvVar, nowMS int64) error {
	return db.addonTx(ctx, a.BotID, nowMS, func(exec func(string, ...any) (int64, error)) error {
		if _, err := exec(`INSERT INTO bot_addons (bot_id, kind, memory_bytes, created_at_ms, updated_at_ms) VALUES (?,?,?,?,?)`,
			a.BotID, a.Kind, a.MemoryBytes, nowMS, nowMS); err != nil {
			return err
		}
		if password == nil {
			return nil
		}
		_, err := exec(`INSERT INTO bot_env_vars (bot_id, name, ciphertext, nonce, key_id, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(bot_id, name) DO UPDATE SET ciphertext = excluded.ciphertext, nonce = excluded.nonce,
				key_id = excluded.key_id, updated_at_ms = excluded.updated_at_ms`,
			a.BotID, password.Name, password.Ciphertext, password.Nonce, password.KeyID, nowMS, nowMS)
		return err
	})
}

// UpdateBotAddonMemory changes an add-on's memory limit (stopped bots only).
func (db *DB) UpdateBotAddonMemory(ctx context.Context, botID, kind string, memory, nowMS int64) error {
	return db.addonTx(ctx, botID, nowMS, func(exec func(string, ...any) (int64, error)) error {
		n, err := exec(`UPDATE bot_addons SET memory_bytes = ?, updated_at_ms = ? WHERE bot_id = ? AND kind = ?`, memory, nowMS, botID, kind)
		if err == nil && n == 0 {
			return domain.ErrNotFound
		}
		return err
	})
}

// DeleteBotAddon removes an add-on and its password variable (stopped bots
// only). The add-on's data directory is removed by the caller.
func (db *DB) DeleteBotAddon(ctx context.Context, botID, kind string, nowMS int64) error {
	return db.addonTx(ctx, botID, nowMS, func(exec func(string, ...any) (int64, error)) error {
		n, err := exec(`DELETE FROM bot_addons WHERE bot_id = ? AND kind = ?`, botID, kind)
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrNotFound
		}
		_, err = exec(`DELETE FROM bot_env_vars WHERE bot_id = ? AND name = ?`, botID, domain.AddonPasswordVar(kind))
		return err
	})
}
