package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const extCols = `id, source, active_version, state, granted_json, last_error, failure_count, installed_by, created_at_ms, updated_at_ms`

func scanExtension(row interface{ Scan(...any) error }) (domain.Extension, error) {
	var e domain.Extension
	var granted string
	var by sql.NullString
	if err := row.Scan(&e.ID, &e.Source, &e.ActiveVersion, &e.State, &granted, &e.LastError, &e.FailureCount, &by, &e.CreatedAtMS, &e.UpdatedAtMS); err != nil {
		return e, mapErr(err)
	}
	e.InstalledBy = by.String
	_ = json.Unmarshal([]byte(granted), &e.Granted)
	return e, nil
}

// ListExtensions returns every installed extension.
func (db *DB) ListExtensions(ctx context.Context) ([]domain.Extension, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+extCols+` FROM extensions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Extension
	for rows.Next() {
		e, err := scanExtension(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetExtension returns one installed extension.
func (db *DB) GetExtension(ctx context.Context, id string) (domain.Extension, error) {
	return scanExtension(db.QueryRowContext(ctx, `SELECT `+extCols+` FROM extensions WHERE id = ?`, id))
}

// PutExtension inserts or replaces an extension's row.
func (db *DB) PutExtension(ctx context.Context, e domain.Extension) error {
	granted, _ := json.Marshal(nonNil(e.Granted))
	_, err := db.ExecContext(ctx, `INSERT INTO extensions (`+extCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET source = excluded.source, active_version = excluded.active_version,
			state = excluded.state, granted_json = excluded.granted_json, last_error = excluded.last_error,
			failure_count = excluded.failure_count, updated_at_ms = excluded.updated_at_ms`,
		e.ID, e.Source, e.ActiveVersion, e.State, string(granted), e.LastError, e.FailureCount, nullStr(e.InstalledBy), e.CreatedAtMS, e.UpdatedAtMS)
	return mapErr(err)
}

// SetExtensionState changes the state and error of an extension.
func (db *DB) SetExtensionState(ctx context.Context, id, state, lastError string, failures int, now int64) error {
	res, err := db.ExecContext(ctx, `UPDATE extensions SET state = ?, last_error = ?, failure_count = ?, updated_at_ms = ? WHERE id = ?`,
		state, lastError, failures, now, id)
	return affected(res, err)
}

// DeleteExtension removes an extension and everything it stored: settings,
// private data, enablements, consents, jobs (by cascade) and its bundles.
func (db *DB) DeleteExtension(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_versions WHERE extension_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM extensions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const extVersionCols = `extension_id, version, source, sha256, manifest_json, key_id, uploaded_by, created_at_ms`

func scanExtVersion(row interface{ Scan(...any) error }, withBundle bool) (domain.ExtensionVersion, error) {
	var v domain.ExtensionVersion
	var by sql.NullString
	dest := []any{&v.ExtensionID, &v.Version, &v.Source, &v.SHA256, &v.ManifestJSON, &v.KeyID, &by, &v.CreatedAtMS}
	if withBundle {
		dest = append(dest, &v.Bundle)
	}
	if err := row.Scan(dest...); err != nil {
		return v, mapErr(err)
	}
	v.UploadedBy = by.String
	return v, nil
}

// PutExtensionVersion stores a bundle, replacing a same-version upload, and
// keeps only the newest `keep` versions besides `protect` (the active one).
func (db *DB) PutExtensionVersion(ctx context.Context, v domain.ExtensionVersion, protect string, keep int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO extension_versions (`+extVersionCols+`, bundle) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(extension_id, version) DO UPDATE SET source = excluded.source, sha256 = excluded.sha256,
			manifest_json = excluded.manifest_json, key_id = excluded.key_id, uploaded_by = excluded.uploaded_by,
			created_at_ms = excluded.created_at_ms, bundle = excluded.bundle`,
		v.ExtensionID, v.Version, v.Source, v.SHA256, v.ManifestJSON, v.KeyID, nullStr(v.UploadedBy), v.CreatedAtMS, v.Bundle); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_versions WHERE extension_id = ? AND version <> ? AND version <> ?
		AND version NOT IN (SELECT version FROM extension_versions WHERE extension_id = ? ORDER BY created_at_ms DESC LIMIT ?)`,
		v.ExtensionID, protect, v.Version, v.ExtensionID, keep); err != nil {
		return err
	}
	return tx.Commit()
}

// ListExtensionVersions returns an extension's stored versions (no bundles), newest first.
func (db *DB) ListExtensionVersions(ctx context.Context, id string) ([]domain.ExtensionVersion, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+extVersionCols+` FROM extension_versions WHERE extension_id = ? ORDER BY created_at_ms DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ExtensionVersion
	for rows.Next() {
		v, err := scanExtVersion(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListPendingExtensionIDs returns ids that have stored versions but are not installed.
func (db *DB) ListPendingExtensionIDs(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT extension_id FROM extension_versions
		WHERE extension_id NOT IN (SELECT id FROM extensions) ORDER BY extension_id`)
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

