-- rivetpanel:foreign-keys-off
--
-- Sites that are built or run: a site can now be backed by a bots row of the
-- new kind 'web' (sites.app_id). That row owns the site's checkout, builds,
-- GitHub link, environment, add-ons, logs and limits; the site keeps its
-- addresses and releases. Static sites built from a repository publish the
-- build output as a release; app sites run a container that the sites
-- listener proxies to.
--
-- bots and sites are rebuilt because SQLite cannot change a CHECK constraint
-- in place: bots.kind gains 'web' and bots.runtime gains 'php'; sites.mode
-- gains 'app'. Every column is copied by name. The runner checks foreign keys
-- after this file (foreign_key_check) before committing.

CREATE TABLE bots_new (
    id                  TEXT PRIMARY KEY NOT NULL,
    owner_id            TEXT NOT NULL
                        REFERENCES users(id) ON DELETE RESTRICT,
    node_id             TEXT NOT NULL
                        REFERENCES nodes(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL
                        CHECK (length(trim(name)) BETWEEN 1 AND 64),
    runtime             TEXT NOT NULL
                        CHECK (runtime IN (
                            'nodejs', 'python', 'rust',
                            'go', 'java', 'ruby', 'php'
                        )),
    image_ref           TEXT NOT NULL,
    argv_json           TEXT NOT NULL
                        CHECK (
                            json_valid(argv_json)
                            AND json_type(argv_json) = 'array'
                            AND json_array_length(argv_json) > 0
                        ),
    memory_bytes        INTEGER NOT NULL
                        CHECK (memory_bytes > 0),
    nano_cpus           INTEGER NOT NULL
                        CHECK (nano_cpus > 0),
    pids_limit          INTEGER NOT NULL DEFAULT 128
                        CHECK (pids_limit BETWEEN 1 AND 4096),
    desired_state       TEXT NOT NULL DEFAULT 'stopped'
                        CHECK (desired_state IN (
                            'stopped', 'running', 'deleted'
                        )),
    observed_state      TEXT NOT NULL DEFAULT 'stopped'
                        CHECK (observed_state IN (
                            'unknown', 'stopped', 'building',
                            'starting', 'running', 'stopping', 'failed'
                        )),
    generation          INTEGER NOT NULL DEFAULT 0
                        CHECK (generation >= 0),
    observed_generation INTEGER NOT NULL DEFAULT 0
                        CHECK (
                            observed_generation >= 0
                            AND observed_generation <= generation
                        ),
    container_id        TEXT UNIQUE,
    last_exit_code      INTEGER,
    last_error          TEXT,
    observed_at_ms      INTEGER,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL,
    entrypoint_json     TEXT
                        CHECK (entrypoint_json IS NULL OR (
                            json_valid(entrypoint_json) AND json_type(entrypoint_json) = 'array')),
    source_type         TEXT NOT NULL DEFAULT 'manual'
                        CHECK (source_type IN ('manual', 'template', 'github')),
    template_id         TEXT,
    network_enabled     INTEGER NOT NULL DEFAULT 1
                        CHECK (network_enabled IN (0, 1)),
    bandwidth_kbps      INTEGER
                        CHECK (bandwidth_kbps IS NULL OR bandwidth_kbps > 0),
    restart_policy      TEXT NOT NULL DEFAULT 'never'
                        CHECK (restart_policy IN ('never', 'on_failure')),
    restart_max_attempts INTEGER NOT NULL DEFAULT 5
                        CHECK (restart_max_attempts BETWEEN 0 AND 100),
    restart_backoff_initial_ms INTEGER NOT NULL DEFAULT 2000
                        CHECK (restart_backoff_initial_ms BETWEEN 100 AND 3600000),
    restart_backoff_max_ms INTEGER NOT NULL DEFAULT 300000
                        CHECK (restart_backoff_max_ms BETWEEN 100 AND 3600000),
    telemetry_key_hash  BLOB
                        CHECK (telemetry_key_hash IS NULL OR length(telemetry_key_hash) = 32),
    auto_backup         INTEGER NOT NULL DEFAULT 1
                        CHECK (auto_backup IN (0, 1)),
    restart_count       INTEGER NOT NULL DEFAULT 0
                        CHECK (restart_count >= 0),
    next_retry_at_ms    INTEGER,
    state_reason        TEXT
                        CHECK (state_reason IS NULL OR length(state_reason) BETWEEN 1 AND 32),
    last_started_at_ms  INTEGER,
    discord_user_id     TEXT NOT NULL DEFAULT '' CHECK (length(discord_user_id) <= 32),
    discord_username    TEXT NOT NULL DEFAULT '' CHECK (length(discord_username) <= 80),
    discord_avatar_url  TEXT NOT NULL DEFAULT '' CHECK (length(discord_avatar_url) <= 512),
    workspace_id        TEXT REFERENCES workspaces(id),
    build_command       TEXT
                        CHECK (build_command IS NULL OR length(build_command) BETWEEN 1 AND 4096),
    logo                BLOB CHECK (logo IS NULL OR length(logo) <= 262144),
    logo_type           TEXT CHECK (logo_type IS NULL OR logo_type IN ('image/png', 'image/jpeg')),
    logo_updated_at_ms  INTEGER,
    kind                TEXT NOT NULL DEFAULT 'bot'
                        CHECK (kind IN ('bot', 'game', 'web')),
    blueprint_id        TEXT
                        REFERENCES blueprints(id) ON DELETE RESTRICT,
    blueprint_revision  INTEGER
                        CHECK (blueprint_revision IS NULL OR blueprint_revision >= 1),
    image_choice        TEXT NOT NULL DEFAULT ''
                        CHECK (length(image_choice) <= 64),
    install_state       TEXT NOT NULL DEFAULT 'none'
                        CHECK (install_state IN ('none', 'pending', 'installing', 'installed', 'failed')),
    installed_version   TEXT NOT NULL DEFAULT ''
                        CHECK (length(installed_version) <= 64),
    jvm_args            TEXT NOT NULL DEFAULT '',
    jvm_args_updated_at_ms INTEGER NOT NULL DEFAULT 0,
    jvm_args_generation INTEGER NOT NULL DEFAULT 0,
    -- A one-shot command run after the build and before the start, with the
    -- full environment (database migrations).
    release_command     TEXT
                        CHECK (release_command IS NULL OR length(release_command) BETWEEN 1 AND 4096)
) STRICT;

INSERT INTO bots_new (id, owner_id, node_id, name, runtime, image_ref, argv_json, memory_bytes, nano_cpus, pids_limit,
    desired_state, observed_state, generation, observed_generation, container_id, last_exit_code, last_error,
    observed_at_ms, created_at_ms, updated_at_ms, entrypoint_json, source_type, template_id, network_enabled,
    bandwidth_kbps, restart_policy, restart_max_attempts, restart_backoff_initial_ms, restart_backoff_max_ms,
    telemetry_key_hash, auto_backup, restart_count, next_retry_at_ms, state_reason, last_started_at_ms,
    discord_user_id, discord_username, discord_avatar_url, workspace_id, build_command, logo, logo_type,
    logo_updated_at_ms, kind, blueprint_id, blueprint_revision, image_choice, install_state, installed_version,
    jvm_args, jvm_args_updated_at_ms, jvm_args_generation)
SELECT id, owner_id, node_id, name, runtime, image_ref, argv_json, memory_bytes, nano_cpus, pids_limit,
    desired_state, observed_state, generation, observed_generation, container_id, last_exit_code, last_error,
    observed_at_ms, created_at_ms, updated_at_ms, entrypoint_json, source_type, template_id, network_enabled,
    bandwidth_kbps, restart_policy, restart_max_attempts, restart_backoff_initial_ms, restart_backoff_max_ms,
    telemetry_key_hash, auto_backup, restart_count, next_retry_at_ms, state_reason, last_started_at_ms,
    discord_user_id, discord_username, discord_avatar_url, workspace_id, build_command, logo, logo_type,
    logo_updated_at_ms, kind, blueprint_id, blueprint_revision, image_choice, install_state, installed_version,
    jvm_args, jvm_args_updated_at_ms, jvm_args_generation
FROM bots;

DROP TABLE bots;
ALTER TABLE bots_new RENAME TO bots;

CREATE INDEX bots_owner_idx ON bots(owner_id);
CREATE INDEX bots_node_state_idx ON bots(node_id, desired_state);
CREATE UNIQUE INDEX bots_telemetry_key_idx ON bots(telemetry_key_hash)
    WHERE telemetry_key_hash IS NOT NULL;
CREATE INDEX bots_workspace_idx ON bots(workspace_id);
CREATE INDEX bots_blueprint_idx ON bots(blueprint_id) WHERE blueprint_id IS NOT NULL;
CREATE INDEX bots_kind_idx ON bots(kind, owner_id);

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
    mode                TEXT NOT NULL DEFAULT 'files' CHECK (mode IN ('page', 'files', 'app')),
    page_title          TEXT NOT NULL DEFAULT '' CHECK (length(page_title) <= 80),
    page_description    TEXT NOT NULL DEFAULT '' CHECK (length(page_description) <= 500),
    page_theme          TEXT NOT NULL DEFAULT 'midnight' CHECK (page_theme IN ('midnight', 'daylight', 'system')),
    page_accent         TEXT NOT NULL DEFAULT '#5865f2' CHECK (length(page_accent) = 7),
    page_html           TEXT NOT NULL DEFAULT '' CHECK (length(page_html) <= 65536),
    page_css            TEXT NOT NULL DEFAULT '' CHECK (length(page_css) <= 32768),
    widgets_public      INTEGER NOT NULL DEFAULT 0 CHECK (widgets_public IN (0, 1)),
    -- NULL only until start-up assigns the primary base domain.
    domain_id           TEXT REFERENCES site_base_domains(id),
    logo                BLOB CHECK (logo IS NULL OR length(logo) <= 262144),
    logo_type           TEXT CHECK (logo_type IS NULL OR logo_type IN ('image/png', 'image/jpeg')),
    logo_updated_at_ms  INTEGER,
    -- The web unit that builds (and for mode 'app', runs) the site.
    app_id              TEXT REFERENCES bots(id) ON DELETE SET NULL,
    framework           TEXT NOT NULL DEFAULT '' CHECK (length(framework) <= 32),
    build_output_dir    TEXT NOT NULL DEFAULT '' CHECK (length(build_output_dir) <= 200),
    app_port            INTEGER CHECK (app_port IS NULL OR app_port BETWEEN 1 AND 65535),
    health_path         TEXT NOT NULL DEFAULT '/'
                        CHECK (length(health_path) BETWEEN 1 AND 256 AND substr(health_path, 1, 1) = '/'),
    max_body_bytes      INTEGER NOT NULL DEFAULT 0 CHECK (max_body_bytes >= 0)
) STRICT;

