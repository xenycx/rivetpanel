package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// Store is the persistence surface the runner uses.
type Store interface {
	GetBot(ctx context.Context, id string) (domain.Bot, error)
	ListBotsByNode(ctx context.Context, nodeID string) ([]domain.Bot, error)
	Observe(ctx context.Context, o sqlite.Observation) (bool, error)
	MarkNodeObservedUnknown(ctx context.Context, nodeID string, nowMS int64) (int64, error)
	DeleteBotRow(ctx context.Context, id string) error
	TouchNode(ctx context.Context, nodeID string, nowMS int64) error
}

// EnvSource returns a bot's decrypted environment.
type EnvSource interface {
	DecryptEnv(ctx context.Context, botID string) (map[string]string, error)
}

// Workspaces gives the runner contained access to bot directories.
type Workspaces interface {
	Path(botID string) (string, error)
	Prepare(botID string, uid, gid int) error
	Exists(botID, rel string) (bool, error)
	Remove(botID string) error
}

// Options tune runner behavior; zero values get defaults from New.
type Options struct {
	NodeID string
	// InstallID identifies this installation. Containers labelled with another
	// installation are never listed, adopted or removed. Containers without the
	// label (created before it existed) are adopted only when their bot exists
	// in this database, and are never swept as orphans.
	InstallID string
	User      string // "uid:gid" of the container user (as seen inside the container)
	// WorkspaceOwner is the "uid:gid" that must own workspace files ON THE HOST
	// so User can write them. It equals User unless the daemon remaps IDs
	// (rootless Docker, userns-remap). Defaults to User.
	WorkspaceOwner string
	Network        string
	Workers        int
	// MaxBuilds bounds concurrent build containers (their memory is the
	// largest transient cost); 0 = no extra bound beyond Workers.
	MaxBuilds      int
	ResyncInterval time.Duration
	StopTimeout    time.Duration
	BuildTimeout   time.Duration
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	StableAfter    time.Duration // a run this long resets the crash backoff

	TmpfsBytes      int64
	BuildTmpfsBytes int64
	BuildMemory     int64
	BuildNanoCPUs   int64
	BuildPids       int64

	// AddonData holds add-on data directories; empty disables add-ons.
	AddonData addons.DataRoot

	Now func() time.Time
}

