package agentnode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// portsRoutes serves POST /node/v1/ports/probe: whether host ports can be
// bound on this node. It is a check, not a reservation; the panel uses it to
// skip busy ports and to refuse a start that Docker would fail anyway.
// published, when set, also reports ports that running containers publish
// (any panel or tool), which a bind test misses without a userland proxy.
func portsRoutes(v1 fiber.Router, probe func(ip string, port int) error, published func(ctx context.Context) ([]runner.PublishedPort, error)) {
	if probe == nil {
		probe = BindProbe
	}
	v1.Post("/ports/probe", func(c fiber.Ctx) error {
		var in agentproto.PortProbeRequest
		if err := json.Unmarshal(c.Body(), &in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid port probe request")
		}
		addr, err := netip.ParseAddr(in.IP)
		if err != nil || addr.Zone() != "" {
			return fiber.NewError(fiber.StatusBadRequest, "the address must be an IP literal")
		}
		if len(in.Ports) == 0 || len(in.Ports) > agentproto.MaxProbePorts {
			return fiber.NewError(fiber.StatusBadRequest, "probe between 1 and 64 ports")
		}
		var pubs []runner.PublishedPort
		if published != nil {
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			pubs, _ = published(ctx) // a failed listing leaves the bind test
			cancel()
		}
		out := agentproto.PortProbe{Ports: make([]agentproto.PortProbeResult, 0, len(in.Ports))}
		for _, p := range in.Ports {
			if p < 1 || p > 65535 {
				return fiber.NewError(fiber.StatusBadRequest, "ports must be between 1 and 65535")
			}
			r := agentproto.PortProbeResult{Port: p, Free: true}
			if h, busy := runner.PortHolder(pubs, addr.String(), p, "", ""); busy {
				r.Free, r.Reason = false, runner.PortReason(h.Container)
			} else if err := probe(addr.String(), p); err != nil {
				r.Free, r.Reason = false, probeReason(err)
			}
			out.Ports = append(out.Ports, r)
		}
		return c.JSON(out)
	})
}

// BindProbe binds and immediately releases a TCP and a UDP socket on
// ip:port, the way Docker publishes a game port. A port that Docker
// publishes without a userland proxy (only firewall rules) is not seen.
func BindProbe(ip string, port int) error {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	l.Close()
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

func probeReason(err error) string {
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return "already in use on the node"
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return "the address is not assigned on the node"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "the agent is not allowed to bind it"
	default:
		return "cannot be bound on the node"
	}
}
