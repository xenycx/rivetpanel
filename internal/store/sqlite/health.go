package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// RecordHeartbeat stores the time of a bot's latest SDK push.
func (db *DB) RecordHeartbeat(ctx context.Context, botID string, nowMS int64, ready *bool) error {
	var r any
	if ready != nil {
		r = boolInt(*ready)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO bot_health (bot_id, last_seen_at_ms, ready) VALUES (?,?,?)
		ON CONFLICT(bot_id) DO UPDATE SET last_seen_at_ms = excluded.last_seen_at_ms,
		ready = COALESCE(excluded.ready, bot_health.ready)`, botID, nowMS, r)
	return err
}

// GetHealth returns a bot's reported health; ErrNotFound when it never pushed.
func (db *DB) GetHealth(ctx context.Context, botID string) (domain.BotHealth, error) {
	var h domain.BotHealth
	var ready sql.NullInt64
	var st int
	err := db.QueryRowContext(ctx, `SELECT bot_id, last_seen_at_ms, ready, stale_alerted FROM bot_health WHERE bot_id = ?`, botID).
		Scan(&h.BotID, &h.LastSeenAtMS, &ready, &st)
	if ready.Valid {
		b := ready.Int64 == 1
		h.Ready = &b
	}
	h.StaleAlerted = st == 1
	return h, mapErr(err)
}

// SetStaleAlerted records whether a stale-heartbeat alert is outstanding.
func (db *DB) SetStaleAlerted(ctx context.Context, botID string, on bool) error {
	_, err := db.ExecContext(ctx, `UPDATE bot_health SET stale_alerted = ? WHERE bot_id = ?`, boolInt(on), botID)
	return err
}

// GetAlertPrefs returns saved preferences or the defaults.
func (db *DB) GetAlertPrefs(ctx context.Context, botID string) (domain.AlertPrefs, error) {
	var c, d, b, r int
	p := domain.DefaultAlertPrefs
	err := db.QueryRowContext(ctx, `SELECT crash, deploy, backup, recovery, heartbeat_after_s FROM bot_alert_prefs WHERE bot_id = ?`, botID).
		Scan(&c, &d, &b, &r, &p.HeartbeatAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultAlertPrefs, nil
	}
	p.Crash, p.Deploy, p.Backup, p.Recovery = c == 1, d == 1, b == 1, r == 1
	return p, err
}

// SetAlertPrefs saves a bot's preferences.
func (db *DB) SetAlertPrefs(ctx context.Context, botID string, p domain.AlertPrefs, nowMS int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_alert_prefs (bot_id, crash, deploy, backup, recovery, heartbeat_after_s, updated_at_ms)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(bot_id) DO UPDATE SET crash = excluded.crash, deploy = excluded.deploy,
		backup = excluded.backup, recovery = excluded.recovery, heartbeat_after_s = excluded.heartbeat_after_s,
		updated_at_ms = excluded.updated_at_ms`, botID, boolInt(p.Crash), boolInt(p.Deploy), boolInt(p.Backup), boolInt(p.Recovery),
		p.HeartbeatAfter, nowMS)
	return mapErr(err)
}

// HeartbeatWatches lists running bots that have a heartbeat alert, with
// their last report (nil when they never pushed).
func (db *DB) HeartbeatWatches(ctx context.Context) ([]domain.HeartbeatWatch, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.bot_id, p.heartbeat_after_s, p.recovery, h.last_seen_at_ms, h.stale_alerted
		FROM bot_alert_prefs p LEFT JOIN bot_health h ON h.bot_id = p.bot_id
		WHERE p.heartbeat_after_s > 0 LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id       string
		after    int
		recovery int
		seen     sql.NullInt64
		alerted  sql.NullInt64
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.after, &r.recovery, &r.seen, &r.alerted); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []domain.HeartbeatWatch
	for _, r := range rs {
		b, err := db.GetBot(ctx, r.id)
		if err != nil {
			continue
		}
		w := domain.HeartbeatWatch{Bot: b, Prefs: domain.AlertPrefs{HeartbeatAfter: r.after, Recovery: r.recovery == 1}}
		if r.seen.Valid {
			w.Health = &domain.BotHealth{BotID: r.id, LastSeenAtMS: r.seen.Int64, StaleAlerted: r.alerted.Int64 == 1}
		}
		out = append(out, w)
	}
	return out, nil
}

func scanHealthProbe(row interface{ Scan(...any) error }) (domain.HealthProbe, error) {
	var p domain.HealthProbe
	var restart int
	err := row.Scan(&p.BotID, &p.Kind, &p.HostPort, &p.Path, &p.IntervalSeconds, &p.TimeoutMS,
		&p.FailureThreshold, &p.SuccessThreshold, &p.StartupGraceSeconds, &restart, &p.Status,
		&p.ConsecutiveFailures, &p.ConsecutiveSuccesses, &p.LastCheckedAtMS, &p.LastError, &p.UpdatedAtMS)
	p.RestartUnhealthy = restart == 1
	return p, mapErr(err)
}

const healthProbeCols = `bot_id,kind,host_port,path,interval_s,timeout_ms,failure_threshold,success_threshold,startup_grace_s,restart_unhealthy,status,consecutive_failures,consecutive_successes,last_checked_at_ms,last_error,updated_at_ms`

func (db *DB) GetHealthProbe(ctx context.Context, botID string) (domain.HealthProbe, error) {
	return scanHealthProbe(db.QueryRowContext(ctx, `SELECT `+healthProbeCols+` FROM bot_health_probes WHERE bot_id=?`, botID))
}

func (db *DB) ListHealthProbes(ctx context.Context) ([]domain.HealthProbe, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+healthProbeCols+` FROM bot_health_probes ORDER BY bot_id LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HealthProbe
	for rows.Next() {
		p, err := scanHealthProbe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) SetHealthProbe(ctx context.Context, p domain.HealthProbe) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_health_probes (bot_id,kind,host_port,path,interval_s,timeout_ms,failure_threshold,success_threshold,startup_grace_s,restart_unhealthy,updated_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(bot_id) DO UPDATE SET kind=excluded.kind,host_port=excluded.host_port,path=excluded.path,interval_s=excluded.interval_s,timeout_ms=excluded.timeout_ms,failure_threshold=excluded.failure_threshold,success_threshold=excluded.success_threshold,startup_grace_s=excluded.startup_grace_s,restart_unhealthy=excluded.restart_unhealthy,status='unknown',consecutive_failures=0,consecutive_successes=0,last_checked_at_ms=NULL,last_error=NULL,updated_at_ms=excluded.updated_at_ms`, p.BotID, p.Kind, p.HostPort, p.Path, p.IntervalSeconds, p.TimeoutMS, p.FailureThreshold, p.SuccessThreshold, p.StartupGraceSeconds, boolInt(p.RestartUnhealthy), p.UpdatedAtMS)
	return mapErr(err)
}

func (db *DB) DeleteHealthProbe(ctx context.Context, botID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM bot_health_probes WHERE bot_id=?`, botID)
	return err
}

func (db *DB) RecordHealthProbe(ctx context.Context, p domain.HealthProbe) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE bot_health_probes SET status=?,consecutive_failures=?,consecutive_successes=?,last_checked_at_ms=?,last_error=? WHERE bot_id=? AND updated_at_ms=?`, p.Status, p.ConsecutiveFailures, p.ConsecutiveSuccesses, p.LastCheckedAtMS, p.LastError, p.BotID, p.UpdatedAtMS)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
