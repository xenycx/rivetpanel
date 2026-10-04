-- Static sites served by the panel's separate sites listener. A site belongs
-- to a workspace and serves one immutable release at a time; deploying makes
-- a new release and switches to it atomically, and older releases remain for
-- rollback. Custom domains are served only after DNS ownership is verified.
CREATE TABLE sites (
    id                  TEXT PRIMARY KEY NOT NULL,
    workspace_id        TEXT NOT NULL
                        REFERENCES workspaces(id),
    owner_id            TEXT NOT NULL
                        REFERENCES users(id),
    name                TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    -- <slug>.<RIVET_SITES_DOMAIN> is the default hostname.
    slug                TEXT NOT NULL UNIQUE
                        CHECK (length(slug) BETWEEN 3 AND 40 AND slug NOT GLOB '*[^a-z0-9-]*'),
    spa                 INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1)),
    clean_urls          INTEGER NOT NULL DEFAULT 1 CHECK (clean_urls IN (0, 1)),
    current_release     TEXT,
    -- Suspended by an administrator: nothing is served.
    disabled            INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    repo_full_name      TEXT CHECK (repo_full_name IS NULL OR length(repo_full_name) <= 201),
    repo_branch         TEXT CHECK (repo_branch IS NULL OR length(repo_branch) <= 255),
    repo_root           TEXT NOT NULL DEFAULT '' CHECK (length(repo_root) <= 200),
    repo_token_user     TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE INDEX sites_workspace_idx ON sites(workspace_id);

CREATE TABLE site_releases (
    id                  TEXT PRIMARY KEY NOT NULL,
    site_id             TEXT NOT NULL
                        REFERENCES sites(id) ON DELETE CASCADE,
    source              TEXT NOT NULL CHECK (source IN ('upload', 'github')),
    source_label        TEXT CHECK (source_label IS NULL OR length(source_label) <= 300),
    files               INTEGER NOT NULL CHECK (files >= 0),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    actor_id            TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms       INTEGER NOT NULL
) STRICT;

CREATE INDEX site_releases_site_idx ON site_releases(site_id, created_at_ms);

CREATE TABLE site_domains (
    -- Lower-case ASCII (internationalized names in punycode form).
    domain              TEXT PRIMARY KEY NOT NULL CHECK (length(domain) BETWEEN 4 AND 253),
    site_id             TEXT NOT NULL
                        REFERENCES sites(id) ON DELETE CASCADE,
    -- Expected value of the _rivetpanel-verify TXT record.
    token               TEXT NOT NULL CHECK (length(token) BETWEEN 16 AND 64),
    verified_at_ms      INTEGER,
    last_checked_at_ms  INTEGER,
    last_error          TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    created_at_ms       INTEGER NOT NULL
) STRICT;

CREATE INDEX site_domains_site_idx ON site_domains(site_id);
