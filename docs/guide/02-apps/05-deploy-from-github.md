---
title: Deploy from GitHub
lede: Link a bot to a GitHub branch, let the panel work out how to run it, and redeploy on every push with rollback when a release misbehaves.
icon: github
aliases: [deploy]
glance:
  - No GitHub account needed for public repositories
  - Polls every 5 minutes without a webhook
  - Analysis reads up to 4,000 paths
related: [apps/publish-to-github, apps/builds-and-logos, websites/from-github, operate/remote-nodes]
---

## Ways to get code in

| Source | How |
|---|---|
| Manual | Upload or edit files in the browser or over SFTP. See [Files, SFTP and packages](02-files.md) |
| GitHub | Link a repository, branch and optional root folder. The panel downloads an archive of the commit, so `git` is never installed on the host |

## Deploy a repository

1. Choose **New → Bot → From GitHub**.
2. Paste `owner/name` or a GitHub link. A `/tree/<branch>/<folder>` link fills
   in the branch and folder too.
3. Press **Analyze repository** to fill in the form (below), then review it.
4. Tick **Start the bot after the first deployment** if the bot should start
   once its files are in place. Otherwise it is created stopped.
5. Create the bot.

Any public repository works without a GitHub connection. Connect GitHub under
**Settings → Connected accounts** for:

- private repositories;
- the list of your own repositories;
- [publishing and pushing](06-publish-to-github.md);
- a higher rate limit: 5,000 requests per hour instead of 60 anonymous ones
  shared by the panel's address.

When the connection exists, it is used for public repositories as well.

## Analyze a repository

**Analyze repository** downloads the branch once and suggests the language,
start command, build command, variables (with descriptions, required and
secret flags, and safe defaults), databases and resources, plus setup steps
such as privileged intents. Its sources, in order:

1. **Verified recipes** for well-known bots (Red-DiscordBot, YAGPDB), tested
   on a real panel. They are offered as one-click buttons under
   **Popular open-source bots**.
2. **Detection** from the repository's files (below).
3. **Refine with AI**, when an administrator configured an AI provider. File
   excerpts (at most about 60 KB: manifests, example configuration, entry
   files, the README) and the detected plan are sent to the provider, which
   returns a plan as JSON. Repository text is treated as untrusted data in
   the prompt.

| Detected from | Gives |
|---|---|
| `package.json` | Start script, TypeScript build, Yarn or pnpm |
| `requirements.txt`, `pyproject.toml`, `setup.py`, `Pipfile` | A Python project |
| `go.mod` and its `package main` folders (`cmd/<name>`) | A Go project |
| `Cargo.toml`, Maven or Gradle files, `Gemfile` | Rust, Java or Ruby projects |
| `.env.example`-style files | Variables; comments become descriptions, and example values are kept only when they are not placeholders or secrets |
| Variables the code reads (`process.env.X`, `os.getenv`, `os.Getenv`, `env::var`, …) | More variables |
| Database clients and docker-compose images | Suggested [services](07-services.md): PostgreSQL, Redis, MongoDB or MariaDB/MySQL |
| Dockerfile `CMD` and `EXPOSE` | Start command and ports |

Every suggestion only prefills the form: nothing is created or run until you
create the bot. The server checks suggestions again: [enforced: panel]

- start commands must be allowed for the runtime;
- unknown services and reserved variables are dropped;
- secret values are never prefilled;
- resources are clamped to your limits.

> [!WARNING]
> Review a suggested build command before creating the bot: it runs in the
> build container, with internet access.

| Analysis limit | Value |
|---|---|
| Archive size read | 512 MiB compressed |
| Paths inspected | 4,000 |
| Source inspected | 48 MiB |
| Variables listed | 60 |

## Deployments tab

The bot's **Deployments** tab shows:

- the current release;
- **What changed?**: the commits and files between the deployed commit and
  the branch;
- a history with **Redeploy**, and **Roll back to this** for an earlier
  commit.

A failed build keeps the previous generation available. The operation
history shows the exact stage and output.

### Safe file replacement

Replacing the files is crash-safe:

- Replaced files are moved aside, and a journal outside the workspace records
  the plan before the first rename.
- If the panel dies midway, the next start rolls the workspace back to the
  previous files.
- The deployed commit is recorded before the previous files are dropped.

A **Stop**, **Delete** or **Unlink** that arrives during a deployment wins.

## Automatic deploys

| Condition | How new commits arrive |
|---|---|
| Your GitHub connection has admin rights on the repository, and the panel has a public address | A push webhook, authenticated with HMAC-SHA256 [enforced: panel] |
| Otherwise, for example someone else's public repository | The panel polls the branch every five minutes |

Unauthenticated webhook deliveries always get the same `401`. Polling uses
the linking account's token when there is one, and deploys a new head once:
a failing commit is not retried every interval.

Submodules and Git LFS are not supported.

## Bots on a remote node

For a bot on a remote node, the panel downloads the archive (at most 1 GiB)
and streams it to the node over the authenticated agent connection. Nothing
is unpacked on the panel's host.

1. The node stages the whole archive. Nothing is staged when the node lacks
   free disk space.
2. The panel checks once more that the bot and its repository link are
   unchanged.
3. The node swaps the files and keeps the previous ones until the panel has
   recorded the commit. A failure, a lost confirmation or an agent crash rolls
   back.

Manual deploys to an offline node are refused. Polling resumes when the agent
reconnects. A webhook push received while the node is offline waits (the
**Deployments** tab says so) and deploys the newest commit once the agent
reconnects, unless the repository link changed meanwhile. See
[Remote nodes](../07-operate/04-remote-nodes.md).
