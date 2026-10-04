package sqlite

import (
	"context"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const botCols = `id, owner_id, node_id, name, runtime, image_ref, argv_json, memory_bytes, nano_cpus, pids_limit,
	desired_state, observed_state, generation, observed_generation, container_id, last_exit_code, last_error,
	observed_at_ms, created_at_ms, updated_at_ms,
	entrypoint_json, source_type, template_id, network_enabled, bandwidth_kbps,
	restart_policy, restart_max_attempts, restart_backoff_initial_ms, restart_backoff_max_ms, auto_backup,
	restart_count, next_retry_at_ms, state_reason, last_started_at_ms,
	discord_user_id, discord_username, discord_avatar_url, COALESCE(workspace_id, ''), COALESCE(build_command, ''),
	COALESCE(logo_updated_at_ms, 0), kind, COALESCE(blueprint_id, ''), COALESCE(blueprint_revision, 0), image_choice, install_state, installed_version,
	jvm_args, jvm_args_updated_at_ms, jvm_args_generation`

func scanBot(row interface{ Scan(...any) error }) (domain.Bot, error) {
	var b domain.Bot
	var argv string
	var entry *string
	var netEnabled, autoBackup int
	err := row.Scan(&b.ID, &b.OwnerID, &b.NodeID, &b.Name, &b.Runtime, &b.ImageRef, &argv, &b.MemoryBytes, &b.NanoCPUs,
		&b.PidsLimit, &b.DesiredState, &b.ObservedState, &b.Generation, &b.ObservedGeneration, &b.ContainerID,
		&b.LastExitCode, &b.LastError, &b.ObservedAtMS, &b.CreatedAtMS, &b.UpdatedAtMS,
		&entry, &b.SourceType, &b.TemplateID, &netEnabled, &b.BandwidthKbps,
		&b.RestartPolicy, &b.RestartMaxAttempts, &b.RestartBackoffInitialMS, &b.RestartBackoffMaxMS, &autoBackup,
		&b.RestartCount, &b.NextRetryAtMS, &b.StateReason, &b.LastStartedAtMS,
		&b.DiscordUserID, &b.DiscordUsername, &b.DiscordAvatarURL, &b.WorkspaceID, &b.BuildCommand,
		&b.LogoUpdatedMS, &b.Kind, &b.BlueprintID, &b.BlueprintRevision, &b.ImageChoice, &b.InstallState, &b.InstalledVersion,
		&b.JVMArgs, &b.JVMArgsUpdatedMS, &b.JVMArgsGeneration)
	if err != nil {
		return b, mapErr(err)
	}
	b.NetworkDisabled = netEnabled == 0
	b.AutoBackupOff = autoBackup == 0
	if entry != nil {
		if err := json.Unmarshal([]byte(*entry), &b.Entrypoint); err != nil {
			return b, err
		}
	}
	return b, json.Unmarshal([]byte(argv), &b.Argv)
}

func (db *DB) SetBotDiscordIdentity(ctx context.Context, botID, userID, username, avatarURL string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE bots SET discord_user_id=?,discord_username=?,discord_avatar_url=?,updated_at_ms=? WHERE id=?`, userID, username, avatarURL, nowMS, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nullJSON(v []string) (*string, error) {
	if len(v) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func defaultInt(v, d int64) int64 {
	if v == 0 {
		return d
	}
	return v
}

func (db *DB) CreateBot(ctx context.Context, b domain.Bot) error {
	argv, err := json.Marshal(b.Argv)
	if err != nil {
		return err
	}
	entry, err := nullJSON(b.Entrypoint)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bots (id, owner_id, node_id, name, runtime, image_ref, argv_json,
		memory_bytes, nano_cpus, pids_limit, created_at_ms, updated_at_ms,
		entrypoint_json, source_type, template_id, network_enabled, bandwidth_kbps,
		restart_policy, restart_max_attempts, restart_backoff_initial_ms, restart_backoff_max_ms, workspace_id, build_command,
		kind, blueprint_id, blueprint_revision, image_choice, install_state)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,
			COALESCE(NULLIF(?, ''), (SELECT id FROM workspaces WHERE owner_id = ?2 AND personal = 1)), NULLIF(?, ''),
			?, NULLIF(?, ''), NULLIF(?, 0), ?, ?)`,
		b.ID, b.OwnerID, b.NodeID, b.Name, b.Runtime, b.ImageRef, string(argv), b.MemoryBytes, b.NanoCPUs,
		b.PidsLimit, b.CreatedAtMS, b.UpdatedAtMS,
		entry, defaultStr(b.SourceType, "manual"), b.TemplateID, boolInt(!b.NetworkDisabled), b.BandwidthKbps,
		defaultStr(b.RestartPolicy, domain.RestartOnFailure), defaultInt(b.RestartMaxAttempts, 5),
		defaultInt(b.RestartBackoffInitialMS, 2000), defaultInt(b.RestartBackoffMaxMS, 300000), b.WorkspaceID, b.BuildCommand,
		defaultStr(b.Kind, domain.KindBot), b.BlueprintID, b.BlueprintRevision, b.ImageChoice, defaultStr(b.InstallState, domain.InstallNone))
	return mapErr(err)
}

// GetBot loads one bot including its published ports.
func (db *DB) GetBot(ctx context.Context, id string) (domain.Bot, error) {
	b, err := scanBot(db.QueryRowContext(ctx, `SELECT `+botCols+` FROM bots WHERE id = ?`, id))
	if err != nil {
		return b, err
	}
	if b.Ports, err = db.ListBotPorts(ctx, id); err != nil {
		return b, err
	}
	if b.IsGame() {
		if b.Allocations, err = db.ListBotAllocations(ctx, id); err != nil {
			return b, err
		}
	}
	b.Addons, err = db.ListBotAddons(ctx, id)
	return b, err
}

