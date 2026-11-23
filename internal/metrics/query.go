package metrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Range limits.
const (
	DefaultPoints = 600
	MaxPoints     = 5000
	MaxWindow     = 400 * 24 * time.Hour
	// ContainerWindow is how far back container samples reach; older ranges
	// are answered from the usage rollups.
	ContainerWindow = ContainerRetention
	DefaultTop      = 8
	MaxTop          = 20
	// rollupAfter: windows longer than this with an hourly or coarser step
	// are read from node_usage rollups instead of raw samples.
	rollupAfter = 8 * 24 * time.Hour
)

// Col is a column of a series. NaN marks a gap and is encoded as null.
type Col []float64

// MarshalJSON writes numbers with at most three decimals and null for NaN.
func (c Col) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(c)*8+2)
	b = append(b, '[')
	for i, v := range c {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b = append(b, "null"...)
			continue
		}
		b = strconv.AppendFloat(b, math.Round(v*1000)/1000, 'f', -1, 64)
	}
	return append(b, ']'), nil
}

func newCol(n int) Col {
	c := make(Col, n)
	for i := range c {
		c[i] = math.NaN()
	}
	return c
}

// Grid is the time axis of a series: bucket start times stepMS apart.
type Grid struct {
	Start, End, Step int64
	Times            []int64
}

func (g Grid) index(t int64) int {
	if len(g.Times) == 0 || t < g.Times[0] {
		return -1
	}
	i := int((t - g.Times[0]) / g.Step)
	if i >= len(g.Times) {
		return -1
	}
	return i
}

// MakeGrid validates a range and picks its step: step <= 0 means
// max(raw, (end-start)/points). Buckets start at multiples of the step;
// with since > 0 only buckets starting after since are included.
func MakeGrid(start, end, step, since int64, points int, raw time.Duration) (Grid, error) {
	if end <= start {
		return Grid{}, domain.Invalid("end must be after start")
	}
	if end-start > MaxWindow.Milliseconds() {
		return Grid{}, domain.Invalid("the range can be at most 400 days")
	}
	if points <= 0 {
		points = DefaultPoints
	}
	points = min(points, MaxPoints)
	if raw <= 0 {
		raw = 30 * time.Second
	}
	if step <= 0 {
		step = (end - start + int64(points) - 1) / int64(points)
	}
	step = max(step, raw.Milliseconds(), (end-start+MaxPoints-1)/MaxPoints)
	// Whole seconds keep bucket boundaries readable.
	if step%1000 != 0 {
		step += 1000 - step%1000
	}
	first := start - ((start%step)+step)%step
	if since > 0 {
		next := since - ((since%step)+step)%step + step
		first = max(first, next)
	}
	g := Grid{Start: start, End: end, Step: step}
	for t := first; t < end; t += step {
		g.Times = append(g.Times, t)
	}
	if g.Times == nil {
		g.Times = []int64{}
	}
	return g, nil
}

// SeriesResult is a node's host series (uPlot columns: one time axis and
// parallel value arrays, null for gaps). CPU is percent of the whole host,
// memory and disk use bytes, network and disk I/O bytes per second.
type SeriesResult struct {
	NodeID    string  `json:"node_id"`
	StartMS   int64   `json:"start_ms"`
	EndMS     int64   `json:"end_ms"`
	StepMS    int64   `json:"step_ms"`
	Times     []int64 `json:"times"`
	CPU       Col     `json:"cpu"`
	CPUMax    Col     `json:"cpu_max"`
	Mem       Col     `json:"mem"`
	MemMax    Col     `json:"mem_max"`
	MemTotal  int64   `json:"mem_total"`
	Load1     Col     `json:"load1"`
	NetRx     Col     `json:"net_rx"`
	NetTx     Col     `json:"net_tx"`
	DiskRead  Col     `json:"disk_read"`
	DiskWrite Col     `json:"disk_write"`
	DiskUsed  Col     `json:"disk_used"`
	DiskTotal int64   `json:"disk_total"`
	Swap      Col     `json:"swap"`
	CPUs      int     `json:"cpus"`
}

