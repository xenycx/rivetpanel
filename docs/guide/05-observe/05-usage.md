---
title: Usage analytics
lede: See how much CPU, memory and network each app used, how often it was up, crashed, deployed and backed up, and the same totals across the whole panel.
icon: chart
aliases: [usage]
glance:
  - Ranges from 1 hour to 90 days
  - Sampled once a minute
  - Daily history kept 400 days
related: [observe/telemetry, observe/health, observe/logs]
---

## An app's Analytics tab

Every bot and game server has an **Analytics** tab. Anyone with console access
to the app can open it, the same people who see its live gauges.

| Chart | Shows |
|---|---|
| CPU and memory | Averages, with peaks as dashed lines against the app's limits |
| Network | Traffic per bucket |
| Uptime | The share of time the app was running while it was meant to run |
| Crashes and starts | Crashes counted by the restart policy, and starts |
| Workspace size | Disk used by the app's files |
| Deployments | Succeeded, failed and average duration (bots only) |
| Backups | Succeeded, failed and bytes written |

| Range | Bucket |
|---|---|
| 1 hour, 24 hours | 5 minutes, including the current five minutes as they are collected |
| 7 days, 30 days | 1 hour |
| 90 days | 1 day |

**Export CSV** downloads the rows shown
(`GET /api/v1/bots/:id/usage?range=…&format=csv`).

## Panel-wide analytics

**Administration → Analytics** (permission `analytics.view`) adds up the same
data across the panel for 24 hours to 90 days:

- accounts, total and new
- bots and game servers, total and running
- CPU, memory and network in use
- uptime, crashes and starts, deployments and backups
- the top 10 consumers by CPU, memory and network

Some parts need more permissions:

| Part | Also needs |
|---|---|
| Each node's average and peak CPU, memory, disk and network, and a CPU trend, grouped by location | `nodes.manage` |
| Tickets opened, closed and waiting, and the median time to the first staff reply | `tickets.view_all` or `tickets.manage` |
| Owners' email addresses | `users.view` |

The CSV is at `GET /api/v1/admin/analytics?range=…&format=csv`.

### Who counts in the totals

A viewer who is not a built-in administrator sees only the accounts it could
manage under the [delegated administration
rule](../06-teams/02-roles-and-permissions.md#delegated-administration), and
those accounts' bots. [enforced: panel]

API clients never count as administrators. A client limited to some bots or
workspaces cannot open the overview. [enforced: panel]

## How it is collected

Nothing is pushed by bots and no agent update is needed: the panel measures
apps itself.

- **Once a minute** the panel reads every bot's state. For running bots it
  takes one stats reading from Docker (locally) or from the agent (connected
  remote nodes). It measures at most 100 running bots per pass, in rotation,
  4 at a time. Bots on a disconnected node count for uptime but are not
  measured.
- **Uptime** is the share of samples in which an app that was meant to run was
  running. Only time the app was meant to run counts.
- **Crashes** are increases of the restart policy's crashes-in-a-row counter.
  **Starts** are changes of the last start time; several in one minute count
  once.
- **Network** is the difference of the container's counters between samples.
- **Workspace size** is measured every 30 minutes, up to 10 apps per pass, for
  apps on this panel's own node only.
- **Deployments and backups** are counted from the operations history (and the
  backup's size) into hourly rows every five minutes, so the counts stay after
  old operations are pruned. On first start, the last 35 days of operations
  still recorded are counted.
- **Nodes** come from the node telemetry samples, rolled up into hourly and
  daily rows so node trends outlive `RIVET_TELEMETRY_RETENTION`.

> [!NOTE]
> Time the panel itself was not running records nothing. It shows as a gap,
> not as downtime.

## Retention

The panel writes one row per app every five minutes, in one transaction, and
recomputes hourly and daily rows for the current and previous hour or day.

| Detail | Kept by default |
|---|---|
| 5-minute rows | 3 days |
| Hourly rows | 35 days |
| Daily rows | 400 days |

Pruning runs hourly, in batches of 1,000 rows. Administrators change these
periods under **Administration → Logs and retention** (see [Log files and
retention](06-logs.md#graph-and-analytics-retention)). Deleting an app deletes
its rows.

The data lives in the SQLite tables `bot_usage` and `node_usage` (migration
`0052`).
