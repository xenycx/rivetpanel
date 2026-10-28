-- Per-account quotas: limits an administrator sets for one account, in place
-- of the panel-wide defaults (RIVET_MAX_BOTS_PER_USER,
-- RIVET_MAX_SITES_PER_USER and RIVET_USER_MEMORY_BYTES). Purely additive:
-- with no rows every account keeps the panel defaults.
--
-- A NULL column falls back to the panel default (unlimited where the panel
-- has none); a number is the limit, and 0 allows none. max_apps counts every
-- app (bots, game servers and web app units together, like
-- RIVET_MAX_BOTS_PER_USER); max_bots, max_games and max_web_apps count one
-- kind each. Apps count until deleted, running or stopped. The application
-- enforces these when apps and sites are created, resized or transferred;
-- administrators are exempt, as from the panel defaults.
CREATE TABLE user_quotas (
    user_id        TEXT PRIMARY KEY NOT NULL
                   REFERENCES users(id) ON DELETE CASCADE,
    max_apps       INTEGER CHECK (max_apps IS NULL OR max_apps >= 0),
    max_bots       INTEGER CHECK (max_bots IS NULL OR max_bots >= 0),
    max_games      INTEGER CHECK (max_games IS NULL OR max_games >= 0),
    max_web_apps   INTEGER CHECK (max_web_apps IS NULL OR max_web_apps >= 0),
    max_sites      INTEGER CHECK (max_sites IS NULL OR max_sites >= 0),
    memory_bytes   INTEGER CHECK (memory_bytes IS NULL OR memory_bytes >= 0),
    updated_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL
) STRICT;
