package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Metrics attribution storage (migration 0076): container_samples,
// process_snapshots and load_spikes. Reads are bounded by time range and a
// row limit; deletes run in small batches (callers loop).

// ListMetricSubjects returns every app that is not deleted with what the
// metrics collector needs (id, name, node, container, state, limits).
func (db *DB) ListMetricSubjects(ctx context.Context) ([]domain.MetricSubject, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, node_id, coalesce(container_id, ''), observed_state, memory_bytes, nano_cpus
		FROM bots WHERE desired_state != 'deleted' ORDER BY id LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MetricSubject
	for rows.Next() {
		var s domain.MetricSubject
		if err := rows.Scan(&s.ID, &s.Name, &s.NodeID, &s.ContainerID, &s.ObservedState, &s.MemoryBytes, &s.NanoCPUs); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// InsertContainerSamples stores one collector pass in one transaction.
func (db *DB) InsertContainerSamples(ctx context.Context, samples []domain.ContainerSample) error {
	if len(samples) == 0 {
		return nil
	}
	return db.tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO container_samples (bot_id, node_id, sampled_at_ms, cpu_cores,
			memory_bytes, net_rx_bps, net_tx_bps, blk_read_bps, blk_write_bps, pids) VALUES (?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, s := range samples {
			if _, err := stmt.ExecContext(ctx, s.BotID, s.NodeID, s.SampledAtMS, max(s.CPUCores, 0), max(s.MemoryBytes, 0),
				max(s.NetRxBps, 0), max(s.NetTxBps, 0), max(s.BlkReadBps, 0), max(s.BlkWriteBps, 0), max(s.PIDs, 0)); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// ListContainerBuckets averages container samples with fromMS <= t < toMS
// into buckets of stepMS (bucket start times, oldest first), per app. With
// botID set only that app is returned, otherwise every app sampled on
// nodeID.
func (db *DB) ListContainerBuckets(ctx context.Context, nodeID, botID string, fromMS, toMS, stepMS int64) ([]domain.ContainerBucket, error) {
	if stepMS < 1 {
		stepMS = 1
	}
	col, key := "node_id", nodeID
	if botID != "" {
		col, key = "bot_id", botID
	}
	rows, err := db.QueryContext(ctx, `SELECT bot_id, (sampled_at_ms / ?1) * ?1 AS t, avg(cpu_cores), avg(memory_bytes),
			avg(net_rx_bps), avg(net_tx_bps), avg(blk_read_bps), avg(blk_write_bps), avg(pids)
		FROM container_samples WHERE `+col+` = ?2 AND sampled_at_ms >= ?3 AND sampled_at_ms < ?4
		GROUP BY bot_id, t ORDER BY t, bot_id LIMIT 200000`, stepMS, key, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ContainerBucket
	for rows.Next() {
		var b domain.ContainerBucket
		if err := rows.Scan(&b.BotID, &b.BucketMS, &b.CPUCores, &b.MemoryBytes, &b.NetRxBps, &b.NetTxBps, &b.BlkReadBps,
			&b.BlkWriteBps, &b.PIDs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ContainerAverages returns each app's mean use on nodeID with
// fromMS <= t < toMS (BucketMS is fromMS).
func (db *DB) ContainerAverages(ctx context.Context, nodeID string, fromMS, toMS int64) ([]domain.ContainerBucket, error) {
	rows, err := db.QueryContext(ctx, `SELECT bot_id, avg(cpu_cores), avg(memory_bytes), avg(net_rx_bps), avg(net_tx_bps),
			avg(blk_read_bps), avg(blk_write_bps), avg(pids)
		FROM container_samples WHERE node_id = ? AND sampled_at_ms >= ? AND sampled_at_ms < ?
		GROUP BY bot_id ORDER BY 2 DESC LIMIT 1000`, nodeID, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ContainerBucket
	for rows.Next() {
		b := domain.ContainerBucket{BucketMS: fromMS}
		if err := rows.Scan(&b.BotID, &b.CPUCores, &b.MemoryBytes, &b.NetRxBps, &b.NetTxBps, &b.BlkReadBps, &b.BlkWriteBps, &b.PIDs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PruneContainerSamples deletes at most batch samples older than beforeMS.
func (db *DB) PruneContainerSamples(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	return db.pruneRowid(ctx, `container_samples`, `sampled_at_ms < ?`, beforeMS, batch)
}

func (db *DB) pruneRowid(ctx context.Context, table, cond string, arg any, batch int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE `+cond+` LIMIT ?)`, arg, batch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UsageBuckets is one app's bot_usage rows of resolution res as means
// (cpu cores, memory bytes, network bytes per second), for ranges older than
// the container samples keep. With botID empty every app on nodeID is
// returned.
func (db *DB) UsageBuckets(ctx context.Context, nodeID, botID string, res, fromMS, toMS int64) ([]domain.ContainerBucket, error) {
	cond, key := `u.bot_id IN (SELECT id FROM bots WHERE node_id = ?2)`, nodeID
	if botID != "" {
		cond, key = `u.bot_id = ?2`, botID
	}
	rows, err := db.QueryContext(ctx, `SELECT u.bot_id, u.bucket_ms,
			CASE WHEN u.measured > 0 THEN u.cpu_sum / u.measured ELSE 0 END,
			CASE WHEN u.measured > 0 THEN CAST(u.mem_sum AS REAL) / u.measured ELSE 0 END,
			CAST(u.net_rx AS REAL) / ?1, CAST(u.net_tx AS REAL) / ?1
		FROM bot_usage u WHERE u.res = ?1 AND `+cond+` AND u.bucket_ms >= ?3 AND u.bucket_ms < ?4
		ORDER BY u.bucket_ms, u.bot_id LIMIT 200000`, res, key, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ContainerBucket
	for rows.Next() {
		var b domain.ContainerBucket
		if err := rows.Scan(&b.BotID, &b.BucketMS, &b.CPUCores, &b.MemoryBytes, &b.NetRxBps, &b.NetTxBps); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InsertProcessSnapshot stores a snapshot and returns its id.
func (db *DB) InsertProcessSnapshot(ctx context.Context, s domain.ProcessSnapshot) (int64, error) {
	items := s.Items
	if items == nil {
		items = []domain.ProcessEntry{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return 0, err
	}
	src := s.Source
	if src == "" {
		src = domain.ProcSourceProc
	}
	res, err := db.ExecContext(ctx, `INSERT INTO process_snapshots (node_id, sampled_at_ms, reason, source, host_cpu_pct, processes_json)
		VALUES (?,?,?,?,?,?)`, s.NodeID, s.SampledAtMS, s.Reason, src, s.HostCPUPct, string(b))
	if err != nil {
		return 0, mapErr(err)
	}
	return res.LastInsertId()
}

// NearestProcessSnapshot returns the snapshot of nodeID closest to tMS within
// maxDistMS (ok=false when there is none).
func (db *DB) NearestProcessSnapshot(ctx context.Context, nodeID string, tMS, maxDistMS int64) (domain.ProcessSnapshot, bool, error) {
	var s domain.ProcessSnapshot
	var raw string
	err := db.QueryRowContext(ctx, `SELECT id, node_id, sampled_at_ms, reason, source, host_cpu_pct, processes_json
		FROM process_snapshots WHERE node_id = ?1 AND sampled_at_ms BETWEEN ?2 - ?3 AND ?2 + ?3
		ORDER BY abs(sampled_at_ms - ?2), id DESC LIMIT 1`, nodeID, tMS, maxDistMS).
		Scan(&s.ID, &s.NodeID, &s.SampledAtMS, &s.Reason, &s.Source, &s.HostCPUPct, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err := json.Unmarshal([]byte(raw), &s.Items); err != nil {
		return s, false, err
	}
	return s, true, nil
}

// PruneProcessSnapshots deletes at most batch snapshots older than beforeMS.
func (db *DB) PruneProcessSnapshots(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	return db.pruneRowid(ctx, `process_snapshots`, `sampled_at_ms < ?`, beforeMS, batch)
}

func spikeJSON(sp domain.LoadSpike) (string, string, error) {
	bots, procs := sp.TopBots, sp.TopProcesses
	if bots == nil {
		bots = []domain.SpikeBot{}
	}
	if procs == nil {
		procs = []domain.ProcessEntry{}
	}
	b1, err := json.Marshal(bots)
	if err != nil {
		return "", "", err
	}
	b2, err := json.Marshal(procs)
	return string(b1), string(b2), err
}

// InsertSpike stores a new spike record and returns its id.
func (db *DB) InsertSpike(ctx context.Context, sp domain.LoadSpike) (int64, error) {
	bots, procs, err := spikeJSON(sp)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `INSERT INTO load_spikes (node_id, started_at_ms, ended_at_ms, peak_cpu_pct, avg_cpu_pct,
		top_bots_json, top_processes_json) VALUES (?,?,?,?,?,?,?)`, sp.NodeID, sp.StartedAtMS, sp.EndedAtMS, sp.PeakCPUPct,
		sp.AvgCPUPct, bots, procs)
	if err != nil {
		return 0, mapErr(err)
	}
	return res.LastInsertId()
}

// UpdateSpike replaces a spike's end, figures and top lists.
func (db *DB) UpdateSpike(ctx context.Context, sp domain.LoadSpike) error {
	bots, procs, err := spikeJSON(sp)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE load_spikes SET ended_at_ms = ?, peak_cpu_pct = ?, avg_cpu_pct = ?, top_bots_json = ?,
		top_processes_json = ? WHERE id = ?`, sp.EndedAtMS, sp.PeakCPUPct, sp.AvgCPUPct, bots, procs, sp.ID)
	return mapErr(err)
}

// CloseOpenSpikes ends spikes of nodeID left open (by a restart) at atMS.
func (db *DB) CloseOpenSpikes(ctx context.Context, nodeID string, atMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE load_spikes SET ended_at_ms = max(?, started_at_ms) WHERE node_id = ? AND ended_at_ms IS NULL`, atMS, nodeID)
	return mapErr(err)
}

// ListSpikes returns spikes of nodeID overlapping [fromMS, toMS], oldest
// first (at most 500).
func (db *DB) ListSpikes(ctx context.Context, nodeID string, fromMS, toMS int64) ([]domain.LoadSpike, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, node_id, started_at_ms, ended_at_ms, peak_cpu_pct, avg_cpu_pct, top_bots_json,
			top_processes_json FROM load_spikes
		WHERE node_id = ? AND started_at_ms <= ? AND (ended_at_ms IS NULL OR ended_at_ms >= ?)
		ORDER BY started_at_ms LIMIT 500`, nodeID, toMS, fromMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.LoadSpike{}
	for rows.Next() {
		var sp domain.LoadSpike
		var bots, procs string
		if err := rows.Scan(&sp.ID, &sp.NodeID, &sp.StartedAtMS, &sp.EndedAtMS, &sp.PeakCPUPct, &sp.AvgCPUPct, &bots, &procs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(bots), &sp.TopBots); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(procs), &sp.TopProcesses); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// PruneSpikes deletes at most batch finished spikes that started before
// beforeMS.
func (db *DB) PruneSpikes(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	return db.pruneRowid(ctx, `load_spikes`, `started_at_ms < ? AND ended_at_ms IS NOT NULL`, beforeMS, batch)
}

// ListTelemetryRange averages node samples with fromMS <= t < toMS into
// buckets of stepMS starting at multiples of stepMS, oldest first.
func (db *DB) ListTelemetryRange(ctx context.Context, nodeID string, fromMS, toMS, stepMS int64) ([]domain.TelemetryBucket, error) {
	if stepMS < 1 {
		stepMS = 1
	}
	rows, err := db.QueryContext(ctx, `SELECT (sampled_at_ms / ?1) * ?1 AS t,
			avg(cpu_percent), max(logical_cpus), CAST(avg(memory_used_bytes) AS INTEGER), max(memory_total_bytes),
			CAST(avg(disk_used_bytes) AS INTEGER), max(disk_total_bytes), CAST(round(avg(running_bots)) AS INTEGER),
			avg(load1), CAST(avg(swap_used_bytes) AS INTEGER), max(swap_total_bytes), CAST(avg(net_rx_bps) AS INTEGER),
			CAST(avg(net_tx_bps) AS INTEGER), CAST(avg(disk_read_bps) AS INTEGER), CAST(avg(disk_write_bps) AS INTEGER),
			max(cpu_percent), max(memory_used_bytes), count(*)
		FROM node_telemetry WHERE node_id = ?2 AND sampled_at_ms >= ?3 AND sampled_at_ms < ?4
		GROUP BY t ORDER BY t ASC LIMIT 100000`, stepMS, nodeID, fromMS, toMS)
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

// OldestTelemetry returns the time of nodeID's oldest raw sample (0 = none).
func (db *DB) OldestTelemetry(ctx context.Context, nodeID string) (int64, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT min(sampled_at_ms) FROM node_telemetry WHERE node_id = ?`, nodeID).Scan(&v)
	return v.Int64, err
}

// ListNodeUsageRange returns nodeID's rollup rows of resolution res with
// fromMS <= bucket < toMS, oldest first.
func (db *DB) ListNodeUsageRange(ctx context.Context, nodeID string, res, fromMS, toMS int64) ([]domain.NodeUsageBucket, error) {
	rows, err := db.QueryContext(ctx, `SELECT node_id, res, bucket_ms, samples, cpu_sum, cpu_max, mem_sum, mem_max,
		mem_total, disk_used, disk_total, net_rx_sum, net_tx_sum, running_max
		FROM node_usage WHERE node_id = ? AND res = ? AND bucket_ms >= ? AND bucket_ms < ? ORDER BY bucket_ms LIMIT 20000`,
		nodeID, res, fromMS, toMS)
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

// eventActions maps audit actions to chart event kinds.
var eventActions = map[string]string{
	"bot.start": "start", "bot.stop": "stop", "bot.kill": "stop", "bot.restart": "restart",
	"bot.crash": "crash", "bot.gave_up": "crash",
}

// ListMetricEvents returns chart annotations for apps on nodeID (or one app
// when botID is set) with fromMS <= t <= toMS, oldest first: starts, stops,
// restarts and crashes from the activity record, finished deployments and
// backups from the operation history and, for a node, CPU spikes.
func (db *DB) ListMetricEvents(ctx context.Context, nodeID, botID string, fromMS, toMS int64, limit int) ([]domain.MetricEvent, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	cond, key := `b.node_id = ?`, nodeID
	if botID != "" {
		cond, key = `b.id = ?`, botID
	}
	actions := make([]string, 0, len(eventActions))
	args := []any{}
	for a := range eventActions {
		actions = append(actions, "?")
		args = append(args, a)
	}
	args = append(args, key, fromMS, toMS, limit)
	q := `SELECT a.at_ms, a.action, coalesce(a.bot_id, ''), coalesce(b.name, a.bot_name, ''), coalesce(a.target, ''), a.outcome
		FROM audit_events a JOIN bots b ON b.id = a.bot_id
		WHERE a.action IN (` + strings.Join(actions, ",") + `) AND ` + cond + ` AND a.at_ms BETWEEN ? AND ?
		ORDER BY a.at_ms DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []domain.MetricEvent
	for rows.Next() {
		var e domain.MetricEvent
		var action, target, outcome string
		if err := rows.Scan(&e.TMS, &action, &e.BotID, &e.BotName, &target, &outcome); err != nil {
			rows.Close()
			return nil, err
		}
		e.Kind = eventActions[action]
		switch {
		case e.Kind == "crash" && strings.Contains(strings.ToLower(target), "out of memory"):
			e.Kind, e.Text = "oom", e.BotName+" was killed: out of memory"
		case action == "bot.gave_up":
			e.Text = e.BotName + " stopped restarting after repeated crashes"
		case e.Kind == "crash":
			e.Text = e.BotName + " crashed"
			if target != "" {
				e.Text += ": " + target
			}
		case action == "bot.kill":
			e.Text = e.BotName + " was killed"
		default:
			e.Text = e.BotName + " " + map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[e.Kind]
		}
		if outcome != "ok" && e.Kind != "crash" && e.Kind != "oom" {
			e.Text += " (" + outcome + ")"
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = db.QueryContext(ctx, `SELECT coalesce(o.finished_at_ms, o.created_at_ms), o.kind, o.bot_id, b.name, o.status
		FROM operations o JOIN bots b ON b.id = o.bot_id
		WHERE o.kind IN ('deploy', 'backup') AND o.status IN ('succeeded', 'failed') AND `+cond+`
			AND coalesce(o.finished_at_ms, o.created_at_ms) BETWEEN ? AND ?
		ORDER BY 1 DESC LIMIT ?`, key, fromMS, toMS, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e domain.MetricEvent
		var status string
		if err := rows.Scan(&e.TMS, &e.Kind, &e.BotID, &e.BotName, &status); err != nil {
			rows.Close()
			return nil, err
		}
		verb := map[string]string{"deploy": "deployment", "backup": "backup"}[e.Kind]
		e.Text = e.BotName + " " + verb + " " + status
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if botID == "" {
		spikes, err := db.ListSpikes(ctx, nodeID, fromMS, toMS)
		if err != nil {
			return nil, err
		}
		for _, sp := range spikes {
			if sp.StartedAtMS < fromMS {
				continue
			}
			e := domain.MetricEvent{TMS: sp.StartedAtMS, Kind: "spike", Text: "CPU spike"}
			if sp.PeakCPUPct > 0 {
				e.Text += " (peak " + formatPct(sp.PeakCPUPct) + ")"
			}
			if len(sp.TopBots) > 0 {
				e.BotID, e.BotName = sp.TopBots[0].BotID, sp.TopBots[0].Name
				e.Text += ", mostly " + sp.TopBots[0].Name
			}
			out = append(out, e)
		}
	}
	sortEvents(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func formatPct(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + "%" }

// sortEvents orders events oldest first (stable for equal times).
func sortEvents(ev []domain.MetricEvent) {
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].TMS < ev[j].TMS })
}