func (o *Options) defaults() {
	set := func(p *time.Duration, v time.Duration) {
		if *p == 0 {
			*p = v
		}
	}
	set(&o.ResyncInterval, 30*time.Second)
	set(&o.StopTimeout, 10*time.Second)
	set(&o.BuildTimeout, 10*time.Minute)
	set(&o.BaseBackoff, time.Second)
	set(&o.MaxBackoff, 5*time.Minute)
	set(&o.StableAfter, time.Minute)
	if o.Workers == 0 {
		o.Workers = 2
	}
	if o.User == "" {
		o.User = "65532:65532"
	}
	if o.Network == "" {
		o.Network = "bridge"
	}
	for p, v := range map[*int64]int64{&o.TmpfsBytes: 64 << 20, &o.BuildTmpfsBytes: 512 << 20, &o.BuildMemory: 768 << 20,
		&o.BuildNanoCPUs: 1_000_000_000, &o.BuildPids: 512} {
		if *p == 0 {
			*p = v
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// ParseUser splits "uid:gid".
func ParseUser(u string) (uid, gid int, err error) {
	a, b, ok := strings.Cut(u, ":")
	if !ok {
		return 0, 0, fmt.Errorf("container user %q must be uid:gid", u)
	}
	if uid, err = strconv.Atoi(a); err != nil {
		return 0, 0, err
	}
	if gid, err = strconv.Atoi(b); err != nil {
		return 0, 0, err
	}
	if uid < 0 || gid < 0 {
		return 0, 0, errors.New("negative uid/gid")
	}
	return uid, gid, nil
}

type botState struct {
	attempts    int       // consecutive crashes of the current generation (restart_count)
	failures    int       // consecutive setup failures (image, install, build, create, start); never counted as crashes
	nextAt      time.Time // earliest time to retry after a failure
	retryAt     time.Time // gate for setup failures (image, build, create, start)
	failGen     int64     // generation the setup failure applies to
	exitSeenID  string    // container whose exit was already counted
	finalExit   string    // container whose exit ends the run (no restart)
	finalMsg    string    // failure text for finalExit; empty for a clean exit
	finalReason string    // domain.Reason* for finalExit
	builtGen    int64     // generation whose build stage completed in this process
	built       bool
	countGen    int64  // generation the crash counter belongs to
	seeded      bool   // counter restored from the database after a panel restart
	prepFailGen int64  // generation whose pre-build failure was recorded
	prepFailMsg string // and its message, so identical retries are not re-recorded
}

// Runner is the in-process implementation of the runner interface.
type Runner struct {
	buildSlots chan struct{}
	store      Store
	docker     Docker
	env        EnvSource
	ws         Workspaces
	cat        *runtimes.Catalog
	bus        *events.Bus
	builds     BuildRecorder
	log        *slog.Logger
	opts       Options
	uid        int
	gid        int

	mu      sync.Mutex
	queue   chan string
	pending map[string]bool
	locks   map[string]*sync.Mutex
	cancels map[string]context.CancelFunc
	states  map[string]*botState
	timers  map[string]*time.Timer
	images  map[string]string // resolved immutable image references
	// blueprints caches parsed blueprint revisions ("id@rev").
	blueprints map[string]blueprint.Spec
	prov       *blueprint.Providers
	ready      error // nil when Docker is reachable and capable
	readySet   bool
	caps       Capabilities
	started    bool
	runCtx     context.Context
}

// Deps groups the runner's collaborators.
type Deps struct {
	Store      Store
	Docker     Docker
	Env        EnvSource
	Workspaces Workspaces
	Catalog    *runtimes.Catalog
	Log        *slog.Logger
	Bus        *events.Bus   // optional: publishes status changes for live views
	Builds     BuildRecorder // optional: durable build history and output
	// Providers download game server software (nil = the public providers).
	Providers *blueprint.Providers
}

// New builds a Runner. Call Run to start it.
func New(d Deps, o Options) (*Runner, error) {
	o.defaults()
	if _, _, err := ParseUser(o.User); err != nil {
		return nil, err
	}
	if o.WorkspaceOwner == "" {
		o.WorkspaceOwner = o.User
	}
	uid, gid, err := ParseUser(o.WorkspaceOwner)
	if err != nil {
		return nil, fmt.Errorf("workspace owner: %w", err)
	}
	if o.NodeID == "" {
		return nil, errors.New("runner node id required")
	}
	return &Runner{
		store: d.Store, docker: d.Docker, env: d.Env, ws: d.Workspaces, cat: d.Catalog, log: d.Log, bus: d.Bus, builds: d.Builds, opts: o,
		prov: d.Providers,
		uid:  uid, gid: gid,
		queue: make(chan string, 4096), pending: map[string]bool{}, locks: map[string]*sync.Mutex{},
		cancels: map[string]context.CancelFunc{}, states: map[string]*botState{}, timers: map[string]*time.Timer{},
		ready:      errors.New("docker connection not yet established"),
		buildSlots: slots(o.MaxBuilds),
	}, nil
}

func (r *Runner) now() time.Time { return r.opts.Now() }

// Check reports whether Docker is reachable and can enforce resource limits.
// It backs the readiness endpoint; it never claims health it has not observed.
func (r *Runner) Check(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

func (r *Runner) setReady(err error, caps Capabilities) {
	r.mu.Lock()
	r.ready, r.caps, r.readySet = err, caps, true
	r.mu.Unlock()
}

// enqueue schedules a reconcile without interrupting in-flight work.
func (r *Runner) enqueue(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending[id] {
		return
	}
	select {
	case r.queue <- id:
		r.pending[id] = true
	default:
		// Queue full: the periodic resync will pick this bot up again.
	}
}

// Notify is called when the API changes a bot's intent. It cancels any
// in-flight reconcile for that bot (e.g. a long build) so new intent applies
// promptly, then schedules a fresh pass.
func (r *Runner) Notify(botID string) {
	r.mu.Lock()
	if c := r.cancels[botID]; c != nil {
		c()
	}
	r.mu.Unlock()
	r.enqueue(botID)
}

func (r *Runner) lockFor(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		r.locks[id] = l
	}
	return l
}

func (r *Runner) stateFor(id string) *botState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[id]
	if s == nil {
		s = &botState{}
		r.states[id] = s
	}
	return s
}

func (r *Runner) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, id)
	delete(r.locks, id)
	if t := r.timers[id]; t != nil {
		t.Stop()
		delete(r.timers, id)
	}
}

// after schedules a future reconcile, replacing any earlier timer for the bot.
func (r *Runner) after(id string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.timers[id]; t != nil {
		t.Stop()
	}
	r.timers[id] = time.AfterFunc(d, func() { r.enqueue(id) })
}

func (r *Runner) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := r.opts.BaseBackoff
	for i := 1; i < attempts && d < r.opts.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, r.opts.MaxBackoff)
}

