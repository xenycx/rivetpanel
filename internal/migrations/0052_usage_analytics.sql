-- Usage analytics rollups. Purely additive.
--
-- bot_usage: per bot and time bucket. res is the bucket length in seconds
-- (300 = 5 minutes written by the collector, 3600 = hourly and 86400 = daily
-- rolled up by the application). Values are sums and counts so a coarser
-- bucket is the exact sum of finer ones:
--   samples   collector passes that saw the bot
--   wanted    passes where it was wanted running
--   up        passes where it was wanted running and was running
--   measured  passes with a resource reading (cpu_sum/mem_sum divide by it)
--   net_rx/net_tx  bytes moved during the bucket (counter differences)
--   disk_max  largest workspace size seen (NULL = not measured)
--   crashes   increases of the bot's consecutive-crash counter
--   starts    times the bot was seen to have started again
-- Deployment and backup columns are filled from the operations table into
-- hourly buckets (and summed into daily ones); 5-minute rows keep them 0.
-- Retention (application): 5-minute rows 3 days, hourly 35 days, daily 400.
CREATE TABLE bot_usage (
    bot_id          TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    res             INTEGER NOT NULL CHECK (res IN (300, 3600, 86400)),
    bucket_ms       INTEGER NOT NULL,
    samples         INTEGER NOT NULL DEFAULT 0 CHECK (samples >= 0),
    wanted          INTEGER NOT NULL DEFAULT 0 CHECK (wanted >= 0),
    up              INTEGER NOT NULL DEFAULT 0 CHECK (up >= 0),
    measured        INTEGER NOT NULL DEFAULT 0 CHECK (measured >= 0),
    cpu_sum         REAL NOT NULL DEFAULT 0 CHECK (cpu_sum >= 0),
    cpu_max         REAL NOT NULL DEFAULT 0 CHECK (cpu_max >= 0),
    mem_sum         INTEGER NOT NULL DEFAULT 0 CHECK (mem_sum >= 0),
    mem_max         INTEGER NOT NULL DEFAULT 0 CHECK (mem_max >= 0),
    mem_limit       INTEGER NOT NULL DEFAULT 0 CHECK (mem_limit >= 0),
    net_rx          INTEGER NOT NULL DEFAULT 0 CHECK (net_rx >= 0),
    net_tx          INTEGER NOT NULL DEFAULT 0 CHECK (net_tx >= 0),
    disk_max        INTEGER CHECK (disk_max IS NULL OR disk_max >= 0),
    crashes         INTEGER NOT NULL DEFAULT 0 CHECK (crashes >= 0),
    starts          INTEGER NOT NULL DEFAULT 0 CHECK (starts >= 0),
    deploys_ok      INTEGER NOT NULL DEFAULT 0 CHECK (deploys_ok >= 0),
    deploys_failed  INTEGER NOT NULL DEFAULT 0 CHECK (deploys_failed >= 0),
    deploy_ms       INTEGER NOT NULL DEFAULT 0 CHECK (deploy_ms >= 0),
    backups_ok      INTEGER NOT NULL DEFAULT 0 CHECK (backups_ok >= 0),
    backups_failed  INTEGER NOT NULL DEFAULT 0 CHECK (backups_failed >= 0),
    backup_bytes    INTEGER NOT NULL DEFAULT 0 CHECK (backup_bytes >= 0),
    PRIMARY KEY (bot_id, res, bucket_ms)
) STRICT, WITHOUT ROWID;
CREATE INDEX bot_usage_res_idx ON bot_usage(res, bucket_ms);

-- node_usage: hourly and daily rollups of node_telemetry samples, so node
-- trends outlive the raw samples (RIVET_TELEMETRY_RETENTION). Sums divide by
-- samples. Retention (application): hourly 35 days, daily 400 days.
CREATE TABLE node_usage (
    node_id         TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    res             INTEGER NOT NULL CHECK (res IN (3600, 86400)),
    bucket_ms       INTEGER NOT NULL,
    samples         INTEGER NOT NULL CHECK (samples >= 0),
    cpu_sum         REAL NOT NULL DEFAULT 0 CHECK (cpu_sum >= 0),
    cpu_max         REAL NOT NULL DEFAULT 0 CHECK (cpu_max >= 0),
    mem_sum         INTEGER NOT NULL DEFAULT 0 CHECK (mem_sum >= 0),
    mem_max         INTEGER NOT NULL DEFAULT 0 CHECK (mem_max >= 0),
    mem_total       INTEGER NOT NULL DEFAULT 0 CHECK (mem_total >= 0),
    disk_used       INTEGER NOT NULL DEFAULT 0 CHECK (disk_used >= 0),
    disk_total      INTEGER NOT NULL DEFAULT 0 CHECK (disk_total >= 0),
    net_rx_sum      INTEGER NOT NULL DEFAULT 0 CHECK (net_rx_sum >= 0),
    net_tx_sum      INTEGER NOT NULL DEFAULT 0 CHECK (net_tx_sum >= 0),
    running_max     INTEGER NOT NULL DEFAULT 0 CHECK (running_max >= 0),
    PRIMARY KEY (node_id, res, bucket_ms)
) STRICT, WITHOUT ROWID;
CREATE INDEX node_usage_res_idx ON node_usage(res, bucket_ms);

-- usage_marks: where the next rollup pass starts (milliseconds), by name.
CREATE TABLE usage_marks (
    name   TEXT PRIMARY KEY NOT NULL CHECK (length(name) BETWEEN 1 AND 32),
    value  INTEGER NOT NULL
) STRICT;
