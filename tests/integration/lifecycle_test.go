//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"github.com/xenycx/rivetpanel/internal/service"
)

const nodeIdle = `
process.stdin.on('data', d => console.log('IN:' + d.toString().trim()));
process.stdin.on('end', () => console.log('STDIN-EOF'));
console.log('READY token=' + process.env.DISCORD_TOKEN);
setInterval(() => {}, 1000);
`

func TestStartStopRestartDeleteLifecycle(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeIdle)
	if err := s.bots.SetEnv(s.ctx, s.user, b.ID, map[string]string{"DISCORD_TOKEN": "tok-123"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	running := s.waitObserved(b.ID, "running", 5*time.Minute)
	if running.ObservedGeneration != running.Generation || running.ContainerID == nil {
		t.Fatalf("%+v", running)
	}
	first := *running.ContainerID
	s.waitLog(first, "READY token=tok-123", 30*time.Second)

	// Stdin stays open with no client attached (no EOF), so bots reading input keep running.
	time.Sleep(3 * time.Second)
	if strings.Contains(s.logs(first), "STDIN-EOF") || s.get(b.ID).ObservedState != "running" {
		t.Fatal("stdin closed while no client was attached")
	}

	// A repeated Start neither changes the generation nor replaces the container.
	again, _ := s.bots.Start(s.ctx, s.user, b.ID)
	if again.Generation != running.Generation {
		t.Fatal("repeat start advanced generation")
	}
	time.Sleep(2500 * time.Millisecond) // a full resync cycle
	if cur := s.get(b.ID); cur.ContainerID == nil || *cur.ContainerID != first {
		t.Fatal("container replaced without a new generation")
	}
	if n := len(s.containers(b.ID)); n != 1 {
		t.Fatalf("%d containers", n)
	}

	// Restart replaces the container.
	if _, err := s.bots.Restart(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("replacement", time.Minute, func() bool {
		c := s.get(b.ID)
		return c.ObservedState == "running" && c.ObservedGeneration == c.Generation && c.ContainerID != nil && *c.ContainerID != first
	})
	if n := len(s.containers(b.ID)); n != 1 {
		t.Fatalf("restart left %d containers", n)
	}

	// Stop.
	if _, err := s.bots.Stop(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	stopped := s.waitObserved(b.ID, "stopped", time.Minute)
	for _, c := range s.containers(b.ID) {
		if c.Live() {
			t.Fatal("container still live after stop")
		}
	}
	// Configuration is editable once stopped and applies on the next start.
	newMem := int64(96 << 20)
	if _, err := s.bots.Update(s.ctx, s.user, b.ID, service.UpdateBotInput{MemoryBytes: &newMem}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitFor("restart with new limits", time.Minute, func() bool {
		return s.get(b.ID).ObservedState == "running" && s.get(b.ID).Generation > stopped.Generation
	})
	cid := *s.get(b.ID).ContainerID
	insp, err := s.sdk.ContainerInspect(s.ctx, cid)
	if err != nil || insp.HostConfig.Memory != newMem {
		t.Fatalf("new memory limit not applied: %v %d", err, insp.HostConfig.Memory)
	}

	// Delete removes containers, workspace and the row.
	if err := s.bots.Delete(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.containers(b.ID)) != 0 {
		t.Fatal("containers remain after delete")
	}
	if _, err := s.ws.Path(b.ID); err == nil {
		t.Fatal("workspace remains after delete")
	}
	if _, err := s.db.GetBot(context.Background(), b.ID); err == nil {
		t.Fatal("row remains after delete")
	}
}

func TestContainerIsolationSettings(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeIdle)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	ins, err := s.sdk.ContainerInspect(s.ctx, *cur.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	h := ins.HostConfig
	if h.Privileged || !h.ReadonlyRootfs || len(h.CapAdd) != 0 || len(h.Devices) != 0 || len(h.PortBindings) != 0 || h.PublishAllPorts {
		t.Fatalf("%+v", h)
	}
	if len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" {
		t.Fatalf("cap drop: %v", h.CapDrop)
	}
	found := false
	for _, o := range h.SecurityOpt {
		if o == "no-new-privileges" || o == "no-new-privileges:true" {
			found = true
		}
		if strings.Contains(o, "unconfined") {
			t.Fatalf("seccomp disabled: %s", o)
		}
	}
	if !found {
		t.Fatalf("no-new-privileges missing: %v", h.SecurityOpt)
	}
	if h.NetworkMode != container.NetworkMode("none") || h.PidMode != "" || h.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Fatalf("network/pid/restart: %+v", h)
	}
	if h.Memory != b.MemoryBytes || h.MemorySwap != b.MemoryBytes || h.NanoCPUs != b.NanoCPUs || h.PidsLimit == nil || *h.PidsLimit != b.PidsLimit {
		t.Fatalf("resources: mem=%d swap=%d cpu=%d pids=%v", h.Memory, h.MemorySwap, h.NanoCPUs, h.PidsLimit)
	}
	if h.LogConfig.Type != "json-file" || h.LogConfig.Config["max-size"] == "" {
		t.Fatalf("log config: %+v", h.LogConfig)
	}
	if len(ins.Mounts) != 1 || ins.Mounts[0].Destination != "/workspace" || ins.Mounts[0].Type != mount.TypeBind || !ins.Mounts[0].RW {
		t.Fatalf("mounts: %+v", ins.Mounts)
	}
	if ins.Config.User != s.opts.User || ins.Config.Labels[runnerLabel("rivetpanel.bot_id")] != b.ID {
		t.Fatalf("user/labels: %q %v", ins.Config.User, ins.Config.Labels)
	}
	// Images are referenced by immutable digest/ID, never by mutable tag.
	if strings.Contains(ins.Config.Image, ":24-alpine") && !strings.Contains(ins.Config.Image, "@sha256:") {
		t.Fatalf("container uses a mutable tag: %s", ins.Config.Image)
	}
}

func runnerLabel(s string) string { return s }

func TestCrashIsRestartedWithBackoffAndRecorded(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeIdle)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	first := *cur.ContainerID
	if err := s.sdk.ContainerKill(s.ctx, first, "KILL"); err != nil {
		t.Fatal(err)
	}
	s.waitFor("restarted after SIGKILL", time.Minute, func() bool {
		c := s.get(b.ID)
		return c.ObservedState == "running" && c.ContainerID != nil && *c.ContainerID != first
	})
	c := s.get(b.ID)
	if c.LastExitCode == nil || *c.LastExitCode != 137 {
		t.Fatalf("exit code not recorded: %+v", c.LastExitCode)
	}
	if c.Generation != cur.Generation {
		t.Fatal("crash restart must not change generation")
	}
	if n := len(s.containers(b.ID)); n != 1 {
		t.Fatalf("%d containers after crash restart", n)
	}
}

func TestRunnerRestartAdoptsRunningContainers(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeIdle)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	id := *cur.ContainerID

	s.stopRunner() // panel crash: containers keep running, database says "running"
	s.startRunner()
	time.Sleep(4 * time.Second)

	after := s.get(b.ID)
	if after.ObservedState != "running" || after.ContainerID == nil || *after.ContainerID != id {
		t.Fatalf("container not adopted: %+v", after)
	}
	if n := len(s.containers(b.ID)); n != 1 {
		t.Fatalf("duplicate container after runner restart: %d", n)
	}

	// Desired=stopped recorded while the runner is down is applied on restart.
	s.stopRunner()
	if _, _, err := s.db.SetDesired(s.ctx, b.ID, "stopped", false, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s.startRunner()
	s.waitObserved(b.ID, "stopped", time.Minute)
	for _, c := range s.containers(b.ID) {
		if c.Live() {
			t.Fatal("container survived stop intent recorded during runner downtime")
		}
	}
}

func TestOutOfMemoryIsEnforcedAndReported(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs", func(in *service.CreateBotInput) { in.MemoryBytes = 64 << 20 })
	s.write(b, "index.js", `const a=[]; for(;;){ a.push(Buffer.alloc(8*1024*1024, 1)); }`)
	s.bots.Start(s.ctx, s.user, b.ID)
	s.waitFor("oom kill recorded", 5*time.Minute, func() bool {
		c := s.get(b.ID)
		return c.LastError != nil && strings.Contains(*c.LastError, "out of memory") && c.LastExitCode != nil && *c.LastExitCode == 137
	})
}

func TestBuildFailureIsReportedAndBuilderRemoved(t *testing.T) {
	s := newStack(t)
	b := s.createBot("go")
	s.write(b, "go.mod", "module smoke\n\ngo 1.24\n")
	s.write(b, "main.go", "package main\nfunc main() { this is not go }\n")
	s.bots.Start(s.ctx, s.user, b.ID)
	s.waitFor("build failure", 8*time.Minute, func() bool {
		c := s.get(b.ID)
		return c.ObservedState == "failed" && c.LastError != nil && strings.HasPrefix(*c.LastError, "build failed (exit code ")
	})
	for _, c := range s.containers(b.ID) {
		t.Fatalf("container left behind after failed build: %+v", c)
	}
	// Fixing the source and restarting recovers.
	s.write(b, "main.go", "package main\nimport \"time\"\nfunc main() { time.Sleep(time.Hour) }\n")
	s.bots.Restart(s.ctx, s.user, b.ID)
	s.waitObserved(b.ID, "running", 5*time.Minute)
}
