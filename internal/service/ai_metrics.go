package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
)

// AIMetrics is the monitoring service the assistant's load-debugging tools
// read (*metrics.Metrics). It does not authorize: node data needs
// system.view and app data access to that app's console, checked here.
type AIMetrics interface {
	LocalNodeID() string
	Series(ctx context.Context, node string, start, end, step int64) (metrics.SeriesResult, error)
	BotsSeries(ctx context.Context, node string, start, end, step int64, top int, metric string) (metrics.BotsSeriesResult, error)
	Events(ctx context.Context, node string, start, end int64) ([]domain.MetricEvent, error)
	Spikes(ctx context.Context, node string, start, end int64) (metrics.SpikesResult, error)
	At(ctx context.Context, node string, t, window int64) (metrics.AtResult, error)
	Processes(ctx context.Context, node string, limit int) (domain.ProcessSnapshot, error)
	BotSeries(ctx context.Context, bot string, start, end, step int64) (metrics.BotSeriesResult, error)
	BotProcesses(ctx context.Context, bot string) (domain.ProcessSnapshot, error)
}

const (
	chartPoints  = 120 // points per series handed to the model (one TimeSeries view)
	statPoints   = 480 // resolution the summary statistics are computed at
	spikeHistory = 31 * 24 * time.Hour
	eventWindow  = 10 * time.Minute
)

// metricsToolNames are the load-debugging tools (all read-only).
var metricsToolNames = map[string]bool{"metrics_query": true, "list_spikes": true, "explain_load": true, "top_processes": true, "cpu_by_app": true}

// metricsTools are the read-only tools that explain host and app load.
func metricsTools() []operator.Tool {
	node := map[string]any{"type": "string", "description": "Node id; defaults to the panel's own node"}
	rng := map[string]any{"type": "string", "description": "Window ending now: 15m, 1h, 6h, 24h, 7d, 30d (default 1h; list_spikes 24h)"}
	start := map[string]any{"type": "string", "description": "Start instead of range: ISO 8601 (UTC unless an offset is given) or Unix ms"}
	end := map[string]any{"type": "string", "description": "End: ISO 8601 or Unix ms (default now)"}
	bot := map[string]any{"type": "string", "description": "App or bot id or name"}
	return []operator.Tool{
		toolSchema("metrics_query", "Read host or per-app resource series over a time range. Returns summary statistics per metric (avg, p95, max with its time, current) and a downsampled chart (times in Unix ms and values, at most 120 points) that can go straight into a show_ui TimeSeries. Host units: cpu and cpu_max percent of the whole host, mem/swap/disk_used bytes, net_rx/net_tx/disk_read/disk_write bytes per second, load1. App units (bot_id): cpu in cores, mem bytes, net_rx/net_tx/blk_read/blk_write bytes per second, pids.",
			map[string]any{"node_id": node, "bot_id": map[string]any{"type": "string", "description": "App or bot id or name: read that app instead of the host"}, "range": rng, "start": start, "end": end,
				"metrics": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "enum": []string{"cpu", "cpu_max", "mem", "load1", "net_rx", "net_tx", "disk_read", "disk_write", "disk_used", "swap", "blk_read", "blk_write", "pids"}}, "description": "Default cpu and mem (the host also load1)"}}),
		toolSchema("list_spikes", "List recorded host CPU spikes (host CPU at or above the spike threshold) in a time range, most recent first: id, start, end, duration, peak and average CPU, the apps and processes that used the most CPU during each, and ready-made chart annotations. Only the panel's own node detects spikes.",
			map[string]any{"node_id": node, "range": rng, "start": start, "end": end, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "description": "default 15"}}),
		toolSchema("explain_load", "Explain what caused host load at a moment or during a spike: each app's share of the CPU the host used, the unattributed host share (system, panel, processes outside apps), the busiest processes with their app, the top memory users, deploys/restarts/crashes/backups within 10 minutes, when the top contributor started rising, and a machine-generated likely cause. Pass spike_id from list_spikes, or at; with neither it explains the latest spike of the last 24 hours, else now.",
			map[string]any{"node_id": node, "spike_id": map[string]any{"type": "integer"}, "at": map[string]any{"type": "string", "description": "ISO 8601 or Unix ms"}, "window": map[string]any{"type": "string", "description": "Span averaged around at, for example 1m or 5m (default 1m)"}}),
		toolSchema("top_processes", "List the busiest processes right now with CPU (100 = one core), resident memory, command line and the app each belongs to: for the host (needs host monitoring access) or, with bot_id, inside one app's container.",
			map[string]any{"node_id": node, "bot_id": bot, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 40, "description": "default 15"}}),
		toolSchema("cpu_by_app", "Per-app CPU (cores) or memory (bytes) series over a range for the top apps, plus \"Other apps\" and \"Host / unattributed\" (host use no app explains). At most 120 points, ready for a stacked show_ui TimeSeries.",
			map[string]any{"node_id": node, "range": rng, "start": start, "end": end, "top": map[string]any{"type": "integer", "minimum": 1, "maximum": 6, "description": "default 5"}, "metric": map[string]any{"type": "string", "enum": []string{"cpu", "mem"}}}),
	}
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// metricsTool runs one load-debugging tool.
func (s *AIService) metricsTool(ctx context.Context, actor domain.User, c domain.AIConversation, a agentArgs, name string) (string, error) {
	if s.Metrics == nil {
		return "", domain.Invalid("monitoring is not available on this panel, so there is no load data to read")
	}
	red := s.redactor(ctx, c)
	var out any
	var err error
	switch name {
	case "metrics_query":
		out, err = s.toolMetricsQuery(ctx, actor, a)
	case "list_spikes":
		out, err = s.toolListSpikes(ctx, actor, a)
	case "explain_load":
		out, err = s.toolExplainLoad(ctx, actor, a)
	case "top_processes":
		out, err = s.toolTopProcesses(ctx, actor, a)
	case "cpu_by_app":
		out, err = s.toolCPUByApp(ctx, actor, a)
	default:
		return "", domain.Invalid("unknown monitoring tool")
	}
	if err != nil {
		return "", err
	}
	return red.Text(compactJSON(out)), nil
}

