package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// 0056 moves each account's limits onto its personal workspace and drops
// user_quotas. Accounts without limits get no row; usage now counts what a
// workspace holds, whoever created it.
func TestWorkspaceQuotasMigrationOnPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, upTo(t, 55)); err != nil {
		t.Fatal(err)
	}
	seed(t, db)
	wsID := personalWorkspaceID(t, db, "u1")
	for _, q := range []string{
		`UPDATE bots SET workspace_id = '` + wsID + `'`,
		`UPDATE bots SET kind = 'game' WHERE id = 'b2'`,
		`UPDATE bots SET memory_bytes = 100, nano_cpus = 500000000 WHERE id = 'b1'`,
		`INSERT INTO sites (id,workspace_id,owner_id,name,slug,created_at_ms,updated_at_ms) VALUES ('s1','` + wsID + `','u1','Blog','blog',1,2)`,
		`INSERT INTO user_quotas (user_id,max_apps,max_bots,max_games,max_web_apps,max_sites,memory_bytes,updated_by,created_at_ms,updated_at_ms)
			VALUES ('u1',NULL,1,0,NULL,2,1073741824,'u3',10,20)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'user_quotas'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("user_quotas still exists: %d %v", left, err)
	}
	q, err := db.GetWorkspaceQuota(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if q.MaxApps != nil || *q.MaxBots != 1 || *q.MaxGames != 0 || q.MaxWebApps != nil || *q.MaxSites != 2 ||
		*q.MemoryBytes != 1<<30 || q.NanoCPUs != nil || *q.UpdatedBy != "u3" || q.CreatedAtMS != 10 || q.UpdatedAtMS != 20 {
		t.Fatalf("copied limits %+v", q)
	}
	all, err := db.ListWorkspaceQuotas(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("quota rows after migration: %d %v", len(all), err)
	}
	u, err := db.WorkspaceUsage(ctx, wsID)
	if err != nil || u != (domain.WorkspaceUsage{Apps: 2, Bots: 1, Games: 1, Sites: 1, Memory: 101, CPU: 500000001}) {
		t.Fatalf("usage %+v %v", u, err)
	}
	// The workspace summary reports the same memory figure.
	sums, err := db.ListWorkspacesForUser(ctx, "u1")
	if err != nil || sums[0].MemoryBytes != u.Memory {
		t.Fatalf("summary memory %+v %v", sums, err)
	}
}

func TestWorkspaceQuotaStore(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	seed(t, db)
	w, _ := db.PersonalWorkspace(ctx, "u3")
	one, zero := int64(1), int64(0)
	q := domain.WorkspaceQuota{WorkspaceID: w.ID, MaxBots: &one, MaxGames: &zero, CreatedAtMS: 5, UpdatedAtMS: 5}
	if err := db.SetWorkspaceQuota(ctx, q); err != nil {
		t.Fatal(err)
	}
	cpu := int64(2e9)
	q.MaxGames, q.MaxSites, q.NanoCPUs, q.UpdatedAtMS = nil, &zero, &cpu, 9
	if err := db.SetWorkspaceQuota(ctx, q); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetWorkspaceQuota(ctx, w.ID)
	if err != nil || *got.MaxBots != 1 || got.MaxGames != nil || *got.MaxSites != 0 || *got.NanoCPUs != cpu || got.CreatedAtMS != 5 || got.UpdatedAtMS != 9 {
		t.Fatalf("quota %+v %v", got, err)
	}
	neg := int64(-1)
	if err := db.SetWorkspaceQuota(ctx, domain.WorkspaceQuota{WorkspaceID: w.ID, MaxApps: &neg}); err == nil {
		t.Fatal("negative quota stored")
	}
	if err := db.DeleteWorkspaceQuota(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetWorkspaceQuota(ctx, w.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("quota not removed")
	}

	// Limits go with the workspace.
	team := domain.Workspace{ID: "w-team", Name: "Team", OwnerID: "u3", CreatedAtMS: 1, UpdatedAtMS: 1}
	if err := db.CreateWorkspace(ctx, team); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkspaceQuota(ctx, domain.WorkspaceQuota{WorkspaceID: team.ID, MaxApps: &one, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWorkspace(ctx, team.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetWorkspaceQuota(ctx, team.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("limits outlived their workspace")
	}

	// A refusing check undoes the insert; the check sees the workspace the
	// bot lands in (its owner's personal one when none is given).
	refuse := errors.New("over quota")
	b := domain.Bot{ID: "b3", OwnerID: "u3", NodeID: domain.LocalNodeID, Name: "b3", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 7, NanoCPUs: 3, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}
	var seen domain.WorkspaceUsage
	if err := db.CreateBotChecked(ctx, b, func(u domain.WorkspaceUsage) error { seen = u; return refuse }); !errors.Is(err, refuse) {
		t.Fatal(err)
	}
	if seen != (domain.WorkspaceUsage{Apps: 1, Bots: 1, Memory: 7, CPU: 3}) {
		t.Fatalf("check saw %+v", seen)
	}
	if _, err := db.GetBot(ctx, "b3"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("refused bot was stored")
	}
	if err := db.CreateBotChecked(ctx, b, func(domain.WorkspaceUsage) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
