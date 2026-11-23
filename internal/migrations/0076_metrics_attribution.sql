-- Metrics attribution (1.20.0): what each app used every 15 seconds, which
-- processes were busy, and when the host's CPU spiked. All three tables are
-- pruned in small batches by the metrics collector: container samples after
-- 48 hours, process snapshots after 7 days and spikes after 30 days. Rows of
-- deleted apps are not cascaded; they age out with the rest.

CREATE TABLE container_samples (
    bot_id          TEXT NOT NULL,
    node_id         TEXT NOT NULL,
    sampled_at_ms   INTEGER NOT NULL,
    cpu_cores       REAL NOT NULL DEFAULT 0 CHECK (cpu_cores >= 0),
    memory_bytes    INTEGER NOT NULL DEFAULT 0 CHECK (memory_bytes >= 0),
    net_rx_bps      REAL NOT NULL DEFAULT 0 CHECK (net_rx_bps >= 0),
    net_tx_bps      REAL NOT NULL DEFAULT 0 CHECK (net_tx_bps >= 0),
    blk_read_bps    REAL NOT NULL DEFAULT 0 CHECK (blk_read_bps >= 0),
    blk_write_bps   REAL NOT NULL DEFAULT 0 CHECK (blk_write_bps >= 0),
    pids            INTEGER NOT NULL DEFAULT 0 CHECK (pids >= 0)
) STRICT;

CREATE INDEX container_samples_node_idx ON container_samples(node_id, sampled_at_ms);
CREATE INDEX container_samples_bot_idx ON container_samples(bot_id, sampled_at_ms);

CREATE TABLE process_snapshots (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id         TEXT NOT NULL,
    sampled_at_ms   INTEGER NOT NULL,
    reason          TEXT NOT NULL CHECK (reason IN ('periodic', 'spike', 'manual')),
    source          TEXT NOT NULL DEFAULT 'proc' CHECK (source IN ('proc', 'docker-top', 'agent')),
    host_cpu_pct    REAL NOT NULL DEFAULT 0,
    processes_json  TEXT NOT NULL DEFAULT '[]'
                    CHECK (json_valid(processes_json) AND length(processes_json) <= 65536)
) STRICT;

CREATE INDEX process_snapshots_node_idx ON process_snapshots(node_id, sampled_at_ms);

CREATE TABLE load_spikes (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id             TEXT NOT NULL,
    started_at_ms       INTEGER NOT NULL,
    ended_at_ms         INTEGER,
    peak_cpu_pct        REAL NOT NULL DEFAULT 0,
    avg_cpu_pct         REAL NOT NULL DEFAULT 0,
    top_bots_json       TEXT NOT NULL DEFAULT '[]'
                        CHECK (json_valid(top_bots_json) AND length(top_bots_json) <= 16384),
    top_processes_json  TEXT NOT NULL DEFAULT '[]'
                        CHECK (json_valid(top_processes_json) AND length(top_processes_json) <= 65536)
) STRICT;

CREATE INDEX load_spikes_node_idx ON load_spikes(node_id, started_at_ms);
