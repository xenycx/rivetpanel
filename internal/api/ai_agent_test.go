package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
)

// Autopilot acts at once: environment, settings, power and any panel route,
// with the person's permissions, and secret values never reach the stored
// transcript.
func TestAIAgentAutopilotActsAcrossThePanel(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, bot, _ := aiEnv(t, fp)
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "set_environment", map[string]any{"bot_id": bot, "vars": map[string]string{"API_KEY": "sk-agent-set-secret", "MODE": "prod"}}),
		toolStep("b", "panel_api", map[string]any{"method": "GET", "path": "/bots"}),
		toolStep("c", "panel_api", map[string]any{"method": "PATCH", "path": "/bots/" + bot, "body": map[string]any{"name": "renamed-by-agent"}}),
		toolStep("d", "panel_api", map[string]any{"method": "POST", "path": "/me/password", "body": map[string]any{"password": "x"}}),
		toolStep("e", "client_action", map[string]any{"action": "set_theme", "value": "dark"}),
		toolStep("f", "panel_routes", map[string]any{"query": "env"}),
		textStep("Done."),
	}
	chat := newChat(t, admin)
	run := sendChat(t, admin, chat, "auto", "set my key and rename the bot", map[string]any{"kind": "page", "path": "/dashboard"})
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	env, err := e.bots.DecryptEnv(context.Background(), bot)
	if err != nil || env["API_KEY"] != "sk-agent-set-secret" || env["MODE"] != "prod" {
		t.Fatalf("environment not set: %v %v", env, err)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, "HTTP 200") || !strings.Contains(res, "aibot") {
		t.Fatalf("GET /bots = %q", res)
	}
	if b, _ := e.bots.Store.GetBot(context.Background(), bot); b.Name != "renamed-by-agent" {
		t.Fatalf("PATCH did not rename the bot: %q (%q)", b.Name, lastToolResult(fp, 3))
	}
	if res := lastToolResult(fp, 4); !strings.Contains(res, "cannot use /me/password") {
		t.Fatalf("credential route reachable: %q", res)
	}
	if res := lastToolResult(fp, 5); !strings.Contains(res, "set_theme dark") {
		t.Fatalf("client action: %q", res)
	}
	if res := lastToolResult(fp, 6); !strings.Contains(res, "PUT /bots/:id/env") || strings.Contains(res, "/me/password") {
		t.Fatalf("routes: %q", res)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM ai_tool_calls WHERE run_id=? AND (arguments_json LIKE '%sk-agent-set-secret%' OR output LIKE '%sk-agent-set-secret%')`, run).Scan(&n)
	if n != 0 {
		t.Fatalf("a secret value was stored in %d tool calls", n)
	}
	e.db.QueryRow(`SELECT count(*) FROM ai_tool_calls WHERE run_id=? AND approval_state='pending'`, run).Scan(&n)
	if n != 0 {
		t.Fatal("autopilot waited for approval")
	}
}

// Ask-first mode stops before each change; a rejection changes nothing.
func TestAIAgentAskFirstWaitsForApproval(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, bot, conv := aiEnv(t, fp)
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "set_environment", map[string]any{"vars": map[string]string{"FLAG": "on"}}),
		textStep("ok"),
	}
	run := startRun(t, admin, conv, "approval", "turn the flag on")
	var id string
	for i := 0; i < 300 && id == ""; i++ {
		e.db.QueryRow(`SELECT id FROM ai_tool_calls WHERE run_id=? AND approval_state='pending'`, run).Scan(&id)
		if id == "" {
			waitBriefly()
		}
	}
	if id == "" {
		t.Fatal("no approval was requested")
	}
	if env, _ := e.bots.DecryptEnv(context.Background(), bot); env["FLAG"] != "" {
		t.Fatal("changed before approval")
	}
	admin.mustStatus(204, "POST", "/api/v1/ai/tool-calls/"+id+"/decision", map[string]any{"approve": false})
	waitRun(t, admin, run, "completed")
	if env, _ := e.bots.DecryptEnv(context.Background(), bot); env["FLAG"] != "" {
		t.Fatal("changed after a rejection")
	}
}

// The agent has exactly the person's permissions.
func TestAIAgentUsesThePersonsPermissions(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, bot, _ := aiEnv(t, fp)
	user := e.user("user@example.com", domain.RoleUser)
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "panel_api", map[string]any{"method": "GET", "path": "/admin/ai/providers"}),
		toolStep("b", "power", map[string]any{"bot_id": bot, "action": "stop"}),
		toolStep("c", "set_environment", map[string]any{"bot_id": bot, "vars": map[string]string{"X": "1"}}),
		textStep("no"),
	}
	_ = admin
	chat := newChat(t, user)
	run := sendChat(t, user, chat, "auto", "show users", map[string]any{"kind": "page", "path": "/dashboard"})
	waitRun(t, user, run, "completed")
	if res := lastToolResult(fp, 1); !strings.Contains(res, "HTTP 403") {
		t.Fatalf("a user reached an admin route: %q", res)
	}
	for i := 2; i <= 3; i++ {
		if res := lastToolResult(fp, i); !strings.Contains(res, "Tool failed") {
			t.Fatalf("tool %d on someone else's bot: %q", i, res)
		}
	}
}

func TestAgentBridgeTokensStayInProcess(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, _, _ := aiEnv(t, fp)
	_ = admin
	req, _ := http.NewRequest("GET", "/api/v1/bots", nil)
	req.Header.Set("Authorization", "Bearer rva_0000000000000000000000000000000000000000000000000")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("a forged assistant token was accepted: %d", resp.StatusCode)
	}
	for _, p := range []string{"/auth/logout", "/api/v1/me/password", "/me/tokens/x", "/ai/tool-calls/x/decision", "/bots/x/terminal", "/a/../me/password"} {
		if _, err := AgentPathAllowed(p); err == nil {
			t.Errorf("%s allowed", p)
		}
	}
	for _, p := range []string{"/bots", "/api/v1/bots/x/backups", "/me/profile", "/me/notification-prefs", "/admin/settings"} {
		if _, err := AgentPathAllowed(p); err != nil {
			t.Errorf("%s refused: %v", p, err)
		}
	}
}

func waitBriefly() { time.Sleep(10 * time.Millisecond) }

// A malformed call (here power with no action, as a real model sent it)
// fails that one call; the run goes on.
func TestAIAgentBadArgumentsFailOneCallNotTheRun(t *testing.T) {
	fp := &fakeProvider{}
	_, _, admin, bot, conv := aiEnv(t, fp)
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "power", map[string]any{}),
		toolStep("b", "power", map[string]any{"bot_id": bot, "action": ""}),
		textStep("recovered"),
	}
	run := startRun(t, admin, conv, "auto", "restart it")
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	for i := 1; i <= 2; i++ {
		if res := lastToolResult(fp, i); !strings.Contains(res, "action must be") {
			t.Fatalf("call %d: %q", i, res)
		}
	}
}

// A secure-input card completes when the person sets the variables on the
// Environment page instead of in the card.
func TestAISecureInputCompletesWhenSetElsewhere(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, bot, conv := aiEnv(t, fp)
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "request_environment_values", map[string]any{"names": []string{"NEW_TOKEN"}}),
		textStep("thanks"),
	}
	run := startRun(t, admin, conv, "auto", "it needs a token")
	var id string
	for i := 0; i < 300 && id == ""; i++ {
		e.db.QueryRow(`SELECT id FROM ai_tool_calls WHERE run_id=? AND approval_state='pending'`, run).Scan(&id)
		if id == "" {
			waitBriefly()
		}
	}
	if id == "" {
		t.Fatal("no secure card")
	}
	admin.mustStatus(200, "PUT", "/api/v1/bots/"+bot+"/env", map[string]any{"vars": map[string]string{"NEW_TOKEN": "set-on-the-page"}})
	waitRun(t, admin, run, "completed")
	if res := lastToolResult(fp, 1); !strings.Contains(res, "are now set") {
		t.Fatalf("result: %q", res)
	}
}

// The assistant knows the panel: it searches and reads the embedded guide
// and sees what this panel offers the person.
func TestAIAgentKnowsThePanel(t *testing.T) {
	fp := &fakeProvider{}
	_, ai, admin, _, conv := aiEnv(t, fp)
	ai.Version = "9.9.9"
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "panel_docs", map[string]any{"query": "verify a custom domain"}),
		toolStep("b", "read_doc", map[string]any{"page": "ai", "section": "run limits"}),
		toolStep("c", "panel_info", map[string]any{}),
		textStep("ok"),
	}
	run := startRun(t, admin, conv, "auto", "how do domains work?")
	waitRun(t, admin, run, "completed")
	if res := lastToolResult(fp, 1); !strings.Contains(res, "/docs/websites/") {
		t.Fatalf("panel_docs: %q", res)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, "Wall time") {
		t.Fatalf("read_doc: %q", res)
	}
	if res := lastToolResult(fp, 3); !strings.Contains(res, `"version": "9.9.9"`) || !strings.Contains(res, "runtimes") || !strings.Contains(res, "nodejs") {
		t.Fatalf("panel_info: %q", res)
	}
}

// The load-debugging tools read the monitoring service with the person's
// access: host data needs system.view; the answer can chart it.
func TestAIAgentExplainsLoad(t *testing.T) {
	fp := &fakeProvider{}
	e, ai, admin, _, _ := aiEnv(t, fp)
	ai.Metrics = &metrics.Metrics{Store: e.db, LocalNode: domain.LocalNodeID, LogicalCPUs: 2, Proc: metrics.ProcSampler{Root: t.TempDir()}, Fast: &metrics.FastSampler{}}
	now := time.Now().UnixMilli()
	for i := int64(0); i < 40; i++ {
		cpu := 30.0
		if i < 6 {
			cpu = 96
		}
		e.db.InsertTelemetry(t.Context(), domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: now - i*30_000, CPUPercent: cpu, LogicalCPUs: 2,
			MemoryUsedBytes: 100, MemoryTotalBytes: 200, DiskUsedBytes: 1, DiskTotalBytes: 2, Load1: 1.5})
	}
	ended := now - 10_000
	if _, err := e.db.InsertSpike(t.Context(), domain.LoadSpike{NodeID: domain.LocalNodeID, StartedAtMS: now - 170_000, EndedAtMS: &ended, PeakCPUPct: 97, AvgCPUPct: 95,
		TopProcesses: []domain.ProcessEntry{{PID: 7, Comm: "stress", Cmd: "stress --cpu 2", CPUPct: 190, RSSBytes: 1 << 20}}}); err != nil {
		t.Fatal(err)
	}
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "list_spikes", map[string]any{"range": "1h"}),
		toolStep("b", "explain_load", map[string]any{}),
		toolStep("c", "metrics_query", map[string]any{"range": "30m", "metrics": []string{"cpu", "load1"}}),
		toolStep("d", "show_ui", map[string]any{"messages": []any{map[string]any{"version": "1.0", "createSurface": map[string]any{"surfaceId": "load", "components": []any{
			map[string]any{"id": "root", "component": "Card", "child": "col"},
			map[string]any{"id": "col", "component": "Column", "children": []any{"cpu", "tbl"}},
			map[string]any{"id": "cpu", "component": "TimeSeries", "unit": "percent", "times": []any{now - 60_000, now}, "threshold": 80, "series": []any{map[string]any{"label": "CPU", "values": []any{96, 30}}}, "annotations": []any{map[string]any{"t": now - 170_000, "end": ended, "label": "Spike", "kind": "spike"}}},
			map[string]any{"id": "tbl", "component": "Table", "bar": "share", "columns": []any{map[string]any{"key": "p", "label": "Process"}, map[string]any{"key": "share", "label": "Share", "format": "percent"}}, "rows": []any{map[string]any{"p": "stress", "share": 95}}},
		}}}}}),
		textStep("stress caused it"),
	}
	chat := newChat(t, admin)
	run := sendChat(t, admin, chat, "approval", "what caused the CPU spike?", map[string]any{"kind": "page", "path": "/admin/host"})
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	if res := lastToolResult(fp, 1); !strings.Contains(res, `"threshold_pct":80`) || !strings.Contains(res, `"comm":"stress"`) || !strings.Contains(res, `"annotations"`) {
		t.Fatalf("list_spikes: %q", res)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, "latest spike") || !strings.Contains(res, `"likely_cause"`) || !strings.Contains(res, "peaked at 97%") {
		t.Fatalf("explain_load: %q", res)
	}
	if res := lastToolResult(fp, 3); !strings.Contains(res, `"max":96`) || !strings.Contains(res, `"load1"`) {
		t.Fatalf("metrics_query: %q", res)
	}
	if res := lastToolResult(fp, 4); !strings.Contains(res, `Showed surface "load" (4 components)`) {
		t.Fatalf("show_ui chart: %q", res)
	}
	sys := fp.body(0)["messages"].([]any)[0].(map[string]any)["content"].(string)
	tools, _ := json.Marshal(fp.body(0)["tools"])
	for _, n := range []string{"metrics_query", "list_spikes", "explain_load", "top_processes", "cpu_by_app"} {
		if !strings.Contains(sys, n) || !strings.Contains(string(tools), `"`+n+`"`) {
			t.Fatalf("%s is not offered or taught", n)
		}
	}
	if !strings.Contains(sys, "TimeSeries") || !strings.Contains(string(tools), "Table{") {
		t.Fatal("the prompt does not teach the chart components")
	}

	// Without system.view the host tools refuse, naming the permission.
	user := e.user("user@example.com", domain.RoleUser)
	fp.steps = append(fp.steps, toolStep("e", "explain_load", map[string]any{}), toolStep("f", "top_processes", map[string]any{}), textStep("no"))
	uchat := newChat(t, user)
	run = sendChat(t, user, uchat, "auto", "why is it slow", nil)
	waitRun(t, user, run, "completed")
	for i := 6; i <= 7; i++ {
		if res := lastToolResult(fp, i); !strings.Contains(res, "Tool failed") || !strings.Contains(res, "system.view") {
			t.Fatalf("host tool %d without system.view: %q", i, res)
		}
	}
}
