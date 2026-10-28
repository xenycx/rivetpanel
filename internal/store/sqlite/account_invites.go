package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// accountInviteCols reads an invitation with its setup and the display names
// of what it refers to. Rows written before the setup columns existed hold
// NULLs and read back as "" (no setup), so old links keep working. The role
// name comes from the custom role when one is set, else the built-in role's
// row.
const accountInviteCols = `i.id, COALESCE(i.email,''), i.role, i.created_by, i.created_at_ms, i.expires_at_ms,
	COALESCE(i.role_id,''), COALESCE(i.quota_json,''), COALESCE(i.node_id,''), COALESCE(i.workspace_id,''), COALESCE(i.workspace_role,''),
	COALESCE(cu.email,''), COALESCE(r.name,''), COALESCE(n.name,''), COALESCE(w.name,'')`

const accountInviteFrom = ` FROM account_invites i
	LEFT JOIN users cu ON cu.id = i.created_by
	LEFT JOIN roles r ON r.id = COALESCE(i.role_id, i.role)
	LEFT JOIN nodes n ON n.id = i.node_id
	LEFT JOIN workspaces w ON w.id = i.workspace_id`

// inviteQuota is the JSON object stored in account_invites.quota_json: the
// API's workspace quota field names, unset limits left out.
type inviteQuota struct {
	MaxApps     *int64 `json:"max_apps,omitempty"`
	MaxBots     *int64 `json:"max_bots,omitempty"`
	MaxGames    *int64 `json:"max_games,omitempty"`
	MaxWebApps  *int64 `json:"max_web_apps,omitempty"`
	MaxSites    *int64 `json:"max_sites,omitempty"`
	MemoryBytes *int64 `json:"memory_bytes,omitempty"`
	NanoCPUs    *int64 `json:"nano_cpus,omitempty"`
}

func encodeInviteQuota(q *domain.WorkspaceQuota) any {
	if q == nil || q.Empty() {
		return nil
	}
	b, err := json.Marshal(inviteQuota{q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes, q.NanoCPUs})
	if err != nil {
		return nil
	}
	return string(b)
}

func decodeInviteQuota(s string) *domain.WorkspaceQuota {
	if s == "" {
		return nil
	}
	var v inviteQuota
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil
	}
	q := domain.WorkspaceQuota{MaxApps: v.MaxApps, MaxBots: v.MaxBots, MaxGames: v.MaxGames, MaxWebApps: v.MaxWebApps,
		MaxSites: v.MaxSites, MemoryBytes: v.MemoryBytes, NanoCPUs: v.NanoCPUs}
	if q.Empty() {
		return nil
	}
	return &q
}

func scanAccountInvite(row interface{ Scan(...any) error }) (domain.AccountInvite, error) {
	var v domain.AccountInvite
	var quota string
	err := row.Scan(&v.ID, &v.Email, &v.Role, &v.CreatedBy, &v.CreatedAtMS, &v.ExpiresAtMS,
		&v.RoleID, &quota, &v.NodeID, &v.WorkspaceID, &v.WorkspaceRole,
		&v.CreatedByEmail, &v.RoleName, &v.NodeName, &v.WorkspaceName)
	if err != nil {
		return domain.AccountInvite{}, mapErr(err)
	}
	v.Quota = decodeInviteQuota(quota)
	return v, nil
}

