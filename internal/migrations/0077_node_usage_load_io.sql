-- Host rollups gain load average, disk I/O and swap (1.21.0), so host
-- charts over ranges longer than the raw samples are kept no longer go blank
-- for those metrics. Additive: existing rows get 0, and the hours whose raw
-- samples still exist are filled in below; older rows stay 0 and the
-- charts show them as gaps (all three zero means "not recorded").
ALTER TABLE node_usage ADD COLUMN load_sum REAL NOT NULL DEFAULT 0 CHECK (load_sum >= 0);
ALTER TABLE node_usage ADD COLUMN disk_read_sum INTEGER NOT NULL DEFAULT 0 CHECK (disk_read_sum >= 0);
ALTER TABLE node_usage ADD COLUMN disk_write_sum INTEGER NOT NULL DEFAULT 0 CHECK (disk_write_sum >= 0);
ALTER TABLE node_usage ADD COLUMN swap_max INTEGER NOT NULL DEFAULT 0 CHECK (swap_max >= 0);

UPDATE node_usage SET load_sum = t.load_sum, disk_read_sum = t.disk_read_sum,
    disk_write_sum = t.disk_write_sum, swap_max = t.swap_max
FROM (SELECT node_id, (sampled_at_ms / 3600000) * 3600000 AS bucket_ms,
        sum(load1) AS load_sum, sum(disk_read_bps) AS disk_read_sum,
        sum(disk_write_bps) AS disk_write_sum, max(swap_used_bytes) AS swap_max
      FROM node_telemetry GROUP BY node_id, sampled_at_ms / 3600000) AS t
WHERE node_usage.res = 3600 AND node_usage.node_id = t.node_id AND node_usage.bucket_ms = t.bucket_ms;

UPDATE node_usage SET load_sum = h.load_sum, disk_read_sum = h.disk_read_sum,
    disk_write_sum = h.disk_write_sum, swap_max = h.swap_max
FROM (SELECT node_id, (bucket_ms / 86400000) * 86400000 AS bucket_ms, sum(load_sum) AS load_sum,
        sum(disk_read_sum) AS disk_read_sum, sum(disk_write_sum) AS disk_write_sum, max(swap_max) AS swap_max
      FROM node_usage WHERE res = 3600 GROUP BY node_id, bucket_ms / 86400000) AS h
WHERE node_usage.res = 86400 AND node_usage.node_id = h.node_id AND node_usage.bucket_ms = h.bucket_ms;
