package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// ---- backups ----

const backupCols = `id, bot_id, kind, status, file_name, size_bytes, sha256_hex, includes_env, error, created_by, created_at_ms,
	label, verified_at_ms, verify_error, consistent`

func scanBackup(row interface{ Scan(...any) error }) (domain.Backup, error) {
	var b domain.Backup
	var env, consistent int
	err := row.Scan(&b.ID, &b.BotID, &b.Kind, &b.Status, &b.FileName, &b.SizeBytes, &b.SHA256Hex, &env, &b.Error, &b.CreatedBy, &b.CreatedAtMS,
		&b.Label, &b.VerifiedAtMS, &b.VerifyError, &consistent)
	b.IncludesEnv, b.Consistent = env == 1, consistent == 1
	return b, mapErr(err)
}

func (db *DB) InsertBackup(ctx context.Context, b domain.Backup) error {
	_, err := db.ExecContext(ctx, `INSERT INTO bot_backups (`+backupCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?, ?,?,?,?)`,
		b.ID, b.BotID, b.Kind, b.Status, b.FileName, b.SizeBytes, b.SHA256Hex, boolInt(b.IncludesEnv), b.Error, b.CreatedBy, b.CreatedAtMS,
		b.Label, b.VerifiedAtMS, b.VerifyError, boolInt(b.Consistent))
	return mapErr(err)
}

// SetBackupLabel changes (or with nil clears) a backup's label.
func (db *DB) SetBackupLabel(ctx context.Context, botID, id string, label *string) error {
	res, err := db.ExecContext(ctx, `UPDATE bot_backups SET label = ? WHERE id = ? AND bot_id = ?`, label, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetBackupVerified records a verification result (verr nil = passed).
func (db *DB) SetBackupVerified(ctx context.Context, id string, atMS int64, verr *string) error {
	_, err := db.ExecContext(ctx, `UPDATE bot_backups SET verified_at_ms = ?, verify_error = ? WHERE id = ?`, atMS, verr, id)
	return err
}

// FinishBackup records the result of an archive job.
func (db *DB) FinishBackup(ctx context.Context, id, status string, size int64, sha, errMsg *string) error {
	res, err := db.ExecContext(ctx, `UPDATE bot_backups SET status = ?, size_bytes = ?, sha256_hex = ?, error = ? WHERE id = ?`,
		status, size, sha, errMsg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) GetBackup(ctx context.Context, botID, id string) (domain.Backup, error) {
	return scanBackup(db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM bot_backups WHERE id = ? AND bot_id = ?`, id, botID))
}

