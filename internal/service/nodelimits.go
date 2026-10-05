package service

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Fallback maximums when neither the panel nor the node's hardware sets one
// (a remote node that has not reported its capabilities yet).
const (
	fallbackMaxMemory int64 = 4 << 30
	fallbackMaxCPUs   int64 = 4_000_000_000
)

// NodeCapacity is a node's hardware as last measured: logical CPUs and total
// memory (0 = unknown).
type NodeCapacity struct {
	CPUs        int
	MemoryBytes int64
}

// LimitsFor returns the resource limits on a node: the panel's maximums
// (0 = none) capped by the node's own CPUs and memory, so a server is never
// given more than its machine has. Docker refuses a CPU limit above the
// host's CPU count when the container is created, so without this cap a
// server could be saved with a limit that can never start.
func (s *BotService) LimitsFor(ctx context.Context, nodeID string) Limits {
	l := s.Limits
	var hw NodeCapacity
	if s.NodeCapacity != nil {
		if nodeID == "" {
			nodeID = s.LocalNode
		}
		hw = s.NodeCapacity(ctx, nodeID)
	}
	l.MaxMemoryBytes = capLimit(l.MaxMemoryBytes, hw.MemoryBytes&^(128<<20-1), fallbackMaxMemory) // whole 128 MiB steps
	l.MaxNanoCPUs = capLimit(l.MaxNanoCPUs, int64(hw.CPUs)*1e9, fallbackMaxCPUs)
	l.MaxMemoryBytes = max(l.MaxMemoryBytes, l.MinMemoryBytes)
	l.MaxNanoCPUs = max(l.MaxNanoCPUs, l.MinNanoCPUs)
	return l
}

// capLimit is the smaller of a configured maximum and the hardware, ignoring
// either when it is 0; fallback when both are.
func capLimit(configured, hardware, fallback int64) int64 {
	switch {
	case configured > 0 && hardware > 0:
		return min(configured, hardware)
	case configured > 0:
		return configured
	case hardware > 0:
		return hardware
	}
	return fallback
}

// LimitsForBot returns the limits on the node an existing server runs on,
// for its settings page.
func (s *BotService) LimitsForBot(ctx context.Context, actor domain.User, id string) (Limits, error) {
	b, err := s.loadPerm(ctx, actor, id, permAny, false)
	if err != nil {
		return Limits{}, err
	}
	return s.LimitsFor(ctx, b.NodeID), nil
}
