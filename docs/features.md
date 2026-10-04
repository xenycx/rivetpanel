# Feature guide

How each panel feature behaves, what it is bound by, and where it stops.
Setup for GitHub/Discord sign-in is in `oauth.md`; the automation API is in
`automation.md`.

## Interface

A single-page app embedded in the binary. Light and dark themes: the header
button (sun/moon/monitor) cycles Light → Dark → Match the system, and the
choice is remembered in the browser. Dark mode uses warm near-black surfaces
with an ember accent; every colour comes from theme tokens, so the terminal and
the code editor follow the theme too. **Settings → Appearance** also offers
twelve accent colours and four corner styles (Square, Subtle, Default,
Rounded); Square removes rounding everywhere, including badges and avatars.
These are per-browser preferences.

- **Fleet** (`/`): search, filters for state, runtime and owner, tags and
  favourites (all kept in the URL), a summary of running / needs attention /
  in progress / stopped bots, batch Start, Stop or Restart of up to 50 bots
  with a review step and a result per bot, and a Ctrl+K "Go to" palette that
  finds pages, administration sections, environment variables, docs, bots (and
  their sections), sites, people, AI chats and actions. Several words narrow
  the search; Tab switches scope; `>` lists actions only.
- **New bot** (`/bots/new`): template, GitHub repository or empty bot; required
  values such as the token up front; review of memory, CPU and build memory;
  administrators can place an empty bot on an enabled, non-draining node.
- **Bot page**: an identity card with shortcuts to the public page studio and
  resource settings, one row of tabs (Manage, Overview, Files, Deploy, Startup,
  Packages, Env, Network, Page, Health, Backups, Schedules, Access, Settings),
  and a status strip with the exact lifecycle state, live CPU, memory, disk and
  network readings, and Start, Restart, Stop and Kill. **Manage** is the
  default: a terminal window with the live console and a line that goes to the
  bot's standard input. A failed bot offers **Ask AI why**. Unsaved edits are
  protected when navigating away; older `?tab=console`, `?tab=ai` and
  `?tab=analytics` links still work.
- **Activity** (`/activity`): work (builds, deployments, backups, restores) and
  changes (who changed what; names only, never values).
- **Workspace switcher** (sidebar): scopes the overview, Sites and new bots to
  one workspace or shows all of them (`workspaces.md`).
- **Public page** (inside each bot): Page Studio for a generated bot site,
  opt-in public widgets, full site files, private insights and a cross-origin
  preview. **Sites** (`/sites`) remains the workspace-wide inventory for sites,
  releases, rollback and custom domains (`sites.md`).
- **Settings**: profile; appearance; workspaces and members; connected
  accounts; Security (password, two-step sign-in, sessions); SFTP keys and
  automation tokens. Each page explains a setting beside its controls on wide
  screens and above them on narrow ones.
- **Administration**: users (with each account's workspaces, bots and recent
  deployments), workspaces, sites and domains (suspend/restore), host
  resources, bots' resource use, capacity and panel logs, panel settings
  (sign-in, registration, the AI assistant), nodes and locations (enrollment,
  connection, drain, certificates), the environment editor, and diagnostics.

## Game servers (preview)

Game servers → New server creates Minecraft Java servers (Paper, Purpur,
Vanilla, Fabric, Forge, NeoForge, Folia, Velocity) and Steam dedicated servers
(Valheim, Rust, Project Zomboid). Steam games are downloaded and updated with
an anonymous SteamCMD login in a separate install container on the first
start or on Reinstall; a "Steam beta branch" setting picks a branch. Steam
servers get the ports they need (for example Valheim's consecutive game and
query ports), stop with the game's own signal or console command so the world
is saved, and show players, name, map and version through the Steam query
while running on the panel's own node. Administrators can also convert a
Pterodactyl egg into a server type under Administration → Server types →
Import Pterodactyl egg: the draft and a list of everything that could not be
mapped are shown for review before anything is saved. Details, limits and the
games' terms are in `docs/game-servers.md`.

## Remote nodes (preview)

