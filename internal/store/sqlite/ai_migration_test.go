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

// 0030 rebuilds ai_conversations, which cascades into everything below it. An
// upgrade must keep every row and give old runs their conversation's target.
func TestAIGlobalChatMigrationKeepsConversations(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	before := fstest.MapFS{}
	all, _ := fs.Glob(migrations.FS, "*.sql")
	for _, f := range all {
		if !strings.HasPrefix(f, "0030_") {
			b, _ := fs.ReadFile(migrations.FS, f)
			before[f] = &fstest.MapFile{Data: b}
		}
	}
	if err := db.Migrate(ctx, before); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x','h',1,1)`)
	if err := db.CreateBot(ctx, domain.Bot{ID: "b1", OwnerID: "u", NodeID: domain.LocalNodeID, Name: "n", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ai_conversations (id,creator_id,bot_id,title,created_at_ms,updated_at_ms) VALUES ('c1','u','b1','old incident',1,2)`)
	exec(`INSERT INTO ai_messages (id,conversation_id,role,content,created_at_ms) VALUES ('m1','c1','user','why?',1)`)
	exec(`INSERT INTO ai_runs (id,conversation_id,user_id,model,mode,status,limits_json,created_at_ms) VALUES ('r1','c1','u','m','approval','completed','{}',1)`)
	exec(`INSERT INTO ai_tool_calls (id,run_id,call_index,name,arguments_json,created_at_ms,provider_call_id) VALUES ('t1','r1',0,'read_file','{}',1,'call_0')`)
	exec(`INSERT INTO ai_change_sets (id,run_id,target_kind,target_id,status,created_at_ms) VALUES ('s1','r1','bot','b1','applied',1)`)
	exec(`INSERT INTO ai_change_files (change_set_id,path,operation,before_gzip,diff) VALUES ('s1','index.js','modify',x'1f8b','d')`)

	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return
	}
	for table, want := range map[string]int{"ai_conversations": 1, "ai_messages": 1, "ai_runs": 1, "ai_tool_calls": 1, "ai_change_sets": 1, "ai_change_files": 1} {
		if got := count(`SELECT count(*) FROM ` + table); got != want {
			t.Fatalf("%s kept %d rows, want %d", table, got, want)
		}
	}
	if got := count(`SELECT count(*) FROM ai_runs WHERE id='r1' AND bot_id='b1'`); got != 1 {
		t.Fatal("an existing run lost its target")
	}
	if got := count(`SELECT count(*) FROM ai_messages WHERE context_json='{}'`); got != 1 {
		t.Fatal("an existing message has no default context")
	}
	// A conversation may now belong to nobody's bot, but never to two targets.
	exec(`INSERT INTO ai_conversations (id,creator_id,title,created_at_ms,updated_at_ms) VALUES ('c2','u','general',1,1)`)
	if _, err := db.ExecContext(ctx, `INSERT INTO ai_conversations (id,creator_id,bot_id,site_id,title,created_at_ms,updated_at_ms) VALUES ('c3','u','b1','s','x',1,1)`); err == nil {
		t.Fatal("a conversation with both a bot and a site was accepted")
	}
	// Deleting the conversation still cascades through the restored subtree.
	exec(`DELETE FROM ai_conversations WHERE id='c1'`)
	if got := count(`SELECT count(*) FROM ai_runs`) + count(`SELECT count(*) FROM ai_change_files`); got != 0 {
		t.Fatal("restored foreign keys no longer cascade")
	}
}
