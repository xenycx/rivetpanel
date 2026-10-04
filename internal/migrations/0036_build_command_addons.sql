-- Hosting larger open-source bots (Red-DiscordBot, YAGPDB, ...):
--
-- * bots.build_command: an optional per-bot shell script that replaces the
--   runtime's default build step (for example `go build ./cmd/yagpdb` or
--   `pip install .`). It runs in the same resource-limited builder container
--   as the default step, without the bot's secrets.
-- * bot_addons: companion services (PostgreSQL, Redis, MongoDB, MariaDB) that
--   run next to the bot on a private per-bot network. Generated passwords are
--   stored as sealed RIVET_ADDON_<KIND>_PASSWORD variables in
--   bot_env_vars, so key rotation, verification and backups cover them.
ALTER TABLE bots ADD COLUMN build_command TEXT
    CHECK (build_command IS NULL OR length(build_command) BETWEEN 1 AND 4096);

CREATE TABLE bot_addons (
    bot_id        TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    -- Validated against the application's add-on catalog; also the add-on's
    -- host name on the bot's private network.
    kind          TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 32),
    memory_bytes  INTEGER NOT NULL CHECK (memory_bytes > 0),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (bot_id, kind)
) STRICT;
