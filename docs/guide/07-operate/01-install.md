---
title: Install RivetPanel
lede: Put RivetPanel on a Linux host with systemd or Docker Compose, put HTTPS in front of it and create the first administrator.
icon: rocket
audience: admin
aliases: [operations]
glance:
  - Linux with cgroup v2 and Docker Engine
  - Listens on 127.0.0.1:8080 by default
  - The setup code changes on every restart
related: [operate/configuration, operate/upgrades, operate/backups, operate/security]
---

## Requirements

- Linux with cgroup v2 and Docker Engine, or a rootless Docker daemon on the
  **systemd** cgroup driver. At start the panel checks that memory, CPU and PID
  limits can be enforced and reports itself not ready otherwise.
  [enforced: panel]
- Go 1.27 or later and Node 22 or later to build from source. Neither is needed
  to run a built binary or the container image.

Choose one way to run it: the binary with systemd units, or the container
image with Docker Compose.

## Install with systemd

1. Build, or unpack a release archive:
   `make build` produces the UI plus `bin/rivetpanel` and `bin/rivet-agent`. A
   release archive (`make release`) has the same layout, with the binaries in
   `bin/`.
2. From the repository root or the archive's top directory, run
   `sudo deploy/install.sh`. It installs the binary, the directories and the
   systemd units. It creates no secrets, and it installs only the panel.
3. Create the encryption key and the agent certificate authority:
   `sudo RIVET_KEY_DIR=/etc/rivetpanel/keys rivetpanel keygen`. The service
   itself cannot write `/etc/rivetpanel`, so this step runs by hand. Back the
   keys up separately from database backups.
4. Start the panel and the nightly installation backup:
   `sudo systemctl enable --now rivetpanel rivetpanel-backup.timer`.
5. Create the first administrator (see below), then put a TLS reverse proxy in
   front of `127.0.0.1:8080`.

The environment file is `/etc/rivetpanel/rivetpanel.env` (mode `0640`), copied
from [`deploy/systemd/rivetpanel.env.example`](../../../deploy/systemd/rivetpanel.env.example)
on first install and never overwritten. See [Configuration](02-configuration.md).

`rivet-agent` is for remote nodes and is installed on each node with
`deploy/install-agent.sh`; see [Remote nodes](04-remote-nodes.md).

> [!NOTE]
> An installation that enabled remote nodes before `keygen` created the agent
> certificate authority runs `sudo rivetpanel keygen --agent-ca` once, with
> the service's `RIVET_KEY_DIR`. Until then the panel stops at start and
> explains that the key directory is read-only to it.

## Install with Docker Compose

Images are published at `ghcr.io/xenycx/rivetpanel`. GitHub Actions produces
the tags `latest`, `X.Y.Z`, `X.Y` and the commit SHA after the Go tests,
`go vet` and the web checks pass; a release tag must match the `VERSION` file.

```sh
curl -O https://raw.githubusercontent.com/xenycx/rivetpanel/main/compose.yaml
curl -o .env https://raw.githubusercontent.com/xenycx/rivetpanel/main/deploy/container.env.example
sudo install -d -m 0750 /var/lib/rivetpanel
docker compose run --rm rivetpanel keygen
docker compose run --rm rivetpanel create-admin you@example.com
docker compose up -d
```

Use `deploy/container.env.example` as `.env`, not the systemd example. The
image sets the listen address (`0.0.0.0:8080` inside the container, published
only on the host's `127.0.0.1`) and every path (database, keys, workspaces,
runtimes) under `/var/lib/rivetpanel`. The systemd example would override them
with a loopback listen address the published port cannot reach and a key
directory outside the data mount: `keygen` would then write a key the next
container cannot see, and the panel would stop with "no encryption keys".

The Compose file has commented ports for SFTP (`2022`), the sites listener
(`127.0.0.1:8081`) and remote nodes (`8444`); uncomment the ones you enable.

### The data directory must be a host directory

The database, encryption keys, agent certificate authority, bot workspaces,
add-on data, backups and logs all live under `/var/lib/rivetpanel` in the
container. Mount a **host directory** there, not a named volume.

- The panel creates bot containers through the host's Docker daemon, which
  resolves bind-mount sources on the host. The panel looks up its own
  container at start and translates its paths through this mount, so
  `/srv/rivetpanel:/var/lib/rivetpanel` works as well as the same path on both
  sides.
- Docker refuses private bind mounts from inside its own data directory, which
  is where named volumes live. With a named volume the panel stops at start
  with an explanation instead of failing every bot start.
- If a needed path is on no mount at all (for example `RIVET_DATA_ROOT`
  pointed at a directory inside the container), the panel stops at start and
  names the path.
- If the panel cannot identify its own container (another daemon, an unusual
  host name), it logs a warning and **Administration → Diagnostics** shows
  "Container data paths". Mount the directory at the same absolute path on the
  host and in the container.

Back up the host directory or use `rivetpanel backup` (see
[Backups](07-backups.md)), and store a copy of `/var/lib/rivetpanel/keys`
apart from database backups. Do not share the directory with a systemd
installation on the same host.

### Root and the Docker socket

The Docker socket mount is required by the in-process runner and gives the
panel root-equivalent control of the host. Do not treat the container as a
security boundary for the panel.

