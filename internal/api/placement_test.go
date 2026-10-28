package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// placementRig is a panel with the local node and one enabled agent node
// whose connection state the test controls.
type placementRig struct {
	e    *env
	node *fakeNodeFiles
}

const remotePlacementName = "Edge agent"

func newPlacementRig(t *testing.T) *placementRig {
	t.Helper()
	e := newEnv(t)
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, remotePlacementName, now, now); err != nil {
		t.Fatal(err)
	}
	node := newFakeNodeFiles(t)
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode }
	e.bots.NodeFiles = node
	return &placementRig{e: e, node: node}
}

func (r *placementRig) setOnline(v bool) {
	r.node.mu.Lock()
	r.node.online = v
	r.node.mu.Unlock()
}

type placementOut struct {
	Node struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Location  string `json:"location"`
		Transport string `json:"transport"`
		Online    bool   `json:"online"`
		Limits    struct {
			MaxMemoryBytes int64 `json:"max_memory_bytes"`
			MaxNanoCPUs    int64 `json:"max_nano_cpus"`
		} `json:"limits"`
	} `json:"node"`
	Reason    string `json:"reason"`
	CanChoose bool   `json:"can_choose"`
}

func placementOf(t *testing.T, c *client, query string) placementOut {
	t.Helper()
	var out placementOut
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/placement"+query, nil), &out)
	return out
}

func nodePath(ws string) string { return "/api/v1/admin/workspaces/" + ws + "/node" }

