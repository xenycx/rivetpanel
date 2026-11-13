---
title: Overview
lede: What RivetPanel runs for you, and where everything is in the interface.
icon: overview
glance:
  - 4 kinds of app in one list
  - One Go binary with SQLite inside
  - About 38 MiB of RAM when idle
related: [get-started/quick-start, get-started/concepts, teams/appearance]
---

## What RivetPanel runs

RivetPanel is a self-hosted control panel. It runs everything below in one
place, with one login and one list of apps, each app in its own locked-down
container on machines you own.

| Kind | What it is |
|---|---|
| **Bot** | A long-running process: a Discord bot or any other Node.js, Python, Rust, Go, Java, Ruby or PHP program |
| **Game server** | A Minecraft Java server, or a Valheim, Rust or Project Zomboid server installed through SteamCMD (preview) |
| **Container** | Any Docker image from an allowed registry, a Compose file imported as a stack, or a one-click app (preview) |
| **Website** | Static files, a static build from GitHub, or a running web app, on a separate sites address |

The panel is a single Go binary with SQLite inside: no database server,
message broker or cloud account. It idles at about 38 MiB of RAM. More
machines can be added later as [remote nodes](../07-operate/04-remote-nodes.md).

### What comes with every app

- **Bots** start from working templates (discord.js, TypeScript, discord.py,
  Poise, JDA, DiscordGo, discordrb) or any GitHub project, with a live
  console, resource gauges, companion databases, a package manager, a file
  editor and SFTP. See [Bots and processes](../02-apps/01-bots.md).
- **Game servers** cover Paper, Purpur, Vanilla, Fabric, Forge, NeoForge,
  Folia and Velocity, plus Steam games and imported Pterodactyl eggs. See
  [Game servers](../03-game-servers/01-overview.md).
- **Containers** add Compose import with a preview and 25 one-click apps such
  as Ghost, Vaultwarden and n8n. See [Containers and stacks](../02-apps/08-containers.md).
- **Websites** get custom domains, `_redirects` and `_headers`, and instant
  rollback. See [Bot Sites](../04-websites/01-bot-sites.md).

Backups, schedules, notifications, an AI assistant, team workspaces and
support tickets work across all of them.

## The interface

### Sidebar

| Item | What it opens |
|---|---|
| **Overview** (`/dashboard`) | Capacity numbers and the apps that matter most right now |
| **Apps** (`/apps`) | Everything the account runs, in one table |
| **Activity** (`/activity`) | One timeline of changes (who changed what; names only, never values) and work (builds, deployments, backups, restores), telling people apart from schedules, GitHub pushes and the panel, with filters, a zoomable chart, a report and CSV export. See [Activity](../05-observe/08-activity.md) |
| **Support** | Support tickets, when your account may use them |
| **Settings** | Your profile, appearance, notifications, workspaces, connected accounts, security, and SFTP and API keys |
| **Administration** | Users, workspaces, sites, host, capacity, logs, panel settings, nodes, the environment editor and diagnostics (administrators) |

The **workspace switcher** in the sidebar scopes the Overview, Apps and the
default target of **New** to one workspace, or shows all of them. Those pages
name the workspace beside their title; Activity notes that it shows every
workspace. See [Workspaces](../06-teams/01-workspaces.md).

One **Help & resources** menu holds this documentation, the help center, the
public status page (when one is published), the Automation API, Diagnostics
(with `system.view`), a product overview and the keyboard shortcuts.
Notifications open from the bell in the top bar.

### Overview

The Overview page has one **New** action and four capacity numbers with
precise bars. Administrators see host memory, CPU and disk; everyone else
sees memory and CPU reserved, and apps, against the workspace's limits, plus
how many are running out of the total.

Below them are the six most relevant bots, game servers (with address and
players) and containers, each with start, stop and an actions menu, and how
many websites are live. An empty table shows one quiet row with a link to
create the first item.

### Apps

**All** shows bots, game servers and containers in one table, then stacks and
websites. **Bots**, **Game servers**, **Containers** (with their stacks) and
**Websites** show one kind with the columns that fit it.

