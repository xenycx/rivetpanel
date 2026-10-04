-- Public status page. Purely additive.
--
-- status_components: what the public page shows. kind/ref_id name the
-- source (the panel, a node or a bot) and are never published; name is the
-- public, administrator-chosen label.
CREATE TABLE status_components (
    id             TEXT PRIMARY KEY NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('panel', 'node', 'bot')),
    ref_id         TEXT NOT NULL DEFAULT '' CHECK (length(ref_id) <= 64 AND (kind <> 'panel' OR ref_id = '')),
    name           TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    description    TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 300),
    position       INTEGER NOT NULL DEFAULT 0,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL,
    UNIQUE (kind, ref_id)
) STRICT;

-- status_incidents: manual incidents and scheduled maintenance.
CREATE TABLE status_incidents (
    id              TEXT PRIMARY KEY NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('incident', 'maintenance')),
    title           TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    impact          TEXT NOT NULL CHECK (impact IN ('none', 'minor', 'major', 'critical')),
    status          TEXT NOT NULL CHECK (
                        (kind = 'incident' AND status IN ('investigating', 'identified', 'monitoring', 'resolved')) OR
                        (kind = 'maintenance' AND status IN ('scheduled', 'in_progress', 'completed'))),
    starts_at_ms    INTEGER,
    ends_at_ms      INTEGER,
    created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL,
    resolved_at_ms  INTEGER
) STRICT;
CREATE INDEX status_incidents_open_idx ON status_incidents(resolved_at_ms, created_at_ms DESC);

CREATE TABLE status_incident_components (
    incident_id   TEXT NOT NULL REFERENCES status_incidents(id) ON DELETE CASCADE,
    component_id  TEXT NOT NULL REFERENCES status_components(id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, component_id)
) STRICT;

-- status_incident_updates: the timeline (plain text, shown as text).
CREATE TABLE status_incident_updates (
    id             TEXT PRIMARY KEY NOT NULL,
    incident_id    TEXT NOT NULL REFERENCES status_incidents(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK (length(status) BETWEEN 1 AND 20),
    body           TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 5000),
    author_id      TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms  INTEGER NOT NULL
) STRICT;
CREATE INDEX status_incident_updates_idx ON status_incident_updates(incident_id, created_at_ms DESC);

-- status_samples: per component and UTC day (days since the Unix epoch), how
-- many periodic samples saw each state. Unknown samples are not counted.
-- The application keeps 90 days.
CREATE TABLE status_samples (
    component_id  TEXT NOT NULL REFERENCES status_components(id) ON DELETE CASCADE,
    day           INTEGER NOT NULL,
    operational   INTEGER NOT NULL DEFAULT 0,
    degraded      INTEGER NOT NULL DEFAULT 0,
    outage        INTEGER NOT NULL DEFAULT 0,
    maintenance   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (component_id, day)
) STRICT;
CREATE INDEX status_samples_day_idx ON status_samples(day);
