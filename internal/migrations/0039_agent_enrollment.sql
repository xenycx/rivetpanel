CREATE TABLE agent_enrollment_tokens (
    id                  TEXT PRIMARY KEY NOT NULL,
    node_id             TEXT NOT NULL UNIQUE
                        REFERENCES nodes(id) ON DELETE CASCADE,
    prefix              TEXT NOT NULL,
    token_hash          BLOB NOT NULL UNIQUE,
    created_at_ms       INTEGER NOT NULL,
    expires_at_ms       INTEGER NOT NULL,
    used_at_ms          INTEGER,
    revoked_at_ms       INTEGER,
    CHECK (length(prefix) BETWEEN 8 AND 24),
    CHECK (expires_at_ms > created_at_ms),
    CHECK (used_at_ms IS NULL OR used_at_ms >= created_at_ms),
    CHECK (revoked_at_ms IS NULL OR revoked_at_ms >= created_at_ms),
    CHECK (used_at_ms IS NULL OR revoked_at_ms IS NULL)
) STRICT;

CREATE INDEX agent_enrollment_tokens_active_idx
    ON agent_enrollment_tokens(expires_at_ms)
    WHERE used_at_ms IS NULL AND revoked_at_ms IS NULL;
