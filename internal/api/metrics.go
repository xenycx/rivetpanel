package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/hostmon"
)

// panelMetrics intentionally exports bounded, low-cardinality aggregates. Bot
// IDs, names, owners, routes and user-controlled strings are not labels.
type panelMetrics struct {
	started    time.Time
	requests   atomic.Uint64
	errors     atomic.Uint64
	inflight   atomic.Int64
	durationNS atomic.Uint64
}

func newPanelMetrics() *panelMetrics { return &panelMetrics{started: time.Now()} }

// stats is the same set of counters the Host page shows.
func (m *panelMetrics) stats() hostmon.HTTPStats {
	st := hostmon.HTTPStats{Requests: m.requests.Load(), Errors: m.errors.Load(), InFlight: m.inflight.Load()}
	if st.Requests > 0 {
		st.AvgMS = float64(m.durationNS.Load()) / float64(st.Requests) / 1e6
	}
	return st
}

func (m *panelMetrics) observe(c fiber.Ctx) error {
	start := time.Now()
	m.requests.Add(1)
	m.inflight.Add(1)
	err := c.Next()
	m.inflight.Add(-1)
	m.durationNS.Add(uint64(time.Since(start)))
	if err != nil || c.Response().StatusCode() >= 500 {
		m.errors.Add(1)
	}
	return err
}

func (m *panelMetrics) handler(d Deps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if d.MetricsToken == "" {
			return fiber.ErrNotFound
		}
		got, ok := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer ")
		if !ok || len(got) != len(d.MetricsToken) || subtle.ConstantTimeCompare([]byte(got), []byte(d.MetricsToken)) != 1 {
			c.Set("WWW-Authenticate", `Bearer realm="metrics"`)
			return fiber.ErrUnauthorized
		}
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()
		var b strings.Builder
		metric := func(help, typ, line string) {
			name := strings.Fields(line)[0]
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s\n", name, help, name, typ, line)
		}
		metric("Panel process uptime in seconds.", "gauge", fmt.Sprintf("rivetpanel_uptime_seconds %.3f", time.Since(m.started).Seconds()))
		metric("HTTP requests received since process start.", "counter", fmt.Sprintf("rivetpanel_http_requests_total %d", m.requests.Load()))
		metric("HTTP requests ending in an internal error.", "counter", fmt.Sprintf("rivetpanel_http_errors_total %d", m.errors.Load()))
		metric("HTTP requests currently executing.", "gauge", fmt.Sprintf("rivetpanel_http_requests_in_flight %d", m.inflight.Load()))
		metric("Cumulative request execution time in seconds.", "counter", fmt.Sprintf("rivetpanel_http_request_duration_seconds_total %.6f", float64(m.durationNS.Load())/float64(time.Second)))
		dbUp := 1
		if err := d.DB.PingContext(ctx); err != nil {
			dbUp = 0
		}
		metric("Whether the panel database responds.", "gauge", fmt.Sprintf("rivetpanel_database_up %d", dbUp))
		if d.Bots != nil {
			if bots, err := d.Bots.Store.ListBots(ctx, ""); err == nil {
				counts := map[string]int{}
				for _, bot := range bots {
					counts[bot.DesiredState+"\x00"+bot.ObservedState]++
				}
				b.WriteString("# HELP rivetpanel_bots Bots by desired and observed lifecycle state.\n# TYPE rivetpanel_bots gauge\n")
				keys := make([]string, 0, len(counts))
				for k := range counts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					p := strings.SplitN(k, "\x00", 2)
					fmt.Fprintf(&b, "rivetpanel_bots{desired=%s,observed=%s} %d\n", strconv.Quote(p[0]), strconv.Quote(p[1]), counts[k])
				}
			}
		}
		if d.Nodes != nil {
			if nodes, err := d.Nodes.ListNodes(ctx); err == nil {
				b.WriteString("# HELP rivetpanel_node_cpu_percent Latest host CPU utilization.\n# TYPE rivetpanel_node_cpu_percent gauge\n# HELP rivetpanel_node_memory_used_bytes Latest host memory usage.\n# TYPE rivetpanel_node_memory_used_bytes gauge\n# HELP rivetpanel_node_disk_used_bytes Latest host disk usage.\n# TYPE rivetpanel_node_disk_used_bytes gauge\n# HELP rivetpanel_node_running_bots Latest running bot count.\n# TYPE rivetpanel_node_running_bots gauge\n")
				for _, n := range nodes {
					rows, err := d.Nodes.ListTelemetry(ctx, n.ID, 0, 1)
					if err != nil || len(rows) == 0 {
						continue
					}
					x := rows[0]
					label := strconv.Quote(n.ID)
					fmt.Fprintf(&b, "rivetpanel_node_cpu_percent{node=%s} %g\nrivetpanel_node_memory_used_bytes{node=%s} %d\nrivetpanel_node_disk_used_bytes{node=%s} %d\nrivetpanel_node_running_bots{node=%s} %d\n", label, x.CPUPercent, label, x.MemoryUsedBytes, label, x.DiskUsedBytes, label, x.RunningBots)
				}
			}
		}
		if d.Health != nil {
			if probes, err := d.Health.Store.ListHealthProbes(ctx); err == nil {
				counts := map[string]int{}
				for _, p := range probes {
					counts[p.Status]++
				}
				b.WriteString("# HELP rivetpanel_health_probes Configured probes by current state.\n# TYPE rivetpanel_health_probes gauge\n")
				for _, state := range []string{"unknown", "starting", "healthy", "unhealthy"} {
					fmt.Fprintf(&b, "rivetpanel_health_probes{status=%s} %d\n", strconv.Quote(state), counts[state])
				}
			}
		}
		c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.SendString(b.String())
	}
}