// Run drives the runner until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("runner already started")
	}
	r.started, r.runCtx = true, ctx
	r.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < r.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.worker(ctx)
		}()
	}
	defer func() {
		wg.Wait()
		r.mu.Lock()
		for _, t := range r.timers {
			t.Stop()
		}
		r.mu.Unlock()
	}()

	wait := 2 * time.Second
	for ctx.Err() == nil {
		caps, err := r.docker.Capabilities(ctx)
		if err == nil {
			err = caps.Validate()
		}
		if err != nil {
			r.setReady(err, caps)
			r.log.Warn("docker unavailable", "err", err, "retry_in", wait)
			if !sleep(ctx, wait) {
				return nil
			}
			wait = min(wait*2, 30*time.Second)
			continue
		}
		wait = 2 * time.Second
		if !caps.SwapLimit {
			r.log.Warn("docker host cannot enforce swap limits; memory caps may be exceeded via swap if the host has swap")
		}
		r.setReady(nil, caps)
		// After process start or loss of contact, live observations are stale.
		if n, err := r.store.MarkNodeObservedUnknown(ctx, r.opts.NodeID, r.now().UnixMilli()); err != nil {
			r.log.Error("mark observations unknown", "err", err)
		} else if n > 0 {
			r.log.Info("demoted stale observations pending reconciliation", "bots", n)
		}
		err = r.watch(ctx)
		if ctx.Err() != nil {
			return nil
		}
		r.setReady(fmt.Errorf("docker connection lost: %w", err), caps)
		r.log.Warn("docker connection lost", "err", err, "retry_in", wait)
		// A remote runner can still reach its local Docker daemon while its
		// control-plane store is offline. In that case Capabilities succeeds but
		// watch/resync fails immediately; without a delay this loop hammers the
		// unavailable panel and emits thousands of log lines per second.
		if !sleep(ctx, wait) {
			return nil
		}
		wait = min(wait*2, 30*time.Second)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// watch consumes events and resyncs periodically until the Docker connection fails.
func (r *Runner) watch(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := r.docker.Events(wctx)
	if err := r.resync(wctx); err != nil {
		return err
	}
	tick := time.NewTicker(r.opts.ResyncInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return errors.New("event stream closed")
			}
			// Builder containers are short-lived and awaited directly; reacting to
			// their events would just retrigger failed builds.
			// Several panels (or tests) may share one daemon: only ever react to
			// containers labelled with THIS runner's node.
			// Diagnostic containers (the AI assistant's offline checks) are owned by
			// the diagnostic runner; reconciling them would kill them mid-run.
			if ev.BotID != "" && ev.Role != RoleBuilder && ev.Role != RoleDiagnostic && ev.NodeID == r.opts.NodeID && r.sameInstall(ev.InstallID) {
				r.enqueue(ev.BotID)
			}
		case err := <-errs:
			if err == nil {
				err = errors.New("event stream ended")
			}
			return err
		case <-tick.C:
			if _, err := r.docker.Capabilities(wctx); err != nil {
				return err
			}
			if err := r.resync(wctx); err != nil {
				return err
			}
		}
	}
}

// resync enqueues every bot whose state might need attention and removes
// managed containers that belong to no known bot.
func (r *Runner) resync(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	bots, err := r.store.ListBotsByNode(rctx, r.opts.NodeID)
	if err != nil {
		return fmt.Errorf("list bots: %w", err)
	}
	conts, err := r.listMine(rctx, "")
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	_ = r.store.TouchNode(rctx, r.opts.NodeID, r.now().UnixMilli())
	r.sweepDiagnostics(rctx)

	has := map[string]bool{}
	for _, c := range conts {
		has[c.Labels[LabelBot]] = true
	}
	known := map[string]bool{}
	for _, b := range bots {
		known[b.ID] = true
		idle := b.DesiredState == domain.DesiredStopped && b.ObservedState == "stopped" &&
			b.ObservedGeneration == b.Generation && !has[b.ID]
		if !idle {
			r.enqueue(b.ID)
		}
	}
	for _, c := range conts {
		id := c.Labels[LabelBot]
		// Only containers provably created by THIS installation are swept: an
		// unlabelled container of an unknown bot may belong to another panel.
		if id != "" && !known[id] && c.Labels[LabelNode] == r.opts.NodeID && r.opts.InstallID != "" && c.Labels[LabelInstall] == r.opts.InstallID {
			r.log.Warn("removing orphan container", "bot", id, "container", c.ID)
			r.enqueue(id) // reconcile handles NotFound by sweeping containers
		}
	}
	return nil
}

