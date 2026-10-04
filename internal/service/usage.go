package service

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Usage analytics: per-bot resource use, uptime, crashes, starts,
// deployments and backups over time, and a panel-wide overview. Built from
// data the panel already has: Docker (or agent) stats streams and the bot
// rows for the collector, the operations and backups tables for deployment
// and backup counts, node_telemetry for nodes, accounts and tickets.
//
// Storage tiers (bot_usage / node_usage, migration 0052):
//   - raw: one collector pass a minute, kept in memory only;
//   - 5-minute rows: written once per bucket in one transaction, 3 days;
//   - hourly rows: recomputed from 5-minute rows (and operations) every
//     5 minutes for the current and previous hour, 35 days;
//   - daily rows (UTC): recomputed from hourly rows, 400 days.
//
// Node rows are hourly/daily rollups of node_telemetry with the same
// retention, so node trends outlive the raw samples.
const (
	UsageSampleInterval = time.Minute

	usageRetention5m = 3 * 24 * time.Hour
	usageRetention1h = 35 * 24 * time.Hour
	usageRetention1d = 400 * 24 * time.Hour

	usageMaxMeasured   = 100 // running bots measured per pass (rotating)
	usageWorkers       = 4
	usageStatsDeadline = 5 * time.Second
	usageDiskEvery     = 30 * time.Minute
	usageDiskPerPass   = 10
	usagePruneBatch    = 1000
	usageTopLimit      = 10

	usageMarkBot  = "bot_hour"
	usageMarkNode = "node_hour"
)

