# Changelog

All notable RivetPanel changes are recorded here. The project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html); `VERSION` is the
authoritative current version.

## Unreleased

### Added

- **Usage analytics (migration `0052`).** Bots and game servers have an
  Analytics tab with CPU, memory, network, uptime, crashes and starts,
  workspace size, deployments (succeeded/failed, average duration) and backups
  (succeeded/failed, size) over 1 hour to 90 days, with CSV export.
  Administration → Analytics shows panel-wide totals and trends: accounts,
  bots and servers by kind, resource use, uptime, deployments, backups, top
  consumers, nodes and locations (with `nodes.manage`) and ticket volume and
  first-reply time (with ticket access). New permission `analytics.view`
  (administrators; delegable): delegated viewers and API clients only see
  accounts they could manage and those accounts' bots; scoped API clients
  cannot open the overview. The panel samples bots once a minute (Docker or
  the agent, at most 100 running bots per pass) and stores 5-minute rows for
  3 days, hourly rollups for 35 days and daily rollups for 400 days in new
  tables `bot_usage`, `node_usage` and `usage_marks`; deployment and backup
  counts are taken from the operations history and survive its pruning. No
  agent protocol change and no new web dependency (the existing chart
  component is reused).
- **Steam game servers (SteamCMD).** New built-in server types for Valheim,
  Rust and Project Zomboid, installed and updated from Steam with an
  anonymous SteamCMD login in a separate install container (no Steam
  credentials are ever asked for or stored). Blueprints gain an
  `install.steamcmd` step (app id, optional beta branch, validate, time limit
  and a size watchdog), stop signals (`stop_signal: SIGINT`), consecutive
  port allocation (`ports.contiguous`), `SERVER_PORT_1`…`SERVER_PORT_10` for
  additional ports, INI config edits and the Steam A2S status query (players,
  name, map, version on the query port; local nodes only). Install scripts
  now see the server's declared variables. Running these games is subject to
  Steam's and each game's terms. See `docs/game-servers.md`.
- **Pterodactyl egg importer.** Administration → Server types → Import
  Pterodactyl egg converts a PTDL_v1/PTDL_v2 egg into a blueprint draft
  (images, startup, stop, variables and validation rules, install script,
  properties/INI/file config edits) and lists everything it could not map as
  warnings; the administrator reviews and edits the YAML before saving it as a
  custom server type. Eggs are untrusted: at most 512 KiB, nothing they link
  to is fetched, and their install scripts run only in the sandboxed install
  container as the unprivileged server user. Requires `blueprints.manage`.
- **In-panel notifications (migration `0048`).** A bell in the top bar shows
  the unread count (polled every 30 s while the tab is visible) and the latest
  notifications; `/notifications` lists, marks read, deletes and clears them.
  They come from the existing event paths: bot crashes and heartbeat alerts,
  deployments, backup failures, node agents offline for 2 minutes and back
  (accounts that manage nodes), sharing/transfers/workspace membership/accepted
  invitations, announcements and support tickets. Settings → Notifications
  turns each category on or off in the panel and by email. Each account keeps
  at most 200 notifications for 90 days. Only browser sessions can read the
  inbox (API clients are refused). See `docs/support.md`.
- **Support tickets (migration `0049`).** Support in the sidebar opens tickets
  (subject, category, priority, optionally a bot or server you can access),
  with threaded replies, statuses (open, pending, resolved, closed), staff
  assignment, internal staff notes never shown to the requester, notifications
  and email on replies, and an activity record. New permissions:
  `tickets.create` (built-in User role), `tickets.view_all` and
  `tickets.manage` (administrators; delegable through custom roles, limited
  to requesters the staff account outranks). Requesters only ever see their
  own tickets. No attachments.
- **Knowledgebase / help center (migration `0050`).** Staff with the new
  `kb.manage` permission write Markdown articles in categories
  (Administration → Knowledgebase) with draft/published state, ordering and
  per-article visibility: public, signed-in accounts, or support staff only.
  `/help` lists and searches what each reader may see; public articles are
  readable without an account only when the administrator turns on the public
  help center. The new-ticket form suggests related articles while the
  subject is typed. Markdown is rendered as text (raw HTML is shown, never
  run) and links other than `http(s)://` and panel paths are refused when
  saving. Changes are recorded in the activity log.
- **Public status page (migration `0051`).** With the new `status.manage`
  permission, Administration → Status page publishes `/status` (and
  `GET /api/v1/status`): chosen components (the panel, nodes, bots or game
  servers) under public names, states derived automatically from the panel,
  node connections, bot state and health checks, manual incidents with an
  update timeline, scheduled maintenance windows, and 90 daily uptime bars
  from 5-minute samples (90 days kept). The public view never includes
  internal ids, names, owners, addresses or errors, answers "not found" while
  disabled, and is rate-limited per client address. Subscriber notifications
  are not included yet.
- **Announcements to the notification inbox.** Administration → Announcements
  can post an announcement (as plain text) to the audience's inbox, with or
  without email, so it works on panels without Mailgun.

- **API clients (migration `0045`).** Settings → SFTP and API keys → API
  clients creates `rvc_` bearer tokens that use the same API as the panel,
  carrying only the permissions you choose from the role catalog (never more
  than your role holds; re-checked against your current role on every
  request), optionally limited to some bots and workspaces, with an expiry of
  up to a year or none. Tokens are shown once and stored hashed; last use is
  recorded; creation, revocation, changes made with a client and refusals are
  in the activity log. Administration → API clients lists every account's
  clients and lets account managers revoke them. See `docs/auth.md`.
- **Single sign-on with OpenID Connect (migration `0046`).** Administration →
  Single sign-on adds any OpenID Connect provider (issuer discovery checked on
  save, client secret stored encrypted). Sign-in uses PKCE, state and nonce
  and verifies the ID token's signature, issuer, audience and expiry. An
  identity links to an existing account automatically only when the provider
  says the address is verified (and never to accounts with administration
  permissions); otherwise people sign in another way and connect the provider
  under Settings → Connected accounts. Optional sign-up gives new accounts a
  chosen role (never Administrator). Two-step sign-in still applies.
- **Passkeys (migration `0047`).** Settings → Security → Passkeys registers,
  renames and removes passkeys (current password required to add). The sign-in
  page offers passwordless passkey sign-in, and accounts with two-step sign-in
  can use a passkey instead of the code. Passkeys need the panel address to be
  a host name.
- **Custom roles with named permissions (migration `0044`).** Administration
  → Roles creates roles from 25 permissions (13 for bots, servers, sites and
  workspaces, 12 for parts of the administration such as accounts, nodes,
  server types, allocations, panel settings, announcements and host
  monitoring). The built-in Administrator and User roles are kept unchanged as
  system roles, and existing accounts keep their behaviour. Roles are assigned
  on the Users page (or when adding a user) and apply on the account's next
  request. A custom role can delegate parts of the administration without
  making someone an administrator; delegates can never grant more than they
  hold. See `docs/permissions.md`.
