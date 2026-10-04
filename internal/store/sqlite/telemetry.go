package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const telemetryCols = `node_id, sampled_at_ms, cpu_percent, logical_cpus, memory_used_bytes, memory_total_bytes,
	disk_used_bytes, disk_total_bytes, running_bots, load1, swap_used_bytes, swap_total_bytes, net_rx_bps, net_tx_bps,
	disk_read_bps, disk_write_bps`

func scanTelemetry(rows interface{ Scan(...any) error }) (domain.Telemetry, error) {
	var s domain.Telemetry
	err := rows.Scan(&s.NodeID, &s.SampledAtMS, &s.CPUPercent, &s.LogicalCPUs, &s.MemoryUsedBytes, &s.MemoryTotalBytes,
		&s.DiskUsedBytes, &s.DiskTotalBytes, &s.RunningBots, &s.Load1, &s.SwapUsedBytes, &s.SwapTotalBytes,
		&s.NetRxBps, &s.NetTxBps, &s.DiskReadBps, &s.DiskWriteBps)
	return s, err
}

// InsertTelemetry stores one node sample. The table's CHECK constraints reject
// out-of-range values, so the sampler must clamp before inserting.
func (db *DB) InsertTelemetry(ctx context.Context, s domain.Telemetry) error {
	_, err := db.ExecContext(ctx, `INSERT INTO node_telemetry (`+telemetryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_id, sampled_at_ms) DO NOTHING`,
		s.NodeID, s.SampledAtMS, s.CPUPercent, s.LogicalCPUs, s.MemoryUsedBytes, s.MemoryTotalBytes,
		s.DiskUsedBytes, s.DiskTotalBytes, s.RunningBots, max(s.Load1, 0), max(s.SwapUsedBytes, 0), max(s.SwapTotalBytes, 0),
		max(s.NetRxBps, 0), max(s.NetTxBps, 0), max(s.DiskReadBps, 0), max(s.DiskWriteBps, 0))
	return mapErr(err)
}

// ListTelemetry returns samples with sampled_at_ms > sinceMS in ascending order.
func (db *DB) ListTelemetry(ctx context.Context, nodeID string, sinceMS int64, limit int) ([]domain.Telemetry, error) {
	// Newest `limit` rows after sinceMS, returned oldest-first.
	rows, err := db.QueryContext(ctx, `SELECT `+telemetryCols+`
		FROM (SELECT * FROM node_telemetry WHERE node_id = ? AND sampled_at_ms > ? ORDER BY sampled_at_ms DESC LIMIT ?)
		ORDER BY sampled_at_ms ASC`, nodeID, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Telemetry
	for rows.Next() {
		s, err := scanTelemetry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListTelemetryBuckets averages samples after sinceMS into buckets of bucketMS
// milliseconds, oldest first. It bounds the work and the response for long
// ranges: a week at 30 seconds is 20,160 rows, a chart needs a few hundred.
func (db *DB) ListTelemetryBuckets(ctx context.Context, nodeID string, sinceMS, bucketMS int64) ([]domain.TelemetryBucket, error) {
	if bucketMS < 1 {
		bucketMS = 1
	}
	rows, err := db.QueryContext(ctx, `SELECT (sampled_at_ms / ?) * ? + ? / 2 AS t,
			avg(cpu_percent), max(logical_cpus), CAST(avg(memory_used_bytes) AS INTEGER), max(memory_total_bytes),
			CAST(avg(disk_used_bytes) AS INTEGER), max(disk_total_bytes), CAST(round(avg(running_bots)) AS INTEGER),
			avg(load1), CAST(avg(swap_used_bytes) AS INTEGER), max(swap_total_bytes), CAST(avg(net_rx_bps) AS INTEGER),
			CAST(avg(net_tx_bps) AS INTEGER), CAST(avg(disk_read_bps) AS INTEGER), CAST(avg(disk_write_bps) AS INTEGER),
			max(cpu_percent), max(memory_used_bytes), count(*)
		FROM node_telemetry WHERE node_id = ? AND sampled_at_ms > ?
		GROUP BY sampled_at_ms / ? ORDER BY t ASC`, bucketMS, bucketMS, bucketMS, nodeID, sinceMS, bucketMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TelemetryBucket
	for rows.Next() {
		b := domain.TelemetryBucket{Telemetry: domain.Telemetry{NodeID: nodeID}}
		if err := rows.Scan(&b.SampledAtMS, &b.CPUPercent, &b.LogicalCPUs, &b.MemoryUsedBytes, &b.MemoryTotalBytes,
			&b.DiskUsedBytes, &b.DiskTotalBytes, &b.RunningBots, &b.Load1, &b.SwapUsedBytes, &b.SwapTotalBytes,
			&b.NetRxBps, &b.NetTxBps, &b.DiskReadBps, &b.DiskWriteBps, &b.CPUMax, &b.MemoryMax, &b.Samples); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PruneTelemetry deletes at most batch samples older than beforeMS and returns
// how many it deleted; callers loop until it returns less than batch. Keeping
// each delete small keeps the write lock short.
func (db *DB) PruneTelemetry(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM node_telemetry WHERE (node_id, sampled_at_ms) IN
		(SELECT node_id, sampled_at_ms FROM node_telemetry WHERE sampled_at_ms < ? LIMIT ?)`, beforeMS, batch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountRunningBots counts bots on a node whose last observation is "running".
func (db *DB) CountRunningBots(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE node_id = ? AND observed_state = 'running'`, nodeID).Scan(&n)
	return n, err
}

// ListNodes returns all nodes.
func (db *DB) ListNodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, location_id, name, transport, endpoint, enabled, last_seen_at_ms, draining, public_address FROM nodes ORDER BY name LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Node
	for rows.Next() {
		var n domain.Node
		var enabled int
		if err := rows.Scan(&n.ID, &n.LocationID, &n.Name, &n.Transport, &n.Endpoint, &enabled, &n.LastSeenMS, &n.Draining, &n.PublicAddress); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		out = append(out, n)
	}
	return out, rows.Err()
}
