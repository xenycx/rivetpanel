---
title: Sites from GitHub
lede: Build a static site on every push, or run a Node.js, PHP, Python, Go or Rust app with persistent data, databases and health checks.
icon: github
aliases: [site-apps]
glance:
  - Builds see no secrets
  - Health check every 30 s, restart after 3 failures
  - Request bodies up to 100 MiB by default
related: [websites/publishing, apps/services, apps/deploy-from-github, websites/administration]
---

## Create a site from a repository

1. Choose **New → Website → From GitHub**. Private repositories work once your
   GitHub account is connected.
2. Pick the repository and branch. The panel downloads the branch, detects the
   framework and proposes a setup.
3. Review the site type, the build and start commands, the output folder or
   port, variables and services.
4. Choose the address and whether **Deploy on every push** is on.
5. Create the site. The first deployment starts at once.

**Deploy on every push** installs a GitHub webhook, or checks the branch every
few minutes when GitHub cannot reach the panel. Everything in the proposal can
be changed before and after creation.

### What the panel proposes

| Framework (preset) | Kind | Build | Serves |
| --- | --- | --- | --- |
| Static files | Static | None | The repository folder as it is |
| Vite (React, Vue, Svelte, Solid…) | Static build | `npm ci && npm run build` | `dist`, single-page app |
| SvelteKit `adapter-static` | Static build | Same | `build` |
| Astro | Static build | Same | `dist` |
| Next.js `output: 'export'` | Static build | Same | `out` |
| Nuxt generate | Static build | `nuxi generate` | `.output/public` |
| Node.js server (Express, Hono, Fastify…) | App | `npm ci` (+ `npm run build`) | `npm start` on `$PORT` |
| SvelteKit `adapter-node` | App | Build, prune dev packages | `node build` |
| Next.js, Nuxt, Astro (Node adapter) | App | Build | The framework's server |
| FastAPI, Flask, Django | App | Virtual environment from `requirements.txt` | uvicorn or gunicorn on port 8000 |
| Go, Rust | App | `go build`, `cargo build --release` | The binary on `$PORT` (8080) |
| Laravel, plain PHP | App | Composer when `composer.json` exists | FrankenPHP on port 8080 (`public/` for Laravel) |

## The site page

Behind the scenes the site gets an **app unit**: its checkout, builds,
deployments, variables, services, logs and resource limits. You manage it from
the site page; it never appears among the bots.

| Tab | What it holds |
|---|---|
| **Overview** | Status and logs |
| **Files** | The checkout, with `/data` as `.rivet-data` |
| **Deployments** | Commits, build output, redeploys |
| **Configuration** | Build and run settings |
| **Variables** | The app's environment |
| **Services** | PostgreSQL, MariaDB, Redis or MongoDB (apps only) |
| **Backups** | Backups that include `/data`, with an **Automatic backups** on/off switch for site admins |

Changing an app's start command, port, memory or build needs the app stopped
[enforced: panel]. Deleting the site deletes the app, its `/data` folder and
its services.

## Builds

Builds run in a throwaway build container with the runtime's image, within
the panel's build allowance (`RIVET_BUILD_*`), one at a time per
`RIVET_MAX_BUILDS`.

Builds get **no secrets** [enforced: panel]. The only variables a build sees
are public ones whose names start with `VITE_`, `PUBLIC_`, `NEXT_PUBLIC_`,
`NUXT_PUBLIC_`, `REACT_APP_` or `GATSBY_`. Frameworks inline these into the
browser bundle, so they are public anyway.

A failed build keeps the current release online and shows its output under
**Deployments**.

### Rolling back

- Static builds publish a new release, so **Serve this** on an earlier release
  rolls back instantly. See [Publishing](03-publishing.md#roll-back).
- Apps roll back by deploying an earlier commit from **Deployments**.

## Apps

Apps run under the same container policy as bots: read-only root file system,
all capabilities dropped, no new privileges, an unprivileged user, and memory,
CPU and process limits [enforced: docker].

An app:

- must listen on `$PORT` (the site's port) on all interfaces; `$HOST` is
  `0.0.0.0`
- keeps persistent files in **`/data`** (`$DATA_DIR`)
- can have a **release command**
- can use services
- gets an HTTP health check
- is restarted on the new code after each deployment

### Persistent data in /data

`/data` survives deployments and restarts, is included in the site's backups,
and can be browsed under **Files** as `.rivet-data`. Repositories can never
write into it [enforced: panel]. Everything else in the checkout is replaced
on each deployment.

### Release command and services

A release command runs before every start with the app's variables and
services, for example `php artisan migrate --force` or
`npx prisma migrate deploy`. A failure stops the start.

Apps can use the same services as bots (PostgreSQL, MariaDB, Redis, MongoDB),
on a private network, with their connection variables added for you. See
[Services](../02-apps/07-services.md).

### Health check

The panel checks the app's health path over HTTP every 30 seconds, with a
5-second timeout and a 60-second grace period after a start. Three failures in
a row restart the app.

While an app builds or starts, visitors get a **Deploying…** page that reloads
itself.

### What the sites listener passes to an app

The sites listener forwards every method, WebSockets and server-sent events to
the app.

- It keeps the visitor's `Host`.
- It replaces `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Host` and
  `X-Forwarded-Proto` with values it checked. Incoming ones are believed only
  from `RIVET_SITES_TRUSTED_PROXIES` [enforced: panel].
- It refuses request bodies over the site's limit with `413`
  [enforced: panel]. The default is `RIVET_SITES_MAX_BODY_BYTES` (100 MiB).
- It answers `502` when the app does not respond.
- The app's own response headers are passed through unchanged.

For proxies in front of the listener, see
[Apps behind the proxy](04-domains.md#apps-behind-the-proxy).

### PHP and Laravel

PHP apps run [FrankenPHP](https://frankenphp.dev). Its image ships the binary
with a file capability that the kernel refuses to run under
no-new-privileges, so the build copies the binary into the checkout
(`.rivet-bin/frankenphp`) and the app runs that copy on port 8080.

Laravel's `storage/` and `bootstrap/cache` are inside the checkout and
writable. Point SQLite databases or uploads at `/data`, because the checkout
is replaced on every deployment.

## Limits

- Apps run on the panel's own node only, not on remote nodes.
- A redeploy restarts the app on the new code: visitors see the **Deploying…**
  page for the build and start time. There is no blue-green switch yet.
- Apps share one Docker network with each other; see
  [What sites do not do](06-administration.md#what-sites-do-not-do).
