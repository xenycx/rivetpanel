# Remote nodes (preview)

RivetPanel can run bots and Minecraft servers on another Linux host through
`rivet-agent`. The agent makes one outbound, mutually authenticated TLS
connection to the control plane; the node needs no inbound management port.
Lifecycle actions, console I/O, resource samples, Minecraft status queries,
backups, GitHub deployments, GitHub publish/push, the package manager, the AI
assistant's file tools and isolated diagnostics, the Files page, SFTP,
add-ons and node telemetry are routed over that connection. Panel and agent
protocol versions must match exactly. This build speaks protocol **8**
(the node-side port probe for game-server allocations and starts; 7 added
remote AI diagnostics, node telemetry and the free-disk check, non-recursive
deletes for SFTP and add-on data removal; 6 added remote add-on states and
logs, 5 remote AI file changes, 4 remote GitHub publish/push, 3 remote GitHub
deployment); an agent of an older build is refused when it connects, so
upgrade the panel and every `rivet-agent` together. A protocol 6 agent would
treat SFTP's non-recursive delete as a recursive one, which is one more
reason the versions must match. A protocol 7 agent is refused at hello with
"protocol 7 is not supported (this panel speaks 8); update rivet-agent" and
keeps retrying until it is upgraded.

This is a preview. It is suitable for operator-controlled nodes, not hostile
node operators or a claim that the control plane is unprivileged. The panel
still holds its local Docker socket when the local runner is enabled, and each
agent holds the Docker socket on its node. A Docker socket is root-equivalent.

## Enable and expose the agent listener

On the control plane, set:

```ini
RIVET_MODULES=agents=preview,game_servers=preview
RIVET_AGENT_LISTEN=:8444
RIVET_AGENT_ADDRESS=panel.example.com:8444
RIVET_AGENT_HOSTS=panel.example.com
```

Restart RivetPanel and allow TCP port `8444` from the node. This is raw TLS
with a private agent protocol, not HTTP or WebSocket; do not put it behind an
HTTP reverse proxy. `RIVET_AGENT_ADDRESS` is what agents dial.
`RIVET_AGENT_HOSTS` lists names/IPs in the listener certificate. If the address
is omitted, RivetPanel derives the public URL host and listener port.

The panel creates an Ed25519 certificate authority under
`RIVET_KEY_DIR/agent-ca/`. Its private key can mint node identities and is as
sensitive as the panel's encryption keys. Never copy it to a node.
`rivetpanel backup --include-keys` includes it with manifest checksums;
`restore --restore-keys` restores it. Startup and backup refuse a partial CA.

## Install and enroll a node

Build or unpack the release on the remote host, then install the service:

```sh
sudo deploy/install-agent.sh
```

In the panel, open **Administration → Nodes → Add remote node**, choose its
location and copy the one-time command. Run that command on the remote host,
then start the service:

```sh
sudo rivet-agent enroll --panel https://panel.example.com --token rvt_enroll_...
sudo systemctl enable --now rivet-agent
sudo systemctl status rivet-agent
```

Enrollment creates `/var/lib/rivet-agent/node.pem` with the node's private key,
client certificate, CA certificate and connection address. Protect and back up
that file as a root credential. The token is shown once, stored by the panel
only as a SHA-256 hash, bound to its pre-created node and valid for 15 minutes
by default (one minute to 24 hours). A successful exchange consumes it in the
same SQLite transaction that records the issued certificate.

Shell-only recovery is also available on the control-plane host:

```sh
rivetpanel agent-token create --ttl 15m edge-one
rivetpanel agent-token list
rivetpanel agent-token revoke TOKEN_ID
rivetpanel agent-token reissue --ttl 15m NODE_ID
rivetpanel agent-token discard NODE_ID
```

`discard` is only for a node that never enrolled. To re-key an enrolled node,
revoke its certificate in Administration → Nodes, remove its old `node.pem`,
then use **Create enrollment** on that same node. Certificate revocation is
checked by the panel during every TLS handshake and disconnects the current
session immediately.

