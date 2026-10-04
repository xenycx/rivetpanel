# Memory footprint

**Target:** under 50 MB aggregate idle RSS for the panel and local runner. In
this deployment profile the local runner runs **inside** the panel process, so
that aggregate is one process. Each remote node adds its own `rivet-agent`
process on that host and is measured separately.

## Method

* Host: Linux 7.2.5, x86_64, Go 1.27.1, production build
  (`CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'`, 17,182,880 bytes,
  UI embedded).
* Fresh start on an empty database, runner attached to a live Docker daemon,
  telemetry sampler running. No browser, no WebSocket, no bot.
* 60 s warm-up, then `VmRSS`, `RssAnon`, `RssFile`, `VmHWM` from
  `/proc/<pid>/status`.
* Excluded by definition: Docker Engine, containers, page cache, browsers.

## Results (2026-09-30)

| Scenario | RSS (bytes) | anon | file-backed | peak (`VmHWM`) |
| --- | ---: | ---: | ---: | ---: |
| A. Idle, no bots | **25,346,048** (24.8 MiB) | 10.1 MiB | 14.0 MiB | 24.2 MiB |
| B. After a login and one running bot, 60 s later | **26,181,632** (25.0 MiB) | 10.8 MiB | 14.1 MiB | 44.0 MiB |

Reported separately (not part of the budget): rootless `dockerd` about 98 MB,
`containerd` about 57 MB, `rootlesskit` about 20 MB; the idle Node.js test bot
used 10.5 MiB of its 256 MiB cap.

**Conclusion:** the 50 MB idle target is met with roughly 2x headroom for this
profile. Nothing here says anything about load: many bots, many console
sessions, large uploads or a slow disk were not measured.

## After the MVP feature expansion (2026-09-30)

Same method (fresh empty database, runner attached to a live Docker daemon, 60 s
warm-up, no browser), production binary of 19,054,752 bytes, with **everything
switched on**: SFTP listener, GitHub and Discord OAuth configured, bot
telemetry API, backup scheduler, alert watcher, deploy service. Docker Engine
29.7.2 (rootful, systemd cgroup driver, cgroup v2).

| Scenario | RSS (bytes) | anon | file-backed | peak (`VmHWM`) |
| --- | ---: | ---: | ---: | ---: |
| A. Idle, no bots | **28,344,320** (27.0 MiB) | 11.2 MiB | 15.8 MiB | 27.0 MiB |
| B. After a password login, one running bot, a live stats stream, a backup, 60 s later | **31,084,544** (29.6 MiB) | 13.7 MiB | 15.9 MiB | **46.3 MiB** |
| C. After a real `sftp` session (list, upload, download, rename, delete) | 31,100,928 (29.6 MiB) | 13.8 MiB | 15.9 MiB | 46.3 MiB |
| D. After deleting the bot | 31,162,368 (29.7 MiB) | 13.8 MiB | 15.9 MiB | 46.3 MiB |

(C and D read 30,372 kB and 30,432 kB from `/proc/<pid>/status`.)

**The 50 MB idle target is still met** (27.0 MiB idle; it was 24.8 MiB before the
expansion). The transient peak of 46.3 MiB is the Argon2id login described
below and leaves only about 3.7 MiB of headroom below 50 MB: an installation
that must never exceed 50 MB at any instant should treat that as the limit and
not enable more concurrent hashing (`HASH_WORKERS`). SFTP password logins use
the same bounded hasher; API-key logins do not hash at all. Not measured: many
concurrent SFTP or console sessions, large backups (they stream through gzip
with fixed buffers, but the walk of a huge workspace was not profiled), and load.

## After the roadmap completion (2026-09-30, evening)

Same method, production binary of 20,586,656 bytes, everything switched on
(SFTP listener, GitHub and Discord OAuth configured, telemetry API, backup
scheduler, alert watcher, deploy service) plus the new always-running pieces:
the schedule loop (30 s), the heartbeat rule loop (1 min), bounded in-memory
MFA tickets and idempotency cache, and the embedded time-zone database.

| Scenario | RSS (bytes) | anon | file-backed | peak (`VmHWM`) |
| --- | ---: | ---: | ---: | ---: |
| A. Idle, no bots, 60 s after start | **34,328,576** (32.7 MiB) | 16.6 MiB | 16.1 MiB | 32.7 MiB |

(`/proc/<pid>/status`: VmRSS 33,524 kB, RssAnon 16,968 kB, RssFile 16,524 kB.)
Idle RSS grew by about 5.7 MiB over the MVP measurement (27.0 MiB), mostly
anonymous memory from the new services and the larger binary. **The 50 MB idle
target is still met** with about 15 MiB to spare. The login peak was not
re-measured in this round; the Argon2id cost is unchanged, and a two-step
sign-in hashes the password once, like a plain login.

## The one thing that nearly broke the target: Argon2id

Argon2id (19 MiB, t=2) allocates a fresh block per hash. Measured on the first
build: one login left RSS at +21 MB and four parallel bad logins pushed it to
**105 MB**, because Go retains garbage long after the work ends. Fixes, each
verified by re-measurement:

