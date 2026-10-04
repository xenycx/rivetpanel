package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/service"
)

// fakeProvider is a scripted OpenAI-compatible streaming endpoint. Each
// request receives the next step; requests are recorded for assertions.
type fakeProvider struct {
	mu     sync.Mutex
	steps  []func(w http.ResponseWriter)
	bodies []map[string]any
	auth   []string
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	n := len(f.bodies)
	f.mu.Unlock()
	if n > len(f.steps) {
		http.Error(w, "unexpected request", 500)
		return
	}
	f.steps[n-1](w)
}

func (f *fakeProvider) body(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		return nil
	}
	return f.bodies[i]
}

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func toolStep(id, name string, args map[string]any) func(http.ResponseWriter) {
	raw, _ := json.Marshal(args)
	a, _ := json.Marshal(string(raw))
	return func(w http.ResponseWriter) {
		sse(w,
			fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":""}}]}}]}`, id, name),
			fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%s}}]},"finish_reason":"tool_calls"}]}`, a),
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}
}

func textStep(text string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		sse(w, fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":"stop"}]}`, text),
			`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`)
	}
}

func aiEnv(t *testing.T, fp http.Handler) (*env, *service.AIService, *client, string, string) {
	t.Helper()
	e := newEnv(t)
	audit := &service.Audit{Store: e.db, Bots: e.bots}
	ai := &service.AIService{Store: e.db, Keys: e.bots.Keys, Bots: e.bots, Files: e.bots.Workspaces.(*filesystem.Manager), Audit: audit, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ai.Start(ctx)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog, SecureCookies: true,
		Nodes: e.db, Files: e.bots.Workspaces.(*filesystem.Manager), MaxUpload: 2 << 20, AI: ai, Audit: audit})
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	admin := e.user("admin@example.com", domain.RoleAdmin)
	var p struct{ ID string }
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/ai/providers", map[string]any{
		"name": "Fake", "enabled": true, "default": true, "base_url": srv.URL, "chat_path": "/chat/completions",
		"models_path": "/models", "default_model": "fake-model", "max_output_tokens": 512, "timeout_ms": 10000, "key": "sk-provider-secret",
	}), &p)
	providers := string(admin.mustStatus(200, "GET", "/api/v1/admin/ai/providers", nil))
	if strings.Contains(providers, "sk-provider-secret") || !strings.Contains(providers, `"key_set":true`) {
		t.Fatalf("provider key exposed or not stored: %s", providers)
	}

	bot := admin.createBot("aibot")
	admin.mustStatus(200, "PUT", "/api/v1/bots/"+bot+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": "tok-very-secret-value"}})
	w, err := e.bots.Workspaces.(*filesystem.Manager).Open(bot)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write("index.js", strings.NewReader("login('tok-very-secret-value')\n"), 1<<20); err != nil {
		t.Fatal(err)
	}
	w.Close()

	var conv struct{ ID string }
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/bots/"+bot+"/ai/conversations", map[string]any{"title": "incident"}), &conv)
	return e, ai, admin, bot, conv.ID
}