// Placement order: a workspace pin wins over the panel default, which wins
// over the local host. GET /placement reports the node, why it was chosen
// and whether the caller may pick another; creation follows it.
func TestPlacementOrder(t *testing.T) {
	r := newPlacementRig(t)
	e := r.e
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	ws := personalWS(t, e, "u@x.io")

	p := placementOf(t, u, "")
	if p.Node.ID != domain.LocalNodeID || p.Reason != "local" || p.CanChoose || !p.Node.Online || p.Node.Transport != "local" ||
		p.Node.Limits.MaxMemoryBytes != 1<<30 || p.Node.Limits.MaxNanoCPUs != 2e9 {
		t.Fatalf("nothing configured: %+v", p)
	}
	if p := placementOf(t, admin, ""); !p.CanChoose {
		t.Fatalf("node managers choose: %+v", p)
	}

	// The panel default applies to every workspace without a pin.
	admin.mustStatus(200, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": remoteDeployNode})
	p = placementOf(t, u, "")
	if p.Node.ID != remoteDeployNode || p.Reason != "panel" || p.Node.Name != remotePlacementName || p.Node.Transport != "agent" || !p.Node.Online {
		t.Fatalf("panel default: %+v", p)
	}
	team := teamWS(u, "Team")
	if p := placementOf(t, u, "?workspace="+team); p.Node.ID != remoteDeployNode || p.Reason != "panel" {
		t.Fatalf("team default: %+v", p)
	}
	// Members only: a stranger learns nothing about the workspace.
	stranger := e.user("s@x.io", domain.RoleUser)
	stranger.mustStatus(404, "GET", "/api/v1/placement?workspace="+team, nil)

	// A workspace pin wins over the panel default.
	admin.mustStatus(200, "PUT", nodePath(ws), map[string]any{"node_id": domain.LocalNodeID})
	if p := placementOf(t, u, ""); p.Node.ID != domain.LocalNodeID || p.Reason != "workspace" {
		t.Fatalf("pinned: %+v", p)
	}

	// Creation lands where /placement says, with no node named.
	var b struct {
		ID         string `json:"id"`
		NodeID     string `json:"node_id"`
		NodeName   string `json:"node_name"`
		NodeOnline *bool  `json:"node_online"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "pinned", "runtime": "nodejs"}), &b)
	if b.NodeID != domain.LocalNodeID {
		t.Fatalf("pinned bot on %s", b.NodeID)
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "team", "runtime": "nodejs", "workspace_id": team}), &b)
	if b.NodeID != remoteDeployNode {
		t.Fatalf("team bot on %s", b.NodeID)
	}
	// The bot detail names its node and reports whether it is reachable;
	// lists do not carry the extra lookup.
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+b.ID, nil), &b)
	if b.NodeName != remotePlacementName || b.NodeOnline == nil || !*b.NodeOnline {
		t.Fatalf("bot detail node: %+v", b)
	}
	if body := u.mustStatus(200, "GET", "/api/v1/bots", nil); strings.Contains(string(body), "node_name") {
		t.Fatal("bot lists carry node names")
	}
	r.setOnline(false)
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+b.ID, nil), &b)
	if b.NodeOnline == nil || *b.NodeOnline {
		t.Fatalf("offline node not reported: %+v", b)
	}

	// Forms read the limits of the node their apps go to, without
	// nodes.manage; any other node's limits stay behind the permission.
	e.bots.NodeCapacity = func(_ context.Context, id string) service.NodeCapacity {
		if id == remoteDeployNode {
			return service.NodeCapacity{CPUs: 1, MemoryBytes: 512 << 20}
		}
		return service.NodeCapacity{}
	}
	limits := func(query string) int64 {
		var rt struct {
			Limits struct {
				MaxMemoryBytes int64 `json:"max_memory_bytes"`
			} `json:"limits"`
		}
		json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/runtimes"+query, nil), &rt)
		return rt.Limits.MaxMemoryBytes
	}
	if got := limits("?node_id=" + remoteDeployNode + "&workspace=" + team); got != 512<<20 {
		t.Fatalf("placement node limits: %d", got)
	}
	if got := limits("?node_id=" + remoteDeployNode); got != 1<<30 { // personal workspace is pinned local
		t.Fatalf("other node limits leaked: %d", got)
	}
	if p := placementOf(t, u, "?workspace="+team); p.Node.Limits.MaxMemoryBytes != 512<<20 || p.Node.Limits.MaxNanoCPUs != 1e9 {
		t.Fatalf("placement limits: %+v", p.Node.Limits)
	}
}

// Only accounts that manage nodes choose a node; the local host may always
// be named (what web apps do).
func TestPlacementUserCannotChoose(t *testing.T) {
	r := newPlacementRig(t)
	u := r.e.user("u@x.io", domain.RoleUser)
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs", "node_id": remoteDeployNode},
		"only accounts that manage nodes can choose the node for a new app")
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs", "node_id": domain.LocalNodeID})
	var n int
	r.e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d bots", n)
	}
}

// An enabled node whose agent is not connected receives no new apps,
// whether chosen explicitly or through a pin; the refusal names the node.
func TestPlacementRefusesOffline(t *testing.T) {
	r := newPlacementRig(t)
	e := r.e
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	ws := personalWS(t, e, "u@x.io")
	r.setOnline(false)

	want := `node \"` + remotePlacementName + `\" is offline; new apps need its agent connected` // as JSON escapes it
	expect400(t, admin, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs", "node_id": remoteDeployNode}, want)

	// Pinning and the default do not need the node online; creating does.
	admin.mustStatus(200, "PUT", nodePath(ws), map[string]any{"node_id": remoteDeployNode})
	if p := placementOf(t, u, ""); p.Node.ID != remoteDeployNode || p.Node.Online {
		t.Fatalf("offline placement: %+v", p)
	}
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs"}, want)
	admin.mustStatus(200, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": remoteDeployNode})

	r.setOnline(true)
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs"})
}

// The panel default node is a setting behind nodes.manage; GET /nodes marks
// it, and a stale setting falls back to the local host.
func TestDefaultNodeSetting(t *testing.T) {
	r := newPlacementRig(t)
	e := r.e
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	ops := e.user("ops@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": admin.createRole("Node operators", domain.PermNodesManage)})

	defaults := func(c *client) map[string]bool {
		var out struct {
			Nodes []struct {
				ID      string `json:"id"`
				Default bool   `json:"default"`
			} `json:"nodes"`
		}
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/nodes", nil), &out)
		m := map[string]bool{}
		for _, n := range out.Nodes {
			m[n.ID] = n.Default
		}
		return m
	}
	u.mustStatus(403, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": remoteDeployNode})
	if d := defaults(admin); !d[domain.LocalNodeID] || d[remoteDeployNode] {
		t.Fatalf("initial defaults %v", d)
	}
	admin.mustStatus(400, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": "no-such-node"})
	ops.mustStatus(200, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": remoteDeployNode})
	if d := defaults(ops); d[domain.LocalNodeID] || !d[remoteDeployNode] {
		t.Fatalf("remote default %v", d)
	}
	// A draining node cannot become the default; a default that starts
	// draining stops applying.
	admin.mustStatus(200, "PATCH", "/api/v1/nodes/"+remoteDeployNode, map[string]any{"draining": true})
	if d := defaults(admin); !d[domain.LocalNodeID] || d[remoteDeployNode] {
		t.Fatalf("draining default %v", d)
	}
	if p := placementOf(t, u, ""); p.Node.ID != domain.LocalNodeID || p.Reason != "local" {
		t.Fatalf("draining placement: %+v", p)
	}
	admin.mustStatus(400, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": remoteDeployNode})
	admin.mustStatus(200, "PATCH", "/api/v1/nodes/"+remoteDeployNode, map[string]any{"draining": false})
	if d := defaults(admin); !d[remoteDeployNode] {
		t.Fatalf("restored default %v", d)
	}
	// Clearing restores the local host.
	admin.mustStatus(200, "PUT", "/api/v1/nodes/default", map[string]any{"node_id": ""})
	if d := defaults(admin); !d[domain.LocalNodeID] || d[remoteDeployNode] {
		t.Fatalf("cleared default %v", d)
	}
}

// A workspace pin needs nodes.manage, shows in the administration views,
// applies to game servers too, and clears when the node is removed.
func TestWorkspaceNodePin(t *testing.T) {
	r := newPlacementRig(t)
	e := r.e
	withGames(t, e)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	u := e.user("u@x.io", domain.RoleUser)
	viewer := e.user("view@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("view@x.io"), map[string]any{"role": admin.createRole("Viewers", domain.PermWorkspacesView)})
	ws := personalWS(t, e, "u@x.io")

	u.mustStatus(403, "PUT", nodePath(ws), map[string]any{"node_id": remoteDeployNode})
	viewer.mustStatus(403, "PUT", nodePath(ws), map[string]any{"node_id": remoteDeployNode})
	admin.mustStatus(404, "PUT", nodePath("00000000-0000-4000-8000-000000000000"), map[string]any{"node_id": remoteDeployNode})
	admin.mustStatus(400, "PUT", nodePath(ws), map[string]any{"node_id": "no-such-node"})
	admin.mustStatus(200, "PUT", nodePath(ws), map[string]any{"node_id": remoteDeployNode})

	nodeOf := func(body []byte, key string) string {
		var out map[string]json.RawMessage
		json.Unmarshal(body, &out)
		var list []struct {
			ID     string `json:"id"`
			NodeID string `json:"node_id"`
		}
		var one struct {
			NodeID string `json:"node_id"`
		}
		if key == "workspaces" {
			json.Unmarshal(out[key], &list)
			for _, w := range list {
				if w.ID == ws {
					return w.NodeID
				}
			}
			return "?"
		}
		json.Unmarshal(out[key], &one)
		return one.NodeID
	}
	if got := nodeOf(viewer.mustStatus(200, "GET", "/api/v1/admin/workspaces", nil), "workspaces"); got != remoteDeployNode {
		t.Fatalf("list node_id %q", got)
	}
	if got := nodeOf(viewer.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+ws, nil), "workspace"); got != remoteDeployNode {
		t.Fatalf("detail node_id %q", got)
	}
	if got := nodeOf(u.mustStatus(200, "GET", "/api/v1/workspaces/"+ws, nil), "workspace"); got != remoteDeployNode {
		t.Fatalf("member detail node_id %q", got)
	}

	// A game server created in the pinned workspace lands on its node.
	var g gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", newGame("Paper")), &g)
	if g.NodeID != remoteDeployNode {
		t.Fatalf("game on %s", g.NodeID)
	}

	// Clearing the pin returns the workspace to the panel default.
	admin.mustStatus(200, "PUT", nodePath(ws), map[string]any{"node_id": ""})
	if got := nodeOf(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+ws, nil), "workspace"); got != "" {
		t.Fatalf("cleared node_id %q", got)
	}
	if p := placementOf(t, u, ""); p.Node.ID != domain.LocalNodeID || p.Reason != "local" {
		t.Fatalf("cleared placement: %+v", p)
	}

	// Removing the node clears every pin to it (the server above holds the
	// node, so pin a fresh team workspace and delete a node without apps).
	team := teamWS(u, "Team")
	now := time.Now().UnixMilli()
	const spare = "33333333-3333-4333-8333-333333333333"
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Spare', 'agent', NULL, 1, ?, ?)`, spare, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(n string) bool { return n == remoteDeployNode || n == spare }
	admin.mustStatus(200, "PUT", nodePath(team), map[string]any{"node_id": spare})
	admin.mustStatus(204, "DELETE", "/api/v1/nodes/"+spare, nil)
	if got := nodeOf(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces/"+team, nil), "workspace"); got != "" {
		t.Fatalf("pin survived node deletion: %q", got)
	}
}
