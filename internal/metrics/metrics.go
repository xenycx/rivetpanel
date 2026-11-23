package metrics

import (
	"context"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// Intervals and retention (see migration 0076).
const (
	ContainerInterval       = 15 * time.Second
	RemoteContainerInterval = 30 * time.Second
	PeriodicSnapshotEvery   = time.Minute
	SpikeSnapshotEvery      = 15 * time.Second
	SpikeEvalEvery          = 5 * time.Second

	ContainerRetention = 48 * time.Hour
	SnapshotRetention  = 7 * 24 * time.Hour
	SpikeRetention     = 30 * 24 * time.Hour

	DefaultSpikeCPUPct = 80
	PeriodicTop        = 25
	MaxCmdLen          = 160

	workers          = 4
	statsDeadline    = 5 * time.Second
	remotePerPass    = 40 // running apps measured per remote node per pass (rotating)
	pruneBatch       = 1000
	pruneEvery       = 10 * time.Minute
	visibilityTTL    = 5 * time.Minute
	liveCacheTTL     = 3 * time.Second
	spikeTopBots     = 8
	spikeTopProcs    = 10
	remoteProcsLimit = 50
)

// Store is the storage the service needs (*sqlite.DB).
type Store interface {
	GetNode(ctx context.Context, id string) (domain.Node, error)
	GetBot(ctx context.Context, id string) (domain.Bot, error)
	ListMetricSubjects(ctx context.Context) ([]domain.MetricSubject, error)
	InsertContainerSamples(ctx context.Context, samples []domain.ContainerSample) error
	ListContainerBuckets(ctx context.Context, nodeID, botID string, fromMS, toMS, stepMS int64) ([]domain.ContainerBucket, error)
	ContainerAverages(ctx context.Context, nodeID string, fromMS, toMS int64) ([]domain.ContainerBucket, error)
	PruneContainerSamples(ctx context.Context, beforeMS int64, batch int) (int64, error)
	UsageBuckets(ctx context.Context, nodeID, botID string, res, fromMS, toMS int64) ([]domain.ContainerBucket, error)
	InsertProcessSnapshot(ctx context.Context, s domain.ProcessSnapshot) (int64, error)
	NearestProcessSnapshot(ctx context.Context, nodeID string, tMS, maxDistMS int64) (domain.ProcessSnapshot, bool, error)
	PruneProcessSnapshots(ctx context.Context, beforeMS int64, batch int) (int64, error)
	InsertSpike(ctx context.Context, sp domain.LoadSpike) (int64, error)
	UpdateSpike(ctx context.Context, sp domain.LoadSpike) error
	CloseOpenSpikes(ctx context.Context, nodeID string, atMS int64) error
	ListSpikes(ctx context.Context, nodeID string, fromMS, toMS int64) ([]domain.LoadSpike, error)
	PruneSpikes(ctx context.Context, beforeMS int64, batch int) (int64, error)
	ListTelemetryRange(ctx context.Context, nodeID string, fromMS, toMS, stepMS int64) ([]domain.TelemetryBucket, error)
	ListNodeUsageRange(ctx context.Context, nodeID string, res, fromMS, toMS int64) ([]domain.NodeUsageBucket, error)
	ListMetricEvents(ctx context.Context, nodeID, botID string, fromMS, toMS int64, limit int) ([]domain.MetricEvent, error)
}

// LocalDocker is the panel's own Docker (*docker.Adapter). Optional.
type LocalDocker interface {
	StatsCounters(ctx context.Context, id string) (docker.StatsCounters, error)
	ContainerTop(ctx context.Context, id string) ([]docker.TopProcess, error)
	ContainerPID(ctx context.Context, id string) (int, error)
}

// StatsStream follows a container's statistics (a remote node's, through
// its agent).
type StatsStream interface {
	StreamStats(ctx context.Context, containerID string, fn func(domain.ResourceSample) bool) error
}

// Remote reaches nodes served by agents. Every field is optional; a nil
// Remote (agents module off) leaves remote nodes with host series only.
type Remote struct {
	// Online lists the connected remote node IDs.
	Online func() []string
	// Stats returns the statistics source of an online remote node.
	Stats func(nodeID string) StatsStream
	// Processes reads a remote node's busiest processes (optionally of one
	// container) through its agent.
	Processes func(ctx context.Context, nodeID string, limit int, containerID string) (domain.ProcessSnapshot, error)
	// Host reads a remote node's current host figures (cpu %, memory, load).
	Host func(ctx context.Context, nodeID string) (HostPoint, error)
}

// Metrics is the monitoring service: it runs the collectors and answers the
// range, attribution and live queries the charts and the assistant use.
type Metrics struct {
	Store       Store
	LocalNode   string
	Proc        ProcSampler   // host processes (RIVET_HOST_PROC)
	Cgroup      *CgroupReader // nil = read containers through Docker only
	Docker      LocalDocker   // nil when the panel runs no local Docker
	Fast        *FastSampler  // nil = no live stream or spike detection
	Remote      *Remote
	SpikeCPUPct float64 // default 80
	// RawInterval is node_telemetry's sampling interval (default 30s), the
	// finest step a host series can have.
	RawInterval time.Duration
	// LogicalCPUs of the local host (0 = read from Proc).
	LogicalCPUs int
	// EnvValues returns an app's environment values so they can be replaced
	// in its process command lines. Optional; values are only held for one
	// snapshot.
	EnvValues func(ctx context.Context, botID string) []string
	Log       *slog.Logger
	Now       func() time.Time

	mu      sync.Mutex
	prev    map[string]prevCounters // bot id -> last cumulative counters
	pids    map[string]int          // container id -> host pid
	last    map[string]domain.ContainerSample
	offset  map[string]int // remote node -> rotation offset
	visible struct {
		at time.Time
		ok bool
	}
	live struct {
		at   time.Time
		snap domain.ProcessSnapshot
	}
	liveMu sync.Mutex
	snapMu sync.Mutex // one local /proc snapshot at a time

	detector  SpikeDetector
	evalAt    time.Time
	spike     *spikeState
	spikeMu   sync.Mutex
	spikeWork chan func(context.Context)
	snapBusy  atomic.Bool
}

type prevCounters struct {
	at                time.Time
	container         string
	cpuNS             uint64
	rx, tx, read, wrt uint64
	hasNet, hasCPU    bool
}

type spikeState struct {
	rec      domain.LoadSpike
	sum      float64
	n        int
	bots     map[string]*botAcc
	passes   int
	procs    map[int]domain.ProcessEntry
	lastSnap time.Time
}

type botAcc struct {
	name string
	sum  float64
}

func (m *Metrics) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Metrics) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// LocalNodeID is the panel's own node.
func (m *Metrics) LocalNodeID() string { return m.LocalNode }

