package service

import (
	"context"
	"testing"
)

func TestLimitsForCapsByNodeHardware(t *testing.T) {
	hw := map[string]NodeCapacity{
		"small":   {CPUs: 2, MemoryBytes: 4000026624}, // a 2-CPU, ~3.7 GiB VPS
		"big":     {CPUs: 8, MemoryBytes: 32 << 30},
		"unknown": {},
	}
	s := &BotService{LocalNode: "small", Limits: Limits{MinMemoryBytes: 32 << 20, MinNanoCPUs: 5e7},
		NodeCapacity: func(_ context.Context, id string) NodeCapacity { return hw[id] }}
	for _, c := range []struct {
		node     string
		capMem   int64
		capCPU   int64
		mem, cpu int64
	}{
		{"small", 0, 0, 3712 << 20, 2e9},          // hardware, memory in whole 128 MiB steps
		{"", 0, 0, 3712 << 20, 2e9},               // "" is the local node
		{"big", 16 << 30, 4e9, 16 << 30, 4e9},     // the panel's cap is lower
		{"small", 16 << 30, 4e9, 3712 << 20, 2e9}, // the node is smaller than the cap
		{"unknown", 0, 0, fallbackMaxMemory, fallbackMaxCPUs},
		{"unknown", 2 << 30, 3e9, 2 << 30, 3e9},
	} {
		s.Limits.MaxMemoryBytes, s.Limits.MaxNanoCPUs = c.capMem, c.capCPU
		l := s.LimitsFor(t.Context(), c.node)
		if l.MaxMemoryBytes != c.mem || l.MaxNanoCPUs != c.cpu {
			t.Errorf("%s (cap %d/%d): got %d/%d want %d/%d", c.node, c.capMem, c.capCPU, l.MaxMemoryBytes, l.MaxNanoCPUs, c.mem, c.cpu)
		}
	}
}
