---
title: Scheduled actions
lede: Back up, start, stop, restart or redeploy an app on a timetable, alone or as a chain of steps.
icon: clock
glance:
  - Up to 20 schedules per app
  - Checked every 30 seconds
  - No backlog after downtime
related: [operate/backups, game-servers/worlds-and-schedules, apps/deploy-from-github]
---

## Add a schedule

1. Open the app's **Schedules** tab.
2. Choose an action (below).
3. Choose when: **Every day**, **Every week**, **Every few hours** or a
   five-field cron expression, in any IANA time zone. The next runs are
   previewed.
4. Press **Add schedule**.

**Run now** in a schedule's menu tests it straight away, without changing
its next run.

| Action | Does | Needs |
|---|---|---|
| **Back up** | Backs up the files and sealed variables, kept with the scheduled backups | *Edit files* |
| **Restart** | Restarts the app, only if it is running | *Start, stop and restart* |
| **Start** | Starts the app, or retries it after it gave up | *Start, stop and restart* |
| **Stop** | Stops the app | *Start, stop and restart* |
| **Deploy from GitHub** | Deploys the newest commit of the linked branch (not for game servers) | *Edit files* |
| **Task chain** | Several steps in order, with waits | The permissions of its steps |

A per-app backup schedule replaces the panel-wide backup interval for that
app. See [Backups](../07-operate/07-backups.md).

## Task chains

A task chain runs up to 20 steps in order. Each step is a console command,
start, stop, restart, kill or backup, with a wait of 0 to 3600 seconds before
it; all waits together are at most one hour. A console command is one line of
at most 1,000 bytes.

A chain runs in the background, one run per schedule at a time. A failed step
stops the chain unless **Continue with the next task if this one fails** is
ticked for it.

The presets **Warned restart** (two chat warnings, `save-all`, restart) and
**Save, then back up** fill in a chain with Minecraft console commands; see
[Worlds and schedules](../03-game-servers/06-worlds-and-schedules.md).

## How schedules run

One scheduler checks for due schedules every 30 seconds.

- **Permissions**: each schedule runs with its creator's *current*
  permissions, re-checked at every run and at every step of a chain. A
  removed grant or a disabled account pauses it, recorded as *denied*.
  [enforced: panel]
- **Downtime**: if the panel was down at the due time and finds the run more
  than 5 minutes late, it is recorded as *missed* and not repeated, so there
  is no backlog after downtime.
- **Running work**: a run that conflicts with work already in progress is
  recorded as *skipped*. A scheduled restart never starts a stopped app.
- **Daylight saving**: times that do not exist in the gap are skipped, and
  repeated hours run once.
- **Activity**: every automatic run, including skipped, missed and denied
  ones, appears on [Activity](../05-observe/08-activity.md) under
  **Schedule**, for the schedule's owner. A chain is recorded when it ends.
  **Run now** is recorded as the person who pressed it.

| Limit | Value |
|---|---|
| Schedules per app | 20 [enforced: panel] |
| Steps per chain | 20 [enforced: panel] |
| Wait before a step | 0–3600 seconds, at most one hour per chain [enforced: panel] |