The panel runs as root inside its container on purpose. It hands workspaces to
the unprivileged bot user (`65532:65532` by default), which needs `CAP_CHOWN`,
and a non-root user with the Docker socket could start a root container
anyway. A non-root `USER` would add configuration (matching the host's
`docker` group id) without reducing what a compromise can do. Bot containers
always run as a non-root user with all capabilities dropped and a read-only
root file system. [enforced: docker] See [Security and isolation](08-security.md).

### Remote nodes with the image

The image also contains `rivet-agent`. To use remote nodes, enable the agents
module and publish raw TCP port `8444`. Run the agent directly on each remote
Docker host with `install-agent.sh` (the supported path), not in a container:
it creates containers through its own host's Docker daemon. Never route the
agent port through the HTTP reverse proxy.

## Create the first administrator

Create it promptly after the first start, before you expose the panel. Either:

- run `rivetpanel create-admin EMAIL` on the host. It asks for the password at
  a hidden prompt or reads `RIVET_ADMIN_PASSWORD`. For systemd, load the
  environment file first:
  `set -a; . /etc/rivetpanel/rivetpanel.env; set +a`.
- or use the first-run setup page. While the database has no accounts, the
  panel logs a one-time setup code and writes it to `setup-code` next to the
  database (mode `0600`). Find it with `journalctl -u rivetpanel | grep setup_code`
  or `docker compose logs rivetpanel | grep setup_code`, open `/setup` and
  enter it.

The setup code proves the person can read the server's log or files. It
changes on every restart, the page refuses requests without it, and setup
closes once an account exists. [enforced: panel] The setup wizard can also
take the first AI provider key; see [AI assistant](10-ai-assistant.md).

## Reverse proxy and TLS

The panel listens on loopback and sets `Secure` session cookies in
production, so it must be reached over HTTPS. Set `RIVET_PUBLIC_URL` to the
proxy's public origin.

```
# Caddy
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

The proxy must:

- pass WebSocket upgrades on `/api/v1/bots/*/console`;
- preserve `Host` and `Origin`: the console rejects cross-origin handshakes.
  [enforced: panel]
- not rewrite the HTML (script injection, minifying), which breaks the
  Content-Security-Policy hashes below. Turn such features off for the panel.

### Client addresses behind a proxy

Behind a proxy on the same host, set `RIVET_PROXY_HEADER` so rate limits apply
per client instead of per proxy: `X-Forwarded-For` for Caddy and nginx,
`CF-Connecting-IP` for Cloudflare. The header is trusted only from loopback
peers [enforced: panel], so the proxy must run on the same machine; without
the setting every client shares the proxy's address. Cloudflare Tunnel setup
is covered with sign-in in [Sign-in](../06-teams/03-sign-in.md).

### Security headers

- With an `https://` public address in production the panel sends
  `Strict-Transport-Security: max-age=31536000` itself. It has no
  `includeSubDomains`, so services on sibling names are unaffected; add that,
  or `preload`, in the proxy if you want them. [enforced: panel]
- Pages carry a Content-Security-Policy whose `script-src` allows only the
  panel's own files and the SHA-256 hashes of the two inline scripts in the
  built page (theme setup and SvelteKit's start script). `style-src` still
  allows inline styles, because Svelte sets style attributes and transition
  rules at runtime. [enforced: panel]

### Plain HTTP warning

If `RIVET_LISTEN` is not a loopback address while `RIVET_PUBLIC_URL` is plain
`http://` on a non-local host, the panel logs a warning at start and
**Administration → Diagnostics** shows "Unencrypted public address". It still
starts, because some deployments terminate TLS on another machine, but
passwords and session cookies then cross that network unencrypted.
[not enforced]

## Verify the installation

| Check | What it tells you |
| --- | --- |
| `GET /api/v1/healthz` | Liveness: the process answers |
| `GET /api/v1/readyz` | Reports `database` and `docker` separately; `503` when either is down |
| `rivetpanel health` | Asks `/api/v1/healthz` on `RIVET_LISTEN`; it is the image's `HEALTHCHECK`, so `docker compose ps` shows `(healthy)` |
| `rivetpanel doctor` | Runs the Diagnostics checks from the command line; see [Diagnostics and footprint](12-diagnostics.md) |

Docker going away does not stop the panel or the bots: the runner reconnects
and re-syncs. Restarting the panel does not restart bots either; it adopts
running containers.

## Commands

| Command | What it does |
| --- | --- |
| `rivetpanel version` | Prints the version |
| `rivetpanel keygen` | Creates the encryption key and agent certificate authority (`--agent-ca` adds only the authority) |
| `rivetpanel create-admin EMAIL` | Creates an administrator |
| `rivetpanel reset-password EMAIL` | Sets a new password for an account |
| `rivetpanel reset-mfa EMAIL` | Turns off two-step sign-in for an account |
| `rivetpanel doctor` | Read-only diagnostics |
| `rivetpanel health` | Liveness probe |
| `rivetpanel verify` | Checks that every stored secret decrypts with the keys present |
| `rivetpanel reseal` | Re-encrypts every sealed value with the active key |
| `rivetpanel backup`, `backup-verify`, `restore` | Installation backups; see [Backups](07-backups.md) |
| `rivetpanel env` | Lists or resets values saved under **Administration → Environment** |
| `rivetpanel agent-token …` | Enrollment tokens for remote nodes |

`make release VERSION=x.y.z` builds Linux amd64 and arm64 archives with
`SHA256SUMS`.