func (m *Metrics) threshold() float64 {
	if m.SpikeCPUPct > 0 {
		return m.SpikeCPUPct
	}
	return DefaultSpikeCPUPct
}

func (m *Metrics) cpus() int {
	if m.LogicalCPUs > 0 {
		return m.LogicalCPUs
	}
	return 1
}

// Run starts the collectors and blocks until ctx ends.
func (m *Metrics) Run(ctx context.Context) {
	m.detector = SpikeDetector{Threshold: m.threshold()}
	m.spikeWork = make(chan func(context.Context), 32)
	if err := m.Store.CloseOpenSpikes(ctx, m.LocalNode, m.now().UnixMilli()); err != nil && ctx.Err() == nil {
		m.log().Warn("metrics: close spikes left open", "err", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case fn := <-m.spikeWork:
				fn(ctx)
			}
		}
	}()
	if m.Fast != nil {
		wg.Add(1)
		go func() { defer wg.Done(); m.Fast.Run(ctx, m.onFastPoint) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); m.runContainers(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); m.runSnapshots(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); m.runPrune(ctx) }()
	wg.Wait()
}

func (m *Metrics) runContainers(ctx context.Context) {
	t := time.NewTicker(ContainerInterval)
	defer t.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			remote := tick%int(RemoteContainerInterval/ContainerInterval) == 0
			if err := m.CollectContainers(ctx, remote); err != nil && ctx.Err() == nil {
				m.log().Warn("metrics: container samples", "err", err)
			}
			tick++
		}
	}
}

func (m *Metrics) runSnapshots(ctx context.Context) {
	t := time.NewTicker(PeriodicSnapshotEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.PeriodicSnapshots(ctx)
		}
	}
}