- **Email verification.** New accounts confirm their address with a one-use
  link from Settings → Profile (sent automatically after registering from an
  invitation when email is set up); existing accounts and GitHub/Discord
  sign-ups count as verified. Changing your own address now needs your
  password and takes effect only when the new address is confirmed; the old
  address is notified. Administration → Panel settings can withhold chosen
  permissions from unverified accounts (off by default), and account managers
  can mark addresses verified by hand on panels without email. The Users page
  shows each account's role and verification state, and account managers can
  change an account's address there (it then needs verifying again).

- **Remote execution nodes (preview, migration `0042`).** The new
  `rivet-agent` binary enrolls with a one-use token, connects outward over
  mutually authenticated TLS, and runs assigned bots and Minecraft servers on
  its own Docker host. Lifecycle, console/stdin, live container statistics,
  files and Minecraft status queries route through the authenticated node
  connection. Administration → Nodes manages locations, enrollment commands,
  connection/capability state, public addresses, drain/enable state,
  certificate revocation and deletion; administrators can choose a node when
  creating an empty bot or game server. Manual, scheduled and pre-restore
  safety backups now stream remote workspaces into panel-managed backup
  storage; restore keeps the old remote workspace journaled until the panel
  confirms its database update. This advances the preview agent protocol to
  version 2, so existing agents must be upgraded with the panel. Release
  archives and container images now include the agent, with a systemd unit
  and installer documented in
  `docs/agents.md`.

- **GitHub deployment for bots on remote nodes (agent protocol 3).** A bot
  assigned to a connected `rivet-agent` node can link a public or private
  repository, deploy manually, and receive webhook or polled auto-deploys.
  The panel downloads the bounded (1 GiB) tarball and streams it to the
  assigned agent over the mutually authenticated node connection; it is never
  unpacked on the panel host. The agent validates and stages the whole
  archive, the panel re-checks that the bot still exists and that its
  repository link and node are unchanged, and only then does the agent swap
  the files. The previous files stay in the agent's journal until the panel
  has recorded the commit; a failed database update rolls them back, and a
  confirmation that never arrives (or an agent that crashes mid-swap) rolls
  back on the node. Deploys to an offline node are refused with a clear
  message, and polling waits until the agent reconnects (a webhook push
  delivered while the node is offline is recorded as a failed deployment).
  Administrators can now choose a connected node when creating a bot from
  GitHub. Remote restores and deployments share one transaction lifecycle.
  **The agent protocol advances to 3: upgrade the panel and every
  `rivet-agent` together**; an older agent is refused at connection time.
- **GitHub publish and push for bots on remote nodes (agent protocol 4).**
  Publish to a new repository and Push to GitHub now work for bots on a
  connected `rivet-agent` node. The node selects the files with the same
  `.gitignore`, dependency/metadata and `.env` exclusions and the same limits
  as local publishing (5,000 files, 200 MiB, 25 MiB per file); the panel reads
  only those files over the authenticated node connection, never copies the
  workspace to its own disk, and records the pushed commit so its webhook does
  not redeploy. Offline nodes are refused before a repository is created or a
  push is queued. **The agent protocol advances to 4: upgrade the panel and
  every `rivet-agent` together**; an older agent is refused at connection
  time.
- **Package manager and AI file tools for bots on remote nodes (agent
  protocol 5).** The visual package manager now lists and edits
  `package.json`, `requirements.txt`, `pyproject.toml`, `Cargo.toml` and
  `go.mod` on the bot's connected `rivet-agent` node. Each edit is written
  only if the manifest is still the version that was read; if someone changed
  it meanwhile the edit is refused with a "reload and try again" conflict
  instead of overwriting their change. The AI assistant's list, read, search,
  propose-change and Undo tools now work on remote bots with the same
  protected paths, 1 MiB per-file and per-run limits and secret redaction as
  local bots: a change is checked against the file revision the assistant
  read, swapped on the node through the same journaled transaction as remote
  restores and deployments, and kept reversible until the panel has recorded
  it; if the node never confirms, the change is rolled back and shown as not
  applied. Neither feature reads or writes the panel's own disk for a remote
  bot, and both are refused with a clear message while the node is offline.
  Isolated AI diagnostics remain local-only and are now refused for remote
  bots instead of running against the panel's copy. **The agent protocol
  advances to 5: upgrade the panel and every `rivet-agent` together.**
- **Template bots on remote nodes.** Administrators can now create a bot from
  a template on a connected `rivet-agent` node. The template files are
  written on that node in one create-only transaction through the agent's
  journal and committed only as the last creation step; if the node refuses
  the files or the commit cannot be confirmed, the change is rolled back and
  the half-created bot is removed through the agent's normal purge instead of
  being left behind looking healthy. Offline nodes are refused before
  anything is recorded, and nothing is written to the panel's own disk. The
  New bot page now offers connected nodes for templates. No protocol change
  (still 5).
- **Modrinth plugins and mods for game servers on remote nodes.** Installing
  from the Plugins/Mods page now works for a server on a connected node. The
  panel downloads the file into a temporary file, verifies the CDN allowlist,
  the 256 MiB limit, the declared size and the published SHA-512 over the
  whole file, and only then streams it to the node, which writes it
  atomically; the temporary file is removed immediately and nothing is
  written to the panel's copy of the server. Offline nodes are refused before
  downloading. No protocol change (still 5).
- **SFTP for bots and game servers on remote nodes.** The SFTP folder of a
  server on a connected `rivet-agent` node now lists, downloads, uploads,
  renames, creates and removes its files on that node. The panel still signs
  the user in, checks the files permission on every operation, applies the
  per-file size limit (`RIVET_SFTP_MAX_FILE_BYTES`) and pauses changes while a
  deployment or restore holds the server; the node's filesystem layer keeps
  every path inside the server's directory. Transfers pass through a private,
  already-deleted temporary file on the panel: an upload is sent to the node
  in one atomic write when the client closes the file (a write that only
  changes part of a file is refused if someone changed the file meanwhile),
  and an upload that hit the size limit is never sent. Remote listings show no
  modification times. A server whose node is offline answers with a clear
  "node is offline" error. No protocol change for SFTP.
- **Add-on states and logs for bots on remote nodes (agent protocol 6).** The
  Add-ons tab now shows the state, health and last log lines of database
  add-ons that run next to a bot on a connected node, read from that node's
  Docker through the agent (at most 500 lines and 1 MiB per request). The
  panel never asks its own runner about a remote bot, and add-on logs are
  refused with a clear message while the node is offline. **The agent
  protocol advances to 6: upgrade the panel and every `rivet-agent`
  together**; an older agent is refused at connection time.
