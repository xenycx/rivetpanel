---
title: Addresses and domains
lede: Change a site's address, serve it on your own domain, and (as an administrator) set up sites domains, DNS and the reverse proxy.
icon: link
glance:
  - Up to 10 custom domains per site
  - Domains re-checked every 6 hours
  - Addresses are 3–40 characters
related: [websites/bot-sites, websites/administration, operate/install, operate/configuration]
---

## A site's address

A site's address is `<slug>.<sites domain>`, such as `docs.sites.example.com`.
A slug is unique per sites domain, so `docs.sites.example.com` and
`docs.pages.example.net` can be two different sites.

### Change the address

Developers and up change a site's slug, and its sites domain when there is
more than one, in the site's **Settings**.

> [!WARNING]
> The move is immediate. The old address stops answering at once and any site
> may take it, so update your links first. Verified custom domains keep
> working.

The same rules apply as at creation [enforced: panel]:

- 3–40 lower-case letters, digits and single hyphens, starting and ending with
  a letter or digit
- not a reserved name (`www`, `api`, `admin`, `panel`, `mail`, …)
- not already used under that sites domain

## Custom domains

A site can answer on up to 10 custom domains [enforced: panel].

1. Add the domain on the site's page, for example `www.example.com`.
   Internationalized names are stored in punycode.
2. Create the two records it shows at your DNS provider:
   - `TXT _rivetpanel-verify.www.example.com` with the value
     `rivetpanel-verify=<token>`, which proves you control the domain.
   - `CNAME www.example.com` pointing at the site's default host (or
     `RIVET_SITES_DNS_TARGET`), or an `A`/`AAAA` record with the server's
     address. Root domains need an ALIAS/ANAME or `A` record.
3. Press **Check DNS now**.

The domain is served only after the TXT record matches [enforced: panel].
Remove the domain to stop serving it.

### Rules for custom domains

- A domain verified for one site cannot be added to another.
- An unverified claim does not reserve a domain: whoever verifies first wins.
- The panel's own host and its subdomains are refused, and so are names under
  any sites domain [enforced: panel].
- Verified domains are re-checked every six hours. A failed re-check is shown
  on the site page but does not stop serving, so DNS hiccups do not take sites
  down.

## Sites domains (administrators)

Administrators manage sites domains under
**Administration → Sites and domains → Sites domains**.

- The host of `RIVET_SITES_BASE_URL` and every `RIVET_SITES_DOMAINS` entry are
  added at start-up, marked *Environment*, and trusted without a DNS record.
- The base URL's host is the first **primary** domain, the default for new
  sites. If that host changes while it is still primary, the domain is renamed
  and its sites move with it.

### Add a sites domain

1. Enter the domain under **Add a domain** and add it, for example a domain
   donated to the project.
2. Create the records shown:
   - `TXT _rivetpanel-domain.<domain>` with the value
     `rivetpanel-domain=<token>`, which proves control
   - `*.<domain>`, and optionally `<domain>`, pointing at this server
