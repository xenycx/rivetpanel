---
title: Diagnostics and footprint
lede: Check the installation's health from the browser or the shell, and know how much memory the panel itself uses.
icon: activity
audience: admin
glance:
  - Each check has a 3-second deadline
  - About 37–39 MiB idle in the latest measurement
  - No built-in memory limit
related: [operate/install, operate/host-and-logs, observe/health, operate/extensions]
---

## The Diagnostics page

**Administration → Diagnostics** (permission `system.view`) runs read-only
checks of this installation and groups them by **Panel**, **Host**, **Bots**
and **Data**. Each check is OK, a warning, a failure or information, and
problems carry the next action in plain words. **Run again** repeats the
checks; **Download report** saves them.

Checks run one at a time, each with a 3-second deadline, so a hung dependency
shows up as "A check did not finish" instead of a hung page. They never run
anything user-supplied and report only an allowlist of facts: no tokens,
environment values, configuration files or logs. [enforced: panel]

| Check | Fails or warns when |
| --- | --- |
| Database | The database does not respond, or its schema version differs from the one this build expects; otherwise shows the schema version and size on disk |
| Encryption keys | The active key is not loaded |
| Docker runner | Docker is not usable; shows the Docker version, cgroup v2 and rootless mode when it is, and is informational when `RIVET_RUNNER_MODE=none` |
| Swap limits | The host cannot limit swap, so an app may use swap beyond its memory limit |
| Reconciliation | Apps have waited more than 2 minutes for a requested change (builds count as waiting) |
| Workspace ownership | The panel cannot hand files to the container user, or runs apps as its own uid; see [Workspace ownership](02-configuration.md#workspace-ownership) |
| Workspaces owned by another user | Workspaces belong to a different user, so their apps cannot start |
| Disk space | Less than 2 GiB or 10% free where workspaces live (warning), or less than 512 MiB or 3% (failure) |
| Files (inodes) | Less than 5% of inodes free |
| Memory for builds | The host has less memory than the hungriest runtime's build needs plus 512 MiB |
| Scheduled backups | No automatic backup succeeded for more than two intervals; informational when they are off |
| Failed backups, Damaged backups | Backups failed in the last 24 hours, or failed verification |
| Panel backup | Always a reminder: app backups do not include the database or keys |
| Public address | `RIVET_PUBLIC_URL` is not https; informational when unset |
| Sign-in providers, SFTP | Informational: which providers and listeners are on |
| Unencrypted public address | The panel listens beyond loopback with a plain `http://` public address; see [Install](01-install.md#plain-http-warning) |
| Container data paths | A containerized panel could not identify its own container; see [Install](01-install.md#the-data-directory-must-be-a-host-directory) |
| App sites network | The network app sites run on has a problem |
| Former installation | Traces of the previous product were found; see [Upgrades](03-upgrades.md#coming-from-the-previous-product-040) |

### From the shell

`rivetpanel doctor` runs the same checks on the host. It is read-only: it
opens the database read-only, runs no migrations and creates no directories,
so a missing database or data root is reported, not created. Its only write is
a short-lived ownership probe file in the data root.

`rivet-agent doctor` does the same for a remote node; see
[Remote nodes](04-remote-nodes.md#troubleshooting).

## Memory footprint

The panel sets **no memory target or limit** of its own, so features such as
one-click apps are not held back by a fixed budget. It once aimed for under
50 MB idle with a soft 48 MiB Go heap limit; both are gone. If you want a
ceiling, set `GOMEMLIMIT` (a hint to Go's garbage collector) or a systemd
`MemoryMax` yourself, well above the login peak below.

The local runner runs inside the panel process, so panel and runner are one
process. Each remote node adds its own `rivet-agent` process on that host,
measured separately.

### What to expect

From the latest measurement (production build, `RIVET_ENV=production`,
default modules, local runner attached to Docker, 60 seconds after start):

| Situation | Resident memory |
| --- | --- |
| Idle, no apps | 36.6–38.6 MiB over three starts |
| After a password login, loading the UI and one running Node.js bot | 39.5 MiB |
| With one Velocity game server installed and running | 43.7 MiB |
| Transient peak during a login | 54–55 MiB |

Run-to-run spread is about 2 MiB of anonymous memory, so smaller differences
are noise. Not measured: many apps, many concurrent console or SFTP sessions,
remote nodes, or hosts with transparent huge pages set to `madvise` or
`never`.

### Where the memory goes

- **Binary pages, a fixed 22–24 MiB.** They are shared, reclaimable page cache
  but count in RSS and in a cgroup's memory. Only a smaller binary lowers
  them.
- **SQLite, about 1 MiB per pool connection**, for the life of the process:
  the parsed schema of about 250 tables, indexes and triggers. The pure-Go
  allocator keeps freed memory for reuse, so closing idle connections does
  not return it. `RIVET_DB_MAX_CONNS=2` saved about 1.8 MiB of anonymous
  memory idle on small installations, at the price of less read concurrency;
  the default stays 4.
- **The Go heap**, a few MiB live.

### The login peak: Argon2id

Password hashing (Argon2id, 19 MiB, t=2) allocates a fresh block per hash.
On the first build one login left RSS 21 MB higher, and four parallel bad
logins pushed it to 105 MB, because Go kept the garbage long after the work.
Fixes, each verified by re-measuring:

1. Hashing is limited to one operation at a time by default (`HASH_WORKERS`),
   and login is rate limited to 10 a minute per address. [enforced: panel]
2. When the last hash in flight finishes, the hasher returns memory to the
   operating system, bringing RSS back to the baseline.

The remaining transient peak is one 19 MiB hash on top of the baseline. Do not
lower the Argon2 parameters below OWASP's minimum to shave it: accept the
peak, or set `MemoryMax` well above it. SFTP password logins use the same
bounded hasher; API-key logins do not hash at all. A two-step
sign-in hashes the password once, like a plain login.

### Other bounds that keep it small

- SQLite pool of 4 with a 2 MiB page cache.
- Request bodies are streamed, so uploads are never buffered whole, and every
  non-upload route has a 1 MiB cap.
- Each console has a queue of 128 messages of at most 8 KiB, with at most 64
  console connections.
- Runner workers are bounded (default 2) and the work queue is capped.
- Telemetry is pruned in batches of 1,000.
- The terminal and editor load lazily: the first UI load is about 33 KB
  gzipped, without xterm or CodeMirror.
- The panel opts its own process out of transparent huge pages before the
  heap grows, and returns start-up garbage to the kernel once before it
  listens. It starts no child processes, so containers are unaffected.
  Setting `disablethp` in `GODEBUG` (either value) leaves the choice to you.
- Regular expressions compile on first use, all rate limiters share one store
  whose single sweeper runs only while it has entries, the periodic pruning
  jobs share one goroutine, the in-memory log ring grows with the lines it
  receives, UI files stream from the embedded image, and the jar scan of a
  game server install reuses one decompressor (a test bounds a 3,000-entry
  scan at 8 MiB of allocations).

### Extensions

With the extensions module off, no extension code is compiled and idle memory
is unchanged. The WebAssembly runtime and the three built-in bundles (about
1.3–1.5 MB each, also stored in the database once synced) grew the binary from
32.2 MB to 39.3 MB.

| Engine | One extension loaded | Two loaded | Compile time | Warm call |
| --- | --- | --- | --- | --- |
| Compiler | About 35 MB | RSS 66 MB → 138 MB | About 1.4 s | About 0.3 ms |
| Interpreter | About 115 MB | RSS 61 MB → 288 MB | About 0.2 s | 2–4 ms |

The interpreter is used automatically where executable memory is forbidden,
which includes the shipped systemd unit (`MemoryDenyWriteExecute=true`); the
container image uses the compiler. Extensions unused for ten minutes are
unloaded, so idle panels pay only for extensions with public endpoints
(ChatOps), which stay loaded to meet Discord's three-second deadline: the
first command after start took 12 ms instead of 4.3 s cold.

On a memory-constrained host, prefer the container image, install only the
extensions you use, and leave ChatOps off unless you need it.

### Email and idle memory

Mailgun email adds no queue, connection pool or background goroutine, and the
binary grew by about 86 KB. Three starts of each build (development mode)
read 34,840 kB before the email work, 35,284 kB with it unconfigured and
33,124 kB with Mailgun configured. The spread between identical starts
(anonymous memory 14.7 to 16.8 MB) is larger than the difference, so the
honest reading is no measurable change. Not measured: the peak of a real TLS
send, or bursts of alert emails.

## How to measure

1. Use a production build:
   `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'`.
2. Start it on a fresh, empty database with the runner attached to a live
   Docker daemon, telemetry running, and no browser, WebSocket or app.
3. Run the binary from a disk path, not tmpfs: on `/tmp` its pages show as
   `RssShmem` instead of `RssFile`, so the split is not comparable.
4. Wait 60 seconds, then read `VmRSS`, `RssAnon`, `RssFile`, `RssShmem` and
   `VmHWM` from `/proc/<pid>/status`. `/proc/<pid>/smaps_rollup` and
   `/proc/<pid>/smaps` break it down further.
5. Repeat over three starts and compare readings, not single values.

Docker Engine, containers, page cache and browsers are excluded by
definition. For heap attribution, use a separate build with a temporary,
build-tag-guarded `net/http/pprof` listener (never shipped) and
`GODEBUG=memprofilerate=1`.

## Measurement history

All on Linux 7.2.5 x86_64 with Go 1.27.1, RSS in MiB unless noted.

| Date | Build | Idle RSS | After a login and one bot | Peak (`VmHWM`) |
| --- | --- | --- | --- | --- |
| 2026-09-30 | First release, 17.2 MB binary, rootless Docker | 24.8 | 25.0 | 44.0 |
| 2026-09-30 | Everything on (SFTP, GitHub and Discord OAuth, telemetry API, backup scheduler, alert watcher, deploy service), 19.1 MB binary, rootful Docker 29.7.2 | 27.0 | 29.6 (also after a real `sftp` session and after deleting the bot) | 46.3 |
| 2026-09-30, evening | Adds the schedule loop (30 s), heartbeat rule loop (1 min), MFA tickets, idempotency cache and time-zone database, 20.6 MB binary | 32.7 | not measured | 32.7 idle |
| 2026-10-04 | Before the reduction work, 30.8 MB binary | 43.0–45.2 | 42.3 | 60.3 |
| 2026-10-04 | After the reduction work | 36.6–38.6 | 39.5 | 54.7 |

In the first measurement rootless `dockerd` used about 98 MB, `containerd`
about 57 MB and `rootlesskit` about 20 MB, and the idle Node.js test bot used
10.5 MiB of its 256 MiB cap; none of that counts toward the panel.

### The 2026-10-04 reduction

A fresh idle panel had grown to about 43–45 MiB, and a running Velocity server
took it to 48.8 MiB (61.9 MiB peak). The causes, idle:

| Part | Size before | Cause |
| --- | --- | --- |
| Go heap arena | 14.3 MiB resident for 7 MiB in use | Transparent huge pages set to `always`: the heap sat in 2 MiB pages that khugepaged refilled after the runtime returned them |
| SQLite | 5.5–6.5 MiB | About 0.95 MiB per pool connection; start-up jobs open all 4 at once |
| Live heap | 3.5 MiB | 1.48 MiB of regular expressions compiled at program start, 0.14 MiB a pre-allocated 2,000-line log ring |
| Goroutine stacks | 2 MiB, 53 goroutines | 19 rate-limiter cleanup loops waking every second |
| Binary pages | 22–23 MiB | Follows binary size |

A game server install also allocated about 1 GB of short-lived heap: the
check that reads a downloaded jar's Java version created a new 40 KiB
decompressor for each of thousands of entries.

The changes listed under [Other bounds](#other-bounds-that-keep-it-small)
brought goroutines from 53 to 31, the live heap after a forced collection from
3.47 MiB to 2.07 MiB, and the resident heap arena from 14.3 MiB to 5.7–6.8 MiB.
With the Velocity server running, RSS fell from 48.8 MiB to 43.7 MiB and the
peak from 61.9 MiB to 53.7 MiB. Lowering `GOGC` made no measurable difference
to idle RSS, and the SQLite pool size and Argon2id parameters were left
unchanged.
