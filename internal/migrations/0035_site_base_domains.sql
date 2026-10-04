-- rivetpanel:foreign-keys-off
-- Sites can be served under more than one base domain: a site's address is
-- <slug>.<its base domain>, and a slug is unique per base domain instead of
-- across the panel. Base domains come from the configuration
-- (RIVET_SITES_BASE_URL, RIVET_SITES_DOMAINS) or are added by an
-- administrator, and serve only after DNS ownership is verified.
--
-- The configured domains are inserted at start-up, which also assigns every
-- existing site to the primary domain (the host of RIVET_SITES_BASE_URL):
-- SQL cannot read the environment.
CREATE TABLE site_base_domains (
    id                  TEXT PRIMARY KEY NOT NULL,
    -- Lower-case ASCII (internationalized names in punycode form).
    domain              TEXT NOT NULL UNIQUE CHECK (length(domain) BETWEEN 1 AND 253),
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    is_primary          INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    label               TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 64),
    -- Optional host name or address shown as the DNS target.
    dns_target          TEXT NOT NULL DEFAULT '' CHECK (length(dns_target) <= 253),
    -- Listed in the environment file: trusted without a TXT record.
    from_config         INTEGER NOT NULL DEFAULT 0 CHECK (from_config IN (0, 1)),
    -- Expected value of the _rivetpanel-domain TXT record.
    token               TEXT NOT NULL CHECK (length(token) BETWEEN 16 AND 64),
    verified_at_ms      INTEGER,
    last_checked_at_ms  INTEGER,
    last_error          TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX site_base_domains_primary_idx ON site_base_domains(is_primary) WHERE is_primary = 1;

-- sites is rebuilt to drop the panel-wide UNIQUE on slug, which SQLite cannot
-- remove in place. Foreign keys are off for this migration, so dropping the
-- old table keeps every release, domain and assistant row that refers to it.
CREATE TABLE sites_new (
    id                  TEXT PRIMARY KEY NOT NULL,
    workspace_id        TEXT NOT NULL
                        REFERENCES workspaces(id),
    owner_id            TEXT NOT NULL
                        REFERENCES users(id),
    name                TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    -- <slug>.<base domain> is the site's address; unique per base domain.
    slug                TEXT NOT NULL
                        CHECK (length(slug) BETWEEN 3 AND 40 AND slug NOT GLOB '*[^a-z0-9-]*'),
    spa                 INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1)),
    clean_urls          INTEGER NOT NULL DEFAULT 1 CHECK (clean_urls IN (0, 1)),
    current_release     TEXT,
    disabled            INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    repo_full_name      TEXT CHECK (repo_full_name IS NULL OR length(repo_full_name) <= 201),
    repo_branch         TEXT CHECK (repo_branch IS NULL OR length(repo_branch) <= 255),
    repo_root           TEXT NOT NULL DEFAULT '' CHECK (length(repo_root) <= 200),
    repo_token_user     TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL,
    bot_id              TEXT REFERENCES bots(id) ON DELETE CASCADE,
    mode                TEXT NOT NULL DEFAULT 'files' CHECK (mode IN ('page', 'files')),
    page_title          TEXT NOT NULL DEFAULT '' CHECK (length(page_title) <= 80),
    page_description    TEXT NOT NULL DEFAULT '' CHECK (length(page_description) <= 500),
    page_theme          TEXT NOT NULL DEFAULT 'midnight' CHECK (page_theme IN ('midnight', 'daylight', 'system')),
    page_accent         TEXT NOT NULL DEFAULT '#5865f2' CHECK (length(page_accent) = 7),
    page_html           TEXT NOT NULL DEFAULT '' CHECK (length(page_html) <= 65536),
    page_css            TEXT NOT NULL DEFAULT '' CHECK (length(page_css) <= 32768),
    widgets_public      INTEGER NOT NULL DEFAULT 0 CHECK (widgets_public IN (0, 1)),
    -- NULL only until start-up assigns the primary base domain.
    domain_id           TEXT REFERENCES site_base_domains(id)
) STRICT;

INSERT INTO sites_new (id, workspace_id, owner_id, name, slug, spa, clean_urls, current_release, disabled,
    repo_full_name, repo_branch, repo_root, repo_token_user, created_at_ms, updated_at_ms,
    bot_id, mode, page_title, page_description, page_theme, page_accent, page_html, page_css, widgets_public)
SELECT id, workspace_id, owner_id, name, slug, spa, clean_urls, current_release, disabled,
    repo_full_name, repo_branch, repo_root, repo_token_user, created_at_ms, updated_at_ms,
    bot_id, mode, page_title, page_description, page_theme, page_accent, page_html, page_css, widgets_public
FROM sites;

DROP TABLE sites;
ALTER TABLE sites_new RENAME TO sites;

CREATE INDEX sites_workspace_idx ON sites(workspace_id);
CREATE UNIQUE INDEX sites_bot_idx ON sites(bot_id) WHERE bot_id IS NOT NULL;
CREATE UNIQUE INDEX sites_address_idx ON sites(ifnull(domain_id, ''), slug);
CREATE INDEX sites_domain_idx ON sites(domain_id);
