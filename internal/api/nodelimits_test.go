package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Resource maximums follow the node's own hardware, and a game server's
// resources can be changed although its startup is a shell wrapper the
// runtime's command list does not allow.
func TestGameServerResourcesFollowNodeHardware(t *testing.T) {
	e := newEnv(t)
	g0 := withGames(t, e)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots,
		Catalog: e.bots.Catalog, Nodes: e.db, Games: g0, SecureCookies: true})
	// The panel allows 1 GiB and 2 CPUs; this node has a single CPU.
	e.bots.NodeCapacity = func(_ context.Context, nodeID string) service.NodeCapacity {
		return service.NodeCapacity{CPUs: 1, MemoryBytes: 8 << 30}
	}
	u := e.user("sizer@example.com", domain.RoleUser)

	var lim struct {
		Limits struct {
			MaxMemory int64 `json:"max_memory_bytes"`
			MaxCPUs   int64 `json:"max_nano_cpus"`
		} `json:"limits"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/runtimes", nil), &lim)
	if lim.Limits.MaxCPUs != 1e9 || lim.Limits.MaxMemory != 1<<30 {
		t.Fatalf("local limits %+v", lim.Limits)
	}

	create := map[string]any{"name": "Sized", "blueprint": "minecraft-paper", "memory_bytes": 1 << 30, "nano_cpus": 2e9,
		"agreements": []string{"minecraft-eula"}, "variables": map[string]string{"MINECRAFT_VERSION": "1.21.11"}}
	if resp, body := u.do("POST", "/api/v1/games", create); resp.StatusCode != 400 || !strings.Contains(string(body), "on this node") {
		t.Fatalf("2 CPUs accepted on a 1-CPU node: %d %s", resp.StatusCode, body)
	}
	// Without a size the server type's suggestion is fitted to the node.
	delete(create, "nano_cpus")
	var g struct {
		ID       string `json:"id"`
		NanoCPUs int64  `json:"nano_cpus"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", create), &g)
	if g.NanoCPUs > 1e9 {
		t.Fatalf("default CPUs %d above the node's", g.NanoCPUs)
	}

	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/runtimes?bot_id="+g.ID, nil), &lim)
	if lim.Limits.MaxCPUs != 1e9 {
		t.Fatalf("server limits %+v", lim.Limits)
	}
	// Saving resources does not re-check the server type's /bin/sh startup.
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+g.ID, map[string]any{"nano_cpus": 5e8})
	if resp, body := u.do("PATCH", "/api/v1/bots/"+g.ID, map[string]any{"nano_cpus": 2e9}); resp.StatusCode != 400 || !strings.Contains(string(body), "1.00 cores") {
		t.Fatalf("CPU above the node saved: %d %s", resp.StatusCode, body)
	}
}

// Players are never sent to the panel's own host name.
func TestGameHostsNeverUsePanelHost(t *testing.T) {
	e := newEnv(t)
	g := withGames(t, e)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots,
		Nodes: e.db, Games: g, PublicURL: "https://panel.example.com", SecureCookies: true,
		GameAddress: func(nodeID string) string {
			if nodeID == domain.LocalNodeID {
				return "203.0.113.7"
			}
			return ""
		}})
	admin := e.user("hosts-admin@example.com", domain.RoleAdmin)

	var hosts struct {
		Hosts map[string]string `json:"hosts"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/game-hosts", nil), &hosts)
	if hosts.Hosts[domain.LocalNodeID] != "203.0.113.7" {
		t.Fatalf("fallback address %v", hosts.Hosts)
	}
	if resp, body := admin.do("PATCH", "/api/v1/nodes/"+domain.LocalNodeID, map[string]any{"public_address": "Panel.Example.com."}); resp.StatusCode != 400 {
		t.Fatalf("panel host accepted as a public address: %d %s", resp.StatusCode, body)
	}
	admin.mustStatus(200, "PATCH", "/api/v1/nodes/"+domain.LocalNodeID, map[string]any{"public_address": "play.example.com"})
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/game-hosts", nil), &hosts)
	if hosts.Hosts[domain.LocalNodeID] != "play.example.com" {
		t.Fatalf("public address %v", hosts.Hosts)
	}
}