3. Press **Check DNS now**.
4. Set up routing and a certificate for it in the reverse proxy; see
   [DNS and reverse proxy](#dns-and-reverse-proxy-administrators).

No site can use the domain, and nothing is served under it, until the TXT
record matches [enforced: panel]. Verified domains are re-checked every six
hours; a failed re-check is reported but does not stop serving.

### Manage sites domains

| Action | Effect |
|---|---|
| **Make primary** | New sites go here |
| **Turn off** | Stops serving every site at its address on this domain and stops offering the domain. Custom domains keep working and nothing is deleted. The primary domain cannot be turned off |
| **Move sites to…** | Moves every site to another sites domain, keeping slugs. If any slug is taken on the target, nothing moves |
| **Remove** | Only for a domain no site uses, that is not primary and not set in the environment file |

### Domains that are refused

A site could set cookies for the panel, so these are refused as a sites
domain [enforced: panel]:

- the panel's host, any domain above it and any domain below it
- a domain that is, is above, or is below another sites domain
- a domain that a verified custom domain already uses or sits under

A custom domain verified before a sites domain was added above it must be
removed before that sites domain can be verified. These checks live in the
panel's host routing; routing and certificates for each sites domain still
have to be set up in the reverse proxy.

## DNS and reverse proxy (administrators)

RivetPanel does not install certificates or edit the proxy configuration. TLS
is terminated and certificates are issued by the reverse proxy; RivetPanel
only answers whether a host name is allowed [enforced: panel].

1. Create a wildcard DNS record, such as `*.sites.example.com`, pointing at
   the server.
2. Terminate TLS in a reverse proxy on the same host and forward site traffic
   to `RIVET_SITES_LISTEN`. Keep forwarding the panel host to `RIVET_LISTEN`.

### Caddy

Caddy's on-demand TLS obtains certificates for the wildcard subdomains and for
verified custom domains as visitors arrive. The sites listener answers Caddy's
permission check at `/.well-known/rivetpanel/tls-allowed?domain=…` for
loopback peers only, so certificates are requested only for host names a live
site answers on.

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

### nginx and other proxies

Use a wildcard certificate for `*.sites.example.com` (DNS challenge) and add
certificates for custom domains yourself. Proxy everything that is not the
panel host to the sites listener with the original `Host` header.

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

Give the panel's router, and any other service on the proxy, an explicit
higher priority such as `priority: 100`, so the catch-all never takes its
host. Remove any older catch-all router that would compete, or give it a
priority between the two.

Every sites domain needs a certificate for `<domain>` and `*.<domain>`:

- Behind Cloudflare with SSL mode *Full (strict)*, create a Cloudflare Origin
  Certificate for both names in that zone, install it on the server, and list
  it under `tls.certificates` in Traefik's dynamic configuration.
- For domains not on Cloudflare, use Traefik's ACME DNS-01 resolver for the
  wildcard, or Caddy's on-demand TLS.

Per sites domain, at its DNS provider:

1. `A *` (and `A @`) pointing at the server's address, proxied when on
   Cloudflare.
2. `TXT _rivetpanel-domain` with `rivetpanel-domain=<token>`. Domains in the
   environment file do not need it.
3. On Cloudflare, SSL/TLS mode *Full (strict)*. Universal SSL covers the apex
   and one wildcard level, which is all sites use.
4. In the panel, add the domain, then press **Check DNS now**. Install the
   origin certificate on the server.

### A domain you already route elsewhere

When the proxy already sends a domain, or a wildcard such as `*.example.com`,
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

### Apps behind the proxy

App sites see the visitor's address and scheme only through the sites
listener. Tell it which peer is your proxy and, behind Cloudflare, which
header carries the visitor's address:

```
RIVET_SITES_TRUSTED_PROXIES=172.16.0.0/12      # the proxy's Docker network, or its address
RIVET_SITES_CLIENT_IP_HEADER=CF-Connecting-IP  # behind Cloudflare
```

The proxy must pass WebSocket upgrades and must not buffer server-sent events.
Traefik and Caddy do both by default; for nginx set
`proxy_http_version 1.1`, the `Upgrade` and `Connection` headers, and
`proxy_buffering off`. Raise the proxy's own body limit if apps accept large
uploads.

When the panel runs in a container (the published `compose.yaml`), it
connects its own container to `RIVET_APPS_NETWORK` at every start, so
`compose.yaml` needs no change. If the Docker daemon cannot identify the
panel's container, **Diagnostics** says so; add the network to the service
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

## Configuration

Administrators set these in the panel's environment. See
[Configuration](../07-operate/02-configuration.md).

| Variable | Default | Meaning |
| --- | --- | --- |
| `RIVET_SITES_LISTEN` | Off (development: `127.0.0.1:8081`) | Address of the sites listener; empty turns hosting off |
| `RIVET_SITES_BASE_URL` | Development: `http://localhost:8081` | Origin whose host is the primary sites domain, such as `https://sites.example.com`; its scheme and port apply to every sites domain |
| `RIVET_SITES_DOMAINS` | None | Further sites domains, comma-separated (such as `pages.example.net,example-sites.org`), trusted without a DNS record |
| `RIVET_SITES_DIR` | `/var/lib/rivetpanel/sites` | Releases, one directory per site |
| `RIVET_SITE_MAX_BYTES` | 104857600 (100 MiB) | Largest release, uncompressed (1 MiB–4 GiB) |
| `RIVET_MAX_SITES_PER_USER` | 10 | Fallback for the default allowance of sites per workspace, used where **Administration → Panel settings → Default allowance** and the workspace's own allowance set none; 0 is no limit |
| `RIVET_SITES_DNS_TARGET` | The site's default host | Host name shown as the CNAME target for custom domains |
| `RIVET_APPS_NETWORK` | `rivetpanel-apps` | Docker network app sites run on; the sites listener reaches them over it (a containerised panel joins it at start) |
| `RIVET_SITES_TRUSTED_PROXIES` | None (loopback only) | Comma-separated addresses or CIDR ranges of the proxy in front of the sites listener; only their `X-Forwarded-*` and client-address headers reach apps |
| `RIVET_SITES_CLIENT_IP_HEADER` | None (`X-Forwarded-For`) | Header from a trusted proxy that holds the visitor's address, such as `CF-Connecting-IP` |
| `RIVET_SITES_MAX_BODY_BYTES` | 104857600 (100 MiB) | Largest request body an app site accepts unless the site sets its own limit |

In development (`RIVET_ENV=development`), hosting is on by default and sites
are reachable at `http://<slug>.localhost:8081`, because browsers resolve
`*.localhost` to the local machine.
