---
title: Bot telemetry
lede: Send stats, command counts, events and widgets from your bot to its private insights, within limits that keep one bot from flooding the panel.
icon: activity
aliases: [telemetry]
glance:
  - 60 pushes per minute per bot
  - 64 KiB per push
  - 7 days of history by default
related: [observe/widgets, observe/health, observe/usage]
---

## What telemetry is

Telemetry is data your bot sends about itself: numeric stats such as guilds or
ping, how often commands ran, named events, and [dashboard
widgets](01-widgets.md). It shows under the bot's **Public page → Private
insights**, which only people with access to the bot can open.

Every accepted push also counts as a heartbeat for [Health and
alerts](04-health.md). Telemetry is pushed by the bot; resource use the panel
measures itself is in [Usage analytics](05-usage.md).

## Connect a bot

A full administrator of the bot sets this up.

1. Open the bot's **Public page → Private insights**.
2. Select **Generate key**. The key is shown once and added to the bot as
   `RIVET_TELEMETRY_KEY`, together with `RIVET_URL` when the panel's public
   address is configured.
3. Copy the SDK snippet for your language into the bot and call it as shown at
   the top of the file.
4. Start the bot, or restart it if it is running: a running bot gets the key
   only when it restarts. Stats appear within a minute.

**Revoke key** on the same page stops the bot's pushes. **Generate a new key**
replaces the old one.

### SDK snippets

The panel serves each snippet as plain text from `/api/v1/sdk/<lang>`:

| `<lang>` | For |
|---|---|
| `discordjs` | discord.js |
| `discordpy` | discord.py |
| `go` | Any Go library, standard library only |
| `rust` | serenity, poise, twilight and others, using reqwest |
| `java` | JDA and others, using the JDK HTTP client |
| `ruby` | discordrb |

The snippets push every 30 seconds. They never queue failed pushes and never
crash the bot.

## Push format

Any client can push without an SDK. Send `POST /api/v1/bot-telemetry`, or one
text frame per push on the WebSocket at `/api/v1/bot-telemetry/ws`, with
`Authorization: Bearer <key>`:

```json
{"ready":true,
 "stats":{"guilds":12,"members":3400,"ping":42},
 "commands":[{"name":"ban","count":2}],
 "events":[{"name":"guild_join","data":{"id":"1"}}]}
```

| Field | Contains |
|---|---|
| `ready` | Whether the bot is connected to Discord (see [Health and alerts](04-health.md)) |
| `stats` | Stat names and numbers |
| `commands` | `{name, count}` objects; `count` is 1–1,000,000 and 1 when left out |
| `events` | `{name, data}` objects; `data` is optional JSON |
| `widgets`, `unpublish` | See [Dashboard widgets](01-widgets.md) |

Stat, command, event and widget names are 1–64 letters, numbers, spaces, dots,
colons, underscores or hyphens. Unknown fields are refused.

An HTTP push is answered `202` with `{"stored": n}`. On the WebSocket each frame
is answered with `{"stored": n}` or `{"error": "…"}`. Validation errors name the
offending field.

## Limits

| Limit | Value | |
|---|---|---|
| Push size | 64 KiB per request or WebSocket frame (larger answers `413`) | [enforced: panel] |
| Push rate | 60 pushes per minute per bot, HTTP and WebSocket together (more answers `429`, or closes the WebSocket) | [enforced: panel] |
| Stats | 16 per push; one sample per stat every 10 seconds (faster samples are acknowledged and dropped) | [enforced: panel] |
| Commands | 20 per push | [enforced: panel] |
| Events | 10 per push; `data` up to 1 KiB of valid JSON | [enforced: panel] |
| Widgets | 48 updates and 48 unpublish keys per push; data valid non-null JSON up to 4096 bytes | [enforced: panel] |
| Stored rows | 20,000 telemetry rows per bot; older rows are trimmed | [enforced: panel] |
| History | 1 hour to 365 days, 7 days by default | [enforced: panel] |

A WebSocket that sends nothing for 3 minutes is closed.

### How long history is kept

An administrator sets telemetry retention under **Administration → Logs and
retention**, from 1 hour to 365 days. Without a value there, the
`RIVET_TELEMETRY_RETENTION` environment variable applies (default `168h`,
seven days). See [Log files and retention](06-logs.md#graph-and-analytics-retention).

## Private insights

**Private insights** shows the stats you send over 1 hour, 6 hours, 24 hours
or 7 days, the command usage and the recent events, plus the bot's
[widgets](01-widgets.md). **Customize** chooses which stats appear and renames
them; that choice is saved in your browser.

None of this reaches the public page. Only widgets can, and only when you
publish them.
