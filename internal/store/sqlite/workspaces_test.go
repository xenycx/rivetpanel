package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// upTo returns the migrations up to and including version max.
func upTo(t *testing.T, max int) fs.FS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, n := range names {
		v, _ := strconv.Atoi(strings.SplitN(n, "_", 2)[0])
		if v > max {
			continue
		}
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = &fstest.MapFile{Data: b}
	}
	return out
}

// Upgrading an existing installation gives every account a personal
// workspace, puts every bot into its owner's, and keeps operation history.
func TestWorkspaceMigrationBackfills(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "old.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, upTo(t, 23)); err != nil {
		t.Fatal(err)
	}
	// This fixture is deliberately held at schema 23, before locations exist.
	// Seed the node using that historical schema rather than the current store
	// helper, which correctly targets the current schema only.
	if _, err := db.ExecContext(ctx, `INSERT INTO nodes
		(id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'local', NULL, 1, 1, 1)`, domain.LocalNodeID, domain.LocalNodeName); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, email, display_name, password_hash, role, disabled, created_at_ms, updated_at_ms) VALUES
			('u1','a@x.io','', 'h', 'user', 0, 1, 1), ('u2','b@x.io','', 'h', 'admin', 0, 2, 2)`,
		`INSERT INTO bots (id, owner_id, node_id, name, runtime, image_ref, argv_json, memory_bytes, nano_cpus, created_at_ms, updated_at_ms)
			VALUES ('b1','u1','` + domain.LocalNodeID + `','one','nodejs','img','["node"]',1,1,1,1), ('b2','u2','` + domain.LocalNodeID + `','two','nodejs','img','["node"]',1,1,1,1)`,
		`INSERT INTO operations (id, bot_id, kind, trigger, status, created_at_ms) VALUES ('o1','b1','deploy','manual','succeeded',5)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"u1", "u2"} {
		w, err := db.PersonalWorkspace(ctx, u)
		if err != nil || !w.Personal || w.Name != "Personal" || len(w.ID) != 36 {
			t.Fatalf("personal workspace of %s = %+v err=%v", u, w, err)
		}
		if role, _ := db.WorkspaceRole(ctx, w.ID, u); role != domain.WorkspaceOwner {
			t.Fatalf("%s role = %q", u, role)
		}
	}
	b, err := db.GetBot(ctx, "b1")
	w1, _ := db.PersonalWorkspace(ctx, "u1")
	if err != nil || b.WorkspaceID != w1.ID {
		t.Fatalf("bot workspace = %q want %q (%v)", b.WorkspaceID, w1.ID, err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE id = 'o1'`).Scan(&n)
	if n != 1 {
		t.Fatal("operation history lost in the operations rebuild")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operations (id, bot_id, kind, trigger, status, created_at_ms) VALUES ('o2','b1','publish','manual','queued',6)`); err != nil {
		t.Fatalf("publish kind rejected: %v", err)
	}
}

