---
title: Host, logs and environment
lede: See what the machine and each app are using, read the panel's own log, and change RIVET_ settings and modules from the browser.
icon: server
audience: admin
aliases: [admin-tools]
glance:
  - Charts from 5 minutes to 30 days, live every 2 seconds
  - Each app's use recorded every 15 seconds for 48 hours
  - CPU spikes kept 30 days with the apps and processes behind them
  - Panel log keeps the newest 2,000 lines
related: [operate/configuration, operate/diagnostics, observe/logs, teams/limits]
---

## The Host page

**Administration → Host** shows the machine that runs the apps, RivetPanel
itself and what each app uses. It needs the `system.view` permission and has
four tabs.

### Resources

- CPU, memory (with cache and swap), load average, disk, network and disk
  throughput, drawn on canvas charts. Charts show averages plus dashed
  peaks. The memory and disk charts fit their axis to the values in view
  (the total is in the chart title), so a jump of a few gigabytes on a
  large disk stays visible over 30 days.
- **CPU by app** and **memory by app**: stacked charts of the eight busiest
  apps, the rest of the apps, and **host** (whatever no app accounts for: the
  panel, Docker and the system).
- Deploys, starts, stops, restarts, crashes, out-of-memory kills, backups and
  spikes are marked on every chart. Markers that would overlap at a long
  range merge into one wider flag; hover it to read the first few events,
  and zoom in to separate them. When the range holds too many to draw
  lines through the chart, only the flags along the top are shown.
- A disk-fill forecast from the trend.
- Host facts, and the RivetPanel process: memory, goroutines, open files,
  requests, errors and database size.
- The Docker runner, and storage per location. Directory sizes are counted in
  the background about every five minutes.
- A **needs attention** list that flags a nearly full disk, memory or swap
  pressure, a high load, a Docker problem and recent errors.

#### Time range and refresh

| Control | What it does |
| --- | --- |
| Range | 5 minutes to 30 days, or the exact span you dragged |
| Drag across a chart | Zooms every chart to that span; double-click returns to the range |
| Hover | One crosshair and the values at that moment on every chart |
| Legend | Click hides a series; Alt- or Cmd-click shows only that one |
| Refresh | Off, 5, 10 or 30 seconds, or 1 minute; only new points are fetched, so the charts scroll instead of redrawing |
| Live | For ranges up to 1 hour, a new host point every 2 seconds (every 5 seconds on a remote node) |

The range, refresh and Live choices are kept in the address, so a copied link
opens the same view. Refreshing pauses while the tab is hidden. A request
that gets no answer within 25 seconds counts as failed and is retried on the
next refresh, and when the shown data has fallen behind the range (after a
sleep or failed refreshes) the whole range is loaded again.

How far back each chart reaches:

| Data | Step | Kept |
| --- | --- | --- |
| Host samples | `RIVET_TELEMETRY_INTERVAL` (30 seconds by default) | `RIVET_TELEMETRY_RETENTION` or **Logs and retention**, 7 days by default; older ranges use hourly and daily rollups, which keep CPU, memory, disk, network, load, disk I/O and swap. Load, disk I/O and swap rollups start with 1.21.0, so hours before the upgrade whose samples had already expired show as gaps |
| Each app's CPU, memory, network, disk I/O and processes | 15 seconds (30 seconds on remote nodes) | 48 hours; older ranges use the 5-minute, hourly and daily usage rollups |
| Process snapshots | Every 60 seconds, and every 15 seconds during a spike | 7 days |
| Spikes | — | 30 days |

#### Finding what caused a spike

A **spike** starts when host CPU stays at or above `RIVET_SPIKE_CPU_PCT` (80 %
by default) for 10 seconds and ends after 30 seconds at least 10 points below
it. The panel records its peak and average and the apps and processes using
the most CPU, and takes a process snapshot every 15 seconds while it lasts.
Spikes are shaded on the CPU chart and listed above the charts with their
main cause; selecting one zooms to it.

Click any point on a chart to open **What happened here?** for that moment:

- host CPU, memory and load
- each app's CPU and memory, with its share of the host
- the busiest processes, with the app each one belongs to
- the events within 10 minutes and the spike, if there was one
- **Ask the assistant**, which starts an AI chat about that moment (see
  [AI assistant](10-ai-assistant.md))

The **Processes** card lists the busiest processes now. Command lines are
shortened to 160 characters, and an app's environment values and anything
that looks like a secret are replaced. Process environments are never read.
[enforced: panel]

Spike detection and the 2-second live stream run for the panel's own machine.
Remote nodes report per-app samples, process snapshots and a 5-second live
stream of CPU, memory and load through their agent (agent 1.20.0 or later),
but record no spikes.

#### Host access for processes

The busiest processes need the host's process list. Installed with systemd,
the panel reads `/proc` directly. The container deployment mounts the host's
`/proc` and `/sys/fs/cgroup` read-only and points `RIVET_HOST_PROC` and
`RIVET_HOST_CGROUP` at them (see `compose.yaml`). Without those mounts the
panel falls back to Docker: per-app processes from `docker top`, per-app
use from Docker's statistics, and no host processes outside apps.

### Apps

Every running app's CPU (cores against its limit), memory against its limit,
network totals since the container started, processes and workspace size.
The list is sortable and refreshes every 10 seconds; at most 60 running apps
are measured per refresh.

