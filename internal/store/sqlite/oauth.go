package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const oauthCols = `provider, provider_user_id, user_id, username, email, avatar_url, scopes,
	token_ciphertext, token_nonce, token_key_id, notify_enabled, created_at_ms, updated_at_ms`

// oauthSelect adds the webhook columns, which are written only by SetOAuthWebhook.
const oauthSelect = oauthCols + `, webhook_ciphertext, webhook_nonce, webhook_key_id`

func scanOAuth(row interface{ Scan(...any) error }) (domain.OAuthAccount, error) {
	var a domain.OAuthAccount
	var notify int
	err := row.Scan(&a.Provider, &a.ProviderUserID, &a.UserID, &a.Username, &a.Email, &a.AvatarURL, &a.Scopes,
		&a.TokenCipher, &a.TokenNonce, &a.TokenKeyID, &notify, &a.CreatedAtMS, &a.UpdatedAtMS,
		&a.WebhookCipher, &a.WebhookNonce, &a.WebhookKeyID)
	a.NotifyEnabled = notify == 1
	return a, mapErr(err)
}

// GetOAuthAccount looks up an identity by provider and provider user ID.
func (db *DB) GetOAuthAccount(ctx context.Context, provider, providerUserID string) (domain.OAuthAccount, error) {
	return scanOAuth(db.QueryRowContext(ctx, `SELECT `+oauthSelect+` FROM oauth_accounts
		WHERE provider = ? AND provider_user_id = ?`, provider, providerUserID))
}

// ListOAuthAccounts returns a user's linked identities.
func (db *DB) ListOAuthAccounts(ctx context.Context, userID string) ([]domain.OAuthAccount, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+oauthSelect+` FROM oauth_accounts WHERE user_id = ? ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OAuthAccount
	for rows.Next() {
		a, err := scanOAuth(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertOAuthAccount links (or refreshes) an identity. The same external
// identity linked to a different user, or a second identity of the same
// provider for one user, returns domain.ErrConflict.
func (db *DB) UpsertOAuthAccount(ctx context.Context, a domain.OAuthAccount) error {
	_, err := db.ExecContext(ctx, `INSERT INTO oauth_accounts (`+oauthCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(provider, provider_user_id) DO UPDATE SET
			username = excluded.username, email = excluded.email, avatar_url = excluded.avatar_url,
			scopes = excluded.scopes, token_ciphertext = excluded.token_ciphertext,
			token_nonce = excluded.token_nonce, token_key_id = excluded.token_key_id,
			updated_at_ms = excluded.updated_at_ms
		WHERE oauth_accounts.user_id = excluded.user_id`,
		a.Provider, a.ProviderUserID, a.UserID, a.Username, a.Email, a.AvatarURL, a.Scopes,
		a.TokenCipher, a.TokenNonce, a.TokenKeyID, boolInt(a.NotifyEnabled), a.CreatedAtMS, a.UpdatedAtMS)
	if err != nil {
		return mapErr(err)
	}
	// The guarded DO UPDATE silently skips an identity owned by someone else.
	cur, err := db.GetOAuthAccount(ctx, a.Provider, a.ProviderUserID)
	if err != nil {
		return err
	}
	if cur.UserID != a.UserID {
		return domain.ErrConflict
	}
	return nil
}

// UnlinkOAuthAccount removes a provider link unless it is the user's only
// remaining sign-in method (another link or a local password). Check and
// delete share one immediate transaction so concurrent unlinks cannot both pass.
func (db *DB) UnlinkOAuthAccount(ctx context.Context, userID, provider string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var others, hasPassword int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM oauth_accounts WHERE user_id = ?1 AND provider <> ?2),
		(SELECT password_hash <> '' FROM users WHERE id = ?1)`, userID, provider).Scan(&others, &hasPassword); err != nil {
		return mapErr(err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM oauth_accounts WHERE user_id = ? AND provider = ?`, userID, provider)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	if others+hasPassword == 0 {
		return domain.ErrLastAuthMethod
	}
	return tx.Commit()
}

// SetOAuthWebhook stores (or with nil clears) the sealed Discord webhook URL.
func (db *DB) SetOAuthWebhook(ctx context.Context, userID, provider string, cipher, nonce []byte, keyID *string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE oauth_accounts SET webhook_ciphertext = ?, webhook_nonce = ?, webhook_key_id = ?,
		updated_at_ms = ? WHERE user_id = ? AND provider = ?`, cipher, nonce, keyID, nowMS, userID, provider)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CreateUserWithOAuth creates a passwordless account and its first linked
// identity atomically. A duplicate email or identity returns domain.ErrConflict.
func (db *DB) CreateUserWithOAuth(ctx context.Context, u domain.User, a domain.OAuthAccount) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Email, u.DisplayName, u.AvatarJPEG, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAtMS, u.UpdatedAtMS, boolInt(u.EmailVerified)); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_accounts (`+oauthCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.Provider, a.ProviderUserID, a.UserID, a.Username, a.Email, a.AvatarURL, a.Scopes,
		a.TokenCipher, a.TokenNonce, a.TokenKeyID, boolInt(a.NotifyEnabled), a.CreatedAtMS, a.UpdatedAtMS); err != nil {
		return mapErr(err)
	}
	if err := createPersonalWorkspace(ctx, tx, u); err != nil {
		return err
	}
	return tx.Commit()
}

// SealedRow is one encrypted value with the associated-data parts needed to
// trial-decrypt it (see the secrets package for the naming).
type SealedRow struct {
	NS, Name, KeyID string
	Ciphertext      []byte
	Nonce           []byte
}

// WalkSealed calls fn for every sealed value outside bot environment variables:
// OAuth tokens, Discord webhooks, GitHub webhook secrets, TOTP secrets, panel
// settings and environment overrides, and OIDC client secrets.
func (db *DB) WalkSealed(ctx context.Context, fn func(SealedRow) error) error {
	rows, err := db.QueryContext(ctx, `SELECT 'oauth:' || user_id, provider || ':token', token_key_id, token_ciphertext, token_nonce
			FROM oauth_accounts WHERE token_ciphertext IS NOT NULL
		UNION ALL SELECT 'oauth:' || user_id, provider || ':webhook', webhook_key_id, webhook_ciphertext, webhook_nonce
			FROM oauth_accounts WHERE webhook_ciphertext IS NOT NULL
		UNION ALL SELECT 'github:' || bot_id, 'webhook', webhook_secret_key_id, webhook_secret_ciphertext, webhook_secret_nonce
			FROM github_repos
		UNION ALL SELECT 'mfa:' || user_id, 'totp', secret_key_id, secret_cipher, secret_nonce FROM user_mfa
		UNION ALL SELECT 'settings', key, secret_key_id, secret_cipher, secret_nonce FROM panel_settings WHERE secret_cipher IS NOT NULL
		UNION ALL SELECT 'env', name, secret_key_id, secret_cipher, secret_nonce FROM env_overrides WHERE secret_cipher IS NOT NULL
		UNION ALL SELECT 'oidc:' || id, 'client_secret', secret_key_id, secret_ciphertext, secret_nonce FROM oidc_providers WHERE secret_ciphertext IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r SealedRow
		if err := rows.Scan(&r.NS, &r.Name, &r.KeyID, &r.Ciphertext, &r.Nonce); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}
