package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
)

// fakeMetrics serves a fixed story: host CPU idles at 20 % on 4 cores, then
// app "worker" jumps to 3 cores at 12:00 right after a deploy, and spike 7
// covers 12:00-12:05.
type fakeMetrics struct{ now int64 }

const (
	fmStart = int64(1_795_000_000_000)
	fmSpike = fmStart + 3_600_000 // one hour in
)

func (f fakeMetrics) LocalNodeID() string { return "local" }

func (f fakeMetrics) grid(start, end, step int64) []int64 {
	var t []int64
	for x := start - start%step; x < end; x += step {
		t = append(t, x)
	}
	return t
}

func (f fakeMetrics) Series(_ context.Context, node string, start, end, step int64) (metrics.SeriesResult, error) {
	if node != "local" {
		return metrics.SeriesResult{}, domain.ErrNotFound
	}
	if step <= 0 {
		step = 30_000
	}
	t := f.grid(start, end, step)
	r := metrics.SeriesResult{NodeID: node, StepMS: step, Times: t, CPUs: 4, MemTotal: 8 << 30}
	nan := func() metrics.Col {
		c := make(metrics.Col, len(t))
		for i := range c {
			c[i] = math.NaN()
		}
		return c
	}
	r.CPU, r.CPUMax, r.Mem, r.MemMax, r.Load1, r.NetRx, r.NetTx, r.DiskRead, r.DiskWrite, r.DiskUsed, r.Swap = nan(), nan(), nan(), nan(), nan(), nan(), nan(), nan(), nan(), nan(), nan()
	for i, x := range t {
		r.CPU[i], r.Mem[i], r.Load1[i] = 20, 2<<30, 0.8
		if x >= fmSpike && x < fmSpike+300_000 {
			r.CPU[i], r.Load1[i] = 95, 3.9
		}
	}
	return r, nil
}

func (f fakeMetrics) BotsSeries(_ context.Context, node string, start, end, step int64, top int, metric string) (metrics.BotsSeriesResult, error) {
	t := f.grid(start, end, step)
	w, o, u := make(metrics.Col, len(t)), make(metrics.Col, len(t)), make(metrics.Col, len(t))
	for i, x := range t {
		w[i], o[i], u[i] = 0.1, 0.05, 0.6
		if x >= fmSpike && x < fmSpike+300_000 {
			w[i] = 3.0
		}
	}
	return metrics.BotsSeriesResult{Metric: metric, StepMS: step, Source: "samples", Times: t, Bots: []metrics.BotLine{{BotID: "b-worker", Name: "worker", Values: w}}, Other: o, Unattributed: u}, nil
}

func (f fakeMetrics) Events(_ context.Context, node string, start, end int64) ([]domain.MetricEvent, error) {
	ev := []domain.MetricEvent{}
	if d := fmSpike - 40_000; d >= start && d <= end {
		ev = append(ev, domain.MetricEvent{TMS: d, Kind: "deploy", BotID: "b-worker", BotName: "worker", Text: "Deployed worker"})
	}
	return ev, nil
}

func (f fakeMetrics) spike() domain.LoadSpike {
	end := fmSpike + 300_000
	return domain.LoadSpike{ID: 7, NodeID: "local", StartedAtMS: fmSpike, EndedAtMS: &end, PeakCPUPct: 97, AvgCPUPct: 93,
		TopBots:      []domain.SpikeBot{{BotID: "b-worker", Name: "worker", CPUCores: 3, SharePct: 81}},
		TopProcesses: []domain.ProcessEntry{{PID: 42, Comm: "node", Cmd: "node worker.js", CPUPct: 298, RSSBytes: 300 << 20, BotID: "b-worker", BotName: "worker"}}}
}

func (f fakeMetrics) Spikes(_ context.Context, node string, start, end int64) (metrics.SpikesResult, error) {
	out := metrics.SpikesResult{ThresholdPct: 80, Spikes: []domain.LoadSpike{}}
	if sp := f.spike(); sp.StartedAtMS <= end && *sp.EndedAtMS >= start {
		out.Spikes = append(out.Spikes, sp)
	}
	return out, nil
}

