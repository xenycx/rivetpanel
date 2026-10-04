package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// userCols are the columns written when an account is inserted.
const userCols = `id, email, display_name, avatar_jpeg, password_hash, role, disabled, created_at_ms, updated_at_ms, email_verified`

// UnverifiedPolicyKey is the panel setting that lists the permissions
// withheld from accounts whose email address is not verified ("" = off).
const UnverifiedPolicyKey = "unverified_restrict"

// userSelect is the column list read for an account, with its custom role
// and the unverified-email policy, for a users table aliased as a.
func userSelect(a string) string {
	p := a + "."
	return p + `id, ` + p + `email, ` + p + `display_name, ` + p + `avatar_jpeg, ` + p + `password_hash, ` + p + `role, ` + p + `disabled, ` +
		p + `created_at_ms, ` + p + `updated_at_ms, ` + p + `email_verified, ` + p + `email_verified_at_ms, ` + p + `role_id,
		(SELECT r.name FROM roles r WHERE r.id = ` + p + `role_id),
		(SELECT r.permissions_json FROM roles r WHERE r.id = ` + p + `role_id),
		(SELECT ps.value FROM panel_settings ps WHERE ps.key = '` + UnverifiedPolicyKey + `')`
}

// userScanDest returns the scan destinations matching userSelect and a
// function that completes the user after the scan.
func userScanDest(u *domain.User) ([]any, func()) {
	var dis, verified int
	var roleID, roleName, perms, policy sql.NullString
	dest := []any{&u.ID, &u.Email, &u.DisplayName, &u.AvatarJPEG, &u.PasswordHash, &u.Role, &dis, &u.CreatedAtMS, &u.UpdatedAtMS,
		&verified, &u.EmailVerifiedAtMS, &roleID, &roleName, &perms, &policy}
	return dest, func() {
		u.Disabled = dis == 1
		u.EmailVerified = verified == 1
		if roleID.Valid && roleName.Valid && u.Role != domain.RoleAdmin {
			u.RoleID, u.RoleName = roleID.String, roleName.String
			u.CustomPermissions = decodePermissions(perms.String)
		}
		if !u.EmailVerified && u.Role != domain.RoleAdmin && policy.String != "" {
			// Only what the role actually grants is withheld (and shown).
			granted := u.RolePermissions()
			for _, p := range SplitPermissionList(policy.String) {
				if slices.Contains(granted, p) {
					u.Withheld = append(u.Withheld, p)
				}
			}
		}
	}
}

// decodePermissions reads a role's permission list, keeping only names in
// the catalog (a stored name that no longer exists grants nothing).
func decodePermissions(js string) []string {
	var raw []string
	if json.Unmarshal([]byte(js), &raw) != nil {
		return nil
	}
	out := raw[:0]
	for _, p := range raw {
		if domain.ValidPermission(p) {
			out = append(out, p)
		}
	}
	return out
}

// SplitPermissionList parses the comma-separated policy setting.
func SplitPermissionList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); domain.ValidPermission(p) {
			out = append(out, p)
		}
	}
	return out
}

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var u domain.User
	dest, done := userScanDest(&u)
	err := row.Scan(dest...)
	done()
	return u, mapErr(err)
}

// CreateUser inserts a user and their personal workspace; a duplicate email
// returns domain.ErrConflict.
func (db *DB) CreateUser(ctx context.Context, u domain.User) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Email, u.DisplayName, u.AvatarJPEG, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAtMS, u.UpdatedAtMS, boolInt(u.EmailVerified)); err != nil {
		return mapErr(err)
	}
	if err := createPersonalWorkspace(ctx, tx, u); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) UpdateProfile(ctx context.Context, id, name string, avatar []byte, replaceAvatar bool, nowMS int64) error {
	q := `UPDATE users SET display_name = ?, updated_at_ms = ? WHERE id = ?`
	args := []any{name, nowMS, id}
	if replaceAvatar {
		q = `UPDATE users SET display_name = ?, avatar_jpeg = ?, updated_at_ms = ? WHERE id = ?`
		args = []any{name, avatar, nowMS, id}
	}
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) GetUserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(db.QueryRowContext(ctx, `SELECT `+userSelect("users")+` FROM users WHERE email = ?`, email))
}

func (db *DB) GetUserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(db.QueryRowContext(ctx, `SELECT `+userSelect("users")+` FROM users WHERE id = ?`, id))
}

func (db *DB) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+userSelect("users")+` FROM users ORDER BY created_at_ms LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ErrLastAdmin refuses changes that would leave no active administrator.
var ErrLastAdmin = domain.Invalid("this is the only active administrator; make someone else an administrator first")

// activeAdminsExcept counts enabled administrators other than id, inside tx.
func activeAdminsExcept(ctx context.Context, tx interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}, id string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled = 0 AND id != ?`, id).Scan(&n)
	return n, err
}

// SetUserRole changes a role. Demoting the last active administrator is
// refused inside the same (immediate) transaction, so two concurrent demotions
// cannot both succeed.
func (db *DB) SetUserRole(ctx context.Context, id, role string, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if role != domain.RoleAdmin {
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
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, role_id = NULL, updated_at_ms = ? WHERE id = ?`, role, nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// SetUserDisabled updates the flag and, when disabling, revokes all sessions in
// the same transaction. Disabling the last active administrator is refused.
func (db *DB) SetUserDisabled(ctx context.Context, id string, disabled bool, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if disabled {
		var role string
		if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
			return mapErr(err)
		}
		if role == domain.RoleAdmin {
			n, err := activeAdminsExcept(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastAdmin
			}
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET disabled = ?, updated_at_ms = ? WHERE id = ?`, boolInt(disabled), nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// BotCountsByOwner returns how many (non-deleted) bots each user owns.
func (db *DB) BotCountsByOwner(ctx context.Context) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT owner_id, count(*) FROM bots WHERE desired_state != 'deleted' GROUP BY owner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
