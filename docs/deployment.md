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

A release archive (`make release`) has the same layout: unpack it and run
`sudo deploy/install.sh` from its top directory; the binaries are in `bin/`.
`install.sh` installs only the panel. `rivet-agent` is for remote nodes and is
installed there with `deploy/install-agent.sh`.

Then follow the printed steps: create the encryption key and the agent
certificate authority (`rivetpanel keygen`; the service itself cannot write
`/etc/rivetpanel`), start `rivetpanel` and `rivetpanel-backup.timer`, and
create the first administrator in one of two ways:

* `rivetpanel create-admin EMAIL` on the host (hidden password prompt or
  `RIVET_ADMIN_PASSWORD`), or
* the first-run setup page: while the database has no accounts, the panel
  logs a one-time setup code (`journalctl -u rivetpanel | grep setup_code`;
  also written to `setup-code` next to the database, mode 0600). Open
  `/setup` in the browser and enter it. The code proves the person can read
  the server's log or files; it changes on every restart, the page refuses
  requests without it, and setup closes once an account exists. Create the
  administrator promptly after the first start, before exposing the panel.

To add a remote execution host, enable `agents=preview`, expose the raw TLS
agent listener, copy the release to the node and run
`sudo deploy/install-agent.sh`. Enrollment and firewall details are in
[`agents.md`](agents.md).

Installations that enabled remote nodes before `keygen` created the agent
certificate authority run `sudo rivetpanel keygen --agent-ca` once (with the
service's `RIVET_KEY_DIR`); otherwise the panel stops at start explaining that
the key directory is read-only to it.

Upgrading from BotPanel 0.4.0 is not an in-place upgrade: see "Upgrading from
BotPanel 0.4.0" below.

## Reverse proxy and TLS

The panel listens on loopback and sets `Secure` session cookies in production,
so it must be reached over HTTPS. If `RIVET_LISTEN` is not a loopback address
while `RIVET_PUBLIC_URL` is plain `http://` on a non-local host, the panel logs
a warning at start and Diagnostics shows "Unencrypted public address"; it still
starts (some deployments terminate TLS on another machine), but passwords and
session cookies then cross that network unencrypted. The proxy must pass WebSocket upgrades on
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

In production with an `https://` public address the panel sends
`Strict-Transport-Security: max-age=31536000` itself (no `includeSubDomains`,
so other services on sibling names are unaffected; add it, or `preload`, in
the proxy if you want them). Its pages carry a Content-Security-Policy whose
`script-src` allows only the panel's own files and the SHA-256 hashes of the
two inline scripts in the built page (theme setup and SvelteKit's start
script); `style-src` still allows inline styles because Svelte sets style
attributes and transition rules at runtime. A proxy that rewrites the HTML
(injecting scripts, minifying) breaks the hashes; turn such features off for
the panel.

## Configuration

See `deploy/systemd/rivetpanel.env.example`. Notable: `RIVET_CONTAINER_USER`
(non-root uid:gid), `RIVET_WORKSPACE_OWNER` (host uid:gid; differs under
rootless/userns), `RIVET_CONTAINER_NETWORK`, `RIVET_MAX_BOT_MEMORY_BYTES` and
`RIVET_MAX_BOT_NANO_CPUS` (optional caps; unset, each node's own memory and
CPU count are the per-server maximums),
`RIVET_RUNTIMES_DIR` (override the six recipes, e.g. to pin digests or use a
registry mirror), `RIVET_KEY_DIR`/`RIVET_ACTIVE_KEY_ID`.

### Workspace ownership (who bots run as)

Bots run as a non-root uid:gid inside their containers and must own their
workspace files on the host. The panel picks it at start:

| Situation | Container user and workspace owner |
| --- | --- |
| `RIVET_CONTAINER_USER` and/or `RIVET_WORKSPACE_OWNER` set | exactly what is set (never overridden) |
| Neither set, panel runs as root or holds `CAP_CHOWN` (the systemd unit) | the dedicated unprivileged `65532:65532` |
| Neither set, no `CAP_CHOWN`, `RIVET_ENV=development` | **the panel's own uid:gid** for both (automatic) |
| Neither set, no `CAP_CHOWN`, production with `RIVET_ALLOW_SHARED_UID=1` | **the panel's own uid:gid** for both (opted in) |
| Neither set, no `CAP_CHOWN`, production without the opt-in | **the panel refuses to start** and says why |

The fallback is what happens when you try the panel out with your own
account in development: without it, the panel could not hand files to
`65532:65532` and no bot could write its workspace. The panel logs a warning
and **Administration → Diagnostics** shows an informational "Workspace
ownership" check (a warning when production runs that way through
`RIVET_ALLOW_SHARED_UID=1`; `rivetpanel doctor` reports the refusal as a
failure).

