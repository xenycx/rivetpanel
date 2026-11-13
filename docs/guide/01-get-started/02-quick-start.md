---
title: Quick start
lede: Create, configure and start your first app in a few minutes.
icon: play
aliases: [quickstart]
related: [apps/bots, apps/environment, game-servers/overview, websites/bot-sites]
---

## Before you start

You need a running panel and an account on it. If you are setting the panel
up yourself, start with [Install](../07-operate/01-install.md).

## Create a bot from a template

A template is a tested starting point that runs as soon as it has a token.

1. Press **New** in the top bar, or open `/new`, and choose
   **Bot → From a template**.
2. Pick a starter, for example **discord.js** or **discord.py**.
3. Enter the required values the form asks for up front, such as
   `DISCORD_TOKEN`.
4. Review the memory, CPU and build memory, then create the bot. Tick
   **Start the bot after creating it** to skip the next step.
5. Press **Start** on the bot's page.
6. Follow the build output and the live console on the bot's **Overview** tab.

Every bot starter registers a `/ping` command, so you can check it in your
Discord server right away.

> [!TIP]
> To compare starters for bots, game servers, one-click apps and websites side
> by side, open **Templates** from the link on `/new`.

## Bring your own code

- **From GitHub**: choose **Bot → From GitHub** and paste `owner/name` or a
  repository link. **Analyze repository** fills in the language, commands and
  variables for you. See [Deploy from GitHub](../02-apps/05-deploy-from-github.md).
- **Empty bot**: choose **Bot → Empty bot**, pick a language, then upload a ZIP
  or your files in **Files**, or over SFTP. See [Files and SFTP](../02-apps/02-files.md).

## Add secrets

1. Open the bot's **Variables** tab.
2. Add each secret, such as `DISCORD_TOKEN`, or use **Import .env**.
3. Save, then start the bot, or restart it if it is running. A running bot
   keeps its old values until it restarts.

Values are encrypted at rest and masked in every list; **Reveal** shows one
value for 30 seconds. See [Variables and network](../02-apps/03-environment.md).

## Check the settings

Before you rely on the bot, look at three settings:

| Setting | Where | Why it matters |
|---|---|---|
| Runtime and start command | **Configuration** | Decides what runs |
| Restart policy | **Configuration** | Whether a crashed bot comes back by itself |
| Memory and CPU | **Settings** | The container is held to them |

See [Bots and processes](../02-apps/01-bots.md) for what each one does.

## Other kinds of app

The same **New** menu creates the other kinds:

- **Game server**: a Minecraft or Steam server. See
  [Game servers](../03-game-servers/01-overview.md).
- **Container**: a Docker image, a Compose stack or a one-click app. See
  [Containers and stacks](../02-apps/08-containers.md).
- **Website**: uploaded files, a GitHub repository or a site template. See
  [Websites from GitHub](../04-websites/02-from-github.md) and
  [Bot Sites](../04-websites/01-bot-sites.md).