func (m *Metrics) raw() time.Duration {
	if m.RawInterval > 0 {
		return m.RawInterval
	}
	return 30 * time.Second
}

// SeriesQuery is a host or app series request. Step <= 0 picks one from
// Points (default 600); Since > 0 returns only buckets starting after it.
type SeriesQuery struct {
	Start, End, Step, Since int64
	Points                  int
}

// Series returns a node's host series between start and end (Unix ms).
func (m *Metrics) Series(ctx context.Context, node string, start, end, step int64) (SeriesResult, error) {
	return m.SeriesFor(ctx, node, SeriesQuery{Start: start, End: end, Step: step})
}

// SeriesFor is Series with every option.
func (m *Metrics) SeriesFor(ctx context.Context, node string, q SeriesQuery) (SeriesResult, error) {
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return SeriesResult{}, err
	}
	g, err := MakeGrid(q.Start, q.End, q.Step, q.Since, q.Points, m.raw())
	if err != nil {
		return SeriesResult{}, err
	}
	return m.hostSeries(ctx, node, g)
}

func (m *Metrics) hostSeries(ctx context.Context, node string, g Grid) (SeriesResult, error) {
	n := len(g.Times)
	r := SeriesResult{NodeID: node, StartMS: g.Start, EndMS: g.End, StepMS: g.Step, Times: g.Times,
		CPU: newCol(n), CPUMax: newCol(n), Mem: newCol(n), MemMax: newCol(n), Load1: newCol(n), NetRx: newCol(n),
		NetTx: newCol(n), DiskRead: newCol(n), DiskWrite: newCol(n), DiskUsed: newCol(n), Swap: newCol(n)}
	if node == m.LocalNode {
		r.CPUs = m.cpus()
	}
	if n == 0 {
		return r, nil
	}
	from, to := g.Times[0], g.Times[n-1]+g.Step
	if to-from > rollupAfter.Milliseconds() && g.Step >= time.Hour.Milliseconds() {
		res := int64(3600)
		if g.Step >= 24*time.Hour.Milliseconds() {
			res = 86400
		}
		rows, err := m.Store.ListNodeUsageRange(ctx, node, res, from, to)
		if err != nil {
			return r, err
		}
		cnt := make([]float64, n)
		for _, b := range rows {
			i := g.index(b.BucketMS)
			if i < 0 || b.Samples <= 0 {
				continue
			}
			s := float64(b.Samples)
			add := func(c Col, v float64) {
				if math.IsNaN(c[i]) {
					c[i] = 0
				}
				c[i] += v
			}
			add(r.CPU, b.CPUSum/s)
			add(r.Mem, float64(b.MemSum)/s)
			add(r.NetRx, float64(b.NetRxSum)/s)
			add(r.NetTx, float64(b.NetTxSum)/s)
			add(r.DiskUsed, float64(b.DiskUsed))
			if math.IsNaN(r.CPUMax[i]) || b.CPUMax > r.CPUMax[i] {
				r.CPUMax[i] = b.CPUMax
			}
			if math.IsNaN(r.MemMax[i]) || float64(b.MemMax) > r.MemMax[i] {
				r.MemMax[i] = float64(b.MemMax)
			}
			cnt[i]++
			r.MemTotal = max(r.MemTotal, b.MemTotal)
			r.DiskTotal = max(r.DiskTotal, b.DiskTotal)
		}
		for i, c := range cnt {
			if c > 1 {
				for _, col := range []Col{r.CPU, r.Mem, r.NetRx, r.NetTx, r.DiskUsed} {
					col[i] /= c
				}
			}
		}
		return r, nil
	}
	rows, err := m.Store.ListTelemetryRange(ctx, node, from, to, g.Step)
	if err != nil {
		return r, err
	}
	for _, b := range rows {
		i := g.index(b.SampledAtMS)
		if i < 0 {
			continue
		}
		r.CPU[i], r.CPUMax[i] = b.CPUPercent, b.CPUMax
		r.Mem[i], r.MemMax[i] = float64(b.MemoryUsedBytes), float64(b.MemoryMax)
		r.Load1[i] = b.Load1
		r.NetRx[i], r.NetTx[i] = float64(b.NetRxBps), float64(b.NetTxBps)
		r.DiskRead[i], r.DiskWrite[i] = float64(b.DiskReadBps), float64(b.DiskWriteBps)
		r.DiskUsed[i], r.Swap[i] = float64(b.DiskUsedBytes), float64(b.SwapUsedBytes)
		r.MemTotal = max(r.MemTotal, b.MemoryTotalBytes)
		r.DiskTotal = max(r.DiskTotal, b.DiskTotalBytes)
		r.CPUs = max(r.CPUs, b.LogicalCPUs)
	}
	return r, nil
}

