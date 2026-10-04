# Platform overhaul implementation status

Updated 2026-10-05 for RivetPanel 0.5.0 (see `CHANGELOG.md`). This is the durable checklist for the requested platform
overhaul. A checked item is implemented in the current working tree; partial
items state exactly what remains.

## RivetPanel repurposing (in progress)

- [x] Product namespace changed across the Go module, executable, web package,
  container image, systemd/Compose deployment, SDKs, environment variables,
  default data/configuration paths, container labels, DNS records, UI and
  operator documentation. Historical changelog entries remain unchanged.
- [x] Preview module catalog with `off`, `preview` and `stable` states,
  `RIVET_MODULES` overrides and authenticated `GET /api/v1/modules` discovery.
  Existing application, site and AI capabilities are stable; game servers are
  a default-on preview; remote node, identity and support modules default to
  off. Enforcement is application-level configuration and API
  discovery; routes for future modules do not exist yet. Scope decisions
  (2026-10-04): Proxmox virtual machines are **dropped** (not planned; the
  `virtual_machines` catalog entry was removed) and
  panel extensions are in the **backlog** (not scheduled; the `extensions`
  entry is likewise inert).
- [x] Clean-break database identity (`0000_rivetpanel_identity.sql`). Migration
  refuses a non-empty database without the RivetPanel schema marker before
  creating its migration ledger or modifying tables. Enforcement is in the
  SQLite migration layer. Automatic conversion is intentionally absent.
- [x] Former-installation detection (`internal/legacy`). At start and in
  `rivetpanel doctor`, environment variables with the former prefixes, the
  former data directory, a former database beside `RIVET_DB_PATH` and
  **running** containers with the former management label are logged as
  warnings and shown in Diagnostics. Detection only: enforcement is limited
  to the backup layer, which refuses former backups in `backup-verify` and
  `restore`; nothing is read beyond existence or changed. Manual clean-up and
  re-creation steps: `docs/upgrading-from-0.4.md`.
- [x] Container deployment runs bots (verified end to end with a locally
  built image, a host-directory data mount at a different host path and a
  Node.js bot). Enforced by the application at start: in a container the panel
  inspects its own container through Docker, translates bind-mount sources
  (workspaces, add-on data, AI scratch) to host paths, and refuses to start
  when a needed path is on no mount or in a named volume (Docker refuses
  private bind mounts from its data root). If its own container cannot be
  identified it only warns and assumes identical paths. `rivet-agent` has no
  such translation and is supported directly on the node, not in a container.
  The image runs as root by design (documented in `docs/container.md`) and has
  a `HEALTHCHECK` (`rivetpanel health`).
- [x] Agent CA creation moved out of the systemd sandbox: `rivetpanel keygen`
  (or `keygen --agent-ca`) creates it; the unit keeps `/etc/rivetpanel`
  read-only (enforced by systemd `ReadOnlyPaths`), and the panel only creates
  the CA itself where the key directory is writable (development, containers).
- [x] Container publishing gated on tests: the GitHub workflow's publish job
  depends on `go vet`, `go test`, version/namespace checks and `npm run
  check`; tag builds fail unless the tag is `v` + `VERSION`. Enforced by CI
  configuration only (not verified here, no runner available locally).
- [x] Applications stay represented as `bots` in storage and `/api/v1/bots`;
  game servers use `kind = game` and the `/servers` UI. This is an intentional
  compatibility decision, not unfinished renaming.
- [x] Remote-node connection and execution foundation (migrations `0038`,
  `0039`, `0042`): locations and agent nodes; one-use hashed enrollment;
  transactional 30-day node certificate issuance and rotation; TLS 1.3 client
  authentication with database checks for node, serial, expiry, revocation and
  enabled state; yamux agent/panel APIs; reconnect and resynchronization;
  remote lifecycle, files, logs, stdin, stats and Minecraft queries; cross-node
  server access checks; node administration, placement and draining; agent CA
  backup/restore; CLI enrollment recovery. Real-Docker integration covers
  enroll → connect → create/files/start/console/stop/delete → revoke.
  Rotation retires superseded certificates: the first successful mTLS
  handshake with a certificate revokes every older unrevoked certificate of
  that node (reason `superseded`, ordered by issue order in SQLite, no schema
  change), so an old `node.pem` is refused afterwards; a renewal that was
  issued but never used does not revoke the old one, so an agent that crashed
  before saving its renewal can still reconnect. Enforced by the hub at the
  application/TLS/SQLite level. Unit tests: `internal/agenthub`
  (`hub_auth_test.go`: valid certificate accepted; other-CA, self-signed,
  expired (certificate and SQLite), revoked, unknown-serial, disabled-node and
  serial/identity-mismatch certificates refused; cross-node bot read, list,
  delete, observe, env, install-state, version and build access answer 404;
  rotation CSRs cannot claim another node; `hub_rotation_test.go`: superseded
  certificate refused after the renewal connects, unused renewal keeps the old
  certificate valid, a later renewal retires both) and `internal/agentclient`
  (`rotation_test.go`: near-expiry renewal, atomic 0600 `node.pem`
  replacement, new serial recorded, reconnect, old serial refused afterwards;
  fresh certificates are not renewed).
  Enforcement boundaries: identity and authorization are application/TLS/SQLite
  level; file containment is the agent filesystem layer; resource and container
  hardening are enforced by Docker on the node. Capability reports are stored
  evidence only, not attestation or kernel/network enforcement. The control
  plane still holds its Docker socket when the local runner is enabled.
- [~] Game hosting (Preview 2, migration `0040`): Minecraft Java server types
  (Paper, Purpur, Vanilla, Fabric, Forge, NeoForge, Folia, Velocity) defined by
  validated YAML blueprints with immutable revisions; provider version lists;
  checksum-verified downloads committed atomically; automatic Java selection
  from the highest of the provider's declared minimum (PaperMC Fill v3), Mojang
  metadata and the downloaded jar's class-file version, raising a too-old
  stored image choice at installation (fixes Velocity 4 on Java 21); install scripts in the hardened build container;
  `server.properties`/`eula.txt` written before each start; graceful stop
  through the console; Server List Ping status; IP:port allocations with a
  pool, automatic port selection and host-port probing; administrator
  blueprint import/export/hide and allocation management. Verified on real
  Docker 29.7.2 with Paper 26.3 (Java 25) and Forge 1.20.1 (Java 17):
  install, start, ping, graceful stop (`tests/integration/game_test.go`,
  opt-in `RIVET_TEST_GAMES=1`). Enforcement: blueprint/variable/allocation
  rules, EULA, checksums and permissions are application level; resource
  limits, non-root, capability drop and read-only root are Docker-runtime
  level. **Not enforced or not implemented**: disk quotas, bandwidth, egress
  filtering, Bedrock types, mounts, FastDL, proxies/subdomains,
  firewall rules and lifecycle hooks. Remote placement
  works and Modrinth installation works remotely (see below); remote
  allocations are probed by the node's agent (protocol 8, see below).
