package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// immediate requests a near-instant follow-up pass.
const immediate = time.Millisecond

// reconcile makes Docker match one bot's persisted intent and returns when the
// bot should be looked at again (0 = only on the next event or resync). Every
// database write is conditioned on the generation read here, so a stale pass
// cannot overwrite newer intent.
func (r *Runner) reconcile(ctx context.Context, id string) time.Duration {
	bot, err := r.store.GetBot(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		r.sweep(ctx, id)
		r.forget(id)
		return 0
	}
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("load bot", "bot", id, "err", err)
		}
		return r.backoff(1)
	}
	if bot.NodeID != r.opts.NodeID {
		return 0
	}
	switch bot.DesiredState {
	case domain.DesiredDeleted:
		if err := r.teardown(ctx, id); err != nil {
			if ctx.Err() == nil {
				r.log.Error("delete bot", "bot", id, "err", err)
				r.observe(ctx, bot, sqlite.Observation{State: "failed", SettleGeneration: true, LastError: "cleanup failed; will retry",
					Reason: domain.ReasonCleanupFailed})
			}
			return r.backoff(r.stateFor(id).bump())
		}
		r.forget(id)
		return 0
	case domain.DesiredStopped:
		return r.doStop(ctx, bot)
	case domain.DesiredRunning:
		return r.doRun(ctx, bot)
	}
	return 0
}

func (s *botState) bump() int { s.attempts++; return s.attempts }

func (r *Runner) observe(ctx context.Context, bot domain.Bot, o sqlite.Observation) bool {
	o.BotID, o.Generation, o.NowMS = bot.ID, bot.Generation, r.now().UnixMilli()
	applied, err := r.store.Observe(ctx, o)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("persist observation", "bot", bot.ID, "err", err)
		}
		return false
	}
	if applied {
		obsGen := bot.ObservedGeneration
		if o.SettleGeneration {
			obsGen = bot.Generation
		}
		r.bus.Publish(events.Status{BotID: bot.ID, DesiredState: bot.DesiredState, ObservedState: o.State,
			Generation: bot.Generation, ObservedGeneration: obsGen, ExitCode: o.ExitCode, LastError: o.LastError})
	}
	return applied
}

// intentChanged re-reads intent immediately before a side effect.
func (r *Runner) intentChanged(ctx context.Context, bot domain.Bot) bool {
	cur, err := r.store.GetBot(ctx, bot.ID)
	return err != nil || cur.Generation != bot.Generation || cur.DesiredState != bot.DesiredState
}

// fail records a generic, secret-free error and schedules a bounded retry.
func (r *Runner) fail(ctx context.Context, bot domain.Bot, msg string, cause error, st *botState) time.Duration {
	if ctx.Err() != nil {
		return immediate
	}
	r.log.Warn("bot reconcile failed", "bot", bot.ID, "reason", msg, "err", cause)
	d := r.backoff(st.bump())
	st.nextAt = r.now().Add(d)
	st.retryAt, st.failGen = st.nextAt, bot.Generation
	reason := domain.ReasonSetupFailed
	if strings.HasPrefix(msg, "build ") {
		reason = domain.ReasonBuildFailed
	}
	r.observe(ctx, bot, sqlite.Observation{State: "failed", SettleGeneration: true, LastError: msg,
		Reason: reason, NextRetryAtMS: st.nextAt.UnixMilli()})
	return d
}

func (r *Runner) removeContainer(ctx context.Context, c ContainerInfo) error {
	if c.Live() {
		if err := r.docker.Stop(ctx, c.ID, r.opts.StopTimeout); err != nil && !errors.Is(err, ErrNoContainer) {
			return err
		}
	}
	if err := r.docker.Remove(ctx, c.ID); err != nil && !errors.Is(err, ErrNoContainer) {
		return err
	}
	return nil
}

