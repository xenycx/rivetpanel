package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
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