- Search, filters for state, runtime and owner, tags and favourites, all kept
  in the URL.
- A summary of running apps.
- Batch **Start**, **Stop** or **Restart** of up to 50 apps, with a review
  step and a result per app.

The older `/bots`, `/servers` and `/sites` lists redirect here with their
filter.

### New

**New** in the top bar (and the primary action of the Overview and Apps
headers) lists the kinds of app your role and the panel's enabled modules
allow:

| Group | Choices |
|---|---|
| Bot | From GitHub, From a template, Empty bot |
| Game server | Minecraft server, Steam game server, Browse all server types |
| Container | One-click app, Docker image, Compose stack |
| Website | Upload files, From GitHub, From a template |

The menu also links to **New** (`/new`), which asks "What are you running?"
and explains each kind. Account and administration chores (workspace,
support ticket, API client, invitation, node, server type, help article,
status incident, announcement) are not in the menu: they are on `/new` under
**Also** and are "New …" actions in Go to. `/new` also links to
**Templates** (`/templates`), which compares every starter side by side.

### App pages

Bots, game servers and containers share one page layout; a website's web app
shows the same tabs inside its site.

- The header has the name, the exact lifecycle state and **Start**,
  **Restart**, **Stop** and **Kill**. A waiting, failing or in-progress state
  gets one compact line.
- One strip shows live CPU, memory, disk and network, plus a game server's
  address and players.
- A failed app offers **Ask AI why**.

Every kind uses the same tab names for the same things:

| Tab | Holds |
|---|---|
| **Overview** | Console, status, the setup checklist for bots, recent activity, log history, source, backups and details |
| **Files**, **Deployments**, **Packages** | Code and how it arrives |
| **Configuration** | A bot's command, build and restart policy; a game server's type, version and JVM arguments; a container's image, command, volumes and writable paths |
| **Variables**, **Network** | Environment variables and ports |
| **Services** | Companion databases |
| **Backups**, **Schedules** | Data and timetables |
| **Health**, **Analytics** | Observability |
| **Settings**, **Access** | Behind the gear: name, memory and CPU, sharing |

Kinds add their own tabs: **Players**, **Properties** and **Mods** or
**Plugins** for game servers, **Public page** for bots and **Terminal** for
containers. With more than seven tabs, the top row shows the groups Overview,
Code (Content for game servers), Configuration, Services, Data and
Observability, and the open group's tabs appear as a second row.

Phones show one column, console first. Unsaved edits are protected when you
navigate away. Older `?tab=` names in links (`overview`, `console`, `logs`,
`deployments`, `configuration`, `variables`, `databases`, `services`,
`health`, `access` and others) still open the right tab.

### Go to (Ctrl+K)

**Ctrl+K** opens the Go to palette. It finds pages, administration sections,
environment variables, docs, apps and their sections, websites, workspaces
(**Switch to …** and their settings), people, AI chats and actions.

- Several words narrow the search.
- **Tab** switches the scope.
- Starting with `>` lists actions only.

### Ask AI

When an administrator has configured a provider, **Ask AI** in the top bar
(or **Ctrl+.**) opens the assistant on every page. It reads console output,
build logs and files to answer questions such as "why did it crash?", and
only changes things after you approve. See
[AI assistant](../07-operate/10-ai-assistant.md).

### Themes

The sun/moon/monitor button in the header cycles Light → Dark → Match the
system, and the browser remembers the choice. Accent colours and corner
styles are under **Settings → Appearance**; see
[Appearance](../06-teams/06-appearance.md).

## Where it stops

Some limits are worth knowing before you rely on RivetPanel:

- The panel holds the Docker socket, which is root-equivalent on the host.
  See [Security model](../07-operate/08-security.md).
- Egress and disk quotas are not enforced. [not enforced] Only an app's size
  (memory, CPU, processes) is a kernel limit, set by Docker. [enforced: docker]
  See [Limits](../06-teams/04-limits.md).
- Remote placement is manual: there is no capacity-based scheduler, live
  migration or high availability.
- Previews are opt-in and still settling. The
  [implementation status](../../implementation-status.md) lists what is done
  and what is not.
