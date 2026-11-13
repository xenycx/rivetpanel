---
title: Bots and processes
lede: Create a bot or any long-running process, start and stop it, choose what runs and decide what happens when it crashes.
icon: terminal
glance:
  - 7 runtimes, 7 starter templates
  - Retries back off up to 5 minutes
  - 256 KiB of output kept per build
related: [apps/builds-and-logos, apps/environment, apps/deploy-from-github, observe/health]
---

## Create a bot

**New → Bot** offers three starting points. All of them open `/bots/new`.

| Start from | What you get |
|---|---|
| **From a template** | A working slash-command bot; you only add the token |
| **From GitHub** | Any public repository or one of your own, analyzed for you. See [Deploy from GitHub](05-deploy-from-github.md) |
| **Empty bot** | A language and an empty workspace; upload your code through **Files** or SFTP |

The form asks for required values such as the token up front, then shows the
memory, CPU and build memory for review. Administrators can place an empty
bot on any enabled node that is not draining. Tick **Start the bot after
creating it** to start it straight away; otherwise it is created stopped.

Despite the name, a bot can be any long-running program in a supported
runtime: Node.js, Python, Rust, Go, Java, Ruby or PHP.

## Templates

Seven bot starters are built into the panel and copied into a new bot:

| Template | Runtime | Notes |
|---|---|---|
| discord.js | Node.js 24 | CommonJS, RivetPanel SDK included |
| discord.js TypeScript | Node.js 24 | `src/index.mts` runs directly through Node's built-in type stripping, with no build step; `npm run check` type-checks locally |
| discord.py | Python | RivetPanel SDK included |
| Poise (Serenity) | Rust | Builds in a 3 GiB build container |
| JDA | Java | Maven is fetched and verified (pinned SHA-512) in the build container |
| DiscordGo | Go | |
| discordrb | Ruby 4 | `bundle install` into `vendor/bundle`; RivetPanel SDK included |

Each starter reads `DISCORD_TOKEN`, exits with a clear message when it is
missing, and registers `/ping`. A template also lists its required variables,
setup steps, privileged intents and tested versions, and may choose its own
start command (the TypeScript starter runs `node src/index.mts`).

Build containers get their own memory floor from the runtime recipe
(`build_memory_bytes`), used only while building.

### The Templates page

**Templates** (`/templates`, linked from `/new` and found with Ctrl+K)
gathers every starting point. One search box covers the open tab and points
to matches in the others. Each tab appears only when its module is on.

| Tab | Shows | Action |
|---|---|---|
| **Bots** | The seven bot starters | **Create bot** (`bots.create`) |
| **Game servers** | Every server type you may create: built-in Minecraft and Steam types, and Pterodactyl eggs imported by administrators (marked "Imported"), with game icons and a short description | **Create server** opens the new-server wizard on that type |
| **Apps** | The one-click app catalog | See [Containers and stacks](08-containers.md#one-click-apps) |
| **Websites** | The five site templates, with a scaled, sandboxed preview (no scripts; a larger preview switches between a template's pages) and tags | **Create site** (`sites.create`) |

Administrators with `blueprints.manage` also get **Import egg** and a link to
the server types on the **Game servers** tab. **Create site** asks for the
name, address and workspace, like **Apps → Websites → Upload files**, and
publishes the template as the site's first release.

## Lifecycle and power controls

| Control | What it does |
|---|---|
| **Start** | Builds if needed and starts the bot |
| **Restart** | Replaces the container |
| **Stop** | Stops the bot |
| **Kill** | Sends `SIGKILL` at once, after recording that the bot should be stopped |

The bot header shows one exact phase, such as starting, building, running,
retrying, failed, exited, stopping or stopped, along with the restart count,
the next retry time and a reason. See
[Concepts](../01-get-started/03-concepts.md#phases) for every phase.

## Resource gauges

Live CPU, memory, disk and network gauges relay Docker's stats stream over
Server-Sent Events. Disk is the size of the bot's workspace next to the free
space on its node; there are no per-bot disk quotas. [not enforced]

Memory and CPU are changed under **Settings**, and Docker holds the container
to them. [enforced: docker]

## Configuration

**Configuration** edits the runtime, the start command and an optional
entrypoint. The bot must be stopped.

Only programs approved for the runtime in its recipe (`runtimes/*.yaml`) are
accepted as the command, and the command runs directly, never through a
shell. [enforced: panel] For example, a Node.js bot may start `node`, `npm`
or `npx`.

An optional **Build command** replaces the runtime's default build step. See
[Builds and logos](04-builds-and-logos.md).

## Restart policy

The **Restart policy** decides what happens when the bot's process exits.

| Policy | Behaviour |
|---|---|
| `on_failure` (default) | Restarts after a non-zero exit, with exponential backoff and a limit on crashes in a row (0 means unlimited) |
| `never` | Leaves the bot down |

- A clean exit (code 0) is never restarted.
- The backoff resets after a minute of stable running.
- When the runner gives up, the bot shows *failed* with the reason, and
  **Start** tries again with a fresh crash budget.

This is separate from **Restart when unhealthy** on the **Health** tab. See
[Health and alerts](../05-observe/04-health.md#restart-when-unhealthy).

## Build records

Every build is recorded with its output and shown live while it runs. The
panel keeps 256 KiB of output per build. **Activity** and the bot's
operation history show the exact stage and output of each one.
