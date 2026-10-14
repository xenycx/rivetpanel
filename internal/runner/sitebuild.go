package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// publicPrefixes are the variable prefixes front-end frameworks inline into
// the browser bundle at build time. Such values are public by design, so a
// web unit's builder receives them; it still gets no other variable.
var publicPrefixes = []string{"VITE_", "PUBLIC_", "NEXT_PUBLIC_", "NUXT_PUBLIC_", "REACT_APP_", "GATSBY_"}

// PublicBuildPrefixes lists the prefixes of variables passed to web builds.
func PublicBuildPrefixes() []string { return append([]string(nil), publicPrefixes...) }

// PublicBuildVar reports whether a variable is passed to a web build.
func PublicBuildVar(name string) bool {
	for _, p := range publicPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// publicBuildEnv returns the web unit's public build variables.
func (r *Runner) publicBuildEnv(ctx context.Context, bot domain.Bot) map[string]string {
	if r.env == nil {
		return nil
	}
	all, err := r.env.DecryptEnv(ctx, bot.ID)
	if err != nil {
		r.log.Warn("decrypt build variables", "bot", bot.ID, "err", err)
		return nil
	}
	out := map[string]string{}
	for k, v := range all {
		if PublicBuildVar(k) {
			out[k] = v
		}
	}
	return out
}

// BuildError is a failed build with a message that is safe to show (builders
// get no secrets).
type BuildError struct{ Msg string }

func (e *BuildError) Error() string { return e.Msg }

// BuildOnce runs a web unit's build once, outside the reconcile loop: static
// sites publish the build output and never start a container. It shares the
// build slots with the reconciler and streams the build output to out. The
// bot's reconcile lock is held so a resync cannot remove the builder.
func (r *Runner) BuildOnce(ctx context.Context, botID string, out io.Writer) error {
	if err := r.Check(ctx); err != nil {
		return err
	}
	bot, err := r.store.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if !bot.IsWeb() || bot.NodeID != r.opts.NodeID {
		return errors.New("not a web unit on this node")
	}
	rt, ok, err := r.runtimeFor(ctx, bot)
	if err != nil || !ok {
		return &BuildError{Msg: "the runtime is not available in the catalog"}
	}
	if bot.BuildCommand == "" {
		return nil // nothing to build: the files are published as they are
	}
	l := r.lockFor(botID)
	l.Lock()
	defer l.Unlock()
	host, err := r.ws.Path(bot.ID)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if err := r.ws.Prepare(bot.ID, r.uid, r.gid); err != nil {
		return &BuildError{Msg: "workspace ownership could not be prepared"}
	}
	if r.buildSlots != nil {
		select {
		case r.buildSlots <- struct{}{}:
		default:
			fmt.Fprintln(out, "Waiting for a free build slot…")
			select {
			case r.buildSlots <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		defer func() { <-r.buildSlots }()
	}
	if msg, err := r.buildStage(ctx, bot, rt, host, &botState{}, "-", out); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &BuildError{Msg: msg}
	}
	return nil
}
