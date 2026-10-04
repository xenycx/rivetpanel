# RivetPanel

Current version: **0.5.0**. See [CHANGELOG.md](CHANGELOG.md) for release notes
and [the implementation status](docs/implementation-status.md) for the staged
platform-overhaul checklist.

A lightweight self-hosted control plane for Discord bots and other isolated
applications, **Minecraft servers**, and static sites, with remote nodes and
other modules being added as opt-in previews. The existing application runtimes cover Node.js, Python, Rust,
Go, Java and Ruby. The control plane uses Go, Fiber, SQLite and an embedded
SvelteKit interface; Docker provides the current execution boundary.

RivetPanel is a clean-break namespace. It does not read or automatically
convert databases created by the predecessor product. Start with a new
`RIVET_DB_PATH`; an unmarked database is refused before it is changed.

* **Minecraft servers** (preview): Paper, Purpur, Vanilla, Fabric, Forge,
  NeoForge, Folia and Velocity from versioned server-type blueprints, with
  version lists, checksum-verified downloads, automatic Java selection
  (provider minimum, Minecraft release and the jar's class files),
  per-server JVM arguments (Aikar's flags and ZGC presets, strictly
  validated), graceful console stops, player counts, port allocations, a Players page,
  a `server.properties` editor, a Modrinth plugin/mod installer, task-chain
  schedules with console commands, and an administrator-managed blueprint
  catalog (`docs/game-servers.md`)
* **Steam game servers** (preview): Valheim, Rust and Project Zomboid
  installed with an anonymous SteamCMD login, Steam A2S player status, and a
  Pterodactyl egg importer that turns eggs into reviewable blueprint drafts
* **Remote nodes** (preview): enroll `rivet-agent` hosts with one-use tokens,
  route lifecycle, console, stats, files, SFTP, packages, AI file tools and
  diagnostics, add-ons, backups, GitHub deployments (pushes during an outage
  run on reconnect), GitHub publish/push and Minecraft queries over outbound
  mTLS, with node telemetry and a free-disk check before staging; choose a
  node during creation, and drain, disable, revoke or delete nodes from
  Administration; panel and agents must be upgraded together (agent protocol
  7, `docs/agents.md`)
* Bot lifecycle (start / stop / restart / delete) driven by persisted desired
  state and a reconciler, safe across crashes and lost Docker responses
* Resource-capped, hardened containers (memory, CPU, PIDs, read-only root,
  no capabilities, non-root); separate build stage per language
* Live stdout/stderr and controlled stdin over WebSocket, reconnect/resume,
  bounded slow-client handling
* Web file manager (CodeMirror), zip upload with safe extraction
* AES-256-GCM encrypted environment variables, masked in every response
* Node telemetry with bounded retention; SQLite-aware backup and restore
* Users with ownership checks, Argon2id, CSRF-protected cookie sessions
* Sign-in with GitHub and Discord (linkable, self-hostable OAuth; `docs/oauth.md`)
* Single sign-on through any OpenID Connect provider, passkeys (passwordless or as the second step), and scoped API clients for the whole API (`docs/auth.md`)
* Optional email through Mailgun: password reset, invitations, bot alerts and security notices (`docs/email.md`) and an admin Announcements tab for HTML news and policy updates; off by default and free at idle
* In-panel notifications (bell, per-category in-panel/email preferences) and support tickets with staff assignment and internal notes (`docs/support.md`)
* Knowledgebase / help center at `/help` (Markdown articles, categories, public / signed-in / staff visibility, optional public access, related articles on new tickets) and a public status page at `/status` (selected components under public names, incidents, maintenance, 90-day uptime bars) (`docs/support.md`)
* One **Templates** page: bot starters (discord.js, discord.py, Poise, JDA,
  DiscordGo and more), every game server type including imported eggs, and
  five static site templates with sandboxed previews; plus GitHub deployments from **any public repository** (no GitHub connection
  needed) with auto-deploy on push or by polling
* **Analyze repository**: detects language, start/build commands, variables,
  databases and resources, with verified recipes for Red-DiscordBot and YAGPDB
  and optional AI refinement
* **Databases** (add-ons): PostgreSQL, Redis, MongoDB and MariaDB per bot on a private,
  internet-less network, with connection variables injected; custom build
  commands for larger projects
* Custom **logos** for bots and sites, Discord avatars fetched with the bot's
  token, site favicons picked up automatically
* Power controls incl. emergency kill, live CPU/RAM/disk gauges, per-bot
  auto-restart policy with backoff, startup/entrypoint editor, network and
  published-port controls
* Team **workspaces** with owner/admin/developer/viewer roles on top of per-bot
  sharing, and administrator views of every workspace, account and deployment
  (`docs/workspaces.md`)
* **Custom account roles** built from named permissions, enforced by the
  server on every route, with delegated administration and audited changes;
  **email verification** with one-use links and an optional restriction for
  unverified accounts (`docs/permissions.md`)
* **Static site hosting** on a separate listener: ZIP or GitHub publishing,
  instant rollback, editable site addresses, several sites domains (added at
  runtime with DNS verification), custom domains with DNS verification,
  on-demand TLS via the reverse proxy (`docs/sites.md`)
* Integrated **Bot Sites** in every Discord bot: a generated public page with
  custom HTML/CSS and opt-in live widgets, or full HTML/CSS/JS files edited in
  a private draft and published as immutable releases
* **Publish bots to GitHub**: create a repository from a bot's files and push
  later changes, `.gitignore`-aware and never force-pushed
* Sub-user sharing with granular permissions; embedded SFTP server with API keys
* Visual package manager (npm, pip, Cargo, Go modules), per-bot backups with
  restore, bot-to-panel analytics API with discord.js / discord.py snippets,
  Discord notifications (`docs/features.md`)

Architecture, deployment, API, and feature guides are collected in [`docs/`](docs/README.md).
Remote placement is explicit and administrator-controlled; there is no
automatic capacity scheduler, live migration, HA, billing or interactive
shell. Several file-backed features remain local-only; see `docs/agents.md`.

## Build and run

```sh
make build      # web UI plus bin/rivetpanel and bin/rivet-agent
make run-dev    # RIVET_ENV=development: data under ./.dev-data, dev key auto-created
make check      # go vet, go test, svelte-check
make integration  # real-Docker tests, needs a DISPOSABLE daemon (see the Makefile)
```

Requires Go >= 1.27 and Node >= 22 to build (module path
`github.com/xenycx/rivetpanel`). Production install: `docs/deployment.md`. First run:

```sh
rivetpanel keygen                          # encryption key and agent CA (kept outside the database)
rivetpanel create-admin you@example.com    # hidden password prompt
rivetpanel                                 # serve on RIVET_LISTEN (default 127.0.0.1:8080)
```

Without `create-admin`, the panel prints a one-time setup code in its log;
open `/setup` in the browser and enter it to create the first administrator.

Other commands: `backup`, `backup-verify`, `restore`, `verify` (`docs/backup.md`).

### Run the published container

```sh
cp deploy/container.env.example .env       # not the systemd example
docker compose run --rm rivetpanel keygen
docker compose up -d
```

The data directory must be a host directory mounted at `/var/lib/rivetpanel`
(bot workspaces are bind-mounted from it by the host's Docker). See
[the container guide](docs/container.md) before mounting the Docker socket.

## Documentation

Start at the [documentation index](docs/README.md). The project home is
[github.com/xenycx/rivetpanel](https://github.com/xenycx/rivetpanel).

* `docs/architecture.md`: state model, reconciliation, console semantics
* `docs/isolation.md`: what containers get, the evidence, and **what is not provided**
* `docs/backup.md`, `docs/deployment.md`, `docs/footprint.md`
* `docs/features.md`: every panel feature, its limits and what it does not do
* `docs/workspaces.md`: team workspaces, roles and administrator oversight
* `docs/permissions.md`: account roles, the permission catalog, delegated administration and email verification
* `docs/sites.md`: static site hosting, custom domains, DNS and reverse proxy setup
* `docs/agents.md`: remote-node installation, enrollment, placement, certificate lifecycle and limits
* `docs/logs.md`: daily log files, the log archive job (gzip at a configurable time, retention) and how long graph data is kept
* [CHANGELOG.md](CHANGELOG.md): versioned release history
* [docs/implementation-status.md](docs/implementation-status.md): completed and remaining platform-overhaul work
* `docs/oauth.md`: GitHub/Discord sign-in setup, Cloudflare, troubleshooting
* `docs/auth.md`: OpenID Connect single sign-on, passkeys and API clients
* `docs/email.md`: Mailgun setup, what is emailed, security properties and limits
* `docs/support.md`: notifications, preferences, support tickets, the knowledgebase, the status page and their authorization rules

## Validation (2026-09-30, Linux 7.2.5 x86_64, Go 1.27.1, Node 26.8.1)

| Check | Result |
| --- | --- |
| `go vet ./...` (also `-tags integration`), `go test ./...`, `go test -race ./internal/...` | pass |
| `svelte-check` | 0 errors (21 `state_referenced_locally` warnings, all in components keyed by a route param) |
| Real-Docker integration tests (`make integration`), all 15 | pass on a fresh **rootful** Docker 29.7.2 daemon (cgroup v2, systemd driver): all six language runtimes, lifecycle, isolation settings, crash restart, adoption after a panel restart, OOM, build failure, backup/restore cycle, console/stdin/reconnect/slow client, and the new SIGKILL, restart-policy (clean exit / gave up / never), published-port (answered over HTTP), custom-entrypoint, no-network and stats-stream tests |
| **Every template built and run through the panel binary in real containers**, with an invalid token | discord.js (`TokenInvalid`), discord.py (`LoginFailure`), DiscordGo (`4004 Authentication failed`), JDA (Maven downloaded and verified, `InvalidTokenException`), Poise (`Sent invalid authentication`): each reached Discord's login |
| Real browser (Playwright, headless Chromium), 30 checks | pass: OAuth buttons on the login page, template gallery, every bot tab, env reveal/hide/import, package search and add, startup/network/backup saves, telemetry key and SDK snippet, connected accounts, SFTP page and API key, no horizontal scroll at phone width, no unexpected console errors |
| Real OpenSSH `sftp` client against the production binary | list, put, mkdir, rename, get, rm, rmdir work; root mkdir, deleting a bot folder and `../..` traversal are refused; a wrong password is rejected; `ssh host cmd` is refused |
| Idle RSS, panel + in-process runner, default modules | **36.6–38.6 MiB** over three starts (target < 50 MB; 14.4–16.4 MiB anonymous, the rest binary pages); 39.5 MiB with one running bot; peak about 55 MiB during an Argon2id login; `docs/footprint.md` |

**Not verified against the real services** (no credentials or accounts were
available): the live GitHub and Discord OAuth exchanges, GitHub's webhook
delivery and API, and Discord webhook posting. They are tested against fake
provider servers that reproduce the documented request and response shapes
(including GitHub's HTTP 200 error bodies and codeload redirects). The first
real login is the moment to check `docs/oauth.md`.

The Ruby runtime has no template (its smoke test passes). Bandwidth limits are
not enforced (Docker has no native cap).

## Known limitations

* Game servers are Minecraft Java plus three SteamCMD games (anonymous
  downloads only; no Bedrock, mounts, FastDL or subdomains). The Pterodactyl
  egg importer does not map yaml/json/xml config edits. Steam status queries
  work only for servers on the panel's own node. They can run on remote
  nodes; their backups and Modrinth installs (verified at the panel, then
  streamed to the node) work through the agent, and the node's agent checks
  that a port is free before it is allocated or the server starts (a check,
  not a reservation; agent protocol 8).
* **Egress, admission control and disk quotas are not enforced by RivetPanel**;
  see `docs/isolation.md` before hosting untrusted users. The firewall example
  there was not run in the test environment.
* The panel holds the Docker socket (root-equivalent). Its systemd sandbox
  limits blast radius but does not isolate that privilege.
* When the panel is neither root nor holds CAP_CHOWN and no container user is
  configured, bots run as the panel's own uid:gid (non-root in the container,
  but the same host uid that owns the database and keys). Development falls
  back automatically; production refuses to start unless
  `RIVET_ALLOW_SHARED_UID=1` is set. Run it as root via systemd in
  production (`docs/deployment.md`).
* Not exercised: the default non-root container uid (65532, needs CAP_CHOWN; the
  tests ran the container as the test user's own uid on a rootful daemon, or as
  root under the earlier rootless one), other CPU architectures, glibc images,
  and load (many bots / connections).
* Dependency installs need outbound network in the build container; the template
  runs exercised real registries (npm, PyPI, Go proxy, crates.io, Maven Central).
* Builds need real RAM: the Rust recipe reserves 3 GiB for its build container
  (`build_memory_bytes`), because `rustc` is SIGKILLed at 1.5 GiB without swap.
* `npm audit` reports low-severity findings in dev dependencies (not triaged).
* Recorded in `docs/architecture.md`: console log delivery is at-least-once at
  reconnect boundaries, not exactly-once.
* One panel-wide **AI assistant** (the Ask AI button in the top bar of every page) that knows
  which bot, site and section you are viewing: streamed investigation that
  reads console output, build logs and files, approval-gated or bounded
  automatic repair (file changes, offline diagnostics and restarts under
  per-run limits, all audited), encrypted OpenAI-compatible providers
  configurable in `/setup` or Administration, cited web research, and staged
  diffs with undo.
* Administrator tools: a Host page with live and historical CPU, memory, load,
  disk and network charts, per-bot resource use, storage, Docker and process
  details and the panel's own log; daily log files of the panel and every
  server's console, gzipped into an archive folder at a configurable time with
  retention, set and downloaded under Administration → Logs and retention and
  each server's Console → Log history (`docs/logs.md`); an Environment page that edits `RIVET_`
  variables from the browser (applied at the next restart); and a Ctrl+K "Go to"
  palette that finds pages, settings, variables, bots, sites, people, chats and
  actions. It cannot change startup commands, deploy, publish sites or
  push to GitHub.
