---
title: Worlds, console and schedules
lede: Move worlds in and out as archives, send console commands, stop servers without losing the world, and chain tasks such as a warned restart.
icon: clock
glance:
  - Archives up to 8 GiB, 200,000 entries
  - Task chains up to 20 steps
  - Waits up to 1 hour per chain
related: [apps/files, apps/scheduled-actions, game-servers/overview]
---

## Worlds and archives

The file manager handles worlds as archives:

- **Download** any folder as a zip, for example `world`.
- **Compress** files or folders into a zip next to them.
- **Extract here** unpacks `.zip`, `.mrpack`, `.tar.gz`, `.tgz` and `.tar`
  archives that are already in the server's files.

To bring in a world, upload it as one archive, then use **Extract here**. Use
SFTP for very large worlds. See [Files](../02-apps/02-files.md).

### Extraction limits

Extraction is staged, so a rejected archive changes nothing.

| Rule | Limit |
|---|---|
| Links, absolute paths and `..` paths | Refused [enforced: panel] |
| Entries | At most 200,000 [enforced: panel] |
| Total size | At most 8 GiB [enforced: panel] |
| One file | At most 4 GiB [enforced: panel] |
| Zip compression ratio | Entries expanding more than 200× are rejected [enforced: panel] |

## Stopping and the console

**Stop** sends the server type's stop command to the console and waits up to
its stop timeout so the world is saved. It then falls back to the regular
`SIGTERM`/`SIGKILL` stop. **Kill** still ends the process immediately.

| Server | Stop | Stop timeout |
|---|---|---|
| Paper, Purpur, Vanilla, Fabric, Folia | `stop` | 90 s |
| Forge, NeoForge | `stop` | 120 s |
| Velocity | `end` | 30 s |
| Valheim | `SIGINT` | 120 s |
| Rust | `SIGINT` | 180 s |
| Project Zomboid | `quit` | 120 s |

The stop signal is sent by the runner through Docker.

The console sends each line to the server process, so Minecraft commands work
without a leading slash.

### Send a command from a script

`POST /api/v1/bots/{id}/command` sends one line to a running server's (or
bot's) console. It needs the start/stop permission, like typing in the
console [enforced: panel].

## Task chains

A schedule can run a **task chain**: several steps in order, with waits in
between. See [Scheduled actions](../02-apps/09-scheduled-actions.md) for
schedules in general.

| Setting | Limit |
|---|---|
| Steps | Up to 20, each a console command, start, stop, restart, kill or backup |
| Wait before a step | 0–3600 seconds |
| All waits together | At most one hour |

A failed step stops the chain unless it has **Continue with the next task if
this one fails** ticked.

Two presets fill in common chains:

- **Warned restart**: chat warnings 5 minutes and 1 minute ahead, `save-all`,
  then a restart. It announces itself in chat and saves the world first.
- **Save, then back up**: `save-all`, then a backup 15 seconds later.

Chains run in the background, one run per schedule at a time. Each step
re-checks the creator's current permission [enforced: panel].