// UsageStore persists usage analytics.
type UsageStore interface {
	ListUsageSubjects(ctx context.Context) ([]domain.UsageSubject, error)
	AddUsage(ctx context.Context, rows []domain.UsageBucket) error
	UsageMark(ctx context.Context, name string) (int64, bool, error)
	SetUsageMark(ctx context.Context, name string, v int64) error
	RollupBotHours(ctx context.Context, fromMS int64) error
	RollupBotEvents(ctx context.Context, fromMS int64) error
	RollupBotDays(ctx context.Context, fromMS int64) error
	RollupNodeHours(ctx context.Context, fromMS int64) error
	RollupNodeDays(ctx context.Context, fromMS int64) error
	PruneUsage(ctx context.Context, res, beforeMS int64, batch int) (int64, error)
	PruneNodeUsage(ctx context.Context, res, beforeMS int64, batch int) (int64, error)
	ListBotUsage(ctx context.Context, botID string, res, fromMS int64) ([]domain.UsageBucket, error)
	AggregateUsage(ctx context.Context, res, fromMS int64, f domain.UsageFilter) ([]domain.UsageAggregate, error)
	TopUsage(ctx context.Context, res, fromMS int64, f domain.UsageFilter, by string, limit int) ([]domain.UsageConsumer, error)
	UsageBotCounts(ctx context.Context, f domain.UsageFilter) (map[string][2]int64, error)
	ListNodeUsage(ctx context.Context, res, fromMS int64) ([]domain.NodeUsageBucket, error)
	ListTicketStats(ctx context.Context, fromMS int64) ([]domain.TicketStat, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	ListNodes(ctx context.Context) ([]domain.Node, error)
	ListLocations(ctx context.Context) ([]domain.Location, map[string]int, error)
}

// UsageStats reads a container's resource use (Docker locally, the agent
// for remote nodes).
type UsageStats interface {
	StreamStats(ctx context.Context, containerID string, fn func(domain.ResourceSample) bool) error
}

// UsageService collects, rolls up and reports usage analytics.
type UsageService struct {
	Store UsageStore
	Bots  *BotService // per-bot authorization
	// StatsFor returns the stats source for a node, or nil when none is
	// available (no local runner, node offline).
	StatsFor func(nodeID string) UsageStats
	// WorkspaceUsage measures a local bot's workspace (nil = disk is not
	// sampled). Remote bots' workspaces are not measured.
	WorkspaceUsage func(botID string) (int64, error)
	LocalNode      string
	Log            *slog.Logger
	Now            func() time.Time
	// Retention returns the analytics retention chosen in the panel; nil
	// (or a zero value) keeps 3 days of 5-minute, 35 days of hourly and
	// 400 days of daily rows.
	Retention func() MetricsRetention

	mu     sync.Mutex
	bucket int64
	acc    map[string]*usageAcc
	prev   map[string]usagePrev
	disks  map[string]usageDisk
	offset int
}

type usageAcc struct {
	samples, wanted, up, measured int64
	cpuSum, cpuMax                float64
	memSum, memMax, memLimit      int64
	rx, tx                        int64
	disk                          *int64
	crashes, starts               int64
}

type usagePrev struct {
	restart   int64
	started   int64 // 0 = never started
	container string
	rx, tx    int64
	hasNet    bool
}

type usageDisk struct {
	at    time.Time
	bytes int64
	ok    bool
}

func (s *UsageService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func bucketOf(ms, res int64) int64 { return ms - ((ms%(res*1000))+res*1000)%(res*1000) }

// Collect runs one collector pass: it reads every bot's state, measures up
// to usageMaxMeasured running bots, and adds the result to the current
// 5-minute bucket. When the bucket changed since the last pass, the closed
// bucket is written first (one transaction). It reports whether a bucket
// was written.
func (s *UsageService) Collect(ctx context.Context) (bool, error) {
	now := s.now()
	bucket := bucketOf(now.UnixMilli(), domain.UsageRes5m)
	flushed := false
	s.mu.Lock()
	var rows []domain.UsageBucket
	if s.bucket != 0 && s.bucket != bucket {
		rows = s.drainLocked()
	}
	s.bucket = bucket
	s.mu.Unlock()
	if len(rows) > 0 {
		if err := s.Store.AddUsage(ctx, rows); err != nil {
			return false, err
		}
		flushed = true
	}

	subs, err := s.Store.ListUsageSubjects(ctx)
	if err != nil {
		return flushed, err
	}
	samples, disks := s.measure(ctx, subs, now)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acc == nil {
		s.acc, s.prev, s.disks = map[string]*usageAcc{}, map[string]usagePrev{}, map[string]usageDisk{}
	}
	for id, d := range disks {
		s.disks[id] = d
	}
	seen := make(map[string]bool, len(subs))
	for _, sub := range subs {
		seen[sub.ID] = true
		a := s.acc[sub.ID]
		if a == nil {
			a = &usageAcc{}
			s.acc[sub.ID] = a
		}
		a.samples++
		if sub.DesiredState == domain.DesiredRunning {
			a.wanted++
			if sub.ObservedState == "running" {
				a.up++
			}
		}
		started := int64(0)
		if sub.LastStartedAtMS != nil {
			started = *sub.LastStartedAtMS
		}
		p, had := s.prev[sub.ID]
		if had {
			switch {
			case sub.RestartCount > p.restart:
				a.crashes += sub.RestartCount - p.restart
			case sub.RestartCount < p.restart:
				// A new generation reset the counter; what it shows now are
				// crashes since then.
				a.crashes += sub.RestartCount
			}
			if started != 0 && started != p.started {
				a.starts++
			}
		}
		np := usagePrev{restart: sub.RestartCount, started: started, container: p.container, rx: p.rx, tx: p.tx, hasNet: p.hasNet}
		if m, ok := samples[sub.ID]; ok && sub.ContainerID != nil {
			a.measured++
			a.cpuSum += max(m.CPUCores, 0)
			a.cpuMax = max(a.cpuMax, m.CPUCores)
			a.memSum += max(m.MemUsedBytes, 0)
			a.memMax = max(a.memMax, m.MemUsedBytes)
			limit := m.MemLimitBytes
			if limit <= 0 {
				limit = sub.MemoryBytes
			}
			a.memLimit = max(a.memLimit, limit)
			if p.hasNet {
				rx, tx := m.NetRxBytes, m.NetTxBytes
				if p.container == *sub.ContainerID {
					// Counters only grow within one container; a smaller value
					// means they were reset, so count what is there now.
					if rx >= p.rx {
						rx -= p.rx
					}
					if tx >= p.tx {
						tx -= p.tx
					}
				}
				a.rx += max(rx, 0)
				a.tx += max(tx, 0)
			}
			np.container, np.rx, np.tx, np.hasNet = *sub.ContainerID, m.NetRxBytes, m.NetTxBytes, true
		}
		if d, ok := s.disks[sub.ID]; ok && d.ok {
			v := d.bytes
			if a.disk == nil || v > *a.disk {
				a.disk = &v
			}
		}
		s.prev[sub.ID] = np
	}
	for id := range s.prev {
		if !seen[id] {
			delete(s.prev, id)
			delete(s.disks, id)
		}
	}
	return flushed, nil
}

// measure reads resource samples of running bots (bounded, rotating through
// all of them across passes) and refreshes due workspace sizes.
func (s *UsageService) measure(ctx context.Context, subs []domain.UsageSubject, now time.Time) (map[string]domain.ResourceSample, map[string]usageDisk) {
	type job struct {
		sub  domain.UsageSubject
		src  UsageStats
		disk bool
	}
	var running []job
	for _, sub := range subs {
		if sub.ObservedState != "running" || sub.ContainerID == nil || s.StatsFor == nil {
			continue
		}
		if src := s.StatsFor(sub.NodeID); src != nil {
			running = append(running, job{sub: sub, src: src})
		}
	}
	s.mu.Lock()
	start := 0
	if n := len(running); n > usageMaxMeasured {
		start = s.offset % n
		s.offset = (start + usageMaxMeasured) % n
	}
	diskDue := map[string]bool{}
	if s.WorkspaceUsage != nil {
		for _, sub := range subs {
			if len(diskDue) >= usageDiskPerPass {
				break
			}
			if sub.NodeID != s.LocalNode {
				continue
			}
			if d, ok := s.disks[sub.ID]; !ok || now.Sub(d.at) >= usageDiskEvery {
				diskDue[sub.ID] = true
			}
		}
	}
	s.mu.Unlock()

	var jobs []job
	for i := 0; i < len(running) && i < usageMaxMeasured; i++ {
		jobs = append(jobs, running[(start+i)%len(running)])
	}
	for id := range diskDue {
		jobs = append(jobs, job{sub: domain.UsageSubject{ID: id}, disk: true})
	}

	var mu sync.Mutex
	out := map[string]domain.ResourceSample{}
	disks := map[string]usageDisk{}
	sem := make(chan struct{}, usageWorkers)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if j.disk {
				n, err := s.WorkspaceUsage(j.sub.ID)
				mu.Lock()
				disks[j.sub.ID] = usageDisk{at: now, bytes: n, ok: err == nil}
				mu.Unlock()
				return
			}
			sctx, cancel := context.WithTimeout(ctx, usageStatsDeadline)
			defer cancel()
			frames := 0
			var last domain.ResourceSample
			// The second frame carries a CPU figure (a difference between two).
			_ = j.src.StreamStats(sctx, *j.sub.ContainerID, func(m domain.ResourceSample) bool {
				last = m
				frames++
				return frames < 2
			})
			if frames > 0 {
				mu.Lock()
				out[j.sub.ID] = last
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out, disks
}

// drainLocked turns the accumulators into rows for the current bucket and
// resets them. Caller holds s.mu.
func (s *UsageService) drainLocked() []domain.UsageBucket {
	rows := make([]domain.UsageBucket, 0, len(s.acc))
	for id, a := range s.acc {
		if a.samples == 0 {
			continue
		}
		rows = append(rows, domain.UsageBucket{BotID: id, Res: domain.UsageRes5m, BucketMS: s.bucket, Samples: a.samples,
			Wanted: a.wanted, Up: a.up, Measured: a.measured, CPUSum: a.cpuSum, CPUMax: a.cpuMax, MemSum: a.memSum,
			MemMax: a.memMax, MemLimit: a.memLimit, NetRx: a.rx, NetTx: a.tx, DiskMax: a.disk, Crashes: a.crashes, Starts: a.starts})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].BotID < rows[j].BotID })
	s.acc = map[string]*usageAcc{}
	return rows
}

// Flush writes the current (partial) bucket, for shutdown.
func (s *UsageService) Flush(ctx context.Context) error {
	s.mu.Lock()
	rows := s.drainLocked()
	s.mu.Unlock()
	return s.Store.AddUsage(ctx, rows)
}

// Rollup recomputes hourly and daily rows from the previous hour on (or,
// the first time, over the hourly retention period) and advances the marks.
func (s *UsageService) Rollup(ctx context.Context) error {
	now := s.now().UnixMilli()
	hour := bucketOf(now, domain.UsageRes1h)
	mark := func(name string) (int64, error) {
		v, ok, err := s.Store.UsageMark(ctx, name)
		if err != nil {
			return 0, err
		}
		if !ok {
			return now - usageRetention1h.Milliseconds(), nil
		}
		return v, nil
	}
	from, err := mark(usageMarkBot)
	if err != nil {
		return err
	}
	for _, f := range []func(context.Context, int64) error{s.Store.RollupBotHours, s.Store.RollupBotEvents, s.Store.RollupBotDays} {
		if err := f(ctx, from); err != nil {
			return err
		}
	}
	// The previous hour is recomputed once more next time: a late flush or a
	// deployment finishing at the turn of the hour is still counted.
	if err := s.Store.SetUsageMark(ctx, usageMarkBot, hour-3600_000); err != nil {
		return err
	}
	nfrom, err := mark(usageMarkNode)
	if err != nil {
		return err
	}
	if err := s.Store.RollupNodeHours(ctx, nfrom); err != nil {
		return err
	}
	if err := s.Store.RollupNodeDays(ctx, nfrom); err != nil {
		return err
	}
	return s.Store.SetUsageMark(ctx, usageMarkNode, hour-3600_000)
}

// Prune deletes rows past their retention in small batches.
func (s *UsageService) Prune(ctx context.Context) error {
	now := s.now()
	r5m, r1h, r1d := usageRetention5m, usageRetention1h, usageRetention1d
	if s.Retention != nil {
		day := 24 * time.Hour
		m := s.Retention()
		if m.Usage5mDays > 0 {
			r5m = time.Duration(m.Usage5mDays) * day
		}
		if m.Usage1hDays > 0 {
			r1h = time.Duration(m.Usage1hDays) * day
		}
		if m.Usage1dDays > 0 {
			r1d = time.Duration(m.Usage1dDays) * day
		}
	}
	for _, t := range []struct {
		res  int64
		keep time.Duration
		node bool
	}{
		{domain.UsageRes5m, r5m, false}, {domain.UsageRes1h, r1h, false}, {domain.UsageRes1d, r1d, false},
		{domain.UsageRes1h, r1h, true}, {domain.UsageRes1d, r1d, true},
	} {
		before := now.Add(-t.keep).UnixMilli()
		for range 100 {
			var n int64
			var err error
			if t.node {
				n, err = s.Store.PruneNodeUsage(ctx, t.res, before, usagePruneBatch)
			} else {
				n, err = s.Store.PruneUsage(ctx, t.res, before, usagePruneBatch)
			}
			if err != nil {
				return err
			}
			if n < usagePruneBatch {
				break
			}
		}
	}
	return nil
}

// Run collects every interval, rolls up after each written bucket, prunes
// hourly, and writes the partial bucket when ctx ends.
func (s *UsageService) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = UsageSampleInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastPrune time.Time
	warn := func(what string, err error) {
		if err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Warn("usage analytics "+what, "err", err)
		}
	}
	// Roll up once at start (backfills deployments and backups from the
	// operations history on first use).
	warn("rollup", s.Rollup(ctx))
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			warn("flush", s.Flush(fctx))
			cancel()
			return
		case <-t.C:
		}
		flushed, err := s.Collect(ctx)
		warn("collect", err)
		if flushed {
			warn("rollup", s.Rollup(ctx))
		}
		if time.Since(lastPrune) > time.Hour {
			lastPrune = time.Now()
			warn("prune", s.Prune(ctx))
		}
	}
}

