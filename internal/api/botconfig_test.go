package api

import (
	"os"
	"path/filepath"
	"strings"

	"encoding/json"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestStartupNetworkRestartSettings(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@x.io", domain.RoleUser)
	id := u.createBot("b")
	base := "/api/v1/bots/" + id
	var b botDTO

	// Entrypoint + args; the entrypoint program must be permitted for the runtime.
	json.Unmarshal(u.mustStatus(200, "PATCH", base, map[string]any{"entrypoint": []string{"node", "--enable-source-maps"}, "argv": []string{"bot.js"}}), &b)
	if len(b.Entrypoint) != 2 || b.Argv[0] != "bot.js" {
		t.Fatalf("%+v", b)
	}
	u.mustStatus(400, "PATCH", base, map[string]any{"entrypoint": []string{"bash", "-c"}, "argv": []string{"x"}})
	u.mustStatus(400, "PATCH", base, map[string]any{"argv": []string{}})
	json.Unmarshal(u.mustStatus(200, "PATCH", base, map[string]any{"entrypoint": []string{}, "argv": []string{"node", "index.js"}}), &b)
	if len(b.Entrypoint) != 0 {
		t.Fatalf("entrypoint not cleared: %+v", b)
	}

	// Restart policy bounds.
	json.Unmarshal(u.mustStatus(200, "PATCH", base, map[string]any{"restart_policy": "never", "restart_max_attempts": 0,
		"restart_backoff_initial_ms": 500, "restart_backoff_max_ms": 60000}), &b)
	if b.RestartPolicy != "never" || b.RestartBackoffInitialMS != 500 {
		t.Fatalf("%+v", b)
	}
	for name, bad := range map[string]map[string]any{
		"policy":       {"restart_policy": "always"},
		"attempts":     {"restart_max_attempts": 101},
		"tiny initial": {"restart_backoff_initial_ms": 10},
		"max<initial":  {"restart_backoff_initial_ms": 5000, "restart_backoff_max_ms": 1000},
		"huge max":     {"restart_backoff_max_ms": 99999999},
	} {
		if resp, _ := u.do("PATCH", base, bad); resp.StatusCode != 400 {
			t.Errorf("%s accepted: %d", name, resp.StatusCode)
		}
	}

	// Network: bandwidth is recorded, ports need networking and stay in policy.
	json.Unmarshal(u.mustStatus(200, "PATCH", base, map[string]any{"bandwidth_kbps": 2048}), &b)
	if b.BandwidthKbps == nil || *b.BandwidthKbps != 2048 {
		t.Fatalf("%+v", b)
	}
	u.mustStatus(400, "PATCH", base, map[string]any{"bandwidth_kbps": 3})
	u.mustStatus(200, "PATCH", base, map[string]any{"bandwidth_kbps": 0})

	port := func(host int, ip string) map[string]any {
		return map[string]any{"ports": []map[string]any{{"container_port": 8080, "host_port": host, "protocol": "tcp", "host_ip": ip}}}
	}
	json.Unmarshal(u.mustStatus(200, "PUT", base+"/ports", port(20080, "")), &b)
	if len(b.Ports) != 1 || b.Ports[0].HostIP != "127.0.0.1" || b.Ports[0].HostPort != 20080 {
		t.Fatalf("%+v", b)
	}
	gen := b.Generation
	u.mustStatus(400, "PUT", base+"/ports", port(80, ""))                      // outside the allowed range
	u.mustStatus(400, "PUT", base+"/ports", port(2022, ""))                    // never the SFTP/panel ports
	u.mustStatus(400, "PUT", base+"/ports", port(20081, "0.0.0.0"))            // public bind disabled by default
	u.mustStatus(400, "PATCH", base, map[string]any{"network_enabled": false}) // ports exist
	u.mustStatus(400, "PUT", base+"/ports", map[string]any{"ports": []map[string]any{
		{"container_port": 1, "host_port": 20090}, {"container_port": 1, "host_port": 20091}}})
	// Host ports are exclusive across bots.
	other := e.user("b@x.io", domain.RoleUser)
	oid := other.createBot("o")
	other.mustStatus(400, "PUT", "/api/v1/bots/"+oid+"/ports", port(20080, ""))
	// Changing ports requires a stopped bot and advances the generation.
	json.Unmarshal(u.mustStatus(200, "GET", base, nil), &b)
	if b.Generation != gen {
		t.Fatalf("rejected changes advanced the generation: %d vs %d", b.Generation, gen)
	}
	e.db.Exec(`UPDATE bots SET observed_state = 'running', desired_state = 'running' WHERE id = ?`, id)
	u.mustStatus(409, "PUT", base+"/ports", port(20082, ""))
	e.db.Exec(`UPDATE bots SET observed_state = 'stopped', desired_state = 'stopped' WHERE id = ?`, id)

	u.mustStatus(200, "PUT", base+"/ports", map[string]any{"ports": []map[string]any{}})
	json.Unmarshal(u.mustStatus(200, "PATCH", base, map[string]any{"network_enabled": false}), &b)
	if b.NetworkEnabled {
		t.Fatal("network still enabled")
	}
	u.mustStatus(400, "PUT", base+"/ports", port(20083, "")) // networking is off

	// Only the owner may publish ports, not even a full-admin sub-user.
	sub := e.user("s@x.io", domain.RoleUser)
	u.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "s@x.io", "permissions": domain.PermFullAdmin})
	sub.mustStatus(403, "PUT", base+"/ports", port(20084, ""))
	sub.mustStatus(200, "PATCH", base, map[string]any{"restart_policy": "on_failure"})
}