- [x] JVM arguments for Java game servers (migration `0053`, schema
  assertion 54, tables assertion unchanged at 47, no agent protocol change):
  `startup.jvm_args`/`jvm_args_default` blueprint fields, panel-provided
  `SERVER_JVM_ARGS` placed unquoted after `-Xmx` in new revisions of all eight
  Minecraft built-ins (Velocity's `-XX:+UseG1GC` became its default),
  `bots.jvm_args`, `jvm_args_updated_at_ms`, `jvm_args_generation`,
  `PUT /api/v1/bots/{id}/game/jvm-args` (environment permission, audited as
  `game.jvm_args` with a preset name only), presets (Aikar's flags with
  >12 GB values, ZGC with `ZGenerational` only on Java 21-23, None), Startup
  page box with Java version, preset Java warnings and "Restart to apply"
  (running generation not newer than the one the options were saved at).
  Enforcement: `blueprint.CheckJVMArgs` is application level, applied on save
  and again by the runner before each start (invalid stored options are
  dropped); it allows only JVM option shapes and a shell-inert character set
  because the value is word-split by `/bin/sh`. Whether the JVM accepts an
  option for its Java version is not checked (the JVM refuses to start).
  Existing servers get the setting through the blueprint "Update" flow.
- [x] SteamCMD game servers (no migration, no agent protocol change):
  `install.steamcmd` blueprint step (app id, optional beta-branch template,
  validate, image, `timeout_minutes` 5-240, `max_size_gb` 1-500), run by the
  runner in a separate `steamcmd/steamcmd` install container before the
  install script, always with an anonymous login (no credential fields exist;
  unknown YAML keys are refused), retried up to 3 times, success detected
  from SteamCMD's own report, `steamclient.so` copied to `.steam/sdk64|32`,
  Steam build id recorded as the installed version. New blueprint fields:
  `startup.stop_signal` (SIGINT/SIGTERM/SIGQUIT/SIGHUP, sent by the runner
  through Docker before the normal stop), `ports.contiguous` (automatic
  allocation of consecutive ports, pool runs first), `SERVER_PORT_1..10`
  system variables (additional allocations in port order), `query: steam` +
  `query_allocation`, config format `ini` (with section), optional `runtime`.
  Install scripts now receive the server's declared variables and the
  panel-provided ones (not hidden agreement records). Built-in types Valheim,
  Rust and Project Zomboid written from public documentation. Steam A2S_INFO
  query in `internal/gamequery` (challenge handling, split responses
  refused), unit-tested against a fake UDP server; local nodes only (the
  agent relays only the Minecraft ping). Verified: real-Docker
  `TestSteamCMDInstall` (`RIVET_TEST_STEAM=1`, Steamworks redistributable app
  1007: anonymous download, install script with variables, consecutive ports,
  `SERVER_PORT_1`, SIGINT graceful stop); Valheim downloaded and started by
  hand with the blueprint's command line, SIGINT saved the world, but it did
  not answer A2S while unlisted. Rust and Project Zomboid were not run.
  Enforcement: the time limit is enforced by the runner (install container
  removed); the size limit is a watchdog inside the install container
  (application level, **not** a disk quota); anonymous-only is enforced by
  blueprint validation and the generated script; resource limits and
  hardening of the install container are Docker-runtime level.
- [x] Pterodactyl egg importer (no migration): `blueprint.ConvertEgg` and
  `POST /api/v1/admin/blueprints/egg-preview` (`blueprints.manage`, audited
  as `admin.blueprint_egg_preview`) turn a PTDL_v1/PTDL_v2 egg (≤ 512 KiB,
  exactly one JSON object) into a blueprint YAML draft plus warnings; nothing
  is stored, fetched or run. Images, startup (`{{VAR}}`, Pterodactyl
  placeholders), stop command/`^C`, done marker, variables (Laravel rules
  mapped where possible), install script (own shell, `/mnt/server` →
  `/workspace`) and container, `properties`/`ini`/`file` config parsers and
  the `eula` feature are mapped; everything else (yaml/json/xml parsers,
  hidden variables, other features, file deny lists, unknown fields,
  unsupported rules/regexes, package-manager calls) is listed as a warning.
  The administrator reviews/edits the YAML and saves it through the normal
  validated import (custom source, immutable revisions). UI: Administration →
  Server types → Import Pterodactyl egg. Tests: hand-written fixtures in
  `internal/blueprint/testdata`, `internal/api/games_steam_test.go`.
  Enforcement: application level (validation, permission); imported install
  scripts run like any blueprint script in the hardened install container as
  the unprivileged server user (Docker-runtime level).
  Browser-checked on a scratch local panel: egg upload → draft with 15
  warnings → reviewed save → offered under New server; a Valheim server was
  created from the new SteamCMD blueprint (ports 2456/2457 consecutive,
  SIGINT stop, SteamCMD shown on Startup). Its 2.2 GB install was not run
  through the panel.
- [x] Minecraft extras (migration `0041`): Players page (ops, whitelist, bans,
  kick/ban through the console), `server.properties` editor that preserves the
  file and detects concurrent changes, Modrinth plugin/mod browser and
  installer (loader check, CDN host allowlist, SHA-512 verification, required
  dependencies listed), one-shot console commands (`POST …/command`, power
  permission) and task-chain schedules (≤20 steps, ≤1 h of waits, per-step
  permission re-check, one run at a time). Enforcement: application level.
- [x] File-manager archives: folder download as zip, compress to zip, and
  staged extraction of zip/mrpack/tar.gz/tgz/tar already in the workspace with
  entry, size and compression-ratio limits (application level, descriptor-
  relative writes inside the workspace).
- [~] Remote-node completeness: manual, scheduled and pre-restore backups now
  stream through the authenticated agent connection into panel storage. Remote
  restore uses the same staged archive validation and keeps the old workspace
  in the agent's filesystem journal until the application-level database
  update is confirmed.
- [x] Remote GitHub deployment (agent protocol 3): manual, webhook and polled
  deploys for bots on connected agent nodes, and node choice when creating a
  bot from GitHub. The panel downloads the tarball (1 GiB compressed cap) and
  streams it over the mTLS node connection; it is never unpacked on the panel
  host. The agent stages and validates the whole archive (entry, size and
  path limits) before any workspace change; the panel then re-checks the bot,
  its repository link and its node (application level) before telling the
  agent to swap. Previous files stay in the agent's filesystem journal until
  the panel records the commit; database failure rolls back, an unconfirmed
  transaction expires to rollback after 5 minutes, and an agent crash is
  rolled back by journal recovery at agent start. Restores and deployments
  share one transaction registry (`internal/nodetx`). Node identity is the
  mTLS certificate checked against SQLite; path containment is the agent's
  descriptor-relative filesystem layer; agent capability reports are not used
  for authorization. Admin-only node choice for new bots and game servers is
  enforced by the application.
- [x] Remote GitHub publish/push (agent protocol 4): publish to a new
  repository and push to a linked one for bots on connected agent nodes. The
  agent selects the files (`POST /node/v1/bots/:id/push-set`) with the same
  shared code as local publishing: `.gitignore` files, built-in dependency/
  metadata exclusions and `.env` secret-file exclusion, and the same 5,000
  files / 200 MiB / 25 MiB-per-file limits, which the agent refuses to raise.
  The panel reads only the selected files, one at a time with a per-file cap,
  through the existing contained `files/content` endpoint; nothing is
  written to the panel's disk. The commit is built through the GitHub API at
  the panel and recorded as `last_sha`; publish/push run under the per-bot
  coordinator claim (concurrent deploy/restore refused). Offline nodes are
  refused before a repository is created or a push queued. Enforcement:
  identity is mTLS + SQLite, file selection rules and limits are application
  level on the agent, path containment is the agent's descriptor-relative
  filesystem layer; capability reports are not consulted.
- [x] Remote package manager (no wire change): manifests are listed, read
  and written through the agent's existing contained `files` and
  `files/content` routes; writes carry `If-Match` on the revision that was
  read (or `If-None-Match: *` for a new manifest), so a concurrent change
  answers 409 and nothing is overwritten. Edit-files permission and the
  deployment/restore lock are enforced by the application; the 1 MiB manifest
  bound is enforced at the panel on read and write; path containment is the
  agent's filesystem layer. Offline nodes are refused; the panel's disk is
  never used for a remote bot.
- [x] Remote AI file tools (agent protocol 5): list/read/search use the
  agent's contained file API with the same protected-path refusal, 1 MiB
  per-file limit and redaction (all application-level at the panel); search
  reads at most 2,000 files. Proposed changes and Undo are revision-checked
  patches applied by the agent through `filesystem.ApplyPatch` and the shared
  `internal/nodetx` registry (`POST /node/v1/bots/:id/patch`, completed via
  `/transactions/:tx`): reversible until the panel records the change set, an
  unconfirmed commit is retried once then rolled back (and expires to
  rollback on the node), an agent crash is rolled back by journal recovery.
  Offline nodes are refused; the panel's disk is never used. Isolated AI
  diagnostics still copy the workspace from the panel and are refused for
  remote bots.
- [x] Remote template creation (no wire change, protocol stays 5): admin-only
  node choice and offline refusal before any row is written (application);
  template size bound (32 files, 1 MiB per file, 8 MiB total) checked at the
  panel before insert and again by the agent's patch ceilings. The row is
  inserted, the agent creates the directory through the existing
  `POST /node/v1/workspaces/:id` route, then all files go as one create-only
  `filesystem.ApplyPatch` through the shared `internal/nodetx` registry and are
  committed as the last creation step (lost answer retried once, then
  rollback). Any failure after the insert marks the bot deleted and hands it
  to the agent's normal purge (directory and row removed by the agent; an
  unreachable agent does it on reconnect). Path containment is the agent's
  filesystem layer; the panel's disk is never used.
- [x] Remote Modrinth installation (no wire change): edit-files permission,
  deployment/restore lock and offline refusal are checked before downloading
  (application). The panel spools the download to a private temporary file
  and verifies CDN host allowlist, 256 MiB cap, declared size and SHA-512 over
  the whole file before streaming it to the node with an exact
  Content-Length through the existing `files/content` route; the agent writes
  to a temporary file and renames atomically; the panel checks the size the
  node reports. The node does not recompute the hash (it trusts the panel
  over the mTLS connection); the agent's upload limit bounds the write.