Certificates last 30 days. When an agent connects with less than a third of
its certificate's lifetime left, it renews the certificate over the
authenticated connection, replaces `node.pem` atomically and reconnects. The first successful handshake
with the renewed certificate revokes every older certificate of that node
(reason `superseded`), so a replaced `node.pem` cannot be reused. The old
certificate is not revoked when the renewal is issued: an agent that stops
before saving its renewal can still reconnect with the old one and renew
again.

## Placement and operation

Administrators choose a node while creating an empty bot, a bot from a
template or from GitHub (both need the node to be connected) or a game server;
other users' servers are placed on the panel's own node. A remote server
keeps its files under `RIVET_AGENT_DATA` (default
`/var/lib/rivet-agent/servers`) and its containers in that node's Docker.

The Nodes page shows connection state, agent version, hostname, last contact,
reported capabilities, certificate expiry and workload count, plus CPU,
memory, disk and running-server metrics per node for the last hour. Disabling a
node or revoking its certificate disconnects it. Draining a node keeps its
existing servers running but refuses new placement. It does not migrate
servers. A node can only be deleted after every server has been removed or
moved by an operator.

If a node goes offline, desired-state changes remain in SQLite. The agent
reconnects with backoff, resynchronizes all assigned servers and resumes
reconciliation. Server containers can keep running while the connection is
down. The panel marks live views unavailable until contact returns: the
Files page and package manager say the node is offline, an open console
says so once and resumes from the last line after the agent reconnects, and
administrators see "(node offline)" next to the node name on the server's
page (status and stats there are the last ones the node reported).

## Current enforcement and limits

Enforced now:

- the panel issues node-bound, client-auth-only 30-day certificates; a
  renewal's first successful handshake revokes the node's older certificates;
- TLS 1.3 requires a certificate chained to the private CA, whose serial is
  present, unexpired, unrevoked, assigned to that node and enabled in SQLite;
- every agent-to-panel server lookup is checked against the authenticated node,
  preventing cross-node server access;
- container memory, CPU and PID limits, non-root user, dropped capabilities,
  read-only runtime and `no-new-privileges` are enforced by Docker on the node;
- file paths and archive extraction are contained by the agent filesystem
  layer; downloaded game artifacts use their published checksums where the
  provider supplies them;
- manual, scheduled and pre-restore backups stream over the authenticated
  connection into the panel's backup directory. A restore is staged and
  journaled on the node; the old workspace is released only after the panel
  records its database update, and an unconfirmed transaction rolls back on
  timeout or before the agent runner starts again after a restart;
- GitHub deployments to a remote bot (manual, webhook and polled) download
  the repository tarball at the panel, capped at 1 GiB, and stream it to the
  assigned agent; it is never unpacked on the panel host. The agent unpacks
  and validates the whole archive into a private staging directory first.
  The panel then re-checks that the bot still exists and that its repository
  link and node did not change, and only then lets the agent swap the files.
  The previous files stay journaled until the panel has recorded the commit:
  a database failure rolls them back, a confirmation that never arrives rolls
  back after 5 minutes, and an agent crash is rolled back by the journal at
  agent start. A manual deployment to an offline node is refused; polling
  waits for the agent to reconnect, and a webhook push received during the
  outage is remembered (the newest one per server, stored in the database so
  it survives a panel restart) and deployed once — the branch head at that
  time — when the agent reconnects. A push whose repository link (name,
  branch, folder) or auto-deploy setting changed meanwhile is dropped and
  shown as a cancelled deployment;
- GitHub publish and push for a remote bot ask the assigned agent to select the
  files (the same `.gitignore`, built-in and `.env` exclusions and the same
  5,000-file / 200 MiB / 25 MiB-per-file limits as local publishing, which the
  agent refuses to exceed), then read only the selected files one at a time
  through the agent's contained file API. The panel builds the commit through
  the GitHub API and records it as deployed; nothing is written to the panel's
  disk. Offline nodes are refused before anything is created;
