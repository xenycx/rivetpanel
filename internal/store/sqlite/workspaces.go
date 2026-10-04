package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// createPersonalWorkspace gives a new account its personal workspace, in the
// transaction that creates the account.
func createPersonalWorkspace(ctx context.Context, tx *sql.Tx, u domain.User) error {
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, owner_id, personal, created_at_ms, updated_at_ms)
		VALUES (?, 'Personal', ?, 1, ?, ?)`, id, u.ID, u.CreatedAtMS, u.CreatedAtMS); err != nil {
		return mapErr(err)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'owner', ?, ?)`, id, u.ID, u.CreatedAtMS, u.CreatedAtMS)
	return mapErr(err)
}

// CreateWorkspace inserts a team workspace with its owner as the first member.
func (db *DB) CreateWorkspace(ctx context.Context, w domain.Workspace) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, owner_id, personal, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`, w.ID, w.Name, w.OwnerID, boolInt(w.Personal), w.CreatedAtMS, w.UpdatedAtMS); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'owner', ?, ?)`, w.ID, w.OwnerID, w.CreatedAtMS, w.CreatedAtMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

const workspaceCols = `w.id, w.name, w.owner_id, w.personal, w.created_at_ms, w.updated_at_ms`

func scanWorkspace(row interface{ Scan(...any) error }, extra ...any) (domain.Workspace, error) {
	var w domain.Workspace
	var personal int
	err := row.Scan(append([]any{&w.ID, &w.Name, &w.OwnerID, &personal, &w.CreatedAtMS, &w.UpdatedAtMS}, extra...)...)
	w.Personal = personal == 1
	return w, mapErr(err)
}