// ---------------------------------------------------------------- access

// hostNode checks system.view and resolves the node (default: local).
func (s *AIService) hostNode(actor domain.User, a agentArgs) (string, error) {
	if !actor.Can(domain.PermSystemView) {
		return "", domain.Denied(actor, domain.PermSystemView)
	}
	if n := a.str("node_id"); n != "" {
		return n, nil
	}
	return s.Metrics.LocalNodeID(), nil
}

// metricsBot authorizes console access to an app given by id or name.
func (s *AIService) metricsBot(ctx context.Context, actor domain.User, ref string) (domain.Bot, error) {
	b, err := s.Bots.Authorize(ctx, actor, ref, domain.PermViewConsole)
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		return b, err
	}
	list, e := s.Bots.List(ctx, actor)
	if e != nil {
		return domain.Bot{}, err
	}
	var hit []domain.Bot
	for _, x := range list {
		if strings.EqualFold(x.Name, ref) {
			hit = append(hit, x)
		}
	}
	switch len(hit) {
	case 1:
		return s.Bots.Authorize(ctx, actor, hit[0].ID, domain.PermViewConsole)
	case 0:
		return domain.Bot{}, err
	}
	return domain.Bot{}, domain.Invalid(fmt.Sprintf("%d apps are named %q: pass the id (list_targets shows them)", len(hit), ref))
}

// ---------------------------------------------------------------- time