- **Remote nodes are feature-complete (agent protocol 7, migration
  `0043`).** The AI assistant's isolated diagnostics (`run_diagnostic`) now
  work for bots and game servers on a connected node: the node checks the
  command against its own allowlist, copies a safe snapshot of the server's
  files and runs the same offline, resource-limited container on its own
  Docker (at most two at once per node); approvals, run limits and secret
  redaction stay on the panel. Agents report CPU, memory, load and the disk
  of their server volume: the panel records it for node charts, shows the
  node's free disk in a remote server's live stats (instead of the panel's)
  and refuses to stage a restore, GitHub deployment, AI change, template or
  large file (1 MiB or more, such as a Modrinth download or SFTP upload) when
  the payload plus 256 MiB would not fit on the node. A GitHub push that
  arrives while the server's node is offline is now remembered and deployed
  (the newest commit, once) when the agent reconnects; the Deploy tab shows
  the waiting push, and a push whose repository link changed meanwhile is
  dropped. Add-ons chosen when creating a server on a remote node are checked
  for that node before anything is saved; PostgreSQL, Redis, MongoDB and
  MariaDB run on agent nodes even when the panel has no Docker runner, and
  their data is deleted on the node when an add-on is removed or attached
  again. **The agent protocol advances to 7: upgrade the panel and every
  `rivet-agent` together**; an older agent is refused at connection time.
- **Busy ports are detected on remote nodes (agent protocol 8).** A game
  server created on, or given another allocation on, a `rivet-agent` node no
  longer receives a port that is already bound on that node (for example by
  another Minecraft server outside the panel): the agent bind-tests the
  candidate ports and the panel skips busy ones; if the node cannot answer,
  the allocation is refused rather than guessed. Starting a stopped remote
  game server whose port has since been taken is refused with the port and
  the reason instead of failing inside Docker on the node. The probe is a
  check, not a reservation, so a start can still fail if something binds the
  port in between. **The agent protocol advances to 8: upgrade the panel and
  every `rivet-agent` together**; a protocol 7 agent is refused at connection
  time with a version message.
- **Minecraft servers (preview, migration `0040`).** A new Game servers
  section creates and runs Minecraft Java servers next to Discord bots: Paper,
  Purpur, Vanilla, Fabric, Forge, NeoForge, Folia and the Velocity proxy. Pick
  a version from the provider's list, accept the Minecraft EULA and start. The
  first start downloads the server software from the official source and
  verifies its published checksum, picks the Java version the Minecraft
  release needs, and runs the Forge/NeoForge installer in a container. Game
  servers share the console, files, SFTP, backups, schedules, sharing and
  workspaces of bots. Stopping sends the server's own `stop` command and waits
  for the world to save. The server page shows the join address, players,
  version and message of the day; Startup changes settings, the version (which
  reinstalls the server software but keeps worlds) and the Java image.
- **Minecraft Players, Properties and Plugins/Mods pages.** Manage operators,
  the whitelist and bans and kick or ban online players; edit the common
  `server.properties` settings without touching the rest of the file; search
  Modrinth for plugins or mods that fit the server's loader and Minecraft
  version and install them with SHA-512 verification (migration `0041`
  records the installed version).
- **Task-chain schedules and console commands.** A schedule can run up to 20
  steps in order (console commands, start, stop, restart, kill, backup) with
  waits between them, for example a warned restart that announces it in chat,
  saves the world and restarts. Console commands can also be sent through the
  API (`POST /api/v1/bots/{id}/command`).
- **Archives in the file manager.** Download a folder (such as a world) as a
  zip, compress files or folders into a zip, and extract `.zip`, `.mrpack`,
  `.tar.gz`, `.tgz` and `.tar` archives already in a server's files. Extraction
  is staged (a rejected archive changes nothing), refuses links, absolute and
  `..` paths, and caps entries (200,000), total size (8 GiB), file size (4 GiB)
  and the zip compression ratio (200x).
- **Allocations.** Game servers get IP:port allocations published for TCP and
  UDP. New servers use a node's free pool ports, or the next free port from
  the type's default (25565), checked against other allocations, published bot
  ports and programs on the host. Administrators manage the pool and the
  addresses shown to players under Administration → Allocations; owners add
  ports and choose the primary one on the Network tab.
- **Server-type blueprints.** Game servers are defined by versioned YAML
  blueprints with immutable revisions. Built-in types update with RivetPanel
  without changing existing servers until their owner updates them.
  Administrators can hide, export and import blueprints under Administration →
  Server types (`docs/game-servers.md`).

- **Preview 1 node bootstrap foundation** (migrations `0038` and `0039`). Nodes now
  belong to locations and can declare the outbound `agent` transport. The
  database records agent protocol/certificate state and retains idempotent,
  deadline-bound commands until an agent acknowledges them, including safe
  replay after an interrupted connection. Operators can create, list and
  revoke single-use hashed enrollment tokens with `rivetpanel agent-token`.
  With `agents=preview`, a rate-limited enrollment endpoint validates an
  Ed25519/P-256/P-384 CSR and returns a 30-day, client-only certificate bound to
  the token's node identity; CSR subjects and SANs are ignored and the private
  key stays on the agent. The dedicated CA lives under
  `RIVET_KEY_DIR/agent-ca`. This bootstrap foundation is now used by the remote
  execution and certificate-lifecycle support described above.
- **Agent enrollment recovery.** `rivetpanel agent-token reissue NODE_ID`
  atomically replaces the previous token for a node that has no current
  certificate, and
  `rivetpanel agent-token discard NODE_ID` deletes such a placeholder node with
  its token. Both refuse enrolled nodes; `discard` also refuses nodes that own
  workloads.
- **Agent CA in installation backups.** `rivetpanel backup --include-keys` now
  includes the agent certificate authority (`keys/agent-ca/`, checksummed in
  the manifest) and `rivetpanel restore --restore-keys` restores it. Restore
  refuses to replace an existing CA without `--force`, and backups refuse an
  incomplete CA.

- **Preview module registry.** `RIVET_MODULES` accepts comma-separated
  `key=off|preview|stable` overrides, and authenticated clients can inspect the
  effective catalog through `GET /api/v1/modules`. Planned remote-node, game
  server, extended identity and support modules remain off until an operator
  explicitly enables them. The `extensions` entry is a backlog placeholder
  (not scheduled) with no routes or code behind it. Proxmox virtual machines
  are not planned and have no catalog entry.

- **Deploy any public GitHub repository without a GitHub connection.** New bot
  → Deploy from GitHub now has *Paste a link* (owner/name, a GitHub URL, or a
  `/tree/<branch>/<folder>` link) next to *My repositories*, with a repository
  card (description, stars, language, license). The bot's Deploy tab accepts
  pasted repositories too. A GitHub connection is still used when present
  (private repositories, higher rate limit).
- **Auto-deploy by polling** when no webhook can be installed (someone else's
  repository, no GitHub connection, or no public panel address): the branch is
  checked every five minutes and each new head is deployed once.
- **Start after the first deployment** for new GitHub bots, so they no longer
  stay stopped until the files arrive.
- **Analyze repository** in the creation flow: verified recipes for
  Red-DiscordBot and YAGPDB (offered as one-click *Popular open-source bots*),
  otherwise detection of the language, start command, build command (TypeScript,
  Yarn/pnpm, Go `cmd/<name>`, Maven/Gradle, Pipenv/Poetry), variables (from
  `.env.example`-style files and the code, with descriptions and secret flags),
  databases (client libraries, Prisma, docker-compose images), Dockerfile hints
  and resources, optionally **refined by the AI** provider. Suggestions only
  prefill the form and are validated again when the bot is created.
- **Add-ons**: PostgreSQL 17, Redis 7, MongoDB 7 and MariaDB 11 next to a bot,
  chosen at creation or on the new **Add-ons** tab. Each runs hardened like a
  bot, on a private per-bot network **without internet access** that only the
  bot reaches, by host name; the bot gets `DATABASE_URL`, `REDIS_URL`,
  `MONGODB_URI`, `MYSQL_URL` and friends automatically, generated passwords are
  sealed, the bot starts only after its add-ons are healthy, and add-on memory
  counts toward memory budgets. The tab shows state, health, data size, the
  connection variables and the add-on's log (migration `0036`). Add-on data is
  not included in backups.
- **`${NAME}` references in bot variables**, expanded when the container
  starts, for example `YAGPDB_PQPASSWORD=${POSTGRES_PASSWORD}`.
- **Custom build commands** (Startup → Build): shell commands that replace the
  language's default build step, run in the existing build container without
  the bot's variables (migration `0036`).
- **Logos for bots and sites** (migration `0037`): upload a custom logo (cropped
  and resized in the browser); bots otherwise show their Discord avatar, which
  **Get avatar from Discord** now fetches with the bot's own token (no SDK
  needed); sites otherwise show their release's favicon, or their bot's logo.
  The sites list shows site icons.

