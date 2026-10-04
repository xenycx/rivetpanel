package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeNodePorts stands in for the agent's port probe (protocol 8).
type fakeNodePorts struct {
	mu      sync.Mutex
	busy    map[int]bool
	offline bool
	calls   int
}

func (f *fakeNodePorts) probe(_ context.Context, nodeID, ip string, ports []int) ([]service.PortState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.offline {
		return nil, errors.New("node is offline")
	}
	out := make([]service.PortState, len(ports))
	for i, p := range ports {
		out[i] = service.PortState{Port: p, Free: !f.busy[p]}
		if f.busy[p] {
			out[i].Reason = "already in use on the node"
		}
	}
	return out, nil
}

func (f *fakeNodePorts) set(busy map[int]bool, offline bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy, f.offline = busy, offline
}

// TestRemoteGamePortProbe: allocating a game server on an agent node skips
// ports the node reports as bound, a stopped server whose port became busy
// is refused at start with the reason, and an offline node does not block
// the start (the intent is recorded) but does block a new allocation.
func TestRemoteGamePortProbe(t *testing.T) {
	e := newEnv(t)
	withGames(t, e)
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Game agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	ports := &fakeNodePorts{busy: map[int]bool{25565: true}}
	e.bots.RemotePorts = ports.probe
	admin := e.user("ops@x.io", domain.RoleAdmin)
	create := map[string]any{"name": "Edge", "blueprint": "minecraft-paper", "memory_bytes": 1 << 30, "node_id": remoteDeployNode,
		"variables": map[string]string{"MINECRAFT_VERSION": "1.21.11"}, "agreements": []string{"minecraft-eula"}}

	// The local probe says every port is free; the node's answer decides.
	var g gameOut
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/games", create), &g)
	if len(g.Allocations) != 1 || g.Allocations[0].Port != 25566 || ports.calls == 0 {
		t.Fatalf("allocations %+v after %d probes", g.Allocations, ports.calls)
	}

	// The next allocation skips 25567, busy on the node.
	ports.set(map[int]bool{25565: true, 25567: true}, false)
	var added struct{ Allocations []allocDTO }
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/bots/"+g.ID+"/allocations", nil), &added)
	if len(added.Allocations) != 2 || added.Allocations[1].Port != 25568 {
		t.Fatalf("added allocation %+v", added)
	}

	// The primary port was taken on the node after allocation: start refused.
	ports.set(map[int]bool{25566: true}, false)
	if out := string(admin.mustStatus(400, "POST", "/api/v1/bots/"+g.ID+"/start", nil)); !strings.Contains(out, "port 25566") ||
		!strings.Contains(out, "already in use on the node") {
		t.Fatalf("busy start: %s", out)
	}

	// A node that cannot answer does not block the start intent ...
	ports.set(nil, true)
	admin.mustStatus(202, "POST", "/api/v1/bots/"+g.ID+"/start", nil)

	// ... but an allocation on it is refused rather than guessed.
	admin.mustStatus(202, "POST", "/api/v1/bots/"+g.ID+"/stop", nil)
	if _, err := e.db.Exec(`UPDATE bots SET observed_state = 'stopped', observed_generation = generation WHERE id = ?`, g.ID); err != nil {
		t.Fatal(err)
	}
	if out := string(admin.mustStatus(400, "POST", "/api/v1/bots/"+g.ID+"/allocations", nil)); !strings.Contains(out, "could not check which ports are free") {
		t.Fatalf("offline allocation: %s", out)
	}
	create["name"] = "Offline"
	admin.mustStatus(400, "POST", "/api/v1/games", create)
	var n int
	e.db.QueryRow(`SELECT count(*) FROM bots WHERE name = 'Offline' AND desired_state != 'deleted'`).Scan(&n)
	if n != 0 {
		t.Fatalf("refused creation left %d bots", n)
	}
}
