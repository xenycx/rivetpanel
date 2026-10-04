# Log files, the daily archive and graph retention

RivetPanel keeps logs on disk one file per day and, once a day has ended,
compresses it into a separate archive folder. A background job does this; its
settings, and how long graph data is kept, are changed in the panel under
**Administration → Logs and retention** (or through the API below).

## What is written

| Log | Live file (the current day) | Archive |
| --- | --- | --- |
| Console output of every bot and game server, local and on remote nodes | `logs/bots/<server id>/<YYYY-MM-DD>.log` | `log-archive/bots/<server id>/<YYYY-MM-DD>.log.gz` |
| The panel's own log (the JSON lines it writes to stderr) | `logs/panel/<YYYY-MM-DD>.log` | `log-archive/panel/<YYYY-MM-DD>.log.gz` |

Both folders are next to the database (`/var/lib/rivetpanel/logs` and
`/var/lib/rivetpanel/log-archive` with the packaged defaults; already inside
the systemd unit's `ReadWritePaths` and the container's data volume). Folders
are created with mode 0700, files with 0600. They are **not** part of
`rivetpanel backup`; include them in host backups if you need them.

A console line is stored as `<timestamp> <stdout|stderr> <text>`, the timestamp
in the configured time zone. Panel lines are the JSON lines of the normal log
(secrets are not redacted there beyond what the panel logs; the in-memory
**Panel logs** viewer redacts its copy).

Not archived by day (and why):

* **Build, deploy and backup output** (`oplogs/`): at most 256 KiB per
  operation, removed with the operation. Its size is reported with the log
  disk usage.
* **Add-on (database) container logs** and **Docker's own container logs**:
  Docker keeps them, rotated at 3 × 10 MB per container. The live console and
  the add-on log viewer still read Docker.
* **The remote agent's own log** (stderr of `rivet-agent`, journald on the
  node): not collected by the panel.

## The day and the archive time

A day starts at the **archive time** (default `00:00`) in the configured
**time zone** (default: the server's local time; any IANA name such as
`Europe/Berlin`) and is named by the date it starts on. With `03:00`, the
file `2026-10-04.log` holds 03:00 on 4 October to 02:59:59 on 5 October.

Shortly after the archive time (the job checks every 30 seconds) the job:

1. copies the newest console output once more,
2. gzips every day file of a day that has ended into the archive folder,
3. deletes archives older than the retention, then the oldest archives
   beyond the size cap (the cap counts the live day files too, so archives
   make room for them).

It also runs when the panel starts (catching up on every missed day) and
after a settings change. **Run archival now** on the administration page
(`POST /api/v1/admin/log-archive/run`) runs it immediately.

Archiving is crash safe: the day file is renamed to a sealed name, the archive
is rebuilt as the existing archive plus one new gzip member (named after the
sealed file) in a temporary file that is fsynced and renamed into place, and
only then is the sealed file removed. An interrupted pass is finished on the
next run without archiving anything twice. Lines that arrive late for an
ended day (a remote node that was offline over midnight) are added to that
day's archive as another gzip member; `zcat`, `gzip -d` and browsers read the
whole file.

## How console output is captured

Every few minutes (default 5) the panel asks Docker, or the node's agent for a
remote server, for the output since the last copied line (a per-server cursor
in `logs/bots/<id>/.cursor`) and appends it to the day files. The live
console is unchanged and still streams from Docker. After a panel restart the
capture resumes from the cursor; the first capture of a server starts at the
beginning of the current day.

Lines can be lost when Docker no longer has them before they were copied: a
server that writes more than Docker's 30 MB rotation window between two
captures, a container that is recreated (deploy, reinstall) within the
capture interval, or a remote node that stays offline longer than its Docker
logs last. Lower the capture interval for very chatty servers.

### Limits on the live files

Live day files are bounded so a noisy server cannot fill the disk that also
holds the database:

