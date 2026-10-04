-- rivetpanel:foreign-keys-off

CREATE TABLE locations (
    id                  TEXT PRIMARY KEY NOT NULL,
    name                TEXT NOT NULL COLLATE NOCASE UNIQUE
                        CHECK (length(trim(name)) BETWEEN 1 AND 64),
    description         TEXT NOT NULL DEFAULT ''
                        CHECK (length(description) <= 500),
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

INSERT INTO locations (id, name, description, created_at_ms, updated_at_ms)
VALUES ('00000000-0000-0000-0000-000000000001', 'Local', 'The control plane host',
        CAST(unixepoch('subsec') * 1000 AS INTEGER), CAST(unixepoch('subsec') * 1000 AS INTEGER));

CREATE TABLE nodes_new (
    id                  TEXT PRIMARY KEY NOT NULL,
    location_id         TEXT NOT NULL
                        REFERENCES locations(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL UNIQUE,
    transport           TEXT NOT NULL
                        CHECK (transport IN ('local', 'https', 'agent')),
    endpoint            TEXT,
    enabled             INTEGER NOT NULL DEFAULT 1
                        CHECK (enabled IN (0, 1)),
    last_seen_at_ms      INTEGER,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL,
    CHECK (
        (transport IN ('local', 'agent') AND endpoint IS NULL)
        OR
        (transport = 'https' AND endpoint IS NOT NULL AND length(endpoint) > 0)
    )
) STRICT;

INSERT INTO nodes_new (id, location_id, name, transport, endpoint, enabled,
                       last_seen_at_ms, created_at_ms, updated_at_ms)
SELECT id, '00000000-0000-0000-0000-000000000001', name, transport, endpoint,
       enabled, last_seen_at_ms, created_at_ms, updated_at_ms
FROM nodes;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;
CREATE INDEX nodes_location_idx ON nodes(location_id, name);

CREATE TABLE node_agent_state (
    node_id              TEXT PRIMARY KEY NOT NULL
                         REFERENCES nodes(id) ON DELETE CASCADE,
    protocol_version     INTEGER NOT NULL DEFAULT 1
                         CHECK (protocol_version >= 1),
    capabilities_json    TEXT NOT NULL DEFAULT '{}'
                         CHECK (json_valid(capabilities_json) AND json_type(capabilities_json) = 'object'),
    connected            INTEGER NOT NULL DEFAULT 0
                         CHECK (connected IN (0, 1)),
    certificate_serial   TEXT,
    certificate_expires_at_ms INTEGER,
    connected_at_ms      INTEGER,
    disconnected_at_ms   INTEGER,
    updated_at_ms        INTEGER NOT NULL
) STRICT;

CREATE TABLE agent_commands (
    id                  TEXT PRIMARY KEY NOT NULL,
    node_id             TEXT NOT NULL
                        REFERENCES nodes(id) ON DELETE CASCADE,
    operation_id        TEXT,
    server_id           TEXT,
    generation          INTEGER NOT NULL DEFAULT 0
                        CHECK (generation >= 0),
    kind                TEXT NOT NULL
                        CHECK (length(kind) BETWEEN 1 AND 64),
    payload_json        TEXT NOT NULL DEFAULT '{}'
                        CHECK (json_valid(payload_json) AND json_type(payload_json) = 'object'),
    idempotency_key     TEXT NOT NULL
                        CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    status              TEXT NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'delivered', 'succeeded', 'failed', 'expired')),
    deadline_at_ms      INTEGER NOT NULL,
    created_at_ms       INTEGER NOT NULL,
    delivered_at_ms     INTEGER,
    completed_at_ms     INTEGER,
    error               TEXT,
    UNIQUE (node_id, idempotency_key),
    CHECK (deadline_at_ms > created_at_ms)
) STRICT;

CREATE INDEX agent_commands_delivery_idx
    ON agent_commands(node_id, status, deadline_at_ms, created_at_ms);
CREATE INDEX agent_commands_operation_idx
    ON agent_commands(operation_id) WHERE operation_id IS NOT NULL;
