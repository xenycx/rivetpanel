-- Custom roles and email verification. Purely additive.
--
-- roles: named permission sets. The two system rows mirror the built-in
-- users.role values ('admin', 'user'); their effective permissions come from
-- the application (every permission / every resource permission) so new
-- permissions apply to them automatically. Accounts with a built-in role keep
-- users.role_id NULL; a custom role is users.role = 'user' plus role_id.
CREATE TABLE roles (
    id               TEXT PRIMARY KEY NOT NULL,
    name             TEXT NOT NULL COLLATE NOCASE UNIQUE
                     CHECK (length(name) BETWEEN 1 AND 64),
    description      TEXT NOT NULL DEFAULT ''
                     CHECK (length(description) <= 280),
    permissions_json TEXT NOT NULL DEFAULT '[]'
                     CHECK (json_valid(permissions_json) AND json_type(permissions_json) = 'array'
                            AND length(permissions_json) <= 4096),
    system           INTEGER NOT NULL DEFAULT 0 CHECK (system IN (0, 1)),
    created_at_ms    INTEGER NOT NULL,
    updated_at_ms    INTEGER NOT NULL
) STRICT;

INSERT INTO roles (id, name, description, permissions_json, system, created_at_ms, updated_at_ms) VALUES
    ('admin', 'Administrator', 'Every permission, including the environment editor and making other administrators.', '["*"]', 1, 0, 0),
    ('user', 'User', 'Own bots, servers, sites and workspaces; no administration.', '[]', 1, 0, 0);

ALTER TABLE users ADD COLUMN role_id TEXT REFERENCES roles(id);
CREATE INDEX users_role_id_idx ON users(role_id);

-- Existing accounts are treated as verified (DEFAULT 1); accounts created
-- from now on are inserted with email_verified = 0 until they confirm.
ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 1
    CHECK (email_verified IN (0, 1));
ALTER TABLE users ADD COLUMN email_verified_at_ms INTEGER;

-- One outstanding verification link per account. Only the SHA-256 of the
-- token is stored; using it deletes the row (single use). email is the
-- address being confirmed: the current one, or a new one for an email change
-- (the account's address changes only when that link is used).
CREATE TABLE email_verifications (
    id            TEXT PRIMARY KEY NOT NULL,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    email         TEXT NOT NULL COLLATE NOCASE CHECK (length(email) BETWEEN 3 AND 254),
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    CHECK (expires_at_ms > created_at_ms)
) STRICT;
CREATE INDEX email_verifications_user_idx ON email_verifications(user_id);
CREATE INDEX email_verifications_expiry_idx ON email_verifications(expires_at_ms);