- the package manager for a remote bot lists the node's workspace root to pick
  the manifest, reads it through the agent's contained file API (1 MiB cap at
  the panel) and writes it back with `If-Match` on the revision it read (or
  create-only when the manifest is new). A concurrent change on the node makes
  the write fail with a conflict and nothing is overwritten; the
  deployment/restore lock still blocks edits;
- the AI assistant's list, read and search tools for a remote bot use the same
  contained file API (protected paths are refused at the panel before any
  request, files over 1 MiB are skipped, a search reads at most 2,000 files,
  and secret values are redacted at the panel before anything reaches the
  model). A proposed change or Undo is sent as a revision-checked patch
  (`POST /node/v1/bots/:id/patch`, protocol 5); the agent applies it through
  the same journal and transaction registry as restores and deployments, and
  keeps the previous file until the panel has recorded the change set. A
  stale revision is refused without changing anything; an unconfirmed commit
  is retried once and then rolled back (the node also rolls it back after 5
  minutes or at agent start after a crash). Nothing is read from or written to
  the panel's disk for a remote bot, and offline nodes are refused;
- creating a template bot on a remote node (administrators only) is refused
  before anything is recorded when the node is offline or the template does
  not fit one patch (32 files, 1 MiB per file, 8 MiB in total; every built-in
  template fits). The panel records the bot, then asks the agent to create
  its directory (`POST /node/v1/workspaces/:id`) and sends every template
  file as one create-only patch through the same journal and transaction
  registry; the panel commits it as the last creation step. If the patch is
  refused or its commit cannot be confirmed (retried once, then rolled back),
  the bot is marked deleted and handed to the agent's normal purge, which
  removes the directory and the row (an agent that cannot be reached does
  this when it reconnects). Nothing is written to the panel's disk;
- Modrinth plugin/mod installation for a remote game server checks the user's
  file permission and refuses offline nodes before downloading. The panel
  downloads the file into a private temporary file (the system temporary
  directory, removed as soon as the install ends) and verifies the CDN host
  allowlist, the 256 MiB limit, the declared size and the published SHA-512
  over the complete file before any byte is sent. Only then is the verified
  file streamed to the node with its exact length through the existing file
  API, where the agent writes it to a temporary file and renames it into
  place; the size the node reports back must match. Streaming the download
  straight through was rejected because the node could finish writing a file
  of the expected length before the checksum is known. The node trusts the
  panel's verification; it does not recompute the SHA-512 itself;
- SFTP for a remote server keeps sign-in, the files permission (re-checked at
  least every 5 seconds, also for open handles), the per-file size cap and the
  deployment/restore lock at the panel; the agent never sees SFTP
  credentials. Every operation uses the existing agent file routes (list,
  `files/content` read and write, `mkdir`, `move`, delete), so path
  containment is the node's filesystem layer. A download is copied into a
  private, already-deleted temporary file on the panel before it is served;
  an upload is collected the same way and sent in one atomic write when the
  client closes the file (a write that keeps part of an existing file is sent
  with `If-Match` on the revision it started from, so a concurrent change is
  refused). `remove` and `rmdir` use the agent's non-recursive delete
  (`DELETE …/files?recursive=false`, protocol 7): the node's own `rmdir`
  decides whether a directory is empty, so a file another session adds at
  the same moment is never deleted with it. Any server assigned to a
  node other than the panel's own is never looked up on the panel's disk:
  it is refused while its node is offline and when the agents module is off.
  Listings carry no modification times or permission bits;
- add-on states and logs (`GET /node/v1/bots/:id/addons` and
  `GET /node/v1/bots/:id/addons/:kind/logs?lines=N`, protocol 6) come from
  the node's Docker through its runner. The panel checks the user's
  permission and that the add-on is attached; the agent validates the server
  id, the add-on kind and the 1–500 line bound and returns at most 1 MiB.
  The panel never asks its own runner about a remote server. A node without
  a runner yet answers 503;