- [x] Remote SFTP (no wire change). Enforcement: identity, the per-operation
  files-permission check (re-validated at most every 5 s, also for open
  handles), the deployment/restore lock and the per-file size cap are
  enforced by the panel application (`internal/sftpd`); path containment is
  enforced by the agent's filesystem layer (`filesystem.Workspace` behind the
  existing agent file routes); identity of the node is mTLS + SQLite. Any bot
  whose node is not the panel's own is served only through its agent
  (`noderoute` `ListDir`, `ReadFileTo`, `WriteFileFrom`, `MakeDir`, `Move`,
  `RemovePath`), refused while the node is offline, and refused when the
  agents module is off; `handler.open` refuses remote bots as a second guard,
  so the panel's disk is never read or written for them (regression tests
  with a decoy directory: `internal/sftpd/remote_test.go`,
  `internal/noderoute/sftp_test.go`, `TestRemoteAgentNode`). Transfers are
  spooled through an unlinked panel temp file; uploads are sent on close in
  one atomic write (`If-Match` on the revision read for partial writes).
  SFTP `remove` and `rmdir` use the non-recursive delete
  (`DELETE …/files?recursive=false`, protocol 7): the emptiness check is the
  node's own `rmdir` system call (kernel level), so a file another session
  adds concurrently is never deleted with the directory (regression test
  `TestRemoteRmdirConcurrentFileSurvives`). Remote listings carry no
  modification times or modes.
- [x] Remote add-on states and logs (agent protocol 6:
  `GET /node/v1/bots/:id/addons`, `GET /node/v1/bots/:id/addons/:kind/logs`).
  Authorization (view-console permission, add-on attached to the bot) is the
  panel application; the agent validates the id, the add-on kind and the
  1–500 line bound and caps the answer at 1 MiB; the panel never asks its own
  runner about a remote bot. The agent workspace route now answers 400 for an
  invalid id.
- [x] Remote-node completion (agent protocol 7, migration `0043`; panel and
  agents must be upgraded together):
  * Isolated AI diagnostics for remote servers run on the node
    (`POST /node/v1/bots/:id/diagnostics`). The panel application keeps
    authorization (edit-files, re-checked after approval), approvals, the
    per-run diagnostic budget and secret redaction of the output; offline
    nodes are refused before anything is sent and the panel never copies its
    own disk or uses its own Docker for a remote server. The agent validates
    the argv against its own runtime catalog (same allowlist, interpreter-eval
    and path rules as the panel), copies a safe snapshot (no symlinks,
    protected/secret paths, `.git`, binaries or files over 1 MiB; 60,000
    files / 512 MiB) into its scratch directory and runs the same locked-down
    container on its Docker (Docker-runtime level: no network, 768 MiB
    memory, 1 CPU, 256 PIDs, 128 MiB tmpfs, configured non-root user;
    10-minute timeout; 1 MiB output). At most two run at once per node
    (application level, 429 beyond). Snapshots and containers are removed
    after each run; leftover snapshots at agent start and orphan diagnostic
    containers by the agent's runner sweep.
  * Node telemetry: `GET /node/v1/telemetry` (CPU, memory, load and the disk
    of the volume holding the node's server files). The panel stores one
    `node_telemetry` sample per connected node each telemetry interval and
    shows the node's disk (not the panel's) in remote servers' live stats.
    Reported by the node, never used for authorization.
  * Free-disk preflight (application level, panel side) before staging on a
    node: restores (archive size), GitHub deploys (size unknown in advance:
    margin only), AI patches and remote template seeding (total size), and
    single-file writes of at least 1 MiB (Modrinth files, SFTP and large
    uploads). Staging is refused with a clear message when the payload plus a
    256 MiB margin does not fit. It is a check, not a reservation: archives
    can expand and concurrent writers can still fill the disk (the node's
    write then fails and the transaction rolls back); a node that reports no
    disk figures is not refused.
  * Webhook pushes for a server whose node is offline are remembered
    (newest push only, `github_repos.pending_push_*`) instead of being
    recorded as failed deployments, and run once (deploying the branch head)
    when the node's agent reconnects. A push whose repository link (name,
    branch, root directory) or auto-deploy setting changed meanwhile is
    dropped and recorded as cancelled; the usual superseded/link-changed/
    moved checks apply while it runs. Pending pushes survive a panel restart.
  * Add-ons chosen at creation are checked for the chosen node before any
    row is written: remote nodes need the panel's agent add-on access (all
    built-in kinds — PostgreSQL, Redis, MongoDB, MariaDB — run on agent
    nodes, which do not need a Docker runner on the panel). Their data lives
    on the node: removing an add-on deletes it there and re-attaching clears
    leftovers there (`DELETE /node/v1/bots/:id/addons/:kind/data`), refused
    while the node is offline; the panel's own add-on data directory is never
    touched for a remote server.
- [x] Remote nodes browser-verified (2026-10-04) in the real panel UI with a
  real `rivet-agent` enrolled through Administration → Nodes on the same
  host's Docker (agent protocol 7): enrollment and connection state, Nodes
  page metrics (CPU, memory, disk, running servers from remote telemetry),
  template bot creation on the node, live stats with NODE DISK, console
  output and input (bot and Paper server), Files (list, edit, upload incl.
  a 2 MiB file, compress, extract, zip upload), package manager, backup and
  restore (with the pre-restore safety copy) and a Paper world backup,
  public GitHub deployment (create, redeploy, Deploy tab), SFTP with
  OpenSSH `sftp` (list, put, get, rename, mkdir, refused non-empty rmdir,
  rm, rmdir), Redis add-on (attach, start, state, log), Paper creation,
  console command, Modrinth install (Chunky loaded by Paper), node offline
  behaviour (Files/Packages/console messages, header marker), reconnect
  (console resumes) and a deferred webhook push deploying once on
  reconnect (pending push simulated in the database, see below). Fixes
  from this pass are in the changelog. Not browser-verified: AI file tools
  and remote `run_diagnostic` (no AI provider configured in the test
  panel); a real GitHub webhook while offline (needs GitHub sign-in to
  obtain the webhook secret; the deferred state was injected into
  `github_repos.pending_push_*` instead); add-on removal/re-attach on the
  node; game server restore; private repositories and publish/push.
- [x] Remote allocation port probing (agent protocol 8,
  `POST /node/v1/ports/probe`; panel and agents must be upgraded together).
  Fixes the browser-test finding that a remote Paper server's automatic
  primary allocation was 25565 while that port was already bound on the
  node. Enforcement boundary: the **agent** bind-tests TCP and UDP on the
  allocation address on its own host (at most 64 ports per request); the
  **panel application** skips ports the node reports busy when it creates
  automatic allocations (and refuses the allocation when the node cannot
  answer), and refuses to start a stopped/failed remote game server whose
  allocated port is busy, naming the port. It is a check, not a
  reservation: a port bound afterwards, or published by Docker through
  firewall rules only (userland proxy off), is not caught, and Docker on the
  node remains the final authority. Pool allocations an administrator
  created are not probed when handed out (same as the local node) but are
  checked at start. An offline node does not block a start (the intent is
  recorded). Capability reports play no part. Tests: noderoute harness
  (`TestRemotePortProbe*`, real bind through hub and agent), api
  (`TestRemoteGamePortProbe`), agenthub (`TestHubRefusesPreviousProtocol`)
  and `TestRemoteAgentNode` against real Docker.
- [x] Docker-published host ports are skipped and port conflicts are not
  crashes (2026-10-04; no schema or protocol change). Enforcement: the
  **panel application** lists the ports every running container on its own
  Docker host publishes (Docker API, so firewall-only publishing is seen)
  and skips them, together with a bind test, for automatic allocations,
  pool allocations being handed out (pool ports are now probed too), Pick a
  free port and administrators' new pool ports (skipped and reported); it
  refuses Start of a stopped/failed local game server onto such a port,
  naming the container. The **agent**'s port probe adds the same Docker
  listing to its bind test (reason text only; protocol stays 8). The
  **runner** (panel and agents) checks the listing before starting any bot
  or server container and classifies Docker's "port is already allocated"
  / "address already in use" start errors as `state_reason =
  port_conflict`: state failed, no automatic retry, crash count untouched,
  no crash alert, until a new generation (Start again, a changed port).
  Still a check, not a reservation: a port taken between the check and the
  start is caught by Docker's error. Tests: runner
  (`TestPortConflictIsNotRetriedOrCountedAsCrash`,
  `TestPortInUseStartErrorIsAConfigurationProblem`, `TestIsPortInUse`), api
  (`TestDockerPublishedPortsAreSkipped`), agentnode
  (`TestPortProbeReportsDockerPublishedPorts`), alerts.
- [x] Crash count ("crashes in a row") fixed (2026-10-04): the runner now
  writes `restart_count` when a container is seen running (zero for a new
  generation), clears it after `StableAfter` (1 minute) of stable running,
  and keeps setup retries (image, install, build, create, start) on a
  separate backoff counter that is never reported as crashes. Test:
  `TestCrashStreakClearsAfterStableRunAndExcludesSetupRetries`.
- [ ] Agents do not self-update, migrate workloads or provide automatic
  capacity scheduling. No privilege-boundary claim is made for Docker
  access.
