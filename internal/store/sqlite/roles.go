package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const roleCols = `id, name, description, permissions_json, system, created_at_ms, updated_at_ms,
	(SELECT count(*) FROM users u WHERE (r.system = 1 AND u.role_id IS NULL AND u.role = r.id) OR u.role_id = r.id)`

func scanRole(row interface{ Scan(...any) error }) (domain.Role, error) {
	var r domain.Role
	var perms string
	var sys int
	err := row.Scan(&r.ID, &r.Name, &r.Description, &perms, &sys, &r.CreatedAtMS, &r.UpdatedAtMS, &r.Users)
	if err != nil {
		return r, mapErr(err)
	}
	r.System = sys == 1
	switch {
	case r.System && r.ID == domain.SystemRoleAdmin:
		r.Permissions = domain.AllPermissions()
	case r.System:
		r.Permissions = domain.DefaultUserPermissions()
	default:
		r.Permissions = decodePermissions(perms)
	}
	return r, nil
}

// ListRoles returns the system roles first, then custom roles by name.
func (db *DB) ListRoles(ctx context.Context) ([]domain.Role, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+roleCols+` FROM roles r ORDER BY system DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRole returns one role.
func (db *DB) GetRole(ctx context.Context, id string) (domain.Role, error) {
	return scanRole(db.QueryRowContext(ctx, `SELECT `+roleCols+` FROM roles r WHERE id = ?`, id))
}

func encodePermissions(p []string) string {
	if p == nil {
		p = []string{}
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// CreateRole inserts a custom role; a duplicate name is domain.ErrConflict.
func (db *DB) CreateRole(ctx context.Context, r domain.Role) error {
	_, err := db.ExecContext(ctx, `INSERT INTO roles (id, name, description, permissions_json, system, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,0,?,?)`, r.ID, r.Name, r.Description, encodePermissions(r.Permissions), r.CreatedAtMS, r.UpdatedAtMS)
	return mapErr(err)
}

// UpdateRole changes a custom role (system roles are never changed).
func (db *DB) UpdateRole(ctx context.Context, r domain.Role) error {
	res, err := db.ExecContext(ctx, `UPDATE roles SET name = ?, description = ?, permissions_json = ?, updated_at_ms = ?
		WHERE id = ? AND system = 0`, r.Name, r.Description, encodePermissions(r.Permissions), r.UpdatedAtMS, r.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ErrRoleInUse refuses deleting a role that accounts still hold.
var ErrRoleInUse = domain.Invalid("accounts still hold this role; give them another role first")

// DeleteRole removes a custom role that no account holds.
func (db *DB) DeleteRole(ctx context.Context, id string) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrRoleInUse
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM oidc_providers WHERE default_role_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return domain.Invalid("a sign-in provider gives this role to new accounts; change the provider first")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id = ? AND system = 0`, id)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

// SetUserCustomRole gives an account a custom role (users.role becomes
// 'user'). Demoting the last active administrator this way is refused.
func (db *DB) SetUserCustomRole(ctx context.Context, id, roleID string, nowMS int64) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		var sys int
		if err := tx.QueryRowContext(ctx, `SELECT system FROM roles WHERE id = ?`, roleID).Scan(&sys); err != nil {
			if err == sql.ErrNoRows {
				return domain.Invalid("that role does not exist")
			}
			return err
		}
		if sys == 1 {
			return domain.Invalid("system roles are assigned as admin or user")
		}
		var cur string
		var dis int
		if err := tx.QueryRowContext(ctx, `SELECT role, disabled FROM users WHERE id = ?`, id).Scan(&cur, &dis); err != nil {
			return mapErr(err)
		}
		if cur == domain.RoleAdmin && dis == 0 {
			n, err := activeAdminsExcept(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastAdmin
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET role = 'user', role_id = ?, updated_at_ms = ? WHERE id = ?`, roleID, nowMS, id)
		return err
	})
}
