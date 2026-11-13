---
title: Game servers
lede: Create a Minecraft or Steam game server in four steps, then run it with the same console, files and backups as a bot.
icon: gamepad
aliases: [game-servers]
glance:
  - 11 built-in server types
  - 4-step create flow
  - Status read every 10–15 seconds
related: [game-servers/ports, game-servers/steam, game-servers/memory-and-jvm, observe/health]
---

## What a game server is

A game server is the same kind of managed container as a bot. It has the same
console, files, SFTP, backups, schedules, sharing, workspaces and resource
limits.

What differs is where its startup comes from. A bot runs your own code with a
language runtime. A game server is defined by a **server type** (a blueprint)
that knows how to download, install, configure and start the game.

Game servers are a preview module, on by default. Administrators turn it off
under **Administration → Modules**, or with `RIVET_MODULES=game_servers=off`.

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

Each Minecraft type offers a version list from its provider. *Latest stable*
picks the newest stable release when the server is installed. Steam types
install the current public build, or a beta branch you name; see
[Steam games](02-steam.md).

## Create a server

**New → Game server** offers **Minecraft server**, **Steam game server** and
**Browse all server types**. The game server list under **Apps** links to the
same flow. It has four steps:

1. **Game**: pick the game from searchable, grouped cards with game icons.
2. **Version**: choose the version (latest stable by default; pre-releases and
   snapshots on request) and the game's own settings.
3. **Server**: set the name, memory (with presets and the type's suggestion)
   and CPU, plus the workspace and node when there is a choice.
4. **Review**: accept the licence terms the game requires, then create.

Every Minecraft type except the Velocity proxy requires the Minecraft EULA
[enforced: panel]. The acceptance is stored with the server and written to
`eula.txt`.

### Memory and CPU follow the node

The largest memory and CPU offered are the chosen node's own memory and CPU
count, capped further by `RIVET_MAX_BOT_MEMORY_BYTES` and
`RIVET_MAX_BOT_NANO_CPUS` when they are set. Choosing another node re-applies
the server type's suggested size within that node's limits.

Saving a larger value is refused with the node's range [enforced: panel]. A
server saved earlier with more CPUs than its node has starts with the node's
CPU count instead of failing, because Docker refuses a CPU limit above the
host's.

## The first start installs the server

1. The panel resolves the version with the provider and downloads the server
   software into the server's files. The download streams into a temporary
   file and is committed only when its size and published checksum match
   [enforced: panel].
2. Unless you chose a Java image yourself, the panel picks the image with the
   lowest Java version that runs the server. See
   [Java version](07-memory-and-jvm.md#java-version).
3. Forge and NeoForge then run their installer in a container: the server's
   own Java image, as the unprivileged server user, with internet access.
4. The installation is recorded as a `build` operation with its output, shown
   while the server says **Installing**.

Later starts skip the installation. Steam types install with SteamCMD instead;
see [Steam games](02-steam.md).

### Change the version or reinstall

The **Configuration** tab lists the server type, the startup command and the
variables. Changing the version, or any setting marked "reinstall", installs
the server software again on the next start. **Reinstall** does the same on
demand. Worlds, plugins, mods and settings in the server's files are kept.

### What the panel writes before every start

Before every start the panel writes the `server.properties` keys
`server-port`, `query.port` and `server-ip` for the primary port and re-writes
`eula.txt`. Other settings in your files are left alone.

## Running a server

**Stop** sends the server's own stop command and waits for the world to save
before falling back to a normal container stop. **Kill** still ends the process
immediately. See [Stopping and the console](06-worlds-and-schedules.md#stopping-and-the-console).

Players connect to the address in the **Connect** block on the **Overview**
tab. See [Ports and allocations](04-ports.md).

## Status

A game server has no **Health** tab, SDK heartbeat or TCP/HTTP probe. Its
health is the process state in the header plus the game query. The API refuses
a probe for a game server and ignores a heartbeat rule.

While a server runs, the panel asks it for its status every 10–15 seconds and
shows players, the version and the message of the day.

| Server | Query | What it shows |
|---|---|---|
| Minecraft types | Standard server-list ping | Players, version, message of the day |
| Steam types | Steam A2S_INFO on the query port | Players, server name, map, version |

A server that does not answer is shown as starting. The Steam query is sent
only for servers on the panel's own node. For a server on a remote node it is
not available yet, because the agent relays only the Minecraft ping.

### Crash alerts

Choose who is told about crashes (including failed installations and starts)
and failed backups under the server's **Settings → Notifications**. Alerts go
to the owner's account (the bell, email when alert emails are on, and the
Discord channel the owner connected), never to players. A crash alert is sent
at most once per 10 minutes per server.

*Crashes in a row* counts only exits of the running server. It clears after a
minute of stable running and on **Start** or **Restart**, and installation or
start retries never count. See [Health and alerts](../05-observe/04-health.md).

## Limits

- RivetPanel does not limit disk space, bandwidth or outbound traffic for game
  servers [not enforced].
- Steam status queries work only on the panel's own node.
- Game servers run on the panel's host or on an enrolled
  [remote node](../07-operate/04-remote-nodes.md).