// BotLine is one app's values in a node's per-app series.
type BotLine struct {
	BotID  string `json:"bot_id"`
	Name   string `json:"name"`
	Values Col    `json:"values"`
}

// BotsSeriesResult splits a node's CPU (cores) or memory (bytes) by app:
// the top apps, the rest summed in Other, and Unattributed for what the
// host used beyond all apps (host processes, the panel, the system).
type BotsSeriesResult struct {
	Metric       string    `json:"metric"`
	StepMS       int64     `json:"step_ms"`
	Source       string    `json:"source"` // samples | rollups
	Times        []int64   `json:"times"`
	Bots         []BotLine `json:"bots"`
	Other        Col       `json:"other"`
	Unattributed Col       `json:"unattributed"`
}

// usageRes picks the bot_usage resolution for a range starting at start
// with buckets of step.
func usageRes(now time.Time, start, step int64) int64 {
	age := now.UnixMilli() - start
	switch {
	case age <= (3*24*time.Hour).Milliseconds() && step < time.Hour.Milliseconds():
		return domain.UsageRes5m
	case age <= (35*24*time.Hour).Milliseconds() && step < 24*time.Hour.Milliseconds():
		return domain.UsageRes1h
	default:
		return domain.UsageRes1d
	}
}

// appBuckets reads per-app buckets for a grid: container samples within
// the last 48 hours, the usage rollups before. The grid's step may grow to
// the rollup resolution, so the grid is returned too.
func (m *Metrics) appBuckets(ctx context.Context, node, bot string, q SeriesQuery) (Grid, []domain.ContainerBucket, string, error) {
	g, err := MakeGrid(q.Start, q.End, q.Step, q.Since, q.Points, ContainerInterval)
	if err != nil {
		return g, nil, "", err
	}
	now := m.now()
	if q.Start >= now.Add(-ContainerWindow).UnixMilli() {
		if len(g.Times) == 0 {
			return g, nil, "samples", nil
		}
		rows, err := m.Store.ListContainerBuckets(ctx, node, bot, g.Times[0], g.Times[len(g.Times)-1]+g.Step, g.Step)
		return g, rows, "samples", err
	}
	res := usageRes(now, q.Start, g.Step)
	if g.Step < res*1000 {
		g, err = MakeGrid(q.Start, q.End, res*1000, q.Since, q.Points, ContainerInterval)
		if err != nil {
			return g, nil, "", err
		}
	}
	if len(g.Times) == 0 {
		return g, nil, "rollups", nil
	}
	rows, err := m.Store.UsageBuckets(ctx, node, bot, res, g.Times[0], g.Times[len(g.Times)-1]+g.Step)
	return g, rows, "rollups", err
}

// BotsSeries returns a node's per-app CPU ("cpu", cores) or memory ("mem",
// bytes) between start and end.
func (m *Metrics) BotsSeries(ctx context.Context, node string, start, end, step int64, top int, metric string) (BotsSeriesResult, error) {
	return m.BotsSeriesFor(ctx, node, SeriesQuery{Start: start, End: end, Step: step}, top, metric)
}