// ---- Reports ----

// UsageRange is a selectable time range and the resolution it is shown at.
type UsageRange struct {
	Span time.Duration
	Res  int64
}

// BotUsageRanges are the ranges of a bot's Analytics tab.
var BotUsageRanges = map[string]UsageRange{
	"1h":  {time.Hour, domain.UsageRes5m},
	"24h": {24 * time.Hour, domain.UsageRes5m},
	"7d":  {7 * 24 * time.Hour, domain.UsageRes1h},
	"30d": {30 * 24 * time.Hour, domain.UsageRes1h},
	"90d": {90 * 24 * time.Hour, domain.UsageRes1d},
}

// OverviewRanges are the ranges of Administration → Analytics.
var OverviewRanges = map[string]UsageRange{
	"24h": {24 * time.Hour, domain.UsageRes1h},
	"7d":  {7 * 24 * time.Hour, domain.UsageRes1h},
	"30d": {30 * 24 * time.Hour, domain.UsageRes1d},
	"90d": {90 * 24 * time.Hour, domain.UsageRes1d},
}

// UsagePoint is one bucket of a bot's chart. Nil means no data.
type UsagePoint struct {
	T        int64    `json:"t"`
	CPU      *float64 `json:"cpu"` // average cores in use
	CPUMax   *float64 `json:"cpu_max"`
	Mem      *int64   `json:"mem"` // average bytes
	MemMax   *int64   `json:"mem_max"`
	MemLimit int64    `json:"mem_limit"`
	NetRx    *int64   `json:"net_rx"` // bytes in the bucket
	NetTx    *int64   `json:"net_tx"`
	Disk     *int64   `json:"disk"`
	Uptime   *float64 `json:"uptime"` // percent of the time it was wanted running
	Crashes  int64    `json:"crashes"`
	Starts   int64    `json:"starts"`
}