1. Hashing is bounded to one concurrent operation by default
   (`HASH_WORKERS`; login is also rate limited to 10/min/IP).
2. When the last in-flight hash finishes, the hasher calls
   `debug.FreeOSMemory()`. RSS returns to about 25 MB (was 105 MB).
3. A soft `debug.SetMemoryLimit(48 MiB)` (or `GOMEMLIMIT`) is applied when
   unset. It is a GC pacing hint, not a cap.

The remaining **transient** peak during a login is about 45 MB (one 19 MiB hash
on top of the baseline). A deployment that must never exceed 50 MB at any
instant should not drop the Argon2 parameters below OWASP's minimum; instead
accept the peak, or run the panel with a `MemoryMax` well above it.

## Other bounds that keep it small

SQLite pool of 4 with a 2 MiB page cache; request bodies streamed (uploads never
buffered whole); 1 MiB cap on every non-upload route; per-console queue of 128
messages of at most 8 KiB, at most 64 console connections; runner workers
bounded (default 2) and the work queue capped; telemetry pruned in batches of
1000; the terminal and editor are lazy-loaded (the initial UI load is about
33 KB gzipped with no xterm or CodeMirror in it).

## Email (Mailgun) does not change the idle footprint (2026-10-01)

Mailgun email adds no queue, no connection pool and no background goroutine: a
message is one HTTPS request made from a goroutine that exists only while it
runs (at most two at a time, extra messages dropped and logged), and settings
are read from SQLite per message. The binary grew by about 86 KB.

Measured the same way as above (fresh empty database, development mode, no
browser, 60 s warm-up, `VmRSS` from `/proc/<pid>/status`), three starts of the
production binary:

| Binary | VmRSS |
| --- | ---: |
| Before the email work (commit 34178e6) | 34,840 kB |
| With the email work, Mailgun not configured | 35,284 kB |
| With the email work, Mailgun configured | 33,124 kB |

The spread between identical starts (anonymous memory ranged 14.7 to 16.8 MB)
is larger than the difference between the builds, so the honest reading is "no
measurable change". These absolute figures are higher than the tables above; the earlier tables
used a production-profile start, and this check used a development-mode start
with the runner at its default, so compare the three rows with each other, not
with the earlier tables. Not measured: the
transient peak of a real TLS send to Mailgun, or bursts of alert emails.

## Idle memory had nearly doubled; found and reduced (2026-10-04)

A fresh idle panel (empty database, default modules, no bots) had grown to
about 43–45 MiB RSS: roughly 21 MiB anonymous memory and 23 MiB of binary
pages, against 10–11 MiB and 14–16 MiB in the first measurements. Headroom
under the 50 MB target was nearly gone, and a running Velocity server took the
panel to 48.8 MiB.

### Method

* Production build (`CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X
  main.version=0.4.0'`), Go 1.27.1, Linux 7.2.5 x86_64, transparent huge pages
  `always` (khugepaged `max_ptes_none` 511), Docker 29.7.2 rootful. Before and
  after were built from the same snapshot of the embedded UI (binaries of
  30,781,600 and 30,793,888 bytes) and run side by side.
* `RIVET_ENV=production` with a scratch database, key, data, backup and sites
  directory, listener on 127.0.0.1, local runner attached to Docker, no
  browser for scenario A. Default modules (game servers on as a preview; no
  SFTP, OAuth or agents).
* `VmRSS`, `RssAnon`, `RssFile`, `RssShmem` and `VmHWM` from
  `/proc/<pid>/status`, plus `/proc/<pid>/smaps_rollup` and a per-mapping
  breakdown from `/proc/<pid>/smaps` (Go heap arena, other runtime mappings,
  unlabeled mappings, binary), at 10, 30 and 60 s.
* **Run the measured binary from a disk path, not from tmpfs.** A binary on
  `/tmp` (tmpfs) shows its pages as `RssShmem` instead of `RssFile`, so the
  split is not comparable with the tables above. These runs used a directory
  under `~/.cache`.
* Profiles came from a separate build with a temporary, build-tag-guarded
  `net/http/pprof` listener (not shipped), with `GODEBUG=memprofilerate=1` for
  exact live-heap attribution.

### What the memory was

| Part (idle, before) | Size | Cause |
| --- | ---: | --- |
| Go heap arena resident | 14.3 MiB (all `AnonHugePages`) for 7 MiB in use | THP `always`: the heap sat in 2 MiB pages and khugepaged refilled what the runtime returned to the kernel |
| SQLite (unlabeled anonymous mappings) | 5.5–6.5 MiB | about 0.95 MiB per pool connection (the parsed schema of ~250 tables, indexes and triggers); startup jobs open all 4 at once. The pure-Go allocator keeps freed memory for reuse, so closing idle connections does not give it back (tested and not kept) |
| Live heap | 3.5 MiB | 1.48 MiB of it regular expressions compiled at program start (bounded repeats like `{0,213}` expand into thousands of instructions), 0.14 MiB a pre-allocated 2,000-line log ring |
| Goroutine stacks | 2 MiB, 53 goroutines | 19 of them were Fiber rate-limiter cleanup loops, each waking every second |
| Binary pages (file-backed) | 22–23 MiB | text 12.8 MB and read-only data mapped almost entirely; this follows binary size (the kernel maps large page-cache folios) |

