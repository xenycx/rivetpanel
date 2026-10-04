-- API clients: scoped application credentials for the whole HTTP API
-- ("rvc_" bearer tokens). Purely additive.
--
-- A client carries a list of permission names from the role catalog (never
-- more than its creator held when it was made; every request intersects them
-- with the creator's CURRENT permissions) and may be limited to some bots
-- and/or workspaces (NULL = not limited). Only the SHA-256 of the token is
-- stored; it is shown once. expires_at_ms NULL means it does not expire.
-- A new table rather than api_keys (whose scope CHECK only allows 'sftp')
-- or automation_tokens (whose actions are the narrow automation API).
CREATE TABLE api_clients (
    id                 TEXT PRIMARY KEY NOT NULL,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name               TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
    prefix             TEXT NOT NULL,
    token_hash         BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    permissions_json   TEXT NOT NULL
                       CHECK (json_valid(permissions_json) AND json_type(permissions_json) = 'array'
                              AND length(permissions_json) <= 4096),
    bot_ids_json       TEXT CHECK (bot_ids_json IS NULL OR (json_valid(bot_ids_json)
                              AND json_type(bot_ids_json) = 'array' AND length(bot_ids_json) <= 4000)),
    workspace_ids_json TEXT CHECK (workspace_ids_json IS NULL OR (json_valid(workspace_ids_json)
                              AND json_type(workspace_ids_json) = 'array' AND length(workspace_ids_json) <= 4000)),
    created_at_ms      INTEGER NOT NULL,
    last_used_at_ms    INTEGER,
    expires_at_ms      INTEGER CHECK (expires_at_ms IS NULL OR expires_at_ms > created_at_ms)
) STRICT;
CREATE INDEX api_clients_user_idx ON api_clients(user_id);
