package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Usage analytics storage (migration 0052). Writes are batched: the
// collector adds one transaction of 5-minute rows per bucket, and each
// rollup is one INSERT ... SELECT over a bounded time range.

// ListUsageSubjects returns what the usage collector samples for every bot
// that is not deleted.
func (db *DB) ListUsageSubjects(ctx context.Context) ([]domain.UsageSubject, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, node_id, desired_state, observed_state, container_id, restart_count,
		last_started_at_ms, memory_bytes FROM bots WHERE desired_state != 'deleted' ORDER BY id LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UsageSubject
	for rows.Next() {
		var s domain.UsageSubject
		if err := rows.Scan(&s.ID, &s.NodeID, &s.DesiredState, &s.ObservedState, &s.ContainerID, &s.RestartCount,
			&s.LastStartedAtMS, &s.MemoryBytes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AddUsage adds collector rows (5-minute buckets) in one transaction. A row
// whose bucket already exists is added to it (a flush on shutdown followed
// by one after restart), and rows of bots deleted meanwhile are skipped.
func (db *DB) AddUsage(ctx context.Context, rows []domain.UsageBucket) error {
	if len(rows) == 0 {
		return nil
	}
	return db.tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO bot_usage (bot_id, res, bucket_ms, samples, wanted, up, measured,
			cpu_sum, cpu_max, mem_sum, mem_max, mem_limit, net_rx, net_tx, disk_max, crashes, starts)
			SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM bots WHERE id = ?1)
			ON CONFLICT(bot_id, res, bucket_ms) DO UPDATE SET
				samples = samples + excluded.samples, wanted = wanted + excluded.wanted, up = up + excluded.up,
				measured = measured + excluded.measured, cpu_sum = cpu_sum + excluded.cpu_sum,
				cpu_max = max(cpu_max, excluded.cpu_max), mem_sum = mem_sum + excluded.mem_sum,
				mem_max = max(mem_max, excluded.mem_max), mem_limit = max(mem_limit, excluded.mem_limit),
				net_rx = net_rx + excluded.net_rx, net_tx = net_tx + excluded.net_tx,
				disk_max = CASE WHEN excluded.disk_max IS NULL THEN disk_max WHEN disk_max IS NULL THEN excluded.disk_max
					ELSE max(disk_max, excluded.disk_max) END,
				crashes = crashes + excluded.crashes, starts = starts + excluded.starts`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, r.BotID, r.Res, r.BucketMS, r.Samples, r.Wanted, r.Up, r.Measured,
				max(r.CPUSum, 0), max(r.CPUMax, 0), max(r.MemSum, 0), max(r.MemMax, 0), max(r.MemLimit, 0),
				max(r.NetRx, 0), max(r.NetTx, 0), r.DiskMax, r.Crashes, r.Starts); err != nil {
				return err
			}
		}
		return nil
	})
}

// UsageMark returns a rollup watermark (ok=false when it was never set).
func (db *DB) UsageMark(ctx context.Context, name string) (int64, bool, error) {
	var v int64
	err := db.QueryRowContext(ctx, `SELECT value FROM usage_marks WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return v, err == nil, err
}

// SetUsageMark stores a rollup watermark.
func (db *DB) SetUsageMark(ctx context.Context, name string, v int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO usage_marks (name, value) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value`, name, v)
	return mapErr(err)
}

// RollupBotHours recomputes the resource columns of hourly rows from the
// 5-minute rows of every hour starting at or after fromMS. Recomputing is
// idempotent: the hour's values are replaced, never added to.
func (db *DB) RollupBotHours(ctx context.Context, fromMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_usage (bot_id, res, bucket_ms, samples, wanted, up, measured,
			cpu_sum, cpu_max, mem_sum, mem_max, mem_limit, net_rx, net_tx, disk_max, crashes, starts)
		SELECT bot_id, 3600, (bucket_ms / 3600000) * 3600000, sum(samples), sum(wanted), sum(up), sum(measured),
			sum(cpu_sum), max(cpu_max), sum(mem_sum), max(mem_max), max(mem_limit), sum(net_rx), sum(net_tx),
			max(disk_max), sum(crashes), sum(starts)
		FROM bot_usage WHERE res = 300 AND bucket_ms >= ?
		GROUP BY bot_id, bucket_ms / 3600000
		ON CONFLICT(bot_id, res, bucket_ms) DO UPDATE SET
			samples = excluded.samples, wanted = excluded.wanted, up = excluded.up, measured = excluded.measured,
			cpu_sum = excluded.cpu_sum, cpu_max = excluded.cpu_max, mem_sum = excluded.mem_sum,
			mem_max = excluded.mem_max, mem_limit = excluded.mem_limit, net_rx = excluded.net_rx,
			net_tx = excluded.net_tx, disk_max = excluded.disk_max, crashes = excluded.crashes, starts = excluded.starts`,
		fromMS-fromMS%3600000)
	return mapErr(err)
}

// RollupBotEvents recomputes the deployment and backup columns of hourly
// rows from operations finished at or after the start of fromMS's hour.
// Operations are pruned per bot later; the counts stay in the rollups.
func (db *DB) RollupBotEvents(ctx context.Context, fromMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_usage (bot_id, res, bucket_ms, deploys_ok, deploys_failed, deploy_ms,
			backups_ok, backups_failed, backup_bytes)
		SELECT o.bot_id, 3600, (o.finished_at_ms / 3600000) * 3600000,
			sum(o.kind = 'deploy' AND o.status = 'succeeded'),
			sum(o.kind = 'deploy' AND o.status = 'failed'),
			sum(CASE WHEN o.kind = 'deploy' THEN max(o.finished_at_ms - coalesce(o.started_at_ms, o.created_at_ms), 0) ELSE 0 END),
			sum(o.kind = 'backup' AND o.status = 'succeeded'),
			sum(o.kind = 'backup' AND o.status = 'failed'),
			sum(CASE WHEN o.kind = 'backup' AND o.status = 'succeeded' THEN coalesce(bb.size_bytes, 0) ELSE 0 END)
		FROM operations o LEFT JOIN bot_backups bb ON o.kind = 'backup' AND bb.id = o.source_ref
		WHERE o.finished_at_ms >= ? AND o.kind IN ('deploy', 'backup') AND o.status IN ('succeeded', 'failed')
		GROUP BY o.bot_id, o.finished_at_ms / 3600000
		ON CONFLICT(bot_id, res, bucket_ms) DO UPDATE SET
			deploys_ok = excluded.deploys_ok, deploys_failed = excluded.deploys_failed, deploy_ms = excluded.deploy_ms,
			backups_ok = excluded.backups_ok, backups_failed = excluded.backups_failed, backup_bytes = excluded.backup_bytes`,
		fromMS-fromMS%3600000)
	return mapErr(err)
}

// RollupBotDays recomputes daily rows (UTC days) from the hourly rows of
// every day starting at or after fromMS's day.
func (db *DB) RollupBotDays(ctx context.Context, fromMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_usage (bot_id, res, bucket_ms, samples, wanted, up, measured,
			cpu_sum, cpu_max, mem_sum, mem_max, mem_limit, net_rx, net_tx, disk_max, crashes, starts,
			deploys_ok, deploys_failed, deploy_ms, backups_ok, backups_failed, backup_bytes)
		SELECT bot_id, 86400, (bucket_ms / 86400000) * 86400000, sum(samples), sum(wanted), sum(up), sum(measured),
			sum(cpu_sum), max(cpu_max), sum(mem_sum), max(mem_max), max(mem_limit), sum(net_rx), sum(net_tx),
			max(disk_max), sum(crashes), sum(starts), sum(deploys_ok), sum(deploys_failed), sum(deploy_ms),
			sum(backups_ok), sum(backups_failed), sum(backup_bytes)
		FROM bot_usage WHERE res = 3600 AND bucket_ms >= ?
		GROUP BY bot_id, bucket_ms / 86400000
		ON CONFLICT(bot_id, res, bucket_ms) DO UPDATE SET
			samples = excluded.samples, wanted = excluded.wanted, up = excluded.up, measured = excluded.measured,
			cpu_sum = excluded.cpu_sum, cpu_max = excluded.cpu_max, mem_sum = excluded.mem_sum,
			mem_max = excluded.mem_max, mem_limit = excluded.mem_limit, net_rx = excluded.net_rx,
			net_tx = excluded.net_tx, disk_max = excluded.disk_max, crashes = excluded.crashes, starts = excluded.starts,
			deploys_ok = excluded.deploys_ok, deploys_failed = excluded.deploys_failed, deploy_ms = excluded.deploy_ms,
			backups_ok = excluded.backups_ok, backups_failed = excluded.backups_failed, backup_bytes = excluded.backup_bytes`,
		fromMS-fromMS%86400000)
	return mapErr(err)
}

