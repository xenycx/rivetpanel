package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

func TestApplicationHealthAndHeartbeatRule(t *testing.T) {
	e := newEnv(t)
	clk := &atomicClock{}
	clk.set(time.Unix(1_800_000_000, 0))
	h := &service.HealthService{Store: e.db, Bots: e.bots, Now: clk.now}
	an := &service.Analytics{Store: e.db, Now: clk.now, Heartbeat: h.Heartbeat}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Analytics: an,
		Health: h, PublicURL: "https://panel.example.com", SecureCookies: true})
	owner := e.user("own@x.io", domain.RoleUser)
	viewer := e.user("view@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "view@x.io", "permissions": domain.PermViewConsole})

	type healthDTO struct {
		State        string        `json:"state"`
		LastSeenAtMS *int64        `json:"last_seen_at_ms"`
		Alerts       alertPrefsDTO `json:"alerts"`
	}
	get := func() healthDTO {
		var v healthDTO
		json.Unmarshal(viewer.mustStatus(200, "GET", base+"/health", nil), &v)
		return v
	}
	// Never pushed: unknown, with the default preferences.
	if v := get(); v.State != "unknown" || !v.Alerts.Crash || v.Alerts.HeartbeatAfter != 0 {
		t.Fatalf("initial: %+v", v)
	}
	// Only full control changes preferences; bounds are enforced.
	viewer.mustStatus(403, "PUT", base+"/alerts", map[string]any{"crash": false})
	owner.mustStatus(400, "PUT", base+"/alerts", map[string]any{"crash": true, "heartbeat_after_s": 10})
	owner.mustStatus(200, "PUT", base+"/alerts", map[string]any{"crash": true, "deploy": true, "backup": false, "recovery": true, "heartbeat_after_s": 120})

	// A push is a heartbeat.
	var k struct{ Key string }
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/telemetry-key", nil), &k)
	anon := &client{e: e}
	if resp, body := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{"ready":true}`); resp.StatusCode/100 != 2 {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	// Mark the bot running since before the push.
	ctx := context.Background()
	b, _, _ := e.db.SetDesired(ctx, id, domain.DesiredRunning, false, clk.ms-1000)
	e.db.Observe(ctx, sqlite.Observation{BotID: id, Generation: b.Generation, State: "running", SettleGeneration: true, NowMS: clk.ms - 1000})
	if v := get(); v.State != "ok" || v.LastSeenAtMS == nil {
		t.Fatalf("after push: %+v", v)
	}
	// Silent for longer than the threshold: stale, and the rule fires once.
	clk.add(3 * time.Minute)
	h.Evaluate(ctx)
	if v := get(); v.State != "stale" {
		t.Fatalf("stale: %+v", v)
	}
	if hl, _ := e.db.GetHealth(ctx, id); !hl.StaleAlerted {
		t.Fatal("rule did not fire")
	}
	// Pushes resume: recovered.
	anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{}`)
	h.Evaluate(ctx)
	if hl, _ := e.db.GetHealth(ctx, id); hl.StaleAlerted {
		t.Fatal("rule did not recover")
	}
	// Test notifications need a Discord webhook.
	owner.mustStatus(400, "POST", base+"/alerts/test", nil)
}

func TestTCPHealthProbeValidationAndUnhealthyRestart(t *testing.T) {
	e := newEnv(t)
	clk := &atomicClock{}
	clk.set(time.Unix(1_800_000_000, 0))
	h := &service.HealthService{Store: e.db, Bots: e.bots, Now: clk.now}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Health: h, SecureCookies: true})
	owner := e.user("probe-owner@x.io", domain.RoleUser)
	viewer := e.user("probe-viewer@x.io", domain.RoleUser)
	id := owner.createBot("probe")
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "probe-viewer@x.io", "permissions": domain.PermViewConsole})
	var disabled healthProbeDTO
	json.Unmarshal(viewer.mustStatus(200, "GET", base+"/health-probe", nil), &disabled)
	if disabled.Status != "disabled" {
		t.Fatalf("default probe: %+v", disabled)
	}
	input := map[string]any{"kind": "tcp", "host_port": 29999, "path": "/", "interval_s": 5, "timeout_ms": 250, "failure_threshold": 1, "success_threshold": 1, "startup_grace_s": 0, "restart_unhealthy": true}
	viewer.mustStatus(403, "PUT", base+"/health-probe", input)
	owner.mustStatus(400, "PUT", base+"/health-probe", input)
	ctx := context.Background()
	b, _ := e.db.GetBot(ctx, id)
	if err := e.db.SetBotPorts(ctx, id, b.Generation, []domain.BotPort{{BotID: id, ContainerPort: 8080, HostPort: 29999, Protocol: "tcp", HostIP: "127.0.0.1"}}, clk.ms); err != nil {
		t.Fatal(err)
	}
	owner.mustStatus(200, "PUT", base+"/health-probe", input)
	b, _ = e.db.GetBot(ctx, id)
	b, _, _ = e.db.SetDesired(ctx, id, domain.DesiredRunning, false, clk.ms)
	e.db.Observe(ctx, sqlite.Observation{BotID: id, Generation: b.Generation, State: "running", SettleGeneration: true, NowMS: clk.ms})
	before, _ := e.db.GetBot(ctx, id)
	h.EvaluateProbes(ctx)
	after, _ := e.db.GetBot(ctx, id)
	var got healthProbeDTO
	json.Unmarshal(viewer.mustStatus(200, "GET", base+"/health-probe", nil), &got)
	if got.Status != "unhealthy" || got.ConsecutiveFailures != 1 || after.Generation != before.Generation+1 {
		t.Fatalf("probe=%+v generation %d -> %d", got, before.Generation, after.Generation)
	}
	owner.mustStatus(200, "PUT", base+"/health-probe", map[string]any{"kind": ""})
	json.Unmarshal(viewer.mustStatus(200, "GET", base+"/health-probe", nil), &got)
	if got.Status != "disabled" {
		t.Fatalf("disable: %+v", got)
	}
}