// BotsSeriesFor is BotsSeries with every option.
func (m *Metrics) BotsSeriesFor(ctx context.Context, node string, q SeriesQuery, top int, metric string) (BotsSeriesResult, error) {
	if metric == "" {
		metric = "cpu"
	}
	if metric != "cpu" && metric != "mem" {
		return BotsSeriesResult{}, domain.Invalid("metric must be cpu or mem")
	}
	if top <= 0 {
		top = DefaultTop
	}
	top = min(top, MaxTop)
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return BotsSeriesResult{}, err
	}
	g, rows, source, err := m.appBuckets(ctx, node, "", q)
	if err != nil {
		return BotsSeriesResult{}, err
	}
	n := len(g.Times)
	out := BotsSeriesResult{Metric: metric, StepMS: g.Step, Source: source, Times: g.Times, Bots: []BotLine{},
		Other: newCol(n), Unattributed: newCol(n)}
	if n == 0 {
		return out, nil
	}
	host, err := m.hostSeries(ctx, node, g)
	if err != nil {
		return out, err
	}
	cols := map[string]Col{}
	totals := map[string]float64{}
	sum := newCol(n)
	for _, b := range rows {
		i := g.index(b.BucketMS)
		if i < 0 {
			continue
		}
		v := b.CPUCores
		if metric == "mem" {
			v = b.MemoryBytes
		}
		c := cols[b.BotID]
		if c == nil {
			c = newCol(n)
			cols[b.BotID] = c
		}
		c[i] = v
		totals[b.BotID] += v
		if math.IsNaN(sum[i]) {
			sum[i] = 0
		}
		sum[i] += v
	}
	ids := make([]string, 0, len(cols))
	for id := range cols {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if totals[ids[i]] != totals[ids[j]] {
			return totals[ids[i]] > totals[ids[j]]
		}
		return ids[i] < ids[j]
	})
	names := m.botNames(ctx)
	for k, id := range ids {
		if k < top {
			out.Bots = append(out.Bots, BotLine{BotID: id, Name: nameOr(names, id), Values: cols[id]})
			continue
		}
		for i, v := range cols[id] {
			if math.IsNaN(v) {
				continue
			}
			if math.IsNaN(out.Other[i]) {
				out.Other[i] = 0
			}
			out.Other[i] += v
		}
	}
	cpus := float64(max(host.CPUs, 1))
	// Host samples come every 30-60 s while app samples come every 15 s, so
	// at a fine step every other bucket has no host value. Carry the last
	// host value forward for up to a minute so the host band stays a line.
	const carry = int64(60_000)
	lastH, lastT := math.NaN(), int64(0)
	for i := range g.Times {
		var h float64
		if metric == "cpu" {
			h = host.CPU[i] / 100 * cpus
		} else {
			h = host.Mem[i]
		}
		if !math.IsNaN(h) {
			lastH, lastT = h, g.Times[i]
		} else if !math.IsNaN(lastH) && g.Times[i]-lastT <= carry {
			h = lastH
		}
		if math.IsNaN(h) {
			continue
		}
		s := sum[i]
		if math.IsNaN(s) {
			s = 0
		}
		out.Unattributed[i] = max(h-s, 0)
	}
	return out, nil
}

func (m *Metrics) botNames(ctx context.Context) map[string]string {
	subs, err := m.Store.ListMetricSubjects(ctx)
	out := map[string]string{}
	if err != nil {
		return out
	}
	for _, s := range subs {
		out[s.ID] = s.Name
	}
	return out
}

func nameOr(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	return "(deleted app)"
}