- [x] Identity batch 1: custom roles and email verification (migration
  `0044`, no agent protocol change).
  - Roles: `roles` table with the seeded system roles `admin` and `user`
    (their permissions are computed by the application, so later permissions
    apply automatically) and admin-defined custom roles (`users.role_id`).
    25 named permissions (`internal/domain/permissions.go`): 13 resource and
    12 administration permissions. **Enforced by the panel application**: API
    route middleware (`requirePerm`) returns 403 on every route a permission
    covers, including the automation API; services re-check administration
    permissions; `BotService` masks the per-bot console/power/files/env bits
    for owners, workspace roles and sharing grants (SFTP uses the same mask).
    Delegated administrators cannot grant permissions they lack, make or
    touch administrators, change their own role, act on (role, address,
    verified flag, enabled state) an account holding administration
    permissions they lack, or invite with the built-in User role unless they
    hold every resource permission. Environment editor,
    modules page and administrator management stay built-in-admin only. Role
    create/update/delete and assignment are audited. Not enforced at the
    container, kernel or network layer; roles do not change isolation.
  - Email verification: `users.email_verified`/`email_verified_at_ms`
    (existing accounts default to verified), `email_verifications` (SHA-256
    of a 256-bit token, single use, 24 h expiry, one outstanding link, one per
    minute in the store plus 5/hour per account and a per-IP confirm limit at
    the HTTP layer). GitHub/Discord sign-ups and password-reset users are
    verified. Self-service email change requires the current password (or a
    recent provider sign-in) and switches the address only when the new
    address confirms; an account manager's change makes it unverified.
    Panel setting `unverified_restrict` lists permissions withheld from
    unverified non-admin accounts (off by default), enforced through the same
    permission checks (403 "verify your email address").
  - UI: Administration → Roles, role assignment, address change and
    verified badges on Users (actions a delegate cannot take are disabled
    with a reason), the unverified policy in Panel settings, `/verify-email`,
    profile email section and a banner while permissions are withheld.
    Browser-verified on a local panel (2026-10-04): role creation, assignment
    when adding a user and through Change role, a delegated Support role
    seeing only Users/Roles and getting 403 from Nodes, the unverified banner
    and profile notice listing withheld permissions, link confirmation
    (Mailgun not configured; the token was injected into SQLite) and a
    second use refused, a delegate's actions on a broader Node operators
    account disabled in the menu and refused (403) by the server, address
    change and mark-verified on a plain user, and the audit records. Role
    deletion and Mailgun delivery were covered by tests only.
  - Not done: account invitations can only carry a built-in role (the
    `account_invites.role` CHECK constraint); verification links need Mailgun
    and a panel address; the CLI `create-admin` account starts unverified
    (administrators are never restricted). OIDC, passkeys and scoped API
    clients followed in identity batch 2 (below).
- [x] Identity batch 2: API clients, OpenID Connect sign-in and passkeys
  (migrations `0045`-`0047`, schema assertion 48, no agent protocol change).
  New dependencies: `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`,
  `github.com/go-webauthn/webauthn` (with `fxamacker/cbor` 2.9.3 → 2.9.4).
  - API clients (`api_clients`, `rvc_` tokens, SHA-256 at rest): permissions
    are a subset of the creator's at creation and are intersected with the
    creator's current role on every request (`domain.User.Client`, `Can`);
    a client never counts as an administrator. Bot/workspace scope is
    enforced by the application in `requireAuth`/`clientGuard` (403) and
    again in `BotService.loadPerm`, `List`, `workspaceRole`,
    `creatableWorkspace` and `ListWorkspaces`. Session-only routes
    (credentials, sessions, sign-in methods, profile, invitations) are
    refused to clients. Expiry, revocation (owner; account managers under
    `manageable`), last use, 600 req/min per client, audit of create/revoke,
    client actions and refusals. Tests: `internal/api/apiclients_test.go`.
  - OIDC (`oidc_providers`, `oidc_identities`): admin CRUD
    (`settings.manage`), discovery on save, secret sealed (namespace
    `oidc:<id>`, included in reseal/key rotation), PKCE S256 + state cookie
    binder + nonce, go-oidc verification of signature/iss/aud/exp. Linking by
    email only on `email_verified` and never to accounts with administration
    permissions; otherwise login-then-link from Settings. Optional sign-up
    with a default role (not admin; delegated managers limited to roles they
    hold). TOTP still required after provider sign-in. Tests against an
    in-process fake issuer: `internal/api/oidc_test.go` (happy path, bad
    signature, wrong aud/iss, expired, nonce and state mismatch, unverified
    email, admin email, link, duplicate link, last-method unlink refusal).
  - Passkeys (`webauthn_credentials`): registration with password re-auth,
    list/rename/delete (last sign-in method refused), discoverable
    passwordless sign-in with user verification (no TOTP after it), passkey
    as the TOTP alternative bound to the MFA ticket's account, clone
    (counter) detection. RP ID = panel host name; unavailable for IP
    addresses. Tests with a software authenticator:
    `internal/api/passkeys_test.go`.
  - Browser-verified on a scratch local panel (2026-10-04): API client
    creation dialog and one-time token, curl checks (200 within permissions,
    403 for an uncarried permission, a session-only route and an admin-only
    page, 401 after revoking from Administration → API clients), adding an
    OIDC provider against a local fake issuer, `link_required` for an
    unverified address of an existing account, SSO sign-up and the linked
    identity in Connected accounts, audit records. Passkeys: the sign-in
    button, the Security section and the registration request reach the
    browser prompt; the browser pane has no authenticator, so the ceremony
    itself is covered by the Go tests only.
  - Not done: OIDC group/claim → role mapping, RP-initiated logout, and
    reading the provider's MFA (`amr`); GitHub/Discord "Disconnect" does not
    count OIDC identities or passkeys as other sign-in methods (it is only
    stricter); passkeys are not offered for SFTP; API clients cannot use the
    console WebSocket from browsers (no header support there) but can from
    other clients.
- [x] Support batch 1: in-panel notifications and support tickets
  (migrations `0048`-`0049`, schema assertion 50, no agent protocol change).
  Guide: `docs/support.md`.
  - Notifications (`notifications`, `notification_prefs`): produced by the
    existing paths (`AlertService.Notify` for crash/heartbeat/deploy/backup,
    `BotService` sharing/transfer/workspace/invitation hooks, agent hub
    `OnDisconnect`/`OnConnect` with a 2-minute grace for nodes, announcements,
    tickets). Enforced by the application: every inbox query is keyed by the
    signed-in account; inbox and preferences are session-only
    (`clientSessionOnly`) and refused in the service for API clients. Email
    only to verified addresses, per-category switch, alert categories also
    need the profile's Alert emails switch. Retention: 200 per account
    (trimmed in the insert transaction), 90 days (hourly prune). Live update
    is polling (30 s, visibility-gated), not a push stream.
  - Tickets (`support_tickets`, `support_ticket_messages`): permissions
    `tickets.create` (resource), `tickets.view_all`, `tickets.manage`
    (administration). Requester-only visibility (no workspace sharing),
    internal notes and assignment events filtered out of requester views by
    the store query and DTO, delegated staff limited by `manageable`, unseen
    tickets answer 404, scoped API clients refused, linked bot checked with
    `BotService.Get` at creation. Per-account rate limits (10 tickets/h, 60
    replies/10 min) and caps (10 active, 500 messages, 10,000 characters).
    Audited as `support.*` (denied attempts kept). No attachments.
  - Tests: `internal/api/support_test.go` (ticket authorization incl.
    internal notes, delegated/view-only staff, assignment, audit; API client
    scope; inbox isolation; preferences; fan-out incl. node grace;
    retention; email gating against a fake Mailgun).
  - Browser-verified on a scratch local panel (2026-10-04, no Docker):
    opening a ticket linked to a bot, staff reply and internal note (note not
    shown to the requester, staff shown as "Support team"), bell badge and
    dropdown, mark all read, Settings → Notifications toggle persisted,
    in-panel-only announcement without Mailgun, staff queue, assignment and
    status change with events and requester notification, `/notifications`
    page, audit rows. Email delivery and node notices were covered by tests
    only.
  - Not done: attachments, a push (SSE/WebSocket) channel for the bell,
    Discord fan-out of the new categories (only the existing bot alerts post
    to Discord). Knowledgebase and status page: support batch 2 below.
