package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// --- Blueprints ---

const blueprintCols = `b.id, b.slug, b.name, b.category, b.description, b.source, b.current_revision, b.enabled,
	b.created_at_ms, b.updated_at_ms, (SELECT count(*) FROM bots WHERE bots.blueprint_id = b.id AND bots.desired_state != 'deleted')`

func scanBlueprint(row interface{ Scan(...any) error }) (domain.Blueprint, error) {
	var b domain.Blueprint
	var enabled int
	err := row.Scan(&b.ID, &b.Slug, &b.Name, &b.Category, &b.Description, &b.Source, &b.CurrentRevision, &enabled,
		&b.CreatedAtMS, &b.UpdatedAtMS, &b.Servers)
	b.Enabled = enabled == 1
	return b, mapErr(err)
}

// ListBlueprints returns every blueprint, by category then name.
func (db *DB) ListBlueprints(ctx context.Context) ([]domain.Blueprint, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+blueprintCols+` FROM blueprints b ORDER BY b.category, b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Blueprint
	for rows.Next() {
		b, err := scanBlueprint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBlueprint finds a blueprint by id or slug.
func (db *DB) GetBlueprint(ctx context.Context, idOrSlug string) (domain.Blueprint, error) {
	return scanBlueprint(db.QueryRowContext(ctx, `SELECT `+blueprintCols+` FROM blueprints b WHERE b.id = ? OR b.slug = ?`, idOrSlug, idOrSlug))
}

// GetBlueprintRevision loads one immutable revision.
func (db *DB) GetBlueprintRevision(ctx context.Context, id string, rev int64) (domain.BlueprintRevision, error) {
	var r domain.BlueprintRevision
	err := db.QueryRowContext(ctx, `SELECT blueprint_id, revision, spec_yaml, spec_sha256, created_at_ms
		FROM blueprint_revisions WHERE blueprint_id = ? AND revision = ?`, id, rev).
		Scan(&r.BlueprintID, &r.Revision, &r.SpecYAML, &r.SHA256, &r.CreatedAtMS)
	return r, mapErr(err)
}

// ListBlueprintRevisions returns a blueprint's revisions, newest first.
func (db *DB) ListBlueprintRevisions(ctx context.Context, id string) ([]domain.BlueprintRevision, error) {
	rows, err := db.QueryContext(ctx, `SELECT blueprint_id, revision, '', spec_sha256, created_at_ms
		FROM blueprint_revisions WHERE blueprint_id = ? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BlueprintRevision
	for rows.Next() {
		var r domain.BlueprintRevision
		if err := rows.Scan(&r.BlueprintID, &r.Revision, &r.SpecYAML, &r.SHA256, &r.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveBlueprintRevision creates the blueprint (slug unknown) or appends a
// revision when the YAML differs from the current one. A built-in slug is
// never taken over by a custom upload and vice versa. It returns the
// blueprint and whether a revision was written.
func (db *DB) SaveBlueprintRevision(ctx context.Context, b domain.Blueprint, yaml, sha string, nowMS int64) (domain.Blueprint, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return b, false, err
	}
	defer tx.Rollback()
	var id, source, curSHA string
	var cur int64
	err = tx.QueryRowContext(ctx, `SELECT b.id, b.source, b.current_revision, r.spec_sha256
		FROM blueprints b JOIN blueprint_revisions r ON r.blueprint_id = b.id AND r.revision = b.current_revision
		WHERE b.slug = ?`, b.Slug).Scan(&id, &source, &cur, &curSHA)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, cur = uuid.NewString(), 0
		if _, err := tx.ExecContext(ctx, `INSERT INTO blueprints
			(id, slug, name, category, description, source, current_revision, enabled, created_at_ms, updated_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`, id, b.Slug, b.Name, b.Category, b.Description, b.Source, nowMS, nowMS); err != nil {
			return b, false, mapErr(err)
		}
	case err != nil:
		return b, false, err
	case source != b.Source:
		return b, false, domain.Invalid("a " + source + " blueprint already uses this slug")
	case curSHA == sha:
		if err := tx.Commit(); err != nil {
			return b, false, err
		}
		out, err := db.GetBlueprint(ctx, id)
		return out, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO blueprint_revisions (blueprint_id, revision, spec_yaml, spec_sha256, created_at_ms)
		VALUES (?, ?, ?, ?, ?)`, id, cur+1, yaml, sha, nowMS); err != nil {
		return b, false, mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE blueprints SET current_revision = ?, name = ?, category = ?, description = ?, updated_at_ms = ?
		WHERE id = ?`, cur+1, b.Name, b.Category, b.Description, nowMS, id); err != nil {
		return b, false, err
	}
	if err := tx.Commit(); err != nil {
		return b, false, err
	}
	out, err := db.GetBlueprint(ctx, id)
	return out, true, err
}

// SetBlueprintEnabled hides or offers a blueprint for new servers.
func (db *DB) SetBlueprintEnabled(ctx context.Context, id string, enabled bool, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE blueprints SET enabled = ?, updated_at_ms = ? WHERE id = ?`, boolInt(enabled), nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteBlueprint removes an unused custom blueprint and its revisions.
func (db *DB) DeleteBlueprint(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source string
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT source, (SELECT count(*) FROM bots WHERE blueprint_id = blueprints.id)
		FROM blueprints WHERE id = ?`, id).Scan(&source, &used); err != nil {
		return mapErr(err)
	}
	if source != "custom" || used > 0 {
		return domain.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blueprints WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Game server state ---

// SetInstallState records an installation transition. imageChoice, when
// non-nil, stores the image chosen by automatic selection.
func (db *DB) SetInstallState(ctx context.Context, botID, state string, imageChoice *string, nowMS int64) error {
	var res sql.Result
	var err error
	if imageChoice != nil {
		res, err = db.ExecContext(ctx, `UPDATE bots SET install_state = ?, image_choice = ?, updated_at_ms = ? WHERE id = ? AND kind = 'game'`,
			state, *imageChoice, nowMS, botID)
	} else {
		res, err = db.ExecContext(ctx, `UPDATE bots SET install_state = ?, updated_at_ms = ? WHERE id = ? AND kind = 'game'`, state, nowMS, botID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetInstalledVersion records the version an installation resolved.
func (db *DB) SetInstalledVersion(ctx context.Context, botID, version string) error {
	_, err := db.ExecContext(ctx, `UPDATE bots SET installed_version = ? WHERE id = ? AND kind = 'game'`, version, botID)
	return err
}

// UpdateGameServer changes a game server's blueprint revision, image choice,
// startup argv and install state, bumping its generation.
func (db *DB) UpdateGameServer(ctx context.Context, b domain.Bot, nowMS int64) error {
	argv, err := nullJSON(b.Argv)
	if err != nil {
		return err
	}
	entry, err := nullJSON(b.Entrypoint)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE bots SET blueprint_revision = ?, image_choice = ?, install_state = ?,
		argv_json = ?, entrypoint_json = ?, image_ref = ?, generation = generation + 1, updated_at_ms = ?
		WHERE id = ? AND kind = 'game' AND desired_state != 'deleted'`,
		b.BlueprintRevision, b.ImageChoice, b.InstallState, argv, entry, b.ImageRef, nowMS, b.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- Allocations ---

const allocCols = `id, node_id, ip, port, alias, notes, bot_id, is_primary, created_at_ms`

func scanAlloc(row interface{ Scan(...any) error }) (domain.Allocation, error) {
	var a domain.Allocation
	var primary int
	err := row.Scan(&a.ID, &a.NodeID, &a.IP, &a.Port, &a.Alias, &a.Notes, &a.BotID, &primary, &a.CreatedAtMS)
	a.Primary = primary == 1
	return a, mapErr(err)
}

func queryAllocs(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, where string, args ...any) ([]domain.Allocation, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+allocCols+` FROM allocations `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Allocation
	for rows.Next() {
		a, err := scanAlloc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListBotAllocations returns a server's allocations, primary first.
func (db *DB) ListBotAllocations(ctx context.Context, botID string) ([]domain.Allocation, error) {
	return queryAllocs(ctx, db, `WHERE bot_id = ? ORDER BY is_primary DESC, port`, botID)
}

// ListAllocations returns a node's allocations ("" = every node).
func (db *DB) ListAllocations(ctx context.Context, nodeID string) ([]domain.Allocation, error) {
	if nodeID == "" {
		return queryAllocs(ctx, db, `ORDER BY node_id, ip, port`)
	}
	return queryAllocs(ctx, db, `WHERE node_id = ? ORDER BY ip, port`, nodeID)
}

// portTaken reports whether a port on the node is used by an allocation or a
// published application port.
func portTaken(ctx context.Context, tx *sql.Tx, nodeID string, port int) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM allocations WHERE node_id = ?1 AND port = ?2) +
		(SELECT count(*) FROM bot_ports p JOIN bots b ON b.id = p.bot_id WHERE b.node_id = ?1 AND p.host_port = ?2)`,
		nodeID, port).Scan(&n)
	return n > 0, err
}

// CreateAllocations adds IP:port reservations, skipping ports already in use
// on the node. It returns the allocations created.
func (db *DB) CreateAllocations(ctx context.Context, nodeID, ip string, ports []int, notes string, nowMS int64) ([]domain.Allocation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var out []domain.Allocation
	for _, p := range ports {
		taken, err := portTaken(ctx, tx, nodeID, p)
		if err != nil {
			return nil, err
		}
		if taken {
			continue
		}
		a := domain.Allocation{ID: uuid.NewString(), NodeID: nodeID, IP: ip, Port: p, Notes: notes, CreatedAtMS: nowMS}
		if _, err := tx.ExecContext(ctx, `INSERT INTO allocations (id, node_id, ip, port, notes, created_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			a.ID, a.NodeID, a.IP, a.Port, a.Notes, a.CreatedAtMS); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, tx.Commit()
}

// DeleteAllocation removes an unassigned allocation.
func (db *DB) DeleteAllocation(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM allocations WHERE id = ? AND bot_id IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM allocations WHERE id = ?`, id).Scan(&exists)
		if exists > 0 {
			return domain.ErrConflict
		}
		return domain.ErrNotFound
	}
	return nil
}

// SetAllocationAlias changes the display address of an allocation.
func (db *DB) SetAllocationAlias(ctx context.Context, id, alias, notes string) error {
	res, err := db.ExecContext(ctx, `UPDATE allocations SET alias = ?, notes = ? WHERE id = ?`, alias, notes, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AllocateForBot assigns count allocations on the node to the server; the
// first becomes primary when the server has none. Free pool entries are used
// first. When the node has no free entry and autoCreate is set, ports from
// preferred upward (skipping used ones and those rejected by usable) are
// created on autoIP. With contiguous, the count ports are consecutive on
// one address (a free pool run first, else an automatically created block).
func (db *DB) AllocateForBot(ctx context.Context, botID, nodeID string, count, preferred int, contiguous, autoCreate bool, autoIP string,
	usable func(port int) bool, nowMS int64) ([]domain.Allocation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var hasPrimary int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM allocations WHERE bot_id = ? AND is_primary = 1`, botID).Scan(&hasPrimary); err != nil {
		return nil, err
	}
	var picked []string
	free, err := queryAllocs(ctx, tx, `WHERE node_id = ? AND bot_id IS NULL ORDER BY (port < ?), port`, nodeID, preferred)
	if err != nil {
		return nil, err
	}
	if contiguous && count > 1 {
		picked = contiguousRun(free, count)
		if picked == nil && autoCreate {
			if preferred < 1024 {
				preferred = 25565
			}
		block:
			for start := preferred; start+count-1 <= 65535; start++ {
				for p := start; p < start+count; p++ {
					taken, err := portTaken(ctx, tx, nodeID, p)
					if err != nil {
						return nil, err
					}
					if taken || (usable != nil && !usable(p)) {
						start = p // the next block starts after this port
						continue block
					}
				}
				for p := start; p < start+count; p++ {
					id := uuid.NewString()
					if _, err := tx.ExecContext(ctx, `INSERT INTO allocations (id, node_id, ip, port, notes, created_at_ms)
						VALUES (?, ?, ?, ?, 'created automatically', ?)`, id, nodeID, autoIP, p, nowMS); err != nil {
						return nil, mapErr(err)
					}
					picked = append(picked, id)
				}
				break
			}
		}
		if len(picked) < count {
			return nil, domain.Invalid(fmt.Sprintf("no %d consecutive free ports are available on this node; an administrator can add ports under Administration → Allocations", count))
		}
	}
	for _, a := range free {
		if len(picked) == count {
			break
		}
		picked = append(picked, a.ID)
	}
	if len(picked) < count && autoCreate {
		if preferred < 1024 {
			preferred = 25565
		}
		for p := preferred; p <= 65535 && len(picked) < count; p++ {
			taken, err := portTaken(ctx, tx, nodeID, p)
			if err != nil {
				return nil, err
			}
			if taken || (usable != nil && !usable(p)) {
				continue
			}
			id := uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO allocations (id, node_id, ip, port, notes, created_at_ms)
				VALUES (?, ?, ?, ?, 'created automatically', ?)`, id, nodeID, autoIP, p, nowMS); err != nil {
				return nil, mapErr(err)
			}
			picked = append(picked, id)
		}
	}
	if len(picked) < count {
		return nil, domain.Invalid("no free allocation is available on this node; an administrator can add ports under Administration → Allocations")
	}
	for i, id := range picked {
		primary := hasPrimary == 0 && i == 0
		if _, err := tx.ExecContext(ctx, `UPDATE allocations SET bot_id = ?, is_primary = ? WHERE id = ? AND bot_id IS NULL`,
			botID, boolInt(primary), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return db.ListBotAllocations(ctx, botID)
}

// contiguousRun returns the ids of the first run of count consecutive free
// pool ports on one address, in the pool's preference order, or nil.
func contiguousRun(free []domain.Allocation, count int) []string {
	byAddr := map[string]map[int]string{}
	for _, a := range free {
		if byAddr[a.IP] == nil {
			byAddr[a.IP] = map[int]string{}
		}
		byAddr[a.IP][a.Port] = a.ID
	}
	for _, a := range free {
		ports := byAddr[a.IP]
		ids := make([]string, 0, count)
		for p := a.Port; p < a.Port+count; p++ {
			id, ok := ports[p]
			if !ok {
				break
			}
			ids = append(ids, id)
		}
		if len(ids) == count {
			return ids
		}
	}
	return nil
}

// ReleaseAllocation returns a non-primary allocation to the pool.
func (db *DB) ReleaseAllocation(ctx context.Context, botID, allocID string) error {
	res, err := db.ExecContext(ctx, `UPDATE allocations SET bot_id = NULL WHERE id = ? AND bot_id = ? AND is_primary = 0`, allocID, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var primary int
		if err := db.QueryRowContext(ctx, `SELECT is_primary FROM allocations WHERE id = ? AND bot_id = ?`, allocID, botID).Scan(&primary); err != nil {
			return mapErr(err)
		}
		return domain.Invalid("the primary allocation cannot be removed; make another one primary first")
	}
	return nil
}

// SetPrimaryAllocation makes one of the server's allocations primary.
func (db *DB) SetPrimaryAllocation(ctx context.Context, botID, allocID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM allocations WHERE id = ? AND bot_id = ?`, allocID, botID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE allocations SET is_primary = 0 WHERE bot_id = ?`, botID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE allocations SET is_primary = 1 WHERE id = ?`, allocID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bots SET generation = generation + 1 WHERE id = ?`, botID); err != nil {
		return err
	}
	return tx.Commit()
}
