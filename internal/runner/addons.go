package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// AddonNetwork is the private, internal network that joins a bot to its
// add-ons. Add-ons are attached only to it, so they have no outbound access
// and no other bot can reach them.
func AddonNetwork(botID string) string { return "rivetpanel-" + botID + "-net" }

// AddonContainerName is deterministic like every other managed container.
func AddonContainerName(botID, kind string) string { return "rivetpanel-" + botID + "-addon-" + kind }

const (
	addonTmpfsBytes = 64 << 20
	// addonReadyTimeout bounds how long a bot start waits for its add-ons'
	// health checks (a first PostgreSQL or MariaDB start initialises files).
	addonReadyTimeout = 3 * time.Minute
)

func (r *Runner) networker() (Networker, error) {
	n, ok := r.docker.(Networker)
	if !ok {
		return nil, errors.New("this runner cannot create private networks for add-ons")
	}
	return n, nil
}

// addonSpec builds an add-on container. The password only reaches the
// add-on's environment; the spec hash never includes it.
func (r *Runner) addonSpec(bot domain.Bot, a domain.BotAddon, k addons.Kind, image, dataDir, password string) ContainerSpec {
	var env []string
	if k.Env != nil {
		for n, v := range k.Env(password) {
			env = append(env, n+"="+v)
		}
		sort.Strings(env)
	}
	var argv []string
	if k.Argv != nil {
		argv = k.Argv(password)
	}
	s := ContainerSpec{
		Name: AddonContainerName(bot.ID, k.ID), Role: RoleAddon, AddonKind: k.ID, BotID: bot.ID, NodeID: bot.NodeID,
		InstallID: r.opts.InstallID, Generation: bot.Generation, Image: image, Argv: argv, KeepImageEntrypoint: true,
		Env: env, User: r.opts.User, Network: AddonNetwork(bot.ID), NetworkAliases: []string{k.ID},
		Mounts: []Mount{{Source: dataDir, Target: k.DataPath}}, ExtraTmpfs: k.Tmpfs, Health: k.Health,
		MemoryBytes: a.MemoryBytes, NanoCPUs: k.NanoCPUs, PidsLimit: k.PidsLimit, TmpfsBytes: addonTmpfsBytes,
	}
	b, _ := json.Marshal(struct {
		Kind, Image, User, Network, Data string
		Argv                             []string
		Mem, CPU, Pids                   int64
	}{k.ID, k.Image, s.User, s.Network, dataDir, argv, s.MemoryBytes, s.NanoCPUs, s.PidsLimit})
	h := sha256.Sum256(b)
	s.SpecHash = hex.EncodeToString(h[:16])
	return s
}

// ensureAddons makes the bot's add-on containers match its configuration and
// waits until they report healthy. existing are the bot's current add-on
// containers; env is the bot's decrypted environment (it holds the
// generated passwords).
func (r *Runner) ensureAddons(ctx context.Context, bot domain.Bot, existing []ContainerInfo, env map[string]string) (string, error) {
	want := map[string]domain.BotAddon{}
	for _, a := range bot.Addons {
		want[a.Kind] = a
	}
	byKind := map[string]ContainerInfo{}
	for _, c := range existing {
		kind := c.Labels[LabelAddon]
		if _, ok := want[kind]; !ok || byKind[kind].ID != "" {
			if err := r.removeContainer(ctx, c); err != nil {
				return "a removed add-on could not be stopped", err
			}
			continue
		}
		byKind[kind] = c
	}
	if len(want) == 0 {
		if len(existing) > 0 {
			if nw, err := r.networker(); err == nil {
				_ = nw.RemoveNetwork(ctx, AddonNetwork(bot.ID))
			}
		}
		return "", nil
	}
	nw, err := r.networker()
	if err != nil {
		return "add-ons are not supported by this runner", err
	}
	if r.opts.AddonData.Dir == "" {
		return "add-ons are not configured on this panel", errors.New("no add-on data directory")
	}
	labels := map[string]string{LabelManaged: "true", LabelBot: bot.ID, LabelNode: bot.NodeID, LabelNetwork: "addons"}
	if r.opts.InstallID != "" {
		labels[LabelInstall] = r.opts.InstallID
	}
	if err := nw.EnsureNetwork(ctx, AddonNetwork(bot.ID), labels); err != nil {
		return "the private add-on network could not be created", err
	}
	var started []string
	for _, a := range bot.Addons {
		k, ok := addons.Get(a.Kind)
		if !ok {
			return fmt.Sprintf("add-on %q is not available", a.Kind), errors.New("unknown add-on")
		}
		pw := env[domain.AddonPasswordVar(k.ID)]
		if k.Password && pw == "" {
			return k.DisplayName + " has no password; remove and add it again", errors.New("missing add-on password")
		}
		dir, err := r.opts.AddonData.Ensure(bot.ID, k.ID, r.uid, r.gid)
		if err != nil {
			return k.DisplayName + " data directory could not be prepared", err
		}
		image, err := r.resolve(ctx, k.Image)
		if err != nil {
			return k.DisplayName + " image is unavailable", err
		}
		spec := r.addonSpec(bot, a, k, image, dir, pw)
		if c, ok := byKind[k.ID]; ok {
			if c.Labels[LabelSpec] == spec.SpecHash {
				if !c.Live() {
					if err := r.docker.Start(ctx, c.ID); err != nil {
						return k.DisplayName + " failed to start", err
					}
				}
				started = append(started, c.ID)
				continue
			}
			if err := r.removeContainer(ctx, c); err != nil {
				return "could not replace the " + k.DisplayName + " container", err
			}
		}
		id, err := r.docker.Create(ctx, spec)
		if err != nil {
			return k.DisplayName + " container could not be created", err
		}
		if err := r.docker.Start(ctx, id); err != nil {
			return k.DisplayName + " failed to start", err
		}
		started = append(started, id)
	}
	return r.awaitHealthy(ctx, started)
}

