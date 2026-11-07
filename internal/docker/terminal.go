package docker

import (
	"context"
	"errors"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// terminal is an interactive exec with a TTY. It runs with the container's
// own user, capabilities and no-new-privileges, like the app itself.
type terminal struct {
	a    *Adapter
	id   string
	resp types.HijackedResponse
}

func (t *terminal) Read(p []byte) (int, error)  { return t.resp.Reader.Read(p) }
func (t *terminal) Write(p []byte) (int, error) { return t.resp.Conn.Write(p) }
func (t *terminal) Close() error                { t.resp.Close(); return nil }

func (t *terminal) Resize(cols, rows uint) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return t.a.cli.ContainerExecResize(context.Background(), t.id, container.ResizeOptions{Width: cols, Height: rows})
}

// OpenTerminal starts cmd with a TTY in a container app's running container.
func (a *Adapter) OpenTerminal(ctx context.Context, bot domain.Bot, cmd []string, cols, rows uint) (runner.TerminalSession, error) {
	if bot.ContainerID == nil || *bot.ContainerID == "" {
		return nil, runner.ErrNoContainer
	}
	opts := container.ExecOptions{Cmd: cmd, Tty: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}}
	if cols > 0 && rows > 0 {
		opts.ConsoleSize = &[2]uint{rows, cols}
	}
	created, err := a.cli.ContainerExecCreate(ctx, *bot.ContainerID, opts)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, runner.ErrNoContainer
		}
		if errdefs.IsConflict(err) {
			return nil, errors.New("the container is not running")
		}
		return nil, err
	}
	resp, err := a.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true, ConsoleSize: opts.ConsoleSize})
	if err != nil {
		return nil, err
	}
	return &terminal{a: a, id: created.ID, resp: resp}, nil
}
