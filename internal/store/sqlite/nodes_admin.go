package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// --- Locations ---

// ListLocations returns every location with its node count.
func (db *DB) ListLocations(ctx context.Context) ([]domain.Location, map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.id, l.name, l.description, l.created_at_ms, l.updated_at_ms,
		(SELECT count(*) FROM nodes n WHERE n.location_id = l.id) FROM locations l ORDER BY l.name`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []domain.Location
	counts := map[string]int{}
	for rows.Next() {
		var l domain.Location
		var n int
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.CreatedAtMS, &l.UpdatedAtMS, &n); err != nil {
			return nil, nil, err
		}
		counts[l.ID] = n
		out = append(out, l)
	}
	return out, counts, rows.Err()
}

// CreateLocation adds a location; names are unique (case-insensitive).
func (db *DB) CreateLocation(ctx context.Context, name, description string, nowMS int64) (domain.Location, error) {
	l := domain.Location{ID: uuid.NewString(), Name: name, Description: description, CreatedAtMS: nowMS, UpdatedAtMS: nowMS}
	_, err := db.ExecContext(ctx, `INSERT INTO locations (id, name, description, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, ?)`,
		l.ID, l.Name, l.Description, nowMS, nowMS)
	return l, mapErr(err)
}

// UpdateLocation renames or re-describes a location.
func (db *DB) UpdateLocation(ctx context.Context, id, name, description string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE locations SET name = ?, description = ?, updated_at_ms = ? WHERE id = ?`, name, description, nowMS, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteLocation removes an empty location other than the built-in one.
func (db *DB) DeleteLocation(ctx context.Context, id string) error {
	if id == domain.LocalLocationID {
		return domain.ErrConflict
	}
	var nodes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE location_id = ?`, id).Scan(&nodes); err != nil {
		return err
	}
	if nodes > 0 {
		return domain.ErrConflict
	}
	res, err := db.ExecContext(ctx, `DELETE FROM locations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- Nodes ---

// NodeUpdate is a partial node change; nil fields are unchanged.
type NodeUpdate struct {
	Name          *string
	LocationID    *string
	Enabled       *bool
	Draining      *bool
	PublicAddress *string
}

// UpdateNode applies a partial change.
func (db *DB) UpdateNode(ctx context.Context, id string, u NodeUpdate, nowMS int64) error {
	sets, args := []string{"updated_at_ms = ?"}, []any{nowMS}
	if u.Name != nil {
		sets, args = append(sets, "name = ?"), append(args, *u.Name)
	}
	if u.LocationID != nil {
		sets, args = append(sets, "location_id = ?"), append(args, *u.LocationID)
	}
	if u.Enabled != nil {
		sets, args = append(sets, "enabled = ?"), append(args, boolInt(*u.Enabled))
	}
	if u.Draining != nil {
		sets, args = append(sets, "draining = ?"), append(args, boolInt(*u.Draining))
	}
	if u.PublicAddress != nil {
		sets, args = append(sets, "public_address = ?"), append(args, *u.PublicAddress)
	}
	args = append(args, id)
	res, err := db.ExecContext(ctx, `UPDATE nodes SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return domain.Invalid("unknown location")
		}
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteAgentNode removes an agent node that owns no servers.
func (db *DB) DeleteAgentNode(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var transport string
	if err := tx.QueryRowContext(ctx, `SELECT transport FROM nodes WHERE id = ?`, id).Scan(&transport); err != nil {
		return mapErr(err)
	}
	if transport != "agent" {
		return domain.ErrConflict
	}
	var bots int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE node_id = ?`, id).Scan(&bots); err != nil {
		return err
	}
	if bots > 0 {
		return domain.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// CountNodeBots counts the servers placed on a node (not deleted).
func (db *DB) CountNodeBots(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE node_id = ? AND desired_state != 'deleted'`, nodeID).Scan(&n)
	return n, err
}

// --- Agent connection state ---

const agentStateCols = `node_id, protocol_version, capabilities_json, connected, certificate_serial, certificate_expires_at_ms,
	connected_at_ms, disconnected_at_ms, updated_at_ms, agent_version, hostname`

func scanAgentState(row interface{ Scan(...any) error }) (domain.AgentState, error) {
	var s domain.AgentState
	var conn int
	err := row.Scan(&s.NodeID, &s.ProtocolVersion, &s.CapabilitiesJSON, &conn, &s.CertificateSerial, &s.CertificateExpiresAtMS,
		&s.ConnectedAtMS, &s.DisconnectedAtMS, &s.UpdatedAtMS, &s.AgentVersion, &s.Hostname)
	s.Connected = conn == 1
	return s, mapErr(err)
}

// GetAgentState returns a node's last agent report.
func (db *DB) GetAgentState(ctx context.Context, nodeID string) (domain.AgentState, error) {
	return scanAgentState(db.QueryRowContext(ctx, `SELECT `+agentStateCols+` FROM node_agent_state WHERE node_id = ?`, nodeID))
}

// ListAgentStates returns every agent node's state.
func (db *DB) ListAgentStates(ctx context.Context) (map[string]domain.AgentState, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+agentStateCols+` FROM node_agent_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.AgentState{}
	for rows.Next() {
		s, err := scanAgentState(rows)
		if err != nil {
			return nil, err
		}
		out[s.NodeID] = s
	}
	return out, rows.Err()
}

// AgentConnected records a new authenticated connection and its report.
func (db *DB) AgentConnected(ctx context.Context, nodeID string, protocol int, version, hostname, capsJSON string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE node_agent_state SET connected = 1, protocol_version = ?, agent_version = ?, hostname = ?,
		capabilities_json = ?, connected_at_ms = ?, updated_at_ms = ? WHERE node_id = ?`,
		protocol, version, hostname, capsJSON, nowMS, nowMS, nodeID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE nodes SET last_seen_at_ms = ? WHERE id = ?`, nowMS, nodeID)
	return err
}

// AgentDisconnected records the end of a connection.
func (db *DB) AgentDisconnected(ctx context.Context, nodeID string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE node_agent_state SET connected = 0, disconnected_at_ms = ?, updated_at_ms = ? WHERE node_id = ?`,
		nowMS, nowMS, nodeID)
	return err
}

// ResetAgentConnections marks every agent disconnected (panel start).
func (db *DB) ResetAgentConnections(ctx context.Context, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE node_agent_state SET connected = 0, disconnected_at_ms = ? WHERE connected = 1`, nowMS)
	return err
}

// --- Certificates ---

// GetAgentCertificate returns one issued certificate.
func (db *DB) GetAgentCertificate(ctx context.Context, serial string) (domain.AgentCertificate, error) {
	var c domain.AgentCertificate
	err := db.QueryRowContext(ctx, `SELECT serial, node_id, issued_at_ms, expires_at_ms, revoked_at_ms, revoked_reason
		FROM agent_certificates WHERE serial = ?`, serial).Scan(&c.Serial, &c.NodeID, &c.IssuedAtMS, &c.ExpiresAtMS, &c.RevokedAtMS, &c.RevokedReason)
	return c, mapErr(err)
}

// ListAgentCertificates returns a node's certificates, newest first.
func (db *DB) ListAgentCertificates(ctx context.Context, nodeID string) ([]domain.AgentCertificate, error) {
	rows, err := db.QueryContext(ctx, `SELECT serial, node_id, issued_at_ms, expires_at_ms, revoked_at_ms, revoked_reason
		FROM agent_certificates WHERE node_id = ? ORDER BY issued_at_ms DESC LIMIT 50`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AgentCertificate
	for rows.Next() {
		var c domain.AgentCertificate
		if err := rows.Scan(&c.Serial, &c.NodeID, &c.IssuedAtMS, &c.ExpiresAtMS, &c.RevokedAtMS, &c.RevokedReason); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordRotatedCertificate stores a renewal for a node that presented a
// valid certificate, and makes it the node's current one.
func (db *DB) RecordRotatedCertificate(ctx context.Context, nodeID, serial string, expiresAtMS, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_certificates (serial, node_id, issued_at_ms, expires_at_ms) VALUES (?, ?, ?, ?)`,
		serial, nodeID, nowMS, expiresAtMS); err != nil {
		return mapErr(err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE node_agent_state SET certificate_serial = ?, certificate_expires_at_ms = ?, updated_at_ms = ?
		WHERE node_id = ?`, serial, expiresAtMS, nowMS, nodeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// RetireSupersededAgentCertificates runs after a node completed an mTLS
// handshake with serial. Every other unrevoked certificate of the node that
// was issued before it is revoked with reason "superseded", and serial
// becomes the node's current certificate. Certificates issued after serial
// are left alone: an agent that crashed between receiving a renewal and
// persisting it reconnects with its previous certificate and must not be
// stranded; the unused renewal is retired once a newer one is used.
// It returns how many certificates were revoked.
func (db *DB) RetireSupersededAgentCertificates(ctx context.Context, nodeID, serial string, nowMS int64) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Issue order is the row order (rowid), not issued_at_ms, so a wall-clock
	// step between issuing two certificates cannot reorder them.
	var order, expires int64
	var revoked *int64
	err = tx.QueryRowContext(ctx, `SELECT rowid, expires_at_ms, revoked_at_ms FROM agent_certificates
		WHERE serial = ? AND node_id = ?`, serial, nodeID).Scan(&order, &expires, &revoked)
	if err != nil {
		return 0, mapErr(err)
	}
	if revoked != nil {
		return 0, errors.New("certificate revoked")
	}
	res, err := tx.ExecContext(ctx, `UPDATE agent_certificates SET revoked_at_ms = ?, revoked_reason = 'superseded'
		WHERE node_id = ? AND serial <> ? AND revoked_at_ms IS NULL AND rowid < ?`, nowMS, nodeID, serial, order)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE node_agent_state SET certificate_serial = ?, certificate_expires_at_ms = ?
		WHERE node_id = ? AND (certificate_serial IS NULL OR certificate_serial <> ?)`, serial, expires, nodeID, serial); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// RevokeAgentCertificates revokes every unrevoked certificate of a node (the
// connection is refused until it enrolls again) and clears the current one
// so a fresh enrollment token can be issued.
func (db *DB) RevokeAgentCertificates(ctx context.Context, nodeID, reason string, nowMS int64) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE agent_certificates SET revoked_at_ms = ?, revoked_reason = ?
		WHERE node_id = ? AND revoked_at_ms IS NULL`, nowMS, reason, nodeID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE node_agent_state SET certificate_serial = NULL, certificate_expires_at_ms = NULL,
		updated_at_ms = ? WHERE node_id = ?`, nowMS, nodeID); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// AuthorizeAgentCertificate reports whether serial is a valid, unrevoked,
// unexpired certificate of nodeID on an enabled agent node.
func (db *DB) AuthorizeAgentCertificate(ctx context.Context, nodeID, serial string, nowMS int64) error {
	var certNode, transport string
	var expires int64
	var revoked *int64
	var enabled int
	err := db.QueryRowContext(ctx, `SELECT c.node_id, c.expires_at_ms, c.revoked_at_ms, n.transport, n.enabled
		FROM agent_certificates c JOIN nodes n ON n.id = c.node_id WHERE c.serial = ?`, serial).
		Scan(&certNode, &expires, &revoked, &transport, &enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errors.New("unknown certificate")
	case err != nil:
		return err
	case certNode != nodeID:
		return errors.New("certificate belongs to another node")
	case revoked != nil:
		return errors.New("certificate revoked")
	case expires <= nowMS:
		return errors.New("certificate expired")
	case transport != "agent":
		return errors.New("node is not an agent node")
	case enabled != 1:
		return errors.New("node is disabled")
	}
	return nil
}

// ListBotIDsByNode returns the ids of a node's servers (agent resync).
func (db *DB) ListBotIDsByNode(ctx context.Context, nodeID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM bots WHERE node_id = ?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
