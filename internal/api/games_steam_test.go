package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestSteamBlueprintServerGetsConsecutivePorts(t *testing.T) {
	e := newEnv(t)
	e.bots.Limits.MaxMemoryBytes = 8 << 30
	withGames(t, e)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	u := e.user("player@example.com", domain.RoleUser)
	// A pool with a gap: 2456 and 2458-2459 are free, so the Valheim
	// server (two consecutive ports) must take 2458-2459.
	admin.mustStatus(201, "POST", "/api/v1/admin/allocations", map[string]string{"ip": "0.0.0.0", "ports": "2456,2458-2459"})
	var g gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Vikings", "blueprint": "steam-valheim",
		"memory_bytes": 2 << 30, "variables": map[string]string{"SERVER_PASSWORD": "hunter22"}}), &g)
	if len(g.Allocations) != 2 {
		t.Fatalf("allocations %+v", g.Allocations)
	}
	ports := map[int]bool{}
	for _, a := range g.Allocations {
		ports[a.Port] = a.Primary
	}
	if primary, ok := ports[2458]; !ok || !primary {
		t.Fatalf("expected primary 2458: %+v", g.Allocations)
	}
	if primary, ok := ports[2459]; !ok || primary {
		t.Fatalf("expected extra 2459: %+v", g.Allocations)
	}
	if !strings.Contains(g.Argv[0], "-port ${SERVER_PORT}") {
		t.Fatalf("startup %v", g.Argv)
	}
	// Without a pool run, a fresh block is created automatically.
	var g2 gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Zomboid", "blueprint": "steam-project-zomboid",
		"memory_bytes": 3 << 30}), &g2)
	if len(g2.Allocations) != 2 || g2.Allocations[0].Port+1 != g2.Allocations[1].Port && g2.Allocations[1].Port+1 != g2.Allocations[0].Port {
		t.Fatalf("zomboid allocations %+v", g2.Allocations)
	}
	// The query endpoint answers (offline: not running) without errors.
	u.mustStatus(200, "GET", "/api/v1/bots/"+g.ID+"/game/query", nil)
}

func TestEggPreviewAndSave(t *testing.T) {
	e := newEnv(t)
	withGames(t, e)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	egg := `{"meta":{"version":"PTDL_v2"},"name":"Tiny Game","docker_images":{"Alpine":"alpine:3.20"},
		"startup":"./tiny --port {{SERVER_PORT}} --name {{NAME}}","config":{"files":"{}","startup":"{\"done\":\"ready\"}","stop":"^C"},
		"scripts":{"installation":{"script":"cd /mnt/server\necho hi > tiny","container":"alpine:3.20","entrypoint":"ash"}},
		"variables":[{"name":"Name","env_variable":"NAME","default_value":"tiny","user_viewable":true,"user_editable":true,"rules":"required|string|max:20"}]}`
	if resp, _ := user.do("POST", "/api/v1/admin/blueprints/egg-preview", map[string]string{"egg": egg}); resp.StatusCode != 403 {
		t.Fatalf("user previewed an egg: %d", resp.StatusCode)
	}
	if resp, _ := admin.do("POST", "/api/v1/admin/blueprints/egg-preview", map[string]string{"egg": "{not json"}); resp.StatusCode != 400 {
		t.Fatalf("invalid egg: %d", resp.StatusCode)
	}
	var d blueprint.EggDraft
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/blueprints/egg-preview", map[string]string{"egg": egg}), &d)
	if d.Error != "" || d.Format != "PTDL_v2" || !strings.Contains(d.YAML, "slug: egg-tiny-game") || len(d.Warnings) == 0 {
		t.Fatalf("draft %+v", d)
	}
	// Nothing was stored by the preview.
	if resp, _ := admin.do("GET", "/api/v1/blueprints/egg-tiny-game", nil); resp.StatusCode != 404 {
		t.Fatalf("preview stored a blueprint: %d", resp.StatusCode)
	}
	admin.mustStatus(201, "POST", "/api/v1/admin/blueprints", map[string]string{"yaml": d.YAML})
	var bp blueprintDTO
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/blueprints/egg-tiny-game", nil), &bp)
	if bp.Source != "custom" || bp.Spec.Startup.StopSignal != "SIGINT" {
		t.Fatalf("saved %+v", bp)
	}
	// A second preview reports the slug clash.
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/blueprints/egg-preview", map[string]string{"egg": egg}), &d)
	if !strings.Contains(strings.Join(d.Warnings, "\n"), "already uses the slug egg-tiny-game") {
		t.Fatalf("no clash warning: %v", d.Warnings)
	}
	var g gameOut
	json.Unmarshal(user.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Tiny", "blueprint": "egg-tiny-game"}), &g)
	if g.Kind != "game" {
		t.Fatalf("server %+v", g)
	}
}
