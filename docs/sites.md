# Site hosting

RivetPanel hosts websites next to your bots, in three kinds:

* **Static files**: HTML, CSS, JavaScript and images served exactly as
  uploaded, edited in the panel, or taken from a GitHub branch.
* **Static builds**: a GitHub repository is built on the server (`npm run
  build` for Vite, SvelteKit, Astro, Next.js export, Nuxt generate…) on every
  push, and the output folder is published.
* **Apps**: a server process from a GitHub repository (Node.js and TypeScript,
  PHP and Laravel, Python, Go, Rust) runs in a container and receives every
  request for the site's addresses, with variables, a persistent `/data`
  folder, optional databases and logs.

All three get the same addresses, custom domains and access rules. See
[Sites from GitHub: builds and apps](#sites-from-github-builds-and-apps).

## Bot Sites and Page Studio

Every Discord bot can own one site directly. Open the bot and choose **Public
page**; the old Analytics destination now opens Page Studio by default, with
private telemetry retained under **Private insights**.

There are two presentation modes on the same address:

* **Generated page** publishes immediately from structured settings: headline,
  introduction, Midnight/Daylight/system theme, accent color, a custom HTML
  section, custom CSS, and optional bot widgets.
* **Custom files** serves an immutable release of HTML, CSS, JavaScript, images
  and other static assets. **Site files** is a contained private draft with the
  same explorer and conflict-aware editor used for bot files. Saving does not
  alter the live site; **Publish draft** snapshots and activates it.

Switching mode never removes the generated-page settings or file releases.
Domains and the public address remain the same. Use **Address, domains &
releases** from Page Studio to change the address, and for DNS, GitHub
deployments, ZIP uploads, rollback and settings.

Generated pages may expose declarative bot widgets, but the switch is off by
default. Enabling it publishes only the widget title, layout and validated
payload. It never publishes raw stat series, command names/counts, events,
console output, environment variables, owner email, workspace membership or
deployment details. Custom CSS can target one widget with
`[data-widget="latency"]` or every renderer of a kind with `.kind-metric`.

Custom HTML, CSS and scripts are powerful and are treated as author code. They
run only on the separate Sites origin described below, including inside Page
Studio's cross-origin preview. They never execute in the authenticated panel.

## How it is served (and why separately)

Sites are served by a **separate listener** (`RIVET_SITES_LISTEN`) that
never serves the panel, and always on **different host names** from the panel.
Site files are arbitrary user HTML and scripts; on the panel's origin they
could read CSRF tokens, register service workers or act as the signed-in
user. On their own host names they cannot.

* Default address: `<slug>.<sites domain>`, for example
  `https://docs.sites.example.com`. The sites domain is the host of
  `RIVET_SITES_BASE_URL` unless the site was placed under another sites
  domain (see [Addresses and sites domains](#addresses-and-sites-domains)).
* Custom domains: any number of verified host names per site (up to 10).
* The listener maps the request's `Host` to the site's current **release** and
  serves files from that directory through a descriptor-contained root: paths
  cannot leave the release, and symlinks cannot point outside it.
* Dot-files and dot-directories (`.env`, `.git/`, …) are never served, even if
  uploaded (`.well-known/` is the exception).
* Static sites answer `GET` and `HEAD` only. HTML is sent with
  `Cache-Control: no-cache` so a new release shows immediately.
  Fingerprinted build output (`_app/immutable/`, `_astro/`, `_next/static/`,
  `_nuxt/`, and hash-named files under `assets/` such as
  `index-B4x9kQ2a.js`) is cached for a year as `immutable`; other files for an
  hour, then served stale for up to a day while they revalidate. Conditional
  and range requests are supported. `_headers` can override all of this.
* Text files (HTML, CSS, JavaScript, JSON, SVG, fonts…) of 1 KiB or more are
  gzipped when a release is published and sent compressed to browsers that
  accept it. `.br` and `.gz` files shipped next to the original (SvelteKit's
  `precompress`, Vite compression plugins) are preferred, brotli first.
* Options for custom-file sites: **single-page application** (unknown paths serve
  `/index.html`) and **clean URLs** (`/about` serves `about.html` or
  `about/index.html`; on by default). A `404.html` at the top level is used for
  missing pages.

Recommendation: use a **separate registrable domain** for sites (like
`github.io` vs `github.com`), for example `example-sites.net`. RivetPanel
refuses configurations where the panel's host would be a site host name, and
in production refuses a panel host equal to the sites domain. A sites domain
that merely shares a parent with the panel (panel `panel.example.com`, sites
`sites.example.com`) works, but sites could then set cookies for
`example.com`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `RIVET_SITES_LISTEN` | off (development: `127.0.0.1:8081`) | address of the sites listener; empty disables hosting |
| `RIVET_SITES_BASE_URL` | development: `http://localhost:8081` | origin whose host is the primary sites domain, e.g. `https://sites.example.com`; its scheme and port apply to every sites domain |
| `RIVET_SITES_DOMAINS` | none | further sites domains, comma-separated (e.g. `pages.example.net,example-sites.org`), trusted without a DNS record |
| `RIVET_SITES_DIR` | `/var/lib/rivetpanel/sites` | releases, one directory per site |
| `RIVET_SITE_MAX_BYTES` | 104857600 (100 MiB) | largest release, uncompressed (1 MiB–4 GiB) |
| `RIVET_MAX_SITES_PER_USER` | 10 | sites an account may create; 0 = unlimited; administrators are exempt |
| `RIVET_SITES_DNS_TARGET` | the site's default host | host name shown as the CNAME target for custom domains |
| `RIVET_APPS_NETWORK` | `rivetpanel-apps` | Docker network app sites run on; the sites listener reaches them over it (a containerised panel joins it at start) |
| `RIVET_SITES_TRUSTED_PROXIES` | none (loopback only) | comma-separated addresses or CIDR ranges of the proxy in front of the sites listener; only their `X-Forwarded-*` and client-address headers reach apps |
| `RIVET_SITES_CLIENT_IP_HEADER` | none (`X-Forwarded-For`) | header from a trusted proxy holding the visitor's address, for example `CF-Connecting-IP` |
| `RIVET_SITES_MAX_BODY_BYTES` | 104857600 (100 MiB) | largest request body an app site accepts unless the site sets its own limit |

ZIP uploads are also bounded by `RIVET_MAX_UPLOAD_BYTES` (compressed size).
Five releases are kept per site (the serving one is never pruned).

In development (`RIVET_ENV=development`) hosting is on by default and sites
are reachable at `http://<slug>.localhost:8081` (browsers resolve
`*.localhost` to the local machine).

## DNS and reverse proxy

1. Create a wildcard DNS record `*.sites.example.com` pointing at the server.
2. Terminate TLS in a reverse proxy on the same host and forward site traffic
   to `RIVET_SITES_LISTEN`. Keep forwarding the panel host to
   `RIVET_LISTEN` as before.

With **Caddy**, on-demand TLS obtains certificates for the wildcard subdomains
and for verified custom domains as visitors arrive. The sites listener answers
Caddy's permission check at `/.well-known/rivetpanel/tls-allowed?domain=…`
(loopback peers only), so certificates are only requested for host names a
live site answers on:

```
{
    on_demand_tls {
        ask http://127.0.0.1:8081/.well-known/rivetpanel/tls-allowed
    }
}

panel.example.com {
    reverse_proxy 127.0.0.1:8080
}

# Every site subdomain and every verified custom domain.
https:// {
    tls {
        on_demand
    }
    reverse_proxy 127.0.0.1:8081
}
```

With nginx or another proxy, use a wildcard certificate for
`*.sites.example.com` (DNS challenge) and add certificates for custom domains
yourself; proxy everything that is not the panel host to the sites listener
with the original `Host` header.

### Several sites domains (Traefik, Cloudflare)

So that a new sites domain needs no proxy change, send every host name the
proxy does not otherwise know to the sites listener at the lowest priority.
The panel decides what a host is: unknown host names get its "No site here"
page. With Traefik's file provider:

```yaml
http:
  routers:
    sites-web:
      entryPoints: [web]
      rule: "HostRegexp(`^[a-z0-9.-]+$`)"
      priority: 1
      service: sites
      middlewares: [to-https]
    sites:
      entryPoints: [websecure]
      rule: "HostRegexp(`^[a-z0-9.-]+$`)"
      priority: 1
      service: sites
      tls: {}
  services:
    sites:
      loadBalancer:
        servers: [{ url: "http://127.0.0.1:8081" }]
```

Give the panel's router (and any other service on the proxy) an explicit
higher priority, for example `priority: 100`, so the catch-all never takes
its host. Remove, or give a priority between the two, any older catch-all
router that would compete with it.

Every sites domain also needs a certificate for `<domain>` and `*.<domain>`.
Behind Cloudflare with SSL mode *Full (strict)*, create a Cloudflare Origin
Certificate for both names in that zone, install it on the server, and list it
under `tls.certificates` in Traefik's dynamic configuration. For domains not
on Cloudflare, use Traefik's ACME DNS-01 resolver for the wildcard, or Caddy's
on-demand TLS above. RivetPanel does not install certificates or edit the proxy
configuration.

Per sites domain, at its DNS provider:

1. `A *` (and `A @`) → the server's address, proxied when on Cloudflare.
2. `TXT _rivetpanel-domain` → `rivetpanel-domain=<token>` (not needed for domains
   in the environment file).
3. Cloudflare: SSL/TLS mode *Full (strict)*. Universal SSL covers the apex and
   one wildcard level, which is all sites use.
4. In the panel: add the domain, then **Check DNS now**. Install the origin
   certificate on the server.

Enforcement boundary: TLS is terminated and certificates are issued by the
reverse proxy. RivetPanel only answers whether a host name is allowed.

### Apps behind the proxy

App sites see the visitor's address and scheme only through the sites
listener. Tell it which peer is your proxy, and (behind Cloudflare) which
header carries the visitor's address:

```
RIVET_SITES_TRUSTED_PROXIES=172.16.0.0/12      # the proxy's Docker network, or its address
RIVET_SITES_CLIENT_IP_HEADER=CF-Connecting-IP  # behind Cloudflare
```

The proxy must pass WebSocket upgrades and must not buffer server-sent events
(Traefik and Caddy do both by default; for nginx set
`proxy_http_version 1.1`, the `Upgrade`/`Connection` headers and
`proxy_buffering off`). Raise the proxy's own body limit if apps accept large
uploads.

When the panel itself runs in a container (the published `compose.yaml`), it
connects its own container to `RIVET_APPS_NETWORK` at every start, so
`compose.yaml` needs no change. If the Docker daemon cannot identify the
panel's container, Diagnostics says so; add the network to the service
yourself:

```yaml
services:
  rivetpanel:
    networks: [default, rivetpanel-apps]
networks:
  rivetpanel-apps:
    external: true
```

A panel installed with systemd reaches the apps' container addresses
directly.

### A domain you already route elsewhere

When the proxy already sends a domain (or a wildcard such as `*.example.com`)
somewhere else, add a router for the site's custom domains with a higher
priority than that rule, pointing at the sites listener:

```yaml
http:
  routers:
    my-sites:
      entryPoints: [websecure]
      rule: "Host(`example.com`) || Host(`www.example.com`) || Host(`map.example.com`)"
      priority: 60
      service: sites
      tls: {}
```

Then add the domains to the sites as custom domains and verify them.

## Addresses and sites domains

A site's address is `<slug>.<sites domain>`. A slug is unique **per sites
domain**, so `docs.sites.example.com` and `docs.pages.example.net` can be two
different sites.

**Changing the address.** Developers and up change a site's slug, and its
sites domain when there is more than one, in the site's **Settings**. The move
is immediate: the old address stops answering at once and any site may take
it, so update links first. Verified custom domains keep working. The same
rules as at creation apply: 3–40 lower-case letters, digits and single
hyphens, not a reserved name (`www`, `api`, `admin`, `panel`, `mail`, …), and
not already used under that sites domain.

**Sites domains.** Administrators manage them in **Administration → Sites and
domains → Sites domains**:

* The host of `RIVET_SITES_BASE_URL` and every `RIVET_SITES_DOMAINS`
  entry are added at start-up, marked *Environment*, and trusted without a DNS
  record. The base URL's host is the first **primary** domain (the default for
  new sites). If that host changes while it is still primary, the domain is
  renamed, and its sites move with it, as before this feature existed.
* **Add a domain** (for example one donated for the project) and create the
  records shown: `TXT _rivetpanel-domain.<domain>` with the value
  `rivetpanel-domain=<token>`, which proves control, and `*.<domain>` (plus
  optionally `<domain>`) pointing at this server. Then press **Check DNS
  now**. No site can use the domain, and nothing is served under it, until the
  TXT record matches. Verified domains are re-checked every six hours; a
  failed re-check is reported but does not stop serving.
* **Make primary** changes where new sites go. **Turn off** stops serving every
  site under the domain at its address on that domain (custom domains keep
  working, nothing is deleted) and stops it being offered. The primary domain
  cannot be turned off or removed.
* **Move sites to…** moves every site from one domain to another, keeping
  slugs. If any slug is taken on the target, nothing moves.
* **Remove** works only for a domain no site uses that is not listed in the
  environment file.

Refused as a sites domain: the panel's host, any domain above it and any
domain below it, because a site could set cookies for the panel. Also refused:
a domain that is, is above, or is below another sites domain, and a domain
that a verified custom domain already uses or sits under. Custom domains under
any sites domain are refused too. Enforcement: application level, in the
panel's host routing. Routing and certificates for each sites domain still
have to be set up in the reverse proxy (below).

## Custom domains

1. Add the domain on the site's page (for example `www.example.com`).
   Internationalized names are stored in punycode.
2. Create the two records it shows at your DNS provider:
   * `TXT _rivetpanel-verify.www.example.com` with the value
     `rivetpanel-verify=<token>`: proves you control the domain.
   * `CNAME www.example.com` → the site's default host (or
     `RIVET_SITES_DNS_TARGET`), or an `A`/`AAAA` record with the server's
     address. Root domains need an ALIAS/ANAME or `A` record.
3. Press **Check DNS now**. The domain is served only after the TXT record
   matches.

Rules: a domain verified for one site cannot be added to another; an
unverified claim does not reserve a domain (someone who verifies first wins).
The panel's own host and its subdomains, and names under any sites domain, are
refused (a custom domain verified before a sites domain was added above it
must be removed before that sites domain can be verified). Verified domains are re-checked every six hours; a failed re-check is
reported on the site page but does not stop serving (DNS hiccups must not take
sites down). Remove the domain to stop serving it.

## Sites from GitHub: builds and apps

**Sites → From GitHub** (or `POST /api/v1/sites/from-repo`) creates a site
from a repository. The panel downloads the branch, detects the framework and
proposes a setup you review: the site type, build and start commands, output
folder or port, variables and add-ons. Private repositories work with your
connected GitHub account; **Deploy on every push** installs a webhook (or
checks the branch every few minutes when GitHub cannot reach the panel).

Behind the scenes the site gets an **app unit**: the checkout, builds,
deployments, variables, add-ons, logs and resource limits of the site. It is
managed from the site page (Deployments, Logs, Variables, Add-ons, Files,
Build and run) and never appears among the bots. Deleting the site deletes it
with its data.

| Framework (preset) | Kind | Build | Serves |
| --- | --- | --- | --- |
| Static files | static | none | the repository folder as it is |
| Vite (React, Vue, Svelte, Solid…) | static build | `npm ci && npm run build` | `dist`, single-page app |
| SvelteKit `adapter-static` | static build | same | `build` |
| Astro | static build | same | `dist` |
| Next.js `output: 'export'` | static build | same | `out` |
| Nuxt generate | static build | `nuxi generate` | `.output/public` |
| Node.js server (Express, Hono, Fastify…) | app | `npm ci` (+ `npm run build`) | `npm start` on `$PORT` |
| SvelteKit `adapter-node` | app | build, prune dev packages | `node build` |
| Next.js, Nuxt, Astro (Node adapter) | app | build | the framework's server |
| FastAPI, Flask, Django | app | virtual environment from `requirements.txt` | uvicorn or gunicorn on port 8000 |
| Go, Rust | app | `go build`, `cargo build --release` | the binary on `$PORT` (8080) |
| Laravel, plain PHP | app | Composer when `composer.json` exists | FrankenPHP on port 8080 (`public/` for Laravel) |

Everything in the proposal can be changed before and after creation.

**Builds** run in a throwaway build container with the runtime's image, the
build allowance of the panel (`RIVET_BUILD_*`), one at a time per
`RIVET_MAX_BUILDS`, and **no secrets**: the only variables a build sees are
public ones, whose names start with `VITE_`, `PUBLIC_`, `NEXT_PUBLIC_`,
`NUXT_PUBLIC_`, `REACT_APP_` or `GATSBY_` (frameworks inline these into the
browser bundle, so they are public anyway). A failed build keeps the current
release serving and shows the build output. Static builds publish a new
release, so **Serve this** on an earlier release still rolls back instantly;
apps roll back by deploying an earlier commit from Deployments.

**Apps** run under the same container policy as bots (read-only root file
system, all capabilities dropped, no new privileges, unprivileged user,
memory, CPU and process limits). An app:

* must listen on `$PORT` (the site's port) on all interfaces; `$HOST` is
  `0.0.0.0`;
* keeps persistent files in **`/data`** (`$DATA_DIR`): the folder survives
  deployments and restarts, is included in backups, and can be browsed under
  Files as `.rivet-data`. Repositories can never write into it. Everything
  else in the checkout is replaced on each deployment;
* can have a **release command** that runs before every start with the app's
  variables and add-ons (for example `php artisan migrate --force` or
  `npx prisma migrate deploy`); a failure stops the start;
* can use the same **add-ons** as bots (PostgreSQL, MariaDB, Redis, MongoDB),
  on a private network with their connection variables added for you;
* gets an HTTP **health check** on its health path every 30 seconds; three
  failures in a row restart it;
* is restarted on the new code after each deployment. While it builds or
  starts, visitors get a "Deploying…" page that reloads itself.

The sites listener forwards every method, WebSockets and server-sent events to
the app. It keeps the visitor's `Host`, replaces `X-Forwarded-For`,
`X-Real-IP`, `X-Forwarded-Host` and `X-Forwarded-Proto` with values it checked
(incoming ones are believed only from `RIVET_SITES_TRUSTED_PROXIES`), refuses
request bodies over the site's limit with 413, and answers 502 when the app
does not respond. The app's own response headers are passed through
unchanged.

PHP apps run [FrankenPHP](https://frankenphp.dev). Its image ships the binary
with a file capability that the kernel refuses to run under
no-new-privileges, so the build copies the binary into the checkout
(`.rivet-bin/frankenphp`) and the app runs that copy on port 8080. Laravel's
`storage/` and `bootstrap/cache` are inside the checkout and writable; point
SQLite or uploads at `/data`.

## Redirects and headers

A `_redirects` and a `_headers` file at the top of a static site (the
[Netlify](https://docs.netlify.com/routing/redirects/) and Cloudflare Pages
formats) are checked when a release is published and applied when it is
served. A mistake refuses the release and names the file and line. The files
themselves are never served.

```
# _redirects: from  to  [status][!]
/old-page        /new-page              301
/blog/:slug      /posts/:slug           302
/docs/*          https://docs.example.com/:splat
/app/*           /app/index.html        200
/gone            /410.html              410
```

* Statuses: 301 (default), 302, 303, 307, 308 redirect; 200 serves another
  file of the site without changing the address (rewrites; no proxying to
  other servers); 404 and 410 serve the target file with that status.
* `:name` matches one path segment, a trailing `*` matches the rest
  (`:splat`). The first matching rule wins.
* A file that exists at the requested path wins over a rule (so
  `/* /index.html 200` only catches unknown paths) unless the status ends in
  `!` (`301!`).

```
# _headers: a path line, then indented "Name: value" lines
/*
  X-Frame-Options: DENY
  Permissions-Policy: camera=()
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

Every matching block applies; later blocks override earlier ones. Headers that
would break the listener or sessions are refused: `Set-Cookie`, hop-by-hop
headers, `Content-Length`, `Content-Encoding`, `ETag`, `Location` and the
like. Up to 1,000 rules per file. App sites set their own headers and
redirects; these files do not apply to them.

## Publishing

* **Site templates**: when creating a site, pick a built-in starter instead of
  starting empty. The template's files, with the site name filled in, become
  the first release; edit them afterwards in the draft editor or replace them
  with an upload. Built-in templates (written for RivetPanel, embedded in the
  binary, a few KiB each): *Landing page*, *Docs and blog* (several linked
  pages and a shared stylesheet), *Portfolio*, *Coming soon* and *Game server
  community*. The community page has a status section that reads this
  panel's public status page (`GET /api/v1/status`, which allows any origin
  to read it); it shows live state only when the status page is enabled and
  the panel is reachable over HTTPS from visitors (the sites CSP allows
  `https:` connections), and otherwise falls back to a link. Templates need
  `sites.create` like any new site and count against the site size limit.
  Administrators cannot add their own templates yet. Pick one in **Sites →
  New site → Start from**, or browse them with previews under **Templates →
  Sites** (also "New site from …" in Go to); both ask for the same name,
  address and workspace.
* **Site files**: use the private draft editor, then publish it. Each publish
  is a new immutable release; partial edits never reach visitors.
* **ZIP upload** (drag and drop on the site page, or
  `POST /api/v1/sites/{id}/upload` with the archive as the body). A single
  top-level folder such as `dist/` is unwrapped. The archive is validated and
  extracted with the same hardened extractor as bot uploads (no links, no
  special files, size and entry limits); a rejected archive changes nothing.
* **GitHub (files as they are)**: link a repository, branch and optional
  folder on the site page of an uploaded site, then **Deploy latest commit**.
  The branch should contain built files (for example `gh-pages`, or a `dist`
  folder committed by CI). To build on the server instead, create the site
  with **From GitHub**. Public repositories
  work without a connected account; private ones use the GitHub access of the
  member who linked the repository. The tarball is downloaded through the API,
  as for bots; nothing from the repository runs.
* **Rollback**: every publish is a new immutable release; **Serve this** on an
  earlier release switches back instantly.

## Access and administration

Sites belong to a [workspace](workspaces.md): viewers can look, developers can
publish, deploy, roll back and manage domains, admins can also delete and move
sites. Panel administrators see every site under **Administration → Sites and
domains** and can **suspend** a site: visitors then get an "unavailable" page
on every address until it is restored; nothing is deleted.

## What it does not do

* Apps run on the panel's own node only, not on remote nodes.
* Apps share one Docker network: an app can reach another app's port
  directly (the same as bots on the default network). Use separate panels for
  tenants that must not see each other.
* Sites under one sites domain can set cookies for each other's host names
  unless the domain is on the Public Suffix List; give untrusted tenants their
  own registrable domain.
* A redeploy of an app restarts it on the new code: visitors see the
  "Deploying…" page for the build and start time (no blue-green switch yet).
* No Dockerfile builds, form handling, password protection, bandwidth limits
  or per-site analytics. Uploaded content is not scanned for malware.

See [implementation-status.md](implementation-status.md) for how each control
is enforced.

## API

| Method and path | Purpose |
| --- | --- |
| `GET /sites-info` | whether hosting is on, how addresses look, and the sites domains new sites can use (`domains`) |
| `GET /bots/{bot}/site`, `POST /bots/{bot}/site` `{slug?, domain_id?}` | get or create a bot's integrated site |
| `GET /sites`, `POST /sites` `{name, slug?, domain_id?, workspace_id?, spa?, template_id?}` | list and create standalone sites (`domain_id`: a sites domain id or name; default the primary; `template_id` publishes a site template as the first release) |
| `GET /site-presets` | the frameworks sites can be built or run with, and the public build-variable prefixes |
| `POST /sites/analyze` `{repo, branch?, root_dir?}` | download a branch and propose a site type and preset (creates nothing) |
| `POST /sites/from-repo` `{name, slug?, domain_id?, workspace_id?, kind, framework?, repo:{full_name, branch, root_dir?, auto_deploy?}, runtime, build_command?, output_dir?, spa?, argv?, port?, health_path?, release_command?, memory_bytes?, nano_cpus?, max_body_bytes?, env?, addons?}` | create a static, static build or app site from GitHub and queue its first deployment (`kind`: `static`, `static_build` or `app`) |
| `GET /sites/by-app/{app}` | the site an app unit belongs to |
| `GET /site-templates` | built-in site templates: `{templates:[{id, name, description, tags, accent, theme, status_widget, spa, pages, files, bytes, preview_url}]}` |
| `GET /site-templates/{id}/preview?page=` | one page of a template as a sandboxed, script-free HTML document (stylesheets inlined) that the panel may frame |
| `GET /sites/{id}` | site, role, domains with DNS records, releases, GitHub deploy state; for sites from GitHub also `app` (the app unit) and `repo` |
| `PATCH /sites/{id}` | address (`slug`, `domain_id`), site, generated-page, privacy, mode and repository settings; for sites from GitHub `build_output_dir`, `app_port` (app stopped), `health_path`, `max_body_bytes` |
| `DELETE /sites/{id}` | delete with every release and domain (workspace admin) |
| `POST /sites/{id}/upload` | publish a ZIP (raw body) |
| `GET/PUT/DELETE /sites/{id}/files...` | list, read, edit, move, extract and delete private draft files |
| `POST /sites/{id}/files/publish` | snapshot and activate the private draft |
| `POST /sites/{id}/deploy` `{sha?}` | deploy the linked branch head (background); sites from GitHub build and publish, or rebuild and restart the app, and accept a commit to deploy again |
| `POST /sites/{id}/releases/{release}/activate` | serve an earlier release |
| `POST /sites/{id}/domains` `{domain}` | add a custom domain |
| `POST /sites/{id}/domains/{domain}/verify` | check its TXT record now |
| `DELETE /sites/{id}/domains/{domain}` | remove it |
| `GET /admin/sites`, `PATCH /admin/sites/{id}` `{disabled}` | administrators: list, suspend or restore |
| `GET /admin/site-base-domains`, `POST /admin/site-base-domains` `{domain, label?, dns_target?}` | administrators: list sites domains with their DNS records, add one |
| `POST /admin/site-base-domains/{domain}/verify` | check its `_rivetpanel-domain` TXT record now |
| `PATCH /admin/site-base-domains/{domain}` `{enabled?, primary?, label?, dns_target?}` | turn on or off, make primary, rename the label, set the DNS target shown |
| `POST /admin/site-base-domains/{domain}/move-sites` `{to}` | move every site to another sites domain (all or nothing) |
| `DELETE /admin/site-base-domains/{domain}` | remove an unused, non-primary domain not set in the environment file |

An app unit is a bot of kind `web`: its variables, add-ons, console, files,
backups, deployments (`/bots/{app}/github`, `/bots/{app}/deploy`),
operations and start, stop and restart use the `/bots/{app}/…` endpoints.
Deleting it, publishing ports, moving it to another workspace or node, and
unlinking its repository are refused there; do it through the site.
