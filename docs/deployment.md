# Deployment

## Requirements

* Linux with cgroup v2 and Docker Engine (or a rootless daemon on the **systemd**
  cgroup driver). The panel checks that memory, CPU and PID limits can be
  enforced and reports not-ready otherwise.
* Go >= 1.27 and Node >= 22 to build; neither is needed to run.

## Install

```sh
make build                 # UI plus bin/rivetpanel and bin/rivet-agent
sudo deploy/install.sh     # binary, directories, systemd units; creates no secrets
```

Then follow the printed steps: create an encryption key (`rivetpanel keygen`),
create the first admin (`rivetpanel create-admin EMAIL`, hidden password prompt
or `RIVET_ADMIN_PASSWORD`), start `rivetpanel` and `rivetpanel-backup.timer`.
There is deliberately no unauthenticated first-run setup endpoint.

To add a remote execution host, enable `agents=preview`, expose the raw TLS
agent listener, copy the release to the node and run
`sudo deploy/install-agent.sh`. Enrollment and firewall details are in
[`agents.md`](agents.md).

## Reverse proxy and TLS

The panel listens on loopback and sets `Secure` session cookies in production,
so it must be reached over HTTPS. The proxy must pass WebSocket upgrades on
`/api/v1/bots/*/console` and preserve `Host`/`Origin` (the console rejects
cross-origin handshakes).

```
# Caddy
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Behind a proxy on the same host, set `RIVET_PROXY_HEADER` (`X-Forwarded-For`
for Caddy/nginx, `CF-Connecting-IP` for Cloudflare) so rate limits apply per
client instead of per proxy. The header is trusted only from loopback peers, so
the proxy must run on the same machine; without it all clients share the
proxy's address. Cloudflare Tunnel and `panel.xenyc.ge` specifics are in
`oauth.md`.

## Configuration

See `deploy/systemd/rivetpanel.env.example`. Notable: `RIVET_CONTAINER_USER`
(non-root uid:gid), `RIVET_WORKSPACE_OWNER` (host uid:gid; differs under
rootless/userns), `RIVET_CONTAINER_NETWORK`, `RIVET_MAX_BOT_MEMORY_BYTES`,
`RIVET_RUNTIMES_DIR` (override the six recipes, e.g. to pin digests or use a
registry mirror), `RIVET_KEY_DIR`/`RIVET_ACTIVE_KEY_ID`.

## Optional services and extra directories

* **OAuth sign-in and GitHub deployments:** `oauth.md`. Needs
  `RIVET_PUBLIC_URL`; GitHub auto-deploy webhooks need GitHub to be able to
  reach `<PUBLIC_URL>/api/v1/webhooks/github`.
* **Static sites:** off by default; set `RIVET_SITES_LISTEN` (for example
  `127.0.0.1:8081`) and `RIVET_SITES_BASE_URL` (for example
  `https://sites.example.com`), add a wildcard DNS record and route every
  non-panel host name to that listener in the reverse proxy. Caddy's on-demand
  TLS can ask the listener which host names may get certificates. More sites
  domains come from `RIVET_SITES_DOMAINS` or Administration → Sites and
  domains; each needs its own wildcard DNS record and certificate. Full setup:
  `sites.md`. Releases live in `RIVET_SITES_DIR`
  (`/var/lib/rivetpanel/sites`, inside the service's `ReadWritePaths`); include
  it in host backups.
* **SFTP:** off by default; `RIVET_SFTP_LISTEN=0.0.0.0:2022`. Open the port in
  your firewall. It is plain TCP: it does not work through an HTTP proxy or
  Cloudflare Tunnel. The host key is created at `/var/lib/rivetpanel/sftp_host_ed25519`.
* **Published bot ports:** off unless a bot opts in, within
  `RIVET_PORT_RANGE` (default 20000-29999) on 127.0.0.1. Firewall the range
  from the internet unless you set `RIVET_PORT_PUBLIC_BIND=1`.
* **Backups:** per-bot snapshots in `RIVET_BACKUP_DIR`
  (`/var/lib/rivetpanel/backups`, covered by the service's `ReadWritePaths`).
  They are not part of `rivetpanel backup`; back that directory up too. Sealed
  values in them (and OAuth tokens in the database) need the same key files.
  `rivetpanel verify` checks environment variables, OAuth tokens, Discord webhooks
  and GitHub webhook secrets against the keys.

## Operations

* `/api/v1/healthz` liveness; `/api/v1/readyz` reports `database` and `docker`
  separately and returns 503 when either is down. Docker going away does not
  stop the panel or the bots; the runner reconnects and re-syncs.
* Restarting the panel does not restart bots: it adopts running containers.
* Logs: structured JSON on stderr (journald). Bot output is in Docker's rotated
  logs.
* Read `docs/isolation.md` before hosting untrusted users: there is no egress
  policy, admission control or disk quota out of the box.

### Prometheus metrics

Most variables can also be changed from **Administration → Environment**; those
values are stored in the database and win over the environment file after the
next restart (`rivetpanel env reset` removes them from the host). The systemd unit
restarts the panel after the page's **Restart panel** button because the button
exits with code 75 and the unit has `Restart=on-failure`.

Set `RIVET_METRICS_TOKEN` to a random secret of at least 24 characters to
enable `GET /metrics`. Scrapers must send `Authorization: Bearer <token>`.
Leaving the variable empty keeps the endpoint disabled. Metrics cover process
uptime and requests, database availability, aggregate bot lifecycle counts,
and the latest node CPU, memory, disk, and running-bot gauges. Bot names, user
IDs, and HTTP paths are deliberately not used as labels.
AI provider and search credentials are entered in the encrypted
Administration UI (the first provider key may also be entered in the optional
AI operator step of `/setup`); never place them in bot environment variables.
Web-search requests honour `HTTP(S)_PROXY`; public page fetches always connect
directly, because the destination address is checked at connect time. Diagnostic
containers require the local Docker runner and remain offline. See
[AI operator](ai-operator.md).