// awaitHealthy waits until every container's health check passes.
func (r *Runner) awaitHealthy(ctx context.Context, ids []string) (string, error) {
	deadline := r.now().Add(addonReadyTimeout)
	for _, id := range ids {
		for {
			info, err := r.docker.Inspect(ctx, id)
			if err != nil {
				return "an add-on disappeared while starting", err
			}
			kind := info.Labels[LabelAddon]
			switch {
			case info.State == "running" && (info.Health == "healthy" || info.Health == ""):
			case info.State != "running" && info.State != "created" && info.State != "restarting":
				return fmt.Sprintf("add-on %s stopped (exit code %d); see its log on the Add-ons tab", kind, info.ExitCode), errors.New("add-on exited")
			case r.now().After(deadline):
				return fmt.Sprintf("add-on %s did not become ready", kind), errors.New("add-on health timeout")
			default:
				if !sleep(ctx, time.Second) {
					return "", ctx.Err()
				}
				continue
			}
			break
		}
	}
	return "", nil
}

// reviveAddons restarts add-on containers that stopped while the bot is
// running (a crash or an out-of-memory kill), without touching the bot.
func (r *Runner) reviveAddons(ctx context.Context, bot domain.Bot, existing []ContainerInfo) {
	want := map[string]bool{}
	for _, a := range bot.Addons {
		want[a.Kind] = true
	}
	for _, c := range existing {
		if c.Live() || !want[c.Labels[LabelAddon]] {
			continue
		}
		if !c.FinishedAt.IsZero() && r.now().Sub(c.FinishedAt) < 5*time.Second {
			r.after(bot.ID, 5*time.Second) // avoid a tight crash loop
			continue
		}
		r.log.Warn("restarting stopped add-on", "bot", bot.ID, "addon", c.Labels[LabelAddon], "exit_code", c.ExitCode)
		if err := r.docker.Start(ctx, c.ID); err != nil {
			r.log.Warn("add-on restart failed", "bot", bot.ID, "addon", c.Labels[LabelAddon], "err", err)
		}
	}
}

// removeAddonResources deletes a bot's private network and add-on data.
func (r *Runner) removeAddonResources(ctx context.Context, botID string) error {
	if nw, err := r.networker(); err == nil {
		if err := nw.RemoveNetwork(ctx, AddonNetwork(botID)); err != nil {
			return fmt.Errorf("remove add-on network: %w", err)
		}
	}
	if err := r.opts.AddonData.RemoveBot(botID); err != nil {
		return fmt.Errorf("remove add-on data: %w", err)
	}
	return nil
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandRefs replaces ${NAME} in the bot's own variables with values from
// provided (add-on connection details and the bot's other variables), so a
// bot can be given e.g. YAGPDB_PQPASSWORD=${POSTGRES_PASSWORD}. Unknown
// references and lone $ signs are left exactly as written.
func expandRefs(vals map[string]string, provided map[string]string) map[string]string {
	out := make(map[string]string, len(vals))
	for k, v := range vals {
		if strings.Contains(v, "${") {
			v = varRef.ReplaceAllStringFunc(v, func(m string) string {
				name := m[2 : len(m)-1]
				if name == k {
					return m
				}
				if p, ok := provided[name]; ok {
					return p
				}
				if p, ok := vals[name]; ok && !strings.Contains(p, "${") {
					return p
				}
				return m
			})
		}
		out[k] = v
	}
	return out
}

// addonEnv returns the connection variables for the bot's add-ons and the
// bot's own variables with references expanded and add-on passwords removed.
func addonEnv(bot domain.Bot, env map[string]string) (provided, own map[string]string) {
	kinds := make([]string, 0, len(bot.Addons))
	pw := map[string]string{}
	for _, a := range bot.Addons {
		kinds = append(kinds, a.Kind)
		pw[a.Kind] = env[domain.AddonPasswordVar(a.Kind)]
	}
	provided = addons.BotEnv(kinds, pw)
	own = make(map[string]string, len(env))
	for k, v := range env {
		if !strings.HasPrefix(k, "RIVET_ADDON_") {
			own[k] = v
		}
	}
	return provided, expandRefs(own, provided)
}

// AddonStatus is the observed state of one add-on container.
type AddonStatus struct {
	State, Health string
	ExitCode      int
}

// AddonStates reports a bot's add-on containers by kind.
func (r *Runner) AddonStates(ctx context.Context, botID string) (map[string]AddonStatus, error) {
	conts, err := r.listMine(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := map[string]AddonStatus{}
	for _, c := range conts {
		if c.Role() != RoleAddon {
			continue
		}
		if c.Live() {
			if info, err := r.docker.Inspect(ctx, c.ID); err == nil {
				c = info
			}
		}
		out[c.Labels[LabelAddon]] = AddonStatus{State: c.State, Health: c.Health, ExitCode: c.ExitCode}
	}
	return out, nil
}

// AddonLogs returns the last lines of an add-on container's output.
func (r *Runner) AddonLogs(ctx context.Context, botID, kind string, lines int) (string, error) {
	conts, err := r.listMine(ctx, botID)
	if err != nil {
		return "", err
	}
	for _, c := range conts {
		if c.Role() == RoleAddon && c.Labels[LabelAddon] == kind {
			out, err := r.docker.Tail(ctx, c.ID, lines)
			return tidyLog(out), err
		}
	}
	return "", nil
}

// tidyLog removes terminal control sequences from container output.
func tidyLog(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, s)
}
