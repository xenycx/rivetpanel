package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

// VolumesDir holds a container app's persistent volumes inside its files:
// volumes/<name> is mounted at the volume's path.
const VolumesDir = "volumes"

// StackNetwork is the private network of a stack. Like an add-on network it
// is internal (no route out); services reach each other on it by name.
func StackNetwork(stackID string) string { return "rivetpanel-stack-" + stackID }

// containerEnv merges a container app's variables. Unlike a language
// runtime it gets no HOME or other panel defaults: the image decides.
func containerEnv(layers ...map[string]string) []string {
	merged := map[string]string{}
	for _, l := range layers {
		for k, v := range l {
			merged[k] = v
		}
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// applyContainer adapts a runtime spec for a container app: the image's own
// working directory, entrypoint and command unless the app sets them, no
// /workspace mount, its volumes and writable paths, its user, and its
// stack's network under its service name.
func (r *Runner) applyContainer(bot domain.Bot, s *ContainerSpec, host string, provided, own map[string]string) {
	c := bot.Container
	if c == nil {
		c = &domain.ContainerConfig{}
	}
	s.WorkspaceHostPath = ""
	s.KeepImageEntrypoint = len(bot.Entrypoint) == 0
	s.Env = containerEnv(provided, own)
	if c.User != "" {
		s.User = c.User
	}
	s.Mounts = nil
	if host != "" {
		for _, v := range c.Volumes {
			s.Mounts = append(s.Mounts, Mount{Source: filepath.Join(host, VolumesDir, v.Name), Target: v.Path})
		}
	}
	s.ExtraTmpfs = append([]string(nil), c.Writable...)
	if c.StackID == "" {
		return
	}
	stack := StackNetwork(c.StackID)
	if s.Network == "none" {
		// Outbound access off (or no default network): the stack's internal
		// network is the only one, so the service still reaches its stack.
		s.Network, s.NetworkAliases = stack, []string{c.Service}
		return
	}
	s.Networks = append(s.Networks, stack)
	s.JoinAliases = map[string][]string{stack: {c.Service}}
}

// prepareContainer checks a container app's image against the panel's
// policy and creates what its container needs before it exists: volume
// folders (bind sources must exist), file ownership for its user, and its
// stack's network.
func (r *Runner) prepareContainer(ctx context.Context, bot domain.Bot) (string, error) {
	if r.opts.CheckImage != nil {
		if err := r.opts.CheckImage(bot.ImageRef); err != nil {
			return "the image is not allowed on this panel", err
		}
	}
	c := bot.Container
	if c == nil {
		return "", nil
	}
	if dm, ok := r.ws.(dirMaker); ok {
		for _, v := range c.Volumes {
			if err := dm.EnsureDir(bot.ID, filepath.Join(VolumesDir, v.Name)); err != nil {
				return "a volume folder could not be created", err
			}
		}
	}
	if c.StackID != "" {
		nw, err := r.networker()
		if err != nil {
			return "stack networks need Docker networking", err
		}
		labels := map[string]string{LabelManaged: "true", LabelNetwork: "stack"}
		if r.opts.InstallID != "" {
			labels[LabelInstall] = r.opts.InstallID
		}
		if err := nw.EnsureNetwork(ctx, StackNetwork(c.StackID), labels); err != nil {
			return "the stack's network could not be created", err
		}
	}
	return "", nil
}

// containerOwner is the host owner of a container app's files: the app's own
// user when it sets one and the daemon does not remap IDs, else the panel's.
func (r *Runner) containerOwner(bot domain.Bot) (int, int) {
	if bot.Container == nil || bot.Container.User == "" || r.opts.WorkspaceOwner != r.opts.User {
		return r.uid, r.gid
	}
	uid, gid, err := ParseUser(bot.Container.User)
	if err != nil {
		return r.uid, r.gid
	}
	return uid, gid
}

// checkImageSize refuses an image larger than the panel allows. The image is
// already pulled; a refused one is left for Docker's own image pruning.
func (r *Runner) checkImageSize(ctx context.Context, ref string) error {
	if r.opts.MaxImageBytes <= 0 {
		return nil
	}
	sz, ok := r.docker.(ImageSizer)
	if !ok {
		return nil
	}
	n, err := sz.ImageSize(ctx, ref)
	if err != nil {
		return err
	}
	if n > r.opts.MaxImageBytes {
		return fmt.Errorf("the image is %d MiB; this panel allows at most %d MiB", n>>20, r.opts.MaxImageBytes>>20)
	}
	return nil
}

// waitingFor lists the services a stack service depends on that are not
// running yet (none for a standalone container).
func (r *Runner) waitingFor(ctx context.Context, bot domain.Bot) ([]string, error) {
	c := bot.Container
	if c == nil || c.StackID == "" || len(c.DependsOn) == 0 {
		return nil, nil
	}
	ss, ok := r.store.(StackStates)
	if !ok {
		return nil, nil
	}
	states, err := ss.StackServiceStates(ctx, c.StackID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range c.DependsOn {
		if states[d] != "running" {
			out = append(out, d)
		}
	}
	return out, nil
}

// waitingMessage explains a stack service that waits for others.
func waitingMessage(deps []string) string {
	return "waiting for " + strings.Join(deps, ", ") + " to run"
}

// StackStates is the optional store view stack ordering needs.
type StackStates interface {
	StackServiceStates(ctx context.Context, stackID string) (map[string]string, error)
}

// containerRuntime is the runtime of a container app.
func containerRuntime(bot domain.Bot) runtimes.Runtime { return runtimes.ForImage(bot.ImageRef) }

// removeStackNetwork deletes a stack's network once its last service is
// gone; while other services still use it Docker refuses, which is fine.
func (r *Runner) removeStackNetwork(ctx context.Context, bot domain.Bot) {
	if bot.Container == nil || bot.Container.StackID == "" {
		return
	}
	if nw, err := r.networker(); err == nil {
		_ = nw.RemoveNetwork(ctx, StackNetwork(bot.Container.StackID))
	}
}

// dependencyPoll is how often a stack service checks on the services it
// waits for.
const dependencyPoll = 3 * time.Second

// joinNetworks attaches a created container to its extra networks, under its
// host names where the spec gives some.
func (r *Runner) joinNetworks(ctx context.Context, spec ContainerSpec, id string) error {
	nw, err := r.networker()
	if err != nil {
		return err
	}
	for _, n := range spec.Networks {
		if aliases := spec.JoinAliases[n]; len(aliases) > 0 {
			if ac, ok := nw.(AliasConnector); ok {
				if err := ac.ConnectNetworkAliases(ctx, n, id, aliases); err != nil {
					return err
				}
				continue
			}
		}
		if err := nw.ConnectNetwork(ctx, n, id); err != nil {
			return err
		}
	}
	return nil
}

// RequestPull makes a container app pull its image again before its next
// container is created, so a moving tag such as :latest is refreshed.
func (r *Runner) RequestPull(botID string) {
	r.mu.Lock()
	if r.pulls == nil {
		r.pulls = map[string]bool{}
	}
	r.pulls[botID] = true
	r.mu.Unlock()
}

// takePull reports and clears a pending pull request for a bot.
func (r *Runner) takePull(botID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pulls[botID] {
		return false
	}
	delete(r.pulls, botID)
	return true
}

// ImagePuller pulls an image even when a copy exists locally. Optional.
type ImagePuller interface {
	PullImage(ctx context.Context, ref string) error
}

// refreshImage pulls a container app's image when a pull was requested and
// forgets the cached digest so the new image is used.
func (r *Runner) refreshImage(ctx context.Context, bot domain.Bot, ref string) error {
	if !r.takePull(bot.ID) {
		return nil
	}
	p, ok := r.docker.(ImagePuller)
	if !ok {
		return nil
	}
	if err := p.PullImage(ctx, ref); err != nil {
		r.RequestPull(bot.ID) // try again next time
		return err
	}
	r.mu.Lock()
	delete(r.images, ref)
	r.mu.Unlock()
	return nil
}
