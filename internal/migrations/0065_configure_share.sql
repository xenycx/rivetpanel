-- rivetpanel:foreign-keys-off
--
-- Per-bot Configuration grants. A share or bot invitation may now hold the
-- Configuration bit (32, domain.PermConfigure: a bot's runtime, start
-- command, entrypoint, build command and auto-restart) on its own, so the
-- permissions CHECK widens from 1..31 to 1..63. SQLite cannot change a CHECK
-- in place: both tables are rebuilt with every column copied by name and
-- their index recreated. Existing rows are unchanged. The runner checks
-- foreign keys after this file (foreign_key_check) before committing.

CREATE TABLE bot_subusers_new (
    bot_id              TEXT NOT NULL
                        REFERENCES bots(id) ON DELETE CASCADE,
    user_id             TEXT NOT NULL
                        REFERENCES users(id) ON DELETE CASCADE,
    permissions         INTEGER NOT NULL
                        CHECK (permissions BETWEEN 1 AND 63),
    invited_by          TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL,
    PRIMARY KEY (bot_id, user_id)
) STRICT, WITHOUT ROWID;
INSERT INTO bot_subusers_new (bot_id, user_id, permissions, invited_by, created_at_ms, updated_at_ms)
    SELECT bot_id, user_id, permissions, invited_by, created_at_ms, updated_at_ms FROM bot_subusers;
DROP TABLE bot_subusers;
ALTER TABLE bot_subusers_new RENAME TO bot_subusers;
CREATE INDEX bot_subusers_user_idx ON bot_subusers(user_id);

CREATE TABLE bot_invites_new (
    id             TEXT PRIMARY KEY NOT NULL,
    bot_id         TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    token_hash     BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    permissions    INTEGER NOT NULL CHECK (permissions BETWEEN 1 AND 63),
    created_by     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at_ms  INTEGER NOT NULL,
    expires_at_ms  INTEGER NOT NULL,
    used_by        TEXT REFERENCES users(id) ON DELETE SET NULL,
    used_at_ms     INTEGER
) STRICT;
INSERT INTO bot_invites_new (id, bot_id, token_hash, permissions, created_by, created_at_ms, expires_at_ms, used_by, used_at_ms)
    SELECT id, bot_id, token_hash, permissions, created_by, created_at_ms, expires_at_ms, used_by, used_at_ms FROM bot_invites;
DROP TABLE bot_invites;
ALTER TABLE bot_invites_new RENAME TO bot_invites;
CREATE INDEX bot_invites_bot_idx ON bot_invites(bot_id);
