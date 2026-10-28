package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/xenycx/rivetpanel/internal/migrations"
)

// 0054 rebuilds bots (referenced by nearly every table, most with ON DELETE
// CASCADE) and sites. Every row must survive, references must still cascade,
// and the widened CHECK constraints must accept the new values only.
func TestWebAppsMigrationKeepsBotsAndSites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	before := fstest.MapFS{}
	all, _ := fs.Glob(migrations.FS, "*.sql")
	for _, f := range all {
		if f < "0054_" {
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
	fails := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err == nil {
			t.Fatalf("%s: expected a constraint error", q)
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
	wsID := personalWorkspaceID(t, db, "u1")
	exec(`UPDATE bots SET kind = 'game', build_command = 'make', jvm_args = '-Xmx1G', container_id = 'c2' WHERE id = 'b2'`)
	exec(`INSERT INTO bot_env_vars (bot_id,name,ciphertext,nonce,key_id,created_at_ms,updated_at_ms) VALUES ('b1','TOKEN',zeroblob(16),zeroblob(12),'k',1,1),('b2','A',zeroblob(16),zeroblob(12),'k',1,1)`)
	exec(`INSERT INTO github_repos (bot_id,token_user_id,full_name,branch,webhook_secret_ciphertext,webhook_secret_nonce,webhook_secret_key_id,created_at_ms,updated_at_ms)
		VALUES ('b1','u1','me/repo','main',zeroblob(16),zeroblob(12),'k',1,1)`)
	exec(`INSERT INTO operations (id,bot_id,kind,trigger,status,created_at_ms) VALUES ('o1','b1','deploy','push','succeeded',1),('o2','b2','build','start','failed',1)`)
	exec(`INSERT INTO sites (id,workspace_id,owner_id,bot_id,mode,name,slug,created_at_ms,updated_at_ms) VALUES ('s1',?,'u1','b1','page','Page','page',1,2)`, wsID)
	exec(`INSERT INTO sites (id,workspace_id,owner_id,name,slug,created_at_ms,updated_at_ms) VALUES ('s2',?,'u1','Blog','blog',1,2)`, wsID)
	exec(`INSERT INTO site_releases (id,site_id,source,files,bytes,created_at_ms) VALUES ('r1','s2','upload',1,1,1)`)
	exec(`UPDATE sites SET current_release = 'r1' WHERE id = 's2'`)

	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"bots": 2, "bot_env_vars": 2, "github_repos": 1, "operations": 2, "sites": 2, "site_releases": 1} {
		if got := count(`SELECT count(*) FROM ` + table); got != want {
			t.Fatalf("%s kept %d rows, want %d", table, got, want)
		}
	}
	b2, err := db.GetBot(ctx, "b2")
	if err != nil || b2.Kind != "game" || b2.BuildCommand != "make" || b2.JVMArgs != "-Xmx1G" || b2.ContainerID == nil || *b2.ContainerID != "c2" {
		t.Fatalf("bot after migration = %+v err=%v", b2, err)
	}
	s1, err := db.GetSite(ctx, "s1")
	if err != nil || s1.Mode != "page" || s1.BotID == nil || *s1.BotID != "b1" || s1.HealthPath != "/" || s1.AppID != nil {
		t.Fatalf("site after migration = %+v err=%v", s1, err)
	}
	var indexes int
	db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='bots' AND sql IS NOT NULL`).Scan(&indexes)
	if indexes != 6 {
		t.Fatalf("bots has %d indexes, want 6", indexes)
	}

	// The widened constraints.
	exec(`UPDATE bots SET kind = 'web', runtime = 'php', release_command = 'php artisan migrate --force' WHERE id = 'b1'`)
	fails(`UPDATE bots SET kind = 'vm' WHERE id = 'b1'`)
	fails(`UPDATE bots SET runtime = 'perl' WHERE id = 'b1'`)
	exec(`UPDATE sites SET mode = 'app', app_id = 'b1', app_port = 3000, health_path = '/healthz' WHERE id = 's2'`)
	fails(`UPDATE sites SET health_path = 'healthz' WHERE id = 's2'`)
	fails(`UPDATE sites SET mode = 'proxy' WHERE id = 's2'`)
	exec(`INSERT INTO bot_health_probes (bot_id,kind,host_port,target,updated_at_ms) VALUES ('b1','http',3000,'app',1)`)

	// References into the rebuilt tables still cascade or clear.
	exec(`DELETE FROM sites WHERE id = 's1'`)
	exec(`DELETE FROM bots WHERE id = 'b2'`)
	if got := count(`SELECT count(*) FROM bot_env_vars`); got != 1 {
		t.Fatalf("deleting a bot left %d env rows, want 1", got)
	}
	if got := count(`SELECT count(*) FROM operations`); got != 1 {
		t.Fatalf("deleting a bot left %d operations, want 1", got)
	}
	exec(`DELETE FROM github_repos`)
	exec(`DELETE FROM operations`)
	exec(`DELETE FROM bot_env_vars`)
	exec(`DELETE FROM bot_health_probes`)
	exec(`DELETE FROM bots WHERE id = 'b1'`)
	var appID *string
	db.QueryRowContext(ctx, `SELECT app_id FROM sites WHERE id = 's2'`).Scan(&appID)
	if appID != nil {
		t.Fatal("deleting a site's app must clear sites.app_id")
	}
	if got := count(`SELECT count(*) FROM site_releases WHERE site_id = 's2'`); got != 1 {
		t.Fatal("the release of a site was lost")
	}
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
}