`rivet-agent` runs assigned workloads on a separate Docker host and connects
outward to the panel over mutually authenticated TLS. Lifecycle, console,
Files, live container statistics and Minecraft status are routed through the
node connection. Placement is an explicit administrator choice; draining
refuses new servers but does not migrate existing ones. Backups stream from
the assigned node into the panel's backup storage, and restore remains
journaled on that node until the panel confirms the database update. GitHub
deployments (manual, webhook and polled) stream the downloaded tarball to the
assigned node, which stages and validates it before swapping and keeps the
previous files until the panel records the commit; an administrator can pick a
connected node when creating a bot from GitHub. GitHub publish and push read
the selected files from the node. The package manager and the AI assistant's
file tools edit the node's files directly, with revision checks so a
concurrent change is never overwritten (agent protocol 5). An administrator
can create a template bot on a connected node: the template files are written
there in one transaction that is committed only after the bot is recorded,
and a failed creation is removed again through the agent. Modrinth plugins and
mods for a remote game server are verified completely at the panel (CDN
allowlist, 256 MiB limit, SHA-512) in a temporary file and only then streamed
to the node. SFTP works for remote servers through the agent (the panel's disk
is never used for them), and the Add-ons tab shows remote add-on states and
logs (agent protocol 6). The AI assistant's isolated diagnostics run on the
server's node with the same sandbox; add-ons chosen at creation are checked
for the node and their data is managed there; agents report CPU, memory and
disk for node charts and the live stats strip; the panel refuses to stage a
restore, deployment, change or large file on a node without enough free disk;
and a GitHub push that arrives while a node is offline is deployed when it
reconnects (agent protocol 7). Remote allocation probing is still local-only.
Installation,
certificate enforcement and the full boundary are in [agents.md](agents.md).

## Sharing and permissions (RBAC)

The owner (and administrators) can share a bot from **Access**, by email or
with an **invitation link**. Permissions are a bitmask:

| Permission | Allows |
| --- | --- |
| View console | live output, analytics, resource gauges, health |
| Start / stop | start, stop, restart, kill, console input |
| Edit files | file manager, SFTP, packages, deployments, backups (create/download) |
| Manage env vars | list, reveal, set and delete environment variables |
| Full admin | everything above plus startup, settings, telemetry key, GitHub link, alert rules, restore and delete backups |

Only the **owner or an administrator** can share the bot, delete it, transfer
it or publish host ports. A user with no grant gets `404`, indistinguishable
from a bot that does not exist; a user with a grant lacking a permission gets
`403`. Console input needs *Start / stop* and is re-checked on every line.
Console and SFTP sessions lose access within about 5 seconds of a revoked grant,
disabled account or deleted key.

Invitation links are one-use, expire after 1-14 days, carry the permissions
chosen when they were made, and only work for someone signed in to this panel.
The token lives in the URL fragment, which browsers never send to the server.
At most 20 open invitations per bot; they can be revoked.

Ownership transfer removes the GitHub link (it uses the old owner's GitHub
access) and moves the bot into the new owner's personal workspace. The last
administrator cannot be removed or disabled.

**Workspaces** grant access to every bot in them by role (owner, admin,
developer, viewer) on top of these per-bot grants; see
[workspaces.md](workspaces.md). Static websites are covered in
[sites.md](sites.md).

**Account roles** sit above all of this. Every account holds the built-in
Administrator or User role or a custom role made under Administration →
Roles from named permissions (`bots.create`, `bots.files`, `nodes.manage`,
`users.manage` and so on). The server refuses (`403`) anything the role does
not include, for owners too, and the console/power/files/environment
permissions also mask per-bot grants and workspace roles, so a full-access
share cannot give back what the role removes. Custom roles can delegate parts
of the administration area without making someone an administrator; a
delegate can never grant more than it holds or act on an account with
administration permissions it lacks, and only administrators make
administrators or open the environment editor and modules page. Role changes
are audited. See [permissions.md](permissions.md).

## Lifecycle, power controls and resource gauges

Start, Stop, Restart and **Kill** (immediate `SIGKILL`; records stopped intent
first). The API reports one `phase` per bot (starting, building, running,
retrying, failed, exited, stopping, stopped, ...), the restart count, the next
retry time and a reason code. Live CPU, memory and disk gauges relay Docker's
stats stream over Server-Sent Events. Disk is the size of the bot's workspace
(there are no per-bot disk quotas) next to free space on the node.

## Startup, runtime and auto-restart

**Startup** edits the runtime, the start command and an optional entrypoint.
Only programs approved for the runtime (`runtimes/*.yaml`) are accepted, and
the command runs directly, never through a shell.

**Build command** (optional) replaces the runtime's default build step, for
example `cd cmd/mybot && go build -o /workspace/app .` or `npm ci && npm run
build`. It runs as `sh -c` with `set -e` in the runtime's builder container
before every start, with the same resource limits, internet access and **no
bot variables**: the same privileges as the package-manager install scripts
that already run there. At most 4 KiB. The default Python build now also
installs packaged projects (`setup.py`, or `pyproject.toml` with `[project]` or
Poetry metadata) when there is no `requirements.txt`.

Restart policy: `on_failure` (default) restarts after a non-zero exit with
exponential backoff and a limit on consecutive crashes (0 = unlimited); `never`
leaves the bot down. A clean exit is never restarted. When the runner gives up,
the bot shows *failed* with the reason, and **Start** retries with a fresh
crash budget. Backoff resets after a minute of stable running. Builds are
recorded with their output (256 KiB kept per build) and shown live.

