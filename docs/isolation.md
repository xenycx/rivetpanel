# Isolation and its limits

## What every container gets

Bots, game servers, site web apps, container apps, add-ons and every build,
install or release container share one host configuration. Enforced by the container
runtime through `internal/docker.HostConfig` (unit-tested) and asserted from
**inside real containers** by `tests/integration`:

| Setting | Value |
| --- | --- |
| Memory / swap | `Memory = MemorySwap = configured cap` |
| CPU | `NanoCPUs` |
| PIDs | `PidsLimit` |
| User | non-root `uid:gid` (default 65532:65532), workspace chowned to it |
| Capabilities | drop ALL; no privileged mode, no devices |
| Security | default seccomp kept, `no-new-privileges` |
| Root filesystem | read-only; `/tmp` tmpfs (`nosuid,nodev`, bounded) |
| Mounts | the unit's own workspace at `/workspace` (bind, `CreateMountpoint=false`); a site web app also gets its persistent data folder at `/data`; add-ons get only their own data folder |
| Host namespaces / devices / Docker socket | none |
| Published ports | none unless configured (see *Published ports* and *Game servers* below) |
| Logging | `json-file`, 10 MiB x 3 |
| Restart policy | none (the runner decides) |
| Command | the main process runs in exec form (`Entrypoint`), no shell; build, install and release commands run as `sh -c` in their own limited containers |

Builds run in separate resource-limited containers that receive only the
runtime recipe's environment, never a bot's secrets. Image references are
chosen by administrators, never by the person starting a unit, except for
container apps (below): bots and site web apps use the runtime catalog
(`runtimes/*.yaml`); game servers use their
server type's blueprint, either built in (`blueprints/*.yaml`) or saved by an
account with the `blueprints.manage` permission, including drafts imported
from Pterodactyl eggs, whose image references are validated for shape only
and then pulled as written. Treat that permission as able to choose any image
the host runs. A Start request cannot supply an image, mount or flag.
Dockerfiles are never built.

## Evidence

Verified on Linux 7.2, cgroup v2, **rootless Docker 29.7.2 with the systemd
cgroup driver**, by `make integration` (see README for results): for all six
runtimes the kernel-reported `memory.max`, `cpu.max` and `pids.max` inside the
container equal the configured limits; the root filesystem rejects writes; the
workspace is writable and visible on the host; no Docker socket exists; a bot
that allocates past its cap is OOM-killed and reported as
`killed: out of memory` (exit 137).

The runner refuses to start reconciling unless the daemon can enforce memory,
CPU and PID limits. A rootless daemon on the **cgroupfs** driver reports the
controllers as available but silently ignores limits (observed during
development); the adapter detects rootless + non-systemd and reports the host
as unable to enforce limits.

## Not provided (do not assume otherwise)

* **Admission limits are application rules, not kernel limits.** The panel
  refuses to create apps or sites past a workspace's limits (count, memory,
  CPU) and refuses starts past `RIVET_NODE_MEMORY_BYTES` (`features.md`,
  `workspaces.md`). These are enforced by the application at creation and
  start, not by the kernel; disk usage and inodes are not counted at all.
* **No disk quota.** Workspaces and Docker logs consume host disk (logs are
  rotated; workspaces are not capped beyond upload limits).
* **Egress is not restricted by RivetPanel.** Discord needs outbound access, and
  the default Docker bridge lets a bot reach host services and other private
  networks (e.g. cloud metadata at 169.254.169.254, the Docker host's own
  ports). You must add a host firewall policy. A starting point, **not verified
  in this repository's test environment** (rootless, no privileges):

  ```sh
  docker network create --driver bridge --subnet 172.30.0.0/16 \
      -o com.docker.network.bridge.name=rvbr0 \
      -o com.docker.network.bridge.enable_icc=false rivetpanel
  # RIVET_CONTAINER_NETWORK=rivetpanel
  nft add table inet rivetpanel_egress
  nft add chain inet rivetpanel_egress fwd '{ type filter hook forward priority -1; }'
  nft add rule  inet rivetpanel_egress fwd iifname rvbr0 ip daddr '{ 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 }' drop
  nft add chain inet rivetpanel_egress in  '{ type filter hook input priority -1; }'
  nft add rule  inet rivetpanel_egress in  iifname rvbr0 drop
  ```
