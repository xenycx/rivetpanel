package runner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// PublishedPort is a host port that a running container on this Docker host
// publishes, whoever created the container (this panel, another panel or any
// other tool). Docker reserves these even when it publishes them only with
// firewall rules (no userland proxy), which a bind test cannot see.
type PublishedPort struct {
	HostIP      string // "" or 0.0.0.0 / :: = every address
	HostPort    int
	Proto       string // tcp | udp
	ContainerID string
	Container   string // the container's name, without the leading slash
	BotID       string // the rivetpanel.bot_id label, when it has one
}

// PortLister lists the host ports published by every running container. It
// is optional on a Docker implementation: without it only the start error
// reveals a conflict.
type PortLister interface {
	PublishedPorts(ctx context.Context) ([]PublishedPort, error)
}

func wildcardIP(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" }

// sameHostIP reports whether two host addresses overlap: equal, or either is
// a wildcard that covers every address.
func sameHostIP(a, b string) bool { return wildcardIP(a) || wildcardIP(b) || a == b }

// PortHolder returns the published port that conflicts with ip:port/proto
// (proto "" matches both), ignoring containers of exceptBot.
func PortHolder(pubs []PublishedPort, ip string, port int, proto, exceptBot string) (PublishedPort, bool) {
	for _, p := range pubs {
		if p.HostPort != port || (proto != "" && p.Proto != proto) || !sameHostIP(p.HostIP, ip) {
			continue
		}
		if exceptBot != "" && p.BotID == exceptBot {
			continue
		}
		return p, true
	}
	return PublishedPort{}, false
}

// PortConflictMessage is the state text for a host port that is already
// taken. holder is the container that publishes it ("" when unknown or not a
// container); it is named only when showHolder is set. The stored state text
// is shown to everyone who can see the server, and the container can belong
// to another tenant, so the runner never names it there (administrators
// find it in the panel log, Diagnostics and the allocation views).
func PortConflictMessage(port int, holder string, showHolder bool) string {
	switch {
	case holder != "" && showHolder:
		return fmt.Sprintf("Port %d is already used by container %s. Choose another port in Network or stop that container.", port, holder)
	case holder != "":
		return fmt.Sprintf("Port %d is already used by another container on this host. Choose another port in Network, or ask an administrator to free it.", port)
	}
	return fmt.Sprintf("Port %d is already in use on this host by another program. Choose another port in Network or stop whatever uses it.", port)
}

// HolderReasonPrefix starts a port probe reason that names the container
// holding the port (see PortReason).
const HolderReasonPrefix = "already used by container "

// PortReason words why a probed port is taken by a container, for the
// administrators' allocation views.
func PortReason(holder string) string { return HolderReasonPrefix + holder }

// RedactPortReason hides the container name in a probe reason for
// accounts that may not see other tenants' containers.
func RedactPortReason(reason string) string {
	if strings.HasPrefix(reason, HolderReasonPrefix) {
		return "already used by another container"
	}
	return reason
}

// portErrRe finds the port in Docker's start errors, for example
// "Bind for 0.0.0.0:25565 failed: port is already allocated" and
// "listen tcp4 0.0.0.0:25565: bind: address already in use".
var portErrRe = lazyre.New(`(?:Bind for|listen (?:tcp|udp)[46]?) \[?[0-9A-Fa-f.:]*\]?:(\d{1,5})`)

// IsPortInUse reports whether a container start failed because a host port is
// taken, and which port when the message names it (0 = unknown).
func IsPortInUse(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	msg := err.Error()
	if !strings.Contains(msg, "port is already allocated") && !strings.Contains(msg, "address already in use") && !errors.Is(err, syscall.EADDRINUSE) {
		return 0, false
	}
	if m := portErrRe.FindStringSubmatch(msg); m != nil {
		if p, perr := strconv.Atoi(m[1]); perr == nil {
			return p, true
		}
	}
	return 0, true
}

// wantPorts is the host ports the bot's runtime container publishes, as in
// runtimeSpec.
func (r *Runner) wantPorts(bot domain.Bot) []PortBinding {
	if bot.IsGame() {
		if bot.NetworkDisabled {
			return nil
		}
		if gs, ok := r.cachedGameSpec(bot); ok {
			return gamePorts(gs, bot)
		}
		return nil
	}
	return portBindings(bot)
}

// portConflict looks for a published host port of the bot that a running
// container of anything else already holds. Listing failures are ignored:
// Docker's own start error is the fallback.
// It returns the state text and the holding container's name (for the log).
func (r *Runner) portConflict(ctx context.Context, bot domain.Bot) (msg, holder string) {
	lister, ok := r.docker.(PortLister)
	if !ok {
		return "", ""
	}
	want := r.wantPorts(bot)
	if len(want) == 0 {
		return "", ""
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pubs, err := lister.PublishedPorts(lctx)
	if err != nil {
		return "", ""
	}
	for _, w := range want {
		if h, found := PortHolder(pubs, w.HostIP, w.HostPort, w.Proto, bot.ID); found {
			return PortConflictMessage(w.HostPort, h.Container, false), h.Container
		}
	}
	return "", ""
}

// startErrorPortMessage turns Docker's "port is already allocated" start error
// into the state text, and returns the container that holds the port when
// known (for the log only).
func (r *Runner) startErrorPortMessage(ctx context.Context, bot domain.Bot, port int) (string, string) {
	if port == 0 {
		if want := r.wantPorts(bot); len(want) > 0 {
			port = want[0].HostPort
		}
	}
	holder := ""
	if lister, ok := r.docker.(PortLister); ok && port > 0 {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		pubs, err := lister.PublishedPorts(lctx)
		cancel()
		if err == nil {
			if h, found := PortHolder(pubs, "", port, "", bot.ID); found {
				holder = h.Container
			}
		}
	}
	if port == 0 {
		return "A published port is already in use on this host. Choose another port in Network or stop whatever uses it.", ""
	}
	return PortConflictMessage(port, holder, false), holder
}

// blockPorts records a host port conflict. It is a configuration problem,
// not a crash: nothing is retried automatically and the crash count is left
// alone. A new generation (Start again, a changed port) tries again.
func (r *Runner) blockPorts(ctx context.Context, bot domain.Bot, msg, holder, containerID string) time.Duration {
	r.log.Warn("bot start refused: host port in use", "bot", bot.ID, "reason", msg, "held_by_container", holder)
	o := sqlite.Observation{State: "failed", SettleGeneration: true, LastError: msg, Reason: domain.ReasonPortConflict}
	if containerID != "" {
		o.ContainerID = containerID
	}
	r.observe(ctx, bot, o)
	return 0
}

// portBlocked reports whether this generation already stopped on a port
// conflict, so resyncs and events do not try again on their own.
func portBlocked(bot domain.Bot) bool {
	return bot.ObservedState == "failed" && bot.ObservedGeneration == bot.Generation &&
		bot.StateReason != nil && *bot.StateReason == domain.ReasonPortConflict
}
