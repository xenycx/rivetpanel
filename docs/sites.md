# Static site hosting

RivetPanel can host static websites next to your bots: a bot's dashboard, its
documentation, a landing page or an invite page. A site is a set of files
(HTML, CSS, JavaScript, images) served exactly as uploaded. There is no
server-side code and no build step on the server: build locally or in CI and
publish the output.

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
* `GET` and `HEAD` only. HTML is sent with `Cache-Control: no-cache` so a new
  release shows immediately; other files are cacheable for an hour.
  Conditional and range requests are supported.
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

## Publishing

* **Site files**: use the private draft editor, then publish it. Each publish
  is a new immutable release; partial edits never reach visitors.
* **ZIP upload** (drag and drop on the site page, or
  `POST /api/v1/sites/{id}/upload` with the archive as the body). A single
  top-level folder such as `dist/` is unwrapped. The archive is validated and
  extracted with the same hardened extractor as bot uploads (no links, no
  special files, size and entry limits); a rejected archive changes nothing.
* **GitHub**: link a repository, branch and optional folder on the site page,
  then **Deploy latest commit**. The branch should contain built files (for
  example `gh-pages`, or a `dist` folder committed by CI). Public repositories
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

No server-side code, PHP, redirects/headers configuration files, form handling,
password protection, bandwidth limits, per-site analytics or build pipelines.
Uploaded content is not scanned for malware. These are listed in
[implementation-status.md](implementation-status.md).

## API

| Method and path | Purpose |
| --- | --- |
| `GET /sites-info` | whether hosting is on, how addresses look, and the sites domains new sites can use (`domains`) |
| `GET /bots/{bot}/site`, `POST /bots/{bot}/site` `{slug?, domain_id?}` | get or create a bot's integrated site |
| `GET /sites`, `POST /sites` `{name, slug?, domain_id?, workspace_id?, spa?}` | list and create standalone sites (`domain_id`: a sites domain id or name; default the primary) |
| `GET /sites/{id}` | site, role, domains with DNS records, releases, GitHub deploy state |
| `PATCH /sites/{id}` | address (`slug`, `domain_id`), site, generated-page, privacy, mode and repository settings |
| `DELETE /sites/{id}` | delete with every release and domain (workspace admin) |
| `POST /sites/{id}/upload` | publish a ZIP (raw body) |
| `GET/PUT/DELETE /sites/{id}/files...` | list, read, edit, move, extract and delete private draft files |
| `POST /sites/{id}/files/publish` | snapshot and activate the private draft |
| `POST /sites/{id}/deploy` | deploy the linked branch head (background) |
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