- add-ons chosen while creating a server on a remote node are checked for
  that node before anything is saved. All built-in kinds (PostgreSQL, Redis,
  MongoDB, MariaDB) run on agent nodes — on the node's Docker, in its private
  add-on network, with data in an `addons` directory next to the agent's
  server directory — and the panel does not need a Docker runner of its own
  for them. Removing an add-on deletes its data on the node, and attaching
  one again first clears leftovers there
  (`DELETE /node/v1/bots/:id/addons/:kind/data`, protocol 7); both are
  refused while the node is offline;
- isolated AI diagnostics (`POST /node/v1/bots/:id/diagnostics`, protocol 7)
  run on the server's node. The panel keeps the permission check, approvals,
  the per-run limit and secret redaction; the agent checks the command
  against its own runtime allowlist, copies a safe snapshot of the server's
  files (no secrets, symlinks, `.git` or large/binary files) into
  `ai-scratch` next to its server directory and runs the same offline
  container as the panel (no network, 768 MiB memory, 1 CPU, 256 PIDs,
  128 MiB tmpfs, the agent's container user, 10-minute timeout, 1 MiB of
  output). A node runs at most two diagnostics at once. Snapshots are
  removed after each run and at agent start; orphan containers are removed
  by the agent's runner after 30 minutes;
- node telemetry (`GET /node/v1/telemetry`, protocol 7): CPU, memory, load and
  the disk of the volume holding the agent's server directory. The panel
  stores a sample for each connected node every telemetry interval (Nodes
  page charts), shows the node's free disk in a remote server's live stats,
  and checks free space before it stages anything on the node: restores
  (archive size), GitHub deployments (size unknown, so only the margin),
  AI changes and templates (total size) and single files of 1 MiB or more
  (Modrinth downloads, SFTP and large uploads) are refused when the payload
  plus a 256 MiB margin would not fit. This is a preflight, not a
  reservation: archives can expand and other writers can fill the disk, in
  which case the node's write fails and the change is rolled back. Telemetry
  is reported by the node and is never used for authorization.

- game-server port probing on the node (`POST /node/v1/ports/probe`,
  protocol 8): the agent binds and immediately releases a TCP and a UDP
  socket on the allocation address and port (at most 64 ports per request)
  and says why a port is not free ("already in use on the node", "the
  address is not assigned on the node", ...). The panel asks before it
  creates automatic allocations for a remote game server and skips busy
  ports; a node that cannot answer refuses the allocation instead of
  guessing. Starting a stopped or failed remote game server probes its
  allocations first and refuses the start, naming the port, when one is
  busy; if the node is offline the start is recorded as before and the node
  reports the outcome. **A probe is a check, not a reservation:** another
  process can bind the port right after it, and Docker publishing a port
  only through firewall rules (userland proxy disabled) is not visible to a
  bind test, so the container start on the node can still fail. Free
  allocations an administrator created in the pool are handed out without a
  probe (as on the panel's own node) and are checked at start.

Not implemented or still local-only:

- workload migration, automatic scheduling/capacity placement, HA, remote
  upgrades and certificate re-enrollment in place;
- disk quotas, IO throttling, bandwidth enforcement, egress allowlists,
  malware scanning, user namespaces and stronger sandbox runtimes.

Reported capabilities are evidence sent by the agent and displayed to an
administrator; the control plane does not independently attest the node or
turn a stored report into kernel/network enforcement.

## Troubleshooting

On the node:

```sh
rivet-agent doctor
journalctl -u rivet-agent -f
```

Check that Docker is reachable, the state and data directories are writable,
the panel's HTTPS enrollment URL is reachable, and the raw agent address/port
accepts outbound TCP. A revoked, expired, wrong-node or disabled-node
certificate is rejected at the TLS handshake. Repeated reconnect logs back off
instead of spinning while the panel is unavailable.
