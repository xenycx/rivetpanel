package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// 0035 rebuilds sites, which releases, custom domains and assistant chats
// reference with ON DELETE CASCADE. The upgrade must keep every row, and the
// rebuilt table must still cascade.
func TestSiteBaseDomainsMigrationKeepsSites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	before := fstest.MapFS{}
	all, _ := fs.Glob(migrations.FS, "*.sql")
	for _, f := range all {
		// Everything except 0035 and the later migrations that change sites
		// (they assume 0035's table): the seed below uses current bot columns.
		if !strings.HasPrefix(f, "0035_") && !strings.HasPrefix(f, "0037_") {
			b, _ := fs.ReadFile(migrations.FS, f)
			before[f] = &fstest.MapFile{Data: b}
		}
	}
	if err := db.Migrate(ctx, before); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string, args ...any) (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return
	}
	seed(t, db)
	w, _ := db.PersonalWorkspace(ctx, "u1")
	exec(`INSERT INTO sites (id,workspace_id,owner_id,name,slug,page_html,created_at_ms,updated_at_ms) VALUES ('s1',?,'u1','Docs','docs','<b>hi</b>',1,2)`, w.ID)
	exec(`INSERT INTO sites (id,workspace_id,owner_id,name,slug,created_at_ms,updated_at_ms) VALUES ('s2',?,'u1','Blog','blog',1,2)`, w.ID)
	exec(`INSERT INTO site_releases (id,site_id,source,files,bytes,created_at_ms) VALUES ('r1','s1','upload',1,1,1)`)
	exec(`UPDATE sites SET current_release = 'r1' WHERE id = 's1'`)
	exec(`INSERT INTO site_domains (domain,site_id,token,verified_at_ms,created_at_ms) VALUES ('www.example.com','s1','0123456789abcdef',1,1)`)
	exec(`INSERT INTO ai_conversations (id,creator_id,site_id,title,created_at_ms,updated_at_ms) VALUES ('c1','u1','s1','why',1,1)`)
	exec(`INSERT INTO ai_messages (id,conversation_id,role,content,created_at_ms) VALUES ('m1','c1','user','hi',1)`)

	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"sites": 2, "site_releases": 1, "site_domains": 1, "ai_conversations": 1, "ai_messages": 1} {
		if got := count(`SELECT count(*) FROM ` + table); got != want {
			t.Fatalf("%s kept %d rows, want %d", table, got, want)
		}
	}
	st, err := db.GetSite(ctx, "s1")
	if err != nil || st.Slug != "docs" || st.PageHTML != "<b>hi</b>" || st.CurrentRelease == nil || *st.CurrentRelease != "r1" || st.DomainID != nil {
		t.Fatalf("site after migration = %+v err=%v", st, err)
	}
	// Foreign keys are back on for every connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var fk int
	conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
	conn.Close()
	if fk != 1 {
		t.Fatal("foreign keys left off after the rebuild")
	}

	// Start-up assigns existing sites to the configured primary domain.
	seeds := []domain.SiteBaseDomain{{ID: "d1", Domain: "sites.example.com", Token: "0123456789abcdef"}, {ID: "d2", Domain: "pages.example.net", Token: "0123456789abcdef"}}
	if err := db.EnsureBaseDomains(ctx, seeds, 5); err != nil {
		t.Fatal(err)
	}
	if got := count(`SELECT count(*) FROM sites WHERE domain_id = 'd1'`); got != 2 {
		t.Fatalf("%d sites assigned to the primary domain", got)
	}
	bases, err := db.ListBaseDomains(ctx)
	if err != nil || len(bases) != 2 || !bases[0].Primary || bases[0].Domain != "sites.example.com" || bases[0].Sites != 2 ||
		!bases[1].Serving() || !bases[1].FromConfig || bases[1].Primary {
		t.Fatalf("base domains = %+v err=%v", bases, err)
	}

	// A slug is unique per base domain, not across the panel.
	d2 := "d2"
	if err := db.CreateSite(ctx, domain.Site{ID: "s3", WorkspaceID: w.ID, OwnerID: "u1", Name: "Docs 2", Slug: "docs", DomainID: &d2, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("same slug under another domain: %v", err)
	}
	d1 := "d1"
	if err := db.CreateSite(ctx, domain.Site{ID: "s4", WorkspaceID: w.ID, OwnerID: "u1", Name: "Docs 3", Slug: "docs", DomainID: &d1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != domain.ErrConflict {
		t.Fatalf("duplicate address: %v", err)
	}
	routes, err := db.SiteRoutes(ctx)
	if err != nil || routes.BySlug[domain.SlugKey("d1", "docs")].SiteID != "s1" || routes.BySlug[domain.SlugKey("d2", "docs")].SiteID != "s3" ||
		routes.ByDomain["www.example.com"].SiteID != "s1" || len(routes.Bases) != 2 {
		t.Fatalf("routes = %+v err=%v", routes, err)
	}
	// Moving every site is all or nothing.
	if _, err := db.MoveSitesToBaseDomain(ctx, "d1", "d2", 6); err != domain.ErrConflict {
		t.Fatalf("move onto a taken slug: %v", err)
	}
	if got := count(`SELECT count(*) FROM sites WHERE domain_id = 'd1'`); got != 2 {
		t.Fatal("a refused move changed sites")
	}
	if err := db.DeleteBaseDomain(ctx, "d2"); err != domain.ErrNotFound {
		t.Fatalf("deleted a domain sites use: %v", err)
	}

	// When RIVET_SITES_BASE_URL's host changes, the configured primary
	// follows it and its sites move along; an extra no longer listed stays.
	if err := db.EnsureBaseDomains(ctx, []domain.SiteBaseDomain{{ID: "d9", Domain: "sites.example.org", Token: "0123456789abcdef"}}, 7); err != nil {
		t.Fatal(err)
	}
	st, _ = db.GetSite(ctx, "s1")
	if st.BaseDomain != "sites.example.org" || st.DomainID == nil || *st.DomainID != "d1" {
		t.Fatalf("site after base URL change = %s %v", st.BaseDomain, st.DomainID)
	}
	if b, err := db.GetBaseDomain(ctx, "pages.example.net"); err != nil || b.FromConfig || !b.Serving() {
		t.Fatalf("unlisted extra domain = %+v err=%v", b, err)
	}

	// The rebuilt table still cascades.
	exec(`DELETE FROM sites WHERE id = 's1'`)
	if got := count(`SELECT count(*) FROM site_releases`) + count(`SELECT count(*) FROM site_domains`) + count(`SELECT count(*) FROM ai_messages`); got != 0 {
		t.Fatal("deleting a site no longer cascades")
	}
}
