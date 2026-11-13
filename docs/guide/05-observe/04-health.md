---
title: Health and alerts
lede: Know whether your bot's code is alive and reachable, not just whether its container runs, and get told when that changes.
icon: shield
aliases: [health]
glance:
  - 3 separate health signals
  - Probes reach only 127.0.0.1
  - 1 crash alert per bot per 10 min
related: [observe/telemetry, observe/notifications, game-servers/overview]
---

## Three signals

A bot has three health signals, and each answers a different question.

| Signal | Answers | Where you see it |
|---|---|---|
| Process state | Is the container running? | The bot header |
| Heartbeat | Is the bot's own code alive and logged in? | **Health** tab |
| Probe | Can the host reach the bot's port? | **Health** tab |

A container can run while the code inside it is stuck, or while it has lost
its Discord connection. Process state alone cannot tell you that, so the
heartbeat and the probe exist alongside it.

## Heartbeat

Every accepted telemetry push from the RivetPanel SDK counts as a heartbeat.
The SDK snippet and the key it needs are on the bot's **Public page** tab.

Send the optional `ready` boolean to say whether the bot is connected:

1. Send `ready: true` once your client has logged in to Discord.
2. Send `ready: false` when it disconnects.

A bot that has never reported is *unknown*. It is never *failed*, and it never
raises an alert.

## Probe

A probe checks one of the bot's published TCP ports from the host.

- A **TCP probe** passes when the port accepts a connection.
- An **HTTP probe** sends `GET` to a path and passes on a `200`–`399` reply. It
  does not follow redirects.

Both connect through `127.0.0.1` to a port published under **Network**, so a
probe can never be pointed at another host. [enforced: panel]

| Setting | What it does |
|---|---|
| Check every | The interval between checks |
| Timeout | A check slower than this counts as failed |
| Failures before unhealthy | Failed checks in a row that mark the bot unhealthy |
| Successes before healthy | Passing checks in a row that mark it healthy again |
| Startup grace | Failures right after a start are ignored for this long |

### Restart when unhealthy

With **Restart when unhealthy** on, the panel replaces a stuck container once,
when the bot *becomes* unhealthy. It does not restart on every failed check, so
a broken deployment cannot restart in a loop.

This is separate from the **Restart policy** under **Configuration**, which
retries a process that exits, with bounded exponential backoff.

The *crashes in a row* count clears once a run has lasted a minute, and on
**Start** or **Restart**. It never includes install, build or start retries.

## Game servers

Game servers have no **Health** tab, heartbeat or probe. Their health is the
process state plus the game query: players, latency and version, shown in the
server header. The API refuses a probe for a game server and ignores a
heartbeat rule.

## Alerts

Choose alerts under **Settings → Notifications** of each bot and game server.

| App | Alerts you can choose |
|---|---|
| Bot | Crashes, deployments, backup failures, recoveries, and a heartbeat rule |
| Game server | Crashes (including failed installs and starts) and backup failures |

The **heartbeat rule** alerts once when a running bot stops reporting
heartbeats for a period you choose, from 1 minute to 1 hour, and once more when
it recovers.

Crash alerts are limited to one per bot every 10 minutes.

### Where alerts go

Alerts go to the app owner's account, not through a Discord bot:

- the bell in the top bar
- email, when **Alert emails** are on
- the Discord channel the owner connected under
  **Settings → Connected accounts → Discord**

**Send test** checks the Discord connection.

> [!NOTE]
> A host port that another container already publishes is not a crash. The
> start is refused with "Port N is already used by another container"
> (administrators also see that container's name). Nothing is retried, counted
> or alerted, and a game server offers **Pick a free port**. See
> [Ports and allocations](../03-game-servers/04-ports.md).

## Panel health

For the panel itself, the **Diagnostics** page checks the database, the
container runtime, storage and the runner. See
[Diagnostics and footprint](../07-operate/12-diagnostics.md).