- **Email through Mailgun** (`docs/email.md`), off until an administrator
  configures it in Administration → Panel settings → Email (Mailgun) or with
  `RIVET_MAILGUN_API_KEY`, `RIVET_MAILGUN_DOMAIN`,
  `RIVET_MAILGUN_REGION` (`us` or `eu`) and `RIVET_MAIL_FROM`. The key is
  stored encrypted and never shown again; values in the environment file win
  and show as read-only. **Send test** checks the key, region and the domain's
  DNS verification and sends one message, with plain-language errors.
- **Forgot your password?** on the sign-in page: an emailed one-use link that
  expires after an hour, replaces any earlier link, and signs the account out
  everywhere when used. The request answers identically for unknown and
  disabled accounts, so it cannot be used to discover accounts, and the token
  travels in the URL fragment. It appears only when email and the panel address
  are both set (migration `0033`).
- **Invitation emails**: tick "Email the link to this address" when inviting a
  user. The invitation and its link are always created and shown; if the email
  cannot be sent the dialog says why.
- **Bot alerts by email** to the bot's owner, for the events that already post
  to Discord (crash, deploy, backup, stopped reporting). Switch it off in
  Settings → Profile → Email.
- **Security notices** by email when a password is changed or reset, or
  two-step sign-in is turned on or off. These cannot be switched off.
- **Announcements** (Administration → Announcements): write HTML news or a
  policy update, preview it in a sandboxed frame, send yourself a test (required
  before sending), then email every enabled account or administrators only. A
  *notice* reaches everyone in the audience; *news* skips accounts that turned
  off "Email me news and announcements" in Settings → Profile (migration
  `0034`). Messages go out in batches of up to 100 so each person sees only
  their own address, with a plain-text alternative, a footer saying why they
  received it, and scripts, frames, forms and event handlers stripped. Limits:
  80 KB of HTML, 1,000 recipients, one at a time and one a minute (tests
  exempt). Nothing is stored: no drafts, history or queue.
- Activity record entries for test emails, announcements, news and alert-email
  changes and password resets.
- **Change a site's address.** A site's Settings now edit its address
  (`<name>.<sites domain>`), and `PATCH /sites/{id}` accepts `slug` and
  `domain_id`. The move is immediate: the old address stops working and
  another site can take it, while verified custom domains keep working.
  Addresses follow the same rules as at creation and must be free on the
  chosen sites domain.
- **Several sites domains** (`docs/sites.md`). A site can live under any
  enabled sites domain, and a name is unique per domain, so
  `docs.sites.example.com` and `docs.pages.example.net` can be different
  sites.
  - Add domains with `RIVET_SITES_DOMAINS` (comma-separated, trusted).
  - Or add them at runtime in Administration → Sites and domains → Sites
    domains. These serve only after a `_rivetpanel-domain` TXT record proves
    control, and are re-checked every six hours.
  - Administrators choose the primary domain (the default for new sites), turn
    domains off, move every site from one domain to another (all or nothing),
    and remove unused ones.
  - The New site dialog and a bot's Public page let the creator pick the
    domain.
  - The panel's host, its parent domains and subdomains, overlapping sites
    domains, and custom domains under a sites domain are refused.
  - The reverse proxy still needs a route and a certificate for every sites
    domain. The guide shows a Traefik catch-all route and the Cloudflare
    origin-certificate steps.
- Activity record entries for sites-domain changes.

### Changed

- Bot alert, deployment and backup emails now follow the per-category
  notification preferences as well as the profile's Alert emails switch, and
  go only to verified email addresses (accounts created before verification
  existed count as verified). Discord alerts are unchanged.
- Custom roles created before this version do not include `tickets.create`;
  add it to let their accounts open support tickets.

- The `game_servers` module is now on by default as a preview; set
  `RIVET_MODULES=game_servers=off` to hide it. The main navigation lists Bots
  and Game servers separately.

- **The project is now RivetPanel.** The executable, Go module, web package,
  container image, services, SDKs, configuration prefix, data paths, container
  labels and DNS verification records use the new namespace. This is an
  intentional clean break: compatibility aliases are not provided.
- New databases carry a RivetPanel schema-family marker. A database containing
  tables without that marker is refused before the migration ledger or any
  application table is changed; automatic conversion of earlier installations
  is not supported.

