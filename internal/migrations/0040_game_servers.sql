-- Game servers. Purely additive: new tables and new columns only.
--
-- A row in bots is now either a language-runtime application (kind 'bot',
-- for example a Discord bot) or a game server (kind 'game') defined by a
-- pinned blueprint revision. Game servers keep a runtime value from the
-- existing catalog for compatibility; the runner takes their images,
-- installation and startup from the blueprint instead.

-- Blueprints define game servers: images, install steps, startup, variables,
-- configuration edits and ports. Revisions are immutable; a server pins the
-- revision it was created or last updated with.
CREATE TABLE blueprints (
    id                  TEXT PRIMARY KEY NOT NULL,
    slug                TEXT NOT NULL UNIQUE
                        CHECK (length(slug) BETWEEN 2 AND 64 AND slug NOT GLOB '*[^a-z0-9-]*'),
    name                TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
    category            TEXT NOT NULL CHECK (length(trim(category)) BETWEEN 1 AND 40),
    description         TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    source              TEXT NOT NULL CHECK (source IN ('builtin', 'custom')),
    current_revision    INTEGER NOT NULL CHECK (current_revision >= 1),
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE TABLE blueprint_revisions (
    blueprint_id        TEXT NOT NULL
                        REFERENCES blueprints(id) ON DELETE CASCADE,
    revision            INTEGER NOT NULL CHECK (revision >= 1),
    spec_yaml           TEXT NOT NULL CHECK (length(spec_yaml) BETWEEN 1 AND 262144),
    spec_sha256         TEXT NOT NULL CHECK (length(spec_sha256) = 64),
    created_at_ms       INTEGER NOT NULL,
    PRIMARY KEY (blueprint_id, revision)
) STRICT, WITHOUT ROWID;

ALTER TABLE bots ADD COLUMN kind TEXT NOT NULL DEFAULT 'bot'
    CHECK (kind IN ('bot', 'game'));
ALTER TABLE bots ADD COLUMN blueprint_id TEXT
    REFERENCES blueprints(id) ON DELETE RESTRICT;
ALTER TABLE bots ADD COLUMN blueprint_revision INTEGER
    CHECK (blueprint_revision IS NULL OR blueprint_revision >= 1);
ALTER TABLE bots ADD COLUMN image_choice TEXT NOT NULL DEFAULT ''
    CHECK (length(image_choice) <= 64);
ALTER TABLE bots ADD COLUMN install_state TEXT NOT NULL DEFAULT 'none'
    CHECK (install_state IN ('none', 'pending', 'installing', 'installed', 'failed'));

CREATE INDEX bots_blueprint_idx ON bots(blueprint_id) WHERE blueprint_id IS NOT NULL;
CREATE INDEX bots_kind_idx ON bots(kind, owner_id);

-- Allocations are IP:port reservations on a node. A game server publishes
-- every allocation assigned to it for both TCP and UDP; the primary one is
-- passed to the server as SERVER_PORT.
CREATE TABLE allocations (
    id                  TEXT PRIMARY KEY NOT NULL,
    node_id             TEXT NOT NULL
                        REFERENCES nodes(id) ON DELETE CASCADE,
    ip                  TEXT NOT NULL CHECK (length(ip) BETWEEN 2 AND 45),
    port                INTEGER NOT NULL CHECK (port BETWEEN 1024 AND 65535),
    alias               TEXT NOT NULL DEFAULT '' CHECK (length(alias) <= 253),
    notes               TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 200),
    bot_id              TEXT REFERENCES bots(id) ON DELETE SET NULL,
    is_primary          INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    created_at_ms       INTEGER NOT NULL,
    UNIQUE (node_id, ip, port)
) STRICT;

CREATE INDEX allocations_bot_idx ON allocations(bot_id) WHERE bot_id IS NOT NULL;
CREATE INDEX allocations_free_idx ON allocations(node_id, port) WHERE bot_id IS NULL;
CREATE UNIQUE INDEX allocations_one_primary_idx ON allocations(bot_id) WHERE is_primary = 1;