// GetExtensionVersion returns one stored version with its bundle.
func (db *DB) GetExtensionVersion(ctx context.Context, id, version string) (domain.ExtensionVersion, error) {
	return scanExtVersion(db.QueryRowContext(ctx, `SELECT `+extVersionCols+`, bundle FROM extension_versions
		WHERE extension_id = ? AND version = ?`, id, version), true)
}

// DeleteExtensionVersion removes one stored (inactive) version.
func (db *DB) DeleteExtensionVersion(ctx context.Context, id, version string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM extension_versions WHERE extension_id = ? AND version = ?`, id, version)
	return affected(res, err)
}

// Publishers.

// ListExtensionPublishers returns every publisher key, revoked ones included.
func (db *DB) ListExtensionPublishers(ctx context.Context) ([]domain.ExtensionPublisher, error) {
	rows, err := db.QueryContext(ctx, `SELECT key_id, name, public_key, created_by, created_at_ms, revoked_at_ms
		FROM extension_publishers ORDER BY created_at_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ExtensionPublisher
	for rows.Next() {
		var p domain.ExtensionPublisher
		var by sql.NullString
		var rev sql.NullInt64
		if err := rows.Scan(&p.KeyID, &p.Name, &p.PublicKey, &by, &p.CreatedAtMS, &rev); err != nil {
			return nil, err
		}
		p.CreatedBy = by.String
		if rev.Valid {
			p.RevokedAtMS = &rev.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// InsertExtensionPublisher trusts a key; an existing key id is ErrConflict.
func (db *DB) InsertExtensionPublisher(ctx context.Context, p domain.ExtensionPublisher) error {
	_, err := db.ExecContext(ctx, `INSERT INTO extension_publishers (key_id, name, public_key, created_by, created_at_ms) VALUES (?,?,?,?,?)`,
		p.KeyID, p.Name, p.PublicKey, nullStr(p.CreatedBy), p.CreatedAtMS)
	return mapErr(err)
}

// RevokeExtensionPublisher marks a key revoked.
func (db *DB) RevokeExtensionPublisher(ctx context.Context, keyID string, now int64) error {
	res, err := db.ExecContext(ctx, `UPDATE extension_publishers SET revoked_at_ms = ? WHERE key_id = ? AND revoked_at_ms IS NULL`, now, keyID)
	return affected(res, err)
}

// Settings.

// ListExtensionSettings returns an extension's stored settings.
func (db *DB) ListExtensionSettings(ctx context.Context, id string) ([]domain.ExtensionSetting, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value_json, secret_cipher, secret_nonce, secret_key_id
		FROM extension_settings WHERE extension_id = ? ORDER BY key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ExtensionSetting
	for rows.Next() {
		var s domain.ExtensionSetting
		var val, kid sql.NullString
		if err := rows.Scan(&s.Key, &val, &s.SecretCipher, &s.SecretNonce, &kid); err != nil {
			return nil, err
		}
		s.ValueJSON, s.SecretKeyID = val.String, kid.String
		out = append(out, s)
	}
	return out, rows.Err()
}

// PutExtensionSetting stores one setting value (plain or sealed).
func (db *DB) PutExtensionSetting(ctx context.Context, id string, s domain.ExtensionSetting, now int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO extension_settings (extension_id, key, value_json, secret_cipher, secret_nonce, secret_key_id, updated_at_ms)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(extension_id, key) DO UPDATE SET value_json = excluded.value_json,
			secret_cipher = excluded.secret_cipher, secret_nonce = excluded.secret_nonce, secret_key_id = excluded.secret_key_id,
			updated_at_ms = excluded.updated_at_ms`,
		id, s.Key, nullStr(s.ValueJSON), s.SecretCipher, s.SecretNonce, nullStr(s.SecretKeyID), now)
	return mapErr(err)
}

// DeleteExtensionSetting clears one setting.
func (db *DB) DeleteExtensionSetting(ctx context.Context, id, key string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM extension_settings WHERE extension_id = ? AND key = ?`, id, key)
	return err
}

// Private storage.

// GetExtensionKV reads a live value.
func (db *DB) GetExtensionKV(ctx context.Context, id, scope, key string, now int64) ([]byte, error) {
	var v []byte
	err := db.QueryRowContext(ctx, `SELECT value FROM extension_kv WHERE extension_id = ? AND scope = ? AND key = ?
		AND (expires_at_ms IS NULL OR expires_at_ms > ?)`, id, scope, key, now).Scan(&v)
	return v, mapErr(err)
}

// PutExtensionKV stores a value if the extension stays within quota bytes.
func (db *DB) PutExtensionKV(ctx context.Context, id, scope, key string, value []byte, expiresAt, now, quota int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_kv WHERE extension_id = ? AND expires_at_ms IS NOT NULL AND expires_at_ms <= ?`, id, now); err != nil {
		return err
	}
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(length(value) + length(key) + length(scope)), 0) FROM extension_kv
		WHERE extension_id = ? AND NOT (scope = ? AND key = ?)`, id, scope, key).Scan(&used); err != nil {
		return err
	}
	if used+int64(len(value)+len(key)+len(scope)) > quota {
		return domain.ErrQuota
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extension_kv (extension_id, scope, key, value, expires_at_ms) VALUES (?,?,?,?,?)
		ON CONFLICT(extension_id, scope, key) DO UPDATE SET value = excluded.value, expires_at_ms = excluded.expires_at_ms`,
		id, scope, key, value, nullInt(expiresAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteExtensionKV removes a value.
func (db *DB) DeleteExtensionKV(ctx context.Context, id, scope, key string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM extension_kv WHERE extension_id = ? AND scope = ? AND key = ?`, id, scope, key)
	return err
}

// Per-bot enablement and per-user consent.

// ExtensionBotEnabled reports whether the extension is switched on for a bot.
func (db *DB) ExtensionBotEnabled(ctx context.Context, id, botID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM extension_bot_enablements WHERE extension_id = ? AND bot_id = ?`, id, botID).Scan(&n)
	return n > 0, err
}

// SetExtensionBotEnabled switches the extension on or off for a bot.
func (db *DB) SetExtensionBotEnabled(ctx context.Context, id, botID, by string, on bool, now int64) error {
	var err error
	if on {
		_, err = db.ExecContext(ctx, `INSERT INTO extension_bot_enablements (extension_id, bot_id, enabled_by, created_at_ms) VALUES (?,?,?,?)
			ON CONFLICT DO NOTHING`, id, botID, nullStr(by), now)
	} else {
		_, err = db.ExecContext(ctx, `DELETE FROM extension_bot_enablements WHERE extension_id = ? AND bot_id = ?`, id, botID)
	}
	return mapErr(err)
}

// ListExtensionBots returns the bots an extension is switched on for.
func (db *DB) ListExtensionBots(ctx context.Context, id string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.bot_id FROM extension_bot_enablements e JOIN bots b ON b.id = e.bot_id
		WHERE e.extension_id = ? AND b.desired_state <> 'deleted' ORDER BY e.created_at_ms`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetExtensionConsent returns the scopes a user allowed (ErrNotFound: none).
func (db *DB) GetExtensionConsent(ctx context.Context, id, userID string) ([]string, error) {
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT scopes_json FROM extension_user_consents WHERE extension_id = ? AND user_id = ?`, id, userID).Scan(&raw); err != nil {
		return nil, mapErr(err)
	}
	var scopes []string
	_ = json.Unmarshal([]byte(raw), &scopes)
	return scopes, nil
}

// SetExtensionConsent records (or with no scopes, withdraws) a user's consent.
func (db *DB) SetExtensionConsent(ctx context.Context, id, userID string, scopes []string, now int64) error {
	if len(scopes) == 0 {
		_, err := db.ExecContext(ctx, `DELETE FROM extension_user_consents WHERE extension_id = ? AND user_id = ?`, id, userID)
		return err
	}
	raw, _ := json.Marshal(scopes)
	_, err := db.ExecContext(ctx, `INSERT INTO extension_user_consents (extension_id, user_id, scopes_json, created_at_ms) VALUES (?,?,?,?)
		ON CONFLICT(extension_id, user_id) DO UPDATE SET scopes_json = excluded.scopes_json`, id, userID, string(raw), now)
	return mapErr(err)
}

// Jobs.

// ListExtensionJobs returns job bookkeeping for one extension ("" for all).
func (db *DB) ListExtensionJobs(ctx context.Context, id string) ([]domain.ExtensionJob, error) {
	rows, err := db.QueryContext(ctx, `SELECT extension_id, job, next_run_ms, coalesce(last_run_ms, 0), last_status, last_error
		FROM extension_jobs WHERE ? = '' OR extension_id = ? ORDER BY next_run_ms`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ExtensionJob
	for rows.Next() {
		var j domain.ExtensionJob
		if err := rows.Scan(&j.ExtensionID, &j.Job, &j.NextRunMS, &j.LastRunMS, &j.LastStatus, &j.LastError); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// PutExtensionJob records a job's schedule and last result.
func (db *DB) PutExtensionJob(ctx context.Context, j domain.ExtensionJob) error {
	_, err := db.ExecContext(ctx, `INSERT INTO extension_jobs (extension_id, job, next_run_ms, last_run_ms, last_status, last_error) VALUES (?,?,?,?,?,?)
		ON CONFLICT(extension_id, job) DO UPDATE SET next_run_ms = excluded.next_run_ms, last_run_ms = excluded.last_run_ms,
			last_status = excluded.last_status, last_error = excluded.last_error`,
		j.ExtensionID, j.Job, j.NextRunMS, nullInt(j.LastRunMS), j.LastStatus, j.LastError)
	return mapErr(err)
}

// DeleteExtensionJobsExcept drops bookkeeping for jobs a new version no longer declares.
func (db *DB) DeleteExtensionJobsExcept(ctx context.Context, id string, keep []string) error {
	raw, _ := json.Marshal(nonNil(keep))
	_, err := db.ExecContext(ctx, `DELETE FROM extension_jobs WHERE extension_id = ? AND job NOT IN (SELECT value FROM json_each(?))`, id, string(raw))
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