func (m *Metrics) runPrune(ctx context.Context) {
	t := time.NewTicker(pruneEvery)
	defer t.Stop()
	for {
		if n, err := m.Prune(ctx); err != nil && ctx.Err() == nil {
			m.log().Warn("metrics: prune", "err", err)
		} else if n > 0 {
			m.log().Debug("metrics: pruned", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Prune deletes container samples older than 48 hours, process snapshots
// older than 7 days and spikes older than 30 days, in small batches.
func (m *Metrics) Prune(ctx context.Context) (int64, error) {
	now := m.now()
	var total int64
	for _, p := range []struct {
		fn     func(context.Context, int64, int) (int64, error)
		before time.Time
	}{
		{m.Store.PruneContainerSamples, now.Add(-ContainerRetention)},
		{m.Store.PruneProcessSnapshots, now.Add(-SnapshotRetention)},
		{m.Store.PruneSpikes, now.Add(-SpikeRetention)},
	} {
		for {
			n, err := p.fn(ctx, p.before.UnixMilli(), pruneBatch)
			total += n
			if err != nil {
				return total, err
			}
			if n < pruneBatch {
				break
			}
			select {
			case <-ctx.Done():
				return total, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	return total, nil
}

// --- container samples ---

type counters struct {
	cpuNS     uint64
	hasCPU    bool
	cpuCores  float64 // when only an instant figure is known (remote)
	mem       int64
	pids      int64
	rx, tx    uint64
	read, wrt uint64
	hasNet    bool
}

// CollectContainers takes one sample of every running app on the panel's
// node and, when remote is set, of the running apps on online remote nodes.
func (m *Metrics) CollectContainers(ctx context.Context, remote bool) error {
	subs, err := m.Store.ListMetricSubjects(ctx)
	if err != nil {
		return err
	}
	var local []domain.MetricSubject
	byNode := map[string][]domain.MetricSubject{}
	keep := map[string]bool{}
	for _, s := range subs {
		if s.ObservedState != "running" || s.ContainerID == "" {
			continue
		}
		keep[s.ContainerID] = true
		if s.NodeID == m.LocalNode {
			local = append(local, s)
		} else {
			byNode[s.NodeID] = append(byNode[s.NodeID], s)
		}
	}
	type job struct {
		sub  domain.MetricSubject
		read func(context.Context) (counters, error)
	}
	var jobs []job
	for _, s := range local {
		s := s
		jobs = append(jobs, job{s, func(ctx context.Context) (counters, error) { return m.readLocal(ctx, s.ContainerID) }})
	}
	if remote && m.Remote != nil && m.Remote.Stats != nil && m.Remote.Online != nil {
		for _, node := range m.Remote.Online() {
			list := byNode[node]
			src := m.Remote.Stats(node)
			if len(list) == 0 || src == nil {
				continue
			}
			m.mu.Lock()
			if m.offset == nil {
				m.offset = map[string]int{}
			}
			start := m.offset[node] % len(list)
			if len(list) > remotePerPass {
				m.offset[node] = (start + remotePerPass) % len(list)
			}
			m.mu.Unlock()
			for i := 0; i < len(list) && i < remotePerPass; i++ {
				s := list[(start+i)%len(list)]
				jobs = append(jobs, job{s, func(ctx context.Context) (counters, error) { return readStream(ctx, src, s.ContainerID) }})
			}
		}
	}

	now := m.now()
	var mu sync.Mutex
	got := map[string]counters{}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			jctx, cancel := context.WithTimeout(ctx, statsDeadline)
			defer cancel()
			c, err := j.read(jctx)
			if err != nil {
				return
			}
			mu.Lock()
			got[j.sub.ID] = c
			mu.Unlock()
		}()
	}
	wg.Wait()
	if m.Cgroup != nil {
		m.Cgroup.Forget(keep)
	}

	var out []domain.ContainerSample
	m.mu.Lock()
	if m.prev == nil {
		m.prev, m.last = map[string]prevCounters{}, map[string]domain.ContainerSample{}
	}
	for id := range m.pids {
		if !keep[id] {
			delete(m.pids, id)
		}
	}
	seen := map[string]bool{}
	for _, j := range jobs {
		c, ok := got[j.sub.ID]
		if !ok {
			continue
		}
		seen[j.sub.ID] = true
		p, had := m.prev[j.sub.ID]
		np := prevCounters{at: now, container: j.sub.ContainerID, cpuNS: c.cpuNS, hasCPU: c.hasCPU, rx: c.rx, tx: c.tx,
			read: c.read, wrt: c.wrt, hasNet: c.hasNet}
		m.prev[j.sub.ID] = np
		s := domain.ContainerSample{BotID: j.sub.ID, NodeID: j.sub.NodeID, SampledAtMS: now.UnixMilli(), MemoryBytes: c.mem, PIDs: c.pids}
		fresh := !had || p.container != j.sub.ContainerID
		dt := now.Sub(p.at).Seconds()
		if !c.hasCPU {
			s.CPUCores = c.cpuCores
		} else if fresh || !p.hasCPU || dt <= 0 {
			continue // a rate needs two readings of the same container
		} else if c.cpuNS >= p.cpuNS {
			s.CPUCores = float64(c.cpuNS-p.cpuNS) / 1e9 / dt
		}
		if !fresh && dt > 0 {
			rate := func(cur, old uint64) float64 {
				if cur < old {
					return 0
				}
				return float64(cur-old) / dt
			}
			if c.hasNet && p.hasNet {
				s.NetRxBps, s.NetTxBps = rate(c.rx, p.rx), rate(c.tx, p.tx)
			}
			s.BlkReadBps, s.BlkWriteBps = rate(c.read, p.read), rate(c.wrt, p.wrt)
		}
		out = append(out, s)
		m.last[j.sub.ID] = s
	}
	// Forget apps that are no longer running.
	running := map[string]bool{}
	for _, s := range subs {
		if s.ObservedState == "running" && s.ContainerID != "" {
			running[s.ID] = true
		}
	}
	for id := range m.prev {
		if !running[id] && !seen[id] {
			delete(m.prev, id)
			delete(m.last, id)
		}
	}
	m.mu.Unlock()

	names := make(map[string]string, len(subs))
	for _, s := range subs {
		names[s.ID] = s.Name
	}
	m.addToSpike(out, names)
	return m.Store.InsertContainerSamples(ctx, out)
}

// readLocal reads a local container: the cgroup files when they are
// reachable, Docker's statistics otherwise (and for the network counters
// when the container's network namespace cannot be read).
func (m *Metrics) readLocal(ctx context.Context, id string) (counters, error) {
	if m.Cgroup != nil {
		netPath := ""
		if pid := m.pidOf(ctx, id); pid > 0 {
			netPath = filepath.Join(m.Proc.root(), strconv.Itoa(pid), "net", "dev")
		}
		if cg, err := m.Cgroup.Read(id, netPath); err == nil {
			c := counters{cpuNS: cg.CPUUsec * 1000, hasCPU: true, mem: cg.MemBytes, pids: cg.PIDs, read: cg.ReadBytes,
				wrt: cg.WriteBytes, rx: cg.NetRx, tx: cg.NetTx, hasNet: cg.HasNet}
			if !c.hasNet && m.Docker != nil {
				if d, err := m.Docker.StatsCounters(ctx, id); err == nil {
					c.rx, c.tx, c.hasNet = d.NetRx, d.NetTx, true
				}
			}
			return c, nil
		}
	}
	if m.Docker == nil {
		return counters{}, ErrNoCgroup
	}
	d, err := m.Docker.StatsCounters(ctx, id)
	if err != nil {
		return counters{}, err
	}
	return counters{cpuNS: d.CPUUsageNS, hasCPU: true, mem: d.MemUsed, pids: d.PIDs, rx: d.NetRx, tx: d.NetTx,
		read: d.BlkRead, wrt: d.BlkWrite, hasNet: true}, nil
}

// pidOf returns a local container's host process ID (cached per container).
func (m *Metrics) pidOf(ctx context.Context, id string) int {
	m.mu.Lock()
	pid, ok := m.pids[id]
	m.mu.Unlock()
	if ok {
		return pid
	}
	if m.Docker == nil {
		return 0
	}
	pid, err := m.Docker.ContainerPID(ctx, id)
	if err != nil {
		return 0
	}
	m.mu.Lock()
	if m.pids == nil {
		m.pids = map[string]int{}
	}
	m.pids[id] = pid
	m.mu.Unlock()
	return pid
}

// readStream takes the second frame of a statistics stream (the first has
// no CPU figure).
func readStream(ctx context.Context, src StatsStream, id string) (counters, error) {
	frames := 0
	var last domain.ResourceSample
	err := src.StreamStats(ctx, id, func(s domain.ResourceSample) bool {
		last = s
		frames++
		return frames < 2
	})
	if frames == 0 {
		if err == nil {
			err = context.DeadlineExceeded
		}
		return counters{}, err
	}
	return counters{cpuCores: max(last.CPUCores, 0), mem: last.MemUsedBytes, pids: last.PIDs, rx: uint64(max(last.NetRxBytes, 0)),
		tx: uint64(max(last.NetTxBytes, 0)), read: uint64(max(last.BlkReadBytes, 0)), wrt: uint64(max(last.BlkWriteBytes, 0)), hasNet: true}, nil
}

// --- process snapshots ---

// secretArg matches secret-looking arguments: key=value pairs and
// "--flag value" pairs whose name suggests a credential, and passwords in
// URLs.
var (
	secretKV   = regexp.MustCompile(`(?i)([\w.-]*(?:password|passwd|secret|token|api[_-]?key|apikey|credential|private[_-]?key|access[_-]?key)[\w.-]*=)("[^"]*"|'[^']*'|\S+)`)
	secretFlag = regexp.MustCompile(`(?i)((?:^|\s)--?[\w.-]*(?:password|passwd|secret|token|api[_-]?key|apikey|credential|private[_-]?key|access[_-]?key)[\w.-]*\s+)("[^"]*"|'[^']*'|\S+)`)
	urlCreds   = regexp.MustCompile(`(://[^/\s:@]+:)([^@\s/]+)(@)`)
)

// RedactCmd replaces known secret values and secret-looking arguments in a
// command line and cuts it to MaxCmdLen characters.
func RedactCmd(cmd string, red *ai.Redactor) string {
	cmd = red.Text(cmd)
	cmd = secretKV.ReplaceAllString(cmd, "${1}[REDACTED]")
	cmd = secretFlag.ReplaceAllString(cmd, "${1}[REDACTED]")
	cmd = urlCreds.ReplaceAllString(cmd, "${1}[REDACTED]${3}")
	if utf8.RuneCountInString(cmd) > MaxCmdLen {
		r := []rune(cmd)
		cmd = string(r[:MaxCmdLen-1]) + "…"
	}
	return cmd
}

// attribute maps processes to apps and redacts their command lines. Each
// app's environment values are fetched once and dropped afterwards.
func (m *Metrics) attribute(ctx context.Context, items []domain.ProcessEntry, subs []domain.MetricSubject) []domain.ProcessEntry {
	byContainer := map[string]domain.MetricSubject{}
	for _, s := range subs {
		if s.ContainerID != "" {
			byContainer[s.ContainerID] = s
		}
	}
	reds := map[string]*ai.Redactor{}
	for i := range items {
		e := &items[i]
		if s, ok := byContainer[e.ContainerID]; ok && e.ContainerID != "" {
			e.BotID, e.BotName = s.ID, s.Name
		}
		red := reds[e.BotID]
		if _, done := reds[e.BotID]; !done {
			if e.BotID != "" && m.EnvValues != nil {
				red = ai.NewRedactor(m.EnvValues(ctx, e.BotID))
			}
			reds[e.BotID] = red
		}
		e.Cmd = RedactCmd(e.Cmd, red)
		if len(e.Comm) > 64 {
			e.Comm = e.Comm[:64]
		}
	}
	return items
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// hostVisible reports whether the proc root shows the host's processes:
// true when nothing runs locally or a running container's processes appear.
// Cached for a few minutes.
func (m *Metrics) hostVisible(subs []domain.MetricSubject) bool {
	m.mu.Lock()
	if !m.visible.at.IsZero() && m.now().Sub(m.visible.at) < visibilityTTL {
		ok := m.visible.ok
		m.mu.Unlock()
		return ok
	}
	m.mu.Unlock()
	ids := map[string]bool{}
	for _, s := range subs {
		if s.NodeID == m.LocalNode && s.ObservedState == "running" && s.ContainerID != "" {
			ids[s.ContainerID] = true
		}
	}
	ok := m.Proc.SeesContainers(ids)
	m.mu.Lock()
	m.visible.at, m.visible.ok = m.now(), ok
	m.mu.Unlock()
	return ok
}

// LocalSnapshot reads the panel node's busiest processes (of one container
// when containerID is set). Without a view of the host's processes it falls
// back to Docker's per-container lists (source "docker-top").
func (m *Metrics) LocalSnapshot(ctx context.Context, limit int, containerID string) (domain.ProcessSnapshot, error) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	if limit <= 0 {
		limit = PeriodicTop
	}
	subs, err := m.Store.ListMetricSubjects(ctx)
	if err != nil {
		return domain.ProcessSnapshot{}, err
	}
	snap := domain.ProcessSnapshot{NodeID: m.LocalNode, Source: domain.ProcSourceProc, Items: []domain.ProcessEntry{}}
	if m.hostVisible(subs) {
		ps, err := m.Proc.Sample(ctx, Filter{Limit: limit, Container: containerID})
		if err == nil {
			snap.SampledAtMS, snap.HostCPUPct = ps.At.UnixMilli(), round2(ps.HostCPUPct)
			for _, p := range ps.Processes {
				snap.Items = append(snap.Items, domain.ProcessEntry{PID: p.PID, Comm: p.Comm, Cmd: p.Cmd, CPUPct: round2(p.CPUPct),
					RSSBytes: p.RSSBytes, ContainerID: p.ContainerID})
			}
			snap.Items = m.attribute(ctx, snap.Items, subs)
			return snap, nil
		}
		if m.Docker == nil {
			return snap, err
		}
	}
	if m.Docker == nil {
		return snap, ErrNoProcesses
	}
	// Fallback: Docker's process list of each running local container.
	snap.Source = domain.ProcSourceDockerTop
	snap.SampledAtMS = m.now().UnixMilli()
	if m.Fast != nil {
		if p, ok := m.Fast.Latest(); ok {
			snap.HostCPUPct = round2(p.CPU)
		}
	}
	var targets []domain.MetricSubject
	for _, s := range subs {
		if s.NodeID != m.LocalNode || s.ObservedState != "running" || s.ContainerID == "" {
			continue
		}
		if containerID != "" && s.ContainerID != containerID {
			continue
		}
		targets = append(targets, s)
	}
	var mu sync.Mutex
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, s := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			tctx, cancel := context.WithTimeout(ctx, statsDeadline)
			defer cancel()
			top, err := m.Docker.ContainerTop(tctx, s.ContainerID)
			if err != nil {
				return
			}
			mu.Lock()
			for _, p := range top {
				snap.Items = append(snap.Items, domain.ProcessEntry{PID: p.PID, Comm: p.Comm, Cmd: p.Args, CPUPct: round2(p.CPUPct),
					RSSBytes: p.RSSBytes, ContainerID: s.ContainerID})
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	sortProcs(snap.Items)
	if len(snap.Items) > limit {
		snap.Items = snap.Items[:limit]
	}
	snap.Items = m.attribute(ctx, snap.Items, subs)
	return snap, nil
}

func sortProcs(items []domain.ProcessEntry) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CPUPct != items[j].CPUPct {
			return items[i].CPUPct > items[j].CPUPct
		}
		return items[i].RSSBytes > items[j].RSSBytes
	})
}

// ErrNoProcesses means no process source is available.
var ErrNoProcesses = domain.Invalid("process lists are not available on this node")

// PeriodicSnapshots stores the busiest processes of the panel's node and of
// every online remote node (reason "periodic").
func (m *Metrics) PeriodicSnapshots(ctx context.Context) {
	if m.snapBusy.CompareAndSwap(false, true) {
		snap, err := m.LocalSnapshot(ctx, PeriodicTop, "")
		m.snapBusy.Store(false)
		if err == nil {
			snap.Reason = domain.SnapshotPeriodic
			if _, err := m.Store.InsertProcessSnapshot(ctx, snap); err != nil && ctx.Err() == nil {
				m.log().Warn("metrics: process snapshot", "err", err)
			}
		} else if ctx.Err() == nil {
			m.log().Debug("metrics: process snapshot", "err", err)
		}
	}
	if m.Remote == nil || m.Remote.Processes == nil || m.Remote.Online == nil {
		return
	}
	subs, err := m.Store.ListMetricSubjects(ctx)
	if err != nil {
		return
	}
	for _, node := range m.Remote.Online() {
		if node == m.LocalNode {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		snap, err := m.Remote.Processes(rctx, node, PeriodicTop, "")
		cancel()
		if err != nil {
			continue
		}
		snap.NodeID, snap.Reason = node, domain.SnapshotPeriodic
		snap.Items = m.attribute(ctx, snap.Items, subs)
		_, _ = m.Store.InsertProcessSnapshot(ctx, snap)
	}
}

// --- spikes ---

// onFastPoint runs on the fast sampler's goroutine: it feeds the spike
// detector with the mean of the last SpikeEvalEvery of points and hands any
// database work to the spike worker.
func (m *Metrics) onFastPoint(p HostPoint) {
	t := time.UnixMilli(p.T)
	if !m.evalAt.IsZero() && t.Sub(m.evalAt) < SpikeEvalEvery {
		return
	}
	since := m.evalAt.UnixMilli()
	if m.evalAt.IsZero() {
		since = p.T - SpikeEvalEvery.Milliseconds()
	}
	m.evalAt = t
	var sum float64
	n := 0
	for _, q := range m.Fast.Recent(since) {
		sum += q.CPU
		n++
	}
	if n == 0 {
		return
	}
	cpu := sum / float64(n)
	switch m.detector.Observe(t, cpu) {
	case SpikeOpened:
		start := m.detector.StartedAt()
		m.queue(func(ctx context.Context) { m.openSpike(ctx, start, cpu) })
	case SpikeClosed:
		end := m.detector.EndedAt()
		m.queue(func(ctx context.Context) { m.closeSpike(ctx, end) })
	default:
		if m.detector.Open() {
			m.spikeMu.Lock()
			// Readings in the closing tail (below threshold-10) are not
			// part of the spike's figures.
			if m.spike != nil && cpu >= m.threshold()-10 {
				m.spike.sum += cpu
				m.spike.n++
				m.spike.rec.PeakCPUPct = max(m.spike.rec.PeakCPUPct, round2(cpu))
			}
			due := m.spike != nil && m.now().Sub(m.spike.lastSnap) >= SpikeSnapshotEvery
			m.spikeMu.Unlock()
			if due {
				m.queue(func(ctx context.Context) { m.spikeSnapshot(ctx) })
			}
		}
	}
}

func (m *Metrics) queue(fn func(context.Context)) {
	if m.spikeWork == nil {
		return
	}
	select {
	case m.spikeWork <- fn:
	default: // the worker is behind; a later reading catches up
	}
}

func (m *Metrics) openSpike(ctx context.Context, start time.Time, cpu float64) {
	st := &spikeState{rec: domain.LoadSpike{NodeID: m.LocalNode, StartedAtMS: start.UnixMilli(), PeakCPUPct: round2(cpu),
		AvgCPUPct: round2(cpu)}, sum: cpu, n: 1, bots: map[string]*botAcc{}, procs: map[int]domain.ProcessEntry{}}
	// Seed the apps with the latest samples so a short spike still names them.
	subs, _ := m.Store.ListMetricSubjects(ctx)
	names := map[string]string{}
	for _, s := range subs {
		names[s.ID] = s.Name
	}
	m.mu.Lock()
	for id, s := range m.last {
		if s.NodeID == m.LocalNode {
			st.bots[id] = &botAcc{name: names[id], sum: s.CPUCores}
		}
	}
	m.mu.Unlock()
	if len(st.bots) > 0 {
		st.passes = 1
	}
	m.finishTop(st)
	id, err := m.Store.InsertSpike(ctx, st.rec)
	if err != nil {
		m.log().Warn("metrics: record spike", "err", err)
		return
	}
	st.rec.ID = id
	m.spikeMu.Lock()
	m.spike = st
	m.spikeMu.Unlock()
	m.log().Info("cpu spike started", "cpu_pct", round2(cpu), "threshold", m.threshold())
	m.spikeSnapshot(ctx)
}

// addToSpike adds a container pass to the open spike's per-app totals.
func (m *Metrics) addToSpike(samples []domain.ContainerSample, names map[string]string) {
	m.spikeMu.Lock()
	defer m.spikeMu.Unlock()
	st := m.spike
	if st == nil {
		return
	}
	any := false
	for _, s := range samples {
		if s.NodeID != m.LocalNode {
			continue
		}
		a := st.bots[s.BotID]
		if a == nil {
			a = &botAcc{}
			st.bots[s.BotID] = a
		}
		if a.name == "" {
			a.name = names[s.BotID]
		}
		a.sum += s.CPUCores
		any = true
	}
	if any {
		st.passes++
	}
}

// spikeSnapshot stores a process snapshot (reason "spike"), merges it into
// the open spike's top processes and saves the spike's figures so far.
func (m *Metrics) spikeSnapshot(ctx context.Context) {
	m.spikeMu.Lock()
	if m.spike == nil {
		m.spikeMu.Unlock()
		return
	}
	m.spike.lastSnap = m.now()
	m.spikeMu.Unlock()
	if !m.snapBusy.CompareAndSwap(false, true) {
		return
	}
	snap, err := m.LocalSnapshot(ctx, PeriodicTop, "")
	m.snapBusy.Store(false)
	if err == nil {
		snap.Reason = domain.SnapshotSpike
		_, _ = m.Store.InsertProcessSnapshot(ctx, snap)
	}
	m.spikeMu.Lock()
	st := m.spike
	if st == nil {
		m.spikeMu.Unlock()
		return
	}
	for _, p := range snap.Items {
		if old, ok := st.procs[p.PID]; !ok || p.CPUPct > old.CPUPct {
			st.procs[p.PID] = p
		}
	}
	m.finishTop(st)
	rec := st.rec
	m.spikeMu.Unlock()
	if err := m.Store.UpdateSpike(ctx, rec); err != nil && ctx.Err() == nil {
		m.log().Warn("metrics: update spike", "err", err)
	}
}

// finishTop fills the spike record's mean, top apps and top processes.
// Caller holds spikeMu (or owns st).
func (m *Metrics) finishTop(st *spikeState) {
	if st.n > 0 {
		st.rec.AvgCPUPct = round2(st.sum / float64(st.n))
	}
	used := st.rec.AvgCPUPct / 100 * float64(m.cpus())
	bots := make([]domain.SpikeBot, 0, len(st.bots))
	passes := max(st.passes, 1)
	for id, a := range st.bots {
		cores := a.sum / float64(passes)
		if cores <= 0 {
			continue
		}
		b := domain.SpikeBot{BotID: id, Name: a.name, CPUCores: round2(cores)}
		if used > 0 {
			b.SharePct = round2(min(cores/used*100, 100))
		}
		bots = append(bots, b)
	}
	sort.Slice(bots, func(i, j int) bool { return bots[i].CPUCores > bots[j].CPUCores })
	if len(bots) > spikeTopBots {
		bots = bots[:spikeTopBots]
	}
	st.rec.TopBots = bots
	procs := make([]domain.ProcessEntry, 0, len(st.procs))
	for _, p := range st.procs {
		procs = append(procs, p)
	}
	sortProcs(procs)
	if len(procs) > spikeTopProcs {
		procs = procs[:spikeTopProcs]
	}
	st.rec.TopProcesses = procs
}

func (m *Metrics) closeSpike(ctx context.Context, end time.Time) {
	m.spikeMu.Lock()
	st := m.spike
	m.spike = nil
	if st == nil {
		m.spikeMu.Unlock()
		return
	}
	// Names of apps first seen during the spike.
	m.spikeMu.Unlock()
	subs, _ := m.Store.ListMetricSubjects(ctx)
	for _, s := range subs {
		if a := st.bots[s.ID]; a != nil && a.name == "" {
			a.name = s.Name
		}
	}
	m.finishTop(st)
	e := max(end.UnixMilli(), st.rec.StartedAtMS)
	st.rec.EndedAtMS = &e
	if err := m.Store.UpdateSpike(ctx, st.rec); err != nil && ctx.Err() == nil {
		m.log().Warn("metrics: close spike", "err", err)
	}
	m.log().Info("cpu spike ended", "peak_pct", st.rec.PeakCPUPct, "avg_pct", st.rec.AvgCPUPct)
}
