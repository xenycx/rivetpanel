package sqlite

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SetSiteAutoBackup turns scheduled backups of a site on or off.
func (db *DB) SetSiteAutoBackup(ctx context.Context, siteID string, on bool, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE sites SET auto_backup = ?, updated_at_ms = ? WHERE id = ?`, boolInt(on), nowMS, siteID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const siteBackupCols = `id, site_id, kind, release_id, label, files, bytes, sha256, created_by, created_at_ms`

func scanSiteBackup(row interface{ Scan(...any) error }) (domain.SiteBackup, error) {
	var b domain.SiteBackup
	err := row.Scan(&b.ID, &b.SiteID, &b.Kind, &b.ReleaseID, &b.Label, &b.Files, &b.Bytes, &b.SHA256, &b.CreatedBy, &b.CreatedAtMS)
	return b, mapErr(err)
}

// InsertSiteBackup records a finished site backup.
func (db *DB) InsertSiteBackup(ctx context.Context, b domain.SiteBackup) error {
	_, err := db.ExecContext(ctx, `INSERT INTO site_backups (`+siteBackupCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.SiteID, b.Kind, b.ReleaseID, b.Label, b.Files, b.Bytes, b.SHA256, b.CreatedBy, b.CreatedAtMS)
	return mapErr(err)
}

// ListSiteBackups returns a site's backups, newest first.
func (db *DB) ListSiteBackups(ctx context.Context, siteID string) ([]domain.SiteBackup, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+siteBackupCols+` FROM site_backups WHERE site_id = ? ORDER BY created_at_ms DESC, id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SiteBackup{}
	for rows.Next() {
		b, err := scanSiteBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetSiteBackup loads one backup of a site.
func (db *DB) GetSiteBackup(ctx context.Context, siteID, id string) (domain.SiteBackup, error) {
	return scanSiteBackup(db.QueryRowContext(ctx, `SELECT `+siteBackupCols+` FROM site_backups WHERE site_id = ? AND id = ?`, siteID, id))
}

// DeleteSiteBackup removes a backup row.
func (db *DB) DeleteSiteBackup(ctx context.Context, siteID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM site_backups WHERE site_id = ? AND id = ?`, siteID, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
