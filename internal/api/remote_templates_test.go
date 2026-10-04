package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/templates"
)

// recordingPurger stands in for the node router's purge: it records which
// servers were handed to their agent for teardown.
type recordingPurger struct {
	mu  sync.Mutex
	ids []string
}

func (p *recordingPurger) Purge(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, id)
	return nil
}

func (f *fakeNodeFiles) takeOps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ops := f.ops
	f.ops = nil
	return ops
}

// TestRemoteTemplateCreation creates template bots on an agent node: the
// files are seeded on the node in one create-only transaction, never on the
// panel's disk; an offline node is refused before anything is recorded; and a
// creation that fails partway leaves no usable-looking bot behind.
func TestRemoteTemplateCreation(t *testing.T) {
	e := newEnv(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	e.bots.Files = fm
	node := newFakeNodeFiles(t)
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Template agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	e.bots.NodeFiles = node
	purger := &recordingPurger{}
	e.bots.Purger = purger

	u := e.user("tpl@x.io", domain.RoleUser)
	req := func(name string) map[string]any {
		return map[string]any{"name": name, "template_id": "discordjs", "node_id": remoteDeployNode, "env": map[string]string{"DISCORD_TOKEN": "x.y.z"}}
	}
	live := func() int {
		var n int
		e.db.QueryRow(`SELECT count(*) FROM bots WHERE desired_state != 'deleted'`).Scan(&n)
		return n
	}

	// Only administrators choose the node.
	u.mustStatus(400, "POST", "/api/v1/bots", req("not-admin"))
	if _, err := e.db.Exec(`UPDATE users SET role = 'admin'`); err != nil {
		t.Fatal(err)
	}

	// Offline: refused before a row or a node call exists.
	node.online = false
	if out := string(u.mustStatus(400, "POST", "/api/v1/bots", req("offline"))); !strings.Contains(out, "offline") {
		t.Fatalf("offline node: %s", out)
	}
	node.online = true
	if n := live(); n != 0 {
		t.Fatalf("refused creations left %d bots", n)
	}
	if ops := node.takeOps(); len(ops) != 0 {
		t.Fatalf("offline refusal reached the node: %v", ops)
	}

	// The database refuses the row: nothing reaches the node.
	if _, err := e.db.Exec(`CREATE TRIGGER fail_bot BEFORE INSERT ON bots WHEN NEW.name = 'dbfail' BEGIN SELECT RAISE(ABORT, 'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	u.mustStatus(500, "POST", "/api/v1/bots", req("dbfail"))
	if ops := node.takeOps(); len(ops) != 0 {
		t.Fatalf("a failed insert reached the node: %v", ops)
	}

	// Success: every template file lands on the node in one committed patch.
	var b botDTO
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", req("remote-template")), &b)
	if b.NodeID != remoteDeployNode || b.SourceType != "template" {
		t.Fatalf("created bot: %+v", b)
	}
	want, err := templates.Files("discordjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range want {
		got, ok := node.read(t, b.ID, f.Path)
		if !ok || got != string(f.Data) {
			t.Fatalf("node %s = %q (exists %v)", f.Path, got, ok)
		}
	}
	if ops := node.takeOps(); !slices.Equal(ops, []string{"mkdir", "patch", "complete true"}) {
		t.Fatalf("node calls = %v", ops)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, b.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the panel created a local workspace for a remote bot: %v", err)
	}
	var envCount int
	e.db.QueryRow(`SELECT count(*) FROM bot_env_vars WHERE bot_id = ?`, b.ID).Scan(&envCount)
	if envCount != 1 {
		t.Fatalf("env vars = %d", envCount)
	}

	// The node refuses the patch: the bot is marked deleted and handed to
	// its agent for teardown; nothing is left looking healthy.
	node.patchErr = domain.Invalid("disk full")
	if out := string(u.mustStatus(400, "POST", "/api/v1/bots", req("patch-refused"))); !strings.Contains(out, "disk full") {
		t.Fatalf("patch refusal: %s", out)
	}
	if n := live(); n != 1 {
		t.Fatalf("live bots after a refused patch = %d", n)
	}

	// The commit cannot be confirmed (twice): rolled back on the node.
	node.takeOps()
	node.failCommits = 2
	u.mustStatus(500, "POST", "/api/v1/bots", req("commit-lost"))
	if ops := node.takeOps(); !slices.Equal(ops, []string{"mkdir", "patch", "complete false"}) {
		t.Fatalf("node calls after a lost commit = %v", ops)
	}
	var lostID string
	e.db.QueryRow(`SELECT id FROM bots WHERE name = 'commit-lost'`).Scan(&lostID)
	if lostID == "" {
		t.Fatal("discarded bot row missing before its agent purge")
	}
	if _, ok := node.read(t, lostID, "index.js"); ok {
		t.Fatal("a rolled-back template left files on the node")
	}
	if n := live(); n != 1 {
		t.Fatalf("live bots after a lost commit = %d", n)
	}
	purger.mu.Lock()
	purged := slices.Clone(purger.ids)
	purger.mu.Unlock()
	if len(purged) != 2 || purged[1] != lostID {
		t.Fatalf("purged = %v", purged)
	}
	// A lost first answer is retried once and the bot is kept.
	node.failCommits = 1
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", req("commit-retried")), &b)
	if got, ok := node.read(t, b.ID, "index.js"); !ok || got == "" {
		t.Fatal("retried commit lost the files")
	}
	ents, _ := os.ReadDir(e.dataDir)
	for _, en := range ents {
		if en.Name() != "keys" && !strings.HasPrefix(en.Name(), ".") {
			t.Fatalf("panel data dir gained %q", en.Name())
		}
	}
}
