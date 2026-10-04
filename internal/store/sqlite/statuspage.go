package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Status page persistence (migration 0051).

// ListStatusComponents returns every component in display order.
func (db *DB) ListStatusComponents(ctx context.Context) ([]domain.StatusComponent, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, kind, ref_id, name, description, position, created_at_ms, updated_at_ms
		FROM status_components ORDER BY position, lower(name), id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StatusComponent
	for rows.Next() {
		var c domain.StatusComponent
		if err := rows.Scan(&c.ID, &c.Kind, &c.RefID, &c.Name, &c.Description, &c.Position, &c.CreatedAtMS, &c.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetStatusComponent returns one component.
func (db *DB) GetStatusComponent(ctx context.Context, id string) (domain.StatusComponent, error) {
	var c domain.StatusComponent
	err := db.QueryRowContext(ctx, `SELECT id, kind, ref_id, name, description, position, created_at_ms, updated_at_ms
		FROM status_components WHERE id = ?`, id).Scan(&c.ID, &c.Kind, &c.RefID, &c.Name, &c.Description, &c.Position, &c.CreatedAtMS, &c.UpdatedAtMS)
	return c, mapErr(err)
}

// PutStatusComponent inserts or updates a component (the source cannot
// change; a source already shown is ErrConflict).
func (db *DB) PutStatusComponent(ctx context.Context, c domain.StatusComponent) error {
	_, err := db.ExecContext(ctx, `INSERT INTO status_components (id, kind, ref_id, name, description, position, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name = excluded.name, description = excluded.description,
		position = excluded.position, updated_at_ms = excluded.updated_at_ms`,
		c.ID, c.Kind, c.RefID, c.Name, c.Description, c.Position, c.CreatedAtMS, c.UpdatedAtMS)
	return mapErr(err)
}

// DeleteStatusComponent removes a component, its samples and its incident
// links.
func (db *DB) DeleteStatusComponent(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM status_components WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const incidentCols = `id, kind, title, impact, status, starts_at_ms, ends_at_ms, created_by, created_at_ms, updated_at_ms, resolved_at_ms`

func scanIncident(row interface{ Scan(...any) error }) (domain.StatusIncident, error) {
	var i domain.StatusIncident
	err := row.Scan(&i.ID, &i.Kind, &i.Title, &i.Impact, &i.Status, &i.StartsAtMS, &i.EndsAtMS, &i.CreatedBy, &i.CreatedAtMS, &i.UpdatedAtMS, &i.ResolvedAtMS)
	return i, mapErr(err)
}

// fillIncidents loads the component ids and the timeline of incidents.
func (db *DB) fillIncidents(ctx context.Context, incs []domain.StatusIncident) error {
	for k := range incs {
		i := &incs[k]
		rows, err := db.QueryContext(ctx, `SELECT component_id FROM status_incident_components WHERE incident_id = ? ORDER BY component_id`, i.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			i.ComponentIDs = append(i.ComponentIDs, id)
		}
		rows.Close()
		ur, err := db.QueryContext(ctx, `SELECT id, incident_id, status, body, author_id, created_at_ms FROM status_incident_updates
			WHERE incident_id = ? ORDER BY created_at_ms DESC, rowid DESC LIMIT 200`, i.ID)
		if err != nil {
			return err
		}
		for ur.Next() {
			var u domain.StatusIncidentUpdate
			if err := ur.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Body, &u.AuthorID, &u.CreatedAtMS); err != nil {
				ur.Close()
				return err
			}
			i.Updates = append(i.Updates, u)
		}
		ur.Close()
	}
	return nil
}

// ListStatusIncidents returns open incidents and maintenance, plus those
// closed at or after closedSince, newest first (at most limit).
func (db *DB) ListStatusIncidents(ctx context.Context, closedSince int64, limit int) ([]domain.StatusIncident, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := db.QueryContext(ctx, `SELECT `+incidentCols+` FROM status_incidents
		WHERE resolved_at_ms IS NULL OR resolved_at_ms >= ? ORDER BY created_at_ms DESC, id LIMIT ?`, closedSince, limit)
	if err != nil {
		return nil, err
	}
	var out []domain.StatusIncident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, db.fillIncidents(ctx, out)
}

// GetStatusIncident returns one incident with its components and timeline.
func (db *DB) GetStatusIncident(ctx context.Context, id string) (domain.StatusIncident, error) {
	i, err := scanIncident(db.QueryRowContext(ctx, `SELECT `+incidentCols+` FROM status_incidents WHERE id = ?`, id))
	if err != nil {
		return i, err
	}
	list := []domain.StatusIncident{i}
	err = db.fillIncidents(ctx, list)
	return list[0], err
}

// SaveStatusIncident inserts or updates an incident, replaces its component
// links and appends upd (if any) in one transaction.
func (db *DB) SaveStatusIncident(ctx context.Context, i domain.StatusIncident, upd *domain.StatusIncidentUpdate) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_incidents (`+incidentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET title = excluded.title, impact = excluded.impact, status = excluded.status,
			starts_at_ms = excluded.starts_at_ms, ends_at_ms = excluded.ends_at_ms, updated_at_ms = excluded.updated_at_ms,
			resolved_at_ms = excluded.resolved_at_ms`,
			i.ID, i.Kind, i.Title, i.Impact, i.Status, i.StartsAtMS, i.EndsAtMS, i.CreatedBy, i.CreatedAtMS, i.UpdatedAtMS, i.ResolvedAtMS); err != nil {
			return mapErr(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM status_incident_components WHERE incident_id = ?`, i.ID); err != nil {
			return err
		}
		for _, c := range i.ComponentIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO status_incident_components (incident_id, component_id) VALUES (?,?)`, i.ID, c); err != nil {
				return mapErr(err)
			}
		}
		if upd != nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO status_incident_updates (id, incident_id, status, body, author_id, created_at_ms)
				VALUES (?,?,?,?,?,?)`, upd.ID, i.ID, upd.Status, upd.Body, upd.AuthorID, upd.CreatedAtMS); err != nil {
				return mapErr(err)
			}
		}
		return nil
	})
}

// DeleteStatusIncident removes an incident and its timeline.
func (db *DB) DeleteStatusIncident(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM status_incidents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AddStatusSamples counts one sample per component on day. States other
// than operational, degraded, outages and maintenance (unknown) are skipped.
func (db *DB) AddStatusSamples(ctx context.Context, day int64, states map[string]string) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		for id, st := range states {
			col := ""
			switch st {
			case domain.StateOperational:
				col = "operational"
			case domain.StateDegraded:
				col = "degraded"
			case domain.StatePartial, domain.StateMajor:
				col = "outage"
			case domain.StateMaintenance:
				col = "maintenance"
			default:
				continue
			}
			// col is one of the fixed names above.
			if _, err := tx.ExecContext(ctx, `INSERT INTO status_samples (component_id, day, `+col+`) VALUES (?,?,1)
				ON CONFLICT(component_id, day) DO UPDATE SET `+col+` = `+col+` + 1`, id, day); err != nil {
				if strings.Contains(err.Error(), "FOREIGN KEY") {
					continue // the component was deleted meanwhile
				}
				return err
			}
		}
		return nil
	})
}

// StatusDays returns the sample counts from sinceDay on.
func (db *DB) StatusDays(ctx context.Context, sinceDay int64) ([]domain.StatusDay, error) {
	rows, err := db.QueryContext(ctx, `SELECT component_id, day, operational, degraded, outage, maintenance
		FROM status_samples WHERE day >= ? ORDER BY component_id, day LIMIT 100000`, sinceDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StatusDay
	for rows.Next() {
		var d domain.StatusDay
		if err := rows.Scan(&d.ComponentID, &d.Day, &d.Operational, &d.Degraded, &d.Outage, &d.Maintenance); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PruneStatusSamples deletes counts of days before beforeDay.
func (db *DB) PruneStatusSamples(ctx context.Context, beforeDay int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM status_samples WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
