# Containers and Compose stacks (preview)

A **container app** runs a Docker image you choose, such as `redis:7`,
`nginxinc/nginx-unprivileged:1.27` or `ghcr.io/owner/app:v1`. A **stack**
is a Compose file imported as several container apps that reach each other
by service name. Both live under **Apps → Containers** next to bots, game
servers and websites, and use the same console, files, variables, network,
services, backups, schedules, analytics and access tabs.

**One-click apps** install ready-made self-hosted apps (Ghost, Vaultwarden,
n8n and more) as stacks with their database and sign-in already set up; see
[One-click apps](#one-click-apps).

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

Variables of a container app may use any name except `RIVET_*`, including
`PATH` and `LD_*` (reserved for bots, whose runtime limits which commands
run; a container runs whatever its image does anyway).

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

Each stack gets its own private Docker network, which takes a subnet from the
Docker daemon's address pools. With Docker's defaults there are only about 30
such subnets on a host (shared with every other Compose project), so a host
running many stacks should give Docker smaller, more numerous subnets in
`/etc/docker/daemon.json`, then restart Docker:

```json
{ "default-address-pools": [{ "base": "10.200.0.0/16", "size": 24 }] }
```

The panel removes a stack's network when the stack is deleted, and removes
idle networks of stopped stacks; they are created again on the next start.

## One-click apps

**Apps → Containers** lists the catalog below your containers (also under
**Templates → Apps** and **New → Container → One-click app**). Search or pick
a category, choose an app, then:

1. Give it a name and choose **where it opens**: a **web address** (a site of
   this panel, such as `ghost.sites.example.com`, which can get its own
   domain later) or **port only** (a free host port of the panel's range,
   bound to 127.0.0.1 unless `RIVET_PORT_PUBLIC_BIND=1`).
2. Fill in the few values the app cannot choose itself (usually none, or an
   admin user name). Passwords, database passwords and secret keys are
   generated on the server with `crypto/rand`; **More options** lets you set
   them yourself.
3. **Install.** The stack page shows the containers starting (the first
   start pulls the images), then an **Open** button, the sign-in values with
   a **Show** button (each reveal is audited like any variable reveal) and the
   app's first steps.

The catalog (25 apps):

| Category | Apps |
| --- | --- |
| Publishing | Flarum, Ghost |
| Productivity | Actual Budget, Clipboard Vault, Excalidraw, Mealie, Memos, n8n, Stirling-PDF, Vikunja, Wiki.js |
| Developer tools | CyberChef, Gitea, IT-Tools |
| Monitoring | Gotify, ntfy, Umami, Uptime Kuma |
| Reading | linkding, Miniflux |
| Media and files | Navidrome |
| Security | PrivateBin, SearXNG, Vaultwarden |
| AI and agents | Herdr, ZeroClaw |

Many apps create their administrator on the first visit (Ghost, Gitea,
n8n, Uptime Kuma, Wiki.js, Memos and others) and a few start with a published
default login (Mealie, Umami). With a public web address, open the app and
finish that step right after installing, before someone else does; the app's
notes say which step it needs.

How it works and what it guarantees:

* A template is a reviewed Compose file plus the variables it needs, embedded
  in the panel (`internal/apptemplates/data/<id>/`). Installing it fills the
  variables in and imports the file through the **same Compose import** as a
  pasted file, so every container gets the same isolation: non-root user,
  read-only root filesystem, no capabilities, `no-new-privileges`, memory, CPU
  and process limits, and only allowed registries. This is enforced by the
  application (the import refuses everything else) and the container runtime.
* The stored Compose file keeps `${VAR}` placeholders; values live only in
  each container's encrypted variables. Change them under the container's
  Variables (stopped) like any app.
* Images are pinned to exact versions. Updating an app means changing a
  container's image under Configuration; read the app's upgrade notes first.
* Data lives in the containers' volumes (`Files → volumes/…`) and is part of
  their backups. Deleting the stack deletes the containers, their data and
  the app's web address.
* The installer reserves `APP_URL`, `APP_HOST`, `APP_PORT`, `APP_SCHEME`,
  `APP_SECURE`, `APP_ORIGIN` and `STACK_NAME`; an app that must know its own
  address (Ghost, Gitea, n8n…) gets it from `APP_URL`. Changing the address
  after install is not offered yet: install the app again, or change
  the address variables of its containers by hand.
* An administrator can hide apps by narrowing `RIVET_CONTAINER_REGISTRIES`:
  an app whose image comes from a registry that is not allowed is shown as
  unavailable with the reason.

**Trusted capabilities.** A built-in template may ask that one of its
services gets back one or more of `CHOWN`, `SETUID`, `SETGID`,
`DAC_OVERRIDE` and `FOWNER` (for images that cannot run otherwise). Only the
template installer can grant them: a pasted Compose file still has `cap_add`
refused, the API has no field for it, the Docker adapter drops any capability
outside this list, the root filesystem stays read-only, `no-new-privileges`
stays on and root (`user: 0`) is still refused. Changing such a container's
image to another repository removes the capabilities. Enforced by the
application and the container runtime, not by the kernel beyond Docker's own
capability handling.

### Web addresses for containers

A one-click app installed with a web address is a site (Websites list) in
**app** mode whose app is the template's web container. The container joins
the shared apps network (`RIVET_APPS_NETWORK`, `rivetpanel-apps`) under a
private alias, and the sites listener proxies the address to the container's
port, with HTTPS, custom domains, the request body limit and the health check
of any app site. The site does not own the container: deleting the site only
removes the address (the container leaves the apps network and keeps
running); deleting the stack deletes its sites. A container on the apps
network can be reached by other site apps on that network, as for web apps.
On a server behind a reverse proxy such as Traefik, route the sites domain
(or each custom domain) to the sites listener as for every site.

### Terminal

Container apps have a **Terminal** tab (owners and administrators of the app,
`full admin` on shared apps). It opens an interactive terminal (`docker exec`
with a TTY) inside the running container: the container's own user,
capabilities, read-only root filesystem and limits, never the host. One-click
apps used from a terminal (Herdr) start their own command there; **New
shell** opens `sh`. Every opened terminal is recorded in the audit log as
`bot.terminal` with the command. Terminals close after 30 minutes without
input. Changes outside volumes disappear when the container is recreated.

### Testing one-click apps

`tests/apps` installs apps on a running panel through its public API and
checks each one end to end: every container reaches running and stays
running, the web interface answers (or the terminal runs a command), it
survives a stack restart, and the stack is deleted cleanly. Use a disposable
panel with Docker:

```bash
RIVET_APPS_PANEL=http://127.0.0.1:8080 RIVET_APPS_EMAIL=admin@example.com \
RIVET_APPS_PASSWORD=… RIVET_APPS=all go test -tags apps -count=1 -timeout 120m -v ./tests/apps
```

`RIVET_APPS=miniflux,ntfy` picks apps, `RIVET_APPS_ADDRESS=site` installs with
a web address (needs site hosting), `RIVET_APPS_KEEP=1` keeps them installed.
The run ends with a pass/fail table.

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
| `DELETE /api/v1/stacks/{id}` | delete the stack, its services and their web addresses |
| `GET /api/v1/app-templates` | the one-click app catalog: each app's variables (never generated values), containers, memory, and whether this panel allows it |
| `POST /api/v1/app-templates/{id}/install` | `name`, `workspace_id`, `values` (by variable name), `address` (`port` or `site`), `slug`, `domain_id`, `dry_run`; returns the plan, the `url` and (201) the stack |
| `GET /api/v1/bots/{id}/terminal` | WebSocket: an interactive terminal in a running container app (`cmd` as a JSON list, `cols`, `rows`); binary frames carry keystrokes and output, text frames `{"type":"resize","cols":…,"rows":…}` |

Stacks installed from a template also carry `template_id`, `template` (first
steps, which variables are sign-in values and on which container, the web and
console containers), `url` and `site_id`.

Everything else (console, files, variables, ports, backups, power) uses the
same `/api/v1/bots/{id}/…` routes as every app.
