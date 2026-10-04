package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const schedCols = `s.id, s.bot_id, s.owner_id, COALESCE(u.email, ''), s.action, s.spec, s.timezone, s.enabled, s.next_run_at_ms,
	s.last_run_at_ms, s.last_status, s.last_message, s.created_at_ms, s.updated_at_ms, s.chain`

// chainPlaceholder is stored in schedules.action for task chains; it is never
// executed (the action column predates chains and keeps its constraint).
const chainPlaceholder = "restart"

func scanSchedule(row interface{ Scan(...any) error }) (domain.Schedule, error) {
	var s domain.Schedule
	var en, chain int
	err := row.Scan(&s.ID, &s.BotID, &s.OwnerID, &s.OwnerEmail, &s.Action, &s.Spec, &s.Timezone, &en, &s.NextRunMS,
		&s.LastRunMS, &s.LastStatus, &s.LastMessage, &s.CreatedAtMS, &s.UpdatedAtMS, &chain)
	s.Enabled = en == 1
	if chain == 1 {
		s.Action = domain.ScheduleChain
	}
	return s, mapErr(err)
}

func storedAction(s domain.Schedule) (string, int) {
	if s.Action == domain.ScheduleChain {
		return chainPlaceholder, 1
	}
	return s.Action, 0
}

func writeTasks(ctx context.Context, tx *sql.Tx, s domain.Schedule) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_tasks WHERE schedule_id = ?`, s.ID); err != nil {
		return err
	}
	if s.Action != domain.ScheduleChain {
		return nil
	}
	for i, t := range s.Tasks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_tasks (schedule_id, seq, action, payload, delay_seconds, continue_on_failure)
			VALUES (?, ?, ?, ?, ?, ?)`, s.ID, i, t.Action, t.Payload, t.DelaySeconds, boolInt(t.ContinueOnFailure)); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// loadTasks fills the tasks of chain schedules.
func (db *DB) loadTasks(ctx context.Context, list []domain.Schedule) error {
	for i := range list {
		if list[i].Action != domain.ScheduleChain {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT action, payload, delay_seconds, continue_on_failure FROM schedule_tasks
			WHERE schedule_id = ? ORDER BY seq`, list[i].ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var t domain.ScheduleTask
			var cont int
			if err := rows.Scan(&t.Action, &t.Payload, &t.DelaySeconds, &cont); err != nil {
				rows.Close()
				return err
			}
			t.ContinueOnFailure = cont == 1
			list[i].Tasks = append(list[i].Tasks, t)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) InsertSchedule(ctx context.Context, s domain.Schedule) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	action, chain := storedAction(s)
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedules (id, bot_id, owner_id, action, spec, timezone, enabled, next_run_at_ms,
		created_at_ms, updated_at_ms, chain) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, s.ID, s.BotID, s.OwnerID, action, s.Spec, s.Timezone,
		boolInt(s.Enabled), s.NextRunMS, s.CreatedAtMS, s.UpdatedAtMS, chain); err != nil {
		return mapErr(err)
	}
	if err := writeTasks(ctx, tx, s); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) UpdateSchedule(ctx context.Context, s domain.Schedule) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	action, chain := storedAction(s)
	res, err := tx.ExecContext(ctx, `UPDATE schedules SET action = ?, spec = ?, timezone = ?, enabled = ?, next_run_at_ms = ?,
		updated_at_ms = ?, chain = ? WHERE id = ? AND bot_id = ?`, action, s.Spec, s.Timezone, boolInt(s.Enabled), s.NextRunMS, s.UpdatedAtMS, chain, s.ID, s.BotID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	if err := writeTasks(ctx, tx, s); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) GetSchedule(ctx context.Context, botID, id string) (domain.Schedule, error) {
	s, err := scanSchedule(db.QueryRowContext(ctx, `SELECT `+schedCols+` FROM schedules s LEFT JOIN users u ON u.id = s.owner_id
		WHERE s.id = ? AND s.bot_id = ?`, id, botID))
	if err != nil {
		return s, err
	}
	list := []domain.Schedule{s}
	err = db.loadTasks(ctx, list)
	return list[0], err
}

func (db *DB) ListSchedules(ctx context.Context, botID string) ([]domain.Schedule, error) {
	return db.querySchedules(ctx, `SELECT `+schedCols+` FROM schedules s LEFT JOIN users u ON u.id = s.owner_id
		WHERE s.bot_id = ? ORDER BY s.created_at_ms LIMIT 50`, botID)
}

// DueSchedules returns enabled schedules due at nowMS, oldest first, bounded.
func (db *DB) DueSchedules(ctx context.Context, nowMS int64, limit int) ([]domain.Schedule, error) {
	return db.querySchedules(ctx, `SELECT `+schedCols+` FROM schedules s LEFT JOIN users u ON u.id = s.owner_id
		WHERE s.enabled = 1 AND s.next_run_at_ms IS NOT NULL AND s.next_run_at_ms <= ? ORDER BY s.next_run_at_ms LIMIT ?`, nowMS, limit)
}

func (db *DB) querySchedules(ctx context.Context, q string, args ...any) ([]domain.Schedule, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, db.loadTasks(ctx, out)
}

// RecordScheduleRun stores the outcome of a run and the next due time; a nil
// next disables the schedule.
func (db *DB) RecordScheduleRun(ctx context.Context, id string, runMS int64, status, msg string, next *int64, disable bool) error {
	_, err := db.ExecContext(ctx, `UPDATE schedules SET last_run_at_ms = ?, last_status = ?, last_message = ?, next_run_at_ms = ?,
		enabled = CASE WHEN ? THEN 0 ELSE enabled END, updated_at_ms = ? WHERE id = ?`,
		runMS, status, nullStr(msg), next, boolInt(disable), runMS, id)
	return err
}

func (db *DB) DeleteSchedule(ctx context.Context, botID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ? AND bot_id = ?`, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// BotsWithBackupSchedule lists bots that have their own enabled backup
// schedule, so the panel-wide backup interval skips them (no double backups).
func (db *DB) BotsWithBackupSchedule(ctx context.Context) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT s.bot_id FROM schedules s WHERE s.enabled = 1 AND
		((s.chain = 0 AND s.action = 'backup') OR (s.chain = 1 AND EXISTS
			(SELECT 1 FROM schedule_tasks t WHERE t.schedule_id = s.id AND t.action = 'backup')))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