// BotSeriesResult is one app's series: CPU in cores, memory in bytes,
// network and block I/O in bytes per second, process count.
type BotSeriesResult struct {
	BotID    string  `json:"bot_id"`
	StepMS   int64   `json:"step_ms"`
	Source   string  `json:"source"`
	Times    []int64 `json:"times"`
	CPU      Col     `json:"cpu"`
	Mem      Col     `json:"mem"`
	NetRx    Col     `json:"net_rx"`
	NetTx    Col     `json:"net_tx"`
	BlkRead  Col     `json:"blk_read"`
	BlkWrite Col     `json:"blk_write"`
	PIDs     Col     `json:"pids"`
	MemLimit int64   `json:"mem_limit"`
	CPULimit float64 `json:"cpu_limit"`
}

// BotSeries returns one app's series between start and end. The caller has
// authorized access to the app.
func (m *Metrics) BotSeries(ctx context.Context, bot string, start, end, step int64) (BotSeriesResult, error) {
	return m.BotSeriesFor(ctx, bot, SeriesQuery{Start: start, End: end, Step: step})
}

// BotSeriesFor is BotSeries with every option.
func (m *Metrics) BotSeriesFor(ctx context.Context, bot string, q SeriesQuery) (BotSeriesResult, error) {
	b, err := m.Store.GetBot(ctx, bot)
	if err != nil {
		return BotSeriesResult{}, err
	}
	g, rows, source, err := m.appBuckets(ctx, b.NodeID, b.ID, q)
	if err != nil {
		return BotSeriesResult{}, err
	}
	n := len(g.Times)
	out := BotSeriesResult{BotID: b.ID, StepMS: g.Step, Source: source, Times: g.Times, CPU: newCol(n), Mem: newCol(n),
		NetRx: newCol(n), NetTx: newCol(n), BlkRead: newCol(n), BlkWrite: newCol(n), PIDs: newCol(n),
		MemLimit: b.MemoryBytes, CPULimit: float64(b.NanoCPUs) / 1e9}
	for _, r := range rows {
		i := g.index(r.BucketMS)
		if i < 0 {
			continue
		}
		out.CPU[i], out.Mem[i], out.NetRx[i], out.NetTx[i] = r.CPUCores, r.MemoryBytes, r.NetRxBps, r.NetTxBps
		if source == "samples" {
			out.BlkRead[i], out.BlkWrite[i], out.PIDs[i] = r.BlkReadBps, r.BlkWriteBps, r.PIDs
		}
	}
	return out, nil
}

// Events returns chart annotations for a node between start and end.
func (m *Metrics) Events(ctx context.Context, node string, start, end int64) ([]domain.MetricEvent, error) {
	if end < start {
		return nil, domain.Invalid("end must be after start")
	}
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return nil, err
	}
	ev, err := m.Store.ListMetricEvents(ctx, node, "", start, end, 2000)
	if ev == nil {
		ev = []domain.MetricEvent{}
	}
	return ev, err
}

// SpikesResult lists a node's CPU spikes with the threshold that opens one
// (RIVET_SPIKE_CPU_PCT), so a chart can draw it.
type SpikesResult struct {
	ThresholdPct float64            `json:"threshold_pct"`
	Spikes       []domain.LoadSpike `json:"spikes"`
}

// Spikes returns a node's CPU spikes overlapping start..end.
func (m *Metrics) Spikes(ctx context.Context, node string, start, end int64) (SpikesResult, error) {
	out := SpikesResult{ThresholdPct: m.threshold(), Spikes: []domain.LoadSpike{}}
	if end < start {
		return out, domain.Invalid("end must be after start")
	}
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return out, err
	}
	sp, err := m.Store.ListSpikes(ctx, node, start, end)
	if sp != nil {
		out.Spikes = sp
	}
	return out, err
}

// AtHost is the host's mean use around a moment.
type AtHost struct {
	CPU   float64 `json:"cpu"`
	Mem   int64   `json:"mem"`
	Load1 float64 `json:"load1"`
}