// sweep removes every managed container for a bot.
func (r *Runner) sweep(ctx context.Context, id string) {
	conts, err := r.listMine(ctx, id)
	if err != nil {
		return
	}
	for _, c := range conts {
		// The bot is not in this database: only a container provably created by
		// this installation is ours to remove. An unlabelled one may be another
		// panel's (both created before installation labels existed).
		if r.opts.InstallID != "" && c.Labels[LabelInstall] != r.opts.InstallID {
			continue
		}
		if err := r.removeContainer(ctx, c); err != nil {
			r.log.Warn("remove orphan container", "bot", id, "container", c.ID, "err", err)
		}
	}
	if len(conts) > 0 {
		if nw, err := r.networker(); err == nil {
			_ = nw.RemoveNetwork(ctx, AddonNetwork(id))
		}
	}
}

// teardown removes containers, then the workspace, and only then the row, so a
// failure at any step leaves the row (marked deleted) for a retry.
func (r *Runner) teardown(ctx context.Context, id string) error {
	conts, err := r.listMine(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range conts {
		if err := r.removeContainer(ctx, c); err != nil {
			return fmt.Errorf("remove container: %w", err)
		}
	}
	if err := r.removeAddonResources(ctx, id); err != nil {
		return err
	}
	if err := r.ws.Remove(id); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	return r.store.DeleteBotRow(ctx, id)
}

func (r *Runner) doStop(ctx context.Context, bot domain.Bot) time.Duration {
	st := r.stateFor(bot.ID)
	conts, err := r.listMine(ctx, bot.ID)
	if err != nil {
		r.log.Error("list containers", "bot", bot.ID, "err", err)
		return r.backoff(1)
	}
	var live bool
	for _, c := range conts {
		live = live || c.Live()
	}
	if live && bot.ObservedState != "stopping" {
		if !r.observe(ctx, bot, sqlite.Observation{State: "stopping", LastError: ""}) {
			return immediate
		}
	}
	var last *ContainerInfo
	for _, c := range conts {
		if c.Role() == RoleAddon {
			if c.Live() {
				if err := r.docker.Stop(ctx, c.ID, r.opts.StopTimeout); err != nil && !errors.Is(err, ErrNoContainer) {
					return r.fail(ctx, bot, "an add-on did not stop", err, st)
				}
			}
			continue
		}
		if c.Role() == RoleBuilder {
			if err := r.removeContainer(ctx, c); err != nil {
				return r.fail(ctx, bot, "could not remove build container", err, st)
			}
			continue
		}
		if c.Live() {
			if bot.IsGame() && c.State == "running" {
				r.gracefulStop(ctx, bot, c)
			}
			if err := r.docker.Stop(ctx, c.ID, r.opts.StopTimeout); err != nil && !errors.Is(err, ErrNoContainer) {
				return r.fail(ctx, bot, "container did not stop", err, st)
			}
			if info, err := r.docker.Inspect(ctx, c.ID); err == nil {
				c = info
			}
		}
		if last == nil || c.Generation() >= last.Generation() {
			cc := c
			last = &cc
		}
	}
	settled := bot.ObservedState == "stopped" && bot.ObservedGeneration == bot.Generation &&
		(last == nil) == (bot.ContainerID == nil) && bot.LastError == nil && bot.NextRetryAtMS == nil &&
		bot.RestartCount == 0 && (bot.StateReason == nil || *bot.StateReason == domain.ReasonKilled)
	if !settled {
		zero := int64(0)
		o := sqlite.Observation{State: "stopped", SettleGeneration: true, RestartCount: &zero}
		if bot.StateReason != nil && *bot.StateReason == domain.ReasonKilled {
			o.Reason = domain.ReasonKilled
		}
		if last != nil {
			ec := int64(last.ExitCode)
			o.ContainerID, o.ExitCode = last.ID, &ec
		} else {
			o.ClearContainer = true
		}
		if !r.observe(ctx, bot, o) {
			return immediate
		}
	}
	st.attempts, st.nextAt = 0, time.Time{}
	return 0
}

func (r *Runner) doRun(ctx context.Context, bot domain.Bot) time.Duration {
	st := r.stateFor(bot.ID)
	rt, ok, err := r.runtimeFor(ctx, bot)
	if err != nil {
		return r.fail(ctx, bot, "blueprint is unavailable", err, st)
	}
	if !ok {
		if bot.ObservedState != "failed" {
			r.observe(ctx, bot, sqlite.Observation{State: "failed", SettleGeneration: true, LastError: "runtime is not available in the catalog",
				Reason: domain.ReasonRuntimeMissing})
		}
		return 0
	}
	// Setup failures (image, build, create, start) retry on a bounded backoff,
	// regardless of how many events or resyncs arrive in between. A new
	// generation (edit, restart) bypasses the gate.
	if st.failGen == bot.Generation && r.now().Before(st.retryAt) {
		return st.retryAt.Sub(r.now())
	}
	host, err := r.ws.Path(bot.ID)
	if err != nil {
		return r.fail(ctx, bot, "workspace is unavailable", err, st)
	}
	conts, err := r.listMine(ctx, bot.ID)
	if err != nil {
		r.log.Error("list containers", "bot", bot.ID, "err", err)
		return r.backoff(st.attempts + 1)
	}

	want := r.wantHash(bot, rt, host)
	var run *ContainerInfo
	var addonConts []ContainerInfo
	for _, c := range conts {
		switch {
		case c.Role() == RoleAddon:
			addonConts = append(addonConts, c)
		case c.Role() == RoleBuilder:
			// The reconciler is serialized per bot, so a builder found here is a
			// leftover from an interrupted pass.
			if err := r.removeContainer(ctx, c); err != nil {
				return r.fail(ctx, bot, "could not remove build container", err, st)
			}
		case run == nil && c.Generation() == bot.Generation && c.Labels[LabelSpec] == want:
			cc := c
			run = &cc
		default: // outdated specification or duplicate: replace deliberately
			if err := r.removeContainer(ctx, c); err != nil {
				return r.fail(ctx, bot, "could not replace outdated container", err, st)
			}
		}
	}

	if run != nil {
		switch run.State {
		case "running":
			if bot.ObservedState != "running" || bot.ObservedGeneration != bot.Generation ||
				bot.ContainerID == nil || *bot.ContainerID != run.ID || bot.LastError != nil || bot.StateReason != nil || bot.NextRetryAtMS != nil {
				o := sqlite.Observation{State: "running", SettleGeneration: true, ContainerID: run.ID}
				if bot.ObservedState != "running" {
					// Adopting a container that was already running (e.g. after a
					// panel restart): keep its real start time, not now.
					o.StartedAtMS = r.containerStartMS(ctx, *run)
				}
				if !r.observe(ctx, bot, o) {
					return immediate
				}
			}
			r.reviveAddons(ctx, bot, addonConts)
			return 0
		case "created":
			if len(bot.Addons) == 0 {
				return r.startContainer(ctx, bot, run.ID, st)
			}
			// It may have missed joining the add-on network (an interrupted
			// pass): create it again together with its add-ons.
			if err := r.removeContainer(ctx, *run); err != nil {
				return r.fail(ctx, bot, "could not replace container", err, st)
			}
		case "exited", "dead":
			if d, done := r.handleExit(ctx, bot, *run, st); done {
				return d
			}
		default: // paused, restarting, removing: not a state we manage
			if err := r.removeContainer(ctx, *run); err != nil {
				return r.fail(ctx, bot, "could not replace container", err, st)
			}
		}
	}
	return r.create(ctx, bot, rt, host, st, addonConts)
}

// containerStartMS is when a live container started, or 0 when unknown.
// Listings omit the start time of live containers, so it is inspected then.
func (r *Runner) containerStartMS(ctx context.Context, c ContainerInfo) int64 {
	started := c.StartedAt
	if started.IsZero() {
		info, err := r.docker.Inspect(ctx, c.ID)
		if err != nil {
			return 0
		}
		started = info.StartedAt
	}
	if started.IsZero() || started.After(r.now()) {
		return 0
	}
	return started.UnixMilli()
}

// crashBackoff is the delay before restart attempt number `attempts`, using the
// bot's configured initial/maximum delays (runner defaults when unset).
func (r *Runner) crashBackoff(bot domain.Bot, attempts int) time.Duration {
	base, max := r.opts.BaseBackoff, r.opts.MaxBackoff
	if bot.RestartBackoffInitialMS > 0 {
		base = time.Duration(bot.RestartBackoffInitialMS) * time.Millisecond
	}
	if bot.RestartBackoffMaxMS > 0 {
		max = time.Duration(bot.RestartBackoffMaxMS) * time.Millisecond
	}
	if max < base {
		max = base
	}
	if attempts < 1 {
		attempts = 1
	}
	d := base
	for i := 1; i < attempts && d < max; i++ {
		d *= 2
	}
	return min(d, max)
}

// restartVerdict decides what to do after an exit. A clean exit (code 0) is
// never restarted; a crash is restarted under the on_failure policy until the
// consecutive-crash budget is used up.
func restartVerdict(bot domain.Bot, c ContainerInfo, attempts int) (restart bool, why string) {
	switch {
	case c.ExitCode == 0 && !c.OOMKilled:
		return false, ""
	case bot.RestartPolicy == domain.RestartNever:
		return false, describeExit(c)
	case bot.RestartMaxAttempts > 0 && int64(attempts) > bot.RestartMaxAttempts:
		return false, fmt.Sprintf("%s; gave up after %d restarts", describeExit(c), bot.RestartMaxAttempts)
	}
	return true, ""
}

// handleExit applies the restart policy and crash backoff. done=false means the
// dead container was removed and the caller should create a fresh one.
func (r *Runner) handleExit(ctx context.Context, bot domain.Bot, c ContainerInfo, st *botState) (time.Duration, bool) {
	now := r.now()
	ec := int64(c.ExitCode)
	if st.countGen != bot.Generation {
		// A new generation (explicit Start/Restart, config change, deploy) gets
		// a fresh crash budget; after a panel restart the persisted count of the
		// same generation is restored instead.
		st.countGen, st.attempts = bot.Generation, 0
		if !st.seeded && bot.ObservedGeneration == bot.Generation {
			st.attempts = int(bot.RestartCount)
		}
		st.seeded = true
	}
	if st.exitSeenID != c.ID {
		st.exitSeenID = c.ID
		if !c.StartedAt.IsZero() && c.FinishedAt.Sub(c.StartedAt) >= r.opts.StableAfter {
			st.attempts = 0
		}
		st.attempts++
		st.nextAt = now.Add(r.crashBackoff(bot, st.attempts))
		st.finalExit = ""
		if restart, why := restartVerdict(bot, c, st.attempts); !restart {
			st.finalExit, st.finalMsg = c.ID, why
			st.finalReason = domain.ReasonCleanExit
			switch {
			case why == "":
			case bot.RestartPolicy == domain.RestartNever:
				st.finalReason = domain.ReasonExitedNoRetry
			default:
				st.finalReason = domain.ReasonGaveUp
			}
		}
	}
	count := int64(st.attempts)
	if st.finalExit == c.ID {
		// Terminal: keep the exited container for its logs and exit code and
		// wait for an explicit Start/Restart (a new generation).
		o := sqlite.Observation{State: "stopped", SettleGeneration: true, ContainerID: c.ID, ExitCode: &ec,
			Reason: st.finalReason, RestartCount: &count}
		if st.finalMsg != "" {
			o.State, o.LastError = "failed", st.finalMsg
		}
		settled := bot.ObservedState == o.State && bot.ObservedGeneration == bot.Generation &&
			bot.ContainerID != nil && *bot.ContainerID == c.ID && (o.LastError == "") == (bot.LastError == nil) &&
			bot.StateReason != nil && *bot.StateReason == o.Reason && bot.NextRetryAtMS == nil
		if !settled {
			r.observe(ctx, bot, o)
		}
		return 0, true
	}
	if now.Before(st.nextAt) {
		r.observe(ctx, bot, sqlite.Observation{State: "failed", SettleGeneration: true, ContainerID: c.ID, ExitCode: &ec, LastError: describeExit(c),
			Reason: domain.ReasonCrashBackoff, NextRetryAtMS: st.nextAt.UnixMilli(), RestartCount: &count})
		return st.nextAt.Sub(now), true
	}
	if err := r.removeContainer(ctx, c); err != nil {
		return r.fail(ctx, bot, "could not remove exited container", err, st), true
	}
	return 0, false
}

func (r *Runner) resolve(ctx context.Context, ref string) (string, error) {
	r.mu.Lock()
	if v, ok := r.images[ref]; ok {
		r.mu.Unlock()
		return v, nil
	}
	r.mu.Unlock()
	v, err := r.docker.ResolveImage(ctx, ref)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.images == nil {
		r.images = map[string]string{}
	}
	r.images[ref] = v
	r.mu.Unlock()
	return v, nil
}

func (r *Runner) create(ctx context.Context, bot domain.Bot, rt runtimes.Runtime, host string, st *botState, addonConts []ContainerInfo) time.Duration {
	if !r.observe(ctx, bot, sqlite.Observation{State: "building"}) {
		return immediate
	}
	build := r.needsBuild(bot, rt, st)
	// Problems before a build can start (image, ownership) are recorded once
	// per generation and message, not once per retry.
	fail := func(msg string, err error) time.Duration {
		if build && ctx.Err() == nil && (st.prepFailGen != bot.Generation || st.prepFailMsg != msg) {
			st.prepFailGen, st.prepFailMsg = bot.Generation, msg
			r.buildEnd(ctx, r.buildBegin(ctx, bot), msg, err)
		}
		return r.fail(ctx, bot, msg, err, st)
	}
	var image string
	var err error
	// A game server that is about to install may still change its image
	// (automatic Java selection): resolve it after the installation.
	if !(bot.IsGame() && build) {
		if image, err = r.resolve(ctx, rt.ImageRef()); err != nil {
			return fail("runtime image is unavailable", err)
		}
	}
	if err := r.ws.Prepare(bot.ID, r.uid, r.gid); err != nil {
		return fail("workspace ownership could not be prepared", err)
	}
	st.prepFailMsg = ""
	if build {
		op := r.buildBegin(ctx, bot)
		if r.buildSlots != nil {
			select {
			case r.buildSlots <- struct{}{}:
			default:
				// Every build slot is busy: say so, then wait (bounded by ctx).
				r.buildStageName(ctx, op, "Waiting for a free build slot")
				select {
				case r.buildSlots <- struct{}{}:
				case <-ctx.Done():
					r.buildEnd(ctx, op, "", ctx.Err())
					return immediate
				}
			}
		}
		var msg string
		var err error
		if bot.IsGame() {
			r.buildStageName(ctx, op, "Installing")
			rt, msg, err = r.installGame(ctx, bot, rt, host, st, op)
		} else {
			r.buildStageName(ctx, op, "Preparing the build image")
			msg, err = r.buildStage(ctx, bot, rt, host, st, op, nil)
		}
		if r.buildSlots != nil {
			<-r.buildSlots
		}
		r.buildEnd(ctx, op, msg, err)
		if err != nil {
			if bot.IsGame() {
				if gs, gerr := r.gameStore(); gerr == nil {
					_ = gs.SetInstallState(context.WithoutCancel(ctx), bot.ID, domain.InstallFailed, nil, r.now().UnixMilli())
				}
			}
			return r.fail(ctx, bot, msg, err, st)
		}
		if bot.IsGame() {
			// The install may have chosen the image automatically.
			if fresh, err := r.store.GetBot(ctx, bot.ID); err == nil && fresh.Generation == bot.Generation {
				bot = fresh
			}
			if image, err = r.resolve(ctx, rt.ImageRef()); err != nil {
				return r.fail(ctx, bot, "server image is unavailable", err, st)
			}
		}
	}
	if r.intentChanged(ctx, bot) {
		return immediate
	}
	envMap, err := r.env.DecryptEnv(ctx, bot.ID)
	if err != nil {
		return r.fail(ctx, bot, "environment could not be decrypted", err, st)
	}
	if len(bot.Addons) > 0 || len(addonConts) > 0 {
		if msg, err := r.ensureAddons(ctx, bot, addonConts, envMap); err != nil {
			if ctx.Err() != nil {
				return immediate
			}
			return r.fail(ctx, bot, msg, err, st)
		}
		if r.intentChanged(ctx, bot) {
			return immediate
		}
	}
	if bot.IsGame() {
		if err := r.applyGameConfig(ctx, bot, envMap); err != nil {
			return r.fail(ctx, bot, "server configuration could not be written", err, st)
		}
		if err := r.ws.Prepare(bot.ID, r.uid, r.gid); err != nil {
			return r.fail(ctx, bot, "workspace ownership could not be prepared", err, st)
		}
	}
	spec := r.runtimeSpec(bot, rt, image, host, envMap)
	id, err := r.docker.Create(ctx, spec)
	if err != nil {
		// A lost response may still have created the container; the next pass
		// rediscovers it by label instead of creating a duplicate.
		return r.fail(ctx, bot, "container could not be created", err, st)
	}
	if len(spec.Networks) > 0 {
		nw, err := r.networker()
		for _, n := range spec.Networks {
			if err == nil {
				err = nw.ConnectNetwork(ctx, n, id)
			}
		}
		if err != nil {
			_ = r.docker.Remove(ctx, id)
			return r.fail(ctx, bot, "the bot could not join its add-on network", err, st)
		}
	}
	// Persist the ID before starting so a crash cannot orphan a running container.
	if !r.observe(ctx, bot, sqlite.Observation{State: "starting", ContainerID: id}) {
		return immediate
	}
	return r.startContainer(ctx, bot, id, st)
}

// needsBuild reports whether the build stage will run for this generation.
func (r *Runner) needsBuild(bot domain.Bot, rt runtimes.Runtime, st *botState) bool {
	if bot.IsGame() {
		return bot.InstallState != domain.InstallInstalled
	}
	if st.built && st.builtGen == bot.Generation {
		return false
	}
	if bot.BuildCommand != "" {
		return true // a custom command always runs, whatever files exist
	}
	if len(rt.BuildArgv) == 0 {
		return false
	}
	if len(rt.BuildIfFiles) == 0 {
		return true
	}
	for _, f := range rt.BuildIfFiles {
		if ok, err := r.ws.Exists(bot.ID, f); err == nil && ok {
			return true
		}
	}
	return false
}

func (r *Runner) buildBegin(ctx context.Context, bot domain.Bot) string {
	if r.builds == nil {
		return "-" // a build without recording
	}
	id := r.builds.BuildStarted(ctx, bot.ID, bot.Generation)
	if id == "" {
		return "-"
	}
	return id
}

func (r *Runner) buildStageName(ctx context.Context, op, stage string) {
	if r.builds != nil && op != "-" && op != "" {
		r.builds.BuildStage(ctx, op, stage)
	}
}

// buildEnd records the outcome of a recorded build.
func (r *Runner) buildEnd(ctx context.Context, op, msg string, err error) {
	if r.builds == nil || op == "" || op == "-" {
		return
	}
	switch {
	case err == nil:
		r.builds.BuildFinished(ctx, op, domain.OpSucceeded, "", "")
	case ctx.Err() != nil:
		r.builds.BuildFinished(ctx, op, domain.OpCancelled, "superseded", "stopped because a newer request replaced it")
	case msg == "build timed out":
		r.builds.BuildFinished(ctx, op, domain.OpFailed, "timeout", msg)
	case strings.HasPrefix(msg, "build failed"):
		r.builds.BuildFinished(ctx, op, domain.OpFailed, "build_failed", msg)
	default:
		r.builds.BuildFinished(ctx, op, domain.OpFailed, "setup_failed", msg)
	}
}

func (r *Runner) startContainer(ctx context.Context, bot domain.Bot, id string, st *botState) time.Duration {
	if r.intentChanged(ctx, bot) {
		return immediate // the next pass removes this now-outdated container
	}
	if bot.ObservedState != "starting" {
		if !r.observe(ctx, bot, sqlite.Observation{State: "starting", ContainerID: id}) {
			return immediate
		}
	}
	if err := r.docker.Start(ctx, id); err != nil {
		return r.fail(ctx, bot, "container failed to start", err, st)
	}
	info, err := r.docker.Inspect(ctx, id)
	if err != nil || info.State != "running" {
		return immediate // exited already; the next pass applies crash backoff
	}
	if !r.observe(ctx, bot, sqlite.Observation{State: "running", SettleGeneration: true, ContainerID: id}) {
		return immediate
	}
	return 0
}

// buildStage runs the dependency/compile step in a resource-limited
// container. Its output is copied to the operation's retained log while it
// runs; the builder gets no bot secrets.
func (r *Runner) buildStage(ctx context.Context, bot domain.Bot, rt runtimes.Runtime, host string, st *botState, op string, shared io.Writer) (string, error) {
	image, err := r.resolve(ctx, rt.BuilderRef())
	if err != nil {
		return "builder image is unavailable", err
	}
	id, err := r.docker.Create(ctx, r.builderSpec(bot, rt, image, host))
	if err != nil {
		return "build container could not be created", err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = r.removeContainer(cctx, ContainerInfo{ID: id, State: "running"})
	}()
	var out io.WriteCloser
	if shared != nil {
		out = nopWriteCloser{shared} // the caller owns and closes it
	} else if r.builds != nil && op != "-" {
		out = r.builds.BuildOutput(op)
	}
	followDone := make(chan struct{})
	fctx, stopFollow := context.WithCancel(context.WithoutCancel(ctx))
	defer stopFollow()
	if err := r.docker.Start(ctx, id); err != nil {
		if out != nil {
			out.Close()
		}
		return "build container failed to start", err
	}
	r.buildStageName(ctx, op, "Building")
	if out != nil {
		go func() {
			defer close(followDone)
			_ = r.docker.Follow(fctx, id, out)
		}()
	} else {
		close(followDone)
	}
	finishOutput := func() {
		// The stream ends when the builder exits; do not wait forever for it.
		select {
		case <-followDone:
		case <-time.After(2 * time.Second):
			stopFollow()
			<-followDone
		}
		if out != nil {
			out.Close()
		}
	}
	timeout := r.opts.BuildTimeout
	if rt.BuildTimeout > 0 {
		timeout = rt.BuildTimeout
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	code, err := r.docker.Wait(wctx, id)
	if err != nil {
		stopFollow()
		finishOutput()
		if ctx.Err() == nil && errors.Is(wctx.Err(), context.DeadlineExceeded) {
			return "build timed out", err
		}
		return "build could not be completed", err
	}
	finishOutput()
	if code != 0 {
		msg := fmt.Sprintf("build failed (exit code %d)", code)
		// Builders receive no bot secrets, so their output is safe to surface.
		// It is what tells the user why the build failed.
		if tail, err := r.docker.Tail(context.WithoutCancel(ctx), id, 12); err == nil {
			if t := tidyBuildOutput(tail); t != "" {
				msg += ": " + t
			}
		}
		return msg, fmt.Errorf("builder exited %d", code)
	}
	st.built, st.builtGen = true, bot.Generation
	return "", nil
}

// tidyBuildOutput reduces builder output to a short single-purpose excerpt:
// control characters and ANSI colors removed, at most ~700 characters, ending on
// the last lines (where the actual error is).
func tidyBuildOutput(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
	}
	// Compiler invocations can be a single enormous line whose end holds the
	// reason (e.g. "(signal: 9, SIGKILL: kill)"): keep the tail of long lines.
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	for i, l := range lines {
		if r := []rune(l); len(r) > 200 {
			lines[i] = "…" + string(r[len(r)-160:])
		}
	}
	out := strings.Join(lines, "\n")
	if r := []rune(out); len(r) > 700 {
		out = "…" + string(r[len(r)-700:])
	}
	return out
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
