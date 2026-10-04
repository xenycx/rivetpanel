-- OpenID Connect sign-in providers and the identities linked to accounts.
-- Purely additive.
--
-- oidc_providers: configured by administrators. The client secret is sealed
-- with the panel keyring (namespace 'oidc:<id>', name 'client_secret'); a
-- public client (PKCE only) has no secret. default_role_id NULL means the
-- built-in User role for accounts created on first sign-in (the application
-- never allows the administrator role here). A role named here cannot be
-- deleted while a provider uses it.
CREATE TABLE oidc_providers (
    id                  TEXT PRIMARY KEY NOT NULL,
    slug                TEXT NOT NULL UNIQUE
                        CHECK (length(slug) BETWEEN 1 AND 32 AND slug NOT GLOB '*[^a-z0-9-]*'),
    name                TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
    issuer              TEXT NOT NULL CHECK (length(issuer) BETWEEN 8 AND 512),
    client_id           TEXT NOT NULL CHECK (length(client_id) BETWEEN 1 AND 256),
    secret_ciphertext   BLOB,
    secret_nonce        BLOB,
    secret_key_id       TEXT,
    scopes              TEXT NOT NULL DEFAULT 'openid email profile'
                        CHECK (length(scopes) <= 512),
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    allow_signup        INTEGER NOT NULL DEFAULT 0 CHECK (allow_signup IN (0, 1)),
    link_by_email       INTEGER NOT NULL DEFAULT 1 CHECK (link_by_email IN (0, 1)),
    default_role_id     TEXT REFERENCES roles(id),
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL,
    CHECK (
        (secret_ciphertext IS NULL AND secret_nonce IS NULL AND secret_key_id IS NULL)
        OR
        (secret_ciphertext IS NOT NULL AND length(secret_ciphertext) >= 16
         AND secret_nonce IS NOT NULL AND length(secret_nonce) = 12
         AND secret_key_id IS NOT NULL)
    )
) STRICT;

-- One identity (issuer subject) per provider and account. Linking happens
-- through a signed-in account, or automatically by email only when the
-- provider asserted email_verified (see docs/auth.md).
CREATE TABLE oidc_identities (
    provider_id         TEXT NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    subject             TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    user_id             TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email               TEXT CHECK (email IS NULL OR length(email) <= 254),
    email_verified      INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0, 1)),
    created_at_ms       INTEGER NOT NULL,
    last_login_at_ms    INTEGER,
    PRIMARY KEY (provider_id, subject),
    UNIQUE (user_id, provider_id)
) STRICT;
CREATE INDEX oidc_identities_user_idx ON oidc_identities(user_id);
