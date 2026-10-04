package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const siteCols = `s.id, s.workspace_id, s.owner_id, s.name, s.slug, s.spa, s.clean_urls, s.current_release, s.disabled,
	s.repo_full_name, s.repo_branch, s.repo_root, s.repo_token_user, s.created_at_ms, s.updated_at_ms,
	s.bot_id, s.mode, s.page_title, s.page_description, s.page_theme, s.page_accent, s.page_html, s.page_css, s.widgets_public,
	s.domain_id, COALESCE(bd.domain, ''), COALESCE(u.email, ''), COALESCE(w.name, ''),
	(SELECT count(*) FROM site_domains d WHERE d.site_id = s.id),
	COALESCE((SELECT r.bytes FROM site_releases r WHERE r.id = s.current_release), 0), COALESCE(s.logo_updated_at_ms, 0)`

const siteFrom = ` FROM sites s LEFT JOIN users u ON u.id = s.owner_id LEFT JOIN workspaces w ON w.id = s.workspace_id
	LEFT JOIN site_base_domains bd ON bd.id = s.domain_id`

func scanSite(row interface{ Scan(...any) error }) (domain.Site, error) {
	var s domain.Site
	var spa, clean, disabled, widgetsPublic int
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.OwnerID, &s.Name, &s.Slug, &spa, &clean, &s.CurrentRelease, &disabled,
		&s.RepoFullName, &s.RepoBranch, &s.RepoRoot, &s.RepoTokenUser, &s.CreatedAtMS, &s.UpdatedAtMS,
		&s.BotID, &s.Mode, &s.PageTitle, &s.PageDescription, &s.PageTheme, &s.PageAccent, &s.PageHTML, &s.PageCSS, &widgetsPublic,
		&s.DomainID, &s.BaseDomain, &s.OwnerEmail, &s.WorkspaceName, &s.Domains, &s.ReleaseBytes, &s.LogoUpdatedMS)
	s.SPA, s.CleanURLs, s.Disabled, s.WidgetsPublic = spa == 1, clean == 1, disabled == 1, widgetsPublic == 1
	return s, mapErr(err)
}

func (db *DB) listSites(ctx context.Context, where string, args ...any) ([]domain.Site, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+siteCols+siteFrom+` `+where+` ORDER BY lower(s.name), s.created_at_ms LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Site
	for rows.Next() {
		s, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreateSite inserts a site; a slug taken under the same base domain returns
// domain.ErrConflict.
func (db *DB) CreateSite(ctx context.Context, s domain.Site) error {
	if s.Mode == "" {
		s.Mode = "files"
	}
	if s.PageTheme == "" {
		s.PageTheme = "midnight"
	}
	if s.PageAccent == "" {
		s.PageAccent = "#5865f2"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO sites (id, workspace_id, owner_id, bot_id, name, slug, domain_id, spa, clean_urls, mode,
		page_title, page_description, page_theme, page_accent, page_html, page_css, widgets_public, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, s.ID, s.WorkspaceID, s.OwnerID, s.BotID, s.Name, s.Slug, s.DomainID, boolInt(s.SPA), boolInt(s.CleanURLs), s.Mode,
		s.PageTitle, s.PageDescription, s.PageTheme, s.PageAccent, s.PageHTML, s.PageCSS, boolInt(s.WidgetsPublic), s.CreatedAtMS, s.UpdatedAtMS)
	return mapErr(err)
}

// GetSite returns one site.
func (db *DB) GetSite(ctx context.Context, id string) (domain.Site, error) {
	return scanSite(db.QueryRowContext(ctx, `SELECT `+siteCols+siteFrom+` WHERE s.id = ?`, id))
}

// GetSiteForBot returns the single public site attached to a bot.
func (db *DB) GetSiteForBot(ctx context.Context, botID string) (domain.Site, error) {
	return scanSite(db.QueryRowContext(ctx, `SELECT `+siteCols+siteFrom+` WHERE s.bot_id = ?`, botID))
}

// ListSitesForUser returns the sites in the workspaces userID belongs to.
func (db *DB) ListSitesForUser(ctx context.Context, userID string) ([]domain.Site, error) {
	return db.listSites(ctx, `WHERE s.workspace_id IN (SELECT workspace_id FROM workspace_members WHERE user_id = ?)`, userID)
}

// ListAllSites returns every site (administrators).
func (db *DB) ListAllSites(ctx context.Context) ([]domain.Site, error) {
	return db.listSites(ctx, ``)
}

// ListWorkspaceSites returns the sites in one workspace.
func (db *DB) ListWorkspaceSites(ctx context.Context, workspaceID string) ([]domain.Site, error) {
	return db.listSites(ctx, `WHERE s.workspace_id = ?`, workspaceID)
}

// CountOwnedSites counts the sites an account created.
func (db *DB) CountOwnedSites(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE owner_id = ?`, userID).Scan(&n)
	return n, err
}

