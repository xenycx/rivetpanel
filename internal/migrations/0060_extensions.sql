-- Panel extensions: signed, sandboxed WebAssembly bundles. Purely additive.
--
-- extension_publishers: Ed25519 keys an administrator trusts to sign bundles.
--   Revoking a key fails every extension whose active version it signed.
-- extensions: one row per installed extension. state is enabled, disabled or
--   failed (repeated crashes or a revoked key); granted_json is the set of
--   capabilities an administrator approved. source is builtin (shipped in the
--   binary), upload (signed) or dev (unsigned, development mode only).
-- extension_versions: the uploaded bundles themselves (so backups carry them
--   and rollback needs no network); the three newest are kept.
-- extension_settings: administrator settings; secret values are sealed with
--   namespace 'ext:' || extension_id and never returned to anyone.
-- extension_kv: the extension's private storage.
-- extension_bot_enablements: bots a bot manager switched the extension on for
--   (system jobs and events only ever see these bots).
-- extension_user_consents: users who allowed the extension to act for them
--   (ChatOps), with the scopes they allowed.
-- extension_jobs: scheduled job bookkeeping.
-- The application enforces every check; the database only stores state.
CREATE TABLE extension_publishers (
    key_id        TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    public_key    TEXT NOT NULL,
    created_by    TEXT,
    created_at_ms INTEGER NOT NULL,
    revoked_at_ms INTEGER
);

CREATE TABLE extensions (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL CHECK (source IN ('builtin','upload','dev')),
    active_version  TEXT NOT NULL,
    state           TEXT NOT NULL CHECK (state IN ('enabled','disabled','failed')),
    granted_json    TEXT NOT NULL DEFAULT '[]',
    last_error      TEXT NOT NULL DEFAULT '',
    failure_count   INTEGER NOT NULL DEFAULT 0,
    installed_by    TEXT,
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL
);

CREATE TABLE extension_versions (
    extension_id  TEXT NOT NULL,
    version       TEXT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('builtin','upload','dev')),
    bundle        BLOB NOT NULL,
    sha256        TEXT NOT NULL,
    manifest_json TEXT NOT NULL,
    key_id        TEXT NOT NULL DEFAULT '',
    uploaded_by   TEXT,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (extension_id, version)
);

CREATE TABLE extension_settings (
    extension_id  TEXT NOT NULL REFERENCES extensions(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    value_json    TEXT,
    secret_cipher BLOB,
    secret_nonce  BLOB,
    secret_key_id TEXT,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (extension_id, key)
);

CREATE TABLE extension_kv (
    extension_id  TEXT NOT NULL REFERENCES extensions(id) ON DELETE CASCADE,
    scope         TEXT NOT NULL,
    key           TEXT NOT NULL,
    value         BLOB NOT NULL,
    expires_at_ms INTEGER,
    PRIMARY KEY (extension_id, scope, key)
);

CREATE TABLE extension_bot_enablements (
    extension_id  TEXT NOT NULL REFERENCES extensions(id) ON DELETE CASCADE,
    bot_id        TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    enabled_by    TEXT,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (extension_id, bot_id)
);
CREATE INDEX extension_bot_enablements_bot_idx ON extension_bot_enablements(bot_id);

CREATE TABLE extension_user_consents (
    extension_id  TEXT NOT NULL REFERENCES extensions(id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scopes_json   TEXT NOT NULL DEFAULT '[]',
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (extension_id, user_id)
);
CREATE INDEX extension_user_consents_user_idx ON extension_user_consents(user_id);

CREATE TABLE extension_jobs (
    extension_id TEXT NOT NULL REFERENCES extensions(id) ON DELETE CASCADE,
    job          TEXT NOT NULL,
    next_run_ms  INTEGER NOT NULL,
    last_run_ms  INTEGER,
    last_status  TEXT NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (extension_id, job)
);
