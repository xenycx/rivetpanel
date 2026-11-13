---
title: Host, logs and environment
lede: See what the machine and each app are using, read the panel's own log, and change RIVET_ settings and modules from the browser.
icon: server
audience: admin
aliases: [admin-tools]
glance:
  - Charts from 1 hour to 30 days
  - Panel log keeps the newest 2,000 lines
  - At most 60 running apps measured per refresh
related: [operate/configuration, operate/diagnostics, observe/logs, teams/limits]
---

## The Host page

**Administration → Host** shows the machine that runs the apps, RivetPanel
itself and what each app uses. It needs the `system.view` permission and has
four tabs.

### Resources

- CPU, memory (with cache and swap), load average, disk, network and disk
  throughput, now and over 1 hour to 30 days. Charts show averages plus dashed
  peaks. Samples are taken every `RIVET_TELEMETRY_INTERVAL` (30 seconds by
  default), and the range is capped by how long samples are kept: 7 days by
  default, set under **Administration → Logs and retention** or
  `RIVET_TELEMETRY_RETENTION`.
- A disk-fill forecast from the trend.
- Host facts, and the RivetPanel process: memory, goroutines, open files,
  requests, errors and database size.
- The Docker runner, and storage per location. Directory sizes are counted in
  the background about every five minutes.
- A **needs attention** list that flags a nearly full disk, memory or swap
  pressure, a high load, a Docker problem and recent errors.

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
| Decide what the panel may do to the host | `RIVET_DOCKER_HOST`, `RIVET_CONTAINER_USER`, `RIVET_WORKSPACE_OWNER`, `RIVET_ALLOW_ROOT_CONTAINER_USER`, `RIVET_ALLOW_SHARED_UID`, `RIVET_CONTAINER_NETWORK`, `RIVET_APPS_NETWORK`, `RIVET_PROXY_HEADER` |
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