INSERT INTO sites_new (id, workspace_id, owner_id, name, slug, spa, clean_urls, current_release, disabled,
    repo_full_name, repo_branch, repo_root, repo_token_user, created_at_ms, updated_at_ms, bot_id, mode,
    page_title, page_description, page_theme, page_accent, page_html, page_css, widgets_public, domain_id,
    logo, logo_type, logo_updated_at_ms)
SELECT id, workspace_id, owner_id, name, slug, spa, clean_urls, current_release, disabled,
    repo_full_name, repo_branch, repo_root, repo_token_user, created_at_ms, updated_at_ms, bot_id, mode,
    page_title, page_description, page_theme, page_accent, page_html, page_css, widgets_public, domain_id,
    logo, logo_type, logo_updated_at_ms
FROM sites;

DROP TABLE sites;
ALTER TABLE sites_new RENAME TO sites;

CREATE INDEX sites_workspace_idx ON sites(workspace_id);
CREATE UNIQUE INDEX sites_bot_idx ON sites(bot_id) WHERE bot_id IS NOT NULL;
CREATE UNIQUE INDEX sites_address_idx ON sites(ifnull(domain_id, ''), slug);
CREATE INDEX sites_domain_idx ON sites(domain_id);
CREATE UNIQUE INDEX sites_app_idx ON sites(app_id) WHERE app_id IS NOT NULL;

-- The commit a release was built from.
ALTER TABLE site_releases ADD COLUMN source_ref TEXT
    CHECK (source_ref IS NULL OR length(source_ref) <= 64);

-- Health probes of app sites dial the container on the apps network instead
-- of a published host port.
ALTER TABLE bot_health_probes ADD COLUMN target TEXT NOT NULL DEFAULT 'host'
    CHECK (target IN ('host', 'app'));
