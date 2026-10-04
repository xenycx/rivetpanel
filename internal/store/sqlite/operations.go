package sqlite

import (
	"context"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// maxOpsPerBot bounds the retained history per bot.
const maxOpsPerBot = 200

const opCols = `o.id, o.bot_id, b.name, o.kind, o.trigger, o.actor_id, u.email, o.status, o.stage, o.source_ref, o.source_label,
	o.generation, o.result_code, o.message, o.detail_json, o.log_bytes, o.created_at_ms, o.started_at_ms, o.finished_at_ms`

const opFrom = ` FROM operations o JOIN bots b ON b.id = o.bot_id LEFT JOIN users u ON u.id = o.actor_id`

func scanOp(row interface{ Scan(...any) error }) (domain.Operation, error) {
	var o domain.Operation
	err := row.Scan(&o.ID, &o.BotID, &o.BotName, &o.Kind, &o.Trigger, &o.ActorID, &o.ActorEmail, &o.Status, &o.Stage,
		&o.SourceRef, &o.SourceLabel, &o.Generation, &o.ResultCode, &o.Message, &o.DetailJSON, &o.LogBytes,
		&o.CreatedAtMS, &o.StartedAtMS, &o.FinishedAt)
	return o, mapErr(err)
}

// InsertOperation records a new operation and prunes the bot's oldest
// finished ones beyond the retention bound. It returns the IDs pruned so the
// caller can delete their log files.
func (db *DB) InsertOperation(ctx context.Context, o domain.Operation) ([]string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO operations (id, bot_id, kind, trigger, actor_id, status, stage, source_ref,
		source_label, generation, created_at_ms, started_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.BotID, o.Kind, o.Trigger, o.ActorID, o.Status, o.Stage, o.SourceRef, o.SourceLabel, o.Generation,
		o.CreatedAtMS, o.StartedAtMS); err != nil {
		return nil, mapErr(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM operations WHERE bot_id = ? AND status NOT IN ('queued','running')
		ORDER BY created_at_ms DESC LIMIT -1 OFFSET ?`, o.BotID, maxOpsPerBot)
	if err != nil {
		return nil, err
	}
	var old []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	rows.Close()
	for _, id := range old {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operations WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	return old, tx.Commit()
}

// SetOperationStage moves an active operation to a stage (and to running).
func (db *DB) SetOperationStage(ctx context.Context, id, stage string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE operations SET stage = ?, status = 'running',
		started_at_ms = COALESCE(started_at_ms, ?) WHERE id = ? AND status IN ('queued','running')`, stage, nowMS, id)
	return err
}

// SetOperationSource records what an operation works on once it is known
// (e.g. the commit a deployment resolved).
func (db *DB) SetOperationSource(ctx context.Context, id string, ref, label *string) error {
	_, err := db.ExecContext(ctx, `UPDATE operations SET source_ref = COALESCE(?, source_ref),
		source_label = COALESCE(?, source_label) WHERE id = ?`, ref, label, id)
	return err
}

// FinishOperation records the final outcome of an active operation. A
// finished operation is never changed again.
func (db *DB) FinishOperation(ctx context.Context, id, status string, code, msg, detail *string, logBytes, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE operations SET status = ?, result_code = ?, message = ?, detail_json = COALESCE(?, detail_json),
		log_bytes = ?, finished_at_ms = ?, started_at_ms = COALESCE(started_at_ms, ?)
		WHERE id = ? AND status IN ('queued','running')`, status, code, truncPtr(msg, 2000), detail, logBytes, nowMS, nowMS, id)
	return err
}

func truncPtr(s *string, n int) *string {
	if s == nil || len(*s) <= n {
		return s
	}
	t := (*s)[:n]
	return &t
}

func (db *DB) GetOperation(ctx context.Context, id string) (domain.Operation, error) {
	return scanOp(db.QueryRowContext(ctx, `SELECT `+opCols+opFrom+` WHERE o.id = ?`, id))
}

// OperationFilter selects operations. UserID limits results to bots the user
// owns or has been granted (empty = no restriction, for administrators).
type OperationFilter struct {
	BotID       string
	UserID      string
	WorkspaceID string // bots in this workspace (administrator views)
	OwnerID     string // bots owned by this account (administrator views)
	Kinds       []string
	Active      bool
	BeforeMS    int64 // cursor: created_at_ms strictly before this
	Limit       int
}

func (db *DB) ListOperations(ctx context.Context, f OperationFilter) ([]domain.Operation, error) {
	var where []string
	var args []any
	if f.BotID != "" {
		where, args = append(where, "o.bot_id = ?"), append(args, f.BotID)
	}
	if f.UserID != "" {
		where = append(where, `(b.owner_id = ? OR o.bot_id IN (SELECT bot_id FROM bot_subusers WHERE user_id = ?)
			OR b.workspace_id IN (SELECT workspace_id FROM workspace_members WHERE user_id = ?))`)
		args = append(args, f.UserID, f.UserID, f.UserID)
	}
	if f.WorkspaceID != "" {
		where, args = append(where, "b.workspace_id = ?"), append(args, f.WorkspaceID)
	}
	if f.OwnerID != "" {
		where, args = append(where, "b.owner_id = ?"), append(args, f.OwnerID)
	}
	if len(f.Kinds) > 0 {
		where = append(where, "o.kind IN (?"+strings.Repeat(",?", len(f.Kinds)-1)+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Active {
		where = append(where, "o.status IN ('queued','running')")
	}
	if f.BeforeMS > 0 {
		where, args = append(where, "o.created_at_ms < ?"), append(args, f.BeforeMS)
	}
	where = append(where, "b.desired_state != 'deleted'")
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := `SELECT ` + opCols + opFrom + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY o.created_at_ms DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Operation
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// InterruptActiveOperations marks work that was active when the panel
// stopped as interrupted, and returns those operations for recovery.
func (db *DB) InterruptActiveOperations(ctx context.Context, nowMS int64) ([]domain.Operation, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+opCols+opFrom+` WHERE o.status IN ('queued','running')`)
	if err != nil {
		return nil, err
	}
	var ops []domain.Operation
	for rows.Next() {
		o, err := scanOp(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		ops = append(ops, o)
	}
	rows.Close()
	msg := "the panel restarted while this was running"
	code := "interrupted"
	for _, o := range ops {
		if err := db.FinishOperation(ctx, o.ID, domain.OpInterrupted, &code, &msg, nil, o.LogBytes, nowMS); err != nil {
			return nil, err
		}
	}
	return ops, nil
}

// OperationIDs returns every retained operation ID (for log-file sweeps).
func (db *DB) OperationIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM operations`)
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
