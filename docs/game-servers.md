# Game servers (preview)

RivetPanel hosts Minecraft Java servers and a few Steam dedicated servers
(installed with SteamCMD) next to Discord bots. A game server is
the same kind of managed container as a bot: it has the same console, files,
SFTP, backups, schedules, sharing, workspaces and resource limits. What is
different is where its startup comes from. A bot runs your own code with a
language runtime; a game server is defined by a **server type** (a blueprint)
that knows how to download, install, configure and start the game.

The `game_servers` module is on by default as a preview. Turn it off with
`RIVET_MODULES=game_servers=off`.

## Server types

| Type | What it is | Install |
| --- | --- | --- |
| Paper | High-performance server for Bukkit, Spigot and Paper plugins | PaperMC download, SHA-256 verified |
| Purpur | Paper fork with extra options | Purpur download, MD5 verified |
| Vanilla | Mojang's official server | Mojang download, SHA-1 verified |
| Fabric | Lightweight mod loader | Fabric server launcher (TLS only; Fabric publishes no checksum) |
| Forge | Classic mod loader | Installer from Forge Maven (SHA-1), run in a container |
| NeoForge | Modern Forge fork | Installer from NeoForged Maven (SHA-1), run in a container |
| Folia | Multi-threaded Paper fork for very large servers | PaperMC download, SHA-256 verified |
| Velocity | Proxy linking several servers into one network | PaperMC download, SHA-256 verified |
| Valheim | Valheim dedicated server (Steam app 896660) | SteamCMD, anonymous login |
| Rust | Rust dedicated server (Steam app 258550) | SteamCMD, anonymous login |
| Project Zomboid | Project Zomboid dedicated server (Steam app 380870) | SteamCMD, anonymous login |

Each Minecraft type offers a version list from its provider. "latest" picks
the newest stable release when the server is installed. Steam types install
the current public build (or a beta branch you name).

## Steam games (SteamCMD)

The Steam types are written by RivetPanel from each game's public dedicated
server documentation. On the first start (and on Reinstall, or when the beta
branch changes) the panel runs SteamCMD in a separate install container
(`steamcmd/steamcmd:latest`, the same hardening as every install container,
with internet access) that downloads or updates the app into the server's
files:

* **Anonymous login only.** No Steam account, password or Steam Guard code is
  ever asked for or stored, so games that need a licensed account to download
  (for example ARK: Survival Ascended, whose server is Windows-only anyway)
  cannot be offered. Counter-Strike 2 downloads anonymously but needs 35+ GB
  and a game server login token to be listed, so it is not a built-in type;
  administrators can still write a blueprint for it.
* **Beta branches** are a variable ("Steam beta branch"). Only plain branch
  names are accepted (letters, digits, `.`, `_`, `-`); password-protected
  branches are not supported.
* **Bounded**: the download has its own time limit (60 minutes by default,
  `timeout_minutes`, at most 240) and a size watchdog (`max_size_gb`) that
  stops SteamCMD when the server's files grow beyond it. The watchdog runs
  inside the install container; it is **not** a disk quota.
* SteamCMD keeps its own client files in `.steamcmd` in the server's files so
  later updates are faster, and `steamclient.so` is copied to
  `.steam/sdk64` / `.steam/sdk32` where dedicated servers look for it. The
  installed Steam build id is shown as the installed version.
* The servers run in the same image (Ubuntu with SteamCMD's 32-bit
  libraries). Valheim's optional crossplay library needs PulseAudio and
  libatomic, which this image does not have, so the blueprint does not offer
  crossplay.

Per game:

* **Valheim**: game port plus the next port (Steam query); ports are allocated
  as a consecutive pair. Set a server password of at least 5 characters (it
  must not appear in the server name) before the first start — Valheim does
  not start without one. Stop sends SIGINT, which saves the world (verified
  with a real server). Valheim answered no Steam queries while unlisted
  (`-public 0`) in our test, so the panel shows players only for listed
  servers.
* **Rust**: game port plus a Steam query port. RCON and the Rust+ companion
  port are not published. Stop sends SIGINT. Rust needs a lot of memory (the
  blueprint asks for at least 8 GiB) and about 10 GB of disk.