Security note: in that fallback the containers are still not root inside
the container, but they run with the **same uid as the panel process on the
host**, the uid that owns `rivetpanel.db` and the key folder. A container
escape, or a bind mount that exposes more than the workspace, would then act
as the panel's user (which can reach the Docker socket and decrypt every
sealed secret) instead of a uid that owns nothing else. That is why
production refuses it unless `RIVET_ALLOW_SHARED_UID=1` is set. For
production run the panel as root through the systemd unit, which keeps the
dedicated unprivileged user, give it `CAP_CHOWN`, or set both variables
explicitly. Under rootless Docker or `userns-remap` the container uid is not
the host uid: set both variables explicitly (see `isolation.md`).

Workspaces created while the panel ran as another user (for example as root
with `65532:65532`) are repaired at start when the panel can; otherwise
Diagnostics lists them as "Workspaces owned by another user", the bot's start
error names the file, and `sudo chown -R <uid:gid> <data root>` fixes them.
`rivet-agent serve` applies the same rule when `--user`/`--workspace-owner`
and `RIVET_AGENT_CONTAINER_USER`/`RIVET_AGENT_WORKSPACE_OWNER` are unset: it
falls back automatically only with `RIVET_ENV=development`, otherwise it
refuses to start unless `--allow-shared-uid` (or
`RIVET_AGENT_ALLOW_SHARED_UID=1`) is given, because its own uid owns the node
key and certificate.

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
  logs. The panel also keeps one file per day of its own log and of every
  server's console under `/var/lib/rivetpanel/logs` and gzips ended days into
  `/var/lib/rivetpanel/log-archive` at the archive time (default 00:00, 30
  days kept); see `logs.md`.
* Read `docs/isolation.md` before hosting untrusted users: there is no egress
  policy, admission control or disk quota out of the box.
* `rivetpanel doctor` runs the Diagnostics checks from the command line. It
  is read-only: it opens the database read-only, runs no migrations and
  creates no directories (a missing database or data root is reported, not
  created); the only write is a short-lived ownership probe file in the data
  root.
* `rivetpanel health` asks `/api/v1/healthz` on `RIVET_LISTEN` (the container
  image's `HEALTHCHECK`).

## Upgrades and rollback

Database migrations run automatically at start and are **forward-only**:
there are no down migrations, and an older binary refuses a database whose
schema is newer than it knows. Plan every upgrade so it can be undone by a
restore:

1. Read the release notes (`CHANGELOG.md`) for migrations and changed
   defaults.
2. Back up: `sudo rivetpanel backup /var/backups/rivetpanel/pre-X.Y.Z`
   (database snapshot and workspaces; keep the key directory backed up
   separately, or add `--include-keys` and protect the result), then
   `rivetpanel backup-verify` it. Per-bot backups in `RIVET_BACKUP_DIR` and
   site releases are not part of it; copy them too if you rely on them.
3. Keep the old binary: `sudo cp /usr/local/bin/rivetpanel /usr/local/bin/rivetpanel.prev`.
4. Install the new release (`deploy/install.sh`, or a new image tag), start it
   and check the log, `rivetpanel doctor` and `/api/v1/readyz`. Bots keep
   running during the restart; the panel adopts their containers.

To roll back, stop the panel, put the previous binary back, restore the
backup taken before the upgrade with that binary
(`sudo rivetpanel restore --force /var/backups/rivetpanel/pre-X.Y.Z`), and
start it. Changes made after the upgrade (new bots, settings, accounts) are
lost; bot containers started by the newer version are reconciled to the
restored state. For the container image, pin the previous tag and restore the
backup into the data directory the same way (`docker compose run --rm
rivetpanel restore --force ...` with the backup inside the mounted directory).

## Upgrading from BotPanel 0.4.0

RivetPanel is a deliberate clean break from BotPanel 0.4.0 (also published as
BotForge). It does not read, convert or modify a BotPanel installation: the
service, data directory, environment prefix, container labels and backup
format all changed, and BotPanel backups cannot be restored (`backup-verify`
and `restore` say so). Accounts, bots, environment variables and settings must
be re-created.

The panel looks for traces of a former installation at start: environment
variables with the former prefixes, the former data directory, a former
database next to the RivetPanel database, and **running** containers with the
former management label. Each one is logged as a `WARNING` and shown in
Administration → Diagnostics as "Former installation"; nothing is changed.

The step-by-step clean-up (stopping and disabling the former service,
removing its containers so that no Discord token runs twice, moving its data
aside, the new SFTP host key, and re-creating accounts and bots) is in
[`upgrading-from-0.4.md`](upgrading-from-0.4.md).

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