- [x] Support batch 2: knowledgebase (help center) and public status page
  (migrations `0050`-`0051`, schema assertion 52, tables assertion 44, no
  agent protocol change, no new dependency). Guide: `docs/support.md`.
  - Knowledgebase (`kb_categories`, `kb_articles`): permission `kb.manage`
    (administration; built-in administrators, delegable). Enforced by the
    application in `service.KBService`: drafts only for `kb.manage`;
    published `staff` articles only for `kb.manage`, `tickets.view_all`,
    `tickets.manage`; `users` articles for every signed-in account; `public`
    articles also for anonymous visitors **only** while the
    `kb_public` panel setting is on (otherwise anonymous requests answer
    401). Articles a reader may not see answer 404 and are left out of lists
    and search. Scoped API clients are refused (`kb` is outside their
    areas). Markdown safety: the server refuses control characters and any
    link target that is not `http(s)://` or a same-site path (no
    `javascript:`, `data:`, `//host`, backslash or quote tricks) outside code;
    bodies are stored as text and served only inside JSON; the interface
    renders them with `SafeMarkdown.svelte`, which builds Svelte text nodes
    (no `{@html}` anywhere in the web app), so raw HTML is displayed
    literally. Search is a bounded `LIKE` prefilter (200 rows, 8 terms) with
    scoring in Go, not a full-text index. Related articles on the new-ticket
    form use the same search (common words dropped). Public reads are
    rate-limited to 120 requests/minute per client IP. Audited as `kb.*`
    (denied attempts kept); article bodies are not recorded.
  - Status page (`status_components`, `status_incidents`,
    `status_incident_components`, `status_incident_updates`,
    `status_samples`): permission `status.manage` (administration). The
    public page `/status` and `GET /api/v1/status` answer 404 unless enabled
    and are rate-limited to 60 requests/minute per client IP. They expose
    only the configured title/intro, each selected component's opaque
    status-page id, administrator-chosen name and description, state, daily
    bars and uptime, and incident titles, impacts, statuses, windows and
    update text — never node/bot ids or real names, owners, addresses or
    error messages (asserted by `TestStatusPagePublicExposure`). Component
    states are derived by the application from data it already has: panel =
    operational while it answers; local node = operational; agent node =
    hub connection (`noderoute.Router.Online`), disabled = major outage, no
    router = unknown; bot = observed state (+ failing health probe =
    degraded; bot on a disconnected node = unknown). Open incidents raise
    the state to their impact; maintenance marked in progress or inside its
    window shows as maintenance. Delegated managers can only add nodes with
    `nodes.manage` and bots they can open. Samples every 5 minutes while the
    page is enabled, counted per component and UTC day, 90 days kept (pruned
    each run). Panel downtime is not observed by the panel itself: days or
    hours without samples are "no data", not downtime. Audited as `status.*`.
  - Tests: `internal/api/kb_test.go` (visibility for anonymous / user /
    staff / manager, public toggle, drafts, search, suggestions, category
    deletion, audit; unsafe links and control characters refused, raw HTML
    only JSON-escaped), `internal/api/statuspage_test.go` (public JSON leaks
    nothing unselected, delegated sources, incident and maintenance
    lifecycle, sampling with uptime math and 90-day retention, rate limit).
  - Browser-verified on a scratch local panel (2026-10-04, no Docker):
    anonymous `/help` asks to sign in and `/status` says there is no page;
    category and article created in Administration → Knowledgebase; a
    `javascript:` link refused on save; an article with `<script>` and
    `<img onerror>` renders them as text (no script or img element, nothing
    executed); relative and https links rendered; public help center toggle;
    status page enabled with the panel and local node under public names; an
    incident shown as partial outage, then resolved with a timeline; related
    article suggested while typing a ticket subject; signed out, `/help`,
    the article and `/status` work anonymously; the public JSON contains no
    node id or account; the 61st request in a minute answers 429.
  - Not done (deferred): status subscribers/notifications, incident
    notifications through `NotificationService`, sites as status components,
    external (third-party) uptime checks, article attachments/images,
    revision history, full-text index, per-workspace knowledgebases.

- [x] Usage analytics (migration `0052`, schema assertion 53, tables
  assertion 47, no agent protocol change, no new dependency). Guide:
  `docs/features.md` ("Usage analytics").
  - Data reused: Docker stats (local) and agent stats (connected remote
    nodes) through the existing `StatsStream` sources, bot rows
    (`observed_state`, `desired_state`, `restart_count`,
    `last_started_at_ms`), `operations` + `bot_backups` (deployment and
    backup outcomes, duration, size), `node_telemetry` (nodes), `users`,
    `support_tickets`/`support_ticket_messages`. New storage only for what
    was not kept: per-bot resource history (`bot_usage`), node rollups that
    outlive raw telemetry (`node_usage`), rollup marks (`usage_marks`).
  - Collector (`service.UsageService`, started in `cmd/rivetpanel/main.go`):
    one pass a minute, at most 100 running bots measured per pass (rotating,
    4 concurrent, 5 s deadline each), workspace size every 30 minutes for
    local bots only (10 per pass). Raw passes stay in memory; one
    transaction of 5-minute rows per bucket (the open bucket is written on
    shutdown). Rollups every 5 minutes recompute the current and previous
    hour (and their days) with `INSERT … SELECT … ON CONFLICT DO UPDATE`
    (idempotent); deployment/backup counts are recomputed from operations
    finished in those hours and stay after the operations are pruned.
    Retention: 5-minute 3 days, hourly 35 days, daily 400 days (bots and
    nodes), pruned hourly in batches of 1,000.
  - API: `GET /api/v1/bots/:id/usage?range=1h|24h|7d|30d|90d[&format=csv]`
    (enforced by `BotService.Authorize` with the console bit, so sharing
    grants, role masks and API-client scopes apply);
    `GET /api/v1/admin/analytics?range=24h|7d|30d|90d[&format=csv]`
    (`requirePerm(analytics.view)` plus a service re-check). Permission
    `analytics.view` (administration group, delegable). Enforced by the
    panel application: non-administrators (including every API client)
    count only accounts that `manageable` allows and bots those accounts
    own; nodes need `nodes.manage`, tickets `tickets.view_all` or
    `tickets.manage`, owner addresses `users.view`; scoped API clients are
    refused by `clientGuard`.
  - UI: Analytics tab on bot and server pages (`UsageAnalytics.svelte`),
    Administration → Analytics (`/admin/analytics`), both on the existing
    `TimeChart`/`Sparkline` SVG components (theme tokens, light and dark).
  - Tests: `internal/api/usage_test.go` (exact 5-minute rows from passes
    incl. crash/start/network deltas and the open bucket, idempotent hourly
    and daily rollups, deployment/backup counting, range resolutions, CSV,
    retention per tier incl. node rows and bot deletion, authorization:
    owner/other account 404, share without console 403, overview 403
    without permission, delegated scope without administrator data, node and
    ticket sections by permission, administrator's API client treated as
    delegated, scoped clients refused).
  - Limits (not enforced or not measured): bots beyond 100 running are
    measured in rotation, not every minute; remote workspaces' disk size is
    not measured; several starts within one minute count once; a panel that
    is not running records nothing (gaps, not downtime); no per-user/billing
    or revenue analytics; operations pruned before a rollup counted
    them (more than 200 finished operations of one bot within five minutes,
    or older than the 200 kept per bot when analytics first start) are not
    counted.

- [x] Daily log files and log archive job (no migration; settings in
  `panel_settings`, keys `logs.*` and `metrics.*`). Enforced by the
  application: `internal/logarchive` writes the panel log (a buffered
  `io.Writer` beside stderr) and every server's console output (captured every
  `capture_minutes` with a per-server cursor through Docker's since/until
  locally and the existing agent console stream remotely; no agent protocol
  change) to `logs/<scope>/<day>.log`, and `LogArchiveService.Run` gzips ended
  days into `log-archive/<scope>/<day>.log.gz` (seal by rename, rebuild with
  a named gzip member, fsync, rename, remove; idempotent and catches up after
  restarts), then applies age and size retention (the size cap counts live
  day files too). Live files are bounded by the application: a per-scope
  per-day cap (`logs.max_day_mb`, default 256 MB; whole lines up to the cap,
  then one `[rivetpanel] log capped:` marker line, the rest of the day
  dropped and the day reported as `capped`), and no log line is written and
  the console capture is skipped while the log filesystem has less free space
  than `RIVET_MIN_FREE_DISK_BYTES`. Days are defined by the
  configured archive time and IANA time zone. API: `GET/PUT
  /api/v1/admin/log-archive`, `POST /api/v1/admin/log-archive/run`
  (`settings.manage`, audited; `GET` also with `system.view`, read-only),
  `GET /api/v1/admin/logs/days[/:date]`
  (`system.view`), `GET /api/v1/bots/:id/logs/days[/:date]` (console
  access). Graph retention (telemetry hours, analytics 5m/1h/1d days, status
  days) is configurable within bounds and read live by the samplers and
  pruners. Tests: `internal/logarchive/store_test.go` (boundary, archive time
  and time zone, gzip integrity, catch-up, interrupted-pass idempotency, late
  lines, age/size retention, capture cursor and quiet followed streams, panel
  sink, day cap and marker across restarts, low-disk refusal, live bytes in
  the size cap), `internal/api/logarchive_test.go` (settings validation/permission/
  persistence, run now, bot day authorization and downloads).
  UI: Administration → Logs and retention (`web/src/routes/admin/logs`;
  form with client-side bound checks that mirror the server's, usage, last
  run, Run archival now, panel log days) and Console → Log history on every
  server (`LogDays.svelte`); the UI only hides what the server refuses.
  Not done / not enforced: add-on container logs,
  build output (bounded per operation) and the agent's own log are not
  archived by day; output Docker rotated away or a container recreated
  between captures is lost; logs are not part of `rivetpanel backup`.
