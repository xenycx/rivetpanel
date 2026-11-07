-- Backups of sites made from uploaded files (and the file editor). Such a
-- site has no app unit, so the per-app backups never covered it: its serving
-- release is now archived on the panel's backup schedule (sites.auto_backup,
-- on by default) or on demand. Restoring publishes the archive as a new
-- release. Archive files live under <backup dir>/sites/<site_id>/.

ALTER TABLE sites ADD COLUMN auto_backup INTEGER NOT NULL DEFAULT 1
    CHECK (auto_backup IN (0, 1));

CREATE TABLE site_backups (
    id                  TEXT PRIMARY KEY NOT NULL,
    site_id             TEXT NOT NULL
                        REFERENCES sites(id) ON DELETE CASCADE,
    kind                TEXT NOT NULL CHECK (kind IN ('auto', 'manual')),
    -- The release the archive was made from ('' if it is gone since).
    release_id          TEXT NOT NULL DEFAULT '' CHECK (length(release_id) <= 64),
    label               TEXT CHECK (label IS NULL OR length(label) <= 100),
    files               INTEGER NOT NULL DEFAULT 0 CHECK (files >= 0),
    bytes               INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    sha256              TEXT NOT NULL DEFAULT '' CHECK (length(sha256) <= 64),
    created_by          TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms       INTEGER NOT NULL
) STRICT;

CREATE INDEX site_backups_site_idx ON site_backups(site_id, created_at_ms);
