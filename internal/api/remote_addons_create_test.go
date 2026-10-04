package api

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// panelAddonData is this panel's add-on data root; a remote server's add-ons
// must never touch it.
type panelAddonData struct {
	mu    sync.Mutex
	calls []string
}

func (p *panelAddonData) Remove(botID, kind string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "remove "+botID+" "+kind)
	return nil
}

func (p *panelAddonData) Usage(botID, kind string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "usage "+botID+" "+kind)
	return 1
}

// TestRemoteAddonsAtCreation: add-ons chosen while creating a server on a
// remote node are checked for that node before any row is written; they are
// supported through the node's agent (no panel Docker needed), and their
// data is managed on the node, never on the panel.
func TestRemoteAddonsAtCreation(t *testing.T) {
	e := newEnv(t)
	e.bots.Coord = &service.Coordinator{}
	admin := e.user("admin@x.io", domain.RoleAdmin)
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Add-on agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	files := newFakeNodeFiles(t)
	e.bots.NodeFiles = files
	e.bots.AddonData = nil // this panel has no Docker runner of its own
	count := func() (bots, addons int) {
		e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&bots)
		e.db.QueryRow(`SELECT count(*) FROM bot_addons`).Scan(&addons)
		return
	}
	create := func(status int, addons ...string) []byte {
		t.Helper()
		var list []map[string]any
		for _, k := range addons {
			list = append(list, map[string]any{"kind": k})
		}
		return admin.mustStatus(status, "POST", "/api/v1/bots", map[string]any{"name": "db", "runtime": "go",
			"node_id": remoteDeployNode, "addons": list})
	}

	// The panel cannot manage add-ons on remote nodes: refused, nothing written.
	if out := string(create(400, "postgres")); !strings.Contains(out, "remote nodes") {
		t.Fatalf("unconfigured: %s", out)
	}
	if b, a := count(); b != 0 || a != 0 {
		t.Fatalf("refused creation wrote %d bots, %d add-ons", b, a)
	}
	// Local servers still need this panel's runner.
	admin.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "db", "runtime": "go", "addons": []map[string]any{{"kind": "redis"}}})

	node := &fakeNodeAddons{online: true}
	e.bots.RemoteAddons = node
	create(400, "postgres", "nosuchdb")
	create(400, "redis", "redis")
	if b, a := count(); b != 0 || a != 0 {
		t.Fatalf("invalid add-ons wrote %d bots, %d add-ons", b, a)
	}

	// Every built-in kind is supported on an agent node.
	var b botDTO
	json.Unmarshal(create(201, "postgres", "redis", "mongodb", "mariadb"), &b)
	if b.NodeID != remoteDeployNode {
		t.Fatalf("node %q", b.NodeID)
	}
	if _, a := count(); a != 4 {
		t.Fatalf("add-ons %d", a)
	}
	if len(node.calls) != 0 {
		t.Fatalf("a new server's add-on data was cleared on the node: %v", node.calls)
	}

	// Later changes manage the data on the node: removing deletes it there,
	// attaching again clears any leftover first.
	base := "/api/v1/bots/" + b.ID
	admin.mustStatus(204, "DELETE", base+"/addons/redis", nil)
	admin.mustStatus(201, "POST", base+"/addons", map[string]any{"kind": "redis"})
	want := []string{"data " + remoteDeployNode + " " + b.ID + " redis", "data " + remoteDeployNode + " " + b.ID + " redis"}
	if strings.Join(node.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("node calls %v", node.calls)
	}

	// Offline: removal is refused before the add-on is detached.
	files.mu.Lock()
	files.online = false
	files.mu.Unlock()
	if out := string(admin.mustStatus(400, "DELETE", base+"/addons/postgres", nil)); !strings.Contains(out, "offline") {
		t.Fatalf("offline removal: %s", out)
	}
	if _, a := count(); a != 4 {
		t.Fatalf("offline removal detached the add-on (%d left)", a)
	}
	admin.mustStatus(400, "POST", base+"/addons", map[string]any{"kind": "postgres"})

	// A panel with its own runner still never touches its data for a remote server.
	files.mu.Lock()
	files.online = true
	files.mu.Unlock()
	local := &panelAddonData{}
	e.bots.AddonData = local
	json.Unmarshal(create(201, "redis"), &b)
	admin.mustStatus(204, "DELETE", "/api/v1/bots/"+b.ID+"/addons/redis", nil)
	admin.mustStatus(200, "GET", "/api/v1/bots/"+b.ID+"/addons", nil)
	if len(local.calls) != 0 {
		t.Fatalf("panel add-on data used for a remote server: %v", local.calls)
	}
}