- [x] AI provider history fix: every message is sent with `content`, the
  history is normalized before each request (matched tool results, synthesized
  failures for missing ones, ids, no empty turns), read-only tools time out
  after 90 s (`AIService.ToolTimeout`), open tool calls are closed when a run
  ends and on startup, and the pre-rename deploy manifest is honoured once,
  removed and protected from the AI. Tests: `internal/ai/provider_test.go`
  (strict and OpenAI wire rules), `internal/api/ai_history_test.go`,
  `internal/filesystem` legacy manifest test.
- [x] Interface clean-up (frontend only, no API or schema change): overview
  header with one New action, capacity numbers and server/bot tables; new
  `/bots` list page; sidebar with a single Help & resources menu; Ask AI
  only from the top bar; warm-grey dark tokens, neutral light tokens, accent
  limited to primary actions, active indicators, focus and warnings; bot and
  server pages with power controls in the header, a width-aware tab bar
  ("More" menu) and a console that fills the window; aligned wizard footers.
  Visual only: no access-control or enforcement boundary changed.

## Completed in 0.4.0, 0.3.0 and since 0.2.0

- [x] Hosting larger open-source bots (Unreleased), verified on a real panel
  with Docker 29.7.2: Red-DiscordBot (built from the public repository,
  connected to Discord) and YAGPDB (built from `cmd/yagpdb` with PostgreSQL and
  Redis add-ons, connected to both databases).
  - Public GitHub repositories deploy without a GitHub connection (anonymous
    API, 60 requests/hour per address); auto-deploy polls branches without a
    webhook every five minutes; new GitHub bots can start after their first
    deployment. Enforcement: application level.
  - Repository analysis (verified recipes, file detection, optional AI
    refinement). Suggestions only prefill the creation form and are validated
    again on create; enforcement of commands, add-ons, variables and resources
    is application level, as for any manual input.
  - Per-bot custom build commands (migration `0036`). They run in the existing
    build container: isolation is Docker-runtime level, unchanged.
  - Add-ons (PostgreSQL, Redis, MongoDB, MariaDB; migration `0036`).
    Enforcement: container limits and hardening are Docker-runtime level; the
    no-internet, bot-only reachability is Docker network level (`Internal`
    bridge per bot, verified); memory budgets are application level.
    Not provided: add-on backups, version choice, Lavalink.
- [x] Custom bot and site logos, Discord avatar lookup with the bot's token,
  and automatic site favicons (Unreleased; migration `0037`). Access control is
  application level; images are validated (PNG/JPEG, size and dimensions) and
  served sandboxed with `nosniff`.

- [x] Email through Mailgun (Unreleased): password reset by emailed link,
  invitation emails, bot alerts by email, and security notices; configured in
  Panel settings (key sealed) or `RIVET_MAILGUN_*`. Enforcement: the sealing,
  the always-identical reset answer, one-use hashed expiring links, the
  sign-out of every session on reset and the sending caps (20 per recipient,
  300 per hour) are application level; delivery and reputation are Mailgun's.
  No queue, pool or background goroutine, so idle memory is unchanged
  (`footprint.md`). Limits: no Mailgun webhooks (bounces and complaints are not
  tracked), no retries, one sender, plain text plus simple HTML, no templates
  or attachments. Announcements (Administration → Announcements): administrators
  email sanitized HTML as a notice or opt-out news to all accounts or admins
  (batched, recipients hidden from each other, test-to-self required first, one
  at a time, at most 1,000 recipients); enforcement is application level. No
  drafts, history, scheduling, tracking or per-person targeting. Also fixed: `SetPassword` with no session to keep now really
  signs the account out everywhere (it used a SQL `!=` against NULL).

- [x] Host monitoring and panel logs (0.4.0): richer host samples (load,
  swap, network, disk throughput) with bucketed 1 h to 30 d history, a live
  host/process/Docker/storage snapshot, per-bot resource use, an in-memory
  panel log viewer with redaction, and an attention list. Enforcement:
  read-only, administrator-only, application level. Limits: the log buffer
  holds the newest 2,000 lines and is empty after a restart (full history stays
  in journald/Docker); directory sizes are counted in the background every five
  minutes and bounded (20 s, 3 M files); per-bot live numbers measure at most
  60 running bots per refresh; network and disk throughput are host totals over
  physical interfaces and whole block devices only.
- [x] Environment variables editable from the panel (0.4.0). Overrides are
  stored in the database (secrets sealed) and layered over the process
  environment at start; they apply after a restart and never write the
  environment file. Enforcement: application level. Variables read before the
  database opens, paths the command-line tools share, the Docker endpoint,
  container user, root-container switch, container network and proxy header
  stay environment-file-only; sign-in providers stay on Panel settings. A saved
  set that no longer validates is ignored at start. "Restart panel" exits with
  code 75 and relies on systemd or a container restart policy, which RivetPanel
  cannot verify.
- [x] Go to palette indexes pages, administration sections, environment
  variables, docs, bots (with sections), sites, people, AI chats and actions
  (0.4.0). Client-side only; results follow what the API returns to the
  signed-in account.
- [x] AI settings layout and optional-field markers (0.4.0).

- [x] Team workspaces with owner/admin/developer/viewer roles, a personal
  workspace per account, moving bots between workspaces, and a sidebar
  switcher. Enforcement: application level, on every bot, site, operation,
  activity, SFTP and automation request (`docs/workspaces.md`).
- [x] Administrator oversight of every workspace and account: members, bots
  with owners and state, sites, and the latest 40 operations (deployments,
  builds, backups, restores, GitHub pushes).
- [x] GitHub publishing: create a repository from a bot's files and link it,
  or push the current files to the linked branch/folder through the Git Data
  API with the acting user's own token; `.gitignore`-aware, `.env` excluded,
  changed files only, never forced, recorded as `publish` operations.
- [x] Static site hosting on a separate listener with ZIP and GitHub
  publishing, five immutable releases and rollback, SPA/clean-URL options and
  custom 404 pages (`docs/sites.md`).
- [x] Site templates: five built-in static starters (landing, docs and blog,
  portfolio, coming soon, game server community with a live status section),
  `GET /api/v1/site-templates`, a sandboxed preview and `template_id` on
  `POST /api/v1/sites`, which publishes the files as the first release through
  the normal release path (`sites.create`, size limits, audit). No migration.
  Enforcement: application level; previews are served with a `sandbox` CSP
  (opaque origin, no scripts) and template files run only on the Sites
  origin. Admin-authored site templates are **not** implemented. The public
  status JSON now allows cross-origin reads (public data only).
- [x] Game server join address: a Connect block on Manage, in the header
  strip and the servers list, resolved from allocation alias/IP, the node's
  public address (existing Administration → Nodes field, local node
  included) or the panel host, with loopback/LAN warnings;
  `GET /api/v1/game-hosts`. Enforcement: display only; reachability still
  depends on the host's firewall and router port forwarding.
- [x] Server page: Manage merges the former Overview tab (console, status,
  activity, log history; side column with connect, source/server type,
  backups, details); `?tab=overview` redirects; "Add-ons" renamed
  "Databases" in the UI (API unchanged).
- [x] Unified Templates page (`/templates`): bot templates, game server types
  (built-in and imported eggs) and site templates with sandboxed previews,
  one search, Create bot/server/site actions, egg import for administrators,
  "Start from" on Sites → New site, New menu and Go to entries. Enforcement:
  the UI only hides what the role or modules would refuse; the server
  enforces `bots.create`, `sites.create`, `blueprints.manage` and module
  gates. Preview iframes use `sandbox="allow-same-origin"` (no scripts,
  forms, popups or top navigation) and the server's `sandbox` CSP keeps the
  document on an opaque origin.
- [x] Integrated Bot Sites: one public site per Discord bot, an in-bot Page
  Studio, generated public pages with custom HTML/CSS, explicit safe-widget
  publication, and editable private site drafts that publish as immutable
  releases. Enforcement: author code runs only on the separate Sites origin;
  public page queries omit private analytics and operator/account data.
- [x] Editable site addresses and several sites domains: a slug is unique per
  sites domain; developers change a site's slug and sites domain in its
  settings; administrators add sites domains at runtime (served only after a
  `_rivetpanel-domain` TXT record is verified; environment-file domains are
  trusted), choose the primary, turn domains off, move every site between
  domains (all or nothing) and remove unused ones. Enforcement: application
  level, in the sites listener's host routing; the panel's host and its
  parents/subdomains, overlapping sites domains, and custom domains under a
  sites domain are refused. Routing and certificates per sites domain are
  **not** managed by RivetPanel: the reverse proxy needs a catch-all route and
  a certificate for each domain (`docs/sites.md`).
