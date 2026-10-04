package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const oidcProviderCols = `id, slug, name, issuer, client_id, secret_ciphertext, secret_nonce, secret_key_id, scopes,
	enabled, allow_signup, link_by_email, default_role_id, created_at_ms, updated_at_ms`

func scanOIDCProvider(row interface{ Scan(...any) error }) (domain.OIDCProvider, error) {
	var p domain.OIDCProvider
	var en, su, le int
	var role sql.NullString
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Issuer, &p.ClientID, &p.SecretCipher, &p.SecretNonce, &p.SecretKeyID, &p.Scopes,
		&en, &su, &le, &role, &p.CreatedAtMS, &p.UpdatedAtMS); err != nil {
		return p, mapErr(err)
	}
	p.Enabled, p.AllowSignup, p.LinkByEmail, p.DefaultRoleID = en == 1, su == 1, le == 1, role.String
	return p, nil
}

// ListOIDCProviders returns every provider by name.
func (db *DB) ListOIDCProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+oidcProviderCols+` FROM oidc_providers ORDER BY name LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OIDCProvider
	for rows.Next() {
		p, err := scanOIDCProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetOIDCProvider returns one provider by id.
func (db *DB) GetOIDCProvider(ctx context.Context, id string) (domain.OIDCProvider, error) {
	return scanOIDCProvider(db.QueryRowContext(ctx, `SELECT `+oidcProviderCols+` FROM oidc_providers WHERE id = ?`, id))
}

// GetOIDCProviderBySlug returns one provider by its URL name.
func (db *DB) GetOIDCProviderBySlug(ctx context.Context, slug string) (domain.OIDCProvider, error) {
	return scanOIDCProvider(db.QueryRowContext(ctx, `SELECT `+oidcProviderCols+` FROM oidc_providers WHERE slug = ?`, slug))
}

// InsertOIDCProvider stores a provider; a duplicate slug is ErrConflict.
func (db *DB) InsertOIDCProvider(ctx context.Context, p domain.OIDCProvider) error {
	_, err := db.ExecContext(ctx, `INSERT INTO oidc_providers (`+oidcProviderCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Slug, p.Name, p.Issuer, p.ClientID, p.SecretCipher, p.SecretNonce, p.SecretKeyID, p.Scopes,
		boolInt(p.Enabled), boolInt(p.AllowSignup), boolInt(p.LinkByEmail), nullStr(p.DefaultRoleID), p.CreatedAtMS, p.UpdatedAtMS)
	return mapErr(err)
}

// UpdateOIDCProvider replaces a provider's settings (including the sealed
// secret fields as given).
func (db *DB) UpdateOIDCProvider(ctx context.Context, p domain.OIDCProvider) error {
	res, err := db.ExecContext(ctx, `UPDATE oidc_providers SET slug = ?, name = ?, issuer = ?, client_id = ?, secret_ciphertext = ?,
		secret_nonce = ?, secret_key_id = ?, scopes = ?, enabled = ?, allow_signup = ?, link_by_email = ?, default_role_id = ?,
		updated_at_ms = ? WHERE id = ?`, p.Slug, p.Name, p.Issuer, p.ClientID, p.SecretCipher, p.SecretNonce, p.SecretKeyID, p.Scopes,
		boolInt(p.Enabled), boolInt(p.AllowSignup), boolInt(p.LinkByEmail), nullStr(p.DefaultRoleID), p.UpdatedAtMS, p.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteOIDCProvider removes a provider and every identity linked through it.
func (db *DB) DeleteOIDCProvider(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM oidc_providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const oidcIdentityCols = `i.provider_id, i.subject, i.user_id, coalesce(i.email, ''), i.email_verified, i.created_at_ms, i.last_login_at_ms, p.slug, p.name`

func scanOIDCIdentity(row interface{ Scan(...any) error }) (domain.OIDCIdentity, error) {
	var i domain.OIDCIdentity
	var v int
	if err := row.Scan(&i.ProviderID, &i.Subject, &i.UserID, &i.Email, &v, &i.CreatedAtMS, &i.LastLoginAtMS, &i.ProviderSlug, &i.ProviderName); err != nil {
		return i, mapErr(err)
	}
	i.EmailVerified = v == 1
	return i, nil
}

// GetOIDCIdentity resolves an issuer subject.
func (db *DB) GetOIDCIdentity(ctx context.Context, providerID, subject string) (domain.OIDCIdentity, error) {
	return scanOIDCIdentity(db.QueryRowContext(ctx, `SELECT `+oidcIdentityCols+` FROM oidc_identities i
		JOIN oidc_providers p ON p.id = i.provider_id WHERE i.provider_id = ? AND i.subject = ?`, providerID, subject))
}

// ListOIDCIdentities returns the identities linked to an account.
func (db *DB) ListOIDCIdentities(ctx context.Context, userID string) ([]domain.OIDCIdentity, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+oidcIdentityCols+` FROM oidc_identities i
		JOIN oidc_providers p ON p.id = i.provider_id WHERE i.user_id = ? ORDER BY p.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OIDCIdentity
	for rows.Next() {
		i, err := scanOIDCIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UpsertOIDCIdentity links a subject to an account or records a sign-in. A
// subject already linked to another account, or a second subject from the
// same provider for one account, is ErrConflict.
func (db *DB) UpsertOIDCIdentity(ctx context.Context, i domain.OIDCIdentity) error {
	res, err := db.ExecContext(ctx, `INSERT INTO oidc_identities (provider_id, subject, user_id, email, email_verified, created_at_ms, last_login_at_ms)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT (provider_id, subject) DO UPDATE SET email = excluded.email,
		email_verified = excluded.email_verified, last_login_at_ms = coalesce(excluded.last_login_at_ms, last_login_at_ms)
		WHERE user_id = excluded.user_id`,
		i.ProviderID, i.Subject, i.UserID, nullStr(i.Email), boolInt(i.EmailVerified), i.CreatedAtMS, i.LastLoginAtMS)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrConflict
	}
	return nil
}

// DeleteOIDCIdentity unlinks an account from a provider.
func (db *DB) DeleteOIDCIdentity(ctx context.Context, userID, providerID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM oidc_identities WHERE user_id = ? AND provider_id = ?`, userID, providerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CreateUserWithOIDC creates an account (with an optional custom role), its
// personal workspace and the linked identity in one transaction.
func (db *DB) CreateUserWithOIDC(ctx context.Context, u domain.User, i domain.OIDCIdentity) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			u.ID, u.Email, u.DisplayName, u.AvatarJPEG, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAtMS, u.UpdatedAtMS, boolInt(u.EmailVerified)); err != nil {
			return mapErr(err)
		}
		if u.RoleID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET role_id = ? WHERE id = ?`, u.RoleID, u.ID); err != nil {
				return mapErr(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO oidc_identities (provider_id, subject, user_id, email, email_verified, created_at_ms, last_login_at_ms)
			VALUES (?,?,?,?,?,?,?)`, i.ProviderID, i.Subject, u.ID, nullStr(i.Email), boolInt(i.EmailVerified), i.CreatedAtMS, i.LastLoginAtMS); err != nil {
			return mapErr(err)
		}
		return createPersonalWorkspace(ctx, tx, u)
	})
}