## Environment variables

Values are masked in every list. **Reveal** fetches one value on request and
hides it again after 30 seconds. **Import .env** previews names before saving.
Names starting `RIVET_` and `LD_`, and `PATH`, are reserved;
`RIVET_TELEMETRY_KEY` and `RIVET_URL` are set by the panel.

## Network and ports

Outbound access can be turned off (Docker's `none` network). Ports are **not
published by default**. The owner can publish host ports inside
`RIVET_PORT_RANGE` (default 20000-29999), unique across bots, bound to
`127.0.0.1` unless `RIVET_PORT_PUBLIC_BIND=1`.

**Bandwidth limits are recorded but not enforced.** Docker has no built-in
bandwidth cap; enforcing one needs traffic shaping (`tc`) on the host.

## Files

Web file manager with breadcrumbs, filter, upload with progress and cancel,
zip extraction and a CodeMirror editor (loaded on demand). Saves are
conditional on the file's revision: if SFTP, a deployment or another editor
changed the file, the editor offers to download your version, load theirs or
replace it deliberately, and a failed save keeps your text. While a deployment
or restore replaces files, edits wait with a clear 409.

Bot Sites use the same explorer and conflict-aware editor for a private site
draft. Unlike bot files, a save never changes production: **Publish draft**
creates and activates an immutable site release. Generated pages instead use
structured Page Studio settings plus bounded custom HTML and CSS.

## Package manager

Reads and edits `package.json`, `requirements.txt` (or `pyproject.toml`
`[project].dependencies`), `Cargo.toml` and `go.mod`. Search uses npm and
crates.io; PyPI and the Go proxy resolve an exact name. Names and version specs
are matched against per-ecosystem patterns, so nothing can be injected.
Dependencies install when the bot next starts. Java (Maven) and Ruby (Bundler)
manifests are not edited by the panel.

## Templates

Seven starters are embedded in the binary and copied into a new bot:

| Template | Runtime | Notes |
| --- | --- | --- |
| discord.js | Node.js 24 | CommonJS, RivetPanel SDK included |
| discord.js TypeScript | Node.js 24 | `src/index.mts` run directly by Node's built-in type stripping (no build step); `npm run check` type-checks locally |
| discord.py | Python | RivetPanel SDK included |
| Poise (Serenity) | Rust | builds in a 3 GiB build container |
| JDA | Java | Maven fetched and verified (pinned SHA-512) in the build container |
| DiscordGo | Go | |
| discordrb | Ruby 4 | `bundle install` into `vendor/bundle`; RivetPanel SDK included |

Each reads `DISCORD_TOKEN`, exits with a clear message when it is missing, and
registers `/ping`. Template metadata lists required variables, setup steps,
privileged intents and tested versions. A template may choose its start
command (the TypeScript starter runs `node src/index.mts`). Build containers get
their own memory floor from the runtime recipe (`build_memory_bytes`), used
only while building.

## GitHub deployments

Link a bot to a repository, branch and optional root directory. **Any public
repository works without a GitHub connection**: paste `owner/name` or a GitHub
link (a `/tree/<branch>/<folder>` link fills in the branch and folder too). A
GitHub connection (`oauth.md`) is needed for private repositories, listing your
own repositories, publishing and pushing, and raises GitHub's rate limit from
60 anonymous requests per hour (shared by the panel's address) to 5,000. When
the connection exists it is used for public repositories as well. The panel downloads the commit's
**tarball** through the GitHub API and unpacks it with the contained extractor:
no `git`, hooks or filters run on the host. **Deployments** shows the release,
"What changed?" (commits and files between the deployed commit and the branch),
and a history with redeploy and roll back to a chosen commit.

The file replacement is **crash-safe**: replaced files are moved aside, and a
journal outside the workspace records the plan before the first rename. If the
panel dies mid-way, the next start rolls the workspace back to the previous
files; the deployed commit is recorded before the previous files are dropped.
A Stop, Delete or Unlink that arrives during a deployment wins.

For a bot on a remote node the panel downloads the tarball (at most 1 GiB) and
streams it to the node over the authenticated agent connection; nothing is
unpacked on the panel host. The node stages the whole archive, the panel
checks once more that the bot and its repository link are unchanged, and the
node then swaps the files and keeps the previous ones until the panel has
recorded the commit (a failure, a lost confirmation or an agent crash rolls
back). Manual deploys to an offline node are refused; polling resumes when
the agent reconnects, and a webhook push received while the node is offline
waits (the Deploy tab says so) and deploys the newest commit once the agent
reconnects, unless the repository link changed meanwhile. Nothing is staged
when the node lacks free disk space. See [agents.md](agents.md).

Auto-deploy uses a push webhook authenticated by HMAC-SHA256; unauthenticated
deliveries always get the same `401`. A webhook needs your GitHub connection
with admin rights on the repository and a public panel address; otherwise (for
example someone else's public repository) the panel **polls** the branch every
five minutes and deploys a new head once (a failing commit is not retried every
interval). Polling uses the linking account's token when there is one. Limits:
no submodules or Git LFS.

When creating a bot from GitHub, **Start the bot after the first deployment**
starts it once the files are in place (the bot is otherwise created stopped).

### Analyzing a repository

**Analyze repository** in the new-bot flow downloads the branch once and
suggests the language, start command, build command, variables (with
descriptions, required/secret flags and safe defaults), databases and
resources, plus setup steps such as privileged intents. Sources, in order:

1. **Verified recipes** for well-known bots (Red-DiscordBot, YAGPDB), tested on
   a real panel. They are offered as one-click buttons under *Popular
   open-source bots*.
2. **Detection** from files: `package.json` (start script, TypeScript build,
   Yarn/pnpm), `requirements.txt`/`pyproject.toml`/`setup.py`/`Pipfile`,
   `go.mod` with the `package main` directories (`cmd/<name>`), `Cargo.toml`,
   Maven/Gradle, `Gemfile`; `.env.example`-style files (comments become
   descriptions; example values are kept only when they are not placeholders or
   secrets); variables the code reads (`process.env.X`, `os.getenv`,
   `os.Getenv`, `env::var`, ...); database clients and docker-compose images
   (PostgreSQL, Redis, MongoDB, MariaDB/MySQL); Dockerfile `CMD` and `EXPOSE`.
3. Optionally **refined by the AI** (*Refine with AI*, when an administrator
   configured a provider): file excerpts (at most about 60 KB: manifests,
   example configuration, entry files, README) and the detected plan are sent
   to the provider, which returns a plan as JSON.

Every suggestion only prefills the form; nothing is created or run until you
create the bot. The server validates suggestions again (start commands must be
allowed for the runtime, unknown add-ons and reserved variables are dropped,
secret values are never prefilled, resources are clamped to the limits), and
repository text is treated as untrusted data in the AI prompt. **Review a
suggested build command before creating the bot**: it runs in the build
container. Limits: archives up to 512 MiB compressed are read; at most 4,000
paths and 48 MiB of source are inspected; 60 variables are listed.

## Add-ons (databases)

A bot can run up to four companion databases next to it: **PostgreSQL 17**,
**Redis 7**, **MongoDB 7** and **MariaDB 11**. Add them while creating a bot
(*Start, build and databases*) or on the bot's **Add-ons** tab (bot stopped).

* Each add-on is its own container with a memory limit (default 128–512 MiB),
  CPU and PID limits, a read-only root filesystem, all capabilities dropped,
  `no-new-privileges` and the panel's unprivileged container user.
* Add-ons join a private, **internal** Docker network per bot
  (`rivetpanel-<bot>-net`): they have **no internet access**, only that bot (and
  its other add-ons) can reach them, by host name (`postgres`, `redis`,
  `mongodb`, `mariadb`). The bot keeps its normal network and joins the private
  one; a bot with networking disabled uses only the private network.
* The bot receives connection variables automatically, for example
  `DATABASE_URL`, `POSTGRES_PASSWORD`, `REDIS_URL`, `MONGODB_URI` and
  `MYSQL_URL` (*Show connection* reveals them). Your own variables can
  reference them as `${NAME}`, for example
  `YAGPDB_PQPASSWORD=${POSTGRES_PASSWORD}`; unknown references and lone `$`
  signs are left as written.
* Passwords are generated (144 bits) and stored sealed like other variables
  (as hidden `RIVET_ADDON_<KIND>_PASSWORD` rows, so key rotation and
  verification cover them); they never appear in container labels. Redis has
  no password: the private network is its access control.
* Add-ons start before the bot and the bot is created only after their health
  checks pass (up to three minutes); they stop with the bot and keep their
  containers, so a restart reuses them. A crashed add-on is restarted while the
  bot runs. The tab shows state, health, memory, data size and the last 200
  log lines.
* Data lives in `<database directory>/addons/<bot>/<kind>`, outside the bot's
  workspace (file manager, SFTP and deployments never touch it). Removing an
  add-on or deleting the bot deletes its data.
* Add-on memory counts toward per-user and node memory budgets.

Limits: one add-on of each kind per bot; add-on data is **not** included in
bot backups or `rivetpanel backup` (dump it from the bot, for example with
`pg_dump`); no version choice or extensions; no Lavalink yet.

## Logos

Bots and sites can have a **custom logo** (PNG or JPEG, resized in the browser
to at most 256×256, stored up to 256 KiB). A bot shows its custom logo, else
its Discord avatar. The avatar arrives through the telemetry SDK, or
**Get avatar from Discord** (bot Settings) asks Discord's API for the bot user
with the token found in the bot's variables (`DISCORD_TOKEN`, `BOT_TOKEN`,
`TOKEN`, `RED_TOKEN`, `YAGPDB_BOTTOKEN`, ... or any value shaped like a bot
token); the token is only sent to Discord. A site shows its custom logo, else
the favicon of its active release (the `<link rel="icon">` in `index.html`, or
`favicon.ico`/`.png`/`.svg`), else its bot's logo. Logos are served to signed-in
people with access only, sandboxed and without content sniffing.

### Publishing a bot to GitHub

The other direction also works: **Deploy → Publish to GitHub** creates a new
repository (under your account or one of your organizations, private by
default) from the bot's current files, pushes them as the first commit and
links the bot to it, optionally with auto-deploy. For a linked bot, **Push to
GitHub** commits the current files (for example after editing them in the
panel or over SFTP) to the linked branch.

* Pushing uses the **acting user's own** GitHub connection with repository
  access (`repo` scope), never the token of whoever linked the repository, and
  needs *Edit files* on the bot (publishing a new repository needs *Full
  admin*).
* Nothing runs on the host: files become blobs, a tree and a commit through
  the GitHub Git Data API. Files GitHub already has are not uploaded again.
* The push is never forced. If the branch moved on GitHub meanwhile, the push
  stops with an explanation; deploy those changes first.
* With a linked sub-folder, only that folder of the repository is replaced;
  the rest of the repository stays.
* What is sent: every file except those excluded by `.gitignore` files (root
  and nested, with negation) and the built-in rules: `.git`, dependency and
  cache folders (`node_modules`, `.venv`, `venv`, `__pycache__`, `target` at
  the top, …), panel metadata, and `.env`/`.env.*` files (`.env.example`,
  `.env.sample`, `.env.template` and `.env.dist` are kept). Environment
  variables stored in the panel are never pushed. The dialog lists every file
  before anything is created, so secrets in other files can be spotted.
* Limits: 5,000 files, 200 MiB in total, 25 MiB per file (larger files are
  skipped and listed). Each push is recorded as a *Push to GitHub* operation
  in the deployment history; the pushed commit is recorded as deployed, and
  its own webhook delivery does not redeploy the bot.
* For a bot on a remote node, the node selects the files with the same rules
  and limits and the panel reads only those files, one at a time, over the
  authenticated agent connection; nothing is copied to the panel's disk.
  Publishing and pushing are refused while the node is offline.

## Backups

Manual and scheduled snapshots under `RIVET_BACKUP_DIR` (mode 0600, SHA-256
recorded), with labels, integrity verification, a health summary and an
optional "stop the bot while copying" mode. Contents: the workspace without
rebuildable folders plus, optionally, the environment as **sealed** rows,
openable only with this server's key and the same bot ID.

Restore (full admin, bot stopped): one review dialog; verifies the checksum;
checks the environment decrypts **before changing anything**; takes a
`pre_restore` safety copy; unpacks into staging (rejecting `..`, absolute paths,
devices, hard links, escaping symlinks; dropping setuid bits); then replaces the
files with the same **journaled, crash-safe commit** as deployments. The
previous files are kept until the database records the restore, so a database
error or a crash returns the complete previous state.

Limits: 50 backups per bot (5 slots reserved for scheduled and safety copies),
2 GiB per workspace (`RIVET_BACKUP_MAX_BYTES`). Backups, restores and
deployments refuse to start with less than `RIVET_MIN_FREE_DISK_BYTES`
(default 1 GiB) free. They are **not** included in `rivetpanel backup`.
For a remote server the archive is created on its assigned node and streamed
into the same panel backup directory; restores require that node to be online.

## Scheduled actions

**Schedules** runs backup, start, stop, restart or deploy on a timetable:
presets (daily, weekly, every few hours) or five-field cron, in any IANA time
zone, with the next runs previewed. Daylight-saving gaps are skipped and
repeated hours run once. One scheduler checks due rows every 30 seconds.

- Each schedule runs with its creator's **current** permissions; a removed
  grant or disabled account pauses it ("denied").
- If the panel was down at the due time, the run is recorded as **missed** and
  not repeated: no backlog after downtime.
- A scheduled restart never starts a stopped bot. Conflicts with running work
  are recorded as skipped.
- Per-bot backup schedules replace the panel-wide backup interval for that bot.
- At most 20 schedules per bot. "Run now" tests a schedule.

## Health and alerts

**Health & alerts** shows application health as reported by the bot itself,
separately from Docker's process state: every SDK push is a heartbeat and may
say whether the bot is connected to Discord (`ready`). A bot that never reported
is *unknown*, never *failed*.

An optional TCP or HTTP probe checks one of the bot's published TCP ports
through `127.0.0.1`. Intervals, timeouts, success/failure thresholds and startup
grace are configurable. When enabled, the panel replaces the container once on
an unhealthy transition; startup grace and transition-only restarts prevent a
broken deployment from flapping continuously. Probe targets cannot name an
arbitrary host, so this feature cannot be used for server-side request forgery.

Per-bot Discord notification preferences: crashes, deployments, backup
failures, recoveries, and an optional **heartbeat rule** that alerts once when a
running bot stops reporting for 1 minute to 1 hour (and once when it recovers).
A test message can be sent. Crash alerts are throttled to one per bot per 10
minutes.

## Notifications and support

A bell in the top bar collects in-panel notifications (alerts, deployments,
backups, sharing, node state for node managers, announcements, support
replies) with per-category in-panel/email preferences under Settings →
Notifications. **Support** opens tickets with threaded replies, statuses,
staff assignment and internal notes (`tickets.*` permissions). The
**help center** (`/help`) shows Markdown articles written under
Administration → Knowledgebase (`kb.manage`): draft or published, visible
to everyone, signed-in accounts or support staff, searchable, suggested
while a ticket subject is typed, and readable without an account only when
the public help center is on. The **status page** (`/status`,
`status.manage`) publishes chosen components under public names with
automatic states, incidents, maintenance windows and 90-day uptime bars.
Details, limits and authorization rules: `docs/support.md`.

## Host monitoring and panel logs

**Administration → Host** has four tabs.

- **Resources**: CPU, memory (cache, swap), load average, disk, network and disk
  throughput now and over 1 hour to 30 days (averages plus dashed peaks; the
  range is capped by `RIVET_TELEMETRY_RETENTION`, 7 days by default), a
  disk-fill forecast from the trend, host facts, the RivetPanel process (memory,
  goroutines, open files, requests, errors, database size), the Docker runner
  and storage per location (directory sizes are counted in the background
  about every five minutes). A "needs attention" list flags a nearly full disk,
  memory or swap pressure, a high load, a Docker problem and recent errors.
- **Bots**: every bot's CPU (cores against its limit), memory against its
  limit, network totals since the container started, processes and workspace
  size; sortable, refreshed every 10 seconds, at most 60 running bots measured
  per refresh.
- **Capacity**: the admission budgets below, with each limit's current value
  and source.
- **Panel logs**: the newest 2,000 lines the panel logged since it started,
  with level filter, search, live follow, copy and download. Secrets, tokens and
  passwords are redacted before a line is kept. The view starts empty after a
  restart; the complete log stays in journald (`journalctl -u rivetpanel`) or
  `docker logs`.

## Usage analytics

Every bot and game server has an **Analytics** tab (anyone with console
access to it, the same as the live gauges): CPU and memory (averages with
dashed peaks against the limits), network traffic per bucket, uptime, crashes
and starts, workspace size, deployments (succeeded, failed, average duration;
bots only) and backups (succeeded, failed, bytes written). Ranges: 1 hour and
24 hours (5-minute buckets, the current five minutes included as they are
collected), 7 and 30 days (hourly) and 90 days (daily). **Export CSV**
downloads the rows shown (`GET /api/v1/bots/:id/usage?range=…&format=csv`).

**Administration → Analytics** (permission `analytics.view`) sums the same
data across the panel for 24 hours to 90 days: accounts (total and new), bots
and game servers (total and running), CPU and memory in use, network,
uptime, crashes and starts, deployments, backups, and the top 10 consumers by
CPU, memory and network. With `nodes.manage` it adds each node's average and
peak CPU, memory, disk, network and a CPU trend, grouped by location; with
`tickets.view_all` or `tickets.manage` it adds tickets opened, closed and
waiting and the median time to the first staff reply. Owners' addresses are
shown with `users.view`. A delegated viewer (not a built-in administrator)
sees only the accounts it could manage under the delegated-administration
rule and their bots; API clients never count as administrators, and clients
limited to bots or workspaces cannot open the overview. CSV:
`GET /api/v1/admin/analytics?range=…&format=csv`.

How it is collected (no new agent protocol, nothing pushed by bots):

- Once a minute the panel reads every bot's state and, for running bots,
  one Docker stats reading (locally) or an agent stats reading (connected
  remote nodes), at most 100 bots per pass in rotation with 4 at a time.
  Bots on a disconnected node are counted for uptime but not measured.
- Uptime is the share of samples in which a bot that was meant to run was
  running. Crashes are increases of the restart policy's consecutive-crash
  counter; starts are changes of the last start time (several in one minute
  count once).
- Network is the difference of the container's counters between samples.
- Workspace size is measured every 30 minutes for bots on this panel's own
  node only (up to 10 per pass).
- Deployments and backups are counted from the operations history (and the
  backup's size) into hourly rows every five minutes, so the counts stay
  after old operations are pruned. On first start the last 35 days of
  operations still recorded are counted.
- Nodes come from the existing node telemetry samples, rolled up into
  hourly and daily rows so node trends outlive
  `RIVET_TELEMETRY_RETENTION`.

Storage and retention (SQLite tables `bot_usage`, `node_usage`, migration
`0052`): one transaction of 5-minute rows per five minutes; hourly and daily
rows are recomputed for the current and previous hour/day only. 5-minute
rows are kept 3 days, hourly rows 35 days, daily rows 400 days; pruning runs
hourly in batches of 1,000. Deleting a bot deletes its rows. Time the panel
itself was not running records nothing (a gap, not downtime).

## Environment variables from the panel

**Administration → Environment** lists every `RIVET_` variable with its
description, default and source (default, environment file or set here) and
lets an administrator change most of them. Values are stored in the panel's
database (the metrics token encrypted) and layered over the process environment
when RivetPanel starts, so a change applies after a restart; the page shows what
is waiting. The environment file is never edited. Precedence: value set here,
then environment, then the built-in default; a value set here may be empty to
switch off something the environment enables.

Not editable here: variables read before the database opens (`RIVET_ENV`,
`_LISTEN`, `_DB_PATH`, `_DB_MAX_CONNS`, `_DATA_ROOT`, `_KEY_DIR`,
`_ACTIVE_KEY_ID`, `_RUNTIMES_DIR`), ones that decide what the panel may do to
the host (`_DOCKER_HOST`, `_CONTAINER_USER`, `_WORKSPACE_OWNER`,
`_ALLOW_ROOT_CONTAINER_USER`, `_CONTAINER_NETWORK`, `_PROXY_HEADER`), and the
sign-in and address variables, which Panel settings owns. Every save is checked
as a whole configuration first; if a saved set ever stops validating, the panel
starts without it and says so on the page. **Restart panel** exits with code 75
and needs systemd (`Restart=on-failure`) or a container restart policy; without
a detected supervisor the button is hidden. Bot containers keep running through
a restart. From the host: `rivetpanel env` lists the saved values and `rivetpanel
env reset [NAME…]` drops them.

## Capacity and admission

Administrators can set budgets in the environment file or on the Environment
page (0 = unlimited):

| Setting | Effect |
| --- | --- |
| `RIVET_NODE_MEMORY_BYTES` | sum of memory limits of bots wanted running; a start that would exceed it is refused (409) with the numbers |
| `RIVET_USER_MEMORY_BYTES` | sum of a user's bot memory limits |
| `RIVET_MAX_BOTS_PER_USER` | bots per user |
| `RIVET_MAX_BUILDS` | concurrent build containers (default 1); waiting builds say so |
| `RIVET_MIN_FREE_DISK_BYTES` | free space backups, restores and deployments need (default 1 GiB) |

The node check and the start are one database transaction, so simultaneous
starts cannot overcommit. Per-user budgets do not apply to administrators.
These are admission rules, not kernel limits: containers still have their own
hard memory limits, and there are no disk quotas.

## Accounts and sign-in

Passwords (Argon2id), GitHub and Discord sign-in (`oauth.md`), OpenID Connect
single sign-on and passkeys (`auth.md`), password change
that signs out other sessions, a sessions list with sign-out, and at most 20
sessions per user. `rivetpanel reset-password EMAIL` is the host recovery path.
With Mailgun configured (`email.md`) people can also reset a forgotten password
from the sign-in page by emailed one-use link, administrators can email
invitations, bot owners can receive alerts by email (switch in Profile), and
every password or two-step change sends a security notice. Administrators can
email HTML news and policy updates to accounts (Administration →
Announcements; `email.md`).

**Email verification**: new accounts confirm their address with a one-use
link (24 hours, only the SHA-256 is stored, resends rate limited) from
Settings → Profile; GitHub/Discord sign-ups and accounts from before
verification existed count as verified. Changing your own address needs your
password and takes effect only when the new address is confirmed. Panel
settings can withhold chosen permissions from unverified accounts (off by
default); account managers can mark addresses verified by hand. See
[permissions.md](permissions.md).

**Single sign-on (OpenID Connect)**: administrators add providers under
Administration → Single sign-on (discovery checked on save, secret sealed,
PKCE + state + nonce, ID token signature/issuer/audience/expiry verified).
Identities link automatically only by a provider-verified address and never to
accounts with administration permissions; otherwise people link from
Settings → Connected accounts. Optional sign-up with a default role (never
Administrator). See [auth.md](auth.md).

**Passkeys** (WebAuthn): added under Security (password required); sign in
without a password, or use one instead of the TOTP code. Needs the panel
address to be a host name.

**API clients**: `rvc_` bearer tokens for the whole API with a chosen subset of
the creator's permissions (re-checked every request, never administrator),
optionally limited to bots/workspaces (403 elsewhere), expiring or not,
revocable by the owner or under Administration → API clients, audited. See
[auth.md](auth.md).

**Two-step sign-in** (TOTP): set up in Security with any authenticator app
(key shown for manual entry, plus an `otpauth://` link for phones), confirmed
with a first code, with 10 one-use recovery codes (only hashes stored). It
applies after both password and provider sign-in. Codes cannot be replayed;
a sign-in ticket allows 5 attempts in 5 minutes. With two-step sign-in on, SFTP
no longer accepts the account password (use SFTP keys). `rivetpanel reset-mfa
EMAIL` removes it from the host.

## SFTP

Off unless `RIVET_SFTP_LISTEN` is set. Sign in with your panel email plus
your password or an **SFTP key** (Settings → SFTP & API keys). One folder per
bot you may edit. Same containment as the web file manager. Access is re-checked
about every 5 seconds, including open file handles. No shell/exec/forwarding;
3 auth tries per connection, 10 failures per minute per address, 32 connections,
a per-file size cap. The host key is kept next to the database. Folders of
servers on a remote node are served through that node's agent: transfers pass
through a private temporary file on the panel, an upload reaches the node in
one atomic write when the file is closed, listings show no modification
times, and an offline node answers with an error. **SFTP does not
work through Cloudflare's proxy or a Cloudflare Tunnel.**

## Automation API

Scoped bearer tokens (`bpa_...`) for scripts and CI: actions `read`, `power`,
`deploy`, `backup`, optionally limited to chosen bots, expiring after 1-366
days, revocable, with last use shown. Effective access is the token's scope
intersected with the owner's current permissions. `Idempotency-Key` makes
retries safe; every response has `X-Request-ID`; 120 requests per minute per
token. SFTP keys never work here and tokens never work for SFTP. See
`automation.md` and `/api/v1/automation/openapi.yaml`.

## Bot analytics

Generate a telemetry key in **Public page → Private insights** (stop the bot first; the key becomes
`RIVET_TELEMETRY_KEY`). Bots push to `POST /api/v1/bot-telemetry` (or the
WebSocket at `/api/v1/bot-telemetry/ws`) with `Authorization: Bearer <key>`:

```json
{"ready":true,
 "stats":{"guilds":12,"members":3400,"ping":42},
 "commands":[{"name":"ban","count":2}],
 "events":[{"name":"guild_join","data":{"id":"1"}}]}
```

Limits: 16 KiB per push, 60 pushes per minute per bot, 16 stats / 20 commands /
10 events per push, event data up to 1 KiB of JSON, one sample per stat per 10
seconds, at most 20,000 rows per bot, 7 days retention. Snippets (served from
`/api/v1/sdk/<lang>`): discord.js, discord.py, Go (any library, standard
library only), Rust (reqwest), Java (JDK HTTP client) and Ruby (discordrb). They
push every 30 seconds, never queue failed pushes and never crash the bot.

## Discord notifications

If a user connects Discord with *Enable notifications*, alerts for their bots
are posted to the webhook they chose, following each bot's preferences (see
Health and alerts). Only Discord webhook URLs are ever called.

## Installation identity

Each installation has an identity stored in its database. Containers are
labelled with it, so two panels sharing one Docker daemon never list, adopt or
remove each other's containers. Containers created before the label existed are
adopted only when their bot exists in this database, and are never swept as
orphans. A restored installation keeps its identity. A lock file stops two
processes from using one database.

## Operations

`rivetpanel version | doctor | verify | reseal | backup | backup-verify | restore
| create-admin | reset-password | reset-mfa | keygen`. `reseal` (panel stopped)
re-encrypts every sealed value with the active key and can be re-run to
continue; keep old key files while per-bot backups made before the reseal exist.
`make release VERSION=x.y.z` builds Linux amd64/arm64 archives with
`SHA256SUMS`.
## AI assistant

One private AI chat opens from **Ask AI** on every page (bottom right, or the
sparkle button in the header). It sees which bot, site, section and file you
are looking at, can read a bot’s console output and build log, and can propose
changes that wait for your approval. See [AI operator](ai-operator.md) for
providers, permissions, approval modes, research, limits, data boundaries,
diagnostics and recovery behavior.