- [x] Custom domains for sites with DNS TXT ownership verification and
  six-hourly re-checks. Enforcement: the sites listener serves a custom domain
  only after verification (application level). TLS is **not** terminated by
  RivetPanel: certificates are issued by the reverse proxy; RivetPanel only answers
  its on-demand permission check.
- [x] Settings redesign (side-by-side sections, sticky navigation, folded
  password form), themed checkboxes/radios, accent-aware dark glows, and a
  corner-style preference including a fully square mode.
- [~] Target-scoped AI operator with encrypted OpenAI-compatible providers,
  private 90-day conversations, streamed tool records, Approval/Auto envelopes,
  Risa/SearxNG research, secret redaction, revision-checked journaled changes
  and undo, and direct-argv offline diagnostics. Enforcement: RBAC, approvals,
  limits, protected paths and SSRF checks are application-level; resource,
  mount, capability and network isolation are Docker-runtime-level. AI file
  tools route through `rivet-agent` for remote bots (protocol 5); isolated
  diagnostics run on the remote server's node with the same sandbox
  (protocol 7, `docs/ai-operator.md`).
- [x] AI operator follow-ups: every run limit (rounds, wall time, diagnostics,
  apply attempts, lifecycle actions, changed files/bytes, retained output) is
  enforced per run in both modes; model-initiated file applies, diagnostics,
  restarts and secure environment input are audited in Auto mode too;
  research checks addresses at connect time (DNS rebinding and redirects,
  `100.64.0.0/10` blocked) while allowing a private self-hosted search origin;
  `GET /api/v1/ai/conversations/:id/runs` restores runs, approvals,
  secure-input cards, change sets and Undo after a reload; tool-call rows use
  panel UUIDs; finished run streams are dropped after five minutes; providers
  are fully editable in Administration and the first key can be set in `/setup`.
  Startup, deploy and site-publish tools were **not** implemented; the
  operator has no such tools and the documentation no longer claims them.

- [x] AI assistant as one panel-wide chat instead of a per-bot/per-site
  workspace: the Ask AI window follows the person across pages and every
  message carries its context (bot or site id authorized and named by the
  server, section, open file). Runs store their own target; `list_targets` /
  `focus_target` let a target-less run open one of the person's bots in
  Approval mode, and Auto repair requires a target. New tools `read_logs` and
  `build_output` give it the crash and build output. Enforcement: target
  authorization, per-run target pinning and run limits are application-level.
- [x] Diagnostics fixed: the runner's reconciler used to treat an AI
  diagnostic container (labelled with the bot id) as a stale duplicate of the
  bot and stop it (exit 137, no logs); diagnostic containers are now excluded
  from reconciliation and orphans are swept on resync. A relative data
  directory no longer breaks the scratch bind mount. Runtime allowlists now
  include version/syntax checks and running the entry file, and a refusal lists
  what is allowed. Enforcement: policy is application-level; isolation is
  Docker-runtime-level.

Migrations added since 0.2.0 (the schema-version assertion in
`internal/store/sqlite/db_test.go` checks 43 applied migrations):