var durRe = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*(s|m|min|h|d|w)\s*$`)

func parseSpan(v string) (time.Duration, bool) {
	m := durRe.FindStringSubmatch(strings.ToLower(v))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "min": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	d := time.Duration(n * float64(unit))
	return d, d > 0
}

// parseMoment reads Unix ms (or seconds), RFC 3339 or a zone-less ISO time
// (taken as UTC).
func parseMoment(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n < 100_000_000_000 { // seconds
			n *= 1000
		}
		return n, nil
	}
	for _, f := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(f, v); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, domain.Invalid(fmt.Sprintf("%q is not a time: use ISO 8601 such as 2026-11-23T14:05:00Z or Unix milliseconds", v))
}

// timeRange reads range or start/end (Unix ms).
func (s *AIService) timeRange(a agentArgs, def time.Duration) (int64, int64, error) {
	now := s.now()
	end := now
	if v := a.str("end"); v != "" {
		t, err := parseMoment(v)
		if err != nil {
			return 0, 0, err
		}
		end = min(t, now)
	}
	span := def
	if v := a.str("range"); v != "" {
		d, ok := parseSpan(v)
		if !ok {
			return 0, 0, domain.Invalid("range must look like 15m, 1h, 6h, 24h or 7d")
		}
		span = d
	}
	start := end - span.Milliseconds()
	if v := a.str("start"); v != "" {
		t, err := parseMoment(v)
		if err != nil {
			return 0, 0, err
		}
		start = t
		if a.str("end") == "" && a.str("range") != "" {
			end = min(start+span.Milliseconds(), now)
		}
	}
	if end <= start {
		return 0, 0, domain.Invalid("the range must end after it starts (and not in the future)")
	}
	if end-start > metrics.MaxWindow.Milliseconds() {
		return 0, 0, domain.Invalid("the range can be at most 400 days")
	}
	return start, end, nil
}

func iso(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

func stepFor(start, end int64, points int64) int64 {
	return max((end-start+points-1)/points, 1000)
}

// ---------------------------------------------------------------- numbers

func r3(v float64) float64 { return math.Round(v*1000) / 1000 }
func r1(v float64) float64 { return math.Round(v*10) / 10 }

// stat summarises one column (NaN = gap).
type stat struct {
	Unit    string   `json:"unit"`
	Avg     *float64 `json:"avg"`
	P95     *float64 `json:"p95"`
	Max     *float64 `json:"max"`
	MaxAt   string   `json:"max_at,omitempty"`
	Current *float64 `json:"current"`
	Samples int      `json:"samples"`
}

func fp(v float64) *float64 { x := r3(v); return &x }

func summarize(times []int64, col []float64, unit string) stat {
	st := stat{Unit: unit}
	var vals []float64
	sum, mx, mxAt := 0.0, math.Inf(-1), int64(0)
	for i, v := range col {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		vals = append(vals, v)
		sum += v
		if v > mx {
			mx, mxAt = v, times[i]
		}
	}
	st.Samples = len(vals)
	if len(vals) == 0 {
		return st
	}
	st.Avg, st.Max, st.MaxAt = fp(sum/float64(len(vals))), fp(mx), iso(mxAt)
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	st.P95 = fp(sorted[min(len(sorted)-1, int(math.Ceil(0.95*float64(len(sorted))))-1)])
	for i := len(col) - 1; i >= 0; i-- {
		if !math.IsNaN(col[i]) {
			st.Current = fp(col[i])
			break
		}
	}
	return st
}

// downsample averages consecutive buckets so at most n remain; the time of
// each group is its first bucket.
func downsample(times []int64, cols [][]float64, n int) ([]int64, [][]float64) {
	k := (len(times) + n - 1) / max(n, 1)
	if k <= 1 {
		return times, cols
	}
	var t []int64
	out := make([][]float64, len(cols))
	for i := 0; i < len(times); i += k {
		t = append(t, times[i])
		for c, col := range cols {
			sum, w := 0.0, 0
			for j := i; j < min(i+k, len(col)); j++ {
				if !math.IsNaN(col[j]) {
					sum, w = sum+col[j], w+1
				}
			}
			v := math.NaN()
			if w > 0 {
				v = sum / float64(w)
			}
			out[c] = append(out[c], v)
		}
	}
	return t, out
}

// chartCol is a column for the model: null for gaps, three decimals.
func chartCol(col []float64) []*float64 {
	out := make([]*float64, len(col))
	for i, v := range col {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out[i] = fp(v)
		}
	}
	return out
}

// ---------------------------------------------------------------- metrics_query

var hostUnits = map[string]string{"cpu": "percent", "cpu_max": "percent", "mem": "bytes", "load1": "number", "net_rx": "bytes_per_s", "net_tx": "bytes_per_s",
	"disk_read": "bytes_per_s", "disk_write": "bytes_per_s", "disk_used": "bytes", "swap": "bytes"}
var botUnits = map[string]string{"cpu": "cores", "mem": "bytes", "net_rx": "bytes_per_s", "net_tx": "bytes_per_s", "blk_read": "bytes_per_s", "blk_write": "bytes_per_s", "pids": "number"}

func (s *AIService) toolMetricsQuery(ctx context.Context, actor domain.User, a agentArgs) (any, error) {
	start, end, err := s.timeRange(a, time.Hour)
	if err != nil {
		return nil, err
	}
	step := stepFor(start, end, statPoints)
	want := a.strs("metrics")
	out := map[string]any{"start": iso(start), "end": iso(end), "now": iso(s.now())}
	var times []int64
	cols := map[string]metrics.Col{}
	units := hostUnits
	if ref := a.str("bot_id"); ref != "" {
		b, err := s.metricsBot(ctx, actor, ref)
		if err != nil {
			return nil, err
		}
		r, err := s.Metrics.BotSeries(ctx, b.ID, start, end, step)
		if err != nil {
			return nil, err
		}
		units, times = botUnits, r.Times
		cols = map[string]metrics.Col{"cpu": r.CPU, "mem": r.Mem, "net_rx": r.NetRx, "net_tx": r.NetTx, "blk_read": r.BlkRead, "blk_write": r.BlkWrite, "pids": r.PIDs}
		if len(want) == 0 {
			want = []string{"cpu", "mem"}
		}
		out["scope"], out["app"], out["bot_id"], out["source"] = "app", b.Name, b.ID, r.Source
		if r.MemLimit > 0 {
			out["mem_limit_bytes"] = r.MemLimit
		}
		if r.CPULimit > 0 {
			out["cpu_limit_cores"] = r.CPULimit
		}
		if r.Source == "rollups" {
			out["note"] = "older than 48 hours: averaged rollups (5 min, hourly or daily); blk_* and pids are not kept that long"
		}
	} else {
		node, err := s.hostNode(actor, a)
		if err != nil {
			return nil, err
		}
		r, err := s.Metrics.Series(ctx, node, start, end, step)
		if err != nil {
			return nil, err
		}
		times = r.Times
		cols = map[string]metrics.Col{"cpu": r.CPU, "cpu_max": r.CPUMax, "mem": r.Mem, "load1": r.Load1, "net_rx": r.NetRx, "net_tx": r.NetTx,
			"disk_read": r.DiskRead, "disk_write": r.DiskWrite, "disk_used": r.DiskUsed, "swap": r.Swap}
		if len(want) == 0 {
			want = []string{"cpu", "mem", "load1"}
		}
		out["scope"], out["node_id"], out["cpus"] = "host", node, r.CPUs
		if r.MemTotal > 0 {
			out["mem_total_bytes"] = r.MemTotal
		}
		if r.DiskTotal > 0 {
			out["disk_total_bytes"] = r.DiskTotal
		}
	}
	var keep []string
	stats := map[string]stat{}
	for _, m := range want {
		col, ok := cols[m]
		if !ok {
			return nil, domain.Invalid(fmt.Sprintf("%s is not a metric of this %s; choose from %s", m, out["scope"], strings.Join(unitNames(units), ", ")))
		}
		stats[m] = summarize(times, col, units[m])
		keep = append(keep, m)
	}
	list := make([][]float64, len(keep))
	for i, m := range keep {
		list[i] = cols[m]
	}
	ct, cc := downsample(times, list, chartPoints)
	values := map[string]any{}
	for i, m := range keep {
		values[m] = chartCol(cc[i])
	}
	out["step_ms"] = step
	out["stats"] = stats
	out["chart"] = map[string]any{"times": ct, "values": values, "units": pickUnits(units, keep)}
	if len(times) == 0 || allEmpty(stats) {
		out["note"] = "no samples in this range (the panel or node may not have been running, or the range is older than the history kept)"
	}
	return out, nil
}

func allEmpty(st map[string]stat) bool {
	for _, x := range st {
		if x.Samples > 0 {
			return false
		}
	}
	return true
}

func unitNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func pickUnits(m map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

// ---------------------------------------------------------------- spikes

type procView struct {
	PID      int     `json:"pid"`
	Comm     string  `json:"comm"`
	Cmd      string  `json:"cmd,omitempty"`
	CPUPct   float64 `json:"cpu_pct"`
	RSSBytes int64   `json:"rss_bytes"`
	App      string  `json:"app,omitempty"`
	BotID    string  `json:"bot_id,omitempty"`
}

func procViews(items []domain.ProcessEntry, n, cmdLen int) []procView {
	out := make([]procView, 0, min(n, len(items)))
	for _, p := range items {
		if len(out) == n {
			break
		}
		out = append(out, procView{PID: p.PID, Comm: p.Comm, Cmd: clipService(p.Cmd, cmdLen), CPUPct: r1(p.CPUPct), RSSBytes: p.RSSBytes, App: p.BotName, BotID: p.BotID})
	}
	return out
}

type spikeView struct {
	ID          int64             `json:"id"`
	Start       string            `json:"start"`
	StartMS     int64             `json:"start_ms"`
	End         string            `json:"end,omitempty"`
	EndMS       *int64            `json:"end_ms"`
	Ongoing     bool              `json:"ongoing,omitempty"`
	DurationS   int64             `json:"duration_s"`
	PeakCPUPct  float64           `json:"peak_cpu_pct"`
	AvgCPUPct   float64           `json:"avg_cpu_pct"`
	TopApps     []domain.SpikeBot `json:"top_apps"`
	TopProcs    []procView        `json:"top_processes"`
	Unaccounted *float64          `json:"unattributed_share_pct,omitempty"`
}

func (s *AIService) spikeView(sp domain.LoadSpike, procs int) spikeView {
	v := spikeView{ID: sp.ID, Start: iso(sp.StartedAtMS), StartMS: sp.StartedAtMS, EndMS: sp.EndedAtMS, PeakCPUPct: r1(sp.PeakCPUPct), AvgCPUPct: r1(sp.AvgCPUPct), TopApps: []domain.SpikeBot{}}
	end := s.now()
	if sp.EndedAtMS != nil {
		end, v.End = *sp.EndedAtMS, iso(*sp.EndedAtMS)
	} else {
		v.Ongoing = true
	}
	v.DurationS = max(end-sp.StartedAtMS, 0) / 1000
	share := 0.0
	for i, b := range sp.TopBots {
		share += b.SharePct
		if i < 5 {
			b.CPUCores, b.SharePct = r3(b.CPUCores), r1(b.SharePct)
			v.TopApps = append(v.TopApps, b)
		}
	}
	if len(sp.TopBots) > 0 {
		u := r1(max(100-share, 0))
		v.Unaccounted = &u
	}
	v.TopProcs = procViews(sp.TopProcesses, procs, 80)
	return v
}

func (s *AIService) toolListSpikes(ctx context.Context, actor domain.User, a agentArgs) (any, error) {
	node, err := s.hostNode(actor, a)
	if err != nil {
		return nil, err
	}
	start, end, err := s.timeRange(a, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	res, err := s.Metrics.Spikes(ctx, node, start, end)
	if err != nil {
		return nil, err
	}
	limit := int(a.int("limit"))
	if limit <= 0 {
		limit = 15
	}
	limit = min(limit, 30)
	sp := append([]domain.LoadSpike(nil), res.Spikes...)
	sort.Slice(sp, func(i, j int) bool { return sp[i].StartedAtMS > sp[j].StartedAtMS })
	out := map[string]any{"node_id": node, "start": iso(start), "end": iso(end), "now": iso(s.now()), "threshold_pct": res.ThresholdPct, "count": len(sp)}
	views := []spikeView{}
	ann := []map[string]any{}
	for i, x := range sp {
		if i == limit {
			break
		}
		v := s.spikeView(x, 3)
		views = append(views, v)
		an := map[string]any{"t": x.StartedAtMS, "label": fmt.Sprintf("Spike %d (peak %.0f%%)", x.ID, x.PeakCPUPct), "kind": "spike"}
		if x.EndedAtMS != nil {
			an["end"] = *x.EndedAtMS
		}
		ann = append(ann, an)
	}
	out["spikes"] = views
	out["annotations"] = ann
	if len(sp) > limit {
		out["more"] = len(sp) - limit
	}
	if node != s.Metrics.LocalNodeID() {
		out["note"] = "spike detection runs only on the panel's own node; for this node use metrics_query and explain_load with at"
	} else if len(sp) == 0 {
		out["note"] = fmt.Sprintf("no spike in this range: host CPU did not stay at or above %.0f%% long enough to open one", res.ThresholdPct)
	}
	return out, nil
}

// ---------------------------------------------------------------- explain_load

type appShare struct {
	App      string  `json:"app"`
	BotID    string  `json:"bot_id,omitempty"`
	CPUCores float64 `json:"cpu_cores"`
	SharePct float64 `json:"share_pct"`
}

type eventView struct {
	Time  string `json:"time"`
	TMS   int64  `json:"t_ms"`
	Kind  string `json:"kind"`
	App   string `json:"app,omitempty"`
	BotID string `json:"bot_id,omitempty"`
	Text  string `json:"text"`
}

func (s *AIService) findSpike(ctx context.Context, node string, id int64) (*domain.LoadSpike, error) {
	now := s.now()
	res, err := s.Metrics.Spikes(ctx, node, now-spikeHistory.Milliseconds(), now)
	if err != nil {
		return nil, err
	}
	for i := range res.Spikes {
		if res.Spikes[i].ID == id {
			return &res.Spikes[i], nil
		}
	}
	return nil, nil
}

func (s *AIService) toolExplainLoad(ctx context.Context, actor domain.User, a agentArgs) (any, error) {
	node, err := s.hostNode(actor, a)
	if err != nil {
		return nil, err
	}
	now := s.now()
	window := time.Minute.Milliseconds()
	if v := a.str("window"); v != "" {
		d, ok := parseSpan(v)
		if !ok {
			return nil, domain.Invalid("window must look like 30s, 1m or 5m")
		}
		window = d.Milliseconds()
	}
	var spike *domain.LoadSpike
	var t int64
	basis := ""
	switch {
	case a.has("spike_id"):
		sp, err := s.findSpike(ctx, node, a.int("spike_id"))
		if err != nil {
			return nil, err
		}
		if sp == nil {
			return nil, domain.Invalid("no spike with that id on this node in the last 30 days; call list_spikes")
		}
		spike = sp
		basis = "spike"
	case a.str("at") != "":
		if t, err = parseMoment(a.str("at")); err != nil {
			return nil, err
		}
		basis = "moment"
	default:
		res, err := s.Metrics.Spikes(ctx, node, now-24*time.Hour.Milliseconds(), now)
		if err != nil {
			return nil, err
		}
		for i := range res.Spikes {
			if spike == nil || res.Spikes[i].StartedAtMS > spike.StartedAtMS {
				spike = &res.Spikes[i]
			}
		}
		if spike != nil {
			basis = "latest spike of the last 24 hours"
		} else {
			t, basis = now, "now (no spike in the last 24 hours)"
		}
	}
	if spike != nil {
		end := now
		if spike.EndedAtMS != nil {
			end = *spike.EndedAtMS
		}
		// The whole spike, averaged, with t in its middle.
		window = min(max(end-spike.StartedAtMS, 10_000), (6 * time.Hour).Milliseconds())
		t = spike.StartedAtMS + (end-spike.StartedAtMS)/2
	}
	at, err := s.Metrics.At(ctx, node, t, window)
	if err != nil {
		return nil, err
	}
	if spike == nil && at.Spike != nil {
		spike = at.Spike
	}
	out := map[string]any{"node_id": node, "basis": basis, "moment": iso(t), "t_ms": t, "window_s": at.WindowMS / 1000, "now": iso(now)}
	var notes []string

	// Host.
	cpus := 0
	if ser, err := s.Metrics.Series(ctx, node, t-30*time.Minute.Milliseconds(), min(t+30*time.Minute.Milliseconds(), max(now, t+1000)), 60_000); err == nil {
		cpus = ser.CPUs
	}
	used := 0.0
	if at.Host != nil {
		h := map[string]any{"cpu_pct": r1(at.Host.CPU), "mem_bytes": at.Host.Mem, "load1": at.Host.Load1}
		if cpus > 0 {
			used = at.Host.CPU / 100 * float64(cpus)
			h["cpus"], h["cores_used"] = cpus, r3(used)
		}
		out["host"] = h
	} else {
		notes = append(notes, "no host sample around this moment (outside the kept history, or the node was offline)")
	}
	if spike != nil {
		out["spike"] = s.spikeView(*spike, 5)
	}

	// CPU attribution: apps by share, then the host remainder.
	shares := []appShare{}
	total := 0.0
	for _, b := range at.Bots {
		total += b.SharePct
	}
	for i, b := range at.Bots {
		if i == 8 || b.CPUCores <= 0 {
			break
		}
		shares = append(shares, appShare{App: b.Name, BotID: b.BotID, CPUCores: r3(b.CPUCores), SharePct: r1(b.SharePct)})
	}
	unattributed := 0.0
	if at.Host != nil && len(at.Bots) > 0 {
		unattributed = max(100-total, 0)
		shares = append(shares, appShare{App: "Host / unattributed (system, panel, processes outside apps)", CPUCores: r3(max(used*unattributed/100, 0)), SharePct: r1(unattributed)})
	}
	out["cpu_attribution"] = shares
	if len(at.Bots) == 0 {
		notes = append(notes, "no per-app samples around this moment (they are kept 48 hours at 15 s; older moments use 5 min/hourly rollups)")
	}

	// Processes.
	if at.Processes != nil {
		items := append([]domain.ProcessEntry(nil), at.Processes.Items...)
		out["processes"] = map[string]any{"sampled_at": iso(at.Processes.SampledAtMS), "offset_s": (at.Processes.SampledAtMS - t) / 1000, "source": at.Processes.Source,
			"host_cpu_pct": r1(at.Processes.HostCPUPct), "top_cpu": procViews(items, 10, 100)}
		sort.SliceStable(items, func(i, j int) bool { return items[i].RSSBytes > items[j].RSSBytes })
		out["memory_top_processes"] = procViews(items, 5, 60)
	} else {
		notes = append(notes, "no process snapshot near this moment (snapshots are taken every 60 s and kept 7 days; remote nodes need an agent that reports processes)")
	}

	// Memory by app.
	mem := append([]metrics.AtBot(nil), at.Bots...)
	sort.SliceStable(mem, func(i, j int) bool { return mem[i].MemBytes > mem[j].MemBytes })
	memTop := []map[string]any{}
	for i, b := range mem {
		if i == 5 || b.MemBytes <= 0 {
			break
		}
		memTop = append(memTop, map[string]any{"app": b.Name, "bot_id": b.BotID, "mem_bytes": b.MemBytes})
	}
	out["memory_top_apps"] = memTop

	// Events within 10 minutes (of the spike's start when explaining one).
	ref := t
	if spike != nil {
		ref = spike.StartedAtMS
	}
	evs := at.Events
	if spike != nil {
		if e, err := s.Metrics.Events(ctx, node, ref-eventWindow.Milliseconds(), ref+eventWindow.Milliseconds()); err == nil {
			evs = e
		}
	}
	events := []eventView{}
	for _, e := range evs {
		if e.Kind == "spike" {
			continue
		}
		if len(events) == 15 {
			break
		}
		events = append(events, eventView{Time: iso(e.TMS), TMS: e.TMS, Kind: e.Kind, App: e.BotName, BotID: e.BotID, Text: clipService(e.Text, 160)})
	}
	out["events"] = events

	// When the top contributor started rising, and the event before it.
	var onset *int64
	var top *appShare
	if len(shares) > 0 && shares[0].BotID != "" {
		top = &shares[0]
		onset = s.onset(ctx, node, top.BotID, ref, now)
		if onset != nil {
			out["onset"] = map[string]any{"app": top.App, "started_rising_at": iso(*onset), "t_ms": *onset}
		}
	}
	out["likely_cause"] = likelyCause(at, spike, top, unattributed, onset, events, out["processes"] != nil, procTop(at))
	if node != s.Metrics.LocalNodeID() {
		notes = append(notes, "remote node: no spike detection; per-app samples every 30 s and process snapshots every 60 s through the agent")
	}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	return out, nil
}

// procGroup is the busiest command at a moment: processes with the same name
// in the same app are added up, so eleven workers at one core each outrank a
// single process at one and a half.
type procGroup struct {
	Comm, BotID, BotName string
	Count                int
	CPUPct               float64
	PID                  int // the busiest member
}

func (g procGroup) String() string {
	if g.Count > 1 {
		return fmt.Sprintf("%d %s processes (busiest pid %d) together at %.0f%% of a core", g.Count, g.Comm, g.PID, g.CPUPct)
	}
	return fmt.Sprintf("%s (pid %d) at %.0f%% of a core", g.Comm, g.PID, g.CPUPct)
}

func procTop(at metrics.AtResult) *procGroup {
	if at.Processes == nil || len(at.Processes.Items) == 0 {
		return nil
	}
	groups := map[string]*procGroup{}
	var best *procGroup
	for _, p := range at.Processes.Items {
		k := p.Comm + "\x00" + p.BotID
		g := groups[k]
		if g == nil {
			g = &procGroup{Comm: p.Comm, BotID: p.BotID, BotName: p.BotName, PID: p.PID}
			groups[k] = g
		}
		g.Count++
		g.CPUPct += p.CPUPct
		if best == nil || g.CPUPct > best.CPUPct {
			best = g
		}
	}
	return best
}

// onset finds when an app's CPU first rose to half of its level at ref,
// looking 15 minutes back (per-app samples, within 48 hours).
func (s *AIService) onset(ctx context.Context, node, bot string, ref, now int64) *int64 {
	from, to := ref-15*time.Minute.Milliseconds(), min(ref+2*time.Minute.Milliseconds(), now)
	if to <= from {
		return nil
	}
	r, err := s.Metrics.BotsSeries(ctx, node, from, to, 15_000, 8, "cpu")
	if err != nil {
		return nil
	}
	var col metrics.Col
	for _, b := range r.Bots {
		if b.BotID == bot {
			col = b.Values
		}
	}
	if len(col) == 0 {
		return nil
	}
	idx := -1
	for i, tm := range r.Times {
		if tm <= ref {
			idx = i
		}
	}
	if idx < 0 {
		return nil
	}
	level := math.NaN()
	for i := idx; i < len(col) && i <= idx+4; i++ {
		if !math.IsNaN(col[i]) && (math.IsNaN(level) || col[i] > level) {
			level = col[i]
		}
	}
	if math.IsNaN(level) || level <= 0 {
		return nil
	}
	// Walk back while it stays above half the level.
	i := idx
	for i > 0 && (math.IsNaN(col[i-1]) || col[i-1] >= level/2) {
		i--
	}
	if i == 0 {
		return nil // high for the whole lookback: no onset in view
	}
	t := r.Times[i]
	return &t
}

func likelyCause(at metrics.AtResult, spike *domain.LoadSpike, top *appShare, unattributed float64, onset *int64, events []eventView, hasProcs bool, proc *procGroup) string {
	if at.Host == nil && spike == nil {
		return "Unknown: there is no host data for this moment."
	}
	var b strings.Builder
	cpu := 0.0
	if at.Host != nil {
		cpu = at.Host.CPU
	}
	if spike != nil {
		fmt.Fprintf(&b, "Spike %d peaked at %.0f%% CPU (average %.0f%%). ", spike.ID, spike.PeakCPUPct, spike.AvgCPUPct)
	} else {
		fmt.Fprintf(&b, "Host CPU was %.0f%% around %s. ", cpu, time.UnixMilli(at.TMS).UTC().Format("15:04:05 UTC"))
	}
	switch {
	case top == nil && len(at.Bots) == 0:
		b.WriteString("No per-app samples cover it, so the apps' shares are unknown")
		if proc != nil {
			fmt.Fprintf(&b, "; the busiest command was %s", proc)
			if proc.BotName != "" {
				fmt.Fprintf(&b, " in app %s", proc.BotName)
			}
		}
		b.WriteString(".")
	case top == nil || unattributed > top.SharePct:
		fmt.Fprintf(&b, "Most of it (%.0f%%) came from outside the apps (system, the panel or host processes)", unattributed)
		if proc != nil && proc.BotID == "" {
			fmt.Fprintf(&b, "; the busiest such command was %s", proc)
		}
		if top != nil {
			fmt.Fprintf(&b, "; the largest app was %s at %.0f%%", top.App, top.SharePct)
		}
		b.WriteString(".")
	default:
		fmt.Fprintf(&b, "The largest contributor was app %s with %.0f%% of the CPU used (%.2f cores)", top.App, top.SharePct, top.CPUCores)
		if proc != nil && proc.BotID == top.BotID {
			fmt.Fprintf(&b, ", mostly %s", proc)
		}
		b.WriteString(".")
		if onset != nil {
			fmt.Fprintf(&b, " It started rising at %s.", time.UnixMilli(*onset).UTC().Format("15:04:05 UTC"))
			if e := eventBefore(events, *onset, top.BotID); e != nil {
				fmt.Fprintf(&b, " That was %s after a %s event (%s).", humanDur(*onset-e.TMS), e.Kind, e.Text)
			}
		}
	}
	if !hasProcs {
		b.WriteString(" No process snapshot is available for this moment.")
	}
	return b.String()
}

// eventBefore is the latest event at most 10 minutes before t (and up to 30
// seconds after), preferring one of the given app.
func eventBefore(events []eventView, t int64, bot string) *eventView {
	var best, latest *eventView
	for i := range events {
		e := &events[i]
		if e.TMS > t+30_000 || e.TMS < t-eventWindow.Milliseconds() {
			continue
		}
		if latest == nil || e.TMS > latest.TMS {
			latest = e
		}
		if e.BotID == bot && (best == nil || e.TMS > best.TMS) {
			best = e
		}
	}
	if best != nil {
		return best
	}
	if latest != nil && latest.BotID == "" {
		return latest // a panel-wide event such as a backup
	}
	return nil
}

func humanDur(ms int64) string {
	if ms < 0 {
		return "just"
	}
	s := ms / 1000
	if s < 90 {
		return fmt.Sprintf("%d s", s)
	}
	return fmt.Sprintf("%d min", (s+30)/60)
}

// ---------------------------------------------------------------- top_processes

func (s *AIService) toolTopProcesses(ctx context.Context, actor domain.User, a agentArgs) (any, error) {
	limit := int(a.int("limit"))
	if limit <= 0 {
		limit = 15
	}
	limit = min(limit, 40)
	out := map[string]any{"now": iso(s.now())}
	var snap domain.ProcessSnapshot
	if ref := a.str("bot_id"); ref != "" {
		b, err := s.metricsBot(ctx, actor, ref)
		if err != nil {
			return nil, err
		}
		snap, err = s.Metrics.BotProcesses(ctx, b.ID)
		if errors.Is(err, metrics.ErrNotRunning) {
			return map[string]any{"app": b.Name, "bot_id": b.ID, "state": b.ObservedState, "note": "the app is not running, so it has no processes"}, nil
		}
		if err != nil {
			return nil, err
		}
		out["scope"], out["app"], out["bot_id"] = "app", b.Name, b.ID
	} else {
		node, err := s.hostNode(actor, a)
		if err != nil {
			return nil, err
		}
		snap, err = s.Metrics.Processes(ctx, node, limit)
		if errors.Is(err, metrics.ErrNoProcesses) {
			return map[string]any{"node_id": node, "note": "this node cannot list its processes (no host /proc access and no agent process endpoint)"}, nil
		}
		if err != nil {
			return nil, err
		}
		out["scope"], out["node_id"] = "host", node
	}
	out["sampled_at"], out["source"], out["host_cpu_pct"] = iso(snap.SampledAtMS), snap.Source, r1(snap.HostCPUPct)
	out["processes"] = procViews(snap.Items, limit, 120)
	if snap.Source == domain.ProcSourceDockerTop {
		out["note"] = "host processes are not visible to the panel; only processes inside app containers are listed"
	}
	return out, nil
}

// ---------------------------------------------------------------- cpu_by_app

func (s *AIService) toolCPUByApp(ctx context.Context, actor domain.User, a agentArgs) (any, error) {
	node, err := s.hostNode(actor, a)
	if err != nil {
		return nil, err
	}
	start, end, err := s.timeRange(a, time.Hour)
	if err != nil {
		return nil, err
	}
	metric := a.str("metric")
	if metric == "" {
		metric = "cpu"
	}
	top := int(a.int("top"))
	if top <= 0 {
		top = 5
	}
	top = min(top, 6)
	r, err := s.Metrics.BotsSeries(ctx, node, start, end, stepFor(start, end, chartPoints), top, metric)
	if err != nil {
		return nil, err
	}
	unit := "cores"
	if metric == "mem" {
		unit = "bytes"
	}
	type line struct {
		Label  string     `json:"label"`
		BotID  string     `json:"bot_id,omitempty"`
		Avg    *float64   `json:"avg"`
		Max    *float64   `json:"max"`
		Values []*float64 `json:"values"`
	}
	cols := [][]float64{}
	labels, ids := []string{}, []string{}
	for _, b := range r.Bots {
		cols, labels, ids = append(cols, b.Values), append(labels, b.Name), append(ids, b.BotID)
	}
	cols, labels, ids = append(cols, r.Other), append(labels, "Other apps"), append(ids, "")
	cols, labels, ids = append(cols, r.Unattributed), append(labels, "Host / unattributed"), append(ids, "")
	times, ds := downsample(r.Times, cols, chartPoints)
	lines := []line{}
	for i, c := range ds {
		st := summarize(r.Times, cols[i], unit)
		if st.Samples == 0 && labels[i] == "Other apps" {
			continue
		}
		lines = append(lines, line{Label: labels[i], BotID: ids[i], Avg: st.Avg, Max: st.Max, Values: chartCol(c)})
	}
	out := map[string]any{"node_id": node, "start": iso(start), "end": iso(end), "metric": metric, "unit": unit, "source": r.Source, "times": times, "series": lines,
		"hint": "draw as one TimeSeries with stacked: true on every series"}
	if r.Source == "rollups" {
		out["note"] = "older than 48 hours: per-app values are 5 min, hourly or daily averages"
	}
	return out, nil
}
