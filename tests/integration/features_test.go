//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
)

func tunePolicy(t *testing.T, s *stack, id, policy string, maxAttempts int) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `UPDATE bots SET restart_policy = ?, restart_max_attempts = ?,
		restart_backoff_initial_ms = 100, restart_backoff_max_ms = 200 WHERE id = ?`, policy, maxAttempts, id); err != nil {
		t.Fatal(err)
	}
}

func TestKillIsSigkillAndStaysDown(t *testing.T) {
	s := newStack(t)
	s.bots.Killer = s.rn
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeIdle)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	id := *cur.ContainerID

	start := time.Now()
	if _, err := s.bots.Kill(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	s.waitObserved(b.ID, "stopped", 30*time.Second)
	ins, err := s.sdk.ContainerInspect(s.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// SIGKILL means exit 137 with no graceful period (StopTimeout in this stack is 3s).
	if ins.State.Running || ins.State.ExitCode != 137 {
		t.Fatalf("state: %+v", ins.State)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Logf("kill+observe took %v", d)
	}
	time.Sleep(5 * time.Second) // several resync passes: it must not come back
	if got := s.get(b.ID); got.ObservedState != "stopped" || got.DesiredState != domain.DesiredStopped {
		t.Fatalf("%+v", got)
	}
	for _, c := range s.containers(b.ID) {
		if c.Live() {
			t.Fatal("a killed bot was restarted")
		}
	}
}

func TestRestartPolicyRealContainers(t *testing.T) {
	// Clean exit: never restarted.
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", "console.log('done'); setTimeout(()=>process.exit(0), 800);")
	tunePolicy(t, s, b.ID, "on_failure", 0)
	s.bots.Start(s.ctx, s.user, b.ID)
	s.waitObserved(b.ID, "stopped", 5*time.Minute)
	time.Sleep(4 * time.Second)
	got := s.get(b.ID)
	if got.ObservedState != "stopped" || got.LastExitCode == nil || *got.LastExitCode != 0 || got.LastError != nil {
		t.Fatalf("clean exit: %+v %s", got, deref(got.LastError))
	}
	if n := len(s.containers(b.ID)); n != 1 {
		t.Fatalf("containers after a clean exit: %d", n)
	}
	// Start again is an explicit retry (new generation, new container).
	gen := got.Generation
	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	if s.get(b.ID).Generation != gen+1 {
		t.Fatal("Start after a terminal state must retry")
	}
	s.waitObserved(b.ID, "stopped", time.Minute)

	// Crash loop: retried with backoff, then the runner gives up.
	c := s.createBot("nodejs")
	s.write(c, "index.js", "console.error('boom'); process.exit(3);")
	tunePolicy(t, s, c.ID, "on_failure", 2)
	s.bots.Start(s.ctx, s.user, c.ID)
	s.waitFor("gave up", 2*time.Minute, func() bool {
		x := s.get(c.ID)
		return x.ObservedState == "failed" && strings.Contains(deref(x.LastError), "gave up after 2 restarts")
	})
	x := s.get(c.ID)
	if x.LastExitCode == nil || *x.LastExitCode != 3 {
		t.Fatalf("exit code: %+v", x.LastExitCode)
	}
	time.Sleep(3 * time.Second)
	for _, ct := range s.containers(c.ID) {
		if ct.Live() {
			t.Fatal("kept restarting after giving up")
		}
	}

	// Policy never: one run, no restart.
	n := s.createBot("nodejs")
	s.write(n, "index.js", "process.exit(1);")
	tunePolicy(t, s, n.ID, "never", 5)
	s.bots.Start(s.ctx, s.user, n.ID)
	s.waitObserved(n.ID, "failed", 5*time.Minute)
	time.Sleep(3 * time.Second)
	if len(s.containers(n.ID)) != 1 || s.containers(n.ID)[0].Live() {
		t.Fatal("policy never restarted the bot")
	}
}

const nodeHTTP = `require('http').createServer((q,s)=>s.end('hello from the bot')).listen(8080, '0.0.0.0', () => console.log('listening'));`

func TestPortsEntrypointAndNoNetworkReal(t *testing.T) {
	s := newStack(t, func(o *runner.Options) { o.Network = "bridge" })
	b := s.createBot("nodejs")
	s.write(b, "index.js", nodeHTTP)
	const hostPort = 29876
	if _, err := s.bots.SetPorts(s.ctx, s.user, b.ID, []service.PortInput{{ContainerPort: 8080, HostPort: hostPort, Protocol: "tcp"}}); err != nil {
		t.Fatal(err)
	}
	// Custom entrypoint: node is the entrypoint, index.js its argument.
	entry := []string{"node", "--no-warnings"}
	argv := []string{"index.js"}
	if _, err := s.bots.Update(s.ctx, s.user, b.ID, service.UpdateBotInput{Entrypoint: &entry, Argv: &argv}); err != nil {
		t.Fatal(err)
	}
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	s.waitLog(*cur.ContainerID, "listening", time.Minute)

	ins, err := s.sdk.ContainerInspect(s.ctx, *cur.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ins.Config.Entrypoint, " ") != "node --no-warnings" || strings.Join(ins.Config.Cmd, " ") != "index.js" {
		t.Fatalf("entrypoint/cmd: %v %v", ins.Config.Entrypoint, ins.Config.Cmd)
	}
	pb := ins.HostConfig.PortBindings["8080/tcp"]
	if len(pb) != 1 || pb[0].HostIP != "127.0.0.1" || pb[0].HostPort != fmt.Sprint(hostPort) {
		t.Fatalf("port bindings: %+v", ins.HostConfig.PortBindings)
	}
	var body string
	s.waitFor("published port answers", 30*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", hostPort))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		return resp.StatusCode == 200
	})
	if body != "hello from the bot" {
		t.Fatalf("body = %q", body)
	}
	// The bind is loopback only: the same port is not reachable on other addresses.
	s.bots.Stop(s.ctx, s.user, b.ID)
	s.waitObserved(b.ID, "stopped", time.Minute)

	// Outbound access off: the container gets no network at all.
	if _, err := s.bots.SetPorts(s.ctx, s.user, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := s.bots.Update(s.ctx, s.user, b.ID, service.UpdateBotInput{NetworkEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	s.write(b, "index.js", `require('dns').lookup('example.com', (err) => { console.log(err ? 'NET_BLOCKED' : 'NET_OPEN'); setInterval(()=>{}, 1e6); });`)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur = s.waitObserved(b.ID, "running", 2*time.Minute)
	ins, _ = s.sdk.ContainerInspect(s.ctx, *cur.ContainerID)
	if string(ins.HostConfig.NetworkMode) != "none" {
		t.Fatalf("network mode = %q", ins.HostConfig.NetworkMode)
	}
	got := s.waitLog(*cur.ContainerID, "NET_", time.Minute)
	if !strings.Contains(got, "NET_BLOCKED") {
		t.Fatalf("outbound network was reachable: %s", got)
	}
}

func TestStatsStreamReal(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", `let x = 0; setInterval(() => { for (let i = 0; i < 2e6; i++) x += Math.sqrt(i); }, 50); console.log('busy');`)
	s.bots.Start(s.ctx, s.user, b.ID)
	cur := s.waitObserved(b.ID, "running", 5*time.Minute)
	s.waitLog(*cur.ContainerID, "busy", time.Minute)

	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	var samples []domain.ResourceSample
	err := s.dk.StreamStats(ctx, *cur.ContainerID, func(m domain.ResourceSample) bool {
		samples = append(samples, m)
		return len(samples) < 4
	})
	if err != nil || len(samples) < 4 {
		t.Fatalf("samples=%d err=%v", len(samples), err)
	}
	last := samples[len(samples)-1]
	if last.MemUsedBytes <= 0 || last.MemLimitBytes != cur.MemoryBytes || last.PIDs < 1 {
		t.Fatalf("%+v (bot memory limit %d)", last, cur.MemoryBytes)
	}
	if last.CPUCores <= 0 || last.CPUCores > 1.5 { // limited to 0.5 CPU by the bot's cgroup
		t.Fatalf("cpu cores = %v", last.CPUCores)
	}
	if samples[0].CPUCores != 0 {
		t.Logf("first frame cpu = %v (expected 0: no previous sample)", samples[0].CPUCores)
	}
}