// RollupNodeHours recomputes hourly node rows from node_telemetry samples of
// every hour starting at or after fromMS's hour.
func (db *DB) RollupNodeHours(ctx context.Context, fromMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO node_usage (node_id, res, bucket_ms, samples, cpu_sum, cpu_max, mem_sum,
			mem_max, mem_total, disk_used, disk_total, net_rx_sum, net_tx_sum, running_max)
		SELECT node_id, 3600, (sampled_at_ms / 3600000) * 3600000, count(*), sum(cpu_percent), max(cpu_percent),
			sum(memory_used_bytes), max(memory_used_bytes), max(memory_total_bytes), max(disk_used_bytes),
			max(disk_total_bytes), sum(net_rx_bps), sum(net_tx_bps), max(running_bots)
		FROM node_telemetry WHERE sampled_at_ms >= ?
		GROUP BY node_id, sampled_at_ms / 3600000
		ON CONFLICT(node_id, res, bucket_ms) DO UPDATE SET
			samples = excluded.samples, cpu_sum = excluded.cpu_sum, cpu_max = excluded.cpu_max, mem_sum = excluded.mem_sum,
			mem_max = excluded.mem_max, mem_total = excluded.mem_total, disk_used = excluded.disk_used,
			disk_total = excluded.disk_total, net_rx_sum = excluded.net_rx_sum, net_tx_sum = excluded.net_tx_sum,
			running_max = excluded.running_max`, fromMS-fromMS%3600000)
	return mapErr(err)
}

// RollupNodeDays recomputes daily node rows from hourly ones.
func (db *DB) RollupNodeDays(ctx context.Context, fromMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO node_usage (node_id, res, bucket_ms, samples, cpu_sum, cpu_max, mem_sum,
			mem_max, mem_total, disk_used, disk_total, net_rx_sum, net_tx_sum, running_max)
		SELECT node_id, 86400, (bucket_ms / 86400000) * 86400000, sum(samples), sum(cpu_sum), max(cpu_max),
			sum(mem_sum), max(mem_max), max(mem_total), max(disk_used), max(disk_total), sum(net_rx_sum),
			sum(net_tx_sum), max(running_max)
		FROM node_usage WHERE res = 3600 AND bucket_ms >= ?
		GROUP BY node_id, bucket_ms / 86400000
		ON CONFLICT(node_id, res, bucket_ms) DO UPDATE SET
			samples = excluded.samples, cpu_sum = excluded.cpu_sum, cpu_max = excluded.cpu_max, mem_sum = excluded.mem_sum,
			mem_max = excluded.mem_max, mem_total = excluded.mem_total, disk_used = excluded.disk_used,
			disk_total = excluded.disk_total, net_rx_sum = excluded.net_rx_sum, net_tx_sum = excluded.net_tx_sum,
			running_max = excluded.running_max`, fromMS-fromMS%86400000)
	return mapErr(err)
}

