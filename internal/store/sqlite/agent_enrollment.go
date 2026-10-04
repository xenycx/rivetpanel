package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const agentEnrollmentCols = `t.id, t.node_id, n.name, n.location_id, t.prefix,
	t.token_hash, t.created_at_ms, t.expires_at_ms, t.used_at_ms, t.revoked_at_ms`

func scanAgentEnrollment(row interface{ Scan(...any) error }) (domain.AgentEnrollment, error) {
	var e domain.AgentEnrollment
	err := row.Scan(&e.ID, &e.NodeID, &e.NodeName, &e.LocationID, &e.Prefix,
		&e.TokenHash, &e.CreatedAtMS, &e.ExpiresAtMS, &e.UsedAtMS, &e.RevokedAtMS)
	return e, mapErr(err)
}

// CreateAgentEnrollment creates the agent node and its single active
// enrollment credential atomically. A node is not visible without the token
// needed to enroll it.
func (db *DB) CreateAgentEnrollment(ctx context.Context, e domain.AgentEnrollment) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO nodes
		(id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, 'agent', NULL, 1, ?, ?)`,
		e.NodeID, e.LocationID, e.NodeName, e.CreatedAtMS, e.CreatedAtMS); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO node_agent_state
		(node_id, protocol_version, capabilities_json, connected, updated_at_ms)
		VALUES (?, 1, '{}', 0, ?)`, e.NodeID, e.CreatedAtMS); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_enrollment_tokens
		(id, node_id, prefix, token_hash, created_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.NodeID, e.Prefix, e.TokenHash,
		e.CreatedAtMS, e.ExpiresAtMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

// ListAgentEnrollments returns newest first without exposing token hashes.
func (db *DB) ListAgentEnrollments(ctx context.Context) ([]domain.AgentEnrollment, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+agentEnrollmentCols+`
		FROM agent_enrollment_tokens t JOIN nodes n ON n.id = t.node_id
		ORDER BY t.created_at_ms DESC, t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AgentEnrollment
	for rows.Next() {
		e, err := scanAgentEnrollment(rows)
		if err != nil {
			return nil, err
		}
		e.TokenHash = nil
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnrollAgent consumes an active token, signs the node certificate and records
// its metadata in one write transaction. If signing or any write fails, the
// transaction rolls back and the token stays usable; a certificate whose
// metadata was not committed is never returned. Concurrent requests cannot
// both obtain the node identity because the write lock is taken up front.
func (db *DB) EnrollAgent(ctx context.Context, hash []byte, nowMS int64,
	issue func(nodeID string) (serial string, expiresAtMS int64, err error)) (domain.AgentEnrollment, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AgentEnrollment{}, err
	}
	defer tx.Rollback()
	e, err := scanAgentEnrollment(tx.QueryRowContext(ctx, `UPDATE agent_enrollment_tokens
		SET used_at_ms = ?
		WHERE token_hash = ? AND used_at_ms IS NULL AND revoked_at_ms IS NULL AND expires_at_ms > ?
		RETURNING id, node_id,
			(SELECT name FROM nodes WHERE nodes.id = agent_enrollment_tokens.node_id),
			(SELECT location_id FROM nodes WHERE nodes.id = agent_enrollment_tokens.node_id),
			prefix, token_hash, created_at_ms, expires_at_ms, used_at_ms, revoked_at_ms`,
		nowMS, hash, nowMS))
	if err != nil {
		return domain.AgentEnrollment{}, err
	}
	serial, expiresAtMS, err := issue(e.NodeID)
	if err != nil {
		return domain.AgentEnrollment{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_certificates (serial, node_id, issued_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?)`, serial, e.NodeID, nowMS, expiresAtMS); err != nil {
		return domain.AgentEnrollment{}, mapErr(err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE node_agent_state
		SET certificate_serial = ?, certificate_expires_at_ms = ?, updated_at_ms = ?
		WHERE node_id = ? AND certificate_serial IS NULL`, serial, expiresAtMS, nowMS, e.NodeID)
	if err != nil {
		return domain.AgentEnrollment{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.AgentEnrollment{}, domain.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return domain.AgentEnrollment{}, err
	}
	e.TokenHash = nil
	return e, nil
}

// unenrolledAgentNode reports whether nodeID is an agent node that never
// completed enrollment. Enrolled nodes need certificate lifecycle operations,
// not enrollment recovery.
func unenrolledAgentNode(ctx context.Context, tx *sql.Tx, nodeID string) error {
	var transport string
	var serial *string
	err := tx.QueryRowContext(ctx, `SELECT n.transport, s.certificate_serial
		FROM nodes n LEFT JOIN node_agent_state s ON s.node_id = n.id
		WHERE n.id = ?`, nodeID).Scan(&transport, &serial)
	if err != nil {
		return mapErr(err)
	}
	if transport != "agent" {
		return domain.ErrNotFound
	}
	if serial != nil {
		return domain.ErrConflict
	}
	return nil
}

// ReissueAgentEnrollment atomically replaces any previous token of a node that
// does not currently hold a certificate. An enrolled node is a conflict.
func (db *DB) ReissueAgentEnrollment(ctx context.Context, e domain.AgentEnrollment) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unenrolledAgentNode(ctx, tx, e.NodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_enrollment_tokens WHERE node_id = ?`, e.NodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_enrollment_tokens
		(id, node_id, prefix, token_hash, created_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.NodeID, e.Prefix, e.TokenHash,
		e.CreatedAtMS, e.ExpiresAtMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

// DeleteUnenrolledAgentNode removes a placeholder agent node, its token and
// state. Enrolled nodes and nodes that own workloads are conflicts.
func (db *DB) DeleteUnenrolledAgentNode(ctx context.Context, nodeID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unenrolledAgentNode(ctx, tx, nodeID); err != nil {
		return err
	}
	var bots int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE node_id = ?`, nodeID).Scan(&bots); err != nil {
		return err
	}
	if bots != 0 {
		return domain.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeAgentEnrollment invalidates one unused token. Revoking an already used
// token is a conflict because certificate revocation is a separate operation.
func (db *DB) RevokeAgentEnrollment(ctx context.Context, id string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE agent_enrollment_tokens SET revoked_at_ms = ?
		WHERE id = ? AND used_at_ms IS NULL AND revoked_at_ms IS NULL`, nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 0 {
		return nil
	}
	var used, revoked *int64
	if err := db.QueryRowContext(ctx, `SELECT used_at_ms, revoked_at_ms
		FROM agent_enrollment_tokens WHERE id = ?`, id).Scan(&used, &revoked); err != nil {
		if err == sql.ErrNoRows {
			return domain.ErrNotFound
		}
		return err
	}
	if revoked != nil {
		return nil
	}
	return domain.ErrConflict
}
