---
title: Game server administration
lede: Manage server types and their revisions, write your own blueprints, and know which game server controls are enforced and where.
icon: cube
audience: admin
glance:
  - Built-in types stored as immutable revisions
  - Disk, bandwidth and egress are not limited
related: [game-servers/pterodactyl-eggs, game-servers/ports, operate/security]
---

## Server types

**Administration → Server types** lists every server type (blueprint) with its
revision and the number of servers using it. You need `blueprints.manage`.

From there administrators can:

- hide a type from new servers
- export its exact YAML
- import custom types, or
  [convert a Pterodactyl egg](03-pterodactyl-eggs.md)

### Revisions

- Built-in types are stored as immutable **revisions**. When a RivetPanel
  update changes one, a new revision is added.
- Existing servers keep the revision they pinned until someone presses
  **Update to revision N** on their **Configuration** tab.
- Custom imports cannot take over a built-in slug [enforced: panel].
  Importing the same slug again adds a revision.
- Only unused custom types can be deleted.

## Blueprint format

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
  jvm_args: false        # true: offer "JVM arguments"; needs {{SERVER_JVM_ARGS}} in command
  jvm_args_default: ""   # options new servers start with (validated like user input)
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

### Variables and the startup command

`{{VAR}}` in the startup command becomes the shell expansion `${VAR}`. Values
reach the server through its environment and are never re-parsed as shell
code [enforced: panel].

The panel provides these variables. Blueprints cannot declare them, or any
`RIVET_` name [enforced: panel].

| Variable | Value |
|---|---|
| `SERVER_MEMORY` | Memory for the server process in MiB (the Java heap for Java types) |
| `SERVER_PORT` | The primary port |
| `SERVER_IP` | The address the server binds inside its container (`0.0.0.0`) |
| `SERVER_ID` | The server's UUID |
| `SERVER_JVM_ARGS` | Extra JVM options (blueprints with `startup.jvm_args` only) |
| `SERVER_PORT_1` … `SERVER_PORT_10` | Additional allocations, in ascending port order |

- `startup.jvm_args: true` and an unquoted `{{SERVER_JVM_ARGS}}` in
  `startup.command` are required together. `startup.jvm_args_default` is
  optional.
- `ports.contiguous` asks automatic allocation for consecutive ports, for
  games that derive a query port from the game port. Ports people add later
  are not forced to be consecutive.
- The install script receives the server's declared variables and the
  panel-provided ones in its environment, but not the hidden agreement
  records.
- Paths must stay inside the server's files [enforced: panel].
- Version providers are `minecraft-vanilla`, `papermc` (`paper`, `folia`,
  `velocity`, `waterfall`), `purpur`, `fabric`, `forge` and `neoforge`.

## Enforcement boundaries

| Layer | What it enforces |
|---|---|
| Application | Blueprint validation, variable rules, the EULA requirement, allocation ownership and conflicts, stopped-only changes, JVM argument validation (on save and before each start), checksum verification of downloads, and every permission check [enforced: panel] |
| Docker runtime | Memory, CPU and PID limits, the non-root user, dropped capabilities, `no-new-privileges` and the read-only root file system (the server writes only to its files and a small `/tmp`), exactly as for bots. Install scripts run with the same hardening and internet access [enforced: docker] |
| SteamCMD downloads | A time limit enforced by the runner, which removes the install container, and a size watchdog inside the install container [enforced: panel] |
| Not enforced | Disk quotas (a world can fill the disk), bandwidth, and egress filtering [not enforced] |

> [!WARNING]
> Published ports bind on the allocation's address, `0.0.0.0` by default, so a
> server is reachable from every network the host is on. RivetPanel does not
> add firewall rules.

### Not implemented

- Minecraft Java and three SteamCMD games ship today. Bedrock, mounts, FastDL,
  subdomains, firewall rules and lifecycle hooks are not implemented.
- The egg importer does not map `yaml`, `json` or `xml` config parsers.
- Modrinth is the only mod and plugin source: no CurseForge or Spigot
  resources.

