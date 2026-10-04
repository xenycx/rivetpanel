package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/runtimes"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

func nodeRuntime(t *testing.T) runtimes.Runtime {
	t.Helper()
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	rt, ok := cat.Get("nodejs")
	if !ok {
		t.Fatal("no nodejs runtime")
	}
	return rt
}

func TestDiagnosticPolicyAllowsInspectionAndExplainsRefusals(t *testing.T) {
	rt := nodeRuntime(t)
	for _, ok := range [][]string{
		{"node", "--version"}, {"node", "-v"}, {"node", "--check", "index.js"}, {"node", "index.js"},
		{"npm", "ls"}, {"npm", "test"}, {"npm", "run", "lint"},
	} {
		if err := validateDiagnostic(rt, ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range [][]string{
		{"ls", "-la"}, {"cat", "index.js"}, {"sh", "-c", "id"}, {"npm", "install"}, {"curl", "http://x"},
		{"node", "-e", "1"}, {"node", "--eval=1"}, {"node", "-p", "1"}, {"node", "../x.js"}, {"node", "/etc/passwd"}, {"node", "a;b"},
	} {
		if err := validateDiagnostic(rt, bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	// A refusal names the runtime and the allowed commands so the caller can
	// pick one instead of guessing again.
	err := validateDiagnostic(rt, []string{"ls"})
	for _, want := range []string{"Node.js", "node --check", "npm test", "read_file", "read_logs"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal lacks %q: %v", want, err)
		}
	}
}

func TestDiagnosticPolicyPythonEvalAndAlias(t *testing.T) {
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := cat.Get("python")
	for _, ok := range [][]string{{"python3", "main.py"}, {"python", "-m", "py_compile", "main.py"}, {"python3", "-m", "pytest", "-c", "setup.cfg"}, {"python", "--version"}} {
		if err := validateDiagnostic(rt, ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"python", "-c", "print(1)"}, {"python3", "-W", "ignore", "-c", "1"}, {"python", "-Ic", "1"}} {
		if err := validateDiagnostic(rt, bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

// A diagnostic container carries its bot's id. The reconciler must not mistake
// it for a stale duplicate of the bot's runtime and stop it mid-run (which made
// every AI diagnostic die with exit code 137).
func TestReconcilerLeavesDiagnosticContainersAlone(t *testing.T) {
	g := newRig(t, func(o *Options) { o.InstallID = "install-1" })
	g.fd.conts["diag1"] = &ContainerInfo{ID: "diag1", Name: "rivetpanel-diag-1", State: "running", StartedAt: g.now,
		Labels: map[string]string{LabelManaged: "true", LabelBot: botID, LabelNode: g.r.opts.NodeID, LabelRole: string(RoleDiagnostic), LabelInstall: "install-1"}}
	g.desire("running", false)
	g.pass()
	if _, ok := g.fd.conts["diag1"]; !ok || g.fd.conts["diag1"].State != "running" {
		t.Fatalf("the reconciler removed or stopped the diagnostic container: %+v", g.fd.conts["diag1"])
	}
	if b := g.wantState("running"); b.ContainerID != nil && *b.ContainerID == "diag1" {
		t.Fatal("the diagnostic container was adopted as the bot's runtime")
	}
}

func TestResyncSweepsOnlyOrphanedDiagnosticContainers(t *testing.T) {
	g := newRig(t, func(o *Options) { o.InstallID = "install-1" })
	lab := func() map[string]string {
		return map[string]string{LabelManaged: "true", LabelBot: botID, LabelNode: g.r.opts.NodeID, LabelRole: string(RoleDiagnostic), LabelInstall: "install-1"}
	}
	g.fd.listOmitsLiveStart = true // the real adapter only reports start times through Inspect
	g.fd.conts["fresh"] = &ContainerInfo{ID: "fresh", Name: "d-fresh", State: "exited", FinishedAt: g.now.Add(-time.Minute), Labels: lab()}
	g.fd.conts["old"] = &ContainerInfo{ID: "old", Name: "d-old", State: "exited", FinishedAt: g.now.Add(-2 * time.Hour), Labels: lab()}
	g.fd.conts["hung"] = &ContainerInfo{ID: "hung", Name: "d-hung", State: "running", StartedAt: g.now.Add(-2 * time.Hour), Labels: lab()}
	if err := g.r.resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.fd.conts["fresh"]; !ok {
		t.Fatal("a recent diagnostic container was swept while its run may still be reading it")
	}
	for _, id := range []string{"old", "hung"} {
		if _, ok := g.fd.conts[id]; ok {
			t.Fatalf("orphaned diagnostic container %s was kept", id)
		}
	}
}