| Migration | Purpose |
| --- | --- |
| `0000_rivetpanel_identity.sql` | Clean-break product/schema-family marker applied before the existing schema; unmarked non-empty databases are refused before migration |
| `0024_workspaces.sql` | Workspaces and members; personal workspace backfill; `bots.workspace_id` |
| `0025_publish_operations.sql` | Rebuilds `operations` to allow the `publish` kind (rows preserved) |
| `0026_static_sites.sql` | Sites, releases and custom domains |
| `0027_bot_sites.sql` | One-to-one bot/site links, generated-page presentation, custom HTML/CSS and public-widget opt-in |
| `0028_ai_operator.sql` | Encrypted provider profiles, target chats, runs, tools, change/undo snapshots and site-aware audit identity |
| `0029_ai_tool_call_ids.sql` | `ai_tool_calls.provider_call_id`; rows are keyed by panel UUIDs (existing rows keep their id) |
| `0031_env_overrides.sql` | Environment overrides saved from the administration page (secrets sealed under namespace `env`; included in `verify`, `reseal` and `WalkSealed`) |
| `0032_host_telemetry.sql` | `node_telemetry` gains `load1`, swap, network and disk throughput columns (existing rows read as zero) |
| `0033_mail.sql` | `password_resets` (hashed one-use reset links, one per account) and `users.email_alerts` (alert-email switch, default on) |
| `0034_mail_news.sql` | `users.email_news` (optional news-email switch, default on) |
| `0035_site_base_domains.sql` | `site_base_domains`; rebuilds `sites` with `domain_id` and a per-domain unique address (`domain_id`, `slug`) instead of a panel-wide unique slug. Runs with foreign keys off (new `-- rivetpanel:foreign-keys-off` migration marker, checked with `PRAGMA foreign_key_check`) so releases, custom domains and assistant chats are kept; start-up assigns existing sites to the primary domain |
| `0036_build_command_addons.sql` | `bots.build_command` (custom build script, at most 4 KiB) and `bot_addons` (kind and memory per bot; passwords are sealed `RIVET_ADDON_<KIND>_PASSWORD` rows in `bot_env_vars`) |
| `0037_logos.sql` | `bots` and `sites` gain `logo`, `logo_type` and `logo_updated_at_ms` for custom logos (PNG/JPEG, at most 256 KiB) |
| `0038_agent_foundation.sql` | Locations and node placement identity; outbound `agent` transport; last reported agent protocol/certificate/capability state; durable idempotent agent commands with delivery, terminal acknowledgement and deadline state. Persistence only: enrollment, mTLS transport and remote enforcement are not yet implemented |
| `0039_agent_enrollment.sql` | Single-use hashed enrollment credentials bound one-to-one to pre-created agent nodes; used/revoked/expiry state. The private certificate authority is file-backed under `RIVET_KEY_DIR/agent-ca`, not stored in SQLite |
| `0040_game_servers.sql` | Additive: `blueprints` and immutable `blueprint_revisions`; `bots.kind` (`bot`/`game`), pinned blueprint revision, image choice and install state; `allocations` (node IP:port pool with server assignment and one primary per server). No table rebuild; existing bots become kind `bot` |
| `0041_schedule_tasks.sql` | Additive: `schedules.chain` flag and `schedule_tasks` (ordered steps with waits and continue-on-failure); `bots.installed_version`. Chain schedules keep a never-executed placeholder in `schedules.action` because that column's constraint predates chains |
| `0042_agent_connections.sql` | Additive: issued agent certificate inventory and revocation metadata; node drain/public address; reported agent version and hostname |
| `0043_pending_pushes.sql` | Additive: `github_repos.pending_push_sha`, `pending_push_link` and `pending_push_at_ms` (a webhook push waiting for its server's offline node to reconnect) |
| `0044_roles_email_verification.sql` | Additive: `roles` (system `admin`/`user` rows seeded, custom roles with a JSON permission list), `users.role_id`, `users.email_verified` (existing accounts default to 1) and `email_verified_at_ms`, `email_verifications` (hashed single-use links, one per account) |
| `0045_api_clients.sql` | Additive: `api_clients` (hashed `rvc_` tokens, JSON permission list, optional bot/workspace id lists, optional expiry, last use) |
| `0046_oidc.sql` | Additive: `oidc_providers` (issuer, client id, sealed secret, scopes, sign-up/link options, default role), `oidc_identities` (provider + subject → account) |
| `0047_passkeys.sql` | Additive: `webauthn_credentials` (credential id, name, go-webauthn credential record JSON, last use) |
| `0048_notifications.sql` | Additive: `notifications` (per-account plain-text rows with optional same-origin link and read time), `notification_prefs` (per account and category: in panel, email) |
| `0049_support_tickets.sql` | Additive: `support_tickets` (number, requester, subject, category, priority, status, optional bot link and name, assignee), `support_ticket_messages` (messages and events; `internal` only for staff) |
| `0050_knowledgebase.sql` | Additive: `kb_categories` (slug, name, description, position), `kb_articles` (category, slug, title, summary, Markdown body as text, draft/published, public/users/staff visibility, position, author and editor label, times) |
| `0051_status_page.sql` | Additive: `status_components` (panel/node/bot source, public name and description), `status_incidents` (incident or maintenance, impact, status, window), `status_incident_components`, `status_incident_updates` (timeline), `status_samples` (per component and UTC day counts) |
| `0052_usage_analytics.sql` | Additive: `bot_usage` (per bot, 5-minute/hourly/daily buckets of sample counts, CPU/memory sums and peaks, network bytes, workspace size, crashes, starts, deployment and backup counts), `node_usage` (hourly/daily rollups of `node_telemetry`), `usage_marks` (rollup watermarks). Retention is applied by the application |
| `0053_game_jvm_args.sql` | Additive: `bots.jvm_args` (validated extra JVM options of a Java game server), `jvm_args_updated_at_ms` (0 = never set) and `jvm_args_generation` (generation when saved, for "Restart to apply") |
| `0030_ai_global_chat.sql` | Rebuilds `ai_conversations` so a chat may have no target; `ai_runs.bot_id`/`site_id`, `ai_messages.context_json` (the conversation subtree is stashed and restored, rows preserved) |

Known gaps in this work:

- [ ] The AI operator has no tools for startup-command changes, deployments or
  site publication; those stay manual.

- [ ] Site release files are not included in `rivetpanel backup`/`restore`.
- [ ] Sites domains: the panel does not write reverse-proxy routes or install
  certificates (no Cloudflare Origin CA or ACME integration); a site lives
  under one sites domain and is not aliased under the others.
- [ ] Sites have no automation-API upload route, redirects/headers files,
  password protection, per-site analytics, bandwidth limits, or server-side
  builds; uploads are not malware-scanned.
- [ ] Workspace-level quotas (limits still apply per bot owner) and ownership
  transfer of a workspace. Account invitations can now be emailed (Mailgun);
  workspace and bot-share invitations are still links only.
- [ ] GitHub publishing does not support Git LFS, submodules, or pushing to
  a repository that has no commits yet.

## Completed in 0.2.0

- [x] In-house, searchable `/docs` page and internal Documentation navigation.
- [x] Widget groups/tabs, 1–3 column spans, minimum height, TTL, stale state,
  unpublish, authenticated deletion, explicit validation, and a 240-widget read
  cap with 48 changes per push.
- [x] Safe renderers for line, area, donut, gauge, heatmap, sparkline, key/value,
  Markdown, image, log, and code widgets.
- [x] Discord bot avatar reporting from ready discord.js/discord.py clients and
  avatar display in Fleet and Favorites, with runtime initials as fallback.
- [x] Authenticated Prometheus `/metrics`, disabled by default and free of
  bot/user/path labels.
- [x] Per-bot TCP/HTTP health probes against published loopback ports, including
  thresholds, startup/readiness grace, persisted state, and a guarded restart
  on an unhealthy transition. Bots only: the **panel application** refuses a
  probe for a game server, never runs a stored one and never evaluates a
  heartbeat rule for one (game server health = process state + game query;
  `TestGameServersHaveNoHeartbeatOrProbe`). Notification choices for both
  kinds moved to Settings → Notifications in the interface; crash alerts
  still cover game servers.

Migrations added in this release:

| Migration | Purpose |
| --- | --- |
| `0021_widget_dashboards.sql` | Widget grouping, layout, expiry, and refresh metadata |
| `0022_bot_discord_identity.sql` | Validated Discord identity and avatar metadata |
| `0023_health_probes.sql` | Health-probe configuration and state |

## Known limitations

- Dependency advisories **GO-2026-4887** (Moby AuthZ plugin bypass with
  oversized request bodies) and **GO-2026-4883** (off-by-one in Moby's plugin
  privilege validation) are reported for `github.com/docker/docker`
  v28.5.2+incompatible, with no fixed version. Both are flaws of the Docker
  **daemon** (its AuthZ middleware and plugin installation), not of the API
  client. The advisories carry no package or symbol list, so `govulncheck`
  marks every use of the module as reachable (its traces are ordinary client
  calls such as `client.NewClientWithOpts` and package `init`s). Checked with
  `go list -deps ./cmd/...`: the panel and `rivet-agent` link only the client
  side (`client`, `api`, `api/types/...`, `errdefs`, `pkg/jsonmessage`,
  `pkg/stdcopy`), none of the daemon, `pkg/authorization` or `plugin`
  packages, and never call the client's plugin endpoints. Not exploitable
  through RivetPanel; the host's Docker daemon must be patched by its own
  updates. The client library is not migrated (to `github.com/moby/moby/client`)
  for now; revisit when a fixed or successor module is adopted.

## Partially completed

- [~] Widget charts have more safe renderer types, but still need true
  multi-series data, axes, legends, time axes, and threshold lines.
- [~] Tables accept richer safe cell values, but still need full sorting,
  alignment, formatting, and pagination.
- [~] Dashboard layout and lifecycle are server-backed, while per-user
  rename/hide/order and per-widget permissions remain browser-local or absent.
- [~] JavaScript and Python SDKs have the richest layout/lifecycle and Discord
  identity support; the remaining language SDKs need full option parity.
- [~] Telemetry already has a WebSocket transport, but browser dashboards still
  poll analytics rather than receiving widget updates in real time.
- [~] Container hardening already includes a non-root user, read-only runtime,
  capability drops, `no-new-privileges`, resource caps, and network disablement.
  Dedicated user namespaces, seccomp/AppArmor policy, and optional gVisor/Kata
  isolation are not yet implemented.
- [x] Workspace ownership default for unprivileged panels: with neither
  `RIVET_CONTAINER_USER` nor `RIVET_WORKSPACE_OWNER` set and a panel (or
  `rivet-agent`) that is neither root nor holds `CAP_CHOWN` (probed with a
  real chown in the data root), containers run as the process's own uid:gid
  instead of the unusable `65532:65532`; a warning is logged and Diagnostics
  shows an informational check, plus a warning listing workspaces owned by
  another user that cannot be repaired. Enforcement: application level
  (selection) and Docker runtime (the container `User`). The fallback is
  still non-root inside the container but shares the panel's host uid; it is
  **not** isolation from the panel process. Root/systemd deployments and
  explicit settings are unchanged. The automatic fallback is limited to
  `RIVET_ENV=development`: a production panel refuses to start (and
  `rivetpanel doctor` reports a failed ownership check) unless
  `RIVET_ALLOW_SHARED_UID=1` is set, in which case Diagnostics shows a
  warning; `rivet-agent serve` refuses likewise unless `--allow-shared-uid`
  / `RIVET_AGENT_ALLOW_SHARED_UID=1` (or `RIVET_ENV=development`). Enforced by
  the application at startup.

## Remaining: security and isolation

- [~] Complete remote feature parity and optionally run the control plane
  without a local Docker runner; `rivet-agent` and its authenticated outbound
  channel are implemented, but the panel still holds Docker when local
  execution is enabled.
- [ ] Enforce per-bot egress allowlists at DNS and firewall/proxy level.
- [ ] Enforce workspace disk quotas and per-bot IO throttling.
- [ ] Enforce bandwidth limits with host traffic shaping such as `tc`/HTB.
- [ ] Add dedicated seccomp/AppArmor profiles and user-namespace remapping.
- [ ] Offer gVisor or Kata as an optional isolation tier for untrusted tenants.
- [ ] Scan uploads for malware and enforce archive expansion/ratio limits.
- [ ] Verify runtime images with cosign and retain/validate their SBOMs.

The egress, disk, IO, and bandwidth settings must not be marked complete until
they are enforced outside configuration storage by the relevant kernel,
network, filesystem, or container-runtime mechanism.

## Remaining: operations and reliability

- [~] Explicit multi-node placement and node drain are implemented; automatic
  capacity scheduling and workload migration are not.
- [ ] PostgreSQL backend plus point-in-time recovery, or an equivalent tested
  SQLite WAL archival and restore design.
- [ ] External bot/panel log shipping to Loki and/or syslog.
- [ ] Maintenance windows, node draining workflow, and signed self-update or
  update notification.
- [ ] Encrypted offsite S3/rclone backups, retention tiers, and scheduled
  verified restore drills.

## Remaining: developer and platform experience

- [ ] Transactional export/import of bot definitions, environment/startup
  versioning and diffs, then CLI and Terraform clients over the automation API.
- [ ] Dependency and license intelligence: npm/pip/Cargo audits, lockfile
  handling, update pull requests, and license checks.
- [~] Custom domains and automatic TLS exist for static sites (TLS through the
  reverse proxy's on-demand issuance). Bots can now run companion database
  containers (add-ons); arbitrary multi-container bots (any image, e.g.
  Lavalink), named volumes, add-on backups and custom domains for bot HTTP
  ports remain.
- [ ] Pull-request previews and health-gated blue/green or canary rollout.
- [ ] A supported panel plugin model for custom pages and jobs (extensions:
  backlog, not scheduled — user decision 2026-10-04).
- [ ] .NET, Deno/Bun, PHP, and opt-in custom Dockerfile runtimes.
- [ ] Signed callback action widgets, dashboard filters, and drill-down.
- [ ] SMTP-backed self-service signup/reset/alert workflows.

## Remaining: governance and compliance

- [ ] Configurable audit retention and export.
- [ ] GDPR data export and deletion workflows.
- [ ] Key-rotation user experience plus optional KMS/HSM integration.
- [ ] License and terms acceptance/versioning.
- [ ] Internationalization of hardcoded English strings.
- [ ] A complete accessibility audit and remediation pass.

## Recommended stage order

1. Botrunner privilege boundary.
2. Egress, disk, IO, and bandwidth enforcement.
3. Log shipping and offsite verified backups.
4. SMTP and account recovery workflows.
5. Config-as-code, CLI, and Terraform foundations.
6. Multi-node scheduling and PostgreSQL/PITR.
7. Governance, internationalization, and accessibility.
