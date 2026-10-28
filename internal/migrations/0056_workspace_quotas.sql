-- Workspace limits replace per-account limits. An administrator sets them on
-- a workspace (Administration → Workspaces), and every member's apps and
-- sites in that workspace count against them together.
--
-- A NULL column falls back to the panel default (Panel settings → Default
-- limits, else RIVET_MAX_BOTS_PER_USER, RIVET_MAX_SITES_PER_USER and
-- RIVET_USER_MEMORY_BYTES; unlimited where neither sets one); a number is
-- the limit, and 0 allows none. max_apps counts every app (bots, game
-- servers and web app units together); max_bots, max_games and max_web_apps
-- count one kind each; nano_cpus is the CPU assigned to all apps together.
-- Apps count until deleted, running or stopped. The application enforces
-- these when apps and sites are created, resized, moved or transferred;
-- workspaces owned by administrators are exempt.
CREATE TABLE workspace_quotas (
    workspace_id   TEXT PRIMARY KEY NOT NULL
                   REFERENCES workspaces(id) ON DELETE CASCADE,
    max_apps       INTEGER CHECK (max_apps IS NULL OR max_apps >= 0),
    max_bots       INTEGER CHECK (max_bots IS NULL OR max_bots >= 0),
    max_games      INTEGER CHECK (max_games IS NULL OR max_games >= 0),
    max_web_apps   INTEGER CHECK (max_web_apps IS NULL OR max_web_apps >= 0),
    max_sites      INTEGER CHECK (max_sites IS NULL OR max_sites >= 0),
    memory_bytes   INTEGER CHECK (memory_bytes IS NULL OR memory_bytes >= 0),
    nano_cpus      INTEGER CHECK (nano_cpus IS NULL OR nano_cpus >= 0),
    updated_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL
) STRICT;

-- An account's limits become the limits of its personal workspace.
INSERT INTO workspace_quotas (workspace_id, max_apps, max_bots, max_games, max_web_apps, max_sites,
    memory_bytes, nano_cpus, updated_by, created_at_ms, updated_at_ms)
SELECT w.id, q.max_apps, q.max_bots, q.max_games, q.max_web_apps, q.max_sites,
    q.memory_bytes, NULL, q.updated_by, q.created_at_ms, q.updated_at_ms
FROM user_quotas q JOIN workspaces w ON w.owner_id = q.user_id AND w.personal = 1;

DROP TABLE user_quotas;
