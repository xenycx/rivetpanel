---
title: Log files and retention
lede: Find and download the console output of any day, and set how long logs and graph data are kept.
icon: archive
aliases: [log-archive]
glance:
  - One log file per app per day
  - Archives kept 30 days by default
  - 256 MiB per app per day by default
related: [observe/usage, operate/host-and-logs, operate/backups]
---

## What is kept

RivetPanel copies console output to disk one file per day and, once the day
has ended, compresses it into an archive.

| Log | The current day | Archive |
|---|---|---|
| Console output of every bot and game server, local and on remote nodes | `logs/bots/<server id>/<YYYY-MM-DD>.log` | `log-archive/bots/<server id>/<YYYY-MM-DD>.log.gz` |
| The panel's own log (the JSON lines it writes to stderr) | `logs/panel/<YYYY-MM-DD>.log` | `log-archive/panel/<YYYY-MM-DD>.log.gz` |

A console line is stored as `<timestamp> <stdout|stderr> <text>`, with the
timestamp in the configured time zone. Panel lines are the normal JSON log
lines. They are not redacted beyond what the panel logs; the in-memory viewer
under **Administration → Host → Panel logs** redacts its own copy.

### Not archived by day

| Output | Where it goes instead |
|---|---|
| Build, deploy and backup output (`oplogs/`) | At most 256 KiB per operation, removed with the operation. Its size is reported with the log disk use |
| Add-on (database) container logs and Docker's own container logs | Docker keeps them, rotated at 3 × 10 MB per container. The live console and the add-on log viewer read Docker |
| The remote agent's own log (`rivet-agent` stderr, journald on the node) | Not collected by the panel |

## Download logs

- **One app:** the app's **Console → Log history** lists its days. Download any
  archive, or view and download the current day.
- **The panel:** **Administration → Logs and retention** lists the panel log's
  days.

| Route | Access | Answer |
|---|---|---|
| `GET /api/v1/bots/:id/logs/days` | The app's console (`bots.console` and console access to it) | `{"days":[{"date","bytes","archived","capped"}],"current_day"}`, newest first; `capped` only on a live file that reached the daily limit |
| `GET /api/v1/bots/:id/logs/days/:date` | Same | The archive (`application/gzip`), or the plain text file for a day not archived yet |
| `GET /api/v1/admin/logs/days` | `system.view` | The panel log's days |
| `GET /api/v1/admin/logs/days/:date` | `system.view` | As above |

Add `?archived=1` for the archive or `?archived=0` for the live file. Without
it, the archive is preferred.

## Days and the archive job

A day starts at the **archive time** (default `00:00`) in the configured **time
zone** (default: the server's local time; any IANA name such as
`Europe/Berlin`). It is named by the date it starts on. With `03:00`, the file
`2026-10-04.log` holds 03:00 on 4 October to 02:59:59 on 5 October.

Shortly after the archive time (the job checks every 30 seconds), the job:

1. copies the newest console output once more,
2. compresses every day file of a day that has ended into the archive folder,
3. deletes archives older than the retention (default 30 days), then the
   oldest archives beyond the size limit. The limit counts the live day files
   too, so archives make room for them.

The job also runs when the panel starts, catching up on every missed day, and
after a settings change. **Run archival now** runs it at once
(`POST /api/v1/admin/log-archive/run`).

Archiving is crash safe. The day file is renamed to a sealed name, the new
archive is written to a temporary file that is synced and renamed into place,
and only then is the sealed file removed. An interrupted pass is finished on
the next run without archiving anything twice.

Lines that arrive late for an ended day, for example from a remote node that
was offline over midnight, are added to that day's archive as another gzip
member. `zcat`, `gzip -d` and browsers read the whole file.

## How console output is captured

Every few minutes (default 5) the panel asks Docker, or the node's agent for a
remote server, for the output since the last copied line. A per-server cursor
in `logs/bots/<id>/.cursor` remembers the place. The live console is unchanged
and still streams from Docker.

After a panel restart the capture resumes from the cursor. The first capture of
a server starts at the beginning of the current day.

Remote nodes need no upgrade (agent protocol 8): their output streams through
the existing console route and is stored on the panel, not on the node. A
server on an offline node is skipped and caught up when the node is back.