// UsageEventPoint is one bucket of deployment and backup counts.
type UsageEventPoint struct {
	T             int64  `json:"t"`
	DeploysOK     int64  `json:"deploys_ok"`
	DeploysFailed int64  `json:"deploys_failed"`
	DeployAvgMS   *int64 `json:"deploy_avg_ms"`
	BackupsOK     int64  `json:"backups_ok"`
	BackupsFailed int64  `json:"backups_failed"`
	BackupBytes   int64  `json:"backup_bytes"`
}

// UsageTotals summarizes a range.
type UsageTotals struct {
	CPUAvg        *float64 `json:"cpu_avg"`
	CPUMax        *float64 `json:"cpu_max"`
	MemAvg        *int64   `json:"mem_avg"`
	MemMax        *int64   `json:"mem_max"`
	NetRx         int64    `json:"net_rx"`
	NetTx         int64    `json:"net_tx"`
	Disk          *int64   `json:"disk"`
	Uptime        *float64 `json:"uptime"`
	Crashes       int64    `json:"crashes"`
	Starts        int64    `json:"starts"`
	DeploysOK     int64    `json:"deploys_ok"`
	DeploysFailed int64    `json:"deploys_failed"`
	DeployAvgMS   *int64   `json:"deploy_avg_ms"`
	BackupsOK     int64    `json:"backups_ok"`
	BackupsFailed int64    `json:"backups_failed"`
	BackupBytes   int64    `json:"backup_bytes"`
}

