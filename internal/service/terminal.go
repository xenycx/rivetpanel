package service

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// ContainerTerminal opens interactive terminals (a TTY exec) in the running
// container of a container app. It runs as the container's own user, inside
// the same isolation as the app: it is a shell in the app, not on the host.
type ContainerTerminal interface {
	OpenTerminal(ctx context.Context, bot domain.Bot, cmd []string, cols, rows uint) (TerminalSession, error)
}

// TerminalSession is one open terminal.
type TerminalSession = runner.TerminalSession

// OpenTerminal opens a terminal in a running container app. Terminals give
// full control of the app, so they need the app's full-admin permission.
func (c *ContainerService) OpenTerminal(ctx context.Context, actor domain.User, id string, cmd []string, cols, rows uint) (TerminalSession, domain.Bot, error) {
	if c.Terminal == nil {
		return nil, domain.Bot{}, domain.Invalid("terminals are not available on this panel")
	}
	b, err := c.Bots.loadPerm(ctx, actor, id, domain.PermFullAdmin, false)
	if err != nil {
		return nil, domain.Bot{}, err
	}
	if !b.IsContainer() {
		return nil, domain.Bot{}, domain.Invalid("terminals are available for container apps")
	}
	if b.ObservedState != "running" {
		return nil, domain.Bot{}, domain.Invalid("start the app to open a terminal")
	}
	if err := checkContainerCommand(cmd, nil); err != nil {
		return nil, domain.Bot{}, err
	}
	if len(cmd) == 0 {
		cmd = []string{"sh"}
	}
	sess, err := c.Terminal.OpenTerminal(ctx, b, cmd, cols, rows)
	return sess, b, err
}
