package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

const botID = "11111111-2222-4333-8444-555555555555"

// flakyStore lets tests inject database failures.
type flakyStore struct {
	*sqlite.DB
	mu           sync.Mutex
	failStarting int // fail this many "starting" observations
}

func (s *flakyStore) Observe(ctx context.Context, o sqlite.Observation) (bool, error) {
	s.mu.Lock()
	if o.State == "starting" && s.failStarting > 0 {
		s.failStarting--
		s.mu.Unlock()
		return false, errors.New("database unavailable")
	}
	s.mu.Unlock()
	return s.DB.Observe(ctx, o)
}

type flakyWS struct {
	*filesystem.Manager
	failRemove int
}

func (w *flakyWS) Remove(id string) error {
	if w.failRemove > 0 {
		w.failRemove--
		return errors.New("disk error")
	}
	return w.Manager.Remove(id)
}

type envMap map[string]string

func (e envMap) DecryptEnv(context.Context, string) (map[string]string, error) { return e, nil }

type rig struct {
	t     *testing.T
	db    *flakyStore
	fd    *fakeDocker
	r     *Runner
	ws    *flakyWS
	now   time.Time
	dir   string
	env   envMap
	clock *time.Time
}

func newRig(t *testing.T, mods ...func(*Options)) *rig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "t.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x.io','h',1,1)`)
	m, err := filesystem.NewManager(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	m.Create(botID)
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	g := &rig{t: t, db: &flakyStore{DB: db}, fd: newFake(), ws: &flakyWS{Manager: m}, dir: dir, now: now,
		env: envMap{"DISCORD_TOKEN": "s3cret"}}
	g.clock = &g.now
	opts := Options{NodeID: domain.LocalNodeID, User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Network: "none",
		Now: func() time.Time { return *g.clock }}
	for _, m := range mods {
		m(&opts)
	}
	g.r, err = New(Deps{Store: g.db, Docker: g.fd, Env: g.env, Workspaces: g.ws, Catalog: cat,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateBot(ctx, domain.Bot{ID: botID, OwnerID: "u", NodeID: domain.LocalNodeID, Name: "b", Runtime: "nodejs",
		ImageRef: "node:24-alpine", Argv: []string{"node", "index.js"}, MemoryBytes: 256 << 20, NanoCPUs: 5e8, PidsLimit: 128,
		CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	// Per-bot restart settings default to 2s/5m/5 attempts; these tests exercise
	// the runner-level 1s base backoff and an unlimited crash budget.
	if _, err := db.ExecContext(ctx, `UPDATE bots SET restart_backoff_initial_ms = 1000, restart_max_attempts = 0 WHERE id = ?`, botID); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *rig) desire(state string, force bool) domain.Bot {
	g.t.Helper()
	b, _, err := g.db.SetDesired(context.Background(), botID, state, force, 2)
	if err != nil {
		g.t.Fatal(err)
	}
	return b
}

func (g *rig) pass() time.Duration { return g.r.reconcile(context.Background(), botID) }

func (g *rig) bot() domain.Bot {
	b, err := g.db.GetBot(context.Background(), botID)
	if err != nil {
		g.t.Fatal(err)
	}
	return b
}

func (g *rig) advance(d time.Duration) { g.now = g.now.Add(d) }

func (g *rig) wantState(state string) domain.Bot {
	g.t.Helper()
	b := g.bot()
	if b.ObservedState != state {
		g.t.Fatalf("observed_state = %s (err=%v), want %s", b.ObservedState, deref(b.LastError), state)
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestRunBacksOffWhenWatchEnds(t *testing.T) {
	g := newRig(t)
	close(g.fd.events) // every event watch ends immediately
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := g.r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	g.fd.mu.Lock()
	calls := g.fd.capsCalls
	g.fd.mu.Unlock()
	if calls > 2 {
		t.Fatalf("capability probe ran %d times after the event stream closed; reconnect loop did not back off", calls)
	}
}

func TestStartCreatesHardenedContainerAndObservesRunning(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	if d := g.pass(); d != 0 {
		t.Fatalf("delay %v", d)
	}
	b := g.wantState("running")
	if b.ObservedGeneration != b.Generation || b.ContainerID == nil || b.LastError != nil {
		t.Fatalf("%+v", b)
	}
	if len(g.fd.created) != 1 {
		t.Fatalf("created %d containers", len(g.fd.created))
	}
	s := g.fd.created[0]
	if s.Name != "rivetpanel-"+botID+"-runtime" || s.Role != RoleRuntime || s.Generation != b.Generation {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(s.Image, "@sha256:") {
		t.Fatalf("image not pinned: %s", s.Image)
	}
	if s.MemoryBytes != 256<<20 || s.NanoCPUs != 5e8 || s.PidsLimit != 128 || !s.OpenStdin || s.Network != "none" {
		t.Fatalf("limits: %+v", s)
	}
	joined := strings.Join(s.Env, "\n")
	for _, want := range []string{"DISCORD_TOKEN=s3cret", "HOME=/workspace", "NODE_ENV=production"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %s", want)
		}
	}
	if !strings.HasSuffix(s.WorkspaceHostPath, botID) || !filepath.IsAbs(s.WorkspaceHostPath) {
		t.Fatalf("workspace path %q", s.WorkspaceHostPath)
	}
	if strings.Contains(s.SpecHash, "s3cret") {
		t.Fatal("spec hash leaks env")
	}
	if len(g.fd.resolved) == 0 {
		t.Fatal("image not resolved")
	}
}

func TestDuplicateStartIsIdempotent(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.desire("running", false)
	g.desire("running", false)
	g.pass()
	g.pass()
	g.pass()
	if len(g.fd.created) != 1 || g.fd.starts != 1 {
		t.Fatalf("created=%d starts=%d", len(g.fd.created), g.fd.starts)
	}
	if b := g.wantState("running"); b.Generation != 1 {
		t.Fatalf("generation %d", b.Generation)
	}
}

func TestLostCreateResponseIsRediscovered(t *testing.T) {
	g := newRig(t)
	g.fd.loseCreateResponses = 1
	g.desire("running", false)
	if d := g.pass(); d <= 0 {
		t.Fatal("expected retry delay after create error")
	}
	b := g.wantState("failed")
	if deref(b.LastError) != "container could not be created" {
		t.Fatal(deref(b.LastError))
	}
	g.advance(1100 * time.Millisecond)
	g.pass()
	g.wantState("running")
	if len(g.fd.created) != 1 {
		t.Fatalf("duplicate container created: %d", len(g.fd.created))
	}
}

func TestCrashBetweenCreateAndPersistDoesNotDuplicate(t *testing.T) {
	g := newRig(t)
	g.db.failStarting = 1 // the write that records the new container ID fails
	g.desire("running", false)
	g.pass()
	if len(g.fd.runtimeContainers(botID)) != 1 || g.fd.starts != 0 {
		t.Fatal("container should exist unstarted")
	}
	g.pass()
	g.wantState("running")
	if len(g.fd.created) != 1 || g.fd.starts != 1 {
		t.Fatalf("created=%d starts=%d", len(g.fd.created), g.fd.starts)
	}
	if b := g.bot(); b.ContainerID == nil {
		t.Fatal("container id not recorded")
	}
}

func TestNewerIntentBeforeStartPreventsStart(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.fd.onCreate = func(ContainerSpec) { // user hits Stop while the runner is creating
		g.fd.onCreate = nil
		g.db.SetDesired(context.Background(), botID, "stopped", false, 3)
	}
	g.pass()
	if g.fd.starts != 0 {
		t.Fatal("container started despite newer stop intent")
	}
	g.pass() // next pass observes the stop intent
	b := g.wantState("stopped")
	if b.ObservedGeneration != b.Generation || b.DesiredState != "stopped" {
		t.Fatalf("%+v", b)
	}
	for _, c := range g.fd.runtimeContainers(botID) {
		if c.Live() {
			t.Fatal("live container after stop")
		}
	}
}

func TestStopThenStartReplacesContainer(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	first := *g.bot().ContainerID
	g.desire("stopped", false)
	g.pass()
	b := g.wantState("stopped")
	if b.ObservedGeneration != b.Generation || b.ContainerID == nil || *b.ContainerID != first || b.LastExitCode == nil || *b.LastExitCode != 143 {
		t.Fatalf("stop not recorded: %+v", b)
	}
	before := g.fd.removes
	g.pass() // idempotent: nothing changes
	if g.fd.removes != before {
		t.Fatal("stop pass not idempotent")
	}
	g.desire("running", false)
	g.pass()
	b = g.wantState("running")
	conts := g.fd.runtimeContainers(botID)
	if len(conts) != 1 || conts[0].ID == first || *b.ContainerID != conts[0].ID {
		t.Fatalf("old container not replaced: %+v", conts)
	}
}

func TestRestartAdvancesGenerationAndReplaces(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	first := *g.bot().ContainerID
	b := g.desire("running", true)
	if b.Generation != 2 {
		t.Fatal(b.Generation)
	}
	g.pass()
	b = g.wantState("running")
	if *b.ContainerID == first || b.ObservedGeneration != 2 || len(g.fd.runtimeContainers(botID)) != 1 {
		t.Fatalf("%+v", b)
	}
	if g.fd.runtimeContainers(botID)[0].Generation() != 2 {
		t.Fatal("generation label not updated")
	}
}

func TestConfigChangeReplacesOutdatedSpec(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	g.desire("stopped", false)
	g.pass()
	// edit memory while stopped (what the API allows), same runner instance
	g.db.ExecContext(context.Background(), `UPDATE bots SET memory_bytes = ?, generation = generation + 1 WHERE id = ?`, 128<<20, botID)
	g.desire("running", false)
	g.pass()
	g.wantState("running")
	if g.fd.created[len(g.fd.created)-1].MemoryBytes != 128<<20 {
		t.Fatal("new limits not applied")
	}
}

func TestUnexpectedExitBackoffAndRecovery(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	id := *g.bot().ContainerID

	g.fd.crash(id, 1, false)
	d := g.pass()
	b := g.wantState("failed")
	if d != time.Second || b.LastExitCode == nil || *b.LastExitCode != 1 || deref(b.LastError) != "exited with code 1" {
		t.Fatalf("d=%v %+v err=%s", d, b, deref(b.LastError))
	}
	if d = g.pass(); d <= 0 || len(g.fd.created) != 1 {
		t.Fatalf("restarted too early: d=%v created=%d", d, len(g.fd.created))
	}
	g.advance(1100 * time.Millisecond)
	g.pass()
	b = g.wantState("running")
	if len(g.fd.created) != 2 || *b.ContainerID == id {
		t.Fatal("expected replacement container")
	}

	// second crash: OOM, delay doubles
	g.fd.crash(*b.ContainerID, 137, true)
	d = g.pass()
	if d != 2*time.Second || deref(g.bot().LastError) != "killed: out of memory" {
		t.Fatalf("d=%v err=%s", d, deref(g.bot().LastError))
	}

	// a long healthy run resets the backoff
	g.advance(3 * time.Second)
	g.pass()
	b = g.wantState("running")
	g.fd.mu.Lock()
	c := g.fd.conts[*b.ContainerID]
	c.StartedAt = time.Now().Add(-10 * time.Minute)
	g.fd.mu.Unlock()
	g.fd.crash(*b.ContainerID, 2, false)
	if d = g.pass(); d != time.Second {
		t.Fatalf("backoff not reset after stable run: %v", d)
	}
}

func TestBackoffIsBounded(t *testing.T) {
	g := newRig(t)
	for i, want := range []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300} {
		if got := g.r.backoff(i + 1); got != want*time.Second {
			t.Errorf("attempt %d: %v want %v", i+1, got, want*time.Second)
		}
	}
}

func TestStartupReconciliation(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	// The database claims the bot is running but Docker has nothing (host rebooted).
	g.desire("running", false)
	g.db.Observe(ctx, sqlite.Observation{BotID: botID, Generation: 1, State: "running", SettleGeneration: true, ContainerID: "gone", NowMS: 5})
	n, _ := g.db.MarkNodeObservedUnknown(ctx, domain.LocalNodeID, 6)
	if n != 1 || g.bot().ObservedState != "unknown" {
		t.Fatal("stale observation not demoted")
	}
	g.pass()
	b := g.wantState("running")
	if len(g.fd.runtimeContainers(botID)) != 1 || *b.ContainerID == "gone" {
		t.Fatal("bot not recreated")
	}

	// Desired stopped but a container is still running (crash while stopping).
	g.desire("stopped", false)
	g.fd.mu.Lock()
	g.fd.conts[*b.ContainerID].State = "running"
	g.fd.mu.Unlock()
	g.db.Observe(ctx, sqlite.Observation{BotID: botID, Generation: 2, State: "unknown", NowMS: 7})
	g.pass()
	g.wantState("stopped")
	if g.fd.runtimeContainers(botID)[0].Live() {
		t.Fatal("container left running")
	}
}

func TestOrphanContainerSweep(t *testing.T) {
	g := newRig(t)
	other := "99999999-2222-4333-8444-555555555555"
	id, _ := g.fd.Create(context.Background(), ContainerSpec{Name: "x", Role: RoleRuntime, BotID: other, NodeID: domain.LocalNodeID})
	g.fd.Start(context.Background(), id)
	g.r.reconcile(context.Background(), other)
	if all, _ := g.fd.ListManaged(context.Background(), ""); len(all) != 0 {
		t.Fatalf("orphan not removed: %+v", all)
	}
}

func TestForeignNameConflictIsReportedNotDuplicated(t *testing.T) {
	g := newRig(t)
	g.fd.mu.Lock()
	g.fd.conts["foreign"] = &ContainerInfo{ID: "foreign", Name: "rivetpanel-" + botID + "-runtime", State: "running", Labels: map[string]string{}}
	g.fd.mu.Unlock()
	g.desire("running", false)
	if d := g.pass(); d <= 0 {
		t.Fatal("expected retry")
	}
	if deref(g.wantState("failed").LastError) != "container could not be created" {
		t.Fatal("wrong error")
	}
}

func TestBuildStage(t *testing.T) {
	g := newRig(t)
	ws, _ := g.ws.Open(botID)
	ws.Write("package.json", strings.NewReader("{}"), 100)
	ws.Close()
	g.fd.builderExit = 3
	g.desire("running", false)
	g.pass()
	b := g.wantState("failed")
	if deref(b.LastError) != "build failed (exit code 3)" || len(g.fd.runtimeContainers(botID)) != 0 {
		t.Fatalf("%s", deref(b.LastError))
	}
	// builder container is always cleaned up and never receives bot secrets
	for _, c := range g.fd.created {
		if c.Role != RoleBuilder {
			t.Fatal("runtime created after failed build")
		}
		if strings.Contains(strings.Join(c.Env, ","), "s3cret") || c.MemoryBytes < 768<<20 || c.PidsLimit < 512 {
			t.Fatalf("bad builder spec: %+v", c)
		}
	}
	if all, _ := g.fd.ListManaged(context.Background(), botID); len(all) != 0 {
		t.Fatal("builder container leaked")
	}
	g.fd.builderExit = 0
	g.advance(1100 * time.Millisecond)
	g.pass()
	g.wantState("running")
	builders := 0
	for _, c := range g.fd.created {
		if c.Role == RoleBuilder {
			builders++
		}
	}
	if builders != 2 {
		t.Fatalf("builders = %d", builders)
	}
	// crash restart within the same generation does not rebuild
	id := *g.bot().ContainerID
	g.fd.crash(id, 1, false)
	g.pass()
	g.advance(2 * time.Second)
	g.pass()
	g.wantState("running")
	builders = 0
	for _, c := range g.fd.created {
		if c.Role == RoleBuilder {
			builders++
		}
	}
	if builders != 2 {
		t.Fatalf("rebuilt on crash restart: %d", builders)
	}
	// a new generation rebuilds
	g.desire("running", true)
	g.pass()
	builders = 0
	for _, c := range g.fd.created {
		if c.Role == RoleBuilder {
			builders++
		}
	}
	if builders != 3 {
		t.Fatalf("expected rebuild on new generation, builders=%d", builders)
	}
}

func TestBuildSkippedWithoutMarkerFile(t *testing.T) {
	g := newRig(t) // nodejs builds only when package.json exists
	g.desire("running", false)
	g.pass()
	for _, c := range g.fd.created {
		if c.Role == RoleBuilder {
			t.Fatal("build ran without package.json")
		}
	}
}

func TestStaleBuilderFromCrashIsRemoved(t *testing.T) {
	g := newRig(t)
	id, _ := g.fd.Create(context.Background(), ContainerSpec{Name: ContainerName(botID, RoleBuilder), Role: RoleBuilder, BotID: botID, NodeID: domain.LocalNodeID, Generation: 1})
	g.fd.Start(context.Background(), id)
	g.desire("running", false)
	g.pass()
	g.wantState("running")
}

func TestDeleteTearsDownContainersWorkspaceThenRow(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	g.desire("running", false)
	g.pass()
	g.db.MarkBotDeleted(ctx, botID, 9)

	g.ws.failRemove = 1
	if d := g.pass(); d <= 0 {
		t.Fatal("expected retry after cleanup failure")
	}
	b, err := g.db.GetBot(ctx, botID)
	if err != nil || b.DesiredState != "deleted" || b.ObservedState != "failed" {
		t.Fatalf("row must survive failed cleanup: %v %+v", err, b)
	}
	if all, _ := g.fd.ListManaged(ctx, botID); len(all) != 0 {
		t.Fatal("containers should be gone before workspace removal")
	}
	g.pass()
	if _, err := g.db.GetBot(ctx, botID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("row not deleted after successful cleanup")
	}
	if _, err := os.Stat(filepath.Join(g.dir, "bots", botID)); !os.IsNotExist(err) {
		t.Fatal("workspace remains")
	}
}

func TestPurgeIsIdempotent(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	g.desire("running", false)
	g.pass()
	g.db.MarkBotDeleted(ctx, botID, 9)
	if err := g.r.Purge(ctx, botID); err != nil {
		t.Fatal(err)
	}
	if err := g.r.Purge(ctx, botID); err != nil {
		t.Fatal("second purge should be a no-op:", err)
	}
	if _, err := g.db.GetBot(ctx, botID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("row remains")
	}
}

func TestImageFailureIsReportedGenerically(t *testing.T) {
	g := newRig(t)
	g.fd.resolveErr = errors.New("pull access denied for registry.example/secretrepo")
	g.desire("running", false)
	if d := g.pass(); d <= 0 {
		t.Fatal("expected retry")
	}
	b := g.wantState("failed")
	if deref(b.LastError) != "runtime image is unavailable" {
		t.Fatal(deref(b.LastError))
	}
}

func TestStartFailureRetries(t *testing.T) {
	g := newRig(t)
	g.fd.startErr = errors.New("oci runtime error")
	g.desire("running", false)
	g.pass()
	if deref(g.wantState("failed").LastError) != "container failed to start" {
		t.Fatal("wrong error")
	}
	g.fd.startErr = nil
	g.advance(1100 * time.Millisecond)
	g.pass()
	g.wantState("running")
	if len(g.fd.created) != 1 {
		t.Fatal("recreated instead of reusing the created container")
	}
}

func TestCapabilitiesMustEnforceLimits(t *testing.T) {
	for name, mod := range map[string]func(*Capabilities){
		"memory": func(c *Capabilities) { c.MemoryLimit = false },
		"cpu":    func(c *Capabilities) { c.CPUQuota = false },
		"pids":   func(c *Capabilities) { c.PidsLimit = false },
	} {
		c := Capabilities{MemoryLimit: true, CPUQuota: true, PidsLimit: true}
		mod(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunLoopEndToEnd(t *testing.T) {
	g := newRig(t, func(o *Options) { o.ResyncInterval = 50 * time.Millisecond; o.Now = time.Now })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "runner ready", func() bool { return g.r.Check(ctx) == nil })

	g.desire("running", false)
	g.r.Notify(botID)
	waitFor(t, "running", func() bool { return g.bot().ObservedState == "running" })

	// Crash is picked up from the event stream, then restarted after backoff.
	first := *g.bot().ContainerID
	g.fd.crash(first, 1, false)
	g.fd.events <- Event{BotID: botID, ContainerID: first, Action: "die"}
	waitFor(t, "restart after crash", func() bool {
		b := g.bot()
		return b.ObservedState == "running" && b.ContainerID != nil && *b.ContainerID != first
	})

	g.desire("stopped", false)
	g.r.Notify(botID)
	waitFor(t, "stopped", func() bool { return g.bot().ObservedState == "stopped" })

	// Missed events are repaired by periodic resync: kill it behind the runner's back.
	g.desire("running", false)
	g.r.Notify(botID)
	waitFor(t, "running again", func() bool { return g.bot().ObservedState == "running" })
	id := *g.bot().ContainerID
	g.fd.mu.Lock()
	delete(g.fd.conts, id) // vanished without any event
	g.fd.mu.Unlock()
	waitFor(t, "resync recreates", func() bool {
		b := g.bot()
		return b.ObservedState == "running" && b.ContainerID != nil && *b.ContainerID != id
	})
}

func TestRunnerUnreadyWhenDockerDown(t *testing.T) {
	g := newRig(t)
	g.fd.capsErr = errors.New("cannot connect")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(100 * time.Millisecond)
	if err := g.r.Check(ctx); err == nil {
		t.Fatal("readiness must fail while Docker is unreachable")
	}
	g.desire("running", false)
	g.r.Notify(botID)
	time.Sleep(50 * time.Millisecond)
	if len(g.fd.created) != 0 {
		t.Fatal("work performed without Docker")
	}
}

func TestRunnerMarksObservationsUnknownOnStart(t *testing.T) {
	g := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	g.desire("stopped", false)
	g.db.Observe(context.Background(), sqlite.Observation{BotID: botID, Generation: 1, State: "running", ContainerID: "zombie", NowMS: 4})
	g.db.SetDesired(context.Background(), botID, "running", false, 5)
	g.db.Observe(context.Background(), sqlite.Observation{BotID: botID, Generation: 2, State: "running", NowMS: 5})
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	// The stale "running" row is demoted then reconciled against the empty Docker.
	waitFor(t, "recreated", func() bool {
		b := g.bot()
		return b.ObservedState == "running" && b.ContainerID != nil && *b.ContainerID != "zombie"
	})
}

func TestParseUser(t *testing.T) {
	if u, g, err := ParseUser("65532:65532"); err != nil || u != 65532 || g != 65532 {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "1", "a:b", "-1:2", "1:x"} {
		if _, _, err := ParseUser(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSetupFailuresAreGatedByBackoff(t *testing.T) {
	g := newRig(t)
	g.fd.resolveErr = errors.New("registry down")
	g.desire("running", false)
	d1 := g.pass()
	if d1 != time.Second {
		t.Fatalf("first delay %v", d1)
	}
	calls := len(g.fd.resolved)
	// Events, resyncs or repeated notifications must not retry before the gate opens.
	for i := 0; i < 5; i++ {
		if d := g.pass(); d <= 0 || d > time.Second {
			t.Fatalf("gated pass returned %v", d)
		}
	}
	if len(g.fd.resolved) != calls {
		t.Fatal("retried before the backoff elapsed")
	}
	g.advance(1100 * time.Millisecond)
	if d := g.pass(); d != 2*time.Second {
		t.Fatalf("second failure should double the delay, got %v", d)
	}
	// A new generation (e.g. restart after fixing the cause) bypasses the gate.
	g.fd.resolveErr = nil
	g.desire("running", true)
	g.pass()
	g.wantState("running")
}

// Several panels (or a test suite) can share one Docker daemon. A runner must
// never touch containers that carry another node's label.
func TestRunnerNeverTouchesOtherNodesContainers(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	other := "99999999-2222-4333-8444-555555555555"
	id, _ := g.fd.Create(ctx, ContainerSpec{Name: "theirs", Role: RoleRuntime, BotID: other, NodeID: "another-node", Generation: 1})
	g.fd.Start(ctx, id)

	// 1. Reconciling a bot id this runner has no row for must not sweep foreign containers.
	g.r.reconcile(ctx, other)
	// 2. A resync must not enqueue or remove them either.
	if err := g.r.resync(ctx); err != nil {
		t.Fatal(err)
	}
	// 3. Events about them are ignored by the watch loop.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go g.r.Run(runCtx)
	g.fd.events <- Event{BotID: other, ContainerID: id, NodeID: "another-node", Action: "die"}
	time.Sleep(300 * time.Millisecond)

	c, err := g.fd.Inspect(ctx, id)
	if err != nil || !c.Live() {
		t.Fatalf("another node's container was touched: %+v %v", c, err)
	}
	// ...while its own orphan (same node label) is still cleaned up.
	mine, _ := g.fd.Create(ctx, ContainerSpec{Name: "mine", Role: RoleRuntime, BotID: "88888888-2222-4333-8444-555555555555", NodeID: domain.LocalNodeID})
	g.fd.Start(ctx, mine)
	g.r.reconcile(ctx, "88888888-2222-4333-8444-555555555555")
	if _, err := g.fd.Inspect(ctx, mine); err == nil {
		t.Fatal("own orphan not removed")
	}
}

func (g *rig) setPolicy(policy string, maxAttempts int) {
	g.t.Helper()
	if _, err := g.db.ExecContext(context.Background(),
		`UPDATE bots SET restart_policy = ?, restart_max_attempts = ? WHERE id = ?`, policy, maxAttempts, botID); err != nil {
		g.t.Fatal(err)
	}
}

func TestRestartPolicyCleanExitIsNotRestarted(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	id := *g.bot().ContainerID
	g.fd.crash(id, 0, false)
	for i := 0; i < 3; i++ { // repeated resyncs must stay quiet
		if d := g.pass(); d != 0 {
			t.Fatalf("clean exit must not schedule a restart, d=%v", d)
		}
		g.advance(time.Minute)
	}
	b := g.wantState("stopped")
	if len(g.fd.created) != 1 || b.LastExitCode == nil || *b.LastExitCode != 0 || b.LastError != nil {
		t.Fatalf("created=%d %+v", len(g.fd.created), b)
	}
	// An explicit restart (new generation) starts it again.
	g.desire("running", true)
	g.pass()
	g.wantState("running")
	if len(g.fd.created) != 2 {
		t.Fatal("explicit restart did not create a new container")
	}
}

func TestRestartPolicyNeverStaysDownAfterCrash(t *testing.T) {
	g := newRig(t)
	g.setPolicy("never", 5)
	g.desire("running", false)
	g.pass()
	g.fd.crash(*g.bot().ContainerID, 1, false)
	if d := g.pass(); d != 0 {
		t.Fatalf("d=%v", d)
	}
	b := g.wantState("failed")
	if deref(b.LastError) != "exited with code 1" {
		t.Fatal(deref(b.LastError))
	}
	g.advance(time.Hour)
	g.pass()
	if len(g.fd.created) != 1 {
		t.Fatal("policy never restarted the bot")
	}
}

func TestRestartPolicyGivesUpAfterMaxAttempts(t *testing.T) {
	g := newRig(t)
	g.setPolicy("on_failure", 2)
	g.desire("running", false)
	g.pass()
	for i := 0; i < 2; i++ { // two crashes are restarted with growing backoff
		g.fd.crash(*g.bot().ContainerID, 1, false)
		g.pass()
		g.wantState("failed")
		g.advance(10 * time.Minute)
		g.pass()
		g.wantState("running")
	}
	if len(g.fd.created) != 3 {
		t.Fatalf("created = %d, want 3", len(g.fd.created))
	}
	g.fd.crash(*g.bot().ContainerID, 1, false) // the third consecutive crash exceeds the budget
	if d := g.pass(); d != 0 {
		t.Fatalf("d=%v", d)
	}
	b := g.wantState("failed")
	if !strings.Contains(deref(b.LastError), "gave up after 2 restarts") {
		t.Fatal(deref(b.LastError))
	}
	g.advance(time.Hour)
	g.pass()
	if len(g.fd.created) != 3 {
		t.Fatal("kept restarting after giving up")
	}
}

func TestCustomBackoffAndEntrypointAndNoNetwork(t *testing.T) {
	g := newRig(t, func(o *Options) { o.Network = "bridge" })
	g.db.ExecContext(context.Background(), `UPDATE bots SET restart_backoff_initial_ms = 5000, network_enabled = 0,
		entrypoint_json = '["node","--inspect=0"]', argv_json = '["index.js"]' WHERE id = ?`, botID)
	g.db.ReplaceBotPorts(context.Background(), botID, []domain.BotPort{{ContainerPort: 8080, HostPort: 20080, Protocol: "tcp", HostIP: "127.0.0.1", CreatedAtMS: 1}})
	g.desire("running", false)
	g.pass()
	spec := g.fd.created[0]
	if spec.Network != "none" || len(spec.Ports) != 0 {
		t.Fatalf("network disabled must use network none and drop port bindings: %+v", spec)
	}
	if strings.Join(spec.Entrypoint, " ") != "node --inspect=0" || strings.Join(spec.Argv, " ") != "index.js" {
		t.Fatalf("entrypoint/argv: %v %v", spec.Entrypoint, spec.Argv)
	}
	g.fd.crash(*g.bot().ContainerID, 1, false)
	if d := g.pass(); d != 5*time.Second {
		t.Fatalf("configured initial backoff not used: %v", d)
	}
}

func TestPortsPublishedWhenNetworkEnabled(t *testing.T) {
	g := newRig(t, func(o *Options) { o.Network = "bridge" })
	g.db.ReplaceBotPorts(context.Background(), botID, []domain.BotPort{{ContainerPort: 8080, HostPort: 20080, Protocol: "tcp", HostIP: "127.0.0.1", CreatedAtMS: 1}})
	g.desire("running", false)
	g.pass()
	p := g.fd.created[0].Ports
	if len(p) != 1 || p[0].HostPort != 20080 || p[0].HostIP != "127.0.0.1" || g.fd.created[0].Network != "bridge" {
		t.Fatalf("%+v", g.fd.created[0])
	}
}

func TestKillIsImmediateAndStaysDown(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	id := *g.bot().ContainerID
	g.desire("stopped", false) // callers record stopped intent before killing
	if err := g.r.Kill(context.Background(), botID); err != nil {
		t.Fatal(err)
	}
	if c, _ := g.fd.Inspect(context.Background(), id); c.Live() || c.ExitCode != 137 {
		t.Fatalf("container not SIGKILLed: %+v", c)
	}
	g.pass()
	g.wantState("stopped")
	if len(g.fd.created) != 1 {
		t.Fatal("a killed bot with stopped intent must not be recreated")
	}
}

func TestBuildFailureIncludesBuilderOutputTail(t *testing.T) {
	g := newRig(t)
	ws, _ := g.ws.Open(botID)
	ws.Write("package.json", strings.NewReader("{}"), 100) // makes the nodejs recipe run its build
	ws.Close()
	g.fd.builderExit = 101
	g.fd.tail = "\x1b[1;31merror[E0432]\x1b[0m: unresolved import `foo`\n --> src/main.rs:1:5\x00\n"
	g.desire("running", false)
	g.pass()
	got := deref(g.wantState("failed").LastError)
	if !strings.HasPrefix(got, "build failed (exit code 101): ") || !strings.Contains(got, "error[E0432]: unresolved import `foo`") {
		t.Fatalf("last_error = %q", got)
	}
	if strings.ContainsAny(got, "\x1b\x00") {
		t.Fatalf("control characters leaked into last_error: %q", got)
	}
	g.fd.tail = strings.Repeat("x", 5000)
	if s := tidyBuildOutput(g.fd.tail); len([]rune(s)) > 705 {
		t.Fatalf("excerpt not bounded: %d", len(s))
	}
	// The reason at the end of a very long line survives.
	long := "error: could not compile `serenity`\nCaused by: rustc " + strings.Repeat("--extern a=b ", 400) + "(signal: 9, SIGKILL: kill)"
	if s := tidyBuildOutput(long); !strings.Contains(s, "(signal: 9, SIGKILL: kill)") || !strings.Contains(s, "could not compile `serenity`") {
		t.Fatalf("excerpt lost the reason: %q", s)
	}
}

func TestBuilderMemoryFloorFromRuntimeRecipe(t *testing.T) {
	g := newRig(t)
	bot := g.bot()
	rt, _ := g.r.cat.Get("nodejs")
	base := g.r.builderSpec(bot, rt, "img", "/h").MemoryBytes
	if base != maxInt64(bot.MemoryBytes, g.r.opts.BuildMemory) {
		t.Fatalf("default builder memory = %d", base)
	}
	rt.BuildMemoryBytes = 3 << 30
	if got := g.r.builderSpec(bot, rt, "img", "/h").MemoryBytes; got != 3<<30 {
		t.Fatalf("recipe floor not applied: %d", got)
	}
	// The runtime container never gets the build allowance.
	if got := g.r.runtimeSpec(bot, rt, "img", "/h", nil).MemoryBytes; got != bot.MemoryBytes {
		t.Fatalf("runtime memory = %d, want the bot's limit %d", got, bot.MemoryBytes)
	}
	cat, _ := g.r.cat.Get("rust")
	if cat.BuildMemoryBytes < 3<<30 {
		t.Fatalf("rust recipe must reserve build memory: %d", cat.BuildMemoryBytes)
	}
}

func TestLifecycleDetailIsPersisted(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	g.fd.crash(*g.bot().ContainerID, 1, false)
	g.pass()
	b := g.wantState("failed")
	if deref(b.StateReason) != domain.ReasonCrashBackoff || b.NextRetryAtMS == nil || b.RestartCount != 1 {
		t.Fatalf("backoff detail: reason=%q next=%v count=%d", deref(b.StateReason), b.NextRetryAtMS, b.RestartCount)
	}
	g.advance(time.Minute)
	g.pass()
	b = g.wantState("running")
	if b.StateReason != nil || b.NextRetryAtMS != nil {
		t.Fatalf("running must clear the reason: %q %v", deref(b.StateReason), b.NextRetryAtMS)
	}
	g.fd.crash(*g.bot().ContainerID, 0, false) // clean exit
	g.pass()
	if b := g.wantState("stopped"); deref(b.StateReason) != domain.ReasonCleanExit {
		t.Fatalf("clean exit reason = %q", deref(b.StateReason))
	}
	g.desire("stopped", false)
	g.pass()
	if b := g.wantState("stopped"); b.StateReason != nil || b.RestartCount != 0 {
		t.Fatalf("stop must clear detail: %q %d", deref(b.StateReason), b.RestartCount)
	}
}

func TestExplicitStartAfterGivingUpGetsAFreshBudget(t *testing.T) {
	g := newRig(t)
	g.setPolicy("on_failure", 1)
	g.desire("running", false)
	g.pass()
	g.fd.crash(*g.bot().ContainerID, 1, false)
	g.pass()
	g.advance(10 * time.Minute)
	g.pass()
	g.fd.crash(*g.bot().ContainerID, 1, false)
	g.pass()
	if b := g.wantState("failed"); deref(b.StateReason) != domain.ReasonGaveUp {
		t.Fatalf("reason = %q", deref(b.StateReason))
	}
	g.desire("running", true) // the user presses Start again
	g.pass()
	g.wantState("running")
	g.fd.crash(*g.bot().ContainerID, 1, false)
	g.pass()
	if b := g.wantState("failed"); deref(b.StateReason) != domain.ReasonCrashBackoff {
		t.Fatalf("first crash after an explicit retry must be restarted, reason = %q (%s)", deref(b.StateReason), deref(b.LastError))
	}
}

func TestCrashCountSurvivesPanelRestart(t *testing.T) {
	g := newRig(t)
	g.setPolicy("on_failure", 2)
	g.desire("running", false)
	g.pass()
	for i := 0; i < 2; i++ {
		g.fd.crash(*g.bot().ContainerID, 1, false)
		g.pass()
		g.advance(10 * time.Minute)
		g.pass()
	}
	// A new runner (panel restart) must continue counting, not start over.
	r2, err := New(Deps{Store: g.db, Docker: g.fd, Env: g.env, Workspaces: g.ws, Catalog: g.r.cat,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, g.r.opts)
	if err != nil {
		t.Fatal(err)
	}
	g.r = r2
	g.fd.crash(*g.bot().ContainerID, 1, false)
	g.pass()
	if b := g.wantState("failed"); deref(b.StateReason) != domain.ReasonGaveUp {
		t.Fatalf("reason after restart = %q count=%d", deref(b.StateReason), b.RestartCount)
	}
}

func TestKillRecordsReason(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	g.desire("stopped", false)
	if err := g.r.Kill(context.Background(), botID); err != nil {
		t.Fatal(err)
	}
	g.pass()
	if b := g.wantState("stopped"); deref(b.StateReason) != domain.ReasonKilled {
		t.Fatalf("reason = %q", deref(b.StateReason))
	}
}

func TestLastStartedIsSetOnTransitionOnly(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	first := g.bot().LastStartedAtMS
	if first == nil {
		t.Fatal("last_started_at_ms not set")
	}
	g.advance(time.Minute)
	g.pass() // settled running: no change
	if got := g.bot().LastStartedAtMS; got == nil || *got != *first {
		t.Fatalf("changed without a transition: %v -> %v", *first, got)
	}
}

func TestAdoptionAfterRestartKeepsStartTime(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	g.desire("running", false)
	g.pass()
	id := *g.bot().ContainerID
	started := g.now.Add(-30 * time.Minute).Truncate(time.Millisecond)
	g.fd.mu.Lock()
	g.fd.conts[id].StartedAt = started
	g.fd.listOmitsLiveStart = true
	g.fd.mu.Unlock()

	// The panel restarts: live observations are demoted, then the still
	// running container is adopted.
	g.advance(time.Hour)
	if n, _ := g.db.MarkNodeObservedUnknown(ctx, domain.LocalNodeID, g.now.UnixMilli()); n != 1 {
		t.Fatal("observation not demoted")
	}
	g.pass()
	b := g.wantState("running")
	if *b.ContainerID != id || len(g.fd.created) != 1 {
		t.Fatal("container was replaced instead of adopted")
	}
	if b.LastStartedAtMS == nil || *b.LastStartedAtMS != started.UnixMilli() {
		t.Fatalf("last_started_at_ms = %v, want %d", b.LastStartedAtMS, started.UnixMilli())
	}
}

type recBuilds struct {
	mu      sync.Mutex
	started int
	stages  []string
	final   []string
	out     strings.Builder
}

func (r *recBuilds) BuildStarted(ctx context.Context, botID string, gen int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started++
	return fmt.Sprintf("op-%d", r.started)
}
func (r *recBuilds) BuildStage(ctx context.Context, id, stage string) {
	r.mu.Lock()
	r.stages = append(r.stages, stage)
	r.mu.Unlock()
}
func (r *recBuilds) BuildOutput(id string) io.WriteCloser { return nopWC{&r.out, &r.mu} }
func (r *recBuilds) BuildFinished(ctx context.Context, id, status, code, msg string) {
	r.mu.Lock()
	r.final = append(r.final, status+":"+code)
	r.mu.Unlock()
}

type nopWC struct {
	b  *strings.Builder
	mu *sync.Mutex
}

func (w nopWC) Write(p []byte) (int, error) { w.mu.Lock(); defer w.mu.Unlock(); return w.b.Write(p) }
func (w nopWC) Close() error                { return nil }

func TestBuildsAreRecordedWithOutput(t *testing.T) {
	g := newRig(t)
	rec := &recBuilds{}
	g.r.builds = rec
	w, _ := g.ws.Open(botID)
	w.Write("package.json", strings.NewReader("{}"), 10)
	w.Close()
	g.fd.tail = "npm ERR! missing script\n"
	g.fd.builderExit = 1
	g.desire("running", false)
	g.pass()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.started != 1 || len(rec.final) != 1 || rec.final[0] != "failed:build_failed" {
		t.Fatalf("recorded: started=%d final=%v", rec.started, rec.final)
	}
	if !strings.Contains(rec.out.String(), "npm ERR!") {
		t.Fatalf("output not captured: %q", rec.out.String())
	}
	if strings.Join(rec.stages, ",") != "Preparing the build image,Building" {
		t.Fatalf("stages = %v", rec.stages)
	}
}

// Two installations on one daemon share the local node id. With installation
// labels, neither sweeps the other's containers, including containers created
// before labels existed; containers of its own bots are still adopted.
func TestInstallationLabelsScopeContainers(t *testing.T) {
	g := newRig(t, func(o *Options) { o.InstallID = "11111111-1111-4111-8111-111111111111" })
	ctx := context.Background()
	theirs := "99999999-2222-4333-8444-555555555555"
	legacy := "77777777-2222-4333-8444-555555555555"
	mine := "88888888-2222-4333-8444-555555555555"
	a, _ := g.fd.Create(ctx, ContainerSpec{Name: "theirs", Role: RoleRuntime, BotID: theirs, NodeID: domain.LocalNodeID, InstallID: "22222222-2222-4222-8222-222222222222"})
	b, _ := g.fd.Create(ctx, ContainerSpec{Name: "legacy", Role: RoleRuntime, BotID: legacy, NodeID: domain.LocalNodeID})
	c, _ := g.fd.Create(ctx, ContainerSpec{Name: "mine", Role: RoleRuntime, BotID: mine, NodeID: domain.LocalNodeID, InstallID: g.r.opts.InstallID})
	for _, id := range []string{a, b, c} {
		g.fd.Start(ctx, id)
	}
	if err := g.r.resync(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{theirs, legacy, mine} {
		g.r.reconcile(ctx, id)
	}
	if _, err := g.fd.Inspect(ctx, a); err != nil {
		t.Fatal("another installation's container was removed")
	}
	if _, err := g.fd.Inspect(ctx, b); err != nil {
		t.Fatal("an unlabelled container of an unknown bot was removed")
	}
	if _, err := g.fd.Inspect(ctx, c); err == nil {
		t.Fatal("own orphan not removed")
	}
	// New containers carry the label.
	g.desire("running", false)
	g.pass()
	if got := g.fd.runtimeContainers(botID)[0].Labels[LabelInstall]; got != g.r.opts.InstallID {
		t.Fatalf("install label = %q", got)
	}
}

// With every build slot taken, a build waits (without creating a builder
// container) until its context ends, then proceeds once a slot frees up.
func TestBuildWaitsForAFreeSlot(t *testing.T) {
	g := newRig(t, func(o *Options) { o.MaxBuilds = 1 })
	ws, _ := g.ws.Open(botID)
	ws.Write("package.json", strings.NewReader("{}"), 100)
	ws.Close()
	g.desire("running", false)
	g.r.buildSlots <- struct{}{} // another build holds the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	g.r.reconcile(ctx, botID)
	cancel()
	if len(g.fd.created) != 0 {
		t.Fatalf("a builder was created while no slot was free: %+v", g.fd.created)
	}
	<-g.r.buildSlots
	g.pass()
	g.wantState("running")
	if len(g.r.buildSlots) != 0 {
		t.Fatal("build slot not released")
	}
}

// restart_count is "crashes in a row". It must clear once the server has run
// for StableAfter, start over on an explicit restart, and never include setup
// (image, install, create, start) retries.
func TestCrashStreakClearsAfterStableRunAndExcludesSetupRetries(t *testing.T) {
	g := newRig(t)
	g.desire("running", false)
	g.pass()
	for i := 0; i < 2; i++ {
		g.fd.crash(*g.bot().ContainerID, 1, false)
		g.pass()
		g.advance(10 * time.Second)
		g.pass()
	}
	b := g.wantState("running")
	if b.RestartCount != 2 {
		t.Fatalf("restart_count after two crashes = %d, want 2", b.RestartCount)
	}
	// Still within StableAfter: the streak is kept, and the runner asks to look again.
	if d := g.pass(); d <= 0 || d > time.Minute {
		t.Fatalf("expected a re-check before the run is stable, got %v", d)
	}
	if b = g.bot(); b.RestartCount != 2 {
		t.Fatalf("restart_count cleared too early: %d", b.RestartCount)
	}
	g.advance(time.Minute)
	if d := g.pass(); d != 0 {
		t.Fatalf("stable run should not be re-checked, got %v", d)
	}
	if b = g.wantState("running"); b.RestartCount != 0 {
		t.Fatalf("restart_count after a stable run = %d, want 0", b.RestartCount)
	}

	// A crash, then a setup failure while restarting: only the crash counts.
	g.fd.crash(*b.ContainerID, 1, false)
	g.pass()
	g.fd.startErr = errors.New("oci runtime error")
	g.advance(10 * time.Second)
	g.pass()
	g.advance(10 * time.Second)
	g.pass()
	g.fd.startErr = nil
	g.advance(10 * time.Second)
	g.pass()
	if b = g.wantState("running"); b.RestartCount != 1 {
		t.Fatalf("restart_count with setup retries = %d, want 1", b.RestartCount)
	}
	g.fd.crash(*b.ContainerID, 1, false)
	g.pass()
	if b = g.bot(); b.RestartCount != 2 {
		t.Fatalf("second crash counted as %d, want 2 (setup retries must not count)", b.RestartCount)
	}

	// An explicit restart starts a fresh streak as soon as the new run is up.
	g.desire("running", true)
	g.pass()
	if b = g.wantState("running"); b.RestartCount != 0 {
		t.Fatalf("restart_count after an explicit restart = %d, want 0", b.RestartCount)
	}
}

// A host port that another container (another panel, any tool) publishes is
// a configuration problem: the start is refused (the holder's name stays out
// of the tenant-visible error; it can be another tenant's container), it is
// not retried or counted as a crash, and Start again tries once more.
func TestPortConflictIsNotRetriedOrCountedAsCrash(t *testing.T) {
	g := newRig(t, func(o *Options) { o.Network = "bridge" })
	ctx := context.Background()
	g.db.ReplaceBotPorts(ctx, botID, []domain.BotPort{{ContainerPort: 8080, HostPort: 20080, Protocol: "tcp", HostIP: "127.0.0.1", CreatedAtMS: 1}})
	g.fd.published = []PublishedPort{{HostIP: "0.0.0.0", HostPort: 20080, Proto: "tcp", ContainerID: "abc", Container: "other-panel-runtime"}}
	g.desire("running", false)
	if d := g.pass(); d != 0 {
		t.Fatalf("a port conflict must not schedule a retry, got %v", d)
	}
	b := g.wantState("failed")
	if deref(b.StateReason) != domain.ReasonPortConflict || !strings.Contains(deref(b.LastError), "Port 20080 is already used by another container") ||
		strings.Contains(deref(b.LastError), "other-panel-runtime") ||
		b.RestartCount != 0 || b.NextRetryAtMS != nil || g.fd.starts != 0 {
		t.Fatalf("reason=%q err=%q count=%d retry=%v starts=%d", deref(b.StateReason), deref(b.LastError), b.RestartCount, b.NextRetryAtMS, g.fd.starts)
	}
	// Resyncs and events do nothing until the user acts.
	for i := 0; i < 3; i++ {
		g.advance(time.Minute)
		if d := g.pass(); d != 0 || g.fd.starts != 0 {
			t.Fatalf("retried on its own: d=%v starts=%d", d, g.fd.starts)
		}
	}
	// The other container goes away; Start again starts it.
	g.fd.published = nil
	g.desire("running", true)
	g.pass()
	if b = g.wantState("running"); b.RestartCount != 0 || b.StateReason != nil {
		t.Fatalf("after Start again: count=%d reason=%q", b.RestartCount, deref(b.StateReason))
	}
	// Its own published port never counts as a conflict.
	if msg, _ := g.r.portConflict(ctx, g.bot()); msg != "" {
		t.Fatalf("own port reported as conflict: %s", msg)
	}
}

// Docker's own "port is already allocated" start error (a port the listing
// could not see) is classified the same way.
func TestPortInUseStartErrorIsAConfigurationProblem(t *testing.T) {
	g := newRig(t, func(o *Options) { o.Network = "bridge" })
	g.db.ReplaceBotPorts(context.Background(), botID, []domain.BotPort{{ContainerPort: 8080, HostPort: 20081, Protocol: "tcp", HostIP: "127.0.0.1", CreatedAtMS: 1}})
	g.fd.startErr = errors.New("Error response from daemon: driver failed programming external connectivity on endpoint x: Bind for 127.0.0.1:20081 failed: port is already allocated")
	g.desire("running", false)
	if d := g.pass(); d != 0 {
		t.Fatalf("retry scheduled: %v", d)
	}
	b := g.wantState("failed")
	if deref(b.StateReason) != domain.ReasonPortConflict || !strings.Contains(deref(b.LastError), "Port 20081 is already in use on this host") || b.RestartCount != 0 {
		t.Fatalf("reason=%q err=%q count=%d", deref(b.StateReason), deref(b.LastError), b.RestartCount)
	}
	g.advance(time.Hour)
	g.pass()
	if g.wantState("failed"); len(g.fd.created) != 1 {
		t.Fatalf("recreated on its own: %d", len(g.fd.created))
	}
}

func TestIsPortInUse(t *testing.T) {
	for msg, want := range map[string]int{
		"Bind for 0.0.0.0:25565 failed: port is already allocated":                           25565,
		"listen tcp4 0.0.0.0:25577: bind: address already in use":                            25577,
		"Error starting userland proxy: listen udp [::]:19132: bind: address already in use": 19132,
		"port is already allocated":                                                          0,
	} {
		if p, ok := IsPortInUse(errors.New(msg)); !ok || p != want {
			t.Errorf("%q: %d %v, want %d", msg, p, ok, want)
		}
	}
	if _, ok := IsPortInUse(errors.New("oci runtime error")); ok {
		t.Error("unrelated error classified as a port conflict")
	}
	pubs := []PublishedPort{{HostIP: "127.0.0.1", HostPort: 1, Proto: "tcp", Container: "a"}, {HostIP: "", HostPort: 2, Proto: "udp", Container: "b", BotID: "me"}}
	if _, busy := PortHolder(pubs, "0.0.0.0", 1, "", ""); !busy {
		t.Error("wildcard request must overlap a specific address")
	}
	if _, busy := PortHolder(pubs, "10.0.0.1", 1, "", ""); busy {
		t.Error("different specific addresses do not overlap")
	}
	if _, busy := PortHolder(pubs, "0.0.0.0", 1, "udp", ""); busy {
		t.Error("tcp and udp do not overlap")
	}
	if _, busy := PortHolder(pubs, "0.0.0.0", 2, "", "me"); busy {
		t.Error("the bot's own container must be ignored")
	}
}