func waitRun(t *testing.T, c *client, id string, want ...string) map[string]any {
	t.Helper()
	var r map[string]any
	for i := 0; i < 300; i++ {
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/ai/runs/"+id, nil), &r)
		for _, s := range want {
			if r["status"] == s {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %v: %v", id, want, r)
	return nil
}

func readBotFile(t *testing.T, e *env, bot, p string) string {
	t.Helper()
	w, err := e.bots.Workspaces.(*filesystem.Manager).Open(bot)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, err := w.Read(p, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAIOperatorApprovalApplyAndUndo(t *testing.T) {
	fp := &fakeProvider{}
	fp.steps = []func(http.ResponseWriter){
		toolStep("call-read", "read_file", map[string]any{"path": "index.js"}),
		toolStep("call-env", "read_file", map[string]any{"path": ".env"}),
		toolStep("call-fix", "propose_file_change", map[string]any{"path": "index.js", "content": "login(process.env.DISCORD_TOKEN)\n", "summary": "Read the token from the environment"}),
		textStep("Moved the token into the environment."),
	}
	e, _, admin, bot, conv := aiEnv(t, fp)

	var run struct{ ID string }
	json.Unmarshal(admin.mustStatus(202, "POST", "/api/v1/ai/conversations/"+conv+"/messages", map[string]any{"content": "Why does login fail?", "mode": "approval"}), &run)
	waitRun(t, admin, run.ID, "waiting_approval")

	// Every tool schema must be valid JSON Schema: real providers reject
	// "required": null on tools without parameters.
	tools, _ := fp.body(0)["tools"].([]any)
	for _, tl := range tools {
		params := tl.(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
		if _, ok := params["required"].([]any); !ok {
			t.Fatalf("tool %v has a non-array required: %#v", tl.(map[string]any)["function"].(map[string]any)["name"], params["required"])
		}
	}
	// Secrets never reach the provider: file contents are redacted and
	// protected paths are refused.
	raw, _ := json.Marshal(fp.body(2))
	if strings.Contains(string(raw), "tok-very-secret-value") || !strings.Contains(string(raw), "[REDACTED]") {
		t.Fatalf("secret value reached the provider or was not redacted: %s", raw)
	}
	if !strings.Contains(string(raw), "Permission is required") {
		t.Fatalf("protected .env read was not refused: %s", raw)
	}
	if got := readBotFile(t, e, bot, "index.js"); !strings.Contains(got, "tok-very-secret-value") {
		t.Fatalf("file changed before approval: %q", got)
	}

	// The run inspector can be rebuilt from the API alone: the waiting run,
	// its tool calls (keyed by our ids, not the provider's) and the draft.
	runs := conversationRuns(t, admin, conv)
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].Status != "waiting_approval" {
		t.Fatalf("runs while waiting: %+v", runs)
	}
	var call string
	for _, c := range runs[0].ToolCalls {
		if strings.HasPrefix(c.ID, "call-") {
			t.Fatalf("tool call row uses the provider id: %+v", c)
		}
		if c.ApprovalState == "pending" {
			call = c.ID
		}
	}
	if call == "" || len(runs[0].ToolCalls) != 3 || len(runs[0].ChangeSets) != 1 || runs[0].ChangeSets[0].Status != "draft" {
		t.Fatalf("no pending approval or draft in %+v", runs[0])
	}
	// The reviewable diff is redacted too; Undo uses the exact snapshot.
	if raw := string(admin.mustStatus(200, "GET", "/api/v1/ai/conversations/"+conv+"/runs", nil)); strings.Contains(raw, "tok-very-secret-value") || !strings.Contains(raw, "login('[REDACTED]')") {
		t.Fatalf("change-set diff exposes the secret: %s", raw)
	}
	admin.mustStatus(204, "POST", "/api/v1/ai/tool-calls/"+call+"/decision", map[string]any{"approve": true})
	r := waitRun(t, admin, run.ID, "completed", "failed")
	if r["status"] != "completed" {
		t.Fatalf("run did not complete: %v", r)
	}
	if got := readBotFile(t, e, bot, "index.js"); got != "login(process.env.DISCORD_TOKEN)\n" {
		t.Fatalf("approved change not applied: %q", got)
	}
	var cv struct {
		Messages []struct{ Role, Content string }
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/ai/conversations/"+conv, nil), &cv)
	if n := len(cv.Messages); n != 2 || cv.Messages[1].Content != "Moved the token into the environment." {
		t.Fatalf("conversation messages: %+v", cv.Messages)
	}

	// After the run finishes the change set, its applied state (for Undo)
	// and the decided approval are still restorable.
	runs = conversationRuns(t, admin, conv)
	if runs[0].Status != "completed" || len(runs[0].ChangeSets) != 1 || runs[0].ChangeSets[0].Status != "applied" {
		t.Fatalf("runs after completion: %+v", runs)
	}
	for _, c := range runs[0].ToolCalls {
		if c.ApprovalState == "pending" {
			t.Fatalf("decided approval still pending after the run: %+v", c)
		}
	}
	change := runs[0].ChangeSets[0].ID
	admin.mustStatus(204, "POST", "/api/v1/ai/change-sets/"+change+"/revert", nil)
	if got := readBotFile(t, e, bot, "index.js"); got != "login('tok-very-secret-value')\n" {
		t.Fatalf("undo did not restore the file: %q", got)
	}

	// Another account cannot see the conversation or its run.
	other := e.user("other@example.com", domain.RoleUser)
	if resp, _ := other.do("GET", "/api/v1/ai/runs/"+run.ID, nil); resp.StatusCode != 404 {
		t.Fatalf("foreign run visible: %d", resp.StatusCode)
	}
}

func TestAIOperatorProviderFailureEndsRun(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"bad key","code":"invalid_api_key"}}`)
	}}}
	_, ai, admin, _, conv := aiEnv(t, fp)
	var run struct{ ID string }
	json.Unmarshal(admin.mustStatus(202, "POST", "/api/v1/ai/conversations/"+conv+"/messages", map[string]any{"content": "hello"}), &run)
	r := waitRun(t, admin, run.ID, "failed", "completed", "cancelled")
	if r["status"] != "failed" || r["error_code"] != "authentication" {
		t.Fatalf("expected an authentication failure: %v", r)
	}
	events, _, cancel := ai.Subscribe(run.ID, 0)
	cancel()
	if last := events[len(events)-1]; last.Type != "error" {
		t.Fatalf("terminal stream event = %q, want error", last.Type)
	}
}

type runView struct {
	ID        string
	Status    string
	ToolCalls []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ApprovalState string `json:"approval_state"`
		Status        string `json:"status"`
		Arguments     string `json:"arguments_json"`
	} `json:"tool_calls"`
	ChangeSets []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"change_sets"`
}

func conversationRuns(t *testing.T, c *client, conv string) []runView {
	t.Helper()
	var out struct{ Runs []runView }
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/ai/conversations/"+conv+"/runs", nil), &out)
	return out.Runs
}

// toolResults returns the tool-result messages the provider received in
// request i, in order.
func toolResults(f *fakeProvider, i int) []map[string]any {
	var out []map[string]any
	msgs, _ := f.body(i)["messages"].([]any)
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
			out = append(out, mm)
		}
	}
	return out
}

// lastToolResult is the content of the newest tool message in request i.
func lastToolResult(f *fakeProvider, i int) string {
	r := toolResults(f, i)
	if len(r) == 0 {
		return ""
	}
	s, _ := r[len(r)-1]["content"].(string)
	return s
}

// approveAll approves every pending decision (the Auto envelope included)
// until the run finishes.
func approveAll(t *testing.T, e *env, c *client, run string) map[string]any {
	t.Helper()
	for i := 0; i < 1000; i++ {
		var r map[string]any
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/ai/runs/"+run, nil), &r)
		switch r["status"] {
		case "completed", "failed", "cancelled", "interrupted":
			return r
		}
		var id string
		e.db.QueryRow(`SELECT id FROM ai_tool_calls WHERE run_id=? AND approval_state='pending' AND name<>'request_environment_values'`, run).Scan(&id)
		if id != "" {
			c.mustStatus(204, "POST", "/api/v1/ai/tool-calls/"+id+"/decision", map[string]any{"approve": true})
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never finished", run)
	return nil
}

type fakeDiagnostic struct {
	out   string
	calls atomic.Int32
}

func (f *fakeDiagnostic) RunDiagnostic(_ context.Context, _, _ string, argv []string) (domain.DiagnosticResult, error) {
	f.calls.Add(1)
	return domain.DiagnosticResult{Output: f.out + strings.Join(argv, " "), Duration: time.Millisecond}, nil
}

func startRun(t *testing.T, c *client, conv, mode, content string) string {
	t.Helper()
	var run struct{ ID string }
	json.Unmarshal(c.mustStatus(202, "POST", "/api/v1/ai/conversations/"+conv+"/messages", map[string]any{"content": content, "mode": mode}), &run)
	return run.ID
}

func TestAIOperatorEnforcesRunLimits(t *testing.T) {
	fp := &fakeProvider{}
	e, ai, admin, bot, conv := aiEnv(t, fp)
	diag := &fakeDiagnostic{out: "ok "}
	ai.Diagnostic = diag
	ai.Limits = service.AIRunLimits{Rounds: 20, Diagnostics: 1, ApplyAttempts: 3, LifecycleActions: 1, WallMinutes: 5, ChangedFiles: 1, ChangedBytes: 64, RetainedOutput: 1 << 20}

	for _, mode := range []string{"approval", "auto"} {
		t.Run(mode, func(t *testing.T) {
			fp.mu.Lock()
			fp.steps, fp.bodies = []func(http.ResponseWriter){
				toolStep("c1", "run_diagnostic", map[string]any{"argv": []string{"node", "--version"}}),
				toolStep("c2", "run_diagnostic", map[string]any{"argv": []string{"npm", "ls"}}),
				toolStep("c3", "restart_bot", map[string]any{}),
				toolStep("c4", "restart_bot", map[string]any{}),
				toolStep("c5", "propose_file_change", map[string]any{"path": "a.txt", "content": "one\n", "summary": "a"}),
				toolStep("c6", "propose_file_change", map[string]any{"path": "b.txt", "content": "two\n", "summary": "b"}),
				toolStep("c7", "propose_file_change", map[string]any{"path": "a.txt", "content": strings.Repeat("x", 100), "summary": "big"}),
				toolStep("c8", "propose_file_change", map[string]any{"path": "a.txt", "content": "three\n", "summary": "a2"}),
				toolStep("c9", "propose_file_change", map[string]any{"path": "a.txt", "content": "four\n", "summary": "a3"}),
				toolStep("c10", "propose_file_change", map[string]any{"path": "a.txt", "content": "five\n", "summary": "a4"}),
				textStep("Stopped at the run limits."),
			}, nil
			fp.mu.Unlock()
			diag.calls.Store(0)
			run := startRun(t, admin, conv, mode, "Check everything")
			if r := approveAll(t, e, admin, run); r["status"] != "completed" {
				t.Fatalf("run: %v", r)
			}
			want := map[int]string{
				1:  "ok node --version",
				2:  "at most 1 diagnostic jobs",
				3:  "Restart requested",
				4:  "at most 1 lifecycle actions",
				5:  "Applied change set",
				6:  "at most 1 changed files",
				7:  "at most 64 changed bytes",
				8:  "Applied change set",
				9:  "Applied change set",
				10: "at most 3 apply attempts",
			}
			for i, w := range want {
				if got := lastToolResult(fp, i); !strings.Contains(got, w) {
					t.Errorf("tool result %d = %q, want %q", i, got, w)
				}
			}
			if n := diag.calls.Load(); n != 1 {
				t.Errorf("diagnostics ran %d times, limit 1", n)
			}
			if got := readBotFile(t, e, bot, "a.txt"); got != "four\n" {
				t.Errorf("a.txt = %q", got)
			}
		})
	}
}

func TestAIOperatorLimitsRetainedOutput(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("c1", "run_diagnostic", map[string]any{"argv": []string{"node", "a"}}),
		toolStep("c2", "list_files", map[string]any{"path": "."}),
		textStep("done"),
	}}
	e, ai, admin, _, conv := aiEnv(t, fp)
	ai.Diagnostic = &fakeDiagnostic{out: strings.Repeat("y", 500)}
	ai.Limits = service.DefaultAIRunLimits()
	ai.Limits.RetainedOutput = 200
	run := startRun(t, admin, conv, "auto", "diagnose")
	approveAll(t, e, admin, run)
	if got := lastToolResult(fp, 1); !strings.Contains(got, "retained-output limit") || len(got) > 400 {
		t.Fatalf("oversized output was not clipped: %d %q", len(got), got)
	}
	if got := lastToolResult(fp, 2); !strings.Contains(got, "bytes of retained tool output") {
		t.Fatalf("tool ran past the retained-output limit: %q", got)
	}
	var stored int
	e.db.QueryRow(`SELECT sum(length(output)) FROM ai_tool_calls WHERE run_id=?`, run).Scan(&stored)
	if stored >= 500 {
		t.Fatalf("retained %d bytes of tool output", stored)
	}
}

func TestAIOperatorAuditsModelActions(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("c1", "request_environment_values", map[string]any{"names": []string{"API_KEY"}}),
		toolStep("c2", "propose_file_change", map[string]any{"path": "fix.js", "content": "ok()\n", "summary": "fix"}),
		toolStep("c3", "run_diagnostic", map[string]any{"argv": []string{"node", "--check", "fix.js"}}),
		toolStep("c4", "restart_bot", map[string]any{}),
		textStep("Fixed."),
	}}
	e, ai, admin, bot, conv := aiEnv(t, fp)
	ai.Diagnostic = &fakeDiagnostic{out: "ok"}
	run := startRun(t, admin, conv, "auto", "fix it")
	go func() {
		// Secure input still waits for the person, even in Auto mode.
		for i := 0; i < 500; i++ {
			var id string
			e.db.QueryRow(`SELECT id FROM ai_tool_calls WHERE run_id=? AND approval_state='pending' AND name='request_environment_values'`, run).Scan(&id)
			if id != "" {
				admin.mustStatus(200, "POST", "/api/v1/ai/tool-calls/"+id+"/secure-input", map[string]any{"values": map[string]string{"API_KEY": "value-never-audited"}})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	if r := approveAll(t, e, admin, run); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	rows, err := e.db.Query(`SELECT action, coalesce(target,''), outcome, coalesce(bot_id,''), coalesce(actor_label,'') FROM audit_events WHERE action LIKE 'ai.%' AND action NOT IN ('ai.run_start','ai.approval','ai.secure_input','ai.conversation_create')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var action, target, outcome, botID, actor string
		rows.Scan(&action, &target, &outcome, &botID, &actor)
		if botID != bot || outcome != "ok" || !strings.Contains(actor, "via AI operator (Auto)") || strings.Contains(target, "value-never-audited") {
			t.Errorf("audit %s: target=%q outcome=%s bot=%s actor=%q", action, target, outcome, botID, actor)
		}
		got[action] = target
	}
	want := map[string]string{"ai.file_apply": "fix.js", "ai.diagnostic": "node --check fix.js", "ai.restart": "", "ai.env_input": "API_KEY"}
	for a, tg := range want {
		if v, ok := got[a]; !ok || v != tg {
			t.Errorf("audit %s = %q (present %v), want %q; all: %v", a, v, ok, tg, got)
		}
	}
}

func TestAIOperatorToolCallIDsAndEnvelope(t *testing.T) {
	// Providers commonly restart call ids at call_0 for every response.
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("call_0", "target_status", map[string]any{}),
		textStep("first"),
		toolStep("call_0", "target_status", map[string]any{}),
		textStep("second"),
	}}
	e, _, admin, _, conv := aiEnv(t, fp)
	for i, mode := range []string{"approval", "auto"} {
		run := startRun(t, admin, conv, mode, "status?")
		if r := approveAll(t, e, admin, run); r["status"] != "completed" {
			t.Fatalf("run %d: %v", i, r)
		}
		var id, pid string
		e.db.QueryRow(`SELECT id, provider_call_id FROM ai_tool_calls WHERE run_id=? AND name='target_status'`, run).Scan(&id, &pid)
		if id == "call_0" || len(id) != 36 || pid != "call_0" {
			t.Fatalf("tool call id=%q provider id=%q", id, pid)
		}
		if res := toolResults(fp, 2*i+1); len(res) != 1 || res[0]["tool_call_id"] != "call_0" {
			t.Fatalf("tool message did not echo the provider id: %v", res)
		}
	}
	runs := conversationRuns(t, admin, conv)
	if len(runs) != 2 || runs[0].ToolCalls[0].Name != "auto_repair_envelope" {
		t.Fatalf("runs: %+v", runs)
	}
	env := runs[0].ToolCalls[0]
	if env.ApprovalState != "approved" || !strings.Contains(env.Arguments, `"apply_attempts":3`) {
		t.Fatalf("envelope: %+v", env)
	}
	for _, claim := range []string{"deploy", "publish", "startup"} {
		if strings.Contains(env.Arguments, claim) {
			t.Fatalf("envelope claims an unimplemented %s action: %s", claim, env.Arguments)
		}
	}
}

func TestAIOperatorRetiresFinishedStreams(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){textStep("hello")}}
	_, ai, admin, _, conv := aiEnv(t, fp)
	ai.StreamGrace = 50 * time.Millisecond
	run := startRun(t, admin, conv, "approval", "hi")
	waitRun(t, admin, run, "completed")
	for i := 0; ; i++ {
		events, ch, cancel := ai.Subscribe(run, 0)
		_, open := <-ch
		cancel()
		if len(events) == 0 && !open {
			return
		}
		if i > 200 {
			t.Fatalf("finished run stream was never dropped (%d events)", len(events))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeDNS answers A queries from answer(name, nth query for that name).
func fakeDNS(t *testing.T, answer func(name string, n int) net.IP) *net.Resolver {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	var mu sync.Mutex
	counts := map[string]int{}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
			b.StartQuestions()
			b.Question(q)
			b.StartAnswers()
			if q.Type == dnsmessage.TypeA {
				name := strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")
				mu.Lock()
				counts[name]++
				c := counts[name]
				mu.Unlock()
				if ip := answer(name, c); ip != nil {
					var a [4]byte
					copy(a[:], ip.To4())
					b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET}, dnsmessage.AResource{A: a})
				}
			}
			msg, _ := b.Finish()
			pc.WriteTo(msg, addr)
		}
	}()
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", pc.LocalAddr().String())
	}}
}

func TestAIResearchBlocksInternalAddresses(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
		fmt.Fprint(w, "internal secret page")
	}))
	t.Cleanup(internal.Close)
	_, port, _ := net.SplitHostPort(internal.Listener.Addr().String())

	var keySeen atomic.Value
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keySeen.Store(r.Header.Get("X-API-Key"))
		if r.URL.Query().Get("q") == "redirect" {
			http.Redirect(w, r, internal.URL+"/steal", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"results":[{"title":"internal","url":"%s/x","content":"no"},{"title":"Public","url":"https://public.test/a","content":"yes"}]}`, internal.URL)
	}))
	t.Cleanup(search.Close)

	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("c1", "web_search", map[string]any{"query": "rivetpanel"}),
		toolStep("c2", "web_search", map[string]any{"query": "redirect"}),
		toolStep("c3", "web_fetch", map[string]any{"url": "http://rebind.test:" + port + "/"}),
		toolStep("c4", "web_fetch", map[string]any{"url": "http://100.64.0.1/"}),
		toolStep("c5", "web_fetch", map[string]any{"url": internal.URL + "/"}),
		textStep("done"),
	}}
	_, ai, admin, _, conv := aiEnv(t, fp)
	// rebind.test passes the first lookup with a public address, then
	// rebinds to loopback for the connection.
	ai.Research = &operator.Research{Resolver: fakeDNS(t, func(name string, n int) net.IP {
		switch {
		case name == "public.test", name == "rebind.test" && n == 1:
			return net.ParseIP("93.184.216.34")
		case name == "rebind.test":
			return net.ParseIP("127.0.0.1")
		}
		return nil
	})}
	// The administrator's self-hosted search origin may be private.
	admin.mustStatus(200, "PUT", "/api/v1/admin/ai/search", map[string]any{"search_enabled": true, "fetch_enabled": true, "base_url": search.URL, "keys": []string{"search-key"}, "results": 5, "language": "auto", "safe_search": 1})

	run := startRun(t, admin, conv, "approval", "research")
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run: %v", r)
	}
	if got := lastToolResult(fp, 1); !strings.Contains(got, "https://public.test/a") || strings.Contains(got, internal.URL) {
		t.Fatalf("search results were not filtered to public URLs: %s", got)
	}
	if keySeen.Load() != "search-key" {
		t.Fatalf("private search origin did not receive its key: %v", keySeen.Load())
	}
	if got := lastToolResult(fp, 2); !strings.Contains(got, "another origin") {
		t.Fatalf("cross-origin search redirect was followed: %s", got)
	}
	for i := 3; i <= 5; i++ {
		if got := lastToolResult(fp, i); !strings.Contains(got, "addresses are blocked") {
			t.Errorf("fetch %d was not blocked: %s", i, got)
		}
	}
	if n := internalHits.Load(); n != 0 {
		t.Fatalf("internal server was reached %d times", n)
	}
}