func (f fakeMetrics) At(_ context.Context, node string, t, window int64) (metrics.AtResult, error) {
	sp := f.spike()
	ev, _ := f.Events(context.Background(), node, t-600_000, t+600_000)
	return metrics.AtResult{TMS: t, WindowMS: window, Host: &metrics.AtHost{CPU: 95, Mem: 3 << 30, Load1: 3.9},
		Bots:      []metrics.AtBot{{BotID: "b-worker", Name: "worker", CPUCores: 3, MemBytes: 400 << 20, SharePct: 79}, {BotID: "b-db", Name: "db", CPUCores: 0.3, MemBytes: 900 << 20, SharePct: 8}},
		Processes: &domain.ProcessSnapshot{SampledAtMS: t + 5000, Source: domain.ProcSourceProc, HostCPUPct: 95, Items: sp.TopProcesses},
		Events:    ev, Spike: &sp}, nil
}

func (f fakeMetrics) Processes(context.Context, string, int) (domain.ProcessSnapshot, error) {
	return domain.ProcessSnapshot{SampledAtMS: f.now, Source: domain.ProcSourceProc, HostCPUPct: 50, Items: f.spike().TopProcesses}, nil
}
func (f fakeMetrics) BotSeries(context.Context, string, int64, int64, int64) (metrics.BotSeriesResult, error) {
	return metrics.BotSeriesResult{}, errors.New("not used")
}
func (f fakeMetrics) BotProcesses(context.Context, string) (domain.ProcessSnapshot, error) {
	return domain.ProcessSnapshot{}, metrics.ErrNotRunning
}