// GetWorkspace returns one workspace.
func (db *DB) GetWorkspace(ctx context.Context, id string) (domain.Workspace, error) {
	return scanWorkspace(db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces w WHERE w.id = ?`, id))
}

// PersonalWorkspace returns a user's personal workspace, creating it if the
// account somehow has none (every creation path makes one; this keeps
// accounts written by other means usable).
func (db *DB) PersonalWorkspace(ctx context.Context, userID string) (domain.Workspace, error) {
	const q = `SELECT ` + workspaceCols + ` FROM workspaces w WHERE w.owner_id = ? AND w.personal = 1`
	w, err := scanWorkspace(db.QueryRowContext(ctx, q, userID))
	if !errors.Is(err, domain.ErrNotFound) {
		return w, err
	}
	u, err := db.GetUserByID(ctx, userID)
	if err != nil {
		return domain.Workspace{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer tx.Rollback()
	if err := createPersonalWorkspace(ctx, tx, u); err != nil && !errors.Is(err, domain.ErrConflict) {
		return domain.Workspace{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Workspace{}, err
	}
	return scanWorkspace(db.QueryRowContext(ctx, q, userID))
}

// summaryCols add the owner's email and usage figures to workspaceCols; the
// query binds the caller's user id first (for their role).
const summaryCols = workspaceCols + `, u.email, COALESCE(m.role, ''),
	(SELECT count(*) FROM workspace_members x WHERE x.workspace_id = w.id),
	(SELECT count(*) FROM bots b WHERE b.workspace_id = w.id AND b.desired_state != 'deleted'),
	(SELECT count(*) FROM bots b WHERE b.workspace_id = w.id AND b.desired_state = 'running'),
	(SELECT COALESCE(sum(b.memory_bytes), 0) FROM bots b WHERE b.workspace_id = w.id AND b.desired_state = 'running'),
	(SELECT count(*) FROM sites s WHERE s.workspace_id = w.id),
	(SELECT COALESCE(max(b.updated_at_ms), 0) FROM bots b WHERE b.workspace_id = w.id AND b.desired_state != 'deleted')
	FROM workspaces w JOIN users u ON u.id = w.owner_id
	LEFT JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = ?`

func (db *DB) listSummaries(ctx context.Context, where string, args ...any) ([]domain.WorkspaceSummary, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+summaryCols+` `+where+` ORDER BY w.personal DESC, lower(w.name), w.created_at_ms LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WorkspaceSummary
	for rows.Next() {
		var s domain.WorkspaceSummary
		w, err := scanWorkspace(rows, &s.OwnerEmail, &s.Role, &s.Members, &s.Bots, &s.RunningBots, &s.MemoryBytes, &s.Sites, &s.LastActiveMS)
		if err != nil {
			return nil, err
		}
		s.Workspace = w
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListWorkspacesForUser returns the workspaces userID is a member of.
func (db *DB) ListWorkspacesForUser(ctx context.Context, userID string) ([]domain.WorkspaceSummary, error) {
	return db.listSummaries(ctx, `WHERE m.user_id IS NOT NULL`, userID)
}

// ListAllWorkspaces returns every workspace (administrators); Role is the
// caller's role where they are a member.
func (db *DB) ListAllWorkspaces(ctx context.Context, callerID string) ([]domain.WorkspaceSummary, error) {
	return db.listSummaries(ctx, ``, callerID)
}

// WorkspaceSummaryFor returns one workspace with figures and the caller's role.
func (db *DB) WorkspaceSummaryFor(ctx context.Context, id, callerID string) (domain.WorkspaceSummary, error) {
	out, err := db.listSummaries(ctx, `WHERE w.id = ?`, callerID, id)
	if err != nil {
		return domain.WorkspaceSummary{}, err
	}
	if len(out) == 0 {
		return domain.WorkspaceSummary{}, domain.ErrNotFound
	}
	return out[0], nil
}

// CountOwnedWorkspaces counts the team workspaces a user owns.
func (db *DB) CountOwnedWorkspaces(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM workspaces WHERE owner_id = ? AND personal = 0`, userID).Scan(&n)
	return n, err
}

// RenameWorkspace changes a workspace's name.
func (db *DB) RenameWorkspace(ctx context.Context, id, name string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE workspaces SET name = ?, updated_at_ms = ? WHERE id = ?`, name, nowMS, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteWorkspace removes an empty team workspace. Bots (including ones still
// being deleted) and sites keep it alive: it returns an error naming them.
func (db *DB) DeleteWorkspace(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var personal, bots, sites int
	if err := tx.QueryRowContext(ctx, `SELECT personal,
		(SELECT count(*) FROM bots WHERE workspace_id = ?1),
		(SELECT count(*) FROM sites WHERE workspace_id = ?1)
		FROM workspaces WHERE id = ?1`, id).Scan(&personal, &bots, &sites); err != nil {
		return mapErr(err)
	}
	if personal == 1 {
		return domain.Invalid("a personal workspace cannot be deleted")
	}
	if bots > 0 || sites > 0 {
		return domain.Invalid("move or delete this workspace's bots and sites first")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

// WorkspaceRole returns userID's role in a workspace ("" when not a member).
func (db *DB) WorkspaceRole(ctx context.Context, workspaceID, userID string) (string, error) {
	var r string
	err := db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// BotWorkspaceRole returns userID's role in the workspace a bot belongs to
// ("" when not a member).
func (db *DB) BotWorkspaceRole(ctx context.Context, botID, userID string) (string, error) {
	var r string
	err := db.QueryRowContext(ctx, `SELECT m.role FROM bots b JOIN workspace_members m ON m.workspace_id = b.workspace_id
		WHERE b.id = ? AND m.user_id = ?`, botID, userID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// ListWorkspaceMembers lists a workspace's members, owner first.
func (db *DB) ListWorkspaceMembers(ctx context.Context, workspaceID string) ([]domain.WorkspaceMember, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.workspace_id, m.user_id, u.email, u.display_name, m.role, m.added_by, m.created_at_ms, m.updated_at_ms
		FROM workspace_members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = ?
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'developer' THEN 2 ELSE 3 END, u.email LIMIT 500`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WorkspaceMember
	for rows.Next() {
		var m domain.WorkspaceMember
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Email, &m.DisplayName, &m.Role, &m.AddedBy, &m.CreatedAtMS, &m.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetWorkspaceMember adds a member or changes their role. The owner's row is
// never changed here.
func (db *DB) SetWorkspaceMember(ctx context.Context, m domain.WorkspaceMember) error {
	_, err := db.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, added_by, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = excluded.role, updated_at_ms = excluded.updated_at_ms
		WHERE workspace_members.role != 'owner'`,
		m.WorkspaceID, m.UserID, m.Role, m.AddedBy, m.CreatedAtMS, m.UpdatedAtMS)
	return mapErr(err)
}

// RemoveWorkspaceMember removes a non-owner member.
func (db *DB) RemoveWorkspaceMember(ctx context.Context, workspaceID, userID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ? AND role != 'owner'`, workspaceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetBotWorkspace moves a bot to another workspace.
func (db *DB) SetBotWorkspace(ctx context.Context, botID, workspaceID string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE bots SET workspace_id = ?, updated_at_ms = ? WHERE id = ? AND desired_state != 'deleted'`, workspaceID, nowMS, botID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListWorkspaceBots returns the bots in a workspace (administrator views).
func (db *DB) ListWorkspaceBots(ctx context.Context, workspaceID string) ([]domain.Bot, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+botCols+` FROM bots WHERE workspace_id = ? AND desired_state != 'deleted'
		ORDER BY created_at_ms LIMIT 1000`, workspaceID)
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