func TestKillWithoutRunnerAndTerminalStartRetries(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@x.io", domain.RoleUser)
	id := u.createBot("b")
	base := "/api/v1/bots/" + id
	u.mustStatus(503, "POST", base+"/kill", nil) // no runner configured in this environment

	// Start twice while the first is still pending: idempotent (one generation).
	var b botDTO
	json.Unmarshal(u.mustStatus(202, "POST", base+"/start", nil), &b)
	g := b.Generation
	json.Unmarshal(u.mustStatus(202, "POST", base+"/start", nil), &b)
	if b.Generation != g {
		t.Fatalf("pending start was not idempotent: %d -> %d", g, b.Generation)
	}
	// Once settled in a terminal state (e.g. clean exit), Start is a retry.
	e.db.Exec(`UPDATE bots SET observed_state = 'stopped', observed_generation = generation WHERE id = ?`, id)
	json.Unmarshal(u.mustStatus(202, "POST", base+"/start", nil), &b)
	if b.Generation != g+1 {
		t.Fatalf("Start after a terminal state must retry: %d -> %d", g, b.Generation)
	}
	// A running bot stays idempotent.
	e.db.Exec(`UPDATE bots SET observed_state = 'running', observed_generation = generation WHERE id = ?`, id)
	g = b.Generation
	json.Unmarshal(u.mustStatus(202, "POST", base+"/start", nil), &b)
	if b.Generation != g {
		t.Fatal("Start on a running bot advanced the generation")
	}
}

func TestCreateFromTemplate(t *testing.T) {
	e := newEnv(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	e.bots.Files = fm
	u := e.user("a@x.io", domain.RoleUser)

	var tl struct {
		Templates []struct{ ID, Runtime string }
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/templates", nil), &tl)
	if len(tl.Templates) != 7 {
		t.Fatalf("templates: %+v", tl)
	}
	for _, tp := range tl.Templates {
		var b botDTO
		// The template dictates the runtime; a conflicting request value is ignored.
		json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": tp.ID, "runtime": "ruby", "template_id": tp.ID}), &b)
		if b.Runtime != tp.Runtime || b.SourceType != "template" || b.TemplateID == nil || *b.TemplateID != tp.ID {
			t.Fatalf("%s: %+v", tp.ID, b)
		}
		ents, _ := os.ReadDir(filepath.Join(e.dataDir, b.ID))
		if len(ents) < 3 {
			t.Fatalf("%s: workspace not seeded: %d entries", tp.ID, len(ents))
		}
	}
	// Seeded projects contain what their runtime builds from.
	var b botDTO
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "g", "runtime": "go", "template_id": "discordgo"}), &b)
	for _, f := range []string{"go.mod", "go.sum", "main.go"} {
		if _, err := os.Stat(filepath.Join(e.dataDir, b.ID, f)); err != nil {
			t.Errorf("discordgo template missing %s", f)
		}
	}
	u.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "nodejs", "template_id": "../x"})
	// A template can choose its command (TypeScript runs its .mts file).
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "ts", "template_id": "discordts"}), &b)
	if strings.Join(b.Argv, " ") != "node src/index.mts" {
		t.Fatalf("discordts argv: %v", b.Argv)
	}
}

