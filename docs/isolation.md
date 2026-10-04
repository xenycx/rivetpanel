# Isolation and its limits

## What every bot and build container gets

Enforced in `internal/docker.HostConfig` (unit-tested) and asserted from
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
| Mounts | only the bot's own workspace at `/workspace` (bind, `CreateMountpoint=false`) |
| Ports / host namespaces / Docker socket | none |
| Logging | `json-file`, 10 MiB x 3 |
| Restart policy | none (the runner decides) |
| Command | exec form (`Entrypoint`); no shell, no interpolation |

Builds run in separate resource-limited containers that receive only the
runtime recipe's environment, never a bot's secrets. Image references come from
the administrator-controlled catalog (`runtimes/*.yaml`); a Start request cannot
supply an image, mount or flag.

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

* **No admission control or quotas.** Per-container caps do not bound the sum
  across bots, disk usage, or inodes. Nothing stops a node being overcommitted.
  Define node/user budgets before offering this to untrusted users.
* **No disk quota.** Workspaces and Docker logs consume host disk (logs are
  rotated; workspaces are not capped beyond upload limits).
* **Egress is not restricted by RivetPanel.** Discord needs outbound access, and
  the default Docker bridge lets a bot reach host services and other private
  networks (e.g. cloud metadata at 169.254.169.254, the Docker host's own
  ports). You must add a host firewall policy. A starting point, **not verified
  in this repository's test environment** (rootless, no privileges):

  ```sh
  docker network create --driver bridge --subnet 172.30.0.0/16 \
      -o com.docker.network.bridge.name=bpbr0 \
      -o com.docker.network.bridge.enable_icc=false rivetpanel
  # RIVET_CONTAINER_NETWORK=rivetpanel
  nft add table inet rivetpanel_egress
  nft add chain inet rivetpanel_egress fwd '{ type filter hook forward priority -1; }'
  nft add rule  inet rivetpanel_egress fwd iifname bpbr0 ip daddr '{ 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 }' drop
  nft add chain inet rivetpanel_egress in  '{ type filter hook input priority -1; }'
  nft add rule  inet rivetpanel_egress in  iifname bpbr0 drop
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
