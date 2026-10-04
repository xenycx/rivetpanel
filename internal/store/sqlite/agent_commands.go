package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const agentCommandCols = `id, node_id, operation_id, server_id, generation, kind,
	payload_json, idempotency_key, status, deadline_at_ms, created_at_ms,
	delivered_at_ms, completed_at_ms, error`

func scanAgentCommand(row interface{ Scan(...any) error }) (domain.AgentCommand, error) {
	var c domain.AgentCommand
	err := row.Scan(&c.ID, &c.NodeID, &c.OperationID, &c.ServerID, &c.Generation, &c.Kind,
		&c.PayloadJSON, &c.IdempotencyKey, &c.Status, &c.DeadlineAtMS, &c.CreatedAtMS,
		&c.DeliveredAtMS, &c.CompletedAtMS, &c.Error)
	return c, mapErr(err)
}

// QueueAgentCommand inserts a command once per node/idempotency key. Repeated
// calls return the original row so a retried control-plane operation cannot
// execute twice on an agent.
func (db *DB) QueueAgentCommand(ctx context.Context, c domain.AgentCommand) (domain.AgentCommand, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AgentCommand{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO agent_commands
		(id, node_id, operation_id, server_id, generation, kind, payload_json,
		 idempotency_key, status, deadline_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?)
		ON CONFLICT(node_id, idempotency_key) DO NOTHING`,
		c.ID, c.NodeID, c.OperationID, c.ServerID, c.Generation, c.Kind, c.PayloadJSON,
		c.IdempotencyKey, c.DeadlineAtMS, c.CreatedAtMS)
	if err != nil {
		return domain.AgentCommand{}, false, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.AgentCommand{}, false, err
	}
	got, err := scanAgentCommand(tx.QueryRowContext(ctx, `SELECT `+agentCommandCols+`
		FROM agent_commands WHERE node_id = ? AND idempotency_key = ?`, c.NodeID, c.IdempotencyKey))
	if err != nil {
		return domain.AgentCommand{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AgentCommand{}, false, err
	}
	return got, n == 1, nil
}

// ListPendingAgentCommands returns queued commands and delivered commands
// whose acknowledgement lease elapsed. Expired commands are finalized first.
func (db *DB) ListPendingAgentCommands(ctx context.Context, nodeID string, nowMS, retryBeforeMS int64, limit int) ([]domain.AgentCommand, error) {
	if limit < 1 || limit > 256 {
		limit = 64
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_commands
		SET status = 'expired', completed_at_ms = ?, error = 'deadline exceeded'
		WHERE node_id = ? AND status IN ('queued', 'delivered') AND deadline_at_ms <= ?`, nowMS, nodeID, nowMS); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+agentCommandCols+` FROM agent_commands
		WHERE node_id = ? AND deadline_at_ms > ?
		  AND (status = 'queued' OR (status = 'delivered' AND delivered_at_ms <= ?))
		ORDER BY created_at_ms, id LIMIT ?`, nodeID, nowMS, retryBeforeMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AgentCommand{}
	for rows.Next() {
		c, err := scanAgentCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkAgentCommandDelivered starts or renews the acknowledgement lease.
func (db *DB) MarkAgentCommandDelivered(ctx context.Context, nodeID, id string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE agent_commands SET status = 'delivered', delivered_at_ms = ?
		WHERE id = ? AND node_id = ? AND status IN ('queued', 'delivered') AND deadline_at_ms > ?`, nowMS, id, nodeID, nowMS)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		err := db.QueryRowContext(ctx, `SELECT status FROM agent_commands WHERE id = ? AND node_id = ?`, id, nodeID).Scan(&status)
		if err == sql.ErrNoRows {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == "succeeded" || status == "failed" || status == "expired" {
			return nil
		}
		return fmt.Errorf("%w: command can no longer be delivered", domain.ErrConflict)
	}
	return nil
}

// CompleteAgentCommand records an idempotent terminal acknowledgement.
func (db *DB) CompleteAgentCommand(ctx context.Context, nodeID, id string, succeeded bool, message string, nowMS int64) error {
	status := "failed"
	var safeError *string
	if succeeded {
		status = "succeeded"
	} else {
		if len(message) > 500 {
			message = message[:500]
		}
		safeError = &message
	}
	res, err := db.ExecContext(ctx, `UPDATE agent_commands SET status = ?, completed_at_ms = ?, error = ?
		WHERE id = ? AND node_id = ? AND status IN ('queued', 'delivered')`, status, nowMS, safeError, id, nodeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 0 {
		return nil
	}
	var current string
	if err := db.QueryRowContext(ctx, `SELECT status FROM agent_commands WHERE id = ? AND node_id = ?`, id, nodeID).Scan(&current); err != nil {
		return mapErr(err)
	}
	if current == status {
		return nil
	}
	return fmt.Errorf("%w: command already completed with a different result", domain.ErrConflict)
}
