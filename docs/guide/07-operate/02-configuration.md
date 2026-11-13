---
title: Configuration
lede: Every RIVET_ environment variable, the optional services they switch on, and who bots run as on the host.
icon: sliders
audience: admin
glance:
  - Values apply after a restart
  - Bots run as 65532:65532 by default
  - Published ports 20000–29999 on 127.0.0.1
related: [operate/install, operate/host-and-logs, operate/security, teams/limits]
---

## Where configuration lives

The panel reads `RIVET_*` variables from its environment: for systemd,
`/etc/rivetpanel/rivetpanel.env`, started from
[`deploy/systemd/rivetpanel.env.example`](../../../deploy/systemd/rivetpanel.env.example);
for the container image, `.env`, started from
[`deploy/container.env.example`](../../../deploy/container.env.example). The
image sets `RIVET_LISTEN` and every path itself.

Most variables can also be changed under **Administration → Environment**,
which stores them in the database and applies them after a restart. The
**Set in** column below says where each one is changed:

| Set in | Meaning |
| --- | --- |
| Panel or file | Editable from **Administration → Environment**; a value saved there wins over the file |
| File only | Read before the database opens, or widens what the panel may do to the host. Change it in the environment file |
| Managed | Owned by another page (named in the notes). For sign-in and email the file wins; for limits and retention the panel's value wins and the file is the fallback |