### Capacity

The admission budgets, with each limit's current value and where it comes
from. See [Limits and allowances](../06-teams/04-limits.md).

### Panel logs

The newest 2,000 lines the panel logged since it started, with a level filter,
search, live follow, copy and download. A count on the tab flags errors from
the last hour. Secrets, tokens and passwords are redacted before a line is
kept. [enforced: panel]

The view starts empty after a restart. The complete log stays in journald
(`journalctl -u rivetpanel`) or `docker logs`, and in the panel's daily log
files.

## Where logs go

| Log | Where |
| --- | --- |
| The panel's own log | Structured JSON on stderr, so journald or `docker logs` |
| App output | Docker's rotated container logs (`json-file`, 3 × 10 MiB) |
| Daily files | One file per day of the panel log and of every server's console under `/var/lib/rivetpanel/logs` |
| Archive | Each ended day gzipped into `/var/lib/rivetpanel/log-archive` at the archive time (default 00:00), kept 30 days by default or until a size cap |

Days are listed and downloaded per server by people with console access, and
for the panel log by accounts with `system.view`. The same retention settings
decide how long graph data is kept (telemetry, analytics tiers, the status
page). Details are in [Logs](../05-observe/06-logs.md).

## Environment variables from the panel

**Administration → Environment** (built-in administrators only) lists every
`RIVET_` variable with its description, default and source: the default, the
environment file, or set here. Less common groups start collapsed. The
reference is in [Configuration](02-configuration.md#variables).

1. Change the values you need. Every save is checked as a whole configuration
   before it is stored.
2. The page shows what is waiting for a restart.
3. Press **Restart panel**, or restart the service yourself.

Values are stored in the panel's database (the metrics token encrypted) and
layered over the process environment when RivetPanel starts. The environment
file itself is never edited.

| Precedence | Source |
| --- | --- |
| 1 | A value set here; it may be empty to switch off something the environment file enables |
| 2 | The environment file |
| 3 | The built-in default |

If a saved set ever stops validating, the panel starts without it and says so
on the page.

### What cannot be changed here

| Kind | Variables |
| --- | --- |
| Read before the database opens | `RIVET_ENV`, `RIVET_LISTEN`, `RIVET_DB_PATH`, `RIVET_DB_MAX_CONNS`, `RIVET_DATA_ROOT`, `RIVET_KEY_DIR`, `RIVET_ACTIVE_KEY_ID`, `RIVET_RUNTIMES_DIR`, `RIVET_EXTENSIONS_SAFE_MODE` |
| Decide what the panel may do to the host | `RIVET_DOCKER_HOST`, `RIVET_CONTAINER_USER`, `RIVET_WORKSPACE_OWNER`, `RIVET_ALLOW_ROOT_CONTAINER_USER`, `RIVET_ALLOW_SHARED_UID`, `RIVET_CONTAINER_NETWORK`, `RIVET_APPS_NETWORK`, `RIVET_PROXY_HEADER`, `RIVET_HOST_PROC`, `RIVET_HOST_CGROUP` |
| Managed by another page | Sign-in and address (**Panel settings**, where the environment file wins); the default allowance fallbacks `RIVET_MAX_BOTS_PER_USER`, `RIVET_MAX_SITES_PER_USER` and `RIVET_USER_MEMORY_BYTES` (**Panel settings → Default allowance**) and `RIVET_TELEMETRY_RETENTION` (**Logs and retention**), where the panel's value wins and the file is the fallback |

Managed variables are marked **Managed** on the page.

### Restart panel

**Restart panel** exits with code 75, so it needs a supervisor that starts
RivetPanel again: the systemd unit (`Restart=on-failure`) or a container
restart policy. Without a detected supervisor the button is hidden. App
containers keep running through a restart.

### From the host

- `rivetpanel env` lists the values saved in the panel.
- `rivetpanel env reset` drops all of them; `rivetpanel env reset NAME…` drops
  only the ones named.

## Modules

**Administration → Modules** (built-in administrators only) switches each
module **Off**, **Preview** or **On**. Off modules show no pages and answer no
API calls [enforced: panel]; preview modules are opt-in and may still change.
The choice is saved to `RIVET_MODULES`, writing only modules that differ from
their built-in state, and applies after a restart, which the page offers.

| Module | Built-in state |
| --- | --- |
| Core hosting | Always on |
| Applications | On |
| Sites | On |
| AI operations | On |
| Containers | Preview |
| Game servers | Preview |
| Remote nodes | Off |
| Extended identity | Off |
| Support and content | Off |
| Extensions | Off |

## Prometheus metrics

Set `RIVET_METRICS_TOKEN` to a random secret of at least 24 characters to
enable `GET /metrics`. Scrapers must send `Authorization: Bearer <token>`.
[enforced: panel] With the variable empty the endpoint stays off.

Metrics cover process uptime and requests, database availability, aggregate
bot lifecycle counts, and the latest node CPU, memory, disk and running-bot
gauges. Bot names, user IDs and HTTP paths are deliberately not used as
labels.

## Search everything

Press Ctrl+K anywhere to search pages, settings, environment variables, bots,
sites, workspaces, people, AI chats and actions. Several words narrow the
search, and Tab switches scope.