// ListBots returns bots owned by ownerID, or all bots when ownerID is empty.
func (db *DB) ListBots(ctx context.Context, ownerID string) ([]domain.Bot, error) {
	q := `SELECT ` + botCols + ` FROM bots`
	var args []any
	if ownerID != "" {
		q += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	// Bots created in the same millisecond still list in a stable order.
	// Bots created in the same millisecond still list in a stable order.
	rows, err := db.QueryContext(ctx, q+` ORDER BY created_at_ms, lower(name), id LIMIT 1000`, args...)
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

// visibleBotsSQL selects the ids of bots a user (?) can see: owned, shared
// with them, or in a workspace they are a member of.
const visibleBotsSQL = `SELECT id FROM bots WHERE owner_id = ?1
	UNION SELECT bot_id FROM bot_subusers WHERE user_id = ?1
	UNION SELECT b.id FROM bots b JOIN workspace_members m ON m.workspace_id = b.workspace_id WHERE m.user_id = ?1`

// ListBotsForUser returns bots the user owns, that are shared with them, or
// that are in one of their workspaces.
func (db *DB) ListBotsForUser(ctx context.Context, userID string) ([]domain.Bot, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+botCols+` FROM bots WHERE id IN (`+visibleBotsSQL+`)
		ORDER BY created_at_ms, lower(name), id LIMIT 1000`, userID)
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

// stoppedPredicate is true only when no container can be live: intent is
// stopped and the last observation is not an active state.
const stoppedPredicate = `desired_state = 'stopped' AND observed_state IN ('stopped', 'failed', 'unknown')`

// UpdateBotConfig applies the mutable configuration fields of b, bumping
// generation. It succeeds only if the bot is stopped and still at
// b.Generation (optimistic concurrency).
func (db *DB) UpdateBotConfig(ctx context.Context, b domain.Bot, nowMS int64) error {
	argv, err := json.Marshal(b.Argv)
	if err != nil {
		return err
	}
	entry, err := nullJSON(b.Entrypoint)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE bots SET name = ?, runtime = ?, image_ref = ?, argv_json = ?, entrypoint_json = ?,
		memory_bytes = ?, nano_cpus = ?, pids_limit = ?, network_enabled = ?, bandwidth_kbps = ?,
		restart_policy = ?, restart_max_attempts = ?, restart_backoff_initial_ms = ?, restart_backoff_max_ms = ?,
		auto_backup = ?, build_command = NULLIF(?, ''),
		generation = generation + 1, updated_at_ms = ?
		WHERE id = ? AND generation = ? AND `+stoppedPredicate,
		b.Name, b.Runtime, b.ImageRef, string(argv), entry, b.MemoryBytes, b.NanoCPUs, b.PidsLimit,
		boolInt(!b.NetworkDisabled), b.BandwidthKbps,
		defaultStr(b.RestartPolicy, domain.RestartOnFailure), b.RestartMaxAttempts, b.RestartBackoffInitialMS, b.RestartBackoffMaxMS,
		boolInt(!b.AutoBackupOff), b.BuildCommand, nowMS, b.ID, b.Generation)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	return db.explainFailedUpdate(ctx, b.ID)
}

// SetBotPorts replaces a bot's published ports and bumps its generation in one
// transaction, only while the bot is stopped and at generation gen. A host
// port taken by another bot returns domain.ErrConflict.
func (db *DB) SetBotPorts(ctx context.Context, botID string, gen int64, ports []domain.BotPort, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE bots SET generation = generation + 1, updated_at_ms = ?
		WHERE id = ? AND generation = ? AND `+stoppedPredicate, nowMS, botID, gen)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		tx.Rollback()
		return db.explainFailedUpdate(ctx, botID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_ports WHERE bot_id = ?`, botID); err != nil {
		return err
	}
	for _, p := range ports {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_ports (bot_id, container_port, host_port, protocol, host_ip, created_at_ms)
			VALUES (?,?,?,?,?,?)`, botID, p.ContainerPort, p.HostPort, p.Protocol, p.HostIP, nowMS); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}

func (db *DB) explainFailedUpdate(ctx context.Context, id string) error {
	cur, err := db.GetBot(ctx, id)
	if err != nil {
		return err
	}
	if cur.DesiredState != domain.DesiredStopped || (cur.ObservedState != "stopped" && cur.ObservedState != "failed" && cur.ObservedState != "unknown") {
		return domain.ErrNotStopped
	}
	return domain.ErrConflict
}

// MarkBotDeleted records deletion intent. Teardown then removes the row.
func (db *DB) MarkBotDeleted(ctx context.Context, id string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE bots SET desired_state = 'deleted',
		generation = CASE WHEN desired_state = 'deleted' THEN generation ELSE generation + 1 END,
		updated_at_ms = ? WHERE id = ?`, nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteBotRow removes a bot that has already been marked deleted.
func (db *DB) DeleteBotRow(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Allocations return to the pool with the server.
	if _, err := tx.ExecContext(ctx, `UPDATE allocations SET bot_id = NULL, is_primary = 0
		WHERE bot_id = ? AND EXISTS (SELECT 1 FROM bots WHERE id = ? AND desired_state = 'deleted')`, id, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bots WHERE id = ? AND desired_state = 'deleted'`, id); err != nil {
		return err
	}
	return tx.Commit()
}
