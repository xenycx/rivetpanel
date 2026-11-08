package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func (db *DB) setLogo(ctx context.Context, table, id string, l *domain.Logo, nowMS int64) error {
	var res sql.Result
	var err error
	if l == nil {
		res, err = db.ExecContext(ctx, `UPDATE `+table+` SET logo = NULL, logo_type = NULL, logo_updated_at_ms = NULL WHERE id = ?`, id)
	} else {
		res, err = db.ExecContext(ctx, `UPDATE `+table+` SET logo = ?, logo_type = ?, logo_updated_at_ms = ? WHERE id = ?`, l.Data, l.ContentType, nowMS, id)
	}
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) getLogo(ctx context.Context, table, id string) (domain.Logo, error) {
	var l domain.Logo
	err := db.QueryRowContext(ctx, `SELECT logo, logo_type, logo_updated_at_ms FROM `+table+` WHERE id = ? AND logo IS NOT NULL`, id).
		Scan(&l.Data, &l.ContentType, &l.UpdatedAtMS)
	return l, mapErr(err)
}

// SetBotLogo stores (nil: removes) a bot's custom logo.
func (db *DB) SetBotLogo(ctx context.Context, botID string, l *domain.Logo, nowMS int64) error {
	return db.setLogo(ctx, "bots", botID, l, nowMS)
}

// GetBotLogo returns a bot's custom logo (domain.ErrNotFound when none).
func (db *DB) GetBotLogo(ctx context.Context, botID string) (domain.Logo, error) {
	return db.getLogo(ctx, "bots", botID)
}

// SetSiteLogo stores (nil: removes) a site's custom logo.
func (db *DB) SetSiteLogo(ctx context.Context, siteID string, l *domain.Logo, nowMS int64) error {
	return db.setLogo(ctx, "sites", siteID, l, nowMS)
}

// GetSiteLogo returns a site's custom logo (domain.ErrNotFound when none).
func (db *DB) GetSiteLogo(ctx context.Context, siteID string) (domain.Logo, error) {
	return db.getLogo(ctx, "sites", siteID)
}

// SetWorkspaceLogo stores (nil: removes) a workspace's custom logo.
func (db *DB) SetWorkspaceLogo(ctx context.Context, workspaceID string, l *domain.Logo, nowMS int64) error {
	return db.setLogo(ctx, "workspaces", workspaceID, l, nowMS)
}

// GetWorkspaceLogo returns a workspace's custom logo (domain.ErrNotFound when none).
func (db *DB) GetWorkspaceLogo(ctx context.Context, workspaceID string) (domain.Logo, error) {
	return db.getLogo(ctx, "workspaces", workspaceID)
}

// logoVariantTables are the tables whose rows carry a generated-picture
// choice (migration 0066).
var logoVariantTables = map[string]bool{"bots": true, "sites": true, "workspaces": true, "users": true}

// NextLogoVariant advances the generated-picture choice of one bot, site,
// workspace or account and returns the new value.
func (db *DB) NextLogoVariant(ctx context.Context, table, id string) (int64, error) {
	if !logoVariantTables[table] {
		return 0, domain.Invalid("unknown item type")
	}
	var v int64
	err := db.QueryRowContext(ctx, `UPDATE `+table+` SET logo_variant = logo_variant + 1 WHERE id = ? RETURNING logo_variant`, id).Scan(&v)
	return v, mapErr(err)
}