A game server install also allocated about **1 GB** of short-lived heap: the
check that reads a downloaded jar's Java version created a new 40 KiB
decompressor for each of thousands of entries.

### What changed

1. The panel opts its own process out of transparent huge pages
   (`prctl(PR_SET_THP_DISABLE)`) before the heap grows, and returns startup
   garbage to the kernel once before it starts listening. The process starts
   no children, so containers are unaffected. Setting `disablethp` in
   `GODEBUG` (either value) leaves the decision to the operator.
2. Package-level regular expressions compile on first use (`internal/lazyre`);
   a test compiles all of them so a bad pattern still fails the build's tests.
3. All rate limiters share one store; its single sweeper goroutine runs only
   while entries exist (no goroutine and no wake-ups in an idle panel).
4. Four hourly/15-minute pruning loops (activity record, notifications,
   sessions, telemetry) run from one goroutine, one task at a time.
5. The in-memory log ring grows with the lines it receives.
6. UI files are streamed from the embedded image instead of copied whole into
   the heap for every request.
7. The jar scan reuses one decompressor; a test bounds a 3,000-entry scan at
   8 MiB of allocations in total.

Not changed: `GOGC` (a lower value made no measurable difference to idle RSS),
the 48 MiB soft memory limit, the SQLite pool size and the Argon2id
parameters.

### Results

KiB as reported by `/proc/<pid>/status`; MiB in parentheses.

**A. Idle, no bots, three fresh starts each, 60 s after start**

| Build | RSS | anon | file-backed | shmem |
| --- | ---: | ---: | ---: | ---: |
| Before, start 1 | 44,068 (43.0) | 21,072 (20.6) | 22,996 (22.5) | 0 |
| Before, start 2 | 46,332 (45.2) | 23,404 (22.9) | 22,928 (22.4) | 0 |
| Before, start 3 | 44,152 (43.1) | 21,352 (20.9) | 22,800 (22.3) | 0 |
| **After, start 1** | **37,500 (36.6)** | 14,788 (14.4) | 22,712 (22.2) | 0 |
| **After, start 2** | **37,640 (36.8)** | 14,868 (14.5) | 22,772 (22.2) | 0 |
| **After, start 3** | **39,528 (38.6)** | 16,756 (16.4) | 22,772 (22.2) | 0 |

The 10 s and 30 s readings were within 0.7 MiB of the 60 s ones. Goroutines
(profiling build): 53 before, 31 after. Live heap after a forced collection:
3.47 MiB before, 2.07 MiB after; resident Go heap arena 14.3 MiB before,
5.7–6.8 MiB after.

**B. After a password login, a browser-like load of the UI shell and its
assets, and one running Node.js bot, 60 s later**

| Build | RSS | anon | file-backed | peak (`VmHWM`) |
| --- | ---: | ---: | ---: | ---: |
| Before | 43,284 (42.3) | 19,460 (19.0) | 23,824 (23.3) | 61,740 (60.3) |
| **After** | **40,476 (39.5)** | 16,616 (16.2) | 23,860 (23.3) | 55,968 (54.7) |

**C. Game servers module (default preview) with one Velocity server
installed and running, 60 s later** (no Minecraft EULA involved)

| Build | RSS | anon | file-backed | peak (`VmHWM`) |
| --- | ---: | ---: | ---: | ---: |
| Before | 49,996 (48.8) | 25,420 (24.8) | 24,576 (24.0) | 63,352 (61.9) |
| **After** | **44,764 (43.7)** | 20,088 (19.6) | 24,676 (24.1) | 55,012 (53.7) |

All test containers were deleted through the panel afterwards.

### What is left, and the honest caveats

* **The transient peak is above 50 MB.** `VmHWM` in B and C is the Argon2id
  login (19 MiB per hash) on top of a baseline whose binary pages alone are
  now 22–24 MiB; it fell by 5–8 MiB but is still 54–55 MiB. The steady state
  is under the target; a deployment that must never exceed 50 MB at any
  instant needs more room than this, as noted in the Argon2id section.
* **Binary pages are a fixed 22–24 MiB.** They are shared, reclaimable page
  cache, but they count in RSS (and in a cgroup's memory). Only a smaller
  binary lowers them; no required dependency was removed.
* **SQLite costs about 1 MiB per pool connection** for the life of the
  process. `RIVET_DB_MAX_CONNS=2` measured about 1.8 MiB less anonymous memory
  idle (12,984 KiB anon) on small installations, at the price of less read
  concurrency; the default stays 4.
* Run-to-run spread is about 2 MiB of anonymous memory (compare the three
  starts above), so differences smaller than that are noise.
* Not measured: many bots, many concurrent console or SFTP sessions, remote
  nodes, or a host with transparent huge pages set to `madvise`/`never` (there
  the THP change does nothing and the before/after gap is smaller).
