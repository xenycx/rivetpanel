# Container deployment

Published images are available from `ghcr.io/xenycx/rivetpanel`. Tags named
`latest`, `X.Y.Z`, `X.Y`, and the commit SHA are produced by GitHub Actions
after the Go tests, `go vet` and the web checks pass; a release tag must match
the `VERSION` file.

```sh
curl -O https://raw.githubusercontent.com/xenycx/rivetpanel/main/compose.yaml
curl -o .env https://raw.githubusercontent.com/xenycx/rivetpanel/main/deploy/container.env.example
sudo install -d -m 0750 /var/lib/rivetpanel
docker compose run --rm rivetpanel keygen
docker compose run --rm rivetpanel create-admin you@example.com
docker compose up -d
```

Use `deploy/container.env.example` as `.env`, **not** the systemd example
(`deploy/systemd/rivetpanel.env.example`). The image sets the listen address
(`0.0.0.0:8080` inside the container, published only on the host's
127.0.0.1) and every path (database, keys, workspaces, runtimes) so that
they live under `/var/lib/rivetpanel`. The systemd example would override
them with a loopback listen address the published port cannot reach and a key
directory outside the data mount, so `keygen` would write a key that the
next container cannot see and the panel would stop with "no encryption keys".

Instead of `create-admin` you can open the panel and enter the one-time setup
code from the log (`docker compose logs rivetpanel | grep setup_code`).

## Data directory: a host directory at /var/lib/rivetpanel

The database, encryption keys and agent certificate authority, bot
workspaces, add-on (database) data, backups and logs all live under
`/var/lib/rivetpanel` in the container. Mount a **host directory** there,
not a named volume:

* The panel creates bot containers through the host's Docker daemon, which
  resolves bind-mount sources on the **host**. The panel looks up its own
  container at start and translates its paths through this mount, so
  `/srv/rivetpanel:/var/lib/rivetpanel` works as well as the same path on
  both sides.
* Docker refuses private bind mounts from inside its own data directory,
  which is where named volumes live. With a named volume the panel stops at
  start with an explanation instead of failing every bot start.
* If a needed path is on no mount at all (for example `RIVET_DATA_ROOT`
  overridden to a directory inside the container), the panel stops at start
  and names the path. If it cannot identify its own container (another
  daemon, unusual host name), it logs a warning and Administration →
  Diagnostics shows "Container data paths": then mount the directory at the
  same absolute path on the host and in the container.

Back up the host directory (or use `rivetpanel backup`, see `backup.md`). The
keys are in `/var/lib/rivetpanel/keys`; store a copy separately from the
database backups. Do not share the directory with a systemd installation on
the same host.

## Security: root and the Docker socket

The Docker socket mount is required by the in-process runner and gives the
panel root-equivalent control of the host. Do not describe the container as a
security boundary for the panel.

The panel runs as root inside its container on purpose. It hands workspaces to
the unprivileged bot user (`65532:65532` by default), which needs
`CAP_CHOWN`, and access to the Docker socket would let a non-root user start a
root container anyway, so a non-root `USER` would add configuration (matching
the host's `docker` group id) without reducing what a compromise can do. Bot
containers themselves always run as a non-root user with all capabilities
dropped and a read-only root file system (see `isolation.md`).

## Health, TLS and upgrades

The image has a `HEALTHCHECK` that runs `rivetpanel health`, which asks the
panel's `/api/v1/healthz`; `docker compose ps` shows `(healthy)`.

Use a reverse proxy for HTTPS and set `RIVET_PUBLIC_URL` to its public origin.
The panel warns at start (and in Diagnostics) when it listens beyond loopback
and the public address is plain `http://` on a non-local host.

Pin a version tag instead of `latest` for controlled upgrades, and back up
before each upgrade: migrations are forward-only (see "Upgrades and rollback"
in `deployment.md`).

## Remote nodes

The image also contains `rivet-agent`. For remote nodes, enable the agents
module on the panel and publish raw TCP port 8444 (the commented Compose port),
then run the agent on each remote Docker host with its own state/data
directory. The agent creates bot containers through its own host's Docker
daemon, so run it directly on the node (the supported path, `install-agent.sh`)
rather than in a container. Do not send the agent port through the HTTP
reverse proxy. The supported systemd enrollment path and current
remote-feature limits are in [`agents.md`](agents.md).
