package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
)

// 0055 is additive: a database that already holds bots, sites and
// workspaces migrates with no quota rows, so every account keeps the panel
// defaults, and its usage counts what it already had.
func TestUserQuotasMigrationOnPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, upTo(t, 54)); err != nil {
		t.Fatal(err)
	}
	seed(t, db)
	w, _ := db.PersonalWorkspace(ctx, "u1")
	for _, q := range []string{
		`UPDATE bots SET kind = 'game' WHERE id = 'b2'`,
		`UPDATE bots SET memory_bytes = 100 WHERE id = 'b1'`,
		`INSERT INTO sites (id,workspace_id,owner_id,name,slug,created_at_ms,updated_at_ms) VALUES ('s1','` + w.ID + `','u1','Blog','blog',1,2)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var rows int
	db.QueryRowContext(ctx, `SELECT count(*) FROM user_quotas`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("quota rows after migration: %d", rows)
	}
	if _, err := db.GetUserQuota(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("quota for an account without one: %v", err)
	}
	u, err := db.AccountUsage(ctx, "u1")
	if err != nil || u != (domain.OwnerUsage{Apps: 2, Bots: 1, Games: 1, Sites: 1, Memory: 101}) {
		t.Fatalf("usage %+v %v", u, err)
	}
	// The count and memory agree with what the panel defaults always used.
	n, mem, _ := db.OwnerUsage(ctx, "u1")
	if int64(n) != u.Apps || mem != u.Memory {
		t.Fatalf("OwnerUsage %d %d", n, mem)
	}
}

func TestUserQuotaStore(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	seed(t, db)
	one, zero := int64(1), int64(0)
	q := domain.UserQuota{UserID: "u3", MaxBots: &one, MaxGames: &zero, CreatedAtMS: 5, UpdatedAtMS: 5}
	if err := db.SetUserQuota(ctx, q); err != nil {
		t.Fatal(err)
	}
	q.MaxGames, q.MaxSites, q.UpdatedAtMS = nil, &zero, 9
	if err := db.SetUserQuota(ctx, q); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetUserQuota(ctx, "u3")
	if err != nil || *got.MaxBots != 1 || got.MaxGames != nil || *got.MaxSites != 0 || got.CreatedAtMS != 5 || got.UpdatedAtMS != 9 {
		t.Fatalf("quota %+v %v", got, err)
	}
	neg := int64(-1)
	if err := db.SetUserQuota(ctx, domain.UserQuota{UserID: "u2", MaxApps: &neg}); err == nil {
		t.Fatal("negative quota stored")
	}
	if err := db.DeleteUserQuota(ctx, "u3"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUserQuota(ctx, "u3"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("quota not removed")
	}

	// A refusing check undoes the insert.
	refuse := errors.New("over quota")
	b := domain.Bot{ID: "b3", OwnerID: "u3", NodeID: domain.LocalNodeID, Name: "b3", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 7, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}
	var seen domain.OwnerUsage
	if err := db.CreateBotChecked(ctx, b, func(u domain.OwnerUsage) error { seen = u; return refuse }); !errors.Is(err, refuse) {
		t.Fatal(err)
	}
	if seen != (domain.OwnerUsage{Apps: 1, Bots: 1, Memory: 7}) {
		t.Fatalf("check saw %+v", seen)
	}
	if _, err := db.GetBot(ctx, "b3"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("refused bot was stored")
	}
	if err := db.CreateBotChecked(ctx, b, func(domain.OwnerUsage) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
