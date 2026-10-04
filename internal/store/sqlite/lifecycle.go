package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SetDesired records lifecycle intent in one short transaction. When the
// desired state is unchanged and force is false nothing is written and
// changed is false (idempotent repeats). Otherwise generation advances.
// Bots marked deleted are treated as absent.
func (db *DB) SetDesired(ctx context.Context, id, desired string, force bool, nowMS int64) (b domain.Bot, changed bool, err error) {
	return db.SetDesiredWithin(ctx, id, desired, force, nowMS, 0)
}

// SetDesiredWithin is SetDesired with admission control: when the bot is to
// run and nodeMemory > 0, the memory limits of every bot wanted running on
// its node (this one included) must fit in nodeMemory. The check and the
// intent change are one immediate transaction, so concurrent starts cannot
// overcommit together.
func (db *DB) SetDesiredWithin(ctx context.Context, id, desired string, force bool, nowMS, nodeMemory int64) (b domain.Bot, changed bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return b, false, err
	}
	defer tx.Rollback()
	b, err = scanBot(tx.QueryRowContext(ctx, `SELECT `+botCols+` FROM bots WHERE id = ?`, id))
	if err != nil {
		return b, false, err
	}
	if b.DesiredState == domain.DesiredDeleted {
		return b, false, domain.ErrNotFound
	}
	if b.DesiredState == desired && !force {
		return b, false, nil
	}
	if desired == domain.DesiredRunning && nodeMemory > 0 && b.DesiredState != domain.DesiredRunning {
		var others int64
		var need int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(`+botMemory+`), 0) FROM bots
			WHERE node_id = ? AND desired_state = 'running' AND id != ?`, b.NodeID, id).Scan(&others); err != nil {
			return b, false, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT `+botMemory+` FROM bots WHERE id = ?`, id).Scan(&need); err != nil {
			return b, false, err
		}
		if others+need > nodeMemory {
			return b, false, &domain.CapacityError{Need: need, Free: max(nodeMemory-others, 0), Budget: nodeMemory}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE bots SET desired_state = ?, generation = generation + 1, updated_at_ms = ? WHERE id = ?`,
		desired, nowMS, id); err != nil {
		return b, false, err
	}
	b.DesiredState = desired
	b.Generation++
	b.UpdatedAtMS = nowMS
	return b, true, tx.Commit()
}

// RestartIfRunning advances the generation (replacing the container) only
// while the bot is still wanted running, so a background job can never undo a
// newer Stop. It reports whether it did.
func (db *DB) RestartIfRunning(ctx context.Context, id string, nowMS int64) (domain.Bot, bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE bots SET generation = generation + 1, updated_at_ms = ?
		WHERE id = ? AND desired_state = 'running'`, nowMS, id)
	if err != nil {
		return domain.Bot{}, false, err
	}
	n, _ := res.RowsAffected()
	b, err := db.GetBot(ctx, id)
	return b, n == 1, err
}

// Observation is a runner's report of actual state for one bot generation.
type Observation struct {
	BotID      string
	Generation int64 // the generation this observation was made for
	State      string
	// SettleGeneration also sets observed_generation = Generation; use it when
	// the state (running/stopped/failed) reflects that generation completely.
	SettleGeneration bool
	// ContainerID is stored when non-empty; ClearContainer sets it to NULL.
	ContainerID    string
	ClearContainer bool
	ExitCode       *int64
	LastError      string // empty clears last_error
	// Reason is a domain.Reason* code; empty clears state_reason.
	Reason string
	// NextRetryAtMS is stored when non-zero and cleared otherwise.
	NextRetryAtMS int64
	// RestartCount replaces restart_count when non-nil.
	RestartCount *int64
	// StartedAtMS, when non-zero, is used instead of NowMS as last_started_at_ms
	// on a transition to running (an adopted container's real start time).
	StartedAtMS int64
	NowMS       int64
}

// Observe persists an observation only if the bot is still at o.Generation, so
// stale runner writes cannot overwrite newer intent. It reports whether the
// row was updated.
func (db *DB) Observe(ctx context.Context, o Observation) (bool, error) {
	var cid sql.NullString
	if o.ContainerID != "" {
		cid = sql.NullString{String: o.ContainerID, Valid: true}
	}
	var lastErr sql.NullString
	if o.LastError != "" {
		lastErr = sql.NullString{String: o.LastError, Valid: true}
	}
	res, err := db.ExecContext(ctx, `UPDATE bots SET
			observed_state = ?,
			observed_generation = CASE WHEN ? THEN generation ELSE observed_generation END,
			container_id = CASE WHEN ? THEN NULL WHEN ? IS NOT NULL THEN ? ELSE container_id END,
			last_exit_code = COALESCE(?, last_exit_code),
			last_error = ?,
			state_reason = ?,
			next_retry_at_ms = ?,
			restart_count = COALESCE(?, restart_count),
			last_started_at_ms = CASE WHEN ? = 'running' AND observed_state != 'running' THEN COALESCE(?, ?) ELSE last_started_at_ms END,
			observed_at_ms = ?
		WHERE id = ? AND generation = ?`,
		o.State, boolInt(o.SettleGeneration), boolInt(o.ClearContainer), cid, cid, o.ExitCode, lastErr,
		nullStr(o.Reason), nullInt(o.NextRetryAtMS), o.RestartCount, o.State, nullInt(o.StartedAtMS), o.NowMS, o.NowMS,
		o.BotID, o.Generation)
	if err != nil {
		return false, mapErr(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ListBotsByNode returns every bot assigned to a node, including those marked
// deleted whose teardown is still pending.
func (db *DB) ListBotsByNode(ctx context.Context, nodeID string) ([]domain.Bot, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+botCols+` FROM bots WHERE node_id = ? ORDER BY created_at_ms LIMIT 10000`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Bot
	for rows.Next() {
		b, err := scanBot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MarkNodeObservedUnknown demotes stale "live" observations after a runner
// (re)start or loss of Docker contact, until reconciliation confirms reality.
func (db *DB) MarkNodeObservedUnknown(ctx context.Context, nodeID string, nowMS int64) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE bots SET observed_state = 'unknown', observed_at_ms = ?
		WHERE node_id = ? AND observed_state IN ('running', 'starting', 'building', 'stopping')`, nowMS, nodeID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// TouchNode records runner contact.
func (db *DB) TouchNode(ctx context.Context, nodeID string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE nodes SET last_seen_at_ms = ?, updated_at_ms = ? WHERE id = ?`, nowMS, nowMS, nodeID)
	return err
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
func nullInt(v int64) sql.NullInt64   { return sql.NullInt64{Int64: v, Valid: v != 0} }
