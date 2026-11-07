-- rivetpanel:foreign-keys-off
--
-- Container apps and stacks. A bots row of the new kind 'container' runs a
-- Docker image its owner chose (bots.image_ref, within the administrator's
-- registry policy) under the same isolation as every other container; its
-- image settings live in bot_containers. A stack groups container apps
-- imported from one Compose file; its services reach each other by name on
-- a private network.
--
-- bots is rebuilt because SQLite cannot change a CHECK constraint in place:
-- kind gains 'container', runtime gains 'image' (the image is the app's own,
-- not a catalog runtime), and a container's command may be empty (the
-- image's own command). Every column is copied by name. The runner checks
-- foreign keys after this file (foreign_key_check) before committing.

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
                            'go', 'java', 'ruby', 'php', 'image'
                        )),
    image_ref           TEXT NOT NULL,
    argv_json           TEXT NOT NULL
                        CHECK (
                            json_valid(argv_json)
                            AND json_type(argv_json) = 'array'
                            AND (json_array_length(argv_json) > 0 OR kind = 'container')
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
                        CHECK (kind IN ('bot', 'game', 'web', 'container')),
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
    desired_state, observed_state, generation, observed_generation, container_id, last_exit_code,
    last_error, observed_at_ms, created_at_ms, updated_at_ms, entrypoint_json, source_type,
    template_id, network_enabled, bandwidth_kbps, restart_policy, restart_max_attempts,
    restart_backoff_initial_ms, restart_backoff_max_ms, telemetry_key_hash, auto_backup,
    restart_count, next_retry_at_ms, state_reason, last_started_at_ms, discord_user_id,
    discord_username, discord_avatar_url, workspace_id, build_command, logo, logo_type,
    logo_updated_at_ms, kind, blueprint_id, blueprint_revision, image_choice, install_state,
    installed_version, jvm_args, jvm_args_updated_at_ms, jvm_args_generation, release_command)
SELECT id, owner_id, node_id, name, runtime, image_ref, argv_json, memory_bytes, nano_cpus, pids_limit,
    desired_state, observed_state, generation, observed_generation, container_id, last_exit_code,
    last_error, observed_at_ms, created_at_ms, updated_at_ms, entrypoint_json, source_type,
    template_id, network_enabled, bandwidth_kbps, restart_policy, restart_max_attempts,
    restart_backoff_initial_ms, restart_backoff_max_ms, telemetry_key_hash, auto_backup,
    restart_count, next_retry_at_ms, state_reason, last_started_at_ms, discord_user_id,
    discord_username, discord_avatar_url, workspace_id, build_command, logo, logo_type,
    logo_updated_at_ms, kind, blueprint_id, blueprint_revision, image_choice, install_state,
    installed_version, jvm_args, jvm_args_updated_at_ms, jvm_args_generation, release_command
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

CREATE TABLE stacks (
    id                  TEXT PRIMARY KEY NOT NULL,
    workspace_id        TEXT NOT NULL
                        REFERENCES workspaces(id),
    owner_id            TEXT NOT NULL
                        REFERENCES users(id),
    name                TEXT NOT NULL
                        CHECK (length(trim(name)) BETWEEN 1 AND 64),
    -- The Compose file the stack was imported from, kept for reference.
    compose_yaml        TEXT NOT NULL DEFAULT ''
                        CHECK (length(compose_yaml) <= 262144),
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE INDEX stacks_workspace_idx ON stacks(workspace_id);

CREATE TABLE bot_containers (
    bot_id              TEXT PRIMARY KEY NOT NULL
                        REFERENCES bots(id) ON DELETE CASCADE,
    -- '' runs as the panel's container user; otherwise a numeric uid:gid.
    run_user            TEXT NOT NULL DEFAULT ''
                        CHECK (length(run_user) <= 23),
    writable_json       TEXT NOT NULL DEFAULT '[]'
                        CHECK (json_valid(writable_json) AND json_type(writable_json) = 'array'),
    volumes_json        TEXT NOT NULL DEFAULT '[]'
                        CHECK (json_valid(volumes_json) AND json_type(volumes_json) = 'array'),
    stack_id            TEXT
                        REFERENCES stacks(id) ON DELETE SET NULL,
    service             TEXT NOT NULL DEFAULT ''
                        CHECK (length(service) <= 63),
    depends_json        TEXT NOT NULL DEFAULT '[]'
                        CHECK (json_valid(depends_json) AND json_type(depends_json) = 'array')
) STRICT;

CREATE UNIQUE INDEX bot_containers_service_idx ON bot_containers(stack_id, service)
    WHERE stack_id IS NOT NULL;
