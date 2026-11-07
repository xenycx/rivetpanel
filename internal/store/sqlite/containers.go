package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func jsonList[T any](v []T) (string, error) {
	if v == nil {
		v = []T{}
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// GetContainerConfig loads a container app's image settings.
func (db *DB) GetContainerConfig(ctx context.Context, botID string) (domain.ContainerConfig, error) {
	var c domain.ContainerConfig
	var writable, volumes, depends string
	var stack sql.NullString
	err := db.QueryRowContext(ctx, `SELECT run_user, writable_json, volumes_json, stack_id, service, depends_json
		FROM bot_containers WHERE bot_id = ?`, botID).Scan(&c.User, &writable, &volumes, &stack, &c.Service, &depends)
	if err != nil {
		return c, mapErr(err)
	}
	c.StackID = stack.String
	if err := json.Unmarshal([]byte(writable), &c.Writable); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(volumes), &c.Volumes); err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(depends), &c.DependsOn)
}

// PutContainerConfig stores a container app's image settings, replacing any.
func (db *DB) PutContainerConfig(ctx context.Context, botID string, c domain.ContainerConfig) error {
	return putContainerConfig(ctx, db, botID, c)
}

func putContainerConfig(ctx context.Context, db execer, botID string, c domain.ContainerConfig) error {
	writable, err := jsonList(c.Writable)
	if err != nil {
		return err
	}
	volumes, err := jsonList(c.Volumes)
	if err != nil {
		return err
	}
	depends, err := jsonList(c.DependsOn)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bot_containers (bot_id, run_user, writable_json, volumes_json, stack_id, service, depends_json)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT (bot_id) DO UPDATE SET run_user = excluded.run_user, writable_json = excluded.writable_json,
			volumes_json = excluded.volumes_json, stack_id = excluded.stack_id, service = excluded.service,
			depends_json = excluded.depends_json`,
		botID, c.User, writable, volumes, c.StackID, c.Service, depends)
	return mapErr(err)
}

// SetContainerImage changes a container app's image and settings and starts a
// new generation, so a running container is replaced with the new image.
func (db *DB) SetContainerImage(ctx context.Context, botID, image string, c domain.ContainerConfig, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE bots SET image_ref = ?, generation = generation + 1, updated_at_ms = ?
		WHERE id = ? AND kind = 'container' AND desired_state != 'deleted'`, image, nowMS, botID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	if err := putContainerConfig(ctx, tx, botID, c); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateStack records a stack.
func (db *DB) CreateStack(ctx context.Context, s domain.Stack) error {
	_, err := db.ExecContext(ctx, `INSERT INTO stacks (id, workspace_id, owner_id, name, compose_yaml, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, s.ID, s.WorkspaceID, s.OwnerID, s.Name, s.ComposeYAML, s.CreatedAtMS, s.UpdatedAtMS)
	return mapErr(err)
}

const stackCols = `id, workspace_id, owner_id, name, compose_yaml, created_at_ms, updated_at_ms`

func scanStack(row interface{ Scan(...any) error }) (domain.Stack, error) {
	var s domain.Stack
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.OwnerID, &s.Name, &s.ComposeYAML, &s.CreatedAtMS, &s.UpdatedAtMS)
	return s, mapErr(err)
}

// GetStack loads one stack.
func (db *DB) GetStack(ctx context.Context, id string) (domain.Stack, error) {
	return scanStack(db.QueryRowContext(ctx, `SELECT `+stackCols+` FROM stacks WHERE id = ?`, id))
}

// ListStacks returns every stack, oldest first.
func (db *DB) ListStacks(ctx context.Context) ([]domain.Stack, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+stackCols+` FROM stacks ORDER BY created_at_ms, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Stack
	for rows.Next() {
		s, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// StackMembers returns the ids of a stack's container apps that are not
// being deleted, in service order.
func (db *DB) StackMembers(ctx context.Context, stackID string) ([]string, error) {
	return db.strings(ctx, `SELECT c.bot_id FROM bot_containers c JOIN bots b ON b.id = c.bot_id
		WHERE c.stack_id = ? AND b.desired_state != 'deleted' ORDER BY c.service`, stackID)
}

// StackServiceStates maps the services of a stack to their bots' observed
// state ("" for a service that does not exist or is being deleted).
func (db *DB) StackServiceStates(ctx context.Context, stackID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.service, b.observed_state FROM bot_containers c JOIN bots b ON b.id = c.bot_id
		WHERE c.stack_id = ? AND b.desired_state != 'deleted'`, stackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var svc, st string
		if err := rows.Scan(&svc, &st); err != nil {
			return nil, err
		}
		out[svc] = st
	}
	return out, rows.Err()
}

// RenameStack changes a stack's name.
func (db *DB) RenameStack(ctx context.Context, id, name string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE stacks SET name = ?, updated_at_ms = ? WHERE id = ?`, name, nowMS, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteStack removes a stack row once it has no live services.
func (db *DB) DeleteStack(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bot_containers c JOIN bots b ON b.id = c.bot_id
		WHERE c.stack_id = ? AND b.desired_state != 'deleted'`, id).Scan(&live); err != nil {
		return mapErr(err)
	}
	if live > 0 {
		return domain.Invalid("delete the stack's services first")
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM stacks WHERE id = ?`, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// loadContainer fills b.Container for a container app.
func (db *DB) loadContainer(ctx context.Context, b *domain.Bot) error {
	if !b.IsContainer() {
		return nil
	}
	c, err := db.GetContainerConfig(ctx, b.ID)
	if errors.Is(err, domain.ErrNotFound) {
		c = domain.ContainerConfig{} // created before its settings were stored
	} else if err != nil {
		return err
	}
	b.Container = &c
	return nil
}

// UsedBotHostPorts returns the host ports published by apps that are not
// being deleted (any address, any protocol).
func (db *DB) UsedBotHostPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.host_port FROM bot_ports p JOIN bots b ON b.id = p.bot_id
		WHERE b.desired_state != 'deleted'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// fillContainers loads the image settings of the container apps in a list
// with one query.
func (db *DB) fillContainers(ctx context.Context, bots []domain.Bot) error {
	idx := map[string]int{}
	for i, b := range bots {
		if b.IsContainer() {
			idx[b.ID] = i
			bots[i].Container = &domain.ContainerConfig{}
		}
	}
	if len(idx) == 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT c.bot_id, c.run_user, c.writable_json, c.volumes_json, c.stack_id, c.service, c.depends_json
		FROM bot_containers c JOIN bots b ON b.id = c.bot_id WHERE b.kind = 'container'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, writable, volumes, depends string
		var c domain.ContainerConfig
		var stack sql.NullString
		if err := rows.Scan(&id, &c.User, &writable, &volumes, &stack, &c.Service, &depends); err != nil {
			return err
		}
		i, ok := idx[id]
		if !ok {
			continue
		}
		c.StackID = stack.String
		if json.Unmarshal([]byte(writable), &c.Writable) != nil || json.Unmarshal([]byte(volumes), &c.Volumes) != nil ||
			json.Unmarshal([]byte(depends), &c.DependsOn) != nil {
			continue
		}
		bots[i].Container = &c
	}
	return rows.Err()
}
