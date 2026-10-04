package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// blockingLogs never returns until its context ends, like a log source on an
// unresponsive node.
type blockingLogs struct{}

func (blockingLogs) Tail(ctx context.Context, _ string, _ int) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// Every message the provider receives carries a "content" string, also an
// assistant turn that only called tools and the result of a tool that
// returned nothing (the cause of "messages[8]: missing field content").
func TestAIProviderAlwaysReceivesContent(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("call-empty", "read_file", map[string]any{"path": "empty.txt"}),
		toolStep("call-legacy", "read_file", map[string]any{"path": filesystem.LegacyDeployManifest}),
		textStep("done"),
	}}
	e, _, admin, bot, conv := aiEnv(t, fp)
	w, err := e.bots.Workspaces.(*filesystem.Manager).Open(bot)
	if err != nil {
		t.Fatal(err)
	}
	w.Write("empty.txt", strings.NewReader(""), 1<<20)
	w.Write(filesystem.LegacyDeployManifest, strings.NewReader(`["index.js"]`), 1<<20)
	w.Close()
	run := startRun(t, admin, conv, "approval", "read the files")
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run = %v", r)
	}
	for i := 1; i <= 2; i++ {
		msgs, _ := fp.body(i)["messages"].([]any)
		for j, m := range msgs {
			mm := m.(map[string]any)
			if _, ok := mm["content"].(string); !ok {
				t.Fatalf("request %d messages[%d] has no content string: %v", i, j, mm)
			}
			if mm["role"] == "tool" && mm["content"] == "" {
				t.Fatalf("request %d messages[%d]: empty tool content", i, j)
			}
		}
	}
	if got := lastToolResult(fp, 2); !strings.Contains(got, "Permission") {
		t.Fatalf("the pre-rename deploy manifest is internal and must be protected: %q", got)
	}
}

// A read-only tool that hangs is stopped after the tool timeout; the model
// and the person get an error and the run continues instead of hanging.
func TestAIToolTimeoutDoesNotHangTheRun(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("call-logs", "read_logs", map[string]any{}),
		textStep("The node did not answer."),
	}}
	e, ai, admin, bot, conv := aiEnv(t, fp)
	ai.Logs = blockingLogs{}
	ai.ToolTimeout = 200 * time.Millisecond
	e.db.Exec(`UPDATE bots SET container_id='c1' WHERE id=?`, bot)
	run := startRun(t, admin, conv, "approval", "logs?")
	if r := waitRun(t, admin, run, "completed", "failed"); r["status"] != "completed" {
		t.Fatalf("run = %v", r)
	}
	if got := lastToolResult(fp, 1); !strings.Contains(got, "did not finish in time") {
		t.Fatalf("tool result = %q", got)
	}
	runs := conversationRuns(t, admin, conv)
	if len(runs) == 0 || len(runs[0].ToolCalls) != 1 || runs[0].ToolCalls[0].Status != "failed" {
		t.Fatalf("tool call = %+v", runs)
	}
}

// Tool calls left open by a run that ended mid-call (stored before runs
// closed their calls) are shown and repaired as ended, never as running.
func TestAIOpenToolCallsOfEndedRunsAreClosed(t *testing.T) {
	fp := &fakeProvider{steps: []func(http.ResponseWriter){
		toolStep("call-read", "read_file", map[string]any{"path": "index.js"}),
		textStep("ok"),
	}}
	e, ai, admin, _, conv := aiEnv(t, fp)
	run := startRun(t, admin, conv, "approval", "read")
	waitRun(t, admin, run, "completed")
	// Simulate the pre-fix state: the run failed while its call was running.
	e.db.Exec(`UPDATE ai_runs SET status='failed' WHERE id=?`, run)
	e.db.Exec(`UPDATE ai_tool_calls SET status='running', finished_at_ms=NULL, error_message=NULL WHERE run_id=?`, run)
	if runs := conversationRuns(t, admin, conv); runs[0].ToolCalls[0].Status != "failed" {
		t.Fatalf("view shows an ended run's call as %q", runs[0].ToolCalls[0].Status)
	}
	// A panel start repairs the stored rows.
	ai.Start(context.Background())
	var status string
	if err := e.db.QueryRow(`SELECT status FROM ai_tool_calls WHERE run_id=?`, run).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("stored status = %q, %v", status, err)
	}
}