// AtBot is one app's mean use around a moment and its share of the CPU the
// host used.
type AtBot struct {
	BotID    string  `json:"bot_id"`
	Name     string  `json:"name"`
	CPUCores float64 `json:"cpu_cores"`
	MemBytes int64   `json:"mem_bytes"`
	SharePct float64 `json:"share_pct"`
}

// AtResult answers "what was happening around t".
type AtResult struct {
	TMS       int64                   `json:"t_ms"`
	WindowMS  int64                   `json:"window_ms"`
	Host      *AtHost                 `json:"host"`
	Bots      []AtBot                 `json:"bots"`
	Processes *domain.ProcessSnapshot `json:"processes"`
	Events    []domain.MetricEvent    `json:"events"`
	Spike     *domain.LoadSpike       `json:"spike"`
}

// At reports the host, the apps (busiest first), the nearest process
// snapshot, events within 10 minutes and the spike covering t on a node.
// window (default 60 s) is the span averaged around t.
func (m *Metrics) At(ctx context.Context, node string, t, window int64) (AtResult, error) {
	if window <= 0 {
		window = time.Minute.Milliseconds()
	}
	window = min(max(window, 5000), (6 * time.Hour).Milliseconds())
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return AtResult{}, err
	}
	out := AtResult{TMS: t, WindowMS: window, Bots: []AtBot{}, Events: []domain.MetricEvent{}}
	from, to := t-window/2, t+window/2

	// Host: the fast ring when it covers t (local node), stored samples
	// otherwise (widened to two raw intervals so a short window finds one).
	if node == m.LocalNode && m.Fast != nil {
		var sum, load float64
		var mem int64
		k := 0
		for _, p := range m.Fast.Recent(from - 1) {
			if p.T > to {
				break
			}
			sum, mem, load = sum+p.CPU, mem+p.Mem, load+p.Load1
			k++
		}
		if k > 0 {
			out.Host = &AtHost{CPU: round2(sum / float64(k)), Mem: mem / int64(k), Load1: round2(load / float64(k))}
		}
	}
	if out.Host == nil {
		half := max(window/2, m.raw().Milliseconds())
		rows, err := m.Store.ListTelemetryRange(ctx, node, t-half, t+half, 2*half+1)
		if err != nil {
			return out, err
		}
		var sum, load, mem, w float64
		for _, b := range rows {
			s := float64(max(b.Samples, 1))
			sum, load, mem, w = sum+b.CPUPercent*s, load+b.Load1*s, mem+float64(b.MemoryUsedBytes)*s, w+s
		}
		if w > 0 {
			out.Host = &AtHost{CPU: round2(sum / w), Mem: int64(mem / w), Load1: round2(load / w)}
		}
	}

	// Apps.
	var rows []domain.ContainerBucket
	var err error
	if t >= m.now().Add(-ContainerWindow).UnixMilli() {
		f, e := from, to
		if window < 2*ContainerInterval.Milliseconds() {
			f, e = t-ContainerInterval.Milliseconds(), t+ContainerInterval.Milliseconds()
		}
		rows, err = m.Store.ContainerAverages(ctx, node, f, e)
	} else {
		res := usageRes(m.now(), t, 0)
		rows, err = m.Store.UsageBuckets(ctx, node, "", res, t-res*1000+1, t+1)
	}
	if err != nil {
		return out, err
	}
	names := m.botNames(ctx)
	cpus := m.cpus()
	if node != m.LocalNode {
		cpus = 1
		if hs, err := m.Store.ListTelemetryRange(ctx, node, t-time.Hour.Milliseconds(), t+time.Hour.Milliseconds(), 2*time.Hour.Milliseconds()+1); err == nil {
			for _, b := range hs {
				cpus = max(cpus, b.LogicalCPUs)
			}
		}
	}
	used := 0.0
	if out.Host != nil {
		used = out.Host.CPU / 100 * float64(cpus)
	}
	for _, r := range rows {
		b := AtBot{BotID: r.BotID, Name: nameOr(names, r.BotID), CPUCores: round2(r.CPUCores), MemBytes: int64(r.MemoryBytes)}
		if used > 0 {
			b.SharePct = round2(min(r.CPUCores/used*100, 100))
		}
		out.Bots = append(out.Bots, b)
	}
	sort.SliceStable(out.Bots, func(i, j int) bool { return out.Bots[i].CPUCores > out.Bots[j].CPUCores })

	// Processes: the snapshot nearest t (periodic ones are a minute apart).
	if snap, ok, err := m.Store.NearestProcessSnapshot(ctx, node, t, max(window, 2*time.Minute.Milliseconds())); err != nil {
		return out, err
	} else if ok {
		out.Processes = &snap
	}

	ev, err := m.Store.ListMetricEvents(ctx, node, "", t-10*time.Minute.Milliseconds(), t+10*time.Minute.Milliseconds(), 200)
	if err != nil {
		return out, err
	}
	if ev != nil {
		out.Events = ev
	}
	spikes, err := m.Store.ListSpikes(ctx, node, t, t)
	if err != nil {
		return out, err
	}
	if len(spikes) > 0 {
		sp := spikes[len(spikes)-1]
		out.Spike = &sp
	}
	return out, nil
}