// BotUsageReport is a bot's Analytics tab.
type BotUsageReport struct {
	Range    string            `json:"range"`
	Res      int64             `json:"resolution_s"`
	FromMS   int64             `json:"from_ms"`
	ToMS     int64             `json:"to_ms"`
	Points   []UsagePoint      `json:"points"`
	EventRes int64             `json:"event_resolution_s"`
	Events   []UsageEventPoint `json:"events"`
	Totals   UsageTotals       `json:"totals"`
}

func usagePoint(t int64, b *domain.UsageBucket) UsagePoint {
	p := UsagePoint{T: t}
	if b == nil {
		return p
	}
	p.MemLimit, p.Crashes, p.Starts, p.Disk = b.MemLimit, b.Crashes, b.Starts, b.DiskMax
	if b.Measured > 0 {
		p.CPU, p.CPUMax = ptr(b.CPUSum/float64(b.Measured)), ptr(b.CPUMax)
		p.Mem, p.MemMax = ptr(b.MemSum/b.Measured), ptr(b.MemMax)
	}
	if b.Samples > 0 {
		p.NetRx, p.NetTx = ptr(b.NetRx), ptr(b.NetTx)
	}
	if b.Wanted > 0 {
		p.Uptime = ptr(float64(b.Up) / float64(b.Wanted) * 100)
	}
	return p
}

func usageEvent(t int64, b *domain.UsageBucket) UsageEventPoint {
	e := UsageEventPoint{T: t}
	if b == nil {
		return e
	}
	e.DeploysOK, e.DeploysFailed, e.BackupsOK, e.BackupsFailed, e.BackupBytes = b.DeploysOK, b.DeploysFailed, b.BackupsOK, b.BackupsFailed, b.BackupBytes
	if n := b.DeploysOK + b.DeploysFailed; n > 0 {
		e.DeployAvgMS = ptr(b.DeployMS / n)
	}
	return e
}

// dense returns the bucket starts from the bucket holding from up to the one
// holding to.
func dense(from, to, res int64) []int64 {
	var out []int64
	for t := bucketOf(from, res); t <= to; t += res * 1000 {
		out = append(out, t)
	}
	return out
}

// BotUsage reports a bot's usage over a range. The actor needs console
// access to the bot (the same as the live resource gauges).
func (s *UsageService) BotUsage(ctx context.Context, actor domain.User, botID, rangeName string) (BotUsageReport, error) {
	r, ok := BotUsageRanges[rangeName]
	if !ok {
		return BotUsageReport{}, domain.Invalid("range must be one of 1h, 24h, 7d, 30d, 90d")
	}
	b, err := s.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole)
	if err != nil {
		return BotUsageReport{}, err
	}
	now := s.now().UnixMilli()
	from := now - r.Span.Milliseconds()
	rep := BotUsageReport{Range: rangeName, Res: r.Res, FromMS: bucketOf(from, r.Res), ToMS: now, EventRes: max(r.Res, domain.UsageRes1h)}
	rows, err := s.Store.ListBotUsage(ctx, b.ID, r.Res, rep.FromMS)
	if err != nil {
		return rep, err
	}
	byT := make(map[int64]*domain.UsageBucket, len(rows)+1)
	for i := range rows {
		byT[rows[i].BucketMS] = &rows[i]
	}
	// The open 5-minute bucket is still in memory.
	if r.Res == domain.UsageRes5m {
		if cur, ok := s.current(b.ID); ok {
			if x := byT[cur.BucketMS]; x != nil {
				x.Add(cur)
			} else {
				byT[cur.BucketMS] = &cur
			}
		}
	}
	var tot domain.UsageBucket
	for _, t := range dense(from, now, r.Res) {
		x := byT[t]
		rep.Points = append(rep.Points, usagePoint(t, x))
		if x != nil {
			tot.Add(*x)
		}
	}
	evRows := rows
	if rep.EventRes != r.Res {
		if evRows, err = s.Store.ListBotUsage(ctx, b.ID, rep.EventRes, bucketOf(from, rep.EventRes)); err != nil {
			return rep, err
		}
	}
	evByT := make(map[int64]*domain.UsageBucket, len(evRows))
	for i := range evRows {
		evByT[evRows[i].BucketMS] = &evRows[i]
	}
	var evTot domain.UsageBucket
	for _, t := range dense(from, now, rep.EventRes) {
		x := evByT[t]
		rep.Events = append(rep.Events, usageEvent(t, x))
		if x != nil {
			evTot.Add(*x)
		}
	}
	rep.Totals = usageTotals(tot, evTot)
	return rep, nil
}

func usageTotals(tot, ev domain.UsageBucket) UsageTotals {
	p := usagePoint(0, &tot)
	e := usageEvent(0, &ev)
	t := UsageTotals{CPUAvg: p.CPU, CPUMax: p.CPUMax, MemAvg: p.Mem, MemMax: p.MemMax, NetRx: tot.NetRx, NetTx: tot.NetTx,
		Disk: tot.DiskMax, Uptime: p.Uptime, Crashes: tot.Crashes, Starts: tot.Starts, DeploysOK: e.DeploysOK,
		DeploysFailed: e.DeploysFailed, DeployAvgMS: e.DeployAvgMS, BackupsOK: e.BackupsOK, BackupsFailed: e.BackupsFailed,
		BackupBytes: e.BackupBytes}
	return t
}

