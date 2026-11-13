---
title: Players, properties and mods
lede: Manage who can join, edit the common server.properties settings, and install Modrinth mods and plugins that fit your server.
icon: users
glance:
  - Player names checked as 1–16 characters
  - Mods SHA-512 verified from Modrinth
related: [game-servers/overview, game-servers/worlds-and-schedules, apps/files]
---

## Which servers have these tabs

Server types that support them add up to three tabs to a server:

| Tab | Server types |
|---|---|
| **Players** | Every Minecraft type except Velocity |
| **Properties** | Every Minecraft type except Velocity |
| **Plugins** | Paper, Purpur, Folia, Velocity |
| **Mods** | Fabric, Forge, NeoForge |

Servers created before a server type gained these tabs get them after
**Update** on their **Configuration** tab, which moves them to the type's new
revision.

## Players

**Players** lists who is online (from the server-list ping), operators, the
whitelist and bans. The lists are read from `ops.json`, `whitelist.json` and
`banned-players.json`.

Buttons send `op`, `deop`, `kick`, `ban`, `pardon` and `whitelist …` through
the console.

| Action | Permission needed |
|---|---|
| See the lists | File access |
| Use the buttons | Start/stop |

Player names are checked before a command is sent: 1–16 letters, digits or
underscores [enforced: panel].

## Properties

**Properties** edits the most-used `server.properties` settings: message of
the day, maximum players, online mode, whitelist, game mode, difficulty, PvP,
world, view distance and more.

- Saving replaces those keys in place and keeps comments, order and every
  other key.
- If the running server changed the file in the meantime, the save is refused
  and you reload.
- The server reads the file at start, so restart to apply.

## Plugins and mods

**Plugins** or **Mods** searches Modrinth for projects that support the
server's loader and installed Minecraft version.

1. Search for a project.
2. Install it: the newest fitting version goes into `plugins/` or `mods/`.
   You can also pick a specific version.
3. Install the required dependencies the panel lists next.
4. Restart the server to load the new files.

Installed jars can be removed from the same tab.

The panel checks that the chosen version supports the loader, downloads only
from `cdn.modrinth.com`, and verifies the published SHA-512 before the file is
committed [enforced: panel].

Modrinth is the only source. CurseForge and Spigot resources are not
supported.
