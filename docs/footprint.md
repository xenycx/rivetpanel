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