// current returns a copy of the bot's open 5-minute bucket.
func (s *UsageService) current(botID string) (domain.UsageBucket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.acc[botID]
	if a == nil || a.samples == 0 {
		return domain.UsageBucket{}, false
	}
	return domain.UsageBucket{BotID: botID, Res: domain.UsageRes5m, BucketMS: s.bucket, Samples: a.samples, Wanted: a.wanted,
		Up: a.up, Measured: a.measured, CPUSum: a.cpuSum, CPUMax: a.cpuMax, MemSum: a.memSum, MemMax: a.memMax,
		MemLimit: a.memLimit, NetRx: a.rx, NetTx: a.tx, DiskMax: a.disk, Crashes: a.crashes, Starts: a.starts}, true
}

// ---- Administration overview ----

// OverviewPoint is one bucket of the panel-wide charts (over the bots the
// viewer may see).
type OverviewPoint struct {
	T             int64    `json:"t"`
	CPU           float64  `json:"cpu"` // sum of average cores in use
	Mem           int64    `json:"mem"` // sum of average bytes
	NetRx         int64    `json:"net_rx"`
	NetTx         int64    `json:"net_tx"`
	Uptime        *float64 `json:"uptime"`
	Crashes       int64    `json:"crashes"`
	Starts        int64    `json:"starts"`
	DeploysOK     int64    `json:"deploys_ok"`
	DeploysFailed int64    `json:"deploys_failed"`
	BackupsOK     int64    `json:"backups_ok"`
	BackupsFailed int64    `json:"backups_failed"`
	BackupBytes   int64    `json:"backup_bytes"`
	NewUsers      int64    `json:"new_users"`
	Tickets       *int64   `json:"tickets,omitempty"` // opened; only with ticket access
}

