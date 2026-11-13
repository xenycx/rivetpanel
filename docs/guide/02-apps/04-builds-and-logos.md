---
title: Builds and logos
lede: Replace a runtime's default build step for larger projects, understand why a build failed, and give bots, sites and workspaces their own picture.
icon: package
aliases: [build]
glance:
  - Build commands up to 4 KiB
  - Retries stop after 3 identical failures
  - Logos stored up to 256 KiB
related: [apps/bots, apps/files, apps/deploy-from-github, teams/workspaces]
---

## Build commands

Each runtime has a default build step, such as installing dependencies. A
**Build command** under **Configuration** replaces it, for example:

```sh
cd cmd/mybot && go build -o /workspace/app .
```

```sh
npm ci && npm run build
```

The command runs before every start in the runtime's build container:

- through `sh -c` with `set -e`, so it stops at the first failing line;
- with the same resource limits as the bot and with internet access;
- without the bot's variables. [enforced: panel]

These are the same privileges the package managers' install scripts already
have there. A build command is at most 4 KiB.

> [!WARNING]
> A build command runs code with internet access on your host. Review one that
> a repository analysis suggested before creating the bot.

### Default builds

| Runtime | Default build |
|---|---|
| Node.js | `npm ci` when `package-lock.json` exists, else `npm install`. A lockfile that no longer matches `package.json` falls back to one `npm install`; see [Lockfiles](02-files.md#lockfiles) |
| Python | Creates a virtual environment in `.venv` and installs `requirements.txt` into it; without one, installs a packaged project (`setup.py`, or `pyproject.toml` with `[project]` or Poetry metadata) |

The Rust recipe reserves a 3 GiB build container, because `rustc` is killed
at 1.5 GiB without swap.

## When a build fails

A failed build reports its exit code and an excerpt of the builder's output:

- for npm, npm's own error code and reason lines, with a hint when
  `package.json` and `package-lock.json` are out of sync, instead of the usage
  text npm prints after them;
- otherwise, the last lines of output.

A build that ran and failed is retried after 15 seconds, then 30, doubling up
to 5 minutes. When the same generation fails the same way three times in a
row, the panel stops retrying and says so in the bot's error. **Start**,
**Restart** or a configuration change builds again, and a panel restart also
allows one new attempt.

A failed build keeps the previous generation available. The operation
history shows the exact stage and output; see
[Build records](01-bots.md#build-records).

## Logos

Bots, sites and workspaces can have a custom logo.

1. Open the logo: **Settings → Logo** of a bot or a site, or the picture
   beside a workspace's name under **Settings → Workspaces** or
   **Administration → Workspaces**.
2. Choose a PNG, JPEG, WebP or GIF image of up to 8 MB.
3. The browser crops it to a square of at most 256×256 pixels and stores it
   as a PNG, or as a JPEG when a PNG would be too large.

The panel accepts only PNG or JPEG images between 16 and 512 pixels a side
and up to 256 KiB. [enforced: panel] Logos are served only to signed-in
people with access, sandboxed and without content sniffing.

| Who may change a logo | Of |
|---|---|
| Full admin on the bot | A bot |
| Developer on the site | A site |
| Whoever may rename the workspace | A workspace |

### What is shown without a logo

| Item | Shown, in order |
|---|---|
| Bot | Its custom logo, else its Discord avatar, else a generated picture |
| Site | Its custom logo, else the favicon of its active release, else its bot's logo, else a generated picture |
| Container | Its custom logo, else a generated picture |
| Workspace or person | Its uploaded logo or profile picture, else a generated picture |
| Game server | Its custom logo, else its game's icon |

A bot's Discord avatar arrives through the telemetry SDK, or from
**Get avatar from Discord** in the bot's **Settings**. That button asks
Discord's API for the bot user, with the token found in the bot's variables
(`DISCORD_TOKEN`, `BOT_TOKEN`, `TOKEN`, `RED_TOKEN`, `YAGPDB_BOTTOKEN` and
others, or any value shaped like a bot token). The token is sent only to
Discord.

A site's favicon is the `<link rel="icon">` in its `index.html`, or
`favicon.ico`, `favicon.png` or `favicon.svg`.

If an uploaded logo, profile picture, Discord avatar or favicon fails to load,
the generated picture is shown in its place.

### Generated pictures

Anything without an image of its own gets a generated
[DiceBear](https://www.dicebear.com) picture (all CC0 1.0 by DiceBear):

| Item | Style |
|---|---|
| Bots | Bottts Neutral (robots, by Pablo Stanley) |
| Workspaces | Disco |
| Containers and one-click apps | Shapes |
| Sites | Glass |
| People | Thumbs |

The picture is drawn in the browser from the item's ID, so it survives a
rename and no request leaves the panel for it.

**New picture**, the shuffle button next to a logo, draws a different
generated picture for everyone. It is in a bot's and a site's **Settings**, a
workspace's logo menu, **Settings → Profile**, and an account's page under
**Administration → Users**. An uploaded image always wins over it.

It needs the same right as changing that item's logo. People change their
own picture, and `users.manage` changes anyone's. [enforced: panel]

### Stored variants and routes

The choice is stored per item as `logo_variant` (migration `0066`). While
the variant is 0 the picture is seeded with the ID, so existing pictures did
not change; after that it is seeded with `<id>:<variant>`.

| Route | Audit event |
|---|---|
| `POST /bots/{id}/logo/shuffle` | `bot.logo_shuffle` |
| `POST /sites/{id}/logo/shuffle` | `site.logo_shuffle` |
| `POST /workspaces/{id}/logo/shuffle` | `workspace.logo_shuffle` |
| `POST /me/avatar/shuffle` | `account.avatar_shuffle` |
| `POST /users/{id}/avatar/shuffle` | `admin.user_avatar_shuffle` |

Each route answers `{logo_variant}`.
