package api

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// fakeStdin records one-shot console commands.
type fakeStdin struct {
	mu    sync.Mutex
	lines []string
}

type stdinWriter struct {
	f *fakeStdin
	b strings.Builder
}

func (w *stdinWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
func (w *stdinWriter) Close() error {
	w.f.mu.Lock()
	w.f.lines = append(w.f.lines, w.b.String())
	w.f.mu.Unlock()
	return nil
}
func (f *fakeStdin) AttachStdin(ctx context.Context, id string) (io.WriteCloser, error) {
	return &stdinWriter{f: f}, nil
}
func (f *fakeStdin) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func markRunning(t *testing.T, e *env, id string) {
	t.Helper()
	b, err := e.db.GetBot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.db.SetDesired(context.Background(), id, domain.DesiredRunning, false, 1); err != nil {
		t.Fatal(err)
	}
	b, _ = e.db.GetBot(context.Background(), id)
	if ok, err := e.db.Observe(context.Background(), sqlite.Observation{BotID: id, Generation: b.Generation, State: "running",
		SettleGeneration: true, ContainerID: "c-" + id[:8], NowMS: 1}); err != nil || !ok {
		t.Fatalf("observe: %v %v", ok, err)
	}
}

func TestScheduleTaskChains(t *testing.T) {
	e, sch, _ := scheduleEnv(t)
	stdin := &fakeStdin{}
	e.bots.Stdin = stdin
	var waited []time.Duration
	sch.Sleep = func(ctx context.Context, d time.Duration) bool { waited = append(waited, d); return true }

	owner := e.user("owner@x.io", domain.RoleUser)
	id := owner.createBot("mc")
	base := "/api/v1/bots/" + id

	// Validation: empty chain, multi-line command, too-long waits, unknown task.
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "chain", "spec": "0 4 * * *"})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "chain", "spec": "0 4 * * *",
		"tasks": []map[string]any{{"action": "command", "payload": "say a\nop me"}}})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "chain", "spec": "0 4 * * *",
		"tasks": []map[string]any{{"action": "command", "payload": "x", "delay_seconds": 3000}, {"action": "restart", "delay_seconds": 3000}}})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "chain", "spec": "0 4 * * *",
		"tasks": []map[string]any{{"action": "shell", "payload": "rm -rf /"}}})

	var sc scheduleDTO
	json.Unmarshal(owner.mustStatus(201, "POST", base+"/schedules", map[string]any{"action": "chain", "spec": "0 4 * * *",
		"tasks": []map[string]any{
			{"action": "command", "payload": "say Restarting in 5 minutes"},
			{"action": "command", "payload": "save-all", "delay_seconds": 300},
			{"action": "restart", "delay_seconds": 10},
		}}), &sc)
	if sc.Action != "chain" || len(sc.Tasks) != 3 || sc.Tasks[1].DelaySeconds != 300 {
		t.Fatalf("created %+v", sc)
	}

	// A stopped server: the first command fails and stops the chain.
	var run struct{ Status, Message string }
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/schedules/"+sc.ID+"/run", nil), &run)
	sch.WaitChains()
	got := e.getSchedule(t, owner, base, sc.ID)
	if run.Status != "ok" || got.LastStatus == nil || *got.LastStatus != "failed" || !strings.Contains(*got.LastMessage, "task 1") {
		t.Fatalf("stopped server chain: %+v / %+v", run, got)
	}

	// Running: both commands are delivered, waits honoured, restart requested.
	markRunning(t, e, id)
	before := e.rec.count()
	owner.mustStatus(200, "POST", base+"/schedules/"+sc.ID+"/run", nil)
	sch.WaitChains()
	lines := stdin.got()
	if len(lines) != 2 || lines[0] != "say Restarting in 5 minutes\n" || lines[1] != "save-all\n" {
		t.Fatalf("commands %q", lines)
	}
	if len(waited) < 3 || waited[len(waited)-2] != 300*time.Second || waited[len(waited)-1] != 10*time.Second {
		t.Fatalf("waits %v", waited)
	}
	if e.rec.count() != before+1 {
		t.Fatal("restart not requested")
	}
	got = e.getSchedule(t, owner, base, sc.ID)
	if got.LastStatus == nil || *got.LastStatus != "ok" {
		t.Fatalf("running chain: %+v", got)
	}

	// The command endpoint uses the same path and permission.
	owner.mustStatus(204, "POST", base+"/command", map[string]string{"command": "list"})
	if l := stdin.got(); l[len(l)-1] != "list\n" {
		t.Fatalf("command endpoint %q", l)
	}
	viewer := e.user("viewer@x.io", domain.RoleUser)
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "viewer@x.io", "permissions": domain.PermViewConsole})
	if resp, _ := viewer.do("POST", base+"/command", map[string]string{"command": "op viewer"}); resp.StatusCode != 403 {
		t.Fatalf("console-only user sent a command: %d", resp.StatusCode)
	}
	// Converting back to a simple schedule drops the tasks.
	var simple scheduleDTO
	json.Unmarshal(owner.mustStatus(200, "PATCH", base+"/schedules/"+sc.ID, map[string]any{"action": "restart"}), &simple)
	if simple.Action != "restart" || len(simple.Tasks) != 0 {
		t.Fatalf("converted %+v", simple)
	}
}

func (e *env) getSchedule(t *testing.T, c *client, base, id string) scheduleDTO {
	t.Helper()
	var list struct {
		Schedules []scheduleDTO `json:"schedules"`
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/schedules", nil), &list)
	for _, s := range list.Schedules {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("schedule %s missing", id)
	return scheduleDTO{}
}