func TestWorkspaceMembershipGrantsVisibility(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	seed(t, db)
	if err := db.CreateUser(ctx, domain.User{ID: "u4", Email: "d@x.io", Role: "user", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM workspaces WHERE owner_id = 'u4' AND personal = 1`).Scan(&n)
	if n != 1 {
		t.Fatal("new accounts get a personal workspace")
	}
	team := domain.Workspace{ID: "11111111-1111-4111-8111-111111111111", Name: "Team", OwnerID: "u1", CreatedAtMS: 1, UpdatedAtMS: 1}
	if err := db.CreateWorkspace(ctx, team); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBotWorkspace(ctx, "b1", team.ID, 2); err != nil {
		t.Fatal(err)
	}
	before, _ := db.ListBotsForUser(ctx, "u3")
	if len(before) != 0 {
		t.Fatalf("non-member sees %d bots", len(before))
	}
	if err := db.SetWorkspaceMember(ctx, domain.WorkspaceMember{WorkspaceID: team.ID, UserID: "u3", Role: domain.WorkspaceViewer, CreatedAtMS: 3, UpdatedAtMS: 3}); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListBotsForUser(ctx, "u3")
	if len(after) != 1 || after[0].ID != "b1" {
		t.Fatalf("member sees %+v", after)
	}
	if role, _ := db.BotWorkspaceRole(ctx, "b1", "u3"); role != domain.WorkspaceViewer {
		t.Fatalf("role = %q", role)
	}
	// The owner's row can be neither demoted nor removed.
	db.SetWorkspaceMember(ctx, domain.WorkspaceMember{WorkspaceID: team.ID, UserID: "u1", Role: domain.WorkspaceViewer, CreatedAtMS: 4, UpdatedAtMS: 4})
	if role, _ := db.WorkspaceRole(ctx, team.ID, "u1"); role != domain.WorkspaceOwner {
		t.Fatalf("owner demoted to %q", role)
	}
	if err := db.RemoveWorkspaceMember(ctx, team.ID, "u1"); err == nil {
		t.Fatal("owner removed")
	}
	// A workspace with bots cannot be deleted; personal ones never.
	if err := db.DeleteWorkspace(ctx, team.ID); err == nil {
		t.Fatal("deleted a workspace that still has bots")
	}
	w, _ := db.PersonalWorkspace(ctx, "u1")
	if err := db.DeleteWorkspace(ctx, w.ID); err == nil {
		t.Fatal("deleted a personal workspace")
	}
	// Transferring a bot moves it into the new owner's personal workspace.
	if _, err := db.TransferBot(ctx, "b1", "u3", false, 5); err != nil {
		t.Fatal(err)
	}
	b, _ := db.GetBot(ctx, "b1")
	w3, _ := db.PersonalWorkspace(ctx, "u3")
	if b.WorkspaceID != w3.ID {
		t.Fatalf("transferred bot stayed in %q", b.WorkspaceID)
	}
	sums, err := db.ListWorkspacesForUser(ctx, "u3")
	if err != nil || len(sums) != 2 || !sums[0].Personal || sums[0].Bots != 1 {
		t.Fatalf("summaries = %+v err=%v", sums, err)
	}
}

func TestSiteDomainsClaims(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	seed(t, db)
	w, _ := db.PersonalWorkspace(ctx, "u1")
	for i, id := range []string{"a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002"} {
		if err := db.CreateSite(ctx, domain.Site{ID: id, WorkspaceID: w.ID, OwnerID: "u1", Name: "s", Slug: "site-" + strconv.Itoa(i), CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
			t.Fatal(err)
		}
	}
	a, b := "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002"
	if err := db.CreateSite(ctx, domain.Site{ID: "a0000000-0000-4000-8000-000000000003", WorkspaceID: w.ID, OwnerID: "u1", Name: "s", Slug: "site-0", CreatedAtMS: 1, UpdatedAtMS: 1}); err != domain.ErrConflict {
		t.Fatalf("duplicate slug: %v", err)
	}
	claim := func(site string) error {
		return db.ClaimDomain(ctx, domain.SiteDomain{Domain: "www.example.com", SiteID: site, Token: "0123456789abcdef0123", CreatedAtMS: 1})
	}
	if err := claim(a); err != nil {
		t.Fatal(err)
	}
	// An unverified claim does not reserve the domain.
	if err := claim(b); err != nil {
		t.Fatalf("second site could not take over an unverified claim: %v", err)
	}
	now := int64(9)
	if err := db.RecordDomainCheck(ctx, "www.example.com", &now, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := claim(a); err != domain.ErrConflict {
		t.Fatalf("verified domain taken over: %v", err)
	}
	routes, err := db.SiteRoutes(ctx)
	if err != nil || routes.ByDomain["www.example.com"].SiteID != b {
		t.Fatalf("routes = %+v err=%v", routes.ByDomain, err)
	}
	// Releases: only the site's own can be activated.
	if err := db.InsertRelease(ctx, domain.SiteRelease{ID: "r1", SiteID: a, Source: "upload", Files: 1, Bytes: 1, CreatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateRelease(ctx, b, "r1", 2); err != domain.ErrNotFound {
		t.Fatalf("activated another site's release: %v", err)
	}
	if err := db.ActivateRelease(ctx, a, "r1", 2); err != nil {
		t.Fatal(err)
	}
}