// Processes returns a node's busiest processes now (at most limit, default
// 25). Local lists are shared for a few seconds between callers.
func (m *Metrics) Processes(ctx context.Context, node string, limit int) (domain.ProcessSnapshot, error) {
	if limit <= 0 {
		limit = PeriodicTop
	}
	limit = min(limit, 100)
	if _, err := m.Store.GetNode(ctx, node); err != nil {
		return domain.ProcessSnapshot{}, err
	}
	if node != m.LocalNode {
		return m.remoteProcesses(ctx, node, limit, "")
	}
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	if !m.live.at.IsZero() && m.now().Sub(m.live.at) < liveCacheTTL && len(m.live.snap.Items) >= min(limit, len(m.live.snap.Items)) {
		s := m.live.snap
		if len(s.Items) > limit {
			s.Items = s.Items[:limit]
		}
		return s, nil
	}
	s, err := m.LocalSnapshot(ctx, 100, "")
	if err != nil {
		return s, err
	}
	m.live.at, m.live.snap = m.now(), s
	if len(s.Items) > limit {
		s.Items = s.Items[:limit]
	}
	return s, nil
}

func (m *Metrics) remoteProcesses(ctx context.Context, node string, limit int, containerID string) (domain.ProcessSnapshot, error) {
	if m.Remote == nil || m.Remote.Processes == nil {
		return domain.ProcessSnapshot{}, ErrNoProcesses
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, err := m.Remote.Processes(rctx, node, limit, containerID)
	if err != nil {
		return s, err
	}
	subs, _ := m.Store.ListMetricSubjects(ctx)
	s.NodeID = node
	if s.Items == nil {
		s.Items = []domain.ProcessEntry{}
	}
	s.Items = m.attribute(ctx, s.Items, subs)
	return s, nil
}

// ErrNotRunning refuses a process list of an app that is not running (409).
var ErrNotRunning = fmt.Errorf("%w: the app is not running, so it has no processes", domain.ErrConflict)

// BotProcesses returns the processes inside one app's container. The caller
// has authorized access to the app.
func (m *Metrics) BotProcesses(ctx context.Context, bot string) (domain.ProcessSnapshot, error) {
	b, err := m.Store.GetBot(ctx, bot)
	if err != nil {
		return domain.ProcessSnapshot{}, err
	}
	if b.ContainerID == nil || *b.ContainerID == "" || b.ObservedState != "running" {
		return domain.ProcessSnapshot{}, ErrNotRunning
	}
	if b.NodeID != m.LocalNode {
		return m.remoteProcesses(ctx, b.NodeID, remoteProcsLimit, *b.ContainerID)
	}
	return m.LocalSnapshot(ctx, remoteProcsLimit, *b.ContainerID)
}