- The default Python build also installs packaged projects (`setup.py`, or a
  `pyproject.toml` with `[project]` or Poetry metadata) when there is no
  `requirements.txt`, and stops at the first failing step.
- The bot status bar keeps the status and power buttons on one row with the
  usage meters in an even grid below; unavailable power buttons are shown
  neutral instead of faded colors.
- Creating a bot from GitHub, configuring a repository, deploying and the
  deployment preview no longer require GitHub sign-in to be configured on the
  panel when the repository is public.

- **Migration `0035`** rebuilds the `sites` table: the panel-wide unique slug
  becomes unique per sites domain, and each site records its sites domain.
  Every release, custom domain and assistant chat is kept. On first start the
  host of `RIVET_SITES_BASE_URL` becomes the primary sites domain and
  existing sites are assigned to it, so every existing address stays the
  same. If that host later changes while it is still the primary domain, the
  domain and its sites follow it, as before. Back up the database before
  upgrading. A database migrated by this version is refused by older builds.
- Migrations that rebuild a table other tables reference can now run with
  foreign keys off (marker `-- rivetpanel:foreign-keys-off`). They run on a
  single connection, are checked with `PRAGMA foreign_key_check` before
  commit, and turn foreign keys back on afterwards.

### Security

- API clients never act as administrators (even when an administrator made
  them), cannot manage passwords, two-step sign-in, passkeys, sessions, linked
  accounts, keys, tokens or clients, and a client limited to some bots or
  workspaces is refused (403) everywhere else, enforced in the authentication
  middleware and again in the bot and workspace services.
- Delegated `settings.manage` holders can only make a sign-on provider give
  new accounts a role whose permissions they hold, and a role a provider uses
  cannot be deleted.
- Role permissions are enforced by the server on every API route they cover,
  including the automation API, and the console, power, files and environment
  permissions also limit access through ownership, workspace roles and
  per-bot sharing (SFTP included), so a full-access share cannot give back
  what a role removes. Only administrators can make or change
  administrators, open the environment editor or change modules. A delegated
  account manager cannot change the role, address, verified flag or enabled
  state of an account holding administration permissions it lacks, so it
  cannot take over a broader delegate by changing its address and resetting
  the password. Creating, changing, deleting and assigning roles, verifying
  addresses and email changes are recorded in the activity log.
- Email verification links store only a SHA-256 hash of a 256-bit token, work
  once, expire after 24 hours, travel in the URL fragment and are rate
  limited (one a minute and five an hour per account, plus a per-IP-address
  confirm limit).

- SFTP no longer looks at the panel's own disk for a bot or game server that
  is assigned to a remote node. Previously the SFTP folder of a remote server
  was opened on the panel host, so a stale panel-local directory with the same
  id (for example from before the server was placed on a node) could have been
  listed, read or changed. Remote servers are now served only through their
  agent, refused while the node is offline, and refused outright when the
  agents module is off; the SFTP listener also starts only after node routing
  is configured.
- Agent certificate rotation now retires the superseded certificate: once an
  agent completes a connection with its renewed certificate, every older
  certificate of that node is revoked (reason `superseded`) instead of staying
  valid until its own expiry. A renewal that was issued but never used does
  not revoke the old certificate, so an agent that stopped before saving its
  renewal can still reconnect. Unit tests now cover forged, foreign-CA,
  expired, revoked, unknown, disabled-node and mismatched agent certificates,
  cross-node server access and agent-side renewal.
- Only administrators can choose the node for a new bot or game server;
  other users' servers are always placed on the panel's own node.
- Agent enrollment consumes the token, signs the node certificate and records
  its serial in one database transaction. A signer or database failure no
  longer burns the token or leaves an issued certificate unrecorded.
- Repository names whose owner or name is only dots (`a/..`) are refused
  everywhere a GitHub API path is built.
- Logos are validated (PNG or JPEG, 16–512 px, at most 256 KiB) and every logo
  or favicon is served only to people with access, sandboxed and with
  `nosniff`.

### Fixed

- Uploading files on the Files page never finished: the upload helper could
  not handle the panel's empty 204 answer, so the first file was written but
  the page stayed at 100 %, never refreshed and never sent the remaining
  files. Every selected file is now uploaded and the list refreshes (local
  and remote servers).
- The console of a server on a remote node stopped following its output for
  good when the node's agent disconnected, even after it reconnected. It now
  says once that the node is offline, keeps waiting for the same container
  and resumes from the last line when the agent is back.
- While a remote node was offline, the Files page showed "internal server
  error" and claimed the folder was empty. The panel now passes on its own
  "the server's node is offline" message (503 answers written by the panel
  are shown; other server errors stay private), and the page no longer shows
  the empty-folder hint when the folder could not be listed. Administrators
  also see "(node offline)" next to the node name on a server's page.
- Deleting a remote game server could fail with "internal server error"
  although the server was removed: the agent's reconciler and the panel's
  purge both tore it down, and the second removal of the already deleted row
  failed. Removing the row is now idempotent on agent nodes, as on the panel.
- The Deploy tab said "GitHub deployments are not set up" for bots deployed
  from a public repository when GitHub sign-in was not configured, hiding the
  repository, history, redeploy and rollback. Public-repository deployment
  now works there without sign-in; publishing and private repositories still
  need it and say so.
- Administration → Nodes now shows the per-node CPU, memory, disk and
  running-server metrics the documentation described, including the samples
  reported by remote agents.
- A console line that was refused (for example while the server was
  restarting) left its error on screen after later lines were delivered.
- Game servers that crash repeatedly are now described as "the server"
  instead of "the bot".
- Deleting an empty folder over SFTP on a remote server could delete a file
  that another session added to it a moment earlier, because the panel
  checked emptiness and then asked the node for a recursive delete. The node
  now deletes the folder only if it is empty at that moment.
- Removing an add-on from a bot on a remote node deleted nothing (the panel
  looked for the data on its own disk), so the data stayed on the node and an
  add-on attached again later could not open it with its new password.
- A webhook push for a bot whose node was offline was recorded as a failed
  deployment and lost; it now waits for the node (see Added).
- The agent's workspace creation route now answers 400 with "invalid server
  id" for a malformed id instead of an internal server error.
- Add-on data usage is no longer read from the panel's disk for a bot on a
  remote node (the data lives on the node).
- Creating a bot with a node selected (the administrator node picker on the
  new-bot page) no longer fails with "invalid JSON body"; the API now accepts
  `node_id` when creating a bot.
- A remote runner whose local Docker daemon was healthy but whose panel
  connection was unavailable now waits before retrying instead of spinning,
  flooding logs and repeatedly resynchronizing several times per millisecond.

