package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type fakeLogs struct{ text string }

func (f fakeLogs) Tail(context.Context, string, int) (string, error) { return f.text, nil }

func systemPrompt(f *fakeProvider, i int) string {
	msgs, _ := f.body(i)["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	s, _ := msgs[0].(map[string]any)["content"].(string)
	return s
}

func userMessages(f *fakeProvider, i int) []string {
	var out []string
	msgs, _ := f.body(i)["messages"].([]any)
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
			s, _ := mm["content"].(string)
			out = append(out, s)
		}
	}
	return out
}

func newChat(t *testing.T, c *client) string {
	t.Helper()
	var conv struct {
		ID    string
		BotID *string `json:"bot_id"`
	}
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/ai/conversations", map[string]any{}), &conv)
	if conv.ID == "" || conv.BotID != nil {
		t.Fatalf("global conversation: %+v", conv)
	}
	return conv.ID
}

func sendChat(t *testing.T, c *client, conv, mode, content string, view map[string]any) string {
	t.Helper()
	var run struct{ ID string }
	json.Unmarshal(c.mustStatus(202, "POST", "/api/v1/ai/conversations/"+conv+"/messages", map[string]any{"content": content, "mode": mode, "context": view}), &run)
	return run.ID
}

func TestAIChatFollowsThePageTheUserIsOn(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		textStep("It is stopped."),
		toolStep("a", "target_status", map[string]any{}),
		toolStep("b", "list_targets", map[string]any{}),
		textStep("Which one?"),
	}}
	e, _, admin, bot, _ := aiEnv(t, fp)
	chat := newChat(t, admin)

	// On a bot's Files tab the run is scoped to that bot and the model is told
	// what is in view. The name comes from the server, not from the browser.
	run := sendChat(t, admin, chat, "approval", "why is this broken?", map[string]any{"kind": "bot", "id": bot, "label": "spoofed name", "path": "/bots/" + bot + "?tab=files", "section": "files", "detail": "Viewing src/index.js"})
	waitRun(t, admin, run, "completed")
	prompt := systemPrompt(fp, 0)
	for _, want := range []string{`scoped to bot "aibot"`, "files section", "Viewing src/index.js", "RivetPanel Assistant"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "spoofed name") {
		t.Fatal("a browser-supplied bot name reached the prompt")
	}
	if u := userMessages(fp, 0); len(u) != 1 || !strings.HasPrefix(u[0], `[Viewing: Bot "aibot" › files section`) {
		t.Fatalf("user message does not carry its context: %q", u)
	}
	var r struct {
		BotID *string `json:"bot_id"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/ai/runs/"+run, nil), &r)
	if r.BotID == nil || *r.BotID != bot {
		t.Fatalf("run target = %v, want %s", r.BotID, bot)
	}

	// Moving to the dashboard keeps the same chat, with no target in focus.
	// Target tools refuse until a bot is focused; list_targets works.
	run = sendChat(t, admin, chat, "approval", "what bots do I have?", map[string]any{"kind": "page", "label": "Dashboard", "path": "/dashboard"})
	waitRun(t, admin, run, "completed")
	if p := systemPrompt(fp, 1); !strings.Contains(p, "No bot or site is in focus") {
		t.Fatalf("prompt for a target-less run: %s", p)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, "no bot or site is in focus") {
		t.Fatalf("target_status without focus = %q", res)
	}
	if res := lastToolResult(fp, 3); !strings.Contains(res, `"name":"aibot"`) || !strings.Contains(res, bot) {
		t.Fatalf("list_targets = %q", res)
	}
	// Earlier messages keep the page they were sent from.
	var cv struct {
		Messages []struct {
			Role    string
			Context struct{ Kind, Label, Section string }
		}
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/ai/conversations/"+chat, nil), &cv)
	if len(cv.Messages) != 4 || cv.Messages[0].Context.Label != "aibot" || cv.Messages[0].Context.Section != "files" || cv.Messages[2].Context.Kind != "page" {
		t.Fatalf("stored contexts: %+v", cv.Messages)
	}
	if list := string(admin.mustStatus(200, "GET", "/api/v1/ai/conversations", nil)); !strings.Contains(list, chat) {
		t.Fatalf("conversation missing from the user's list: %s", list)
	}

	// A chat is private, administrators included, and a bot the person cannot
	// open cannot be named in a context.
	other := e.user("other@example.com", domain.RoleUser)
	if resp, _ := other.do("GET", "/api/v1/ai/conversations/"+chat, nil); resp.StatusCode != 404 {
		t.Fatalf("foreign chat visible: %d", resp.StatusCode)
	}
	if list := string(other.mustStatus(200, "GET", "/api/v1/ai/conversations", nil)); strings.Contains(list, chat) {
		t.Fatalf("foreign chat listed: %s", list)
	}
	oc := newChat(t, other)
	if resp, _ := other.do("POST", "/api/v1/ai/conversations/"+oc+"/messages", map[string]any{"content": "x", "context": map[string]any{"kind": "bot", "id": bot}}); resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Fatalf("a bot the user cannot open was accepted as context: %d", resp.StatusCode)
	}
	// Auto repair is bound to one target, so it needs one.
	if resp, _ := admin.do("POST", "/api/v1/ai/conversations/"+chat+"/messages", map[string]any{"content": "fix", "mode": "auto", "context": map[string]any{"kind": "page", "path": "/dashboard"}}); resp.StatusCode != 400 {
		t.Fatalf("auto repair without a target: %d", resp.StatusCode)
	}
}

func TestAIChatFocusTargetAndRunScope(t *testing.T) {
	fp := &fakeProvider{}
	e, _, admin, bot, _ := aiEnv(t, fp)
	second := admin.createBot("second")
	fp.steps = []func(http.ResponseWriter){
		toolStep("a", "focus_target", map[string]any{"kind": "bot", "id": bot}),
		toolStep("b", "target_status", map[string]any{}),
		toolStep("c", "focus_target", map[string]any{"kind": "bot", "id": second}),
		textStep("Looked at it."),
	}
	chat := newChat(t, admin)
	run := sendChat(t, admin, chat, "approval", "check my first bot", map[string]any{"kind": "page", "path": "/dashboard"})
	waitRun(t, admin, run, "completed")
	if res := lastToolResult(fp, 1); !strings.Contains(res, `Focused on bot "aibot"`) {
		t.Fatalf("focus_target = %q", res)
	}
	if res := lastToolResult(fp, 2); !strings.Contains(res, `"name":"aibot"`) {
		t.Fatalf("target_status after focus = %q", res)
	}
	// The prompt was rebuilt for the focused bot, and the run cannot hop to
	// another one: what the person approved stays on one bot.
	if p := systemPrompt(fp, 1); !strings.Contains(p, `scoped to bot "aibot"`) {
		t.Fatalf("prompt after focus: %s", p)
	}
	if res := lastToolResult(fp, 3); !strings.Contains(res, "already scoped") {
		t.Fatalf("second focus_target = %q", res)
	}
	var got string
	e.db.QueryRow(`SELECT coalesce(bot_id,'') FROM ai_runs WHERE id=?`, run).Scan(&got)
	if got != bot {
		t.Fatalf("run bot_id = %q, want %s", got, bot)
	}
}

func TestAIChatReadLogsAndBuildOutput(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("a", "read_logs", map[string]any{"lines": 100}),
		toolStep("b", "build_output", map[string]any{}),
		textStep("Token is invalid."),
	}}
	_, ai, admin, bot, _ := aiEnv(t, fp)
	ai.Logs = fakeLogs{}
	chat := newChat(t, admin)

	// Without a container there is nothing to read, and the model is told so.
	run := sendChat(t, admin, chat, "approval", "why does it crash?", map[string]any{"kind": "bot", "id": bot, "section": "console"})
	waitRun(t, admin, run, "completed")
	if res := toolResults(fp, 1); len(res) != 1 || !strings.Contains(res[0]["content"].(string), "no container") {
		t.Fatalf("logs without a container: %v", res)
	}

	fp2 := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("a", "read_logs", map[string]any{}),
		textStep("done"),
	}}
	e2, ai2, admin2, bot2, _ := aiEnv(t, fp2)
	ai2.Logs = fakeLogs{text: "Error: An invalid token was provided\n    at login (tok-very-secret-value)\n"}
	e2.db.Exec(`UPDATE bots SET container_id='c1', last_exit_code=1 WHERE id=?`, bot2)
	run = sendChat(t, admin2, newChat(t, admin2), "approval", "why does it crash?", map[string]any{"kind": "bot", "id": bot2})
	waitRun(t, admin2, run, "completed")
	res := lastToolResult(fp2, 1)
	if !strings.Contains(res, "An invalid token was provided") || !strings.Contains(res, "last_exit_code=1") {
		t.Fatalf("logs = %q", res)
	}
	if strings.Contains(res, "tok-very-secret-value") || !strings.Contains(res, "[REDACTED]") {
		t.Fatalf("log output leaked a secret: %q", res)
	}
}

func TestAIToolsTellTheModelWhatDiagnosticsAreAllowed(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){textStep("ok")}}
	_, _, admin, bot, _ := aiEnv(t, fp)
	run := sendChat(t, admin, newChat(t, admin), "approval", "list files", map[string]any{"kind": "bot", "id": bot})
	waitRun(t, admin, run, "completed")
	tools, _ := fp.body(0)["tools"].([]any)
	var desc string
	for _, tl := range tools {
		f := tl.(map[string]any)["function"].(map[string]any)
		if f["name"] == "run_diagnostic" {
			desc, _ = f["description"].(string)
		}
	}
	if !strings.Contains(desc, "node --check") || !strings.Contains(desc, "npm test") {
		t.Fatalf("run_diagnostic does not list the runtime's allowed commands: %q", desc)
	}
}
