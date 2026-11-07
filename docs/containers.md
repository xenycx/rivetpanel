# Containers and Compose stacks (preview)

A **container app** runs a Docker image you choose, such as `redis:7`,
`nginxinc/nginx-unprivileged:1.27` or `ghcr.io/owner/app:v1`. A **stack**
is a Compose file imported as several container apps that reach each other
by service name. Both live under **Apps → Containers** next to bots, game
servers and websites, and use the same console, files, variables, network,
services, backups, schedules, analytics and access tabs.

Containers are a preview module (`containers`), on by default. Turn it off
under Administration → Modules, or allow no registry
(`RIVET_CONTAINER_REGISTRIES=`), to hide them.

## Run an image

New → Container → **Docker image** (`/containers/new`):

- **Image**: a reference from an allowed registry. A reference without a
  registry is Docker Hub (`nginx` is `docker.io/library/nginx:latest`).
  Prefer a version tag: a moving tag such as `latest` is pulled once per
  host, and **Pull again** under Configuration fetches a newer one (and
  restarts the app when it runs).
- **Ports**: container ports to publish. An empty host port takes the next
  free one from `RIVET_PORT_RANGE`. Ports are published on `127.0.0.1` unless
  the administrator allows public binding (`RIVET_PORT_PUBLIC_BIND`).
- **Variables**: `KEY=value` lines, encrypted at rest like every app's.
- **Volumes**: a name and a folder inside the container. The data lives in
  the app's files under `volumes/<name>`, survives restarts and image
  changes, is included in backups and can be edited in Files or over SFTP.
- **Command**, **writable paths**, **run as** and **memory** are optional.
  An empty command keeps the image's own entrypoint and command.

Under **Configuration** the image, volumes, writable paths and user can be
changed at any time: a running app gets a new container with the new
settings. The command and restart policy need a stopped app.

## What every container gets

Containers run under the same isolation as every other app
(`isolation.md`), enforced by the container runtime:

- a **read-only root filesystem**: images that write outside their volumes
  need those folders listed under **Writable paths** (16 MiB in-memory
  folders, empty at every start; `/tmp` is always writable);
- **no root**: the panel's container user (`RIVET_CONTAINER_USER`), or a
  numeric `uid:gid` above 0 under **Run as**; user names are not accepted.
  Images that insist on starting as root need their unprivileged variant
  (for example `nginxinc/nginx-unprivileged` instead of `nginx`). Docker
  lets unprivileged processes bind ports below 1024 inside the container;
- **no capabilities**, `no-new-privileges`, the default seccomp profile,
  memory, CPU and process limits, and no host namespaces, devices or Docker
  socket;
- the image's own working directory (no `/workspace` mount).

The panel refuses an image from a registry outside
`RIVET_CONTAINER_REGISTRIES` when the app is created and again at every
start (the policy may have narrowed), before anything is pulled. After the
pull, an image larger than `RIVET_CONTAINER_MAX_IMAGE_BYTES` is refused.
Pulled images use the host's disk; RivetPanel does not otherwise limit it.

Container apps run on the panel's own host; remote nodes do not run them
yet. They count toward a workspace's **Bots** limit and its apps, memory and
CPU limits. GitHub deployments, packages and build commands do not apply:
the image is the code.

## Import a Compose file

New → Container → **Compose stack** (`/stacks/new`). Paste `compose.yaml`
(or open the file) and, optionally, a `.env` for `${VAR}` interpolation, then
**Preview**. The preview is the exact plan the import follows: every service
with its image, command, published ports, volumes, writable paths, variable
names, dependencies and limits, every option the panel changes, and every
option it refuses. Nothing is created until **Create**, which is offered
only when the preview has no errors and the file has not changed since.

Each service becomes a container app named `<stack>-<service>`. The
services of a stack share a private network without internet access where
each is reachable by its service name (`db`, `redis`, …); each service also
keeps its own outbound access unless `network_mode: none`.