func TestCreateWithInitialEnvironment(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@x.io", domain.RoleUser)
	var b botDTO
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "tok", "runtime": "nodejs",
		"env": map[string]string{"DISCORD_TOKEN": "abc.def"}}), &b)
	body := string(u.mustStatus(200, "GET", "/api/v1/bots/"+b.ID+"/env", nil))
	if !strings.Contains(body, "DISCORD_TOKEN") || strings.Contains(body, "abc.def") {
		t.Fatalf("env list: %s", body)
	}
	// Invalid or reserved names reject the whole request; no bot is left behind.
	u.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "bad", "runtime": "nodejs",
		"env": map[string]string{"RIVET_URL": "x"}})
	var list struct{ Bots []botDTO }
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots", nil), &list)
	if len(list.Bots) != 1 {
		t.Fatalf("bots = %d, want 1", len(list.Bots))
	}
}

func TestTagsFavoritesAndBatch(t *testing.T) {
	e := newEnv(t)
	a := e.user("a@x.io", domain.RoleUser)
	b := e.user("b@x.io", domain.RoleUser)
	id1, id2 := a.createBot("one"), a.createBot("two")
	id3 := b.createBot("theirs")

	var tg struct{ Tags []string }
	json.Unmarshal(a.mustStatus(200, "PUT", "/api/v1/bots/"+id1+"/tags", map[string]any{"tags": []string{"Prod", "music", "prod", " "}}), &tg)
	if strings.Join(tg.Tags, ",") != "music,prod" {
		t.Fatalf("tags = %v", tg.Tags)
	}
	a.mustStatus(400, "PUT", "/api/v1/bots/"+id1+"/tags", map[string]any{"tags": []string{"has space"}})
	a.mustStatus(204, "PUT", "/api/v1/bots/"+id2+"/favorite", map[string]any{"favorite": true})

	var l struct {
		Bots  []botDTO
		Total int
	}
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/bots?tag=prod", nil), &l)
	if l.Total != 1 || l.Bots[0].ID != id1 {
		t.Fatalf("tag filter: %+v", l)
	}
	json.Unmarshal(a.mustStatus(200, "GET", "/api/v1/bots?limit=1&offset=1", nil), &l)
	if l.Total != 2 || len(l.Bots) != 1 || !l.Bots[0].Favorite {
		t.Fatalf("paging/favorite: %+v", l)
	}

	var res struct {
		Results []struct {
			BotID string `json:"bot_id"`
			OK    bool
		}
	}
	json.Unmarshal(a.mustStatus(202, "POST", "/api/v1/bots/batch", map[string]any{"action": "start", "ids": []string{id1, id2, id3}}), &res)
	if len(res.Results) != 3 || !res.Results[0].OK || !res.Results[1].OK || res.Results[2].OK {
		t.Fatalf("batch: %+v", res)
	}
	var st string
	e.db.QueryRow(`SELECT desired_state FROM bots WHERE id = ?`, id3).Scan(&st)
	if st != "stopped" {
		t.Fatal("a batch touched another user's bot")
	}
	a.mustStatus(400, "POST", "/api/v1/bots/batch", map[string]any{"action": "delete", "ids": []string{id1}})
}