func TestAIProviderKeyFromSetupAndAdminSettings(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){textStep("one"), textStep("two")}}
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)
	e, st, _ := setupEnv(t, service.EnvSettings{})
	ai := &service.AIService{Store: e.db, Keys: e.bots.Keys, Bots: e.bots, Files: e.bots.Workspaces.(*filesystem.Manager), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ai.Start(ctx)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog, Settings: st, SecureCookies: true,
		Nodes: e.db, Files: e.bots.Workspaces.(*filesystem.Manager), MaxUpload: 2 << 20, AI: ai})

	anon := &client{e: e}
	code := st.SetupCode()
	provider := map[string]any{"name": "DeepSeek", "base_url": srv.URL, "default_model": "deepseek-chat", "key": "sk-from-setup"}
	// An invalid provider is refused before the administrator is created.
	anon.mustStatus(400, "POST", "/api/v1/setup/complete", map[string]any{"code": code, "email": "owner@example.com", "password": pw,
		"settings": map[string]any{}, "ai_provider": map[string]any{"name": "x", "base_url": srv.URL, "default_model": "m"}})
	resp, raw := anon.do("POST", "/api/v1/setup/complete", map[string]any{"code": code, "email": "owner@example.com", "password": pw,
		"settings": map[string]any{}, "ai_provider": provider})
	if resp.StatusCode != 200 || strings.Contains(string(raw), "settings_error") {
		t.Fatalf("setup: %d %s", resp.StatusCode, raw)
	}
	admin := &client{e: e}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			admin.cookie = ck.Value
		}
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(raw, &out)
	admin.csrf = out.CSRF

	var list struct {
		Providers []map[string]any
	}
	raw = admin.mustStatus(200, "GET", "/api/v1/admin/ai/providers", nil)
	json.Unmarshal(raw, &list)
	if len(list.Providers) != 1 || list.Providers[0]["key_set"] != true || list.Providers[0]["default"] != true || strings.Contains(string(raw), "sk-from-setup") {
		t.Fatalf("setup provider: %s", raw)
	}
	id := list.Providers[0]["id"].(string)

	bot := admin.createBot("aibot")
	var conv struct{ ID string }
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/bots/"+bot+"/ai/conversations", map[string]any{}), &conv)
	waitRun(t, admin, startRun(t, admin, conv.ID, "approval", "hi"), "completed")

	// Administration replaces the key (and edits the other fields) in place.
	edit := map[string]any{"name": "DeepSeek", "enabled": true, "default": true, "base_url": srv.URL, "chat_path": "/chat/completions", "models_path": "/models",
		"default_model": "deepseek-chat", "context_size": 64000, "max_output_tokens": 2048, "temperature": 0.5, "timeout_ms": 30000,
		"input_price_micros": 270000, "output_price_micros": 1100000, "key": "sk-rotated"}
	admin.mustStatus(400, "PATCH", "/api/v1/admin/ai/providers/"+id, map[string]any{"name": "DeepSeek", "base_url": srv.URL, "chat_path": "/c", "models_path": "/m", "default_model": "m", "temperature": 3})
	raw = admin.mustStatus(200, "PATCH", "/api/v1/admin/ai/providers/"+id, edit)
	if strings.Contains(string(raw), "sk-rotated") || !strings.Contains(string(raw), `"context_size":64000`) {
		t.Fatalf("edited provider: %s", raw)
	}
	waitRun(t, admin, startRun(t, admin, conv.ID, "approval", "again"), "completed")
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.auth) != 2 || fp.auth[0] != "Bearer sk-from-setup" || fp.auth[1] != "Bearer sk-rotated" {
		t.Fatalf("provider saw keys %v", fp.auth)
	}
}
