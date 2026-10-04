package sqlite

import (
	"context"
	"fmt"
	"strings"
)

// ResealFunc re-encrypts one sealed value. It returns changed=false to leave
// the row as it is (already on the wanted key).
type ResealFunc func(ns, name, keyID string, ct, nonce []byte) (newCt, newNonce []byte, newKeyID string, changed bool, err error)

// ResealStats counts what a reseal pass did.
type ResealStats struct {
	Seen, Changed, Failed int
}

// sealedSource describes one kind of sealed column group.
type sealedSource struct {
	table, keyCols, ns, name, ct, nonce, keyID string
}

var sealedSources = []sealedSource{
	{"bot_env_vars", "bot_id, name", "bot_id", "name", "ciphertext", "nonce", "key_id"},
	{"oauth_accounts", "user_id, provider", "'oauth:' || user_id", "provider || ':token'", "token_ciphertext", "token_nonce", "token_key_id"},
	{"oauth_accounts", "user_id, provider", "'oauth:' || user_id", "provider || ':webhook'", "webhook_ciphertext", "webhook_nonce", "webhook_key_id"},
	{"github_repos", "bot_id", "'github:' || bot_id", "'webhook'", "webhook_secret_ciphertext", "webhook_secret_nonce", "webhook_secret_key_id"},
	{"user_mfa", "user_id", "'mfa:' || user_id", "'totp'", "secret_cipher", "secret_nonce", "secret_key_id"},
	{"panel_settings", "key", "'settings'", "key", "secret_cipher", "secret_nonce", "secret_key_id"},
	{"env_overrides", "name", "'env'", "name", "secret_cipher", "secret_nonce", "secret_key_id"},
	{"oidc_providers", "id", "'oidc:' || id", "'client_secret'", "secret_ciphertext", "secret_nonce", "secret_key_id"},
}

// Reseal re-encrypts every sealed value through fn, in bounded batches. Each
// row is updated on its own and only if it still holds the ciphertext that
// was read, so the pass is resumable: running it again continues where an
// interrupted pass stopped.
func (db *DB) Reseal(ctx context.Context, fn ResealFunc) (ResealStats, error) {
	var st ResealStats
	for _, s := range sealedSources {
		if err := db.resealSource(ctx, s, fn, &st); err != nil {
			return st, fmt.Errorf("%s.%s: %w", s.table, s.ct, err)
		}
	}
	return st, nil
}

func (db *DB) resealSource(ctx context.Context, s sealedSource, fn ResealFunc, st *ResealStats) error {
	type row struct {
		k1, k2, ns, name, keyID string
		ct, nonce               []byte
	}
	twoKeys := strings.Contains(s.keyCols, ",")
	sel := `SELECT ` + s.keyCols + `, ` + s.ns + `, ` + s.name + `, ` + s.keyID + `, ` + s.ct + `, ` + s.nonce +
		` FROM ` + s.table + ` WHERE ` + s.ct + ` IS NOT NULL ORDER BY ` + s.keyCols
	rows, err := db.QueryContext(ctx, sel)
	if err != nil {
		return err
	}
	var batch []row
	for rows.Next() {
		var r row
		var err error
		if twoKeys {
			err = rows.Scan(&r.k1, &r.k2, &r.ns, &r.name, &r.keyID, &r.ct, &r.nonce)
		} else {
			err = rows.Scan(&r.k1, &r.ns, &r.name, &r.keyID, &r.ct, &r.nonce)
		}
		if err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	where := ` WHERE ` + firstKey(s.keyCols) + ` = ?`
	if twoKeys {
		where += ` AND ` + secondKey(s.keyCols) + ` = ?`
	}
	upd := `UPDATE ` + s.table + ` SET ` + s.ct + ` = ?, ` + s.nonce + ` = ?, ` + s.keyID + ` = ?` + where + ` AND ` + s.ct + ` = ?`
	for _, r := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st.Seen++
		ct, nonce, kid, changed, err := fn(r.ns, r.name, r.keyID, r.ct, r.nonce)
		if err != nil {
			st.Failed++
			continue
		}
		if !changed {
			continue
		}
		args := []any{ct, nonce, kid, r.k1}
		if twoKeys {
			args = append(args, r.k2)
		}
		args = append(args, r.ct)
		if _, err := db.ExecContext(ctx, upd, args...); err != nil {
			return err
		}
		st.Changed++
	}
	return nil
}

func firstKey(cols string) string {
	for i := range cols {
		if cols[i] == ',' {
			return cols[:i]
		}
	}
	return cols
}

func secondKey(cols string) string {
	for i := range cols {
		if cols[i] == ',' {
			return cols[i+2:]
		}
	}
	return ""
}