- **Security:** setting a password with no session to keep did not sign the
  account out anywhere, because the SQL compared against NULL
  (`token_hash != NULL`). `rivetpanel reset-password`, which documents that it
  signs the account out everywhere, now really does; the new emailed reset
  relies on it. Sessions created before the fix are not affected by past resets
  retroactively: reset again, or use "sign out other sessions", if a past reset
  was meant to end a compromise.
- After the panel restarted, bots whose containers kept running showed
  "Running since" the restart time ("just now") instead of when the container
  actually started. Adopted containers now keep Docker's recorded start time;
  uptime shown before this fix corrects itself the next time the bot starts.
- Diagnostic containers left running by an interrupted AI diagnostic run are
  now removed after the 30-minute grace period. Docker's container list does
  not report start times for running containers, so these orphans were never
  cleaned up and kept running until removed by hand.

### Operational notes

- Idle memory is unchanged: email uses the standard library only, with no queue,
  connection pool or background goroutine (measured in `docs/footprint.md`).
  Sending is capped at 20 emails per recipient and 300 per hour; at most two
  sends run at once and extras are dropped and logged, not buffered.
- No Mailgun webhooks: bounces and complaints are not tracked, and nothing is
  retried. A Mailgun sandbox domain only delivers to authorized recipients.
- Migrations `0033` (`password_resets`, `users.email_alerts`) and `0034`
  (`users.email_news`) default to on, which only matters once Mailgun is
  configured and an administrator sends something.

## 0.4.0 - 2026-10-01

### Added

- **Host page rebuilt** (Administration → Host) as four tabs. *Resources* shows
  live CPU, memory (with cache and swap), load average, disk, network and disk
  throughput, with history charts for 1 hour to 30 days (averages with the
  peaks kept as dashed lines, hover read-outs), a disk-fill forecast, the
  machine (OS, kernel, CPU, uptime), the BotForge process (memory, goroutines,
  open files, request counts and errors, database size), the Docker runner
  (version, cgroups, containers, builds, queue) and storage per location with
  filesystem and inode use. A "needs attention" list calls out a nearly full
  disk, memory pressure, a Docker problem, an overloaded CPU or recent errors.
  *Bots* lists every bot with its CPU, memory against its limit, network,
  processes and workspace size, sortable and refreshed every 10 seconds.
  *Capacity* keeps the admission budgets and now shows each limit's current
  value and where it comes from. *Panel logs* tails the panel's own log from
  memory with level filter, search, live follow, expandable fields, copy and
  download; secrets, tokens and passwords are redacted before a line is kept.
  Host samples now also record load, swap, network and disk throughput
  (migration `0032`).
- **Environment page** (Administration → Environment): every `BOTPANEL_`
  variable with its description, default and source (default, environment file
  or set here), editable from the browser. Values are checked, including the
  whole configuration, before they are stored; secrets (the metrics token) are
  stored encrypted and never shown. Changes apply after a restart; the page
  shows what is waiting, and a **Restart panel** button (exit code 75, for
  systemd or a container restart policy) appears when a supervisor is
  detected. Variables read before the database opens or able to lock the panel
  out stay in the environment file (migration `0031`). `botpanel env` and
  `botpanel env reset` list and drop the saved values from the host if a bad
  save ever has to be undone; a saved set that no longer validates is ignored
  at start and reported on the page instead of stopping the panel.
- **Go to (Ctrl+K)** now finds everything: every page, your settings, every
  administration section, the Host tabs, panel logs, environment variables,
  AI provider and research settings, documentation sections, bots and their
  sections (`bluntly files`), sites, users, workspaces, AI chats and actions
  (Ask AI, new chat, new bot, switch theme, sign out). Several words narrow
  the search, Tab switches between scopes, `>` lists actions only, and an
  empty search lists everything grouped.

### Changed

- The AI settings in Panel settings use the full width: providers on the left,
  web research on the right, a guided provider form (presets for DeepSeek,
  OpenAI, OpenRouter or a custom endpoint, required fields marked, tuning and
  pricing folded away under an **Optional** heading) and an **Optional** tag on
  every field that can be skipped.
- Administration → Host moved its budgets table to the Capacity tab; the
  `/api/v1/nodes/:id/telemetry` samples gain `load1`, swap, network and disk
  throughput fields.

### Security

- Environment edits, restarts and panel-log reads are administrator-only, and
  edits and restarts are recorded in the activity log by variable name, never
  by value.

## 0.3.0 - 2026-10-01

### Added

- The AI assistant is now one panel-wide chat. **Ask AI** (bottom right of every
  page, the sparkle button in the header, or `Ctrl+.`) opens a compact
  chat window that follows you from page to page. Each message carries what you
  were looking at, shown as a chip you can dismiss: the bot or site, the tab or
  section, and the open file. The server authorizes the bot or site id itself
  and uses its real name. Chats are private to their creator, listed under a
  history button, and a failed bot shows **Ask AI why**.