// listMine lists managed containers that belong to THIS runner's node. Every
// container lookup goes through it so the runner can never stop, replace or
// remove another node's (or another panel's) containers on a shared daemon.
func (r *Runner) listMine(ctx context.Context, botID string) ([]ContainerInfo, error) {
	all, err := r.docker.ListManaged(ctx, botID)
	if err != nil {
		return nil, err
	}
	mine := all[:0]
	for _, c := range all {
		// A diagnostic container carries its bot's id but is not part of the
		// bot's runtime: the reconciler would stop it as a stale duplicate.
		if c.Role() == RoleDiagnostic {
			continue
		}
		if c.Labels[LabelNode] == r.opts.NodeID && r.sameInstall(c.Labels[LabelInstall]) {
			mine = append(mine, c)
		}
	}
	return mine, nil
}

// diagnosticGrace is how long a diagnostic container may outlive its run
// before a resync treats it as an orphan (the panel restarted mid-run).
const diagnosticGrace = 30 * time.Minute

// sweepDiagnostics removes diagnostic containers that no run owns any more.
func (r *Runner) sweepDiagnostics(ctx context.Context) {
	all, err := r.docker.ListManaged(ctx, "")
	if err != nil {
		return
	}
	for _, c := range all {
		if c.Role() != RoleDiagnostic || c.Labels[LabelNode] != r.opts.NodeID || r.opts.InstallID == "" || c.Labels[LabelInstall] != r.opts.InstallID {
			continue
		}
		since := c.FinishedAt
		if c.Live() {
			if c.StartedAt.IsZero() {
				// the list does not carry start times for live containers
				if info, err := r.docker.Inspect(ctx, c.ID); err == nil {
					c = info
				}
			}
			since = c.StartedAt
		}
		if since.IsZero() || r.now().Sub(since) < diagnosticGrace {
			continue
		}
		r.log.Warn("removing orphan diagnostic container", "container", c.ID)
		if err := r.removeContainer(ctx, c); err != nil {
			r.log.Warn("could not remove orphan diagnostic container", "container", c.ID, "err", err)
		}
	}
}

// sameInstall reports whether a container's installation label is compatible
// with this runner: its own label, or no label at all (legacy containers).
func (r *Runner) sameInstall(label string) bool {
	return label == "" || r.opts.InstallID == "" || label == r.opts.InstallID
}

func (r *Runner) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.queue:
			r.mu.Lock()
			delete(r.pending, id)
			r.mu.Unlock()
			r.runOne(ctx, id)
		}
	}
}

func (r *Runner) runOne(parent context.Context, id string) {
	if r.Check(parent) != nil {
		return // Docker unavailable; resync runs again once it returns
	}
	l := r.lockFor(id)
	l.Lock()
	defer l.Unlock()

	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.cancels[id] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancels, id)
		r.mu.Unlock()
		cancel()
	}()

	delay := r.reconcile(ctx, id)
	switch {
	case parent.Err() != nil:
	case ctx.Err() != nil:
		r.enqueue(id) // interrupted by newer intent
	case delay > 0:
		r.after(id, delay)
	}
}

// Purge tears a bot down completely: containers, workspace, then its row. It is
// idempotent and safe to retry after a partial failure.
func (r *Runner) Purge(ctx context.Context, botID string) error {
	r.Notify(botID) // interrupt any in-flight reconcile
	l := r.lockFor(botID)
	l.Lock()
	defer l.Unlock()
	if err := r.teardown(ctx, botID); err != nil {
		return err
	}
	r.forget(botID)
	return nil
}

// Kill immediately SIGKILLs every live container of a bot without a graceful
// stop period. Callers record stopped intent first so the reconciler does not
// bring the bot back.
func (r *Runner) Kill(ctx context.Context, botID string) error {
	conts, err := r.listMine(ctx, botID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, c := range conts {
		if !c.Live() {
			continue
		}
		if err := r.docker.Kill(ctx, c.ID); err != nil && !errors.Is(err, ErrNoContainer) && firstErr == nil {
			firstErr = err
		}
	}
	// Remember why the bot stopped so it is not mistaken for a crash. The
	// write is generation-conditioned like every other observation.
	if bot, err := r.store.GetBot(ctx, botID); err == nil && bot.DesiredState == domain.DesiredStopped {
		r.observe(ctx, bot, sqlite.Observation{State: "stopping", Reason: domain.ReasonKilled})
	}
	r.Notify(botID)
	return firstErr
}

// Status is a point-in-time view of the runner for diagnostics.
type Status struct {
	Ready        error
	Capabilities Capabilities
	Queued       int // bots waiting for a worker
	Tracked      int // bots with in-memory state (recently active)
	Workers      int
	User         string
}

// Status reports readiness, host capabilities and queue depth.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{Ready: r.ready, Capabilities: r.caps, Queued: len(r.queue), Tracked: len(r.states), Workers: r.opts.Workers, User: r.opts.User}
}

func slots(n int) chan struct{} {
	if n <= 0 {
		return nil
	}
	return make(chan struct{}, n)
}
