# RivetPanel documentation

<!-- Generated from docs/guide by `make docs-index`; edit the guide, not this file. -->

RivetPanel runs Discord bots and other applications, Minecraft and Steam game
servers, Docker containers and Compose stacks, and websites. This guide is the
same one the panel shows under **Help → Documentation**. The authoritative
version is in the root `VERSION` file; release notes are in
[CHANGELOG.md](../CHANGELOG.md).

## Get started

What RivetPanel runs, your first app, and the ideas the rest of the guide builds on.

- [Overview](guide/01-get-started/01-overview.md): What RivetPanel runs for you, and where everything is in the interface.
- [Quick start](guide/01-get-started/02-quick-start.md): Create, configure and start your first app in a few minutes.
- [Concepts](guide/01-get-started/03-concepts.md): The ideas the rest of the guide builds on, from apps and workspaces to how the panel keeps containers in line with what you asked for.
- [What's new](guide/01-get-started/04-whats-new.md): Which version of RivetPanel you run, and where every change is recorded.

## Apps

Bots and other processes: runtimes, files, variables, builds, GitHub, databases and containers.

- [Bots and processes](guide/02-apps/01-bots.md): Create a bot or any long-running process, start and stop it, choose what runs and decide what happens when it crashes.
- [Files, SFTP and packages](guide/02-apps/02-files.md): Edit an app's files in the browser or over SFTP, and manage its dependencies without touching a manifest by hand.
- [Variables and network](guide/02-apps/03-environment.md): Give an app its tokens and settings safely, and decide whether it can reach the internet and which ports it publishes.
- [Builds and logos](guide/02-apps/04-builds-and-logos.md): Replace a runtime's default build step for larger projects, understand why a build failed, and give bots, sites and workspaces their own picture.
- [Deploy from GitHub](guide/02-apps/05-deploy-from-github.md): Link a bot to a GitHub branch, let the panel work out how to run it, and redeploy on every push with rollback when a release misbehaves.
- [Publish to GitHub](guide/02-apps/06-publish-to-github.md): Turn a bot's files into a new GitHub repository, and push later changes made in the panel or over SFTP back to its branch.
- [Services](guide/02-apps/07-services.md): Run PostgreSQL, Redis, MongoDB or MariaDB next to an app, on a private network, with the connection variables filled in for you.
- [Containers and stacks](guide/02-apps/08-containers.md): Run any Docker image, import a Compose file as a stack of services, or install a ready-made self-hosted app in one click.
- [Scheduled actions](guide/02-apps/09-scheduled-actions.md): Back up, start, stop, restart or redeploy an app on a timetable, alone or as a chain of steps.

## Game servers

Minecraft and Steam servers: types, ports, players, mods, worlds and schedules.

- [Game servers](guide/03-game-servers/01-overview.md): Create a Minecraft or Steam game server in four steps, then run it with the same console, files and backups as a bot.
- [Steam games](guide/03-game-servers/02-steam.md): Run a Valheim, Rust or Project Zomboid dedicated server that the panel downloads and updates with SteamCMD, without a Steam account.
- [Pterodactyl eggs](guide/03-game-servers/03-pterodactyl-eggs.md) (administrators): Convert a Pterodactyl egg into a RivetPanel server type, review what could not be mapped, and save it only when it is right.
- [Ports and allocations](guide/03-game-servers/04-ports.md): Give players an address that works, add ports for plugins, and recover when another container already holds a server's port.
- [Players, properties and mods](guide/03-game-servers/05-players-and-mods.md): Manage who can join, edit the common server.properties settings, and install Modrinth mods and plugins that fit your server.
- [Worlds, console and schedules](guide/03-game-servers/06-worlds-and-schedules.md): Move worlds in and out as archives, send console commands, stop servers without losing the world, and chain tasks such as a warned restart.
- [Memory, Java and JVM arguments](guide/03-game-servers/07-memory-and-jvm.md): Understand how a Java server's heap follows its memory limit, which Java version it gets, and how to add JVM options such as Aikar's flags safely.
- [Game server administration](guide/03-game-servers/08-administration.md) (administrators): Manage server types and their revisions, write your own blueprints, and know which game server controls are enforced and where.

## Websites

Bot pages, static sites and running web apps, with releases, domains and TLS.

- [Sites and bot pages](guide/04-websites/01-bot-sites.md): Give a Discord bot a public page with Page Studio, or host any website next to your bots, on host names kept apart from the panel.
- [Sites from GitHub](guide/04-websites/02-from-github.md): Build a static site on every push, or run a Node.js, PHP, Python, Go or Rust app with persistent data, databases and health checks.
- [Publishing and releases](guide/04-websites/03-publishing.md): Start a site from a template, publish files from the draft editor, a ZIP or a GitHub branch, and roll back to an earlier release in one click.
- [Addresses and domains](guide/04-websites/04-domains.md): Change a site's address, serve it on your own domain, and (as an administrator) set up sites domains, DNS and the reverse proxy.
- [Redirects and headers](guide/04-websites/05-redirects-and-headers.md): Add redirects, rewrites and response headers to a static site with _redirects and _headers files in the Netlify and Cloudflare Pages formats.
- [Site administration](guide/04-websites/06-administration.md) (administrators): Know who can do what with a site, suspend one without deleting it, understand the isolation limits, and drive sites through the API.

## Observe

Widgets, telemetry, health checks, usage, logs and the notifications they raise.

- [Dashboard widgets](guide/05-observe/01-widgets.md): Build a bot dashboard from your bot's own code, and choose whether its widgets also appear on the bot's public page.
- [Widget data shapes](guide/05-observe/02-widget-data.md): The exact data object each of the 18 widget kinds expects, with an example you can paste into a push.
- [Bot telemetry](guide/05-observe/03-telemetry.md): Send stats, command counts, events and widgets from your bot to its private insights, within limits that keep one bot from flooding the panel.
- [Health and alerts](guide/05-observe/04-health.md): Know whether your bot's code is alive and reachable, not just whether its container runs, and get told when that changes.
- [Usage analytics](guide/05-observe/05-usage.md): See how much CPU, memory and network each app used, how often it was up, crashed, deployed and backed up, and the same totals across the whole panel.
- [Log files and retention](guide/05-observe/06-logs.md): Find and download the console output of any day, and set how long logs and graph data are kept.
- [Notifications](guide/05-observe/07-notifications.md): Choose what the panel tells you about and where: the bell in the top bar, email, or a Discord channel.
- [Activity](guide/05-observe/08-activity.md): See everything that happened on your apps in one timeline, who or what did it, and a report of any time window.

## Teams and access

Workspaces, roles, sign-in, limits and the support desk.

- [Workspaces](guide/06-teams/01-workspaces.md): Group apps and sites for yourself or a team, give people a role in them, and narrow the panel to one workspace at a time.
- [Roles and permissions](guide/06-teams/02-roles-and-permissions.md): Decide what each account may do with a role, share single apps with other people, and delegate parts of the administration safely.
- [Sign-in and accounts](guide/06-teams/03-sign-in.md): Sign in with a password, GitHub, Discord, single sign-on or a passkey, protect the account with two-step sign-in, and give programs their own scoped API credentials.
- [How limits work](guide/06-teams/04-limits.md): Know which limit stops an app from being created, resized, moved or started, where each one is set, and who is exempt.
- [Support desk, help center and status page](guide/06-teams/05-support.md): Ask for help with a ticket, answer tickets as staff, publish help articles, and show the public how your services are doing.
- [Appearance](guide/06-teams/06-appearance.md): Choose the theme, accent color, corner style and support ticket layout that this browser uses.

## Operate

Install, configure, secure, back up and extend a RivetPanel installation.

- [Install RivetPanel](guide/07-operate/01-install.md) (administrators): Put RivetPanel on a Linux host with systemd or Docker Compose, put HTTPS in front of it and create the first administrator.
- [Configuration](guide/07-operate/02-configuration.md) (administrators): Every RIVET_ environment variable, the optional services they switch on, and who bots run as on the host.
- [Upgrades and rollback](guide/07-operate/03-upgrades.md) (administrators): Upgrade RivetPanel so that every upgrade can be undone by restoring the backup you took just before it.
- [Remote nodes](guide/07-operate/04-remote-nodes.md) (administrators): Run bots and game servers on other Linux hosts through rivet-agent, which connects out to the panel so the node needs no inbound management port.
- [Host, logs and environment](guide/07-operate/05-host-and-logs.md) (administrators): See what the machine and each app are using, read the panel's own log, and change RIVET_ settings and modules from the browser.
- [Email](guide/07-operate/06-email.md) (administrators): Send password resets, invitations, alerts, security notices and announcements through Mailgun, or run without email at all.
- [Backups and restore](guide/07-operate/07-backups.md) (administrators): Back up the whole installation from the host, back up each app from its Backups tab, and restore either without losing your secrets.
- [Security and isolation](guide/07-operate/08-security.md) (administrators): What separates apps from each other and from the host, which layer enforces each boundary, and what you must add yourself.
- [Extensions](guide/07-operate/09-extensions.md) (administrators): Add signed, sandboxed features to the panel, such as Discord insights, Discord ChatOps and a database browser, and write your own.
- [AI assistant](guide/07-operate/10-ai-assistant.md): Ask one private chat about whatever you are looking at, and let it read logs and files, propose fixes and run checks, with every change waiting for your approval.
- [Automation API](guide/07-operate/11-automation-api.md): Let scripts and CI pipelines check, restart, deploy and back up your bots with scoped tokens that never outrank you.
- [Diagnostics and footprint](guide/07-operate/12-diagnostics.md) (administrators): Check the installation's health from the browser or the shell, and know how much memory the panel itself uses.

## Project

- [Implementation status](implementation-status.md): completed and remaining platform-overhaul work
- [Writing the guide](guide/WRITING.md): layout and conventions for these pages