// PruneUsage deletes at most batch bot_usage rows of resolution res older
// than beforeMS; callers loop until fewer than batch are deleted.
func (db *DB) PruneUsage(ctx context.Context, res, beforeMS int64, batch int) (int64, error) {
	r, err := db.ExecContext(ctx, `DELETE FROM bot_usage WHERE (bot_id, res, bucket_ms) IN
		(SELECT bot_id, res, bucket_ms FROM bot_usage WHERE res = ? AND bucket_ms < ? LIMIT ?)`, res, beforeMS, batch)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// PruneNodeUsage is PruneUsage for node_usage.
func (db *DB) PruneNodeUsage(ctx context.Context, res, beforeMS int64, batch int) (int64, error) {
	r, err := db.ExecContext(ctx, `DELETE FROM node_usage WHERE (node_id, res, bucket_ms) IN
		(SELECT node_id, res, bucket_ms FROM node_usage WHERE res = ? AND bucket_ms < ? LIMIT ?)`, res, beforeMS, batch)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

const usageCols = `bot_id, res, bucket_ms, samples, wanted, up, measured, cpu_sum, cpu_max, mem_sum, mem_max, mem_limit,
	net_rx, net_tx, disk_max, crashes, starts, deploys_ok, deploys_failed, deploy_ms, backups_ok, backups_failed, backup_bytes`

// ListBotUsage returns one bot's rows of resolution res from fromMS on,
// oldest first.
func (db *DB) ListBotUsage(ctx context.Context, botID string, res, fromMS int64) ([]domain.UsageBucket, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+usageCols+` FROM bot_usage WHERE bot_id = ? AND res = ? AND bucket_ms >= ?
		ORDER BY bucket_ms LIMIT 5000`, botID, res, fromMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UsageBucket
	for rows.Next() {
		var b domain.UsageBucket
		if err := rows.Scan(&b.BotID, &b.Res, &b.BucketMS, &b.Samples, &b.Wanted, &b.Up, &b.Measured, &b.CPUSum, &b.CPUMax,
			&b.MemSum, &b.MemMax, &b.MemLimit, &b.NetRx, &b.NetTx, &b.DiskMax, &b.Crashes, &b.Starts, &b.DeploysOK,
			&b.DeploysFailed, &b.DeployMS, &b.BackupsOK, &b.BackupsFailed, &b.BackupBytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// usageBotFilter is an SQL condition on a bot id column (col) for f, and
// its arguments. Excluded owners are few (administrators and accounts with
// more administration permissions than the viewer).
func usageBotFilter(col string, f domain.UsageFilter) (string, []any) {
	if len(f.ExcludeOwners) == 0 {
		return "", nil
	}
	args := make([]any, len(f.ExcludeOwners))
	for i, id := range f.ExcludeOwners {
		args[i] = id
	}
	return ` AND ` + col + ` IN (SELECT id FROM bots WHERE owner_id NOT IN (?` + strings.Repeat(",?", len(args)-1) + `))`, args
}

// AggregateUsage sums bot_usage rows of resolution res from fromMS on over
// the bots f allows, per bucket.
func (db *DB) AggregateUsage(ctx context.Context, res, fromMS int64, f domain.UsageFilter) ([]domain.UsageAggregate, error) {
	cond, fargs := usageBotFilter("u.bot_id", f)
	args := append([]any{res, fromMS}, fargs...)
	rows, err := db.QueryContext(ctx, `SELECT bucket_ms,
			sum(CASE WHEN measured > 0 THEN cpu_sum / measured ELSE 0 END),
			CAST(sum(CASE WHEN measured > 0 THEN mem_sum / measured ELSE 0 END) AS INTEGER),
			sum(net_rx), sum(net_tx), sum(wanted), sum(up), sum(crashes), sum(starts), sum(deploys_ok),
			sum(deploys_failed), sum(deploy_ms), sum(backups_ok), sum(backups_failed), sum(backup_bytes)
		FROM bot_usage u WHERE res = ? AND bucket_ms >= ?`+cond+`
		GROUP BY bucket_ms ORDER BY bucket_ms LIMIT 5000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UsageAggregate
	for rows.Next() {
		var a domain.UsageAggregate
		if err := rows.Scan(&a.BucketMS, &a.CPUCores, &a.MemBytes, &a.NetRx, &a.NetTx, &a.Wanted, &a.Up, &a.Crashes,
			&a.Starts, &a.DeploysOK, &a.DeploysFailed, &a.DeployMS, &a.BackupsOK, &a.BackupsFailed, &a.BackupBytes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TopUsage returns the bots with the highest average CPU ("cpu"), memory
// ("mem") or network traffic ("net") over rows of resolution res from fromMS.
func (db *DB) TopUsage(ctx context.Context, res, fromMS int64, f domain.UsageFilter, by string, limit int) ([]domain.UsageConsumer, error) {
	order := map[string]string{"cpu": "cpu", "mem": "mem", "net": "net"}[by]
	if order == "" {
		order = "cpu"
	}
	cond, fargs := usageBotFilter("u.bot_id", f)
	args := append([]any{res, fromMS}, fargs...)
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, `SELECT u.bot_id, b.name, b.kind, b.owner_id, b.node_id,
			coalesce(sum(u.cpu_sum) / nullif(sum(u.measured), 0), 0) AS cpu,
			CAST(coalesce(sum(u.mem_sum) / nullif(sum(u.measured), 0), 0) AS INTEGER) AS mem,
			sum(u.net_rx + u.net_tx) AS net
		FROM bot_usage u JOIN bots b ON b.id = u.bot_id
		WHERE u.res = ? AND u.bucket_ms >= ?`+cond+`
		GROUP BY u.bot_id HAVING `+order+` > 0 ORDER BY `+order+` DESC, b.name LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UsageConsumer
	for rows.Next() {
		var c domain.UsageConsumer
		if err := rows.Scan(&c.BotID, &c.Name, &c.Kind, &c.OwnerID, &c.NodeID, &c.CPUCores, &c.MemBytes, &c.NetBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UsageBotCounts counts bots that are not deleted by kind and whether they
// are running, over the bots f allows.
func (db *DB) UsageBotCounts(ctx context.Context, f domain.UsageFilter) (map[string][2]int64, error) {
	cond, args := usageBotFilter("id", f)
	rows, err := db.QueryContext(ctx, `SELECT kind, observed_state = 'running', count(*) FROM bots
		WHERE desired_state != 'deleted'`+cond+` GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var kind string
		var running bool
		var n int64
		if err := rows.Scan(&kind, &running, &n); err != nil {
			return nil, err
		}
		v := out[kind]
		v[0] += n
		if running {
			v[1] += n
		}
		out[kind] = v
	}
	return out, rows.Err()
}

// ListNodeUsage returns node rows of resolution res from fromMS on.
func (db *DB) ListNodeUsage(ctx context.Context, res, fromMS int64) ([]domain.NodeUsageBucket, error) {
	rows, err := db.QueryContext(ctx, `SELECT node_id, res, bucket_ms, samples, cpu_sum, cpu_max, mem_sum, mem_max,
		mem_total, disk_used, disk_total, net_rx_sum, net_tx_sum, running_max
		FROM node_usage WHERE res = ? AND bucket_ms >= ? ORDER BY node_id, bucket_ms LIMIT 50000`, res, fromMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.NodeUsageBucket
	for rows.Next() {
		var b domain.NodeUsageBucket
		if err := rows.Scan(&b.NodeID, &b.Res, &b.BucketMS, &b.Samples, &b.CPUSum, &b.CPUMax, &b.MemSum, &b.MemMax,
			&b.MemTotal, &b.DiskUsed, &b.DiskTotal, &b.NetRxSum, &b.NetTxSum, &b.RunningMax); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListTicketStats returns tickets opened at or after fromMS and tickets
// still open or pending, with the time of the first staff reply the
// requester could see (at most 5,000, newest first).
func (db *DB) ListTicketStats(ctx context.Context, fromMS int64) ([]domain.TicketStat, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.user_id, t.created_at_ms, t.closed_at_ms, t.status,
			(SELECT min(m.created_at_ms) FROM support_ticket_messages m
				WHERE m.ticket_id = t.id AND m.staff = 1 AND m.internal = 0 AND m.kind = 'message')
		FROM support_tickets t WHERE t.created_at_ms >= ? OR t.status IN ('open', 'pending')
		ORDER BY t.created_at_ms DESC LIMIT 5000`, fromMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TicketStat
	for rows.Next() {
		var t domain.TicketStat
		if err := rows.Scan(&t.UserID, &t.CreatedAtMS, &t.ClosedAtMS, &t.Status, &t.FirstResponseMS); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
