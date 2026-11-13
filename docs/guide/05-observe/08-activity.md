---
title: Activity
lede: See everything that happened on your apps in one timeline, who or what did it, and a report of any time window.
icon: activity
aliases: [activity]
glance:
  - Changes kept 180 days
  - 200 operations kept per app
  - CSV export of up to 10,000 rows
related: [apps/scheduled-actions, apps/deploy-from-github, observe/health, operate/ai-assistant]
---

## The timeline

**Activity** (`/activity`) lists everything that happened on the apps you can
see, newest first and grouped by day. It merges two records:

- **Changes**: what someone did, such as starting or stopping an app, saving a
  file, setting variables, sharing, signing in, or changing settings. Only
  names are recorded (a variable's name, a file's path), never values, file
  contents or passwords.
- **Work**: builds, installations, deployments, rollbacks, backups, restores
  and pushes to GitHub, with their status, duration and result. Work that is
  still running updates on its own, and people who may read an app's console
  can open its build output in place.

Each entry is one sentence: who, did what, to which app. A red, amber or
green knot beside the picture marks a failure, work in progress, or finished
work.

## People and automatic activity

Every entry says who performed it, and people never look like the panel:

| Picture | Who | Examples |
|---|---|---|
| Round picture | A person | Their profile picture or generated avatar |
| **Schedule** (clock) | A scheduled action, run on its owner's behalf | Nightly restart, scheduled backup or deployment, task chains |
| **GitHub push** | A push to the linked repository | Automatic deployments |
| **Panel** (server) | The panel by itself | The build when an app starts, a crash, giving up after repeated crashes |
| **AI operator** (sparkle) | A live action the AI operator took for someone | Changing a file, restarting, running a diagnostic |

Automatic entries name the account they acted for ("for Nino"). Scheduled
runs that were skipped (for example a restart of a stopped app) or missed
(the panel was not running) say so.

> [!NOTE]
> Crashes are recorded at most once a minute per app, so a crash loop cannot
> flood the timeline; giving up after the restart budget is always recorded.

## Time window and chart

Choose **1h**, **24h**, **7d**, **30d** or **90d**, or **Dates…** for exact
start and end times. The chart above the timeline shows the window in bars:
what people did on top, automatic activity below it, and failures hanging
under the baseline.

- Drag across the chart to zoom into that span; click a bar to zoom into it.
- With the chart focused, the arrow keys move between bars and Enter zooms.
- The minimize button beside the chosen dates zooms back out one step; the
  cross returns to the last 7 days.

## Filters

Filters combine, and the address keeps them, so a filtered view can be
bookmarked or shared with someone who can see the same apps.

| Filter | Shows |
|---|---|
| **Everyone / People / Automatic** | All entries, only what people did, or only what schedules, GitHub pushes, the panel and the AI operator did |
| **People** | What the chosen accounts did, or what was done on their behalf |
| **Apps** | Bots, game servers and sites |
| **Kind** | Power and crashes, builds and deployments, backups, files, variables, settings, sharing, accounts, administration, AI operator, extensions |
| **Result** | Succeeded, failed, refused, in progress or cancelled |
| Search | App names, file paths, variable names, account emails and messages |

Clicking a person's name, an entry's kind, or a row of the report adds that
filter. **Clear filters** keeps the time window.

## Report

Beside the timeline (above it on narrow screens, under **Report**) the panel
summarizes the window and filters:

- **Who did it**: how much was done by people and how much automatically,
  split by schedules, GitHub pushes, the panel and the AI operator, and how
  many entries failed or were refused.
- **Most active people** and **Busiest apps**, with their failures.
- **What happened**: entries by kind.
- **When in the week**: a weekday-by-hour grid in your time zone.

A report covers at most 400 days.

## Export

**Export CSV** downloads the filtered timeline, up to 10,000 rows, with the
time (UTC), whether it was a change or work, who performed it, the account,
the kind, the action, the result, the app and the recorded detail. Cells that
a spreadsheet would read as a formula are prefixed with `'`.

## Who sees what

| Account | Sees |
|---|---|
| Administrator | Everything on the panel |
| Everyone else | Changes and work on apps they own, were granted, or share through a workspace, plus their own account's events |

Visibility is checked by the panel for the timeline, the report, the people
list and the export alike. [enforced: panel]

## Retention

| Record | Kept |
|---|---|
| Changes | 180 days, and at most 200,000 entries [enforced: panel] |
| Work | The latest 200 operations per app [enforced: panel] |

The record is meant for the people using the panel. It is not tamper-proof
evidence against a host administrator who can edit the database.
[not enforced]