func runMetricsTool(t *testing.T, s *AIService, actor domain.User, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	call := &domain.AIToolCall{Name: name}
	out, mutated, err, handled := s.executeAgentTool(context.Background(), actor, domain.AIConversation{}, &domain.AIRun{}, call, newAIBudget(AIRunLimits{}), string(raw))
	if !handled || mutated {
		t.Fatalf("%s: handled=%v mutated=%v", name, handled, mutated)
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if e := json.Unmarshal([]byte(out), &m); e != nil {
		t.Fatalf("%s output is not JSON: %v\n%s", name, e, out)
	}
	if len(out) > 24<<10 {
		t.Fatalf("%s output is %d bytes", name, len(out))
	}
	return m, nil
}

func TestMetricsToolsExplainASpike(t *testing.T) {
	now := fmSpike + 2*time.Hour.Milliseconds()
	s := &AIService{Metrics: fakeMetrics{now: now}, Now: func() time.Time { return time.UnixMilli(now) }}
	admin := domain.User{ID: "a", Role: domain.RoleAdmin}

	for _, n := range []string{"metrics_query", "list_spikes", "explain_load", "top_processes", "cpu_by_app"} {
		if !agentReadOnly[n] || !readOnlyAITools[n] || !agentToolNames[n] {
			t.Fatalf("%s must be an offered read-only tool", n)
		}
	}

	q, err := runMetricsTool(t, s, admin, "metrics_query", map[string]any{"range": "6h"})
	if err != nil {
		t.Fatal(err)
	}
	st := q["stats"].(map[string]any)["cpu"].(map[string]any)
	chart := q["chart"].(map[string]any)
	times := chart["times"].([]any)
	cpu := chart["values"].(map[string]any)["cpu"].([]any)
	if st["max"].(float64) != 95 || st["unit"] != "percent" || st["max_at"] == nil || len(times) > chartPoints || len(times) != len(cpu) || q["cpus"].(float64) != 4 {
		t.Fatalf("metrics_query: %v", q)
	}
	if _, err := runMetricsTool(t, s, admin, "metrics_query", map[string]any{"metrics": []string{"pids"}}); err == nil {
		t.Fatal("a host has no pids metric")
	}
	if _, err := runMetricsTool(t, s, admin, "metrics_query", map[string]any{"range": "soon"}); err == nil {
		t.Fatal("bad range accepted")
	}

	ls, err := runMetricsTool(t, s, admin, "list_spikes", map[string]any{"range": "24h"})
	if err != nil {
		t.Fatal(err)
	}
	sp := ls["spikes"].([]any)
	if ls["threshold_pct"].(float64) != 80 || len(sp) != 1 || sp[0].(map[string]any)["duration_s"].(float64) != 300 || len(ls["annotations"].([]any)) != 1 {
		t.Fatalf("list_spikes: %v", ls)
	}

	ex, err := runMetricsTool(t, s, admin, "explain_load", map[string]any{"spike_id": 7})
	if err != nil {
		t.Fatal(err)
	}
	attr := ex["cpu_attribution"].([]any)
	first := attr[0].(map[string]any)
	last := attr[len(attr)-1].(map[string]any)
	cause := ex["likely_cause"].(string)
	if first["app"] != "worker" || first["share_pct"].(float64) != 79 || !strings.HasPrefix(last["app"].(string), "Host / unattributed") || last["share_pct"].(float64) != 13 {
		t.Fatalf("attribution: %v", attr)
	}
	if !strings.Contains(cause, "worker") || !strings.Contains(cause, "79%") || !strings.Contains(cause, "deploy") || ex["onset"] == nil {
		t.Fatalf("likely cause: %q (%v)", cause, ex)
	}
	if len(ex["events"].([]any)) != 1 || ex["memory_top_apps"].([]any)[0].(map[string]any)["app"] != "db" || ex["host"].(map[string]any)["cores_used"].(float64) != 3.8 {
		t.Fatalf("explain_load: %v", ex)
	}
	// Without arguments it explains the latest spike.
	if ex, err := runMetricsTool(t, s, admin, "explain_load", map[string]any{}); err != nil || !strings.Contains(ex["basis"].(string), "latest spike") {
		t.Fatalf("default explain_load: %v %v", ex, err)
	}
	if _, err := runMetricsTool(t, s, admin, "explain_load", map[string]any{"spike_id": 99}); err == nil {
		t.Fatal("unknown spike accepted")
	}

	tp, err := runMetricsTool(t, s, admin, "top_processes", map[string]any{"limit": 5})
	if err != nil || tp["processes"].([]any)[0].(map[string]any)["app"] != "worker" {
		t.Fatalf("top_processes: %v %v", tp, err)
	}

	ca, err := runMetricsTool(t, s, admin, "cpu_by_app", map[string]any{"range": "24h"})
	if err != nil {
		t.Fatal(err)
	}
	lines := ca["series"].([]any)
	names := []string{}
	for _, l := range lines {
		names = append(names, l.(map[string]any)["label"].(string))
		if n := len(l.(map[string]any)["values"].([]any)); n != len(ca["times"].([]any)) || n > chartPoints {
			t.Fatalf("cpu_by_app line has %d values", n)
		}
	}
	if strings.Join(names, ",") != "worker,Other apps,Host / unattributed" || ca["unit"] != "cores" {
		t.Fatalf("cpu_by_app: %v", names)
	}
}

func TestMetricsToolsNeedHostAccess(t *testing.T) {
	now := fmSpike
	s := &AIService{Metrics: fakeMetrics{now: now}, Now: func() time.Time { return time.UnixMilli(now) }}
	user := domain.User{ID: "u", Role: domain.RoleUser}
	if user.Can(domain.PermSystemView) {
		t.Fatal("the built-in user role should not hold system.view")
	}
	for _, n := range []string{"metrics_query", "list_spikes", "explain_load", "top_processes", "cpu_by_app"} {
		_, err := runMetricsTool(t, s, user, n, map[string]any{})
		if !errors.Is(err, domain.ErrForbidden) || !strings.Contains(safeToolError(err), "system.view") {
			t.Fatalf("%s without system.view: %v", n, err)
		}
	}
	// No monitoring service: a clear refusal, not a panic.
	s.Metrics = nil
	if _, err := runMetricsTool(t, s, domain.User{Role: domain.RoleAdmin}, "list_spikes", map[string]any{}); err == nil || !strings.Contains(err.Error(), "monitoring is not available") {
		t.Fatalf("nil metrics: %v", err)
	}
}

func TestParseMoment(t *testing.T) {
	for in, want := range map[string]int64{"1795000000000": 1795000000000, "1795000000": 1795000000000, "2026-11-23T14:05:00Z": time.Date(2026, 11, 23, 14, 5, 0, 0, time.UTC).UnixMilli(), "2026-11-23 14:05": time.Date(2026, 11, 23, 14, 5, 0, 0, time.UTC).UnixMilli()} {
		if got, err := parseMoment(in); err != nil || got != want {
			t.Fatalf("%s: %d %v", in, got, err)
		}
	}
	if _, err := parseMoment("yesterday"); err == nil {
		t.Fatal("accepted yesterday")
	}
}