* **The panel process is root-equivalent** (Docker socket). Its systemd sandbox
  reduces blast radius but cannot isolate that privilege; only a separate
  runner process could, and none is implemented.
* Container user ID mapping: under rootless Docker or `userns-remap`, container
  uid N is not host uid N. Set `RIVET_WORKSPACE_OWNER` to the *host* uid:gid
  that maps to the container user. The integration tests ran the container as
  uid 0, which under rootless Docker maps to the unprivileged host user; the
  default non-root 65532 path (which needs CAP_CHOWN) was **not** exercised.
* Runtime image digests are resolved and pinned in memory per process. To pin
  permanently, put `digest:` / `builder_digest:` in the catalog YAML.

## Extensions (preview)

Extension code runs inside the panel process as WebAssembly (wazero), so it
shares the panel's privileges at the operating-system level; the boundary is
the WebAssembly sandbox and the host-call checks, not a separate process or
user. It has no files, environment, sockets or direct Docker access; it can
reach the outside only through `http.fetch` to the hosts its manifest lists
(private, local and metadata addresses are refused), and bot actions go
through the same authorization as the API, as the person using it. Memory is
capped per extension and calls have a wall-clock deadline; there is no CPU
metering within a call. Custom pages run in a sandboxed frame with an opaque
origin and no network. Database-browser queries run in disposable,
capability-free client containers on the bot's private add-on network; their
read-only mode guards against mistakes and is not a security boundary. See
`docs/extensions.md`.

## Added by the MVP feature set

Each of these widens what a bot or a user can reach; none changes the container
defaults above (dropped capabilities, read-only root, no-new-privileges, hard
limits).

* **Published ports** are opt-in per bot and made by the owner or an
  administrator only, within `RIVET_PORT_RANGE`, on `127.0.0.1` unless the
  administrator enables public binding. A published port is reachable by whoever
  can reach that host address; put authentication in front of it. Verified with
  a real container: a bot's HTTP server answered on its published loopback port.
* **Outbound access off** runs the bot on Docker's `none` network (no interfaces
  but loopback). Verified: a DNS lookup from such a container failed. Build
  containers keep network access (they must download dependencies), so a build
  script can still reach the internet and, without the egress policy above, your
  private networks.
* **Bandwidth limits are not enforced** (see `features.md`).
* **GitHub deployments** never run repository code on the host: the panel
  downloads a tarball and unpacks it with the contained extractor. Repository
  build scripts run only inside the resource-limited build container, exactly as
  uploaded code does.
* **Custom build commands** run as `sh -c` in the same build container (same
  limits, network access, no bot variables). They add no privilege beyond the
  package-manager install scripts that already run there, but a suggested
  command (from repository analysis or the AI) should be reviewed before use.
* **Add-ons** (PostgreSQL, Redis, MongoDB, MariaDB) run in their own containers
  with the same defaults as bots (unprivileged container user, read-only root,
  all capabilities dropped, `no-new-privileges`, memory/CPU/PID limits), on a
  per-bot Docker network created with `Internal: true`. Verified on Docker
  29.7.2: a container on that network could not resolve or reach the internet,
  while the bot (default bridge plus the private network) reached both the
  internet and its add-on by host name. Other bots are not attached to the
  network and cannot reach it. Redis runs without a password; the network is
  its only access control. The panel host (root-equivalent anyway) can reach
  bridge addresses directly.
* **Discord avatar lookup** sends the bot's own token from the panel to
  `discord.com` only, on request.
* **Java builds download Maven** into the workspace inside the build container,
  verified against a SHA-512 pinned in `runtimes/java.yaml`. Dependencies then
  come from Maven Central. Point `RIVET_RUNTIMES_DIR` at your own recipe to
  use a mirror.
