package main

import (
	"context"
	"encoding/json"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// nodeCapacity measures a node's CPUs and memory for the per-server limits:
// the local node from its Docker daemon (else /proc), a remote node from the
// capabilities its agent reported when it last connected.
func nodeCapacity(agentState func(ctx context.Context, nodeID string) (domain.AgentState, error),
	dk func() *docker.Adapter) func(ctx context.Context, nodeID string) service.NodeCapacity {
	return func(ctx context.Context, nodeID string) service.NodeCapacity {
		if nodeID == domain.LocalNodeID {
			if a := dk(); a != nil {
				if cpus, mem, err := a.HostResources(ctx); err == nil {
					return service.NodeCapacity{CPUs: cpus, MemoryBytes: mem}
				}
			}
			var p telemetry.ProcReader
			var c service.NodeCapacity
			c.CPUs, _ = p.LogicalCPUs()
			_, c.MemoryBytes, _ = p.Memory()
			return c
		}
		st, err := agentState(ctx, nodeID)
		if err != nil || st.CapabilitiesJSON == "" {
			return service.NodeCapacity{}
		}
		var caps agentproto.Capabilities
		if json.Unmarshal([]byte(st.CapabilitiesJSON), &caps) != nil {
			return service.NodeCapacity{}
		}
		return service.NodeCapacity{CPUs: caps.CPUs, MemoryBytes: caps.MemoryBytes}
	}
}