func (db *DB) InsertAccountInvite(ctx context.Context, v domain.AccountInvite, hash []byte) error {
	_, err := db.ExecContext(ctx, `INSERT INTO account_invites (id,token_hash,email,role,created_by,created_at_ms,expires_at_ms,
		role_id,quota_json,node_id,workspace_id,workspace_role) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, hash, nullString(v.Email), v.Role, v.CreatedBy, v.CreatedAtMS, v.ExpiresAtMS,
		nullString(v.RoleID), encodeInviteQuota(v.Quota), nullString(v.NodeID), nullString(v.WorkspaceID), nullString(v.WorkspaceRole))
	return mapErr(err)
}

func (db *DB) ListAccountInvites(ctx context.Context, nowMS int64) ([]domain.AccountInvite, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+accountInviteCols+accountInviteFrom+` WHERE i.used_at_ms IS NULL AND i.expires_at_ms > ? ORDER BY i.created_at_ms DESC LIMIT 100`, nowMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AccountInvite
	for rows.Next() {
		v, err := scanAccountInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (db *DB) AccountInviteByHash(ctx context.Context, hash []byte, nowMS int64) (domain.AccountInvite, error) {
	return scanAccountInvite(db.QueryRowContext(ctx, `SELECT `+accountInviteCols+accountInviteFrom+` WHERE i.token_hash=? AND i.used_at_ms IS NULL AND i.expires_at_ms>?`, hash, nowMS))
}

// UseAccountInvite creates the invited account, its personal workspace and
// the invitation's setup in one transaction. Setup whose target vanished or
// no longer qualifies (a role made system, a node disabled or draining, a
// workspace turned personal) is skipped: the account is still created.
func (db *DB) UseAccountInvite(ctx context.Context, hash []byte, u domain.User, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var email, roleID, quotaJSON, nodeID, wsID, wsRole sql.NullString
	var role, createdBy string
	if err := tx.QueryRowContext(ctx, `SELECT email,role,created_by,role_id,quota_json,node_id,workspace_id,workspace_role FROM account_invites
		WHERE token_hash=? AND used_at_ms IS NULL AND expires_at_ms>?`, hash, nowMS).Scan(&email, &role, &createdBy, &roleID, &quotaJSON, &nodeID, &wsID, &wsRole); err != nil {
		return mapErr(err)
	}
	if email.Valid && email.String != u.Email {
		return domain.Invalid("this invitation was sent to a different email address")
	}
	u.Role = role
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`, u.ID, u.Email, u.DisplayName, u.AvatarJPEG, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAtMS, u.UpdatedAtMS, boolInt(u.EmailVerified)); err != nil {
		return mapErr(err)
	}
	personal, err := createPersonalWorkspace(ctx, tx, u)
	if err != nil {
		return err
	}
	if roleID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role_id = ? WHERE id = ?
			AND EXISTS (SELECT 1 FROM roles WHERE id = ? AND system = 0)`, roleID.String, u.ID, roleID.String); err != nil {
			return mapErr(err)
		}
	}
	if q := decodeInviteQuota(quotaJSON.String); q != nil {
		// updated_by names the inviter when that account still exists.
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_quotas (workspace_id, max_apps, max_bots, max_games, max_web_apps, max_sites,
			memory_bytes, nano_cpus, updated_by, created_at_ms, updated_at_ms)
			VALUES (?,?,?,?,?,?,?,?,(SELECT id FROM users WHERE id = ?),?,?)`,
			personal, q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes, q.NanoCPUs, createdBy, nowMS, nowMS); err != nil {
			return mapErr(err)
		}
	}
	if nodeID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET node_id = ? WHERE id = ?
			AND EXISTS (SELECT 1 FROM nodes WHERE id = ? AND enabled = 1 AND draining = 0)`, nodeID.String, personal, nodeID.String); err != nil {
			return mapErr(err)
		}
	}
	if wsID.Valid && wsRole.Valid {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO workspace_members (workspace_id, user_id, role, added_by, created_at_ms, updated_at_ms)
			SELECT w.id, ?, ?, (SELECT id FROM users WHERE id = ?), ?, ? FROM workspaces w WHERE w.id = ? AND w.personal = 0`,
			u.ID, wsRole.String, createdBy, nowMS, nowMS, wsID.String); err != nil {
			return mapErr(err)
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE account_invites SET used_by=?,used_at_ms=? WHERE token_hash=? AND used_at_ms IS NULL`, u.ID, nowMS, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.ErrConflict
	}
	return tx.Commit()
}

func (db *DB) DeleteAccountInvite(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM account_invites WHERE id=? AND used_at_ms IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