> [!WARNING]
> Lines are lost when Docker drops them before they are copied: a server that
> writes more than Docker's 30 MB rotation window between two captures, a
> container recreated (deploy, reinstall) within the capture interval, or a
> remote node offline longer than its Docker logs last. Lower the capture
> interval for very chatty servers.

### Limits on live files

Live day files are bounded so a noisy server cannot fill the disk that also
holds the database.

- **Daily file limit** (default 256 MiB) per server, and for the panel log, per
  day. [enforced: panel] When a write would pass it, the whole lines that fit
  are written, then one line starting with `[rivetpanel] log capped:`. The rest
  of that day's output is dropped from the file; the live console still shows
  it. The next day starts a fresh file, and **Log history** marks the capped
  day **Capped**. With the defaults a server adds at most about 256 MiB a day.
- **Free disk space:** while the log folders' filesystem has less free space
  than `RIVET_MIN_FREE_DISK_BYTES` (default 1 GiB, the threshold backups and
  deployments also use), nothing is written. [enforced: panel] The console
  capture is skipped and its cursor stays, so it catches up later from what
  Docker still holds. The archive job reports `log lines not written: the disk
  is almost full`, and panel log lines from that time are not kept in files.

## Settings

**Administration → Logs and retention** changes these settings (permission
`settings.manage`). Holders of host monitoring (`system.view`) see them
read-only. The page also shows live and archived disk use and the last run.
Changes apply at once, without a restart.

| Setting | API field | Default | Bounds |
|---|---|---|---|
| Archive time | `archive_time` | `00:00` | `HH:MM` |
| Time zone | `timezone` | `""` (server local) | IANA name |
| Keep archives for | `retention_days` | 30 | 1–3650 days |
| Log size limit | `max_archive_mb` | 0 (no limit) | 0–1048576 MiB; archives and live files together |
| Daily file limit | `max_day_mb` | 256 | 1–1048576 MiB; per server (and the panel log) per day |
| Write console output | `capture_bots` | on | |
| Write the panel log | `panel_file` | on | |
| Copy new output every | `capture_minutes` | 5 | 1–60 minutes |

### Graph and analytics retention

The same page sets how long graphs keep data:

| Data | API field | Default | Bounds |
|---|---|---|---|
| Resource graphs (30-second host and node samples) and bot telemetry samples | `metrics.telemetry_hours` | `RIVET_TELEMETRY_RETENTION` (168 hours, 7 days) | 1–8760 hours |
| Analytics, 5-minute detail | `metrics.usage_5m_days` | 3 | 1–14 days |
| Analytics, hourly detail | `metrics.usage_1h_days` | 35 | 2–90 days |
| Analytics, daily detail | `metrics.usage_1d_days` | 400 | 30–1825 days |
| Status page history | `metrics.status_days` | 90 | 90–730 days |

Analytics retention cannot shrink as detail gets coarser: 5-minute ≤ hourly ≤
daily. A telemetry value saved here wins over `RIVET_TELEMETRY_RETENTION`,
which stays the default. Status page history keeps at least 90 days because
the public page shows 90.

### Settings API

`GET /api/v1/admin/log-archive` (`settings.manage`, or `system.view`
read-only) and `PUT /api/v1/admin/log-archive` (`settings.manage`). `PUT`
takes any subset of the fields above, and changes are recorded in the activity
log as `admin.log_archive_settings`.

The `GET` answer also has `defaults`, `bounds`, `usage` (`live_bytes`,
`live_files`, `archived_bytes`, `archived_files`, `oldest_archive`,
`operation_log_bytes`, `operation_log_files`), `last_run` (`started_at_ms`,
`finished_at_ms`, `trigger`, `ok`, `captured_lines`, `archived`,
`archived_bytes`, `deleted`, `deleted_bytes`, `errors`), `running`,
`next_run_at_ms`, `current_day`, `capture_available`, `folder` and
`archive_folder`. Settings are stored in the database (`panel_settings`, keys
`logs.*` and `metrics.*`).

## Storage and backups

Both folders sit next to the database: `/var/lib/rivetpanel/logs` and
`/var/lib/rivetpanel/log-archive` with the packaged defaults. They are already
inside the systemd unit's `ReadWritePaths` and the container's data volume.
Folders are created with mode `0700` and files with `0600`.

> [!IMPORTANT]
> The log folders are not part of `rivetpanel backup`. Include them in your
> host backups if you need them.

Deleting a server deletes its live and archived logs.