* **SFTP** runs in the panel process (root-equivalent, like everything else
  here) but accesses files only through the contained filesystem layer.
* **Kill** sends `SIGKILL` through the Docker API; it needs the same socket
  access as every other lifecycle action.

## Game servers

* **Ports are public by default.** A game server's allocations are published
  for TCP and UDP on `0.0.0.0` (or the allocation's bound IP), unlike a bot's
  opt-in loopback ports, because players must reach them. Allocations come
  from the pool under Administration → Allocations.
* **Installation** downloads server software in the panel (checksums verified
  where the provider publishes them) and runs the blueprint's install script,
  or SteamCMD with an anonymous login, in a separate install container with
  the same defaults as above, as the unprivileged server user, with network
  access. Install scripts from imported eggs run the same way.
* The same egress caveat applies: a game server reaches the internet and,
  without a host firewall policy, your private networks.

## Site web apps

* **Shared apps network.** Every site web app joins one routable Docker
  bridge (`RIVET_APPS_NETWORK`, default `rivetpanel-apps`) so the sites
  listener can proxy to it. Docker's default inter-container communication is
  on for this network, so **site apps can reach each other** by address; treat
  them as mutually trusting or add a firewall policy. Their add-ons stay on
  each app's own internal network.
* Apps are reached only through the sites listener; they publish no host
  ports. They always run on the panel's own host, not on remote nodes.
* A **release command** (for example database migrations) runs as `sh -c`
  once per deployment in a one-shot container shaped like the app's own: same
  image, variables, networks and `/data`.

## Container apps and stacks (preview)

A container app runs an image its owner chose. What changes, and what does
not:

* **Image source is a registry allowlist, enforced by the application.**
  `RIVET_CONTAINER_REGISTRIES` (default `docker.io,ghcr.io,quay.io`) is
  checked when the app is created or its image changes, and again by the
  runner before every pull, so narrowing the list stops existing apps from
  pulling refused images. The Docker daemon contacts the allowed registries
  from the host network. A registry account is never stored: only public
  images work.
* **Image size is checked after the pull** against
  `RIVET_CONTAINER_MAX_IMAGE_BYTES` (default 2 GiB); a refused image stays in
  Docker's image store until pruned. Pulls use host disk, which is not
  otherwise limited.
* **Image code runs as a non-root user** (the panel's container user or a
  numeric `uid:gid` above 0, validated by the application) with the same
  runtime-enforced defaults as every app: read-only root filesystem, all
  capabilities dropped, `no-new-privileges`, default seccomp, memory, CPU
  and PID limits, no host namespaces, devices or Docker socket. Writable
  paths are 16 MiB tmpfs mounts; volumes are bind mounts of
  `volumes/<name>` inside the app's own files, so they stay within backups.
  Volume and writable paths under `/proc`, `/sys`, `/dev` and Docker's
  `/etc/hosts`, `/etc/hostname` and `/etc/resolv.conf` are refused.
* **Ports** follow the same opt-in published-port policy as bots.
* **Stacks** add one Docker network per stack (`rivetpanel-stack-<id>`),
  created `Internal: true` like add-on networks: services reach each other
  by service name and nothing else; each service keeps its own outbound
  access on the default network unless it is turned off. Different stacks
  and standalone apps are not attached to each other's networks.
* **Compose import is translation, not execution.** The panel never runs
  `docker compose`; it maps a fixed set of keys onto container apps and
  refuses privileged mode, added capabilities, security options, devices,
  sysctls, host PID/IPC/network namespaces, the Docker socket, builds and
  other keys that would widen the boundary (`containers.md`).

Compared with a bot, the new exposure is the image itself: its code and
whatever it downloads at start run in the same sandbox a bot's code runs
in, but its supply chain is the image publisher's. Treat container apps as
trusted-tenant until egress and disk quotas are enforced (see *Not
provided*).

