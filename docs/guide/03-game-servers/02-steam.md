---
title: Steam games
lede: Run a Valheim, Rust or Project Zomboid dedicated server that the panel downloads and updates with SteamCMD, without a Steam account.
icon: download
glance:
  - 3 built-in Steam games
  - Anonymous SteamCMD login only
  - Downloads stop after 240 minutes at most
related: [game-servers/overview, game-servers/ports, game-servers/administration]
---

## How Steam servers install

RivetPanel writes the Steam server types from each game's public dedicated
server documentation. Create one with **New → Game server → Steam game
server**.

On the first start, on **Reinstall**, and when the beta branch changes, the
panel runs SteamCMD in a separate install container
(`steamcmd/steamcmd:latest`). That container has the same hardening as every
install container, plus internet access. It downloads or updates the app into
the server's files.

- SteamCMD keeps its own client files in `.steamcmd` in the server's files, so
  later updates are faster.
- `steamclient.so` is copied to `.steam/sdk64` and `.steam/sdk32`, where
  dedicated servers look for it.
- The installed Steam build id is shown as the installed version.
- The servers run in the same image: Ubuntu with SteamCMD's 32-bit libraries.

### Anonymous login only

No Steam account, password or Steam Guard code is ever asked for or stored.
Games that need a licensed account to download cannot be offered, for example
ARK: Survival Ascended (whose server is Windows-only anyway).

Counter-Strike 2 downloads anonymously, but it needs 35+ GB and a game server
login token to be listed, so it is not a built-in type. Administrators can
still write a server type for it; see
[Administration](08-administration.md#blueprint-format).

### Beta branches

The **Steam beta branch** setting picks a branch. Only plain branch names are
accepted: letters, digits, `.`, `_` and `-` [enforced: panel].
Password-protected branches are not supported.

### Download limits

| Limit | Default | Enforced by |
|---|---|---|
| Time limit (`timeout_minutes`) | 60 minutes, at most 240 | The runner, which removes the install container [enforced: panel] |
| Size watchdog (`max_size_gb`) | Set per game | A watchdog inside the install container that stops SteamCMD when the server's files grow beyond it [enforced: panel] |

The size watchdog is not a disk quota [not enforced]: once installed, a server
can still fill the disk.

## Per game

| Game | Ports | Stop | Notes |
|---|---|---|---|
| Valheim | Game port plus the next port (Steam query), allocated as a consecutive pair | `SIGINT`, which saves the world | Needs a server password |
| Rust | Game port plus a Steam query port | `SIGINT` | RCON and the Rust+ companion port are not published |
| Project Zomboid | Two consecutive UDP ports (`-port` and `-udpport`) | Types `quit` in the console | Admin password set on first start |

### Valheim

- Set a server password of at least 5 characters before the first start.
  It must not appear in the server name. Valheim does not start without one.
- The optional crossplay library needs PulseAudio and libatomic, which the
  image does not have, so the server type does not offer crossplay.
- In testing, Valheim answered no Steam queries while unlisted (`-public 0`),
  so the panel shows players only for listed servers.

### Rust

Rust needs a lot of memory: the server type asks for at least 8 GiB. It also
needs about 10 GB of disk.

### Project Zomboid

- The admin password is used when the server creates the `admin` account on
  its first start. If you leave it empty, the server's ID is used, so change it
  in game.
- `servertest.ini` (name, players, public listing, ports) and the Java heap in
  `ProjectZomboid64.json` are kept in sync with the settings before every
  start.

## Status while running

While a Steam server runs on the panel's own node, the panel shows players,
server name, map and version from the Steam query on its query port. The
**Connect** block shows the query port too. See
[Status](01-overview.md#status).

## Terms

Downloading and running these servers is subject to Valve's Steam Subscriber
Agreement and to each game's own terms: Iron Gate and Coffee Stain for
Valheim, Facepunch for Rust, The Indie Stone for Project Zomboid.

RivetPanel does not ask people to accept these terms in the panel. The
operator is responsible for complying, for example with Rust's server owner
rules on monetisation.

## How well each game is tested

> [!NOTE]
> Only the Valheim download, start, Steam query attempt and `SIGINT` stop were
> tried against the real game, by hand in Docker with the server type's
> command line rather than through a full panel install. Rust and Project
> Zomboid are written from their documentation and were not run (their
> downloads are 10 GB and several GB).

The SteamCMD install path itself is covered by a real-Docker integration test
with the small Steamworks redistributable app (`RIVET_TEST_STEAM=1`).