func (db *DB) ListBackups(ctx context.Context, botID string) ([]domain.Backup, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+backupCols+` FROM bot_backups WHERE bot_id = ? ORDER BY created_at_ms DESC LIMIT 200`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) DeleteBackup(ctx context.Context, botID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM bot_backups WHERE id = ? AND bot_id = ?`, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---- bot telemetry logs ----

// InsertBotTelemetry appends a batch in one short transaction.
func (db *DB) InsertBotTelemetry(ctx context.Context, rows []domain.BotTelemetryLog) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO bot_telemetry_logs (bot_id, recorded_at_ms, kind, name, value, payload_json)
		VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range rows {
		if _, err := st.ExecContext(ctx, r.BotID, r.RecordedAtMS, r.Kind, r.Name, r.Value, r.PayloadJSON); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}

// ListBotTelemetry returns the newest `limit` rows after sinceMS, oldest first.
func (db *DB) ListBotTelemetry(ctx context.Context, botID, kind string, sinceMS int64, limit int) ([]domain.BotTelemetryLog, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, bot_id, recorded_at_ms, kind, name, value, payload_json FROM
		(SELECT * FROM bot_telemetry_logs WHERE bot_id = ? AND kind = ? AND recorded_at_ms > ?
		 ORDER BY recorded_at_ms DESC, id DESC LIMIT ?) ORDER BY recorded_at_ms, id`, botID, kind, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotTelemetryLog
	for rows.Next() {
		var r domain.BotTelemetryLog
		if err := rows.Scan(&r.ID, &r.BotID, &r.RecordedAtMS, &r.Kind, &r.Name, &r.Value, &r.PayloadJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneBotTelemetry deletes at most batch rows older than beforeMS.
func (db *DB) PruneBotTelemetry(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM bot_telemetry_logs WHERE id IN
		(SELECT id FROM bot_telemetry_logs WHERE recorded_at_ms < ? LIMIT ?)`, beforeMS, batch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetBotTelemetryKey stores the hash of a bot's telemetry API key (nil clears it).
func (db *DB) SetBotTelemetryKey(ctx context.Context, botID string, hash []byte, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE bots SET telemetry_key_hash = ?, updated_at_ms = ? WHERE id = ?`, hash, nowMS, botID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetBotIDByTelemetryKey authenticates a bot-to-panel request.
func (db *DB) GetBotIDByTelemetryKey(ctx context.Context, hash []byte) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM bots WHERE telemetry_key_hash = ? AND desired_state <> 'deleted'`, hash).Scan(&id)
	return id, mapErr(err)
}

// ---- GitHub source ----

const repoCols = `bot_id, token_user_id, full_name, branch, root_dir, private, auto_deploy,
	webhook_secret_ciphertext, webhook_secret_nonce, webhook_secret_key_id,
	last_deployed_sha, last_deployed_at_ms, last_error, hook_id, created_at_ms, updated_at_ms`

func scanRepo(row interface{ Scan(...any) error }) (domain.GitHubRepo, error) {
	var r domain.GitHubRepo
	var priv, auto int
	err := row.Scan(&r.BotID, &r.TokenUserID, &r.FullName, &r.Branch, &r.RootDir, &priv, &auto,
		&r.SecretCipher, &r.SecretNonce, &r.SecretKeyID, &r.LastSHA, &r.LastDeployedMS, &r.LastError, &r.HookID, &r.CreatedAtMS, &r.UpdatedAtMS)
	r.Private, r.AutoDeploy = priv == 1, auto == 1
	return r, mapErr(err)
}

func (db *DB) UpsertGitHubRepo(ctx context.Context, r domain.GitHubRepo) error {
	_, err := db.ExecContext(ctx, `INSERT INTO github_repos (`+repoCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(bot_id) DO UPDATE SET token_user_id = excluded.token_user_id, full_name = excluded.full_name,
			branch = excluded.branch, root_dir = excluded.root_dir, private = excluded.private,
			auto_deploy = excluded.auto_deploy, hook_id = excluded.hook_id, updated_at_ms = excluded.updated_at_ms`,
		r.BotID, r.TokenUserID, r.FullName, r.Branch, r.RootDir, boolInt(r.Private), boolInt(r.AutoDeploy),
		r.SecretCipher, r.SecretNonce, r.SecretKeyID, r.LastSHA, r.LastDeployedMS, r.LastError, r.HookID, r.CreatedAtMS, r.UpdatedAtMS)
	return mapErr(err)
}

// DeleteGitHubRepo unlinks a bot from its repository.
func (db *DB) DeleteGitHubRepo(ctx context.Context, botID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM github_repos WHERE bot_id = ?`, botID)
	return err
}

func (db *DB) GetGitHubRepo(ctx context.Context, botID string) (domain.GitHubRepo, error) {
	return scanRepo(db.QueryRowContext(ctx, `SELECT `+repoCols+` FROM github_repos WHERE bot_id = ?`, botID))
}

// ListAutoDeployRepos returns auto-deploy sources for a repository so the
// webhook receiver can verify each candidate's HMAC.
func (db *DB) ListAutoDeployRepos(ctx context.Context, fullName string) ([]domain.GitHubRepo, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+repoCols+` FROM github_repos WHERE full_name = ? AND auto_deploy = 1 LIMIT 50`, fullName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.GitHubRepo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPollingRepos returns auto-deploy sources without a webhook, whose
// branches the deploy service checks periodically.
func (db *DB) ListPollingRepos(ctx context.Context, limit int) ([]domain.GitHubRepo, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+repoCols+` FROM github_repos r WHERE auto_deploy = 1 AND hook_id IS NULL
		AND EXISTS (SELECT 1 FROM bots b WHERE b.id = r.bot_id AND b.desired_state != 'deleted') ORDER BY updated_at_ms LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.GitHubRepo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) RecordDeploy(ctx context.Context, botID string, sha *string, errMsg *string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE github_repos SET last_deployed_sha = COALESCE(?, last_deployed_sha),
		last_deployed_at_ms = ?, last_error = ?, updated_at_ms = ? WHERE bot_id = ?`, sha, nowMS, errMsg, nowMS, botID)
	return err
}

// PendingPush is a webhook push waiting for its server's node to reconnect.
type PendingPush struct {
	BotID, SHA, Link string
	AtMS             int64
}

// SetPendingPush remembers the newest push for a bot whose node is offline
// (a later push replaces an earlier one).
func (db *DB) SetPendingPush(ctx context.Context, botID, sha, link string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE github_repos SET pending_push_sha = ?, pending_push_link = ?, pending_push_at_ms = ?
		WHERE bot_id = ?`, sha, link, nowMS, botID)
	return err
}

// TakePendingPushes atomically returns and clears the pending pushes of the
// servers on a node, so each pending push is run at most once.
func (db *DB) TakePendingPushes(ctx context.Context, nodeID string) ([]PendingPush, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT r.bot_id, r.pending_push_sha, COALESCE(r.pending_push_link, ''), COALESCE(r.pending_push_at_ms, 0)
		FROM github_repos r JOIN bots b ON b.id = r.bot_id
		WHERE r.pending_push_sha IS NOT NULL AND b.node_id = ? AND b.desired_state != 'deleted'`, nodeID)
	if err != nil {
		return nil, err
	}
	var out []PendingPush
	for rows.Next() {
		var p PendingPush
		if err := rows.Scan(&p.BotID, &p.SHA, &p.Link, &p.AtMS); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range out {
		if _, err := tx.ExecContext(ctx, `UPDATE github_repos SET pending_push_sha = NULL, pending_push_link = NULL,
			pending_push_at_ms = NULL WHERE bot_id = ?`, p.BotID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

// GetPendingPush reports a bot's pending push (ErrNotFound: none).
func (db *DB) GetPendingPush(ctx context.Context, botID string) (PendingPush, error) {
	var p PendingPush
	err := db.QueryRowContext(ctx, `SELECT bot_id, pending_push_sha, COALESCE(pending_push_link, ''), COALESCE(pending_push_at_ms, 0)
		FROM github_repos WHERE bot_id = ? AND pending_push_sha IS NOT NULL`, botID).Scan(&p.BotID, &p.SHA, &p.Link, &p.AtMS)
	return p, mapErr(err)
}

// ---- ports ----

// ReplaceBotPorts swaps a bot's port set atomically. A host port already used
// by another bot returns domain.ErrConflict.
func (db *DB) ReplaceBotPorts(ctx context.Context, botID string, ports []domain.BotPort) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_ports WHERE bot_id = ?`, botID); err != nil {
		return err
	}
	for _, p := range ports {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_ports (bot_id, container_port, host_port, protocol, host_ip, created_at_ms)
			VALUES (?,?,?,?,?,?)`, botID, p.ContainerPort, p.HostPort, p.Protocol, p.HostIP, p.CreatedAtMS); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}

func (db *DB) ListBotPorts(ctx context.Context, botID string) ([]domain.BotPort, error) {
	rows, err := db.QueryContext(ctx, `SELECT bot_id, container_port, host_port, protocol, host_ip, created_at_ms
		FROM bot_ports WHERE bot_id = ? ORDER BY container_port`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotPort
	for rows.Next() {
		var p domain.BotPort
		if err := rows.Scan(&p.BotID, &p.ContainerPort, &p.HostPort, &p.Protocol, &p.HostIP, &p.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CommandUsage is a command-name aggregate.
type CommandUsage struct {
	Name  string
	Count int64
}

// AggregateBotCommands sums command usage since sinceMS (a row's value is its
// count; missing means 1), most-used first.
func (db *DB) AggregateBotCommands(ctx context.Context, botID string, sinceMS int64, limit int) ([]CommandUsage, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, CAST(SUM(COALESCE(value, 1)) AS INTEGER) AS n FROM bot_telemetry_logs
		WHERE bot_id = ? AND kind = 'command' AND recorded_at_ms > ? GROUP BY name ORDER BY n DESC, name LIMIT ?`, botID, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommandUsage
	for rows.Next() {
		var c CommandUsage
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TrimBotTelemetry deletes at most batch of a bot's oldest rows beyond the
// newest keep rows and returns how many it deleted.
func (db *DB) TrimBotTelemetry(ctx context.Context, botID string, keep, batch int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM bot_telemetry_logs WHERE id IN (
		SELECT id FROM bot_telemetry_logs WHERE bot_id = ? ORDER BY id DESC LIMIT ? OFFSET ?)`, botID, batch, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BotsOverTelemetryCap lists bots that hold more than keep telemetry rows.
func (db *DB) BotsOverTelemetryCap(ctx context.Context, keep int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT bot_id FROM bot_telemetry_logs GROUP BY bot_id HAVING count(*) > ? LIMIT 100`, keep)
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

// HasTelemetryKey reports whether a bot has a telemetry key configured.
func (db *DB) HasTelemetryKey(ctx context.Context, botID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE id = ? AND telemetry_key_hash IS NOT NULL`, botID).Scan(&n)
	return n > 0, err
}

// ListBotEvents returns the newest limit event rows, newest first.
func (db *DB) ListBotEvents(ctx context.Context, botID string, limit int) ([]domain.BotTelemetryLog, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, bot_id, recorded_at_ms, kind, name, value, payload_json FROM bot_telemetry_logs
		WHERE bot_id = ? AND kind = 'event' ORDER BY recorded_at_ms DESC, id DESC LIMIT ?`, botID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BotTelemetryLog
	for rows.Next() {
		var r domain.BotTelemetryLog
		if err := rows.Scan(&r.ID, &r.BotID, &r.RecordedAtMS, &r.Kind, &r.Name, &r.Value, &r.PayloadJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FailStaleBackups marks backups left in 'creating' by a crash as failed.
func (db *DB) FailStaleBackups(ctx context.Context) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE bot_backups SET status = 'failed', error = 'interrupted by a restart' WHERE status = 'creating'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LastBackupAtMS returns the newest non-failed backup time of a kind, or 0.
func (db *DB) LastBackupAtMS(ctx context.Context, botID, kind string) (int64, error) {
	var t *int64
	err := db.QueryRowContext(ctx, `SELECT max(created_at_ms) FROM bot_backups WHERE bot_id = ? AND kind = ? AND status <> 'failed'`, botID, kind).Scan(&t)
	if err != nil || t == nil {
		return 0, err
	}
	return *t, nil
}

// ListBackupsByKind returns a bot's ready backups of a kind, newest first.
func (db *DB) ListBackupsByKind(ctx context.Context, botID, kind string) ([]domain.Backup, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+backupCols+` FROM bot_backups WHERE bot_id = ? AND kind = ? AND status <> 'creating'
		ORDER BY created_at_ms DESC LIMIT 1000`, botID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BotIDs returns the IDs of all bots, deleted-marked ones included.
func (db *DB) BotIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM bots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// LaggingBots counts bots whose latest intent has not been applied for longer
// than olderThanMS (a slow or stuck reconciliation).
func (db *DB) LaggingBots(ctx context.Context, nowMS, olderThanMS int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM bots WHERE desired_state != 'deleted'
		AND observed_generation < generation AND updated_at_ms < ?`, nowMS-olderThanMS).Scan(&n)
	return n, err
}

// BackupSummary describes backups across all bots for diagnostics.
type BackupSummary struct {
	LastReadyMS    int64
	LastVerifiedMS int64
	FailedRecent   int
	VerifyFailed   int
	TotalBytes     int64
}

func (db *DB) BackupSummary(ctx context.Context, sinceMS int64) (BackupSummary, error) {
	var s BackupSummary
	err := db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT max(created_at_ms) FROM bot_backups WHERE status = 'ready'), 0),
		COALESCE((SELECT max(verified_at_ms) FROM bot_backups WHERE verify_error IS NULL), 0),
		(SELECT count(*) FROM bot_backups WHERE status = 'failed' AND created_at_ms >= ?),
		(SELECT count(*) FROM bot_backups WHERE verify_error IS NOT NULL),
		COALESCE((SELECT sum(size_bytes) FROM bot_backups), 0)`, sinceMS).
		Scan(&s.LastReadyMS, &s.LastVerifiedMS, &s.FailedRecent, &s.VerifyFailed, &s.TotalBytes)
	return s, err
}

// SchemaVersion returns the newest applied migration.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}
