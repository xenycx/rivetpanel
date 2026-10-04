-- Agent connections: certificate lifecycle, node draining and agent details.
-- Purely additive.

-- Every node certificate the panel issued. The agent listener refuses a
-- certificate whose serial is unknown, revoked, expired, or not the node's.
CREATE TABLE agent_certificates (
    serial              TEXT PRIMARY KEY NOT NULL
                        CHECK (length(serial) BETWEEN 1 AND 64),
    node_id             TEXT NOT NULL
                        REFERENCES nodes(id) ON DELETE CASCADE,
    issued_at_ms        INTEGER NOT NULL,
    expires_at_ms       INTEGER NOT NULL,
    revoked_at_ms       INTEGER,
    revoked_reason      TEXT CHECK (revoked_reason IS NULL OR length(revoked_reason) <= 200),
    CHECK (expires_at_ms > issued_at_ms)
) STRICT;

CREATE INDEX agent_certificates_node_idx ON agent_certificates(node_id, issued_at_ms);

-- Certificates issued before this table existed.
INSERT INTO agent_certificates (serial, node_id, issued_at_ms, expires_at_ms)
SELECT certificate_serial, node_id, updated_at_ms, certificate_expires_at_ms
FROM node_agent_state
WHERE certificate_serial IS NOT NULL AND certificate_expires_at_ms IS NOT NULL
  AND certificate_expires_at_ms > updated_at_ms;

-- A draining node keeps running what it has but receives no new servers.
ALTER TABLE nodes ADD COLUMN draining INTEGER NOT NULL DEFAULT 0
    CHECK (draining IN (0, 1));
-- The address players and users connect to for this node's servers.
ALTER TABLE nodes ADD COLUMN public_address TEXT NOT NULL DEFAULT ''
    CHECK (length(public_address) <= 253);

ALTER TABLE node_agent_state ADD COLUMN agent_version TEXT NOT NULL DEFAULT ''
    CHECK (length(agent_version) <= 64);
ALTER TABLE node_agent_state ADD COLUMN hostname TEXT NOT NULL DEFAULT ''
    CHECK (length(hostname) <= 253);
