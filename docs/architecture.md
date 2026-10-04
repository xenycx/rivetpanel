# Architecture

```
Browser -> Fiber API -> authn/authz -> BotService -> SQLite (desired state)
                                              |             ^
                                    per-node routing        | observations
                                      /             \       |
                         local Runner               mTLS agent hub
                              |                           |
                        local Docker              rivet-agent -> node Docker
```

The control-plane process contains the API, embedded UI, local runner and
telemetry sampler. Per-node routing sends local work to that in-process runner
and remote work to `rivet-agent` over a TLS 1.3 + yamux connection initiated by
the node. The agent exposes its runner, files, logs, stdin, stats and game query
operations only inside that authenticated connection. Desired state remains in
the panel's SQLite database; the agent resynchronizes it after reconnecting.

**Neither runner is a security boundary from its Docker daemon.** The panel
holds the local Docker socket when local execution is enabled, and each agent
holds its node's root-equivalent Docker socket. Node certificates authenticate
the agent and bind it to one node; they do not attest a hostile node or enforce
network/kernel policy. See `docs/agents.md`.

## Desired vs observed state

`bots.desired_state` is intent (`stopped|running|deleted`); `observed_state` is
the runner's last observation. `generation` increases, in the same SQL
transaction, whenever intent or configuration changes (start, stop, restart,
edit, environment change). The runner writes observations with
`WHERE generation = ?`, so a pass working on stale intent cannot overwrite
newer intent. Configuration edits require a stopped bot (desired `stopped` and
observed `stopped|failed|unknown`).

| Request | Effect |
| --- | --- |
| `POST /bots/{id}/start` | desired=running; generation+1 only if it was not already running |
| repeated `start` | no change; returns the current generation (`202`) |
| `POST /bots/{id}/restart` | desired=running; always generation+1 (replaces the container) |
| `POST /bots/{id}/stop` | desired=stopped; generation+1 if it was running |
| `DELETE /bots/{id}` | desired=deleted, then containers, workspace, and only then the row |

All lifecycle calls return `202` immediately; nothing waits for pulls or builds.

## Reconciliation

One pass per bot, serialized by a per-bot lock, run by a bounded worker pool:

1. Read the bot. Not found: remove any orphan containers.
2. List managed containers by label (`rivetpanel.managed`, `bot_id`, `node_id`,
   `generation`, `role`, `spec`). Containers are named `rivetpanel-<id>-<role>`.
3. Reuse a runtime container only if generation and spec hash match; otherwise
   stop and remove it deliberately. Stale builders from an interrupted pass are removed.
4. Otherwise: `building` (resolve image, prepare workspace ownership, run the
   build stage if the recipe needs one) -> re-read intent -> `ContainerCreate`
   -> persist the container ID -> re-read intent -> `ContainerStart` -> `running`.
5. Failures record a **generic** message (never the Docker error text, which may
   contain paths or names) and retry on bounded exponential backoff (1 s .. 5 min).
   The gate applies to setup failures regardless of how many events arrive.

Docker and SQLite cannot commit together. Crash and lost-response safety come
from deterministic names, labels, serialization and re-listing before create,
not from a unique index. The spec hash excludes environment values (a change
bumps the generation instead) so a label never fingerprints a secret.

Recovery triggers: process start (live observations are demoted to `unknown`
first), Docker events, a 30 s resync that also removes orphan containers, and
loss of the Docker connection (the runner reports not-ready, then re-syncs).
The runner owns restarts; Docker's restart policy is disabled. A remote runner
also backs off when panel-backed resynchronization fails, so an offline control
plane cannot cause a reconnect/logging hot loop.

### Add-ons

A bot's add-ons (`bot_addons`) are reconciled by the same per-bot pass. Before
the runtime container is created, the runner ensures the internal network
`rivetpanel-<bot>-net`, creates or starts each add-on container
(`rivetpanel-<bot>-addon-<kind>`, role `addon`, reused while its spec hash, which
excludes the password, is unchanged), waits for their Docker health checks,
then creates the bot, connects it to the network and starts it. Stopping a bot
stops its add-ons; deleting it removes their containers, the network and the
data directory. Add-on containers are excluded from the "outdated container"
rule that replaces stale runtime containers.

## Console

`GET /bots/{id}/console` upgrades to a WebSocket after authentication,
ownership, same-origin and connection-cap checks. Per connection there is a
bounded outbound queue (128 messages of at most 8 KiB). **Slow-client policy:**
when the queue is full the newest message is dropped and counted; the client
receives `{"type":"dropped","count":N}`; a failed or timed-out write ends the
session. The Docker stream is always read regardless of client speed.

Docker logs have no durable cursor. Clients resume with `since=<last ts>`;
lines at or before that timestamp are skipped, so delivery is at-least-once
with a possible repeat or (extremely rarely) loss of lines sharing the exact
same nanosecond. Output written while no container existed does not exist.
The stream follows a bot across container replacement automatically.

Stdin is authorized separately from viewing (`CanReadLogs` / `CanWriteStdin`),
goes to the bot process (not a shell), is rate limited, and has a single writer
per bot. Containers run with `OpenStdin=true, StdinOnce=false`, so closing a
browser tab neither stops the bot nor closes its stdin.

## Data at rest

SQLite (WAL, small pool) holds six tables plus the migration table. Environment
values are AES-256-GCM encrypted with AAD `(bot_id, name, key_id)`; keys are
files under `RIVET_KEY_DIR`. Encryption protects the database and its
backups; it does **not** protect values from an operator with Docker access,
since the plaintext is in the container's environment. Logs stay in Docker's
rotated `json-file` logs, never SQLite.

## MVP additions (components)

```
internal/oauth        GitHub/Discord authorization-code + PKCE; in-memory pending state
internal/service      OAuthService, AlertService (Discord webhooks), DeployService (GitHub),
                      BackupService, Analytics (bot telemetry), RBAC in BotService.Authorize
internal/github       REST client; deploys download tarballs (no git on the host)
internal/sftpd        embedded SFTP server over the contained filesystem layer
internal/pkgmgr       package.json / requirements.txt / pyproject / Cargo.toml / go.mod editors
internal/templates    embedded starter projects
internal/filesystem   + tar.gz backup/restore and repository deploy (staged, contained)
sdk/                  discord.js / discord.py telemetry snippets served by the panel
```

Authorization is one function, `BotService.Authorize(actor, bot, permission)`;
every bot-scoped route, WebSocket, SSE stream and SFTP operation goes through
it. Background work (backups, deploys, alerts) runs on the server's context, never
on a request's, and copies everything it needs out of request memory first.
Restart decisions stay with the runner: it applies the per-bot policy when it
sees a container exit (`restartVerdict`), so Docker's own restart policy remains
disabled.