* **Daily file limit** (`max_day_mb`, default 256 MB): one server's file, and
  the panel log's file, per day. When a write would go past it, the whole
  lines that fit are written, then one line starting with
  `[rivetpanel] log capped:`, and the rest of that day's output is dropped
  (the live console still shows it). The next day starts a fresh file. The
  log history marks such a day **Capped** (`"capped": true` in the days
  answer). With the default and the 5-minute capture interval a server can
  add at most about 256 MB per day, instead of the up to 32 MB per capture
  pass (several GB a day) it could copy before.
* **Free disk space**: while the filesystem of the log folders has less free
  space than `RIVET_MIN_FREE_DISK_BYTES` (default 1 GiB, the same threshold
  backups and deployments use), nothing is written: the console capture is
  skipped (its cursor stays, so it catches up later from what Docker still
  holds) and the archive job reports `log lines not written: the disk is
  almost full`; panel log lines of that time are not kept in files.

Remote nodes need no upgrade (agent protocol stays 8): their output streams
through the existing console route and is stored on the panel, not on the
node. A server on an offline node is skipped and caught up when the node is
back.

Deleting a server deletes its live and archived logs.

## Downloading

In the panel: a server's **Console** tab has **Log history** (download any
archive, view or download the current day), and **Administration → Logs and
retention** lists the panel log's days.

| Route | Access | Answer |
| --- | --- | --- |
| `GET /api/v1/bots/:id/logs/days` | the server's console (`bots.console` and console access to the server) | `{"days":[{"date","bytes","archived","capped"}],"current_day"}` (`capped` only on a live file that reached the daily limit), newest first |
| `GET /api/v1/bots/:id/logs/days/:date` | same | the archive (`application/gzip`) or, for a day not archived yet, the plain text file |
| `GET /api/v1/admin/logs/days` | `system.view` | the panel log's days |
| `GET /api/v1/admin/logs/days/:date` | `system.view` | as above |

`?archived=1` asks for the archive, `?archived=0` for the live file; without
it the archive is preferred.

## Settings

`GET /api/v1/admin/log-archive` (`settings.manage` or, read-only,
`system.view`) and `PUT /api/v1/admin/log-archive` (`settings.manage`;
changes are in the activity record as `admin.log_archive_settings`). `PUT` takes any subset of the fields:

| Field | Default | Bounds |
| --- | --- | --- |
| `archive_time` | `00:00` | `HH:MM` |
| `timezone` | `""` (server local) | IANA name |
| `retention_days` | 30 | 1–3650 |
| `max_archive_mb` | 0 (no cap) | 0–1048576; archives and live files together |
| `max_day_mb` | 256 | 1–1048576; per server (and the panel log) per day |
| `capture_bots` | true | |
| `panel_file` | true | |
| `capture_minutes` | 5 | 1–60 |
| `metrics.telemetry_hours` | `RIVET_TELEMETRY_RETENTION` (168) | 1–8760 |
| `metrics.usage_5m_days` | 3 | 1–14 |
| `metrics.usage_1h_days` | 35 | 2–90 |
| `metrics.usage_1d_days` | 400 | 30–1825 |
| `metrics.status_days` | 90 | 90–730 |

Analytics retention must not shrink with coarser resolution
(5-minute ≤ hourly ≤ daily). `telemetry_hours` covers host and node telemetry
(Administration → Host, node graphs) and per-bot telemetry samples; a value
saved in the panel wins over `RIVET_TELEMETRY_RETENTION`, which stays the
default. Status page samples keep at least 90 days because the public page
shows 90.

The `GET` answer also has `defaults`, `bounds`, `usage` (`live_bytes`,
`live_files`, `archived_bytes`, `archived_files`, `oldest_archive`,
`operation_log_bytes`, `operation_log_files`), `last_run` (`started_at_ms`,
`finished_at_ms`, `trigger`, `ok`, `captured_lines`, `archived`,
`archived_bytes`, `deleted`, `deleted_bytes`, `errors`), `running`,
`next_run_at_ms`, `current_day`, `capture_available`, `folder` and
`archive_folder`.

All settings are stored in the database (`panel_settings`, keys `logs.*` and
`metrics.*`); nothing needs a restart.
