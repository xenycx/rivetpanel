package noderoute_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/agentnode"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

// diagDocker is the node's Docker as a diagnostic sees it: it records the
// container spec and what the snapshot it was given contains.
type diagDocker struct {
	runner.Docker // unused methods panic

	mu       sync.Mutex
	spec     runner.ContainerSpec
	snapshot map[string]string
	removed  bool
}

func (d *diagDocker) ResolveImage(_ context.Context, ref string) (string, error) { return ref, nil }
func (d *diagDocker) Create(_ context.Context, spec runner.ContainerSpec) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.spec = spec
	d.snapshot = map[string]string{}
	_ = filepath.WalkDir(spec.WorkspaceHostPath, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(spec.WorkspaceHostPath, p)
			d.snapshot[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return "diag-1", nil
}
func (d *diagDocker) Start(context.Context, string) error         { return nil }
func (d *diagDocker) Wait(context.Context, string) (int64, error) { return 3, nil }
func (d *diagDocker) Tail(context.Context, string, int) (string, error) {
	return "SyntaxError: line 1 token=abc", nil
}
func (d *diagDocker) Remove(context.Context, string) error {
	d.mu.Lock()
	d.removed = true
	d.mu.Unlock()
	return nil
}

// TestRemoteDiagnosticRunsOnNode runs an AI diagnostic for a remote server
// through a real hub and agent: the agent's own runner.Diagnostic validates
// the command, copies a safe snapshot of the node's workspace (never the
// live directory, never secret files) and runs the locked-down container on
// the node's Docker; the scratch copy is gone afterwards.
func TestRemoteDiagnosticRunsOnNode(t *testing.T) {
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	dk := &diagDocker{}
	scratch := t.TempDir()
	r, nodeID, files, _ := remoteNodeWith(t, func(d *agentnode.Deps) {
		d.Diagnostics = &runner.Diagnostic{Docker: dk, Files: d.Files, Catalog: cat, ScratchRoot: scratch, InstallID: "install-1",
			NodeID: "node-x", Timeout: time.Minute, MemoryBytes: 768 << 20, NanoCPUs: 1e9, PidsLimit: 256, TmpfsBytes: 128 << 20}
	})
	ctx := t.Context()
	bot := uuid.NewString()
	if err := r.CreateWorkspace(ctx, nodeID, bot); err != nil {
		t.Fatal(err)
	}
	for p, v := range map[string]string{"main.py": "print(", ".env": "TOKEN=secret"} {
		if _, err := r.WriteFile(ctx, nodeID, bot, p, []byte(v), "", true); err != nil {
			t.Fatal(err)
		}
	}

	res, err := r.RunDiagnostic(ctx, nodeID, bot, "python", []string{"python", "-m", "py_compile", "main.py"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || !strings.Contains(res.Output, "SyntaxError") {
		t.Fatalf("result %+v", res)
	}
	dk.mu.Lock()
	spec, snap, removed := dk.spec, dk.snapshot, dk.removed
	dk.mu.Unlock()
	if spec.Network != "none" || spec.Role != runner.RoleDiagnostic || spec.MemoryBytes != 768<<20 || spec.PidsLimit != 256 || spec.TmpfsBytes != 128<<20 || spec.InstallID != "install-1" {
		t.Fatalf("sandbox spec %+v", spec)
	}
	live, _ := files.Path(bot)
	if spec.WorkspaceHostPath == live || !strings.HasPrefix(spec.WorkspaceHostPath, scratch) {
		t.Fatalf("diagnostic mounted %q (live %q)", spec.WorkspaceHostPath, live)
	}
	if snap["main.py"] != "print(" || snap[".env"] != "" {
		t.Fatalf("snapshot %v", snap)
	}
	if _, ok := snap[".env"]; ok {
		t.Fatal("secret file copied into the diagnostic snapshot")
	}
	if !removed {
		t.Fatal("diagnostic container not removed")
	}
	if es, _ := os.ReadDir(scratch); len(es) != 0 {
		t.Fatalf("scratch copy left behind: %v", es)
	}

	// The node validates argv against its own catalog.
	var ve *domain.ValidationError
	if _, err := r.RunDiagnostic(ctx, nodeID, bot, "python", []string{"sh", "-c", "id"}); !errors.As(err, &ve) {
		t.Fatalf("disallowed command: %v", err)
	}
	if _, err := r.RunDiagnostic(ctx, nodeID, bot, "python", []string{"python", "-c", "1"}); !errors.As(err, &ve) {
		t.Fatalf("interpreter eval: %v", err)
	}
	if _, err := r.RunDiagnostic(ctx, nodeID, uuid.NewString(), "python", []string{"python", "--version"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown workspace: %v", err)
	}
	if _, err := r.RunDiagnostic(ctx, uuid.NewString(), bot, "python", []string{"python", "--version"}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline: %v", err)
	}
}

// TestRemoteTelemetryAndDiskPreflight reads a node's telemetry over the real
// wire and checks that staging is refused before anything reaches the node
// when the payload plus the margin would not fit.
func TestRemoteTelemetryAndDiskPreflight(t *testing.T) {
	r, nodeID, files, db := remoteNodeWith(t, func(d *agentnode.Deps) {
		d.Telemetry = func() agentproto.Telemetry {
			return agentproto.Telemetry{LogicalCPUs: 4, MemoryUsedBytes: 1 << 30, MemoryTotalBytes: 4 << 30, Load1: 0.5}
		}
	})
	ctx := t.Context()
	tel, err := r.Telemetry(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	total, free, err := files.Statfs()
	if err != nil {
		t.Fatal(err)
	}
	if tel.LogicalCPUs != 4 || tel.MemoryTotalBytes != 4<<30 || tel.DiskTotalBytes != total || tel.DiskFreeBytes == 0 || tel.SampledAtMS == 0 {
		t.Fatalf("telemetry %+v (statfs %d/%d)", tel, total, free)
	}

	// The panel stores the node's telemetry as a node sample.
	if n := r.SampleNodes(ctx, db); n != 1 {
		t.Fatalf("sampled %d nodes", n)
	}
	samples, err := db.ListTelemetry(ctx, nodeID, 0, 10)
	if err != nil || len(samples) != 1 {
		t.Fatalf("samples %v %v", samples, err)
	}
	if s := samples[0]; s.LogicalCPUs != 4 || s.MemoryTotalBytes != 4<<30 || s.DiskTotalBytes != int64(total) || s.DiskUsedBytes <= 0 || s.Load1 != 0.5 {
		t.Fatalf("stored sample %+v", s)
	}

	bot := uuid.NewString()
	if err := r.CreateWorkspace(ctx, nodeID, bot); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureDiskFree(ctx, nodeID, 1<<20); err != nil {
		t.Fatal("small payload refused:", err)
	}
	// A margin larger than the disk: every staging path is refused up front.
	r.DiskMargin = int64(total) + 1
	var ve *domain.ValidationError
	if err := r.EnsureDiskFree(ctx, nodeID, 10); !errors.Is(err, noderoute.ErrNodeDiskFull) || !errors.As(err, &ve) || !strings.Contains(err.Error(), "free disk space") {
		t.Fatalf("full disk: %v", err)
	}
	big := make([]byte, 2<<20)
	if _, err := r.WriteFile(ctx, nodeID, bot, "big.bin", big, "", true); !errors.Is(err, noderoute.ErrNodeDiskFull) {
		t.Fatalf("large write: %v", err)
	}
	if _, ok := nodeFile(t, files, bot, "big.bin"); ok {
		t.Fatal("refused file reached the node")
	}
	if _, _, err := r.BeginPatch(ctx, nodeID, bot, []filesystem.PatchFile{{Path: "a.txt", After: []byte("x")}}, 1<<20, 1<<20); !errors.Is(err, noderoute.ErrNodeDiskFull) {
		t.Fatalf("patch: %v", err)
	}
	if _, err := r.BeginDeploy(ctx, nodeID, bot, strings.NewReader("not reached"), "", filesystem.DefaultBackupLimits); !errors.Is(err, noderoute.ErrNodeDiskFull) {
		t.Fatalf("deploy: %v", err)
	}
	if _, err := r.BeginRestore(ctx, nodeID, bot, strings.NewReader("not reached"), filesystem.DefaultBackupLimits); !errors.Is(err, noderoute.ErrNodeDiskFull) {
		t.Fatalf("restore: %v", err)
	}
	// Small single-file edits skip the preflight.
	if _, err := r.WriteFile(ctx, nodeID, bot, "small.txt", []byte("ok"), "", true); err != nil {
		t.Fatal("small write:", err)
	}
	// Disabled check.
	r.DiskMargin = -1
	if err := r.EnsureDiskFree(ctx, nodeID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Telemetry(ctx, uuid.NewString()); err == nil {
		t.Fatal("telemetry from an unknown node")
	}
}

// TestRemoteAddonDataRemoval deletes one add-on's data on the node through a
// real hub and agent; other add-ons' data and unknown kinds are untouched.
func TestRemoteAddonDataRemoval(t *testing.T) {
	root := addons.DataRoot{Dir: t.TempDir()}
	r, nodeID, _, _ := remoteNodeWith(t, func(d *agentnode.Deps) { d.AddonData = root })
	ctx := t.Context()
	bot := uuid.NewString()
	for _, k := range []string{"postgres", "redis"} {
		p, err := root.Path(bot, k)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "data"), []byte(k), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RemoveAddonData(ctx, nodeID, bot, "redis"); err != nil {
		t.Fatal(err)
	}
	if p, _ := root.Path(bot, "redis"); exists(p) {
		t.Fatal("redis data still on the node")
	}
	if p, _ := root.Path(bot, "postgres"); !exists(p) {
		t.Fatal("postgres data removed too")
	}
	var ve *domain.ValidationError
	if err := r.RemoveAddonData(ctx, nodeID, bot, "../postgres"); !errors.As(err, &ve) {
		t.Fatalf("unknown kind: %v", err)
	}
	if err := r.RemoveAddonData(ctx, nodeID, "not-a-uuid", "redis"); !errors.As(err, &ve) {
		t.Fatalf("invalid id: %v", err)
	}
	if p, _ := root.Path(bot, "postgres"); !exists(p) {
		t.Fatal("postgres data removed by an invalid request")
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