* **Project Zomboid**: two consecutive UDP ports (`-port`/`-udpport`). The
  admin password is used when the server creates the `admin` account on its
  first start; if left empty the server's ID is used, so change it in game.
  `servertest.ini` (name, players, public listing, ports) and the Java heap in
  `ProjectZomboid64.json` are kept in sync with the settings before every
  start. Stop types `quit` in the console.

**Terms.** Downloading and running these servers is subject to Valve's Steam
Subscriber Agreement and to each game's own terms (Iron Gate/Coffee Stain for
Valheim, Facepunch for Rust, The Indie Stone for Project Zomboid). RivetPanel
does not ask people to accept them in the panel; the operator is responsible
for complying (for example with Rust's server owner rules on monetisation).

Only the Valheim download, start, Steam query attempt and SIGINT stop were
tried against the real game during development (by hand in Docker with the
blueprint's command line, not through a full panel install); Rust and Project Zomboid are
written from their documentation and were not run (their downloads are 10 GB
and several GB). The SteamCMD install path itself is covered by a real-Docker
integration test with the small Steamworks redistributable app
(`RIVET_TEST_STEAM=1`).

## Importing Pterodactyl eggs

Administration → Server types → **Import Pterodactyl egg** converts an egg
export (`PTDL_v1` or `PTDL_v2` JSON, at most 512 KiB) into a blueprint draft.
Nothing is saved until an administrator has reviewed the draft and its
warnings, edited the YAML if needed and pressed Save; the draft is then stored
through the normal blueprint import (same validation, immutable revisions,
`blueprints.manage`). An egg is treated as untrusted input:

* it must be exactly one JSON object within the size limit; unknown top-level
  fields are reported, never silently dropped;
* nothing it references is fetched (no update URL, no image pull, no script
  run) while converting;
* its install script only ever runs later, like any blueprint script: in the
  install container, as the unprivileged server user (not root, unlike
  Pterodactyl), with the usual hardening.

How it maps:

| Egg | Blueprint |
| --- | --- |
| `docker_images` (or v1 `image`) | `images` (invalid references skipped, at most 12; "Java N" labels set the Java version) |
| `startup` with `{{VAR}}` | `startup.command` (same syntax); `{{server.build.default.port}}` and friends are mapped, unknown ones removed with a warning |
| `config.stop` | `startup.stop`, or `stop_signal: SIGINT` for `^C` |
| `config.startup.done` | `startup.done` (first marker only) |
| `variables` | `variables`: env name, name, description, default, `user_editable` → `editable`; Laravel rules `required`, `nullable`, `string`, `integer`, `numeric`, `boolean`, `in:`, `min:`/`max:`/`between:`, `regex:` (RE2-compatible only), `alpha_dash`, `alpha_num`, `url` are mapped |
| `scripts.installation` | `install.script` run with the egg's shell (`bash`/`sh`), `install.image` from the egg's container; `/mnt/server` becomes `/workspace` |
| `config.files` parsers `properties`, `ini`, `file` | `config_files` formats `properties`, `ini` (one entry per section), `lines` |
| `features: [eula]` | a Minecraft EULA agreement |

Reported as warnings and not imported: `yaml`, `json` and `xml` config
parsers, find-and-replace objects, placeholders RivetPanel does not provide,
hidden (`user_viewable: false`) variables (RivetPanel shows every variable on
the Startup page), other features (`java_version`, `pid_limit`, …),
`file_denylist`, `userInteraction`, package-manager calls in install scripts
(they need root), and variables whose names are not `UPPER_SNAKE_CASE` or are
provided by the panel. Eggs declare no resources or ports, so the draft uses
2 GiB, 2 CPUs and one automatic port; adjust them before saving.

## Creating and running a server

Game servers → New server: choose the type, the version, memory and CPU, and
accept the Minecraft EULA (required for every Minecraft type except the proxy;
the acceptance is stored with the server and written to `eula.txt`).

The first start **installs** the server:

1. The panel resolves the version with the provider and downloads the server
   software into the server's files. The download streams into a temporary
   file and is committed only when its size and published checksum match.
2. **Automatic Java**: unless you picked a Java image yourself, the panel looks
   up the Java version the Minecraft release needs (Mojang's version metadata)
   and picks the matching image, for example Java 17 for 1.20.1 and Java 25 for
   26.x.
3. Forge and NeoForge then run their installer in a container (the server's
   own Java image, as the unprivileged server user, with internet access).
4. The installation is recorded as a `build` operation with its output, shown
   while the server says **Installing**.

Later starts skip the installation. Changing the version (or any setting marked
"reinstall") or pressing **Reinstall** installs again on the next start;
worlds, plugins, mods and settings in the server's files are kept.

Before every start the panel writes `server.properties` keys `server-port`,
`query.port` and `server-ip` for the primary allocation and re-writes
`eula.txt`. Other settings in your files are left alone.

**Stopping** sends the type's stop command (`stop`, or `end` for Velocity) to
the console and waits up to its stop timeout (90 s for Paper) so the world is
saved, then falls back to the regular SIGTERM/SIGKILL stop. **Kill** still
ends the process immediately.

The console sends each line to the server process, so Minecraft commands work
without a leading slash.

## Memory

`SERVER_MEMORY` (the Java heap, `-Xmx`) is 85% of the memory limit by default
(80% for Velocity). The rest is left for the JVM itself, threads and native
memory, so the container limit is not exceeded by a correctly sized heap.

## Allocations and ports

An allocation is an IP:port reservation on a node. Each server has a
**primary** allocation, published for TCP and UDP and passed to the server as
`SERVER_PORT`; extra allocations are for plugins such as voice chat or web
maps.

* New servers take free ports from the node's pool (Administration →
  Allocations). When the pool is empty, the panel picks the next port, starting
  at the type's default (25565 for Minecraft, 25577 for Velocity), that is not
  used by another allocation, a published bot port, or any other program on
  the host, and adds it to the pool.
* Administrators add ranges (`25565-25600, 19132`), set the address players
  see (for example `play.example.com`), and delete free ports.
* Server owners add ports, choose the primary one and remove extras on the
  server's Network tab, while it is stopped.
* RivetPanel does not configure your router or firewall: forward the primary
  port to the host to reach the server from the internet.

## Players, properties, mods and plugins

Server types that support it add three pages to a server:

* **Players**: who is online (from the server-list ping), operators, the
  whitelist and bans read from `ops.json`, `whitelist.json` and
  `banned-players.json`, with buttons that send `op`, `deop`, `kick`, `ban`,
  `pardon` and `whitelist …` through the console. Actions need the start/stop
  permission; the lists need file access. Player names are checked (1–16
  letters, digits or underscores) before a command is sent.
* **Properties**: the most-used `server.properties` settings (MOTD, maximum
  players, online mode, whitelist, game mode, difficulty, PvP, world, view
  distance …). Saving replaces those keys in place and keeps comments, order
  and every other key; a concurrent change by the running server is detected
  (the save is refused and you reload). The server reads the file at start.
* **Plugins** (Paper, Purpur, Folia, Velocity) or **Mods** (Fabric, Forge,
  NeoForge): search Modrinth for projects that support the server's loader
  and installed Minecraft version, install the newest fitting version into
  `plugins/` or `mods/`, and remove installed jars. The panel checks that the
  chosen version supports the loader, downloads only from `cdn.modrinth.com`,
  verifies the published SHA-512 before the file is committed, and lists
  required dependencies to install next. A restart loads new files.

Servers created before a server type gained these pages get them after
"Update" on their Startup page (the new revision).

## Worlds and archives

The file manager can download any folder as a zip (for example `world`),
compress files or folders into a zip next to them, and extract `.zip`,
`.mrpack`, `.tar.gz`, `.tgz` and `.tar` archives that are already in the
server's files (upload a world as one archive, then **Extract here**).
Extraction is staged so a rejected archive changes nothing; links, absolute and
`..` paths are refused; at most 200,000 entries, 8 GiB in total, 4 GiB per
file, and zip entries expanding more than 200x are rejected. SFTP works for
very large worlds.

## Console commands and task chains

`POST /api/v1/bots/{id}/command` sends one line to a running server (or bot)
console; it needs the start/stop permission, like typing in the console.

Schedules can be **task chains**: up to 20 steps, each a console command,
start, stop, restart, kill or backup, with a wait of 0–3600 seconds before
it (all waits together at most one hour). A failed step stops the chain unless
it is marked "continue on failure". Presets include a warned restart (two chat
warnings, `save-all`, restart) and save-then-backup. Chains run in the
background, one run per schedule at a time; each step re-checks the
creator's current permission.

## Status

While a server runs, the panel asks it for its status every 10–15 seconds and
shows players, the version and the message of the day: Minecraft types answer
the standard server-list ping, Steam types the Steam A2S_INFO query (players,
server name, map and version) on their query port. A server that does not
answer is shown as starting. The Steam query is only sent for servers on the
panel's own node; for a server on a remote node it is not available yet (the
agent relays only the Minecraft ping).

## Server types for administrators

Administration → Server types lists every blueprint with its revision and the
number of servers using it. Administrators can hide a type from new servers,
export its exact YAML, and import custom types.

* Built-in types are stored as immutable **revisions**. When a RivetPanel
  update changes one, a new revision is added; existing servers keep the
  revision they pinned until someone presses "Update" on their Startup page.
* Custom imports cannot take over a built-in slug. Importing the same slug
  again adds a revision. Only unused custom types can be deleted.

### Blueprint format

```yaml
slug: my-game            # lowercase letters, digits and dashes
name: My game
category: Tests
description: What it is.
runtime: java            # optional compatibility label (default go); unused by game servers
images:
  - {label: "Java 21", ref: "eclipse-temurin:21-jre-alpine", java: 21}
java_from: GAME_VERSION  # optional: a variable holding a Minecraft version
startup:
  command: java -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}}
  stop: stop             # console command for a graceful stop ("" = SIGTERM)
  stop_signal: ""        # or SIGINT/SIGTERM/SIGQUIT/SIGHUP instead of a command
  stop_timeout_seconds: 60
  done: 'Done ('         # console text meaning "started" (informational)
variables:
  - env: SERVER_JARFILE
    name: Server jar
    default: server.jar
    editable: true
    reinstall: false     # true: changing it reinstalls on the next start
    rules: {type: string, pattern: '^[a-z.]+$'}   # string | int | bool | enum
    versions: {provider: papermc, project: paper} # optional version list
install:
  download: {provider: papermc, project: paper, version: "{{GAME_VERSION}}", dest: "{{SERVER_JARFILE}}"}
  steamcmd:              # optional: anonymous SteamCMD download before the script
    app_id: 896660
    beta: "{{STEAM_BRANCH}}"   # optional branch name template
    validate: false
    timeout_minutes: 60
    max_size_gb: 40
  image: ""              # install container image ("" = the server's image)
  script: |              # optional, runs with /bin/sh in /workspace
    echo installing
config_files:
  - path: server.properties
    format: properties   # properties or ini (set keys; ini takes a section) or lines (regex replace)
    create: true
    set: {server-port: "{{SERVER_PORT}}"}
agreements:
  - {id: my-eula, text: I accept the EULA., url: "https://…", file: eula.txt, content: "eula=true\n"}
resources: {memory_mb: 2048, min_memory_mb: 512, cpus: 2, pids: 1024, heap_percent: 85}
ports: {default: 25565, extra: 0, container: 0, contiguous: false}
query: minecraft-java    # minecraft-java, steam (A2S) or empty
query_allocation: 0      # 0 = primary port, n = SERVER_PORT_<n>
features: [minecraft-properties, minecraft-players]   # optional pages
addons: {source: modrinth, kind: plugin, loaders: [paper, spigot, bukkit], dir: plugins}
```

`{{VAR}}` in the startup command becomes the shell expansion `${VAR}`: values
reach the server through its environment and are never re-parsed as shell
code. The panel provides `SERVER_MEMORY`, `SERVER_PORT`, `SERVER_IP`,
`SERVER_ID` and `SERVER_PORT_1` … `SERVER_PORT_10` (the additional
allocations in ascending port order); blueprints cannot declare those or any
`RIVET_` name. `ports.contiguous` asks automatic allocation for consecutive
ports (for games that derive a query port from the game port); ports people
add later are not forced to be consecutive. The install script receives the
server's declared variables and the panel-provided ones in its environment
(not the hidden agreement records). Paths must
stay inside the server's files. Providers are `minecraft-vanilla`, `papermc`
(`paper`, `folia`, `velocity`, `waterfall`), `purpur`, `fabric`, `forge` and
`neoforge`.

## Enforcement boundaries

* **Application level**: blueprint validation, variable rules, the EULA
  requirement, allocation ownership and conflicts, stopped-only changes,
  checksum verification of downloads and every permission check.
* **Docker runtime level**: memory, CPU and PID limits, the non-root user,
  dropped capabilities, `no-new-privileges` and the read-only root file system
  (the server writes only to its files and a small `/tmp`), exactly as for
  bots. Install scripts run with the same hardening and internet access.
* **Not enforced**: disk quotas (a world can fill the disk), bandwidth, and
  egress filtering. Published ports bind on the allocation's address (0.0.0.0
  by default), so the server is reachable from every network the host is on.