// UpdateSite stores the mutable settings of a site. Moving to an address
// taken under the same base domain returns domain.ErrConflict.
func (db *DB) UpdateSite(ctx context.Context, s domain.Site) error {
	res, err := db.ExecContext(ctx, `UPDATE sites SET name = ?, slug = ?, domain_id = ?, spa = ?, clean_urls = ?, repo_full_name = ?, repo_branch = ?, repo_root = ?,
		repo_token_user = ?, workspace_id = ?, mode = ?, page_title = ?, page_description = ?, page_theme = ?, page_accent = ?,
		page_html = ?, page_css = ?, widgets_public = ?, updated_at_ms = ? WHERE id = ?`,
		s.Name, s.Slug, s.DomainID, boolInt(s.SPA), boolInt(s.CleanURLs), s.RepoFullName, s.RepoBranch, s.RepoRoot, s.RepoTokenUser, s.WorkspaceID,
		s.Mode, s.PageTitle, s.PageDescription, s.PageTheme, s.PageAccent, s.PageHTML, s.PageCSS, boolInt(s.WidgetsPublic), s.UpdatedAtMS, s.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetSiteDisabled suspends or restores a site.
func (db *DB) SetSiteDisabled(ctx context.Context, id string, disabled bool, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE sites SET disabled = ?, updated_at_ms = ? WHERE id = ?`, boolInt(disabled), nowMS, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ActivateRelease switches the release a site serves. The release must
// belong to the site.
func (db *DB) ActivateRelease(ctx context.Context, siteID, releaseID string, nowMS int64) error {
	res, err := db.ExecContext(ctx, `UPDATE sites SET current_release = ?1, updated_at_ms = ?2
		WHERE id = ?3 AND EXISTS (SELECT 1 FROM site_releases WHERE id = ?1 AND site_id = ?3)`, releaseID, nowMS, siteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteSite removes a site with its releases and domains (rows only).
func (db *DB) DeleteSite(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// InsertRelease records a new release.
func (db *DB) InsertRelease(ctx context.Context, r domain.SiteRelease) error {
	_, err := db.ExecContext(ctx, `INSERT INTO site_releases (id, site_id, source, source_label, files, bytes, actor_id, created_at_ms)
		VALUES (?,?,?,?,?,?,?,?)`, r.ID, r.SiteID, r.Source, r.SourceLabel, r.Files, r.Bytes, r.ActorID, r.CreatedAtMS)
	return mapErr(err)
}

// ListReleases returns a site's releases, newest first.
func (db *DB) ListReleases(ctx context.Context, siteID string) ([]domain.SiteRelease, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.id, r.site_id, r.source, r.source_label, r.files, r.bytes, r.actor_id, u.email, r.created_at_ms
		FROM site_releases r LEFT JOIN users u ON u.id = r.actor_id WHERE r.site_id = ? ORDER BY r.created_at_ms DESC, r.rowid DESC LIMIT 100`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SiteRelease
	for rows.Next() {
		var r domain.SiteRelease
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Source, &r.SourceLabel, &r.Files, &r.Bytes, &r.ActorID, &r.ActorEmail, &r.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRelease removes a release row that is not being served.
func (db *DB) DeleteRelease(ctx context.Context, siteID, releaseID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM site_releases WHERE id = ?1 AND site_id = ?2
		AND NOT EXISTS (SELECT 1 FROM sites WHERE id = ?2 AND current_release = ?1)`, releaseID, siteID)
	return err
}

// ClaimDomain attaches a domain to a site. A domain verified for another
// site is refused (ErrConflict); an unverified claim by another site is
// replaced, so nobody can reserve a domain they cannot prove they control.
func (db *DB) ClaimDomain(ctx context.Context, d domain.SiteDomain) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var site string
	var verified *int64
	err = tx.QueryRowContext(ctx, `SELECT site_id, verified_at_ms FROM site_domains WHERE domain = ?`, d.Domain).Scan(&site, &verified)
	switch err = mapErr(err); {
	case err == nil && site == d.SiteID:
		return domain.Invalid("this site already has that domain")
	case err == nil && verified != nil:
		return domain.ErrConflict
	case err == nil:
		if _, err := tx.ExecContext(ctx, `DELETE FROM site_domains WHERE domain = ?`, d.Domain); err != nil {
			return err
		}
	case err != domain.ErrNotFound:
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_domains (domain, site_id, token, created_at_ms) VALUES (?,?,?,?)`,
		d.Domain, d.SiteID, d.Token, d.CreatedAtMS); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

const domainCols = `domain, site_id, token, verified_at_ms, last_checked_at_ms, last_error, created_at_ms`

func scanDomain(row interface{ Scan(...any) error }) (domain.SiteDomain, error) {
	var d domain.SiteDomain
	err := row.Scan(&d.Domain, &d.SiteID, &d.Token, &d.VerifiedAtMS, &d.LastCheckedAtMS, &d.LastError, &d.CreatedAtMS)
	return d, mapErr(err)
}

// ListDomains returns a site's domains.
func (db *DB) ListDomains(ctx context.Context, siteID string) ([]domain.SiteDomain, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+domainCols+` FROM site_domains WHERE site_id = ? ORDER BY domain LIMIT 100`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SiteDomain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListVerifiedDomains returns every verified domain (for periodic re-checks).
func (db *DB) ListVerifiedDomains(ctx context.Context) ([]domain.SiteDomain, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+domainCols+` FROM site_domains WHERE verified_at_ms IS NOT NULL LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SiteDomain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDomain returns one domain of a site.
func (db *DB) GetDomain(ctx context.Context, siteID, name string) (domain.SiteDomain, error) {
	return scanDomain(db.QueryRowContext(ctx, `SELECT `+domainCols+` FROM site_domains WHERE site_id = ? AND domain = ?`, siteID, name))
}

// RecordDomainCheck stores the outcome of a DNS verification. verifiedAt nil
// keeps the current verification state (a failed re-check only reports).
func (db *DB) RecordDomainCheck(ctx context.Context, name string, verifiedAt *int64, errMsg *string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE site_domains SET verified_at_ms = COALESCE(?, verified_at_ms), last_error = ?, last_checked_at_ms = ?
		WHERE domain = ?`, verifiedAt, errMsg, nowMS, name)
	return err
}

// DeleteDomain detaches a domain from a site.
func (db *DB) DeleteDomain(ctx context.Context, siteID, name string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM site_domains WHERE site_id = ? AND domain = ?`, siteID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SiteRoutes returns what the sites listener serves: slug routes under each
// base domain, verified custom domains, and the base domains being served.
func (db *DB) SiteRoutes(ctx context.Context) (domain.SiteRouteTable, error) {
	t := domain.SiteRouteTable{BySlug: map[string]domain.SiteRoute{}, ByDomain: map[string]domain.SiteRoute{}}
	bases, err := db.ListBaseDomains(ctx)
	if err != nil {
		return t, err
	}
	for _, b := range bases {
		if b.Serving() {
			t.Bases = append(t.Bases, b)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT s.id, s.slug, COALESCE(s.domain_id, ''), COALESCE(s.current_release, ''), s.spa, s.clean_urls, s.disabled, s.mode, d.domain
		FROM sites s LEFT JOIN site_domains d ON d.site_id = s.id AND d.verified_at_ms IS NOT NULL`)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.SiteRoute
		var slug, domainID string
		var host *string
		var spa, clean, disabled int
		if err := rows.Scan(&r.SiteID, &slug, &domainID, &r.Release, &spa, &clean, &disabled, &r.Mode, &host); err != nil {
			return t, err
		}
		r.SPA, r.CleanURLs, r.Disabled = spa == 1, clean == 1, disabled == 1
		if domainID != "" {
			t.BySlug[domain.SlugKey(domainID, slug)] = r
		}
		if host != nil {
			t.ByDomain[*host] = r
		}
	}
	return t, rows.Err()
}

// ---- base domains ----

const baseDomainCols = `b.id, b.domain, b.enabled, b.is_primary, b.label, b.dns_target, b.from_config, b.token, b.verified_at_ms,
	b.last_checked_at_ms, b.last_error, b.created_at_ms, b.updated_at_ms, (SELECT count(*) FROM sites s WHERE s.domain_id = b.id)`

func scanBaseDomain(row interface{ Scan(...any) error }) (domain.SiteBaseDomain, error) {
	var d domain.SiteBaseDomain
	var enabled, primary, fromConfig int
	err := row.Scan(&d.ID, &d.Domain, &enabled, &primary, &d.Label, &d.DNSTarget, &fromConfig, &d.Token, &d.VerifiedAtMS,
		&d.LastCheckedAtMS, &d.LastError, &d.CreatedAtMS, &d.UpdatedAtMS, &d.Sites)
	d.Enabled, d.Primary, d.FromConfig = enabled == 1, primary == 1, fromConfig == 1
	return d, mapErr(err)
}

// ListBaseDomains returns every base domain, the primary first.
func (db *DB) ListBaseDomains(ctx context.Context) ([]domain.SiteBaseDomain, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+baseDomainCols+` FROM site_base_domains b ORDER BY b.is_primary DESC, b.domain LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SiteBaseDomain
	for rows.Next() {
		d, err := scanBaseDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetBaseDomain returns one base domain by id or by domain name.
func (db *DB) GetBaseDomain(ctx context.Context, idOrName string) (domain.SiteBaseDomain, error) {
	return scanBaseDomain(db.QueryRowContext(ctx, `SELECT `+baseDomainCols+` FROM site_base_domains b WHERE b.id = ?1 OR b.domain = ?1`, idOrName))
}

// EnsureBaseDomains records the configured base domains (seeds[0] is the
// sites base URL's host): missing ones are added verified, ones no longer
// configured lose their configured status, the first seed becomes primary
// when there is none, and sites without a base domain get the primary one.
// An administrator's choices (enabled, primary, label) are kept.
func (db *DB) EnsureBaseDomains(ctx context.Context, seeds []domain.SiteBaseDomain, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(seeds) > 0 {
		// A configured primary follows RIVET_SITES_BASE_URL: when its host
		// changes, the domain (and every site under it) moves along, as it did
		// before base domains could be managed.
		seeded := map[string]bool{}
		for _, d := range seeds {
			seeded[d.Domain] = true
		}
		var id, name string
		var fromConfig, taken int
		err := tx.QueryRowContext(ctx, `SELECT id, domain, from_config FROM site_base_domains WHERE is_primary = 1`).Scan(&id, &name, &fromConfig)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && fromConfig == 1 && !seeded[name] {
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_base_domains WHERE domain = ?`, seeds[0].Domain).Scan(&taken); err != nil {
				return err
			}
			if taken == 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE site_base_domains SET domain = ?, updated_at_ms = ? WHERE id = ?`, seeds[0].Domain, nowMS, id); err != nil {
					return mapErr(err)
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE site_base_domains SET from_config = 0 WHERE from_config = 1`); err != nil {
		return err
	}
	for _, d := range seeds {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_base_domains (id, domain, from_config, token, verified_at_ms, created_at_ms, updated_at_ms)
			VALUES (?1, ?2, 1, ?3, ?4, ?4, ?4)
			ON CONFLICT (domain) DO UPDATE SET from_config = 1, verified_at_ms = COALESCE(verified_at_ms, ?4), last_error = NULL`,
			d.ID, d.Domain, d.Token, nowMS); err != nil {
			return mapErr(err)
		}
	}
	if len(seeds) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE site_base_domains SET is_primary = 1, enabled = 1, updated_at_ms = ?2
			WHERE domain = ?1 AND NOT EXISTS (SELECT 1 FROM site_base_domains WHERE is_primary = 1)`, seeds[0].Domain, nowMS); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET domain_id = (SELECT id FROM site_base_domains WHERE is_primary = 1)
		WHERE domain_id IS NULL`); err != nil {
		return mapErr(err)
	}
	return tx.Commit()
}

// InsertBaseDomain adds a base domain; a known domain returns
// domain.ErrConflict.
func (db *DB) InsertBaseDomain(ctx context.Context, d domain.SiteBaseDomain) error {
	_, err := db.ExecContext(ctx, `INSERT INTO site_base_domains (id, domain, enabled, label, dns_target, token, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?,?)`, d.ID, d.Domain, boolInt(d.Enabled), d.Label, d.DNSTarget, d.Token, d.CreatedAtMS, d.UpdatedAtMS)
	return mapErr(err)
}

// UpdateBaseDomain stores a base domain's editable settings.
func (db *DB) UpdateBaseDomain(ctx context.Context, d domain.SiteBaseDomain) error {
	res, err := db.ExecContext(ctx, `UPDATE site_base_domains SET enabled = ?, label = ?, dns_target = ?, updated_at_ms = ? WHERE id = ?`,
		boolInt(d.Enabled), d.Label, d.DNSTarget, d.UpdatedAtMS, d.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetPrimaryBaseDomain makes one base domain the default for new sites.
func (db *DB) SetPrimaryBaseDomain(ctx context.Context, id string, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE site_base_domains SET is_primary = 0, updated_at_ms = ? WHERE is_primary = 1 AND id != ?`, nowMS, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE site_base_domains SET is_primary = 1, updated_at_ms = ? WHERE id = ?`, nowMS, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// RecordBaseDomainCheck stores the outcome of a DNS verification. verifiedAt
// nil keeps the current verification state (a failed re-check only reports).
func (db *DB) RecordBaseDomainCheck(ctx context.Context, id string, verifiedAt *int64, errMsg *string, nowMS int64) error {
	_, err := db.ExecContext(ctx, `UPDATE site_base_domains SET verified_at_ms = COALESCE(?, verified_at_ms), last_error = ?, last_checked_at_ms = ?
		WHERE id = ?`, verifiedAt, errMsg, nowMS, id)
	return err
}

// MoveSitesToBaseDomain moves every site from one base domain to another,
// keeping slugs. If any slug is taken there, nothing moves
// (domain.ErrConflict). It returns how many sites moved.
func (db *DB) MoveSitesToBaseDomain(ctx context.Context, fromID, toID string, nowMS int64) (int, error) {
	res, err := db.ExecContext(ctx, `UPDATE sites SET domain_id = ?, updated_at_ms = ? WHERE domain_id = ?`, toID, nowMS, fromID)
	if err != nil {
		return 0, mapErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteBaseDomain removes a base domain no site uses.
func (db *DB) DeleteBaseDomain(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM site_base_domains WHERE id = ?1 AND is_primary = 0
		AND NOT EXISTS (SELECT 1 FROM sites WHERE domain_id = ?1)`, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountDomainsUnder counts the custom domains (verified or not) equal to
// name or below it.
func (db *DB) CountDomainsUnder(ctx context.Context, name string, verifiedOnly bool) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM site_domains WHERE (domain = ?1
		OR (length(domain) > length(?1) AND substr(domain, -length(?1) - 1) = '.' || ?1))
		AND (?2 = 0 OR verified_at_ms IS NOT NULL)`, name, boolInt(verifiedOnly)).Scan(&n)
	return n, err
}

// PublicSitePage returns only fields intentionally renderable to anonymous
// visitors. Widgets are included only after the site's explicit opt-in.
func (db *DB) PublicSitePage(ctx context.Context, siteID string) (domain.PublicSitePage, error) {
	var p domain.PublicSitePage
	var widgets int
	err := db.QueryRowContext(ctx, `SELECT s.id, COALESCE(s.bot_id,''), COALESCE(b.name,''), COALESCE(b.discord_username,''),
		COALESCE(b.discord_avatar_url,''), s.page_title, s.page_description, s.page_theme, s.page_accent, s.page_html, s.page_css, s.widgets_public
		FROM sites s LEFT JOIN bots b ON b.id = s.bot_id WHERE s.id = ? AND s.mode = 'page'`, siteID).
		Scan(&p.SiteID, &p.BotID, &p.BotName, &p.DiscordUsername, &p.DiscordAvatarURL, &p.Title, &p.Description,
			&p.Theme, &p.Accent, &p.HTML, &p.CSS, &widgets)
	if err != nil {
		return p, mapErr(err)
	}
	p.WidgetsPublic = widgets == 1
	if p.WidgetsPublic && p.BotID != "" {
		p.Widgets, err = db.ListBotWidgets(ctx, p.BotID)
	}
	return p, err
}