- New assistant tools: `read_logs` (the bot's recent console output),
  `build_output` (recent builds and deployments with the tail of their log),
  and `list_targets` / `focus_target` so a chat started away from any bot can
  look at one of your bots. The assistant now has a full system prompt with a
  debugging method, safety rules, its limits and the current context.
- Runtime diagnostic allowlists now include version checks, syntax checks and
  running the bot's entry file (`node index.js`, `python main.py`, …), so
  startup, import and syntax errors show up. A refused command now returns the
  list of allowed commands and points to the file and log tools, and the
  `run_diagnostic` tool description lists the allowed prefixes for the bot's
  runtime.
- The bot page uses a new layout: an identity card with **Open Studio** and
  **Adjust resources**, one row of tabs, a status strip with live CPU, memory,
  disk and network readings and Start/Restart/Stop/Kill, and a terminal-style
  console as the default **Manage** tab.

- AI Operator incident workspaces for bots and sites, with encrypted
  OpenAI-compatible provider profiles (DeepSeek preset), configurable
  Risa/SearxNG research, private retained chats, streamed tool activity,
  Approval and bounded Auto repair modes, secure environment input, atomic
  revision-checked file changes with undo, and isolated offline diagnostics.
- The first AI provider and its API key can be configured in a new optional
  **AI operator** step of the `/setup` wizard.
- Administration → Panel settings can now edit, enable/disable, delete and
  re-key AI providers with every field (chat/models paths, context size, max
  output tokens, temperature, timeout, prices), discover models from the
  provider, and test the saved web-search settings.
- `GET /api/v1/ai/conversations/:id/runs` returns a conversation's latest runs
  with their tool calls and change sets. The AI workspace uses it to restore
  the active run, pending approvals, secure-input cards, change sets and Undo
  after a reload and after a run finishes.
- Model-initiated AI file applies, diagnostics, restarts and secure
  environment input are recorded in the activity log (`ai.file_apply`,
  `ai.diagnostic`, `ai.restart`, `ai.env_input`), in Auto mode too.

- Bot Sites: every Discord bot can now create one integrated public page from
  its **Public page** tab. Page Studio controls the headline, introduction,
  theme, accent, custom HTML and custom CSS; developers may explicitly publish
  safe declarative bot widgets while raw analytics, command usage, events,
  logs, configuration and account identity remain private.
- Site file workspaces: edit HTML, CSS, JavaScript and assets with the same
  explorer/editor experience as bot files. Changes stay in a private draft
  until **Publish draft** creates and activates an immutable release, so the
  normal release history and rollback workflow still applies.
- Generated bot pages and full custom-file sites can share the same address,
  domains and release history and switch modes without deleting either body of
  work. Widget markup exposes stable `data-widget` and `kind-*` CSS hooks.
- Team workspaces. Every account now has a personal workspace, and anyone can
  create team workspaces with owner, admin, developer, and viewer roles that
  apply to every bot and site in them, on top of per-bot sharing. A sidebar
  switcher scopes the overview, Sites, and new bots to one workspace; bots can
  be moved between workspaces.
- Administrator oversight: Administration now lists every workspace with its
  owner, members, running bots, memory, and sites, and shows each workspace's
  and each account's bots, sites, and recent deployments and operations.
- Static site hosting on a separate listener (`BOTPANEL_SITES_LISTEN`,
  `BOTPANEL_SITES_BASE_URL`): publish a ZIP or a GitHub branch, keep five
  immutable releases with instant rollback, single-page-app and clean-URL
  options, and custom 404 pages. Sites are served only on their own host names,
  never on the panel's origin, and dot-files are never served.
- Custom domains for sites, served only after DNS TXT ownership verification,
  with record instructions (CNAME or A/AAAA), periodic re-checks, and an
  on-demand TLS permission endpoint for Caddy. Administrators can suspend and
  restore any site.
- Publishing bots to GitHub: create a repository (personal or organization,
  private by default) from a bot's files and link the bot to it, or push the
  current files to the linked branch. Pushes use the acting user's own GitHub
  access, honor `.gitignore`, always exclude `.env` files, dependencies and
  `.git`, only upload changed files, show every file before anything is sent,
  are recorded as operations, and are never forced.
- Appearance setting for corner style (Square, Subtle, Default, Rounded).
  Square removes rounding everywhere, including badges and avatars.
- Migrations 24–26: workspaces and membership (with a personal workspace for
  every existing account and every bot moved into its owner's), the `publish`
  operation kind, and static sites, releases, and domains.

### Changed

- The AI operator tab on bots and the AI section on site pages are gone; the
  chat replaced them. The per-bot and per-site conversation endpoints still
  work and keep their target, and `GET`/`POST /api/v1/ai/conversations` list and
  create the new target-less chats (migration 0030 rebuilds the conversation
  table; existing chats, runs, tool calls and undo snapshots are preserved).
  Messages accept an optional `context` object, and a run records its own
  `bot_id`/`site_id`.
- Auto repair now needs a bot or site in view and its approval card names that
  target; a run cannot move to another bot after it starts.
- Bot page tabs were reorganised: the former Console tab is **Manage**
  (`?tab=console` and `?tab=ai` still open it), Deployments is **Deploy**,
  Environment is **Env**, and Public page is **Page**.
- AI web-search requests honour `HTTP(S)_PROXY` (the search origin is
  administrator-trusted); public page fetches still connect directly.
- AI run limits (diagnostics, apply attempts, lifecycle actions, changed
  files and bytes, retained tool output) are now enforced per run in both
  Approval and Auto mode; a call over a limit returns a tool error to the
  model. The Auto envelope lists the covered tools and the actual limits.
- The AI operator documentation, `/docs` page and Auto envelope no longer
  claim startup, deployment or site-publish actions; the operator has no such
  tools.
- AI tool-call rows use panel UUIDs; the provider's call id is kept in the new
  `provider_call_id` column (migration `0029_ai_tool_call_ids.sql`).
- The configured search base URL may include a path prefix
  (`<base>/search`).
- Renamed the bot Analytics tab to **Public page** and made Page Studio its
  default surface. Private telemetry, command activity, events and SDK setup
  remain available under **Private insights** in that tab.
- Reworked the in-product documentation into a visual guide with feature
  diagrams, callouts, runnable payload examples, privacy boundaries and a
  dedicated Bot Sites authoring flow.
- Redesigned the Settings pages: each setting's explanation sits beside its
  controls on wide screens and above them on narrow ones, the section
  navigation stays in view while scrolling, the password form is folded until
  needed, and sessions, keys, and tokens are grouped in cards. Most settings
  pages no longer need scrolling on a desktop screen.
- The account menu is reduced to Profile, Settings and Sign out; the theme
  stays one click away in the header.
- Checkboxes and radio buttons are drawn from the theme in both states instead
  of the browser's default (which rendered a flat grey box when unchecked).
- Dark-mode glows and focus shadows now follow the chosen accent color instead
  of always being orange.
- Transferring a bot moves it into the new owner's personal workspace, so the
  previous owner does not keep access through workspace membership.
- GitHub webhook deliveries for a commit the panel itself pushed no longer
  redeploy (and restart) the bot.
- The bot header is one compact bar (back, name, state, live usage, power
  controls) that stays in view under the top bar, so most bot sections fit a
  desktop screen without scrolling.
- Files is a workbench sized to the window: a foldable explorer with an icon
  toolbar, a full-height editor with a status bar, and a full-screen mode.
- Health & alerts shows the heartbeat, probe and notification state side by
  side, explains every probe setting, exposes the success threshold, links to
  the SDK setup and the expanded health guide, and hides probe timing until a
  probe type is chosen.
- Bot Settings, Startup and Access, and the administration Sites, Host, Panel
  settings and Diagnostics pages use the full width: explanations beside
  controls, stat tiles, sign-in providers side by side, a budgets table, and
  search and filters directly above the sites table.
- Lists, notices, metric tiles and status panels follow the corner-style
  preference instead of always being square.

### Fixed

- AI diagnostics always failed: the runner's reconciler mistook each
  diagnostic container (labelled with the bot id) for a stale duplicate of the
  bot and stopped it, so every run ended with exit code 137 and no output.
  Diagnostic containers are now left alone and orphaned ones are removed on the
  next resync.
- AI diagnostics failed with "mount path must be absolute" when the panel ran
  with a relative data directory (the default for `.dev-data`); the scratch
  directory is now resolved to an absolute path.
- The assistant could not debug a crashed bot because it had no way to see
  console or build output and its only command tool refused almost everything
  with "not allowed by the runtime policy"; see the new tools and allowlists
  under Added.
- `node --eval=…` and `--print=…` were not recognised as interpreter evaluation
  flags, and `python -c` was refused even after `-m pytest`.
- Every real AI run failed on its first provider request: tools without
  parameters were sent with `"required": null`, which DeepSeek (and other
  strict providers) reject. Found by testing against the live API.
- A provider's invalid-request reply (unknown model, rejected parameter) is
  now shown to the user instead of a generic failure.
- **Test search** reports a failing search service as a 502 with its reason
  instead of an internal error; a search key refused with 401/403 is skipped
  in favour of the next configured key.
- AI chat Markdown renders bold and italics, tables that follow a paragraph
  line, and level 4–6 headings; the transcript and run inspector scroll
  inside the workspace instead of growing the page.
- Saving AI web-research settings failed with "invalid JSON body" because the
  form sent back the read-only `key_count`/`key_set` fields, so search stayed
  disabled. The form now sends only its inputs, shows its own errors, and
  **Test search** waits until changes are saved (it tests saved settings).
- AI runs no longer fail with a storage error when a provider reuses a tool
  call id (such as `call_0`) across runs.
- An approval decided right after its card appeared could be lost, leaving
  the run waiting; decided approvals are no longer written back as pending
  when the run finishes. Deciding a call whose run is no longer waiting now
  returns a conflict.
- Finished AI run event streams are dropped from memory after five minutes.
- A data race between starting an AI run and returning it.
- Bot lists order bots created in the same millisecond deterministically
  (by name, then id), which also fixes a flaky test.
- A failed AI run now ends its live stream with an error event, so the
  workspace no longer shows the run as still working.
- AI provider error codes (for example rate-limit or authentication codes)
  are now read from provider responses instead of being lost.
- The session cap could evict the newest session instead of the oldest when
  several sessions were created in the same millisecond.
- The OAuth provider setup steps (administration settings and the setup
  wizard) no longer scroll sideways on phones.
- The workspace switcher in the collapsed sidebar opens beside the rail above
  the page instead of underneath page content, and row menus in rounded or
  scrolling lists are no longer clipped.
- `go.mod` now lists `golang.org/x/net`, `golang.org/x/mod`,
  `github.com/pkg/sftp`, and `github.com/docker/go-connections` as direct
  requirements.

### Deprecated

- `GET`/`POST /api/v1/bots/:id/ai/conversations` and
  `/api/v1/sites/:sid/ai/conversations` are superseded by
  `/api/v1/ai/conversations` with a message `context`. They keep working and
  are not scheduled for removal yet.

### Security

- Change-set diffs shown for AI file changes redact configured secret values;
  Undo still restores the exact original bytes.
- AI web research validates the address of every connection at dial time,
  covering DNS rebinding and redirects, and now also blocks carrier-grade NAT
  (`100.64.0.0/10`), reserved, NAT64 and 6to4 ranges. Research connections no
  longer go through an environment proxy. The administrator-configured search
  origin may be private (self-hosted SearxNG); page fetches stay public-only
  and search requests never follow a redirect to another origin.
- AI tools reauthorize every action, redact configured secret values, exclude
  credential paths, reject SSRF and shell execution, and run diagnostics in
  restricted Docker containers mounting only a private safe snapshot. The
  model receives environment names but never their values.

- Author HTML, CSS and scripts for generated bot pages execute only on the
  separate Sites origin. The panel preview is a cross-origin frame, and public
  widget publication is off by default and never includes private analytics.
- Site files never share the panel's origin; configurations where the panel
  could be addressed as a site host are refused at startup, and in production
  the panel's host may not be the sites domain itself.
- Custom domains cannot be the panel's host or its subdomains, and an
  unverified claim never reserves a domain.
- Workspace and site access checks are enforced by the application on every
  request; non-members receive `404` so identifiers cannot be probed.
- Hosted site files are not part of `botpanel backup` yet; back up
  `BOTPANEL_SITES_DIR` separately.

## 0.2.0 - 2026-10-01

### Added

- Added an in-house, searchable `/docs` experience and changed the product's
  Documentation link to stay inside BotForge.
- Expanded bot dashboards with groups/tabs, responsive spans, minimum heights,
  TTL expiry, stale states, safe Markdown, images, logs, code, key/value data,
  gauges, heatmaps, donuts, sparklines, and line/area charts.
- Added widget lifecycle controls: telemetry unpublish, an authenticated delete
  endpoint, and a dashboard Remove action.
- Added Discord identity reporting to the JavaScript and Python SDKs so a
  connected bot's Discord avatar appears in Fleet and Favorites, with runtime
  initials retained as the fallback.
- Added an opt-in, bearer-protected Prometheus `/metrics` endpoint for panel,
  database, aggregate bot, node, and health-probe measurements.
- Added per-bot TCP and HTTP health probes with startup grace, thresholds,
  loopback-only targets, persistent health state, and guarded restart-on-unhealthy.
- Added database migrations 21–23 for widget layouts/lifecycle, Discord
  identity, and health-probe configuration.
- Added a single authoritative `VERSION` file, synchronized frontend package
  metadata, and an automated consistency check (`make version-check`).

### Changed

- Increased widget updates per telemetry push from 24 to 48 and active widgets
  returned per bot to 240.
- Replaced silent widget coercion with field- and kind-specific validation
  errors, and corrected telemetry decode errors to list widgets, unpublish, and
  identity fields.
- Updated all SDK caps and added full widget layout/lifecycle helpers to the
  JavaScript and Python SDKs.
- Documented the metrics endpoint, widget schemas, health probes, and their
  operational and security boundaries.

### Security

- Discord bot tokens are not decrypted or reused for avatar lookup. Updated
  SDKs report a strictly validated Discord snowflake and CDN avatar URL only
  while their Discord client is ready.
- HTTP probes ignore environment proxies, reject redirects, and can only probe
  an existing published TCP port on `127.0.0.1`.
- Metrics are disabled unless configured with a token of at least 24 characters;
  bearer comparison is constant-time and metric labels omit bot/user identity.

### Known gaps

- The panel still directly owns the root-equivalent Docker socket; the planned
  privileged botrunner boundary has not been implemented.
- Egress, workspace disk/IO, and bandwidth policies are not kernel-enforced.
- Multi-node scheduling, PostgreSQL/PITR, external log shipping, SMTP,
  config-as-code, and the governance work remain planned. See
  `docs/implementation-status.md` for the complete checklist.

## 0.1.0 - 2026-10-01

### Added

- Initial BotForge release: single-host bot lifecycle management, six runtime
  recipes, hardened Docker containers, embedded Svelte interface, SQLite state,
  encrypted secrets, live console, deployments, backups, SFTP, OAuth, sharing,
  schedules, telemetry, and the automation API.
