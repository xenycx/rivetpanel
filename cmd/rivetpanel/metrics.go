package main

import (
	"context"
	"log/slog"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// newMetrics builds the monitoring attribution service. dk is nil without a
// local runner and router is nil without the agents module.
func newMetrics(cfg config.Config, log *slog.Logger, db *sqlite.DB, bots *service.BotService, dk *docker.Adapter, router *noderoute.Router) *metrics.Metrics {
	host := telemetry.ProcReader{}
	m := &metrics.Metrics{Store: db, LocalNode: domain.LocalNodeID, Proc: metrics.ProcSampler{Root: cfg.HostProc},
		Fast: &metrics.FastSampler{Reader: host}, SpikeCPUPct: cfg.SpikeCPUPct, RawInterval: cfg.TelemetryInterval, Log: log}
	if n, err := host.LogicalCPUs(); err == nil {
		m.LogicalCPUs = n
	}
	if cg := (&metrics.CgroupReader{Root: cfg.HostCgroup}); cg.Available() {
		m.Cgroup = cg
	} else {
		log.Info("metrics: no cgroup v2 hierarchy, per-app samples use Docker statistics", "path", cfg.HostCgroup)
	}
	if dk != nil {
		m.Docker = dk
	}
	// An app's environment values are replaced in its process command
	// lines; they are fetched per snapshot and not kept.
	m.EnvValues = func(ctx context.Context, botID string) []string {
		env, err := bots.DecryptEnv(ctx, botID)
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(env))
		for k, v := range env {
			out = append(out, v)
			delete(env, k)
		}
		return out
	}
	if router != nil {
		m.Remote = &metrics.Remote{
			Online: router.OnlineNodes,
			Stats: func(nodeID string) metrics.StatsStream {
				if !router.Online(nodeID) {
					return nil
				}
				return router.StatsFor(nodeID)
			},
			Processes: func(ctx context.Context, nodeID string, limit int, containerID string) (domain.ProcessSnapshot, error) {
				p, err := router.Processes(ctx, nodeID, limit, containerID)
				if err != nil {
					return domain.ProcessSnapshot{}, err
				}
				src := p.Source
				if src == "" {
					src = domain.ProcSourceAgent
				}
				return domain.ProcessSnapshot{NodeID: nodeID, SampledAtMS: p.SampledAtMS, Source: src, HostCPUPct: p.HostCPUPct, Items: p.Items}, nil
			},
			Host: func(ctx context.Context, nodeID string) (metrics.HostPoint, error) {
				t, err := router.Telemetry(ctx, nodeID)
				if err != nil {
					return metrics.HostPoint{}, err
				}
				return metrics.HostPoint{T: t.SampledAtMS, CPU: t.CPUPercent, Mem: t.MemoryUsedBytes, Load1: t.Load1}, nil
			},
		}
	}
	return m
}