| Compose | Becomes |
| --- | --- |
| `image` | the app's image (the registry policy applies) |
| `command`, `entrypoint` | the app's command and entrypoint (strings are split like Compose does; no shell) |
| `environment` (map or list), `${VAR}`, `${VAR:-default}` | the app's variables; values come from the `.env` box, unset ones become empty with a warning |
| `ports` (`8080:80`, `127.0.0.1:8080:80/udp`, long form) | published ports; a host port outside `RIVET_PORT_RANGE` or in use moves to a free one, with a warning |
| `ports` without a host port (`"80"`), `expose` | nothing: other services reach every port on the stack network |
| named `volumes` | the app's volumes; a volume used by several services is copied per service (they do not share files), with a warning |
| `./relative:/path` bind mounts | an empty volume of that name, with a warning to upload the files |
| `/absolute:/path` host bind mounts | not mounted, with a warning |
| `tmpfs`, `type: tmpfs` | writable paths |
| `depends_on` (list or map) | the service waits until those services run; `service_healthy` and other conditions are treated as started |
| `user: "1000"` / `"1000:1000"` | run as that user; `root` or names are ignored with a warning |
| `restart: "no"` | never restarted; every other policy restarts after a crash |
| `mem_limit`, `cpus`, `deploy.resources.limits` | memory and CPU limits (default 512 MiB and one core) |
| `network_mode: none` | no outbound access (the stack network stays) |
| `profiles` | the service is skipped, as Compose does by default |

Refused (the preview lists them as errors): `build`, `privileged: true`,
`cap_add`, `security_opt`, `devices`, `sysctls`, `userns_mode`,
`cgroup_parent`, `runtime`, `gpus`, `isolation`, `pid`/`ipc` sharing,
`network_mode` other than `none` or `bridge`, `extends`, `include`,
`secrets`, `configs`, `volumes_from`, any mount of the Docker socket,
images from registries that are not allowed, and `depends_on` cycles.
Ignored with a note: `healthcheck`, `working_dir`, `extra_hosts`, `dns`,
`ulimits`, `init`, `platform`, `shm_size`, `env_file` and replicas above
one; silently: names, labels, logging and other cosmetic keys.

Well-known images get the writable paths they need: `nginx` (`/var/cache/nginx`,
`/var/run`), `postgres` (`/var/run/postgresql`), `mysql` (`/var/run/mysqld`),
`mariadb` (`/run/mysqld`), `httpd` and `mongo`.

At most 20 services and 256 KiB per file. The whole stack is checked
against the workspace's limits before anything is created; if any service
cannot be created, the ones already created are removed again.

## Stacks

The stack page (`/stacks/<id>`) lists its services with their state and
starts, stops or restarts all of them. Services start in any order and each
one waits (shown as **Waiting**) until the services it depends on run. The
Compose file is kept for reference; later changes are made per service. A
service cannot be moved to another workspace or transferred on its own.
Deleting the stack deletes every service with its files, volumes and
backups.

## API

All routes need a signed-in session or an API client with the matching
permission; creating needs `bots.create`.

| Route | |
| --- | --- |
| `GET /api/v1/containers/policy` | allowed registries, image size limit, whether ports bind publicly |
| `POST /api/v1/containers` | create: `name`, `image`, `workspace_id`, `command`, `entrypoint`, `user`, `writable`, `volumes`, `ports`, `env`, `memory_bytes`, `nano_cpus`, `network_disabled`, `start` |
| `PUT /api/v1/bots/{id}/container` | change `image`, `user`, `writable`, `volumes` |
| `POST /api/v1/bots/{id}/container/pull` | pull the image again (restarts a running app) |
| `POST /api/v1/stacks/import` | `compose`, `env`, `name`, `workspace_id`, `dry_run`, `start`; returns the plan (and the stack when created) |
| `GET /api/v1/stacks`, `GET /api/v1/stacks/{id}` | stacks with their services |
| `PATCH /api/v1/stacks/{id}` | rename |
| `POST /api/v1/stacks/{id}/start` (`stop`, `restart`) | power for every service |
| `DELETE /api/v1/stacks/{id}` | delete the stack and its services |

Everything else (console, files, variables, ports, backups, power) uses the
same `/api/v1/bots/{id}/…` routes as every app.