// Game servers have no SDK heartbeat and no active probe: their health is
// the process state and the game query. Notification choices still apply.
func TestGameServersHaveNoHeartbeatOrProbe(t *testing.T) {
	e := newEnv(t)
	clk := &atomicClock{}
	clk.set(time.Unix(1_800_000_000, 0))
	h := &service.HealthService{Store: e.db, Bots: e.bots, Now: clk.now}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Health: h, SecureCookies: true})
	owner := e.user("game-owner@x.io", domain.RoleUser)
	id := owner.createBot("mc")
	ctx := context.Background()
	if _, err := e.db.ExecContext(ctx, `UPDATE bots SET kind = ? WHERE id = ?`, domain.KindGame, id); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/bots/" + id
	// Bot-style published ports are refused: the server publishes its allocations.
	owner.mustStatus(400, "PUT", base+"/ports", map[string]any{"ports": []map[string]any{{"container_port": 25565, "host_port": 25000, "protocol": "tcp"}}})
	owner.mustStatus(200, "PUT", base+"/ports", map[string]any{"ports": []map[string]any{}})
	b, _ := e.db.GetBot(ctx, id)
	if err := e.db.SetBotPorts(ctx, id, b.Generation, []domain.BotPort{{BotID: id, ContainerPort: 25565, HostPort: 29998, Protocol: "tcp", HostIP: "127.0.0.1"}}, clk.ms); err != nil {
		t.Fatal(err)
	}
	probe := map[string]any{"kind": "tcp", "host_port": 29998, "path": "/", "interval_s": 5, "timeout_ms": 250, "failure_threshold": 1, "success_threshold": 1, "startup_grace_s": 0, "restart_unhealthy": true}
	owner.mustStatus(400, "PUT", base+"/health-probe", probe)
	owner.mustStatus(200, "PUT", base+"/health-probe", map[string]any{"kind": ""})
	var got healthProbeDTO
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/health-probe", nil), &got)
	if got.Status != "disabled" || got.Kind != "" {
		t.Fatalf("game probe: %+v", got)
	}
	// A probe stored before this rule (or by hand) is never run.
	if err := e.db.SetHealthProbe(ctx, domain.HealthProbe{BotID: id, Kind: "tcp", HostPort: 29998, Path: "/", IntervalSeconds: 5, TimeoutMS: 250,
		FailureThreshold: 1, SuccessThreshold: 1, RestartUnhealthy: true, Status: "unknown", UpdatedAtMS: clk.ms}); err != nil {
		t.Fatal(err)
	}
	b, _, _ = e.db.SetDesired(ctx, id, domain.DesiredRunning, false, clk.ms)
	e.db.Observe(ctx, sqlite.Observation{BotID: id, Generation: b.Generation, State: "running", SettleGeneration: true, NowMS: clk.ms})
	before, _ := e.db.GetBot(ctx, id)
	h.EvaluateProbes(ctx)
	if after, _ := e.db.GetBot(ctx, id); after.Generation != before.Generation {
		t.Fatal("a game server was restarted by a health probe")
	}
	if p, _ := e.db.GetHealthProbe(ctx, id); p.LastCheckedAtMS != nil {
		t.Fatal("a game server was probed")
	}

	// Heartbeat alerts are ignored; crash/backup choices are kept.
	var prefs alertPrefsDTO
	json.Unmarshal(owner.mustStatus(200, "PUT", base+"/alerts", map[string]any{"crash": true, "deploy": true, "backup": true, "recovery": true, "heartbeat_after_s": 120}), &prefs)
	if !prefs.Crash || !prefs.Backup || prefs.Deploy || prefs.HeartbeatAfter != 0 {
		t.Fatalf("game prefs: %+v", prefs)
	}
	// Even a heartbeat rule stored directly never fires for a game server.
	if err := e.db.SetAlertPrefs(ctx, id, domain.AlertPrefs{Crash: true, HeartbeatAfter: 60}, clk.ms); err != nil {
		t.Fatal(err)
	}
	if err := e.db.RecordHeartbeat(ctx, id, clk.ms, nil); err != nil {
		t.Fatal(err)
	}
	clk.add(10 * time.Minute)
	h.Evaluate(ctx)
	if hl, _ := e.db.GetHealth(ctx, id); hl.StaleAlerted {
		t.Fatal("heartbeat rule fired for a game server")
	}
	var v struct {
		State  string        `json:"state"`
		Alerts alertPrefsDTO `json:"alerts"`
	}
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/health", nil), &v)
	if v.State != "unknown" || v.Alerts.HeartbeatAfter != 0 || !v.Alerts.Crash {
		t.Fatalf("game health: %+v", v)
	}
}