Game servers can run on an enrolled remote node. There, the node's agent checks
that a port can be bound before it is allocated automatically or the server
starts. That is a check, not a reservation. See
[Remote nodes](../07-operate/04-remote-nodes.md).

## Game icons

The web app bundles a few game icons:

| Icons | Source | Licence |
|---|---|---|
| Minecraft, Fabric, Valheim, Steam | [Dashboard Icons](https://dashboardicons.com) by Homarr Labs (`homarr-labs/dashboard-icons`) | Apache License 2.0 |
| Paper, Folia, Velocity, Purpur | [selfh.st/icons](https://selfh.st/icons), also listed on dashboardicons.com | Creative Commons Attribution 4.0 |

They are kept in `web/src/lib/assets/games/` with both licence texts and a
`NOTICE` that lists each file's source. Neither set has icons for Forge,
NeoForge, Rust (the game) or Project Zomboid, so those use the Minecraft or
Steam icon of their family. Other and imported types show a drawn gamepad.

The mapping is by server type slug in `web/src/lib/gameIcons.ts`. Game names
and logos belong to their owners and are used only to identify the games.

## API

| Request | Effect |
| --- | --- |
| `GET /api/v1/blueprints` | Server types offered for new servers (administrators also see hidden ones) |
| `GET /api/v1/blueprints/{slug}/versions/{env}` | The provider's version list for a variable |
| `POST /api/v1/games` | Create: `name`, `blueprint`, `variables`, `image`, `memory_bytes`, `nano_cpus`, `agreements`, `workspace_id` |
| `GET /api/v1/bots/{id}/game` | Server type, variables with values, startup command, update availability |
| `PUT /api/v1/bots/{id}/game/variables` | Change settings; a running server applies them at its next restart, and a setting that reinstalls needs a stopped server |
| `PUT /api/v1/bots/{id}/game/jvm-args` | Set `args` (extra JVM options, `""` = none); applies at the next start; returns `bot` and `jvm` |
| `POST /api/v1/bots/{id}/game/reinstall` | Install again on the next start |
| `PUT /api/v1/bots/{id}/game/image` | Choose a Java image (`""` = automatic) |
| `POST /api/v1/bots/{id}/game/upgrade` | Move to the type's newest revision |
| `GET /api/v1/bots/{id}/game/query` | Players, version and message of the day |
| `POST /api/v1/bots/{id}/command` | Send one console line (start/stop permission) |
| `GET /api/v1/bots/{id}/game/addons?q=` | Search Modrinth for fitting mods or plugins |
| `GET /api/v1/bots/{id}/game/addons/{project}/versions` | Fitting versions of a project |
| `POST /api/v1/bots/{id}/game/addons` | Install `project` (newest fitting) or a `version` |
| `POST/DELETE /api/v1/bots/{id}/allocations[/{aid}]`, `PUT …/{aid}/primary` | Manage the server's ports |
| `POST /api/v1/bots/{id}/allocations/pick-free` | Move the primary allocation to the next free port (stopped, or waiting after a port conflict) |
| `GET /api/v1/game-hosts` | The nodes' player addresses only |
| `GET/POST /api/v1/admin/allocations`, `PATCH/DELETE …/{aid}` | Allocation pool (administrators); `POST` also returns `skipped: [{port, reason}]` for ports in use |
| `POST /api/v1/admin/blueprints`, `GET …/{slug}/export`, `…/revisions`, `PATCH/DELETE …/{id}` | Server types (administrators) |
| `POST /api/v1/admin/blueprints/egg-preview` | Convert `egg` (Pterodactyl egg JSON text) into a draft: `yaml`, `warnings`, `error`; stores nothing |

`GET …/game` also returns the JVM state under `jvm`: `supported`, `args`,
`java`, `heap_mib`, `presets` and `pending_restart`.

Game servers appear in `GET /api/v1/bots` with `"kind": "game"`. Power, files,
console, backups and schedules use the same `/api/v1/bots/{id}/…` routes as
bots.
