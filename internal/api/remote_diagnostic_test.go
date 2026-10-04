package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// fakeNodeDiagnostics stands in for noderoute.Router.RunDiagnostic (the real
// wire is covered by internal/noderoute's hub+agent test).
type fakeNodeDiagnostics struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeNodeDiagnostics) RunDiagnostic(_ context.Context, nodeID, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, nodeID+" "+botID+" "+runtimeID+" "+strings.Join(argv, " "))
	return domain.DiagnosticResult{ExitCode: 0, Output: "node says ok; token tok-very-secret-value", Duration: time.Millisecond}, nil
}

// TestAIRemoteDiagnosticRunsOnNode: a remote server's diagnostic is sent to
// its node with the node id, the panel's local runner (which would copy the
// panel's disk) is never used, the output is redacted, and an offline node
// is refused before anything is sent.
func TestAIRemoteDiagnosticRunsOnNode(t *testing.T) {
	fp := &fakeProvider{}
	e, ai, admin, bot, conv := aiEnv(t, fp)
	local := &fakeDiagnostic{out: "PANEL-LOCAL "}
	ai.Diagnostic = local
	node := newFakeNodeFiles(t)
	placeOnRemoteNode(t, e, bot, node)

	run := func(want string) {
		t.Helper()
		fp.mu.Lock()
		fp.steps, fp.bodies = []func(http.ResponseWriter){
			toolStep("c1", "run_diagnostic", map[string]any{"argv": []string{"node", "--check", "index.js"}}),
			textStep("done"),
		}, nil
		fp.mu.Unlock()
		r := approveAll(t, e, admin, startRun(t, admin, conv, "auto", "check it"))
		if r["status"] != "completed" {
			t.Fatalf("run: %v", r)
		}
		if got := lastToolResult(fp, 1); !strings.Contains(got, want) {
			t.Fatalf("tool result %q, want %q", got, want)
		}
	}

	// Not configured for remote diagnostics: refused, local runner unused.
	run("not available for servers on remote nodes")

	remote := &fakeNodeDiagnostics{}
	ai.RemoteDiagnostic = remote
	run("node says ok")
	if got := lastToolResult(fp, 1); strings.Contains(got, "tok-very-secret-value") {
		t.Fatalf("node output not redacted: %q", got)
	}
	remote.mu.Lock()
	calls := append([]string(nil), remote.calls...)
	remote.mu.Unlock()
	if len(calls) != 1 || calls[0] != remoteDeployNode+" "+bot+" nodejs node --check index.js" {
		t.Fatalf("node calls %v", calls)
	}

	node.mu.Lock()
	node.online = false
	node.mu.Unlock()
	run("node is offline")
	remote.mu.Lock()
	n := len(remote.calls)
	remote.mu.Unlock()
	if n != 1 {
		t.Fatalf("offline node was asked to run a diagnostic (%d calls)", n)
	}
	if c := local.calls.Load(); c != 0 {
		t.Fatalf("panel-local diagnostic ran %d times for a remote server", c)
	}
}