* SteamCMD downloads are bounded by a time limit (enforced by the runner,
  which removes the install container) and a size watchdog inside the
  install container (application level, not a disk quota). The stop signal
  is sent by the runner through Docker.
* Minecraft Java and three SteamCMD games ship today. Bedrock, mounts,
  FastDL, subdomains, firewall rules and lifecycle hooks are not
  implemented; the egg importer does not map yaml/json/xml config parsers. Modrinth is the only mod/plugin source
  (no CurseForge or Spigot resources). Game servers can run on an enrolled
  remote node; there the node's agent checks that a port can be bound before
  it is allocated automatically or the server starts (a check, not a
  reservation, see `docs/agents.md`).

## API

| Request | Effect |
| --- | --- |
| `GET /api/v1/blueprints` | Server types offered for new servers (administrators also see hidden ones) |
| `GET /api/v1/blueprints/{slug}/versions/{env}` | The provider's version list for a variable |
| `POST /api/v1/games` | Create: `name`, `blueprint`, `variables`, `image`, `memory_bytes`, `nano_cpus`, `agreements`, `workspace_id` |
| `GET /api/v1/bots/{id}/game` | Server type, variables with values, startup command, update availability |
| `PUT /api/v1/bots/{id}/game/variables` | Change settings (stopped servers) |
| `POST /api/v1/bots/{id}/game/reinstall` | Install again on the next start |
| `PUT /api/v1/bots/{id}/game/image` | Choose a Java image (`""` = automatic) |
| `POST /api/v1/bots/{id}/game/upgrade` | Move to the type's newest revision |
| `GET /api/v1/bots/{id}/game/query` | Players, version and message of the day |
| `POST /api/v1/bots/{id}/command` | Send one console line (start/stop permission) |
| `GET /api/v1/bots/{id}/game/addons?q=` | Search Modrinth for fitting mods or plugins |
| `GET /api/v1/bots/{id}/game/addons/{project}/versions` | Fitting versions of a project |
| `POST /api/v1/bots/{id}/game/addons` | Install `project` (newest fitting) or a `version` |
| `POST/DELETE /api/v1/bots/{id}/allocations[/{aid}]`, `PUT …/{aid}/primary` | Manage the server's ports |
| `GET/POST /api/v1/admin/allocations`, `PATCH/DELETE …/{aid}` | Allocation pool (administrators) |
| `POST /api/v1/admin/blueprints`, `GET …/{slug}/export`, `…/revisions`, `PATCH/DELETE …/{id}` | Server types (administrators) |
| `POST /api/v1/admin/blueprints/egg-preview` | Convert `egg` (Pterodactyl egg JSON text) into a draft: `yaml`, `warnings`, `error`; stores nothing |

Game servers appear in `GET /api/v1/bots` with `"kind": "game"`; power, files,
console, backups and schedules use the same `/api/v1/bots/{id}/…` routes as
bots.