The precedence rules, the **Restart panel** button and `rivetpanel env` are
described in [Host, logs and environment](05-host-and-logs.md#environment-variables-from-the-panel).

## Variables

Byte values are whole numbers of bytes; durations are Go durations such as
`30s`, `10m` or `24h`; switches are `1` (on) or `0` (off).

### Panel

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_ENV` | `production` | File only | `production` uses system directories and requires https for public addresses; `development` keeps everything under `./.dev-data` |
| `RIVET_LISTEN` | `127.0.0.1:8080` | File only | Put a TLS reverse proxy in front of it |
| `RIVET_DB_PATH` | `/var/lib/rivetpanel/rivetpanel.db` | File only | SQLite database file |
| `RIVET_DB_MAX_CONNS` | `4` | File only | 1 to 16 database connections; see [footprint](12-diagnostics.md#memory-footprint) for the trade-off |
| `RIVET_DATA_ROOT` | `/var/lib/rivetpanel/bots` | File only | Parent directory of every app's files; command-line backups read it too |
| `RIVET_KEY_DIR` | `/etc/rivetpanel/keys` | File only | Encryption key files; stored values cannot be read without them |
| `RIVET_ACTIVE_KEY_ID` | `k1` | File only | Key that encrypts new values; rotate with `rivetpanel keygen` and `rivetpanel reseal` |
| `RIVET_RUNTIMES_DIR` | built-in catalog | File only | Replaces the six runtime recipes, for example to pin digests or use a registry mirror |
| `RIVET_SESSION_TTL` | `168h` | Panel or file | How long a sign-in lasts without being renewed (1 minute to 90 days) |
| `RIVET_SHUTDOWN_TIMEOUT` | `10s` | Panel or file | How long a restart waits for requests in progress; app containers keep running either way |
| `RIVET_MAX_UPLOAD_BYTES` | 32 MiB | Panel or file | Largest single file or archive an upload may carry (1 KiB to 1 GiB) |
| `RIVET_PROXY_HEADER` | unset | File only | Header with the real client address behind a proxy on this host; see [Install](01-install.md#client-addresses-behind-a-proxy) |

### Preview modules and remote nodes

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_MODULES` | built-in states | Panel or file | Comma-separated `key=off`, `key=preview` or `key=stable`, for example `agents=preview,game_servers=preview`. New preview modules are off until enabled. **Administration → Modules** writes it |
| `RIVET_EXTENSIONS_SAFE_MODE` | `0` | File only | `1` starts the panel without running any extension code; see [Extensions](09-extensions.md#recovery) |
| `RIVET_AGENT_LISTEN` | `:8444` | Panel or file | Raw mutual-TLS listener for `rivet-agent`; never behind an HTTP proxy |
| `RIVET_AGENT_ADDRESS` | public URL host + listener port | Panel or file | The `host:port` agents dial |
| `RIVET_AGENT_HOSTS` | agent address host, `localhost`, `127.0.0.1` | Panel or file | Names and IPs in the listener certificate, comma-separated |

### Apps and Docker

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_RUNNER_MODE` | `local` | Panel or file | `none` turns the runner off so only the control plane runs; apps can then no longer be started, stopped or built from this panel |
| `RIVET_DOCKER_HOST` | `unix:///var/run/docker.sock` | File only | Docker endpoint; a wrong value stops every app from starting |
| `RIVET_CONTAINER_USER` | `65532:65532` | File only | `uid:gid` app processes run as; see [Workspace ownership](#workspace-ownership) |
| `RIVET_WORKSPACE_OWNER` | the container user | File only | Host `uid:gid` that owns workspace files when it differs (rootless Docker, `userns-remap`) |
| `RIVET_ALLOW_ROOT_CONTAINER_USER` | `0` | File only | Permits uid 0 inside app containers; only sensible with a rootless daemon |
| `RIVET_ALLOW_SHARED_UID` | `0` | File only | Production opt-in to run apps as the panel's own uid; see [Workspace ownership](#workspace-ownership) |
| `RIVET_CONTAINER_NETWORK` | `bridge` | File only | Docker network mode for app and build containers; it decides what apps can reach |
| `RIVET_CONTAINER_REGISTRIES` | `docker.io,ghcr.io,quay.io` | Panel or file | Registries container apps may pull from; empty allows none, which turns container apps off |
| `RIVET_CONTAINER_MAX_IMAGE_BYTES` | 2 GiB | Panel or file | Largest container-app image, measured after the pull; `0` is no limit |
| `RIVET_RUNNER_WORKERS` | `2` | Panel or file | Apps that may start, stop or restart at once (1 to 16) |
| `RIVET_MAX_BUILDS` | `1` | Panel or file | Concurrent build containers (1 to 16); each can use its runtime's build memory, 3 GiB for Rust |
| `RIVET_BUILD_TIMEOUT` | `10m` | Panel or file | Longest a build may run (1 minute to 2 hours) |

### Capacity and limits

These are admission rules the panel applies, not kernel limits; see
[Limits and allowances](../06-teams/04-limits.md).

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_MAX_BOT_MEMORY_BYTES` | the node's memory | Panel or file | Largest memory size one app may be given; always capped by the node's memory |
| `RIVET_MAX_BOT_NANO_CPUS` | the node's CPUs | Panel or file | Largest CPU size one app may be given, in billionths of a core (`1000000000` is one core) |
| `RIVET_NODE_MEMORY_BYTES` | no limit | Panel or file | Sum of memory sizes of apps wanted running on each node, add-ons included; a start past it is refused |
| `RIVET_USER_MEMORY_BYTES` | no limit | Managed | Fallback default workspace memory allowance (**Panel settings → Default allowance**) |
| `RIVET_MAX_BOTS_PER_USER` | no limit | Managed | Fallback default workspace allowance of apps (**Panel settings → Default allowance**) |
| `RIVET_MIN_FREE_DISK_BYTES` | 1 GiB | Panel or file | Free space backups, restores and deployments need before they start; `0` turns the check off |

### Backups

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_BACKUP_DIR` | `/var/lib/rivetpanel/backups` | Panel or file | Per-app archives; existing archives are not moved when it changes |
| `RIVET_BACKUP_INTERVAL` | `24h` | Panel or file | Time between automatic backups (1 hour to 90 days); `0` turns them off |
| `RIVET_BACKUP_KEEP` | `7` | Panel or file | Automatic backups kept per app (1 to 45) |
| `RIVET_BACKUP_MAX_BYTES` | 2 GiB | Panel or file | Largest app a backup will archive |

### Published ports

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_PORT_RANGE` | `20000-29999` | Panel or file | Host ports apps may publish (within 1024 to 65535); each app opts in |
| `RIVET_PORT_PUBLIC_BIND` | `0` | Panel or file | `1` lets published ports bind beyond `127.0.0.1`, exposing them to the network |

### Sites

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_SITES_LISTEN` | off | Panel or file | Separate listener that serves only sites; must differ from the panel address |
| `RIVET_SITES_BASE_URL` | unset | Panel or file | Public origin sites are served under; use a different domain than the panel |
| `RIVET_SITES_DOMAINS` | unset | Panel or file | Further site domains, comma-separated |
| `RIVET_SITES_DIR` | `/var/lib/rivetpanel/sites` | Panel or file | Site releases |
| `RIVET_SITE_MAX_BYTES` | 100 MiB | Panel or file | Largest site release, uncompressed (1 MiB to 4 GiB) |
| `RIVET_MAX_SITES_PER_USER` | `10` | Managed | Fallback default workspace allowance of sites; `0` is no limit |
| `RIVET_SITES_DNS_TARGET` | sites base URL host | Panel or file | CNAME target shown for custom domains |
| `RIVET_APPS_NETWORK` | `rivetpanel-apps` | File only | Docker network app sites run on; a containerized panel joins it at start |
| `RIVET_SITES_TRUSTED_PROXIES` | loopback | Panel or file | Addresses or CIDR ranges of the proxy in front of the sites listener |
| `RIVET_SITES_CLIENT_IP_HEADER` | `X-Forwarded-For` | Panel or file | Visitor-address header from a trusted proxy, such as `CF-Connecting-IP` |
| `RIVET_SITES_MAX_BODY_BYTES` | 100 MiB | Panel or file | Default request body limit of app sites (1 KiB to 4 GiB) |

### SFTP

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_SFTP_LISTEN` | off | Panel or file | Embedded SFTP server, for example `0.0.0.0:2022` |
| `RIVET_SFTP_MAX_FILE_BYTES` | 256 MiB | Panel or file | Largest file a client may write (1 MiB to 64 GiB) |
| `RIVET_SFTP_HOST_KEY` | `sftp_host_ed25519` next to the database | Panel or file | Created on first start; changing it makes every client warn about a new key |

### Monitoring

| Variable | Default | Set in | Notes |
| --- | --- | --- | --- |
| `RIVET_TELEMETRY_INTERVAL` | `30s` | Panel or file | Host sampling interval (30 to 60 seconds) |
| `RIVET_TELEMETRY_RETENTION` | `168h` | Managed | How long host samples are kept (1 hour to 365 days) when **Logs and retention** sets nothing |
| `RIVET_METRICS_TOKEN` | off | Panel or file | Bearer secret of 24 or more characters that enables `/metrics`; stored encrypted when set in the panel |

### Sign-in, address and email

All of these are managed under **Administration → Panel settings**, where a
value from the environment file wins and shows read-only.

| Variable | Notes |
| --- | --- |
| `RIVET_PUBLIC_URL` | The https address people use: scheme and host, no path or trailing slash. OAuth callbacks and webhook addresses derive from it |
| `RIVET_OAUTH_ALLOW_SIGNUP` | `1` lets an unknown GitHub or Discord identity with a verified email create a normal account; default `0` |
| `RIVET_GITHUB_CLIENT_ID`, `RIVET_GITHUB_CLIENT_SECRET` | GitHub OAuth; a provider is on only when both are set |
| `RIVET_DISCORD_CLIENT_ID`, `RIVET_DISCORD_CLIENT_SECRET` | Discord OAuth |
| `RIVET_MAILGUN_API_KEY`, `RIVET_MAILGUN_DOMAIN`, `RIVET_MAILGUN_REGION`, `RIVET_MAIL_FROM` | Email; see [Email](06-email.md) |

Client secrets and the Mailgun key are credentials: keep the environment file
owned by root with mode `0640`, as `deploy/install.sh` installs it. Never put
AI provider or search keys in any environment: they are entered encrypted in
the panel (see [AI assistant](10-ai-assistant.md#providers-and-search)).

## Optional services and extra directories

| Service | Off until | What to do |
| --- | --- | --- |
| OAuth sign-in and GitHub deployments | Client IDs and secrets are set | Needs `RIVET_PUBLIC_URL`. Auto-deploy webhooks need GitHub to reach `<PUBLIC_URL>/api/v1/webhooks/github`. See [Sign-in](../06-teams/03-sign-in.md) and [Deploy from GitHub](../02-apps/05-deploy-from-github.md) |
| Sites | `RIVET_SITES_LISTEN` and `RIVET_SITES_BASE_URL` are set | Add a wildcard DNS record and route every non-panel host name to the listener in the proxy. Caddy's on-demand TLS can ask the listener which names may get certificates. Each extra domain needs its own wildcard record and certificate. See [Domains](../04-websites/04-domains.md) |
| SFTP | `RIVET_SFTP_LISTEN` is set | Open the port in your firewall. It is plain TCP: it does not work through an HTTP proxy or Cloudflare Tunnel |
| Published app ports | An app opts in | Ports come from `RIVET_PORT_RANGE` on `127.0.0.1`. Firewall the range from the internet unless you set `RIVET_PORT_PUBLIC_BIND=1` [enforced: docker] |
| Prometheus metrics | `RIVET_METRICS_TOKEN` is set | See [Host, logs and environment](05-host-and-logs.md#prometheus-metrics) |

Three directories hold data that `rivetpanel backup` does not include. All are
inside the service's `ReadWritePaths`; add them to your host backups:

| Directory | Holds |
| --- | --- |
| `RIVET_SITES_DIR` (`/var/lib/rivetpanel/sites`) | Site releases |
| `RIVET_BACKUP_DIR` (`/var/lib/rivetpanel/backups`) | Per-app backups. Sealed values in them need the same key files |
| `/var/lib/rivetpanel/logs` and `/var/lib/rivetpanel/log-archive` | Daily log files and their archive; see [Logs](../05-observe/06-logs.md) |

## Workspace ownership

Bots and game servers run as a non-root `uid:gid` inside their containers and
must own their workspace files on the host. The panel picks that user at
start:

| Situation | Container user and workspace owner |
| --- | --- |
| `RIVET_CONTAINER_USER` or `RIVET_WORKSPACE_OWNER` set | Exactly what is set, never overridden |
| Neither set; panel runs as root or holds `CAP_CHOWN` (the systemd unit) | The dedicated unprivileged `65532:65532` |
| Neither set; no `CAP_CHOWN`; `RIVET_ENV=development` | The panel's own `uid:gid`, automatically |
| Neither set; no `CAP_CHOWN`; production with `RIVET_ALLOW_SHARED_UID=1` | The panel's own `uid:gid`, opted in |
| Neither set; no `CAP_CHOWN`; production without the opt-in | The panel refuses to start and says why [enforced: panel] |

The fallback exists so you can try the panel with your own account: without
it the panel could not hand files to `65532:65532` and no bot could write its
workspace. The panel then logs a warning and **Administration → Diagnostics**
shows an informational "Workspace ownership" check; it is a warning when
production runs that way through `RIVET_ALLOW_SHARED_UID=1`, and
`rivetpanel doctor` reports the refusal as a failure.

> [!WARNING]
> In the fallback, containers are still not root inside, but they run with the
> same host uid as the panel, which owns `rivetpanel.db` and the key folder. A
> container escape, or a bind mount exposing more than the workspace, would
> act as the panel's user, which can reach the Docker socket and decrypt every
> sealed secret. For production run the panel as root through the systemd
> unit, give it `CAP_CHOWN`, or set both variables explicitly.

Under rootless Docker or `userns-remap`, container uid N is not host uid N:
always set both variables, with `RIVET_WORKSPACE_OWNER` as the host `uid:gid`
that maps to the container user. See [Security and isolation](08-security.md#user-ids-under-rootless-docker).

Workspaces created while the panel ran as another user (for example as root
with `65532:65532`) are repaired at start when the panel can. Otherwise
Diagnostics lists them as "Workspaces owned by another user", the bot's start
error names the file, and `sudo chown -R <uid:gid> <data root>` fixes them.

`rivet-agent serve` applies the same rule on remote nodes, with
`--user`/`--workspace-owner` (or `RIVET_AGENT_CONTAINER_USER` and
`RIVET_AGENT_WORKSPACE_OWNER`). With neither set it falls back automatically
only with `RIVET_ENV=development`; otherwise it refuses to start unless
`--allow-shared-uid` or `RIVET_AGENT_ALLOW_SHARED_UID=1` is given, because its
own uid owns the node key and certificate.

## Installation identity

Each installation has an identity stored in its database, and every container
it creates is labelled with it. Two panels sharing one Docker daemon therefore
never list, adopt or remove each other's containers. [enforced: panel]

- Containers created before the label existed are adopted only when their bot
  exists in this database, and are never swept as orphans.
- A restored installation keeps its identity.
- A lock file stops two processes from using one database. [enforced: panel]