// OverviewConsumer is a top consumer row.
type OverviewConsumer struct {
	BotID    string  `json:"bot_id"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Owner    string  `json:"owner,omitempty"` // email; only with users.view
	CPU      float64 `json:"cpu"`
	Mem      int64   `json:"mem"`
	NetBytes int64   `json:"net_bytes"`
}

// OverviewNode is one node's use over the range (nodes.manage only).
type OverviewNode struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Location  string     `json:"location"`
	Samples   int64      `json:"samples"`
	CPUAvg    *float64   `json:"cpu_avg"` // percent of the host
	CPUMax    *float64   `json:"cpu_max"`
	MemAvg    *int64     `json:"mem_avg"`
	MemTotal  int64      `json:"mem_total"`
	DiskUsed  int64      `json:"disk_used"` // latest bucket
	DiskTotal int64      `json:"disk_total"`
	NetRxBps  *int64     `json:"net_rx_bps"`
	NetTxBps  *int64     `json:"net_tx_bps"`
	Running   int64      `json:"running_max"`
	CPUSeries []*float64 `json:"cpu_series"` // per bucket of the overview
}

// OverviewLocation sums nodes by location.
type OverviewLocation struct {
	Name      string   `json:"name"`
	Nodes     int      `json:"nodes"`
	CPUAvg    *float64 `json:"cpu_avg"`
	MemAvg    int64    `json:"mem_avg"`
	MemTotal  int64    `json:"mem_total"`
	DiskUsed  int64    `json:"disk_used"`
	DiskTotal int64    `json:"disk_total"`
}

// OverviewTickets is ticket volume and response time.
type OverviewTickets struct {
	Opened           int64  `json:"opened"`
	Closed           int64  `json:"closed"`
	OpenNow          int64  `json:"open_now"`
	MedianResponseMS *int64 `json:"median_response_ms"`
	Answered         int64  `json:"answered"`
}

// Overview is Administration → Analytics.
type Overview struct {
	Range      string                   `json:"range"`
	Res        int64                    `json:"resolution_s"`
	FromMS     int64                    `json:"from_ms"`
	ToMS       int64                    `json:"to_ms"`
	Scope      string                   `json:"scope"` // all | delegated
	UsersTotal int64                    `json:"users_total"`
	UsersNew   int64                    `json:"users_new"`
	Bots       map[string]OverviewCount `json:"bots"` // by kind
	Points     []OverviewPoint          `json:"points"`
	Totals     UsageTotals              `json:"totals"`
	TopCPU     []OverviewConsumer       `json:"top_cpu"`
	TopMem     []OverviewConsumer       `json:"top_mem"`
	TopNet     []OverviewConsumer       `json:"top_net"`
	Nodes      []OverviewNode           `json:"nodes,omitempty"`
	Locations  []OverviewLocation       `json:"locations,omitempty"`
	Tickets    *OverviewTickets         `json:"tickets,omitempty"`
}

// OverviewCount is bots of one kind.
type OverviewCount struct {
	Total   int64 `json:"total"`
	Running int64 `json:"running"`
}

// Overview reports panel-wide usage. It needs analytics.view. A viewer who
// is not a built-in administrator sees only accounts it could manage and
// their bots (the manageable rule), nodes only with nodes.manage and
// tickets only with ticket access; owners' addresses need users.view.
func (s *UsageService) Overview(ctx context.Context, actor domain.User, rangeName string) (Overview, error) {
	if !actor.Can(domain.PermAnalyticsView) {
		return Overview{}, domain.ErrForbidden
	}
	r, ok := OverviewRanges[rangeName]
	if !ok {
		return Overview{}, domain.Invalid("range must be one of 24h, 7d, 30d, 90d")
	}
	now := s.now().UnixMilli()
	from := now - r.Span.Milliseconds()
	ov := Overview{Range: rangeName, Res: r.Res, FromMS: bucketOf(from, r.Res), ToMS: now, Scope: "all"}

	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return ov, err
	}
	var f domain.UsageFilter
	excluded := map[string]bool{}
	emails := map[string]string{}
	for _, u := range users {
		if manageable(actor, u) != nil {
			excluded[u.ID] = true
			f.ExcludeOwners = append(f.ExcludeOwners, u.ID)
			continue
		}
		emails[u.ID] = u.Email
	}
	if !actor.IsAdmin() {
		ov.Scope = "delegated"
	}
	times := dense(from, now, r.Res)
	index := make(map[int64]int, len(times))
	for i, t := range times {
		index[t] = i
		ov.Points = append(ov.Points, OverviewPoint{T: t})
	}
	for _, u := range users {
		if excluded[u.ID] {
			continue
		}
		ov.UsersTotal++
		if u.CreatedAtMS >= ov.FromMS {
			ov.UsersNew++
			if i, ok := index[bucketOf(u.CreatedAtMS, r.Res)]; ok {
				ov.Points[i].NewUsers++
			}
		}
	}

	counts, err := s.Store.UsageBotCounts(ctx, f)
	if err != nil {
		return ov, err
	}
	ov.Bots = map[string]OverviewCount{domain.KindBot: {}, domain.KindGame: {}}
	for k, v := range counts {
		ov.Bots[k] = OverviewCount{Total: v[0], Running: v[1]}
	}

	agg, err := s.Store.AggregateUsage(ctx, r.Res, ov.FromMS, f)
	if err != nil {
		return ov, err
	}
	var tot domain.UsageBucket
	var cpuSum float64
	var memSum, measuredBuckets int64
	for _, a := range agg {
		i, ok := index[a.BucketMS]
		if !ok {
			continue
		}
		p := &ov.Points[i]
		p.CPU, p.Mem, p.NetRx, p.NetTx, p.Crashes, p.Starts = a.CPUCores, a.MemBytes, a.NetRx, a.NetTx, a.Crashes, a.Starts
		p.DeploysOK, p.DeploysFailed, p.BackupsOK, p.BackupsFailed, p.BackupBytes = a.DeploysOK, a.DeploysFailed, a.BackupsOK, a.BackupsFailed, a.BackupBytes
		if a.Wanted > 0 {
			p.Uptime = ptr(float64(a.Up) / float64(a.Wanted) * 100)
		}
		tot.Add(domain.UsageBucket{NetRx: a.NetRx, NetTx: a.NetTx, Wanted: a.Wanted, Up: a.Up, Crashes: a.Crashes, Starts: a.Starts,
			DeploysOK: a.DeploysOK, DeploysFailed: a.DeploysFailed, DeployMS: a.DeployMS, BackupsOK: a.BackupsOK,
			BackupsFailed: a.BackupsFailed, BackupBytes: a.BackupBytes})
		if a.CPUCores > 0 || a.MemBytes > 0 {
			cpuSum += a.CPUCores
			memSum += a.MemBytes
			measuredBuckets++
			tot.CPUMax = max(tot.CPUMax, a.CPUCores)
			tot.MemMax = max(tot.MemMax, a.MemBytes)
		}
	}
	ov.Totals = usageTotals(tot, tot)
	ov.Totals.CPUAvg, ov.Totals.CPUMax, ov.Totals.MemAvg, ov.Totals.MemMax = nil, nil, nil, nil
	if measuredBuckets > 0 {
		ov.Totals.CPUAvg, ov.Totals.CPUMax = ptr(cpuSum/float64(measuredBuckets)), ptr(tot.CPUMax)
		ov.Totals.MemAvg, ov.Totals.MemMax = ptr(memSum/measuredBuckets), ptr(tot.MemMax)
	}

	owner := func(id string) string {
		if actor.Can(domain.PermUsersView) {
			return emails[id]
		}
		return ""
	}
	for _, t := range []struct {
		by  string
		dst *[]OverviewConsumer
	}{{"cpu", &ov.TopCPU}, {"mem", &ov.TopMem}, {"net", &ov.TopNet}} {
		top, err := s.Store.TopUsage(ctx, r.Res, ov.FromMS, f, t.by, usageTopLimit)
		if err != nil {
			return ov, err
		}
		*t.dst = []OverviewConsumer{}
		for _, c := range top {
			*t.dst = append(*t.dst, OverviewConsumer{BotID: c.BotID, Name: c.Name, Kind: c.Kind, Owner: owner(c.OwnerID),
				CPU: c.CPUCores, Mem: c.MemBytes, NetBytes: c.NetBytes})
		}
	}

	if actor.Can(domain.PermNodesManage) {
		if err := s.overviewNodes(ctx, &ov, r, times, index); err != nil {
			return ov, err
		}
	}
	if actor.Can(domain.PermTicketsViewAll) || actor.Can(domain.PermTicketsManage) {
		stats, err := s.Store.ListTicketStats(ctx, ov.FromMS)
		if err != nil {
			return ov, err
		}
		tk := &OverviewTickets{}
		var waits []int64
		for i := range ov.Points {
			ov.Points[i].Tickets = ptr(int64(0))
		}
		for _, t := range stats {
			if excluded[t.UserID] {
				continue
			}
			if t.Status == "open" || t.Status == "pending" {
				tk.OpenNow++
			}
			if t.ClosedAtMS != nil && *t.ClosedAtMS >= ov.FromMS {
				tk.Closed++
			}
			if t.CreatedAtMS < ov.FromMS {
				continue
			}
			tk.Opened++
			if i, ok := index[bucketOf(t.CreatedAtMS, r.Res)]; ok {
				*ov.Points[i].Tickets++
			}
			if t.FirstResponseMS != nil {
				waits = append(waits, max(*t.FirstResponseMS-t.CreatedAtMS, 0))
			}
		}
		if len(waits) > 0 {
			slices.Sort(waits)
			tk.Answered = int64(len(waits))
			tk.MedianResponseMS = ptr(waits[len(waits)/2])
		}
		ov.Tickets = tk
	}
	return ov, nil
}

func (s *UsageService) overviewNodes(ctx context.Context, ov *Overview, r UsageRange, times []int64, index map[int64]int) error {
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	locs, _, err := s.Store.ListLocations(ctx)
	if err != nil {
		return err
	}
	locName := map[string]string{}
	for _, l := range locs {
		locName[l.ID] = l.Name
	}
	rows, err := s.Store.ListNodeUsage(ctx, r.Res, ov.FromMS)
	if err != nil {
		return err
	}
	byNode := map[string][]domain.NodeUsageBucket{}
	for _, b := range rows {
		byNode[b.NodeID] = append(byNode[b.NodeID], b)
	}
	type locAcc struct {
		OverviewLocation
		cpu float64
		n   int
	}
	byLoc := map[string]*locAcc{}
	var order []string
	for _, n := range nodes {
		on := OverviewNode{ID: n.ID, Name: n.Name, Location: locName[n.LocationID], CPUSeries: make([]*float64, len(times))}
		var cpu float64
		var mem, rx, tx int64
		var latest int64
		for _, b := range byNode[n.ID] {
			if b.Samples == 0 {
				continue
			}
			on.Samples += b.Samples
			cpu += b.CPUSum
			mem += b.MemSum
			rx += b.NetRxSum
			tx += b.NetTxSum
			on.CPUMax = ptr(max(b.CPUMax, deref(on.CPUMax)))
			on.MemTotal = max(on.MemTotal, b.MemTotal)
			on.Running = max(on.Running, b.RunningMax)
			if b.BucketMS >= latest {
				latest, on.DiskUsed, on.DiskTotal = b.BucketMS, b.DiskUsed, b.DiskTotal
			}
			if i, ok := index[b.BucketMS]; ok {
				on.CPUSeries[i] = ptr(b.CPUSum / float64(b.Samples))
			}
		}
		if on.Samples > 0 {
			on.CPUAvg = ptr(cpu / float64(on.Samples))
			on.MemAvg = ptr(mem / on.Samples)
			on.NetRxBps, on.NetTxBps = ptr(rx/on.Samples), ptr(tx/on.Samples)
		}
		ov.Nodes = append(ov.Nodes, on)
		key := on.Location
		la := byLoc[key]
		if la == nil {
			la = &locAcc{OverviewLocation: OverviewLocation{Name: key}}
			byLoc[key] = la
			order = append(order, key)
		}
		la.Nodes++
		if on.CPUAvg != nil {
			la.cpu += *on.CPUAvg
			la.n++
			la.MemAvg += *on.MemAvg
		}
		la.MemTotal += on.MemTotal
		la.DiskUsed += on.DiskUsed
		la.DiskTotal += on.DiskTotal
	}
	sort.Strings(order)
	for _, k := range order {
		la := byLoc[k]
		if la.n > 0 {
			la.CPUAvg = ptr(la.cpu / float64(la.n))
		}
		ov.Locations = append(ov.Locations, la.OverviewLocation)
	}
	return nil
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
