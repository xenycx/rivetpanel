package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xenycx/rivetpanel/internal/runtimes"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// DataMount is where a site app's persistent data directory is mounted.
const DataMount = "/data"

// AppNetworkAlias is a site app's host name on the apps network.
func AppNetworkAlias(botID string) string { return "rp-app-" + botID }

// AppsNetworker creates the shared, routable network site apps run on. It
// is optional: without it (or without Options.AppsNetwork) apps use the
// default network.
type AppsNetworker interface {
	EnsureAppsNetwork(ctx context.Context, name string) error
}

// dirMaker creates a directory inside a workspace (filesystem.Manager).
type dirMaker interface {
	EnsureDir(botID, rel string) error
}

// webEnv are the defaults every site app gets; the app's own variables
// (PORT is one, set from the site's port) override them.
var webEnv = map[string]string{"HOST": "0.0.0.0", "DATA_DIR": DataMount}

// applyWeb adapts a runtime spec for a site app: it joins the apps network
// under a stable alias (the sites listener proxies to it), mounts the
// persistent data directory at /data, and adds the web defaults.
func (r *Runner) applyWeb(bot domain.Bot, s *ContainerSpec, host string, layers ...map[string]string) {
	if r.opts.AppsNetwork != "" {
		s.Network = r.opts.AppsNetwork
		s.NetworkAliases = []string{AppNetworkAlias(bot.ID)}
	}
	if host != "" {
		s.Mounts = append(s.Mounts, Mount{Source: filepath.Join(host, filesystem.DataDir), Target: DataMount})
	}
	if layers != nil {
		s.Env = envList(append([]map[string]string{layers[0], webEnv}, layers[1:]...)...)
	}
}

// prepareWeb creates what a site app's container needs before it exists:
// the data directory (bind sources must exist) and the apps network.
func (r *Runner) prepareWeb(ctx context.Context, bot domain.Bot) error {
	if dm, ok := r.ws.(dirMaker); ok {
		if err := dm.EnsureDir(bot.ID, filesystem.DataDir); err != nil {
			return err
		}
	}
	if r.opts.AppsNetwork != "" {
		if an, ok := r.docker.(AppsNetworker); ok {
			return an.EnsureAppsNetwork(ctx, r.opts.AppsNetwork)
		}
	}
	return nil
}

const releaseTimeout = 10 * time.Minute

// releaseStage runs a web unit's release command (for example database
// migrations) in a one-shot container shaped like the app's own: same image,
// variables, networks and data directory. It runs once per generation; a
// failure keeps the app from starting on code its data is not ready for.
func (r *Runner) releaseStage(ctx context.Context, bot domain.Bot, rt runtimes.Runtime, image, host string, env map[string]string, st *botState) (string, error) {
	if !bot.IsWeb() || bot.ReleaseCommand == "" || st.releasedGen == bot.Generation {
		return "", nil
	}
	spec := r.runtimeSpec(bot, rt, image, host, env)
	spec.Name, spec.Role, spec.OpenStdin = ContainerName(bot.ID, RoleRelease), RoleRelease, false
	spec.Entrypoint, spec.Argv, spec.NetworkAliases = nil, BuildCommandArgv(bot.ReleaseCommand), nil
	id, err := r.docker.Create(ctx, spec)
	if err != nil {
		return "the release command's container could not be created", err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = r.removeContainer(cctx, ContainerInfo{ID: id, State: "running"})
	}()
	if len(spec.Networks) > 0 {
		nw, err := r.networker()
		for _, n := range spec.Networks {
			if err == nil {
				err = nw.ConnectNetwork(ctx, n, id)
			}
		}
		if err != nil {
			return "the release command could not join the add-on network", err
		}
	}
	if err := r.docker.Start(ctx, id); err != nil {
		return "the release command failed to start", err
	}
	wctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	code, err := r.docker.Wait(wctx, id)
	if err != nil {
		if ctx.Err() == nil && wctx.Err() != nil {
			return "the release command timed out", err
		}
		return "the release command could not be completed", err
	}
	if code != 0 {
		// Unlike a build, this output may print values from the environment:
		// it is logged for the operator, not stored as the visible error.
		if tail, err := r.docker.Tail(context.WithoutCancel(ctx), id, 20); err == nil && r.log != nil {
			r.log.Warn("release command failed", "bot", bot.ID, "exit", code, "output", tidyBuildOutput(tail))
		}
		return fmt.Sprintf("the release command failed (exit code %d); see the panel log", code), fmt.Errorf("release exited %d", code)
	}
	st.releasedGen = bot.Generation
	return "", nil
}
