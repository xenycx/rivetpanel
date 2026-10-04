// Package hostmon gathers what the Host page shows beyond the stored samples:
// the machine, the panel process, storage, Docker and per-bot resource use.
// Everything is read-only, bounded and cached, so a page left open cannot load
// the host. It reports facts only; it never returns environment values, file
// contents or logs of bots.
package hostmon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/logbuf"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// BotStore lists bots.
type BotStore interface {
	ListBots(ctx context.Context, ownerID string) ([]domain.Bot, error)
}

// UserStore resolves owner addresses for the bot table.
type UserStore interface {
	ListUsers(ctx context.Context) ([]domain.User, error)
}

// StatsSource follows a container's Docker stats stream.
type StatsSource interface {
	StreamStats(ctx context.Context, containerID string, fn func(domain.ResourceSample) bool) error
}

// Containers lists the containers this panel manages.
type Containers interface {
	ListManaged(ctx context.Context, botID string) ([]runner.ContainerInfo, error)
}

// HTTPStats are the panel's own request counters.
type HTTPStats struct {
	Requests uint64  `json:"requests"`
	Errors   uint64  `json:"errors"`
	InFlight int64   `json:"in_flight"`
	AvgMS    float64 `json:"avg_ms"`
}

// Monitor builds snapshots.
type Monitor struct {
	Proc           telemetry.ProcReader
	Store          BotStore
	Users          UserStore
	Stats          StatsSource // nil when the runner is off
	Containers     Containers  // nil when the runner is off
	Runner         func(context.Context) (runner.Status, bool)
	HTTP           func() HTTPStats
	Logs           *logbuf.Buffer
	WorkspaceUsage func(botID string) (int64, error)

	Version    string
	Started    time.Time
	DBPath     string
	DataRoot   string
	BackupDir  string
	SitesDir   string // empty when site hosting is off
	NodeBudget int64  // admission budget for bot memory, 0 = none

	mu       sync.Mutex
	dirs     map[string]*dirSize
	disks    map[string]*diskEntry
	botCache botCache
	dkCache  dockerCache
}

// Snapshot is the cheap part of the page: it is read from /proc and statfs on
// every request.
type Snapshot struct {
	GeneratedAtMS int64        `json:"generated_at_ms"`
	Host          HostFacts    `json:"host"`
	Memory        MemoryFacts  `json:"memory"`
	Panel         PanelFacts   `json:"panel"`
	Storage       []Location   `json:"storage"`
	Docker        DockerFacts  `json:"docker"`
	Logs          logbuf.Stats `json:"logs"`
}

// HostFacts describes the machine.
type HostFacts struct {
	Hostname   string  `json:"hostname"`
	OS         string  `json:"os"`
	Kernel     string  `json:"kernel"`
	Arch       string  `json:"arch"`
	CPUModel   string  `json:"cpu_model"`
	Cores      int     `json:"cores"`
	UptimeSec  int64   `json:"uptime_sec"`
	BootedAtMS int64   `json:"booted_at_ms"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`
}

// MemoryFacts splits host memory the way `free` does.
type MemoryFacts struct {
	Total     int64 `json:"total"`
	Used      int64 `json:"used"` // total minus available
	Available int64 `json:"available"`
	Free      int64 `json:"free"`
	Buffers   int64 `json:"buffers"`
	Cached    int64 `json:"cached"`
	SwapTotal int64 `json:"swap_total"`
	SwapUsed  int64 `json:"swap_used"`
	// BotReserved is the sum of memory limits of bots wanted running, the
	// number admission checks count (see Capacity).
	NodeBudget int64 `json:"node_budget"`
}

// PanelFacts is the RivetPanel process itself.
type PanelFacts struct {
	Version    string    `json:"version"`
	GoVersion  string    `json:"go_version"`
	PID        int       `json:"pid"`
	UptimeMS   int64     `json:"uptime_ms"`
	StartedMS  int64     `json:"started_ms"`
	RSSBytes   int64     `json:"rss_bytes"`
	HeapBytes  uint64    `json:"heap_bytes"`
	SysBytes   uint64    `json:"sys_bytes"`
	Goroutines int       `json:"goroutines"`
	Threads    int       `json:"threads"`
	OpenFiles  int       `json:"open_files"`
	FileLimit  int64     `json:"file_limit"`
	GCCount    uint32    `json:"gc_count"`
	GCPauseMS  float64   `json:"gc_pause_ms"`
	HTTP       HTTPStats `json:"http"`
	DBBytes    int64     `json:"db_bytes"`
}

// Location is one place the panel keeps data, with the filesystem under it.
type Location struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`   // size of the files below Path (-1 while unknown)
	Partial     bool   `json:"partial"` // the walk stopped early; Bytes is a lower bound
	CountedAtMS int64  `json:"counted_at_ms"`
	FSTotal     int64  `json:"fs_total"`
	FSFree      int64  `json:"fs_free"`
	InodesTotal int64  `json:"inodes_total"`
	InodesFree  int64  `json:"inodes_free"`
	Device      uint64 `json:"device"` // equal values share a filesystem
	Note        string `json:"note,omitempty"`
}

// DockerFacts is the container runtime as the runner sees it.
type DockerFacts struct {
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	Error       string `json:"error,omitempty"`
	Version     string `json:"version,omitempty"`
	CgroupV2    bool   `json:"cgroup_v2"`
	Rootless    bool   `json:"rootless"`
	SwapLimit   bool   `json:"swap_limit"`
	Workers     int    `json:"workers"`
	Queued      int    `json:"queued"`
	Tracked     int    `json:"tracked"`
	User        string `json:"user,omitempty"`
	Running     int    `json:"containers_running"`
	Stopped     int    `json:"containers_stopped"`
	Builders    int    `json:"builders"`
	Diagnostics int    `json:"diagnostics"`
	Problem     string `json:"problem,omitempty"`
}

// Snapshot reads the current state.
func (m *Monitor) Snapshot(ctx context.Context) Snapshot {
	now := time.Now()
	info := m.Proc.Info()
	c, _ := m.Proc.Counters()
	mi, _ := m.Proc.MemInfo()
	self := m.Proc.Self()
	s := Snapshot{GeneratedAtMS: now.UnixMilli(), Logs: m.logStats()}
	s.Host = HostFacts{Hostname: info.Hostname, OS: info.OS, Kernel: info.Kernel, Arch: info.Arch, CPUModel: info.CPUModel,
		Cores: info.LogicalCPU, UptimeSec: info.UptimeSec, BootedAtMS: info.BootedAtMS, Load1: c.Load1, Load5: c.Load5, Load15: c.Load15}
	s.Memory = MemoryFacts{Total: mi.Total, Available: mi.Available, Free: mi.Free, Buffers: mi.Buffers, Cached: mi.Cached,
		SwapTotal: mi.SwapTotal, SwapUsed: max(mi.SwapTotal-mi.SwapFree, 0), NodeBudget: m.NodeBudget}
	s.Memory.Used = min(max(mi.Total-mi.Available, 0), mi.Total)
	s.Panel = PanelFacts{Version: m.Version, GoVersion: self.GoVersion, PID: self.PID, UptimeMS: now.Sub(m.Started).Milliseconds(),
		StartedMS: m.Started.UnixMilli(), RSSBytes: self.RSSBytes, HeapBytes: self.HeapBytes, SysBytes: self.SysBytes,
		Goroutines: self.Goroutines, Threads: self.Threads, OpenFiles: self.OpenFiles, FileLimit: self.FileLimit,
		GCCount: self.GCCount, GCPauseMS: self.GCPauseMS}
	if m.HTTP != nil {
		s.Panel.HTTP = m.HTTP()
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		if st, err := os.Stat(m.DBPath + suf); err == nil {
			s.Panel.DBBytes += st.Size()
		}
	}
	s.Storage = m.locations(ctx)
	s.Docker = m.docker(ctx)
	return s
}

func (m *Monitor) logStats() logbuf.Stats {
	if m.Logs == nil {
		return logbuf.Stats{}
	}
	return m.Logs.Stats()
}

func (m *Monitor) locations(ctx context.Context) []Location {
	type spec struct{ key, label, path, note string }
	specs := []spec{
		{"database", "Database", filepath.Dir(m.DBPath), "SQLite database, write-ahead log and the other panel state beside it."},
		{"bots", "Bot workspaces", m.DataRoot, "Every bot's files, dependencies and build output."},
		{"backups", "Backups", m.BackupDir, "Per-bot backup archives."},
	}
	if m.SitesDir != "" {
		specs = append(specs, spec{"sites", "Hosted sites", m.SitesDir, "Site releases."})
	}
	var out []Location
	for _, sp := range specs {
		if sp.path == "" {
			continue
		}
		loc := Location{Key: sp.key, Label: sp.label, Path: sp.path, Note: sp.note, Bytes: -1}
		var st syscall.Statfs_t
		if err := syscall.Statfs(sp.path, &st); err == nil {
			loc.FSTotal = int64(st.Blocks) * int64(st.Bsize)
			loc.FSFree = int64(st.Bavail) * int64(st.Bsize)
			loc.InodesTotal, loc.InodesFree = int64(st.Files), int64(st.Ffree)
		}
		var sys syscall.Stat_t
		if err := syscall.Stat(sp.path, &sys); err == nil {
			loc.Device = uint64(sys.Dev)
		}
		if sp.key == "backups" {
			loc.Note = "Per-bot backup archives."
		}
		loc.Bytes, loc.Partial, loc.CountedAtMS = m.size(sp.path)
		out = append(out, loc)
	}
	return out
}

// dirSize is a cached, background-computed directory total. Walking a large
// workspace tree takes long enough that doing it per request would be a way to
// load the host; the page shows the last result and when it was counted.
type dirSize struct {
	bytes   int64
	partial bool
	at      time.Time
	running bool
}

const (
	dirRefresh  = 5 * time.Minute
	dirMaxWalk  = 20 * time.Second
	dirMaxFiles = 3_000_000
)

// size returns the cached total of a directory tree (-1 until the first walk
// finishes) and starts a refresh when it is stale.
func (m *Monitor) size(path string) (bytes int64, partial bool, atMS int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs == nil {
		m.dirs = map[string]*dirSize{}
	}
	d := m.dirs[path]
	if d == nil {
		d = &dirSize{bytes: -1}
		m.dirs[path] = d
	}
	if !d.running && time.Since(d.at) > dirRefresh {
		d.running = true
		go m.walk(path, d)
	}
	if d.at.IsZero() {
		return -1, false, 0
	}
	return d.bytes, d.partial, d.at.UnixMilli()
}

func (m *Monitor) walk(path string, d *dirSize) {
	deadline := time.Now().Add(dirMaxWalk)
	var total int64
	files := 0
	partial := false
	_ = filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if files++; files > dirMaxFiles || (files%4096 == 0 && time.Now().After(deadline)) {
			partial = true
			return filepath.SkipAll
		}
		if e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	m.mu.Lock()
	d.bytes, d.partial, d.at, d.running = total, partial, time.Now(), false
	m.mu.Unlock()
}

type dockerCache struct {
	at   time.Time
	list []runner.ContainerInfo
	err  error
}

func (m *Monitor) docker(ctx context.Context) DockerFacts {
	var f DockerFacts
	if m.Runner == nil {
		return f
	}
	st, ok := m.Runner(ctx)
	if !ok {
		return f
	}
	f.Enabled = true
	f.Workers, f.Queued, f.Tracked, f.User = st.Workers, st.Queued, st.Tracked, st.User
	f.Version, f.CgroupV2, f.Rootless, f.SwapLimit = st.Capabilities.ServerVersion, st.Capabilities.CgroupV2, st.Capabilities.Rootless, st.Capabilities.SwapLimit
	f.Problem = st.Capabilities.Problem
	if st.Ready != nil {
		f.Error = st.Ready.Error()
	} else {
		f.Ready = true
	}
	if m.Containers != nil && f.Ready {
		m.mu.Lock()
		stale := time.Since(m.dkCache.at) > 15*time.Second
		m.mu.Unlock()
		if stale {
			cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			list, err := m.Containers.ListManaged(cctx, "")
			cancel()
			m.mu.Lock()
			m.dkCache = dockerCache{at: time.Now(), list: list, err: err}
			m.mu.Unlock()
		}
		m.mu.Lock()
		for _, c := range m.dkCache.list {
			switch c.Role() {
			case runner.RoleBuilder:
				f.Builders++
				continue
			case runner.RoleDiagnostic:
				f.Diagnostics++
				continue
			}
			if c.Live() {
				f.Running++
			} else {
				f.Stopped++
			}
		}
		m.mu.Unlock()
	}
	return f
}

// BotUsage is one bot's current resource use.
type BotUsage struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Owner       string  `json:"owner"`
	Runtime     string  `json:"runtime"`
	State       string  `json:"state"`
	Running     bool    `json:"running"`
	CPUCores    float64 `json:"cpu_cores"`
	CPULimit    float64 `json:"cpu_limit_cores"`
	MemUsed     int64   `json:"mem_used_bytes"`
	MemLimit    int64   `json:"mem_limit_bytes"`
	PIDs        int64   `json:"pids"`
	NetRx       int64   `json:"net_rx_bytes"`
	NetTx       int64   `json:"net_tx_bytes"`
	DiskBytes   int64   `json:"disk_bytes"` // -1 when unknown
	Measured    bool    `json:"measured"`   // live numbers were read for this bot
	StartedAtMS int64   `json:"started_at_ms,omitempty"`
}

// BotsReport is the table of bots and what they use.
type BotsReport struct {
	GeneratedAtMS int64      `json:"generated_at_ms"`
	Bots          []BotUsage `json:"bots"`
	Total         int        `json:"total"`
	Running       int        `json:"running"`
	CPUCores      float64    `json:"cpu_cores"`      // sum over measured bots
	MemUsed       int64      `json:"mem_used_bytes"` // sum over measured bots
	MemReserved   int64      `json:"mem_reserved_bytes"`
	Truncated     bool       `json:"truncated"`
}

type botCache struct {
	at  time.Time
	out BotsReport
}

const (
	botsTTL          = 8 * time.Second
	botsWorkers      = 6
	botsMax          = 60 // running bots measured per refresh
	botStatsDeadline = 6 * time.Second
	botDiskTTL       = 2 * time.Minute
)

// Bots returns every bot with live numbers for the ones that are running. The
// numbers are read from Docker's stats stream (the second frame, because CPU
// is a difference between two) with bounded concurrency and cached briefly, so
// several administrators watching the page cost one round of reads.
func (m *Monitor) Bots(ctx context.Context) (BotsReport, error) {
	m.mu.Lock()
	if time.Since(m.botCache.at) < botsTTL && m.botCache.out.Bots != nil {
		out := m.botCache.out
		m.mu.Unlock()
		return out, nil
	}
	m.mu.Unlock()

	return m.measureBots(ctx)
}

func (m *Monitor) measureBots(ctx context.Context) (BotsReport, error) {
	list, err := m.Store.ListBots(ctx, "")
	if err != nil {
		return BotsReport{}, err
	}
	owners := map[string]string{}
	if m.Users != nil {
		if us, err := m.Users.ListUsers(ctx); err == nil {
			for _, u := range us {
				owners[u.ID] = u.Email
			}
		}
	}
	rep := BotsReport{GeneratedAtMS: time.Now().UnixMilli(), Total: len(list), Bots: make([]BotUsage, 0, len(list))}
	var measure []int
	for _, b := range list {
		u := BotUsage{ID: b.ID, Name: b.Name, Owner: owners[b.OwnerID], Runtime: b.Runtime, State: b.ObservedState,
			Running: b.ObservedState == "running", CPULimit: float64(b.NanoCPUs) / 1e9, MemLimit: b.MemoryBytes, DiskBytes: -1}
		if b.LastStartedAtMS != nil {
			u.StartedAtMS = *b.LastStartedAtMS
		}
		if b.DesiredState == domain.DesiredRunning {
			rep.MemReserved += b.MemoryBytes
		}
		if u.Running {
			rep.Running++
			if b.ContainerID != nil && m.Stats != nil && len(measure) < botsMax {
				measure = append(measure, len(rep.Bots))
			}
		}
		if m.WorkspaceUsage != nil {
			u.DiskBytes = m.botDisk(b.ID)
		}
		rep.Bots = append(rep.Bots, u)
	}
	rep.Truncated = rep.Running > len(measure) && m.Stats != nil

	// Measure running bots in parallel, a handful at a time.
	byID := map[string]string{}
	for _, b := range list {
		if b.ContainerID != nil {
			byID[b.ID] = *b.ContainerID
		}
	}
	sem := make(chan struct{}, botsWorkers)
	var wg sync.WaitGroup
	for _, idx := range measure {
		idx := idx
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, botStatsDeadline)
			defer cancel()
			frames := 0
			var last domain.ResourceSample
			_ = m.Stats.StreamStats(sctx, byID[rep.Bots[idx].ID], func(s domain.ResourceSample) bool {
				last = s
				frames++
				return frames < 2
			})
			if frames > 0 {
				u := &rep.Bots[idx]
				u.CPUCores, u.MemUsed, u.PIDs, u.NetRx, u.NetTx, u.Measured = last.CPUCores, last.MemUsedBytes, last.PIDs, last.NetRxBytes, last.NetTxBytes, true
				if last.MemLimitBytes > 0 {
					u.MemLimit = last.MemLimitBytes
				}
			}
		}()
	}
	wg.Wait()
	for _, u := range rep.Bots {
		if u.Measured {
			rep.CPUCores += u.CPUCores
			rep.MemUsed += u.MemUsed
		}
	}
	// Busiest first: running bots by memory, then the rest by name.
	sort.SliceStable(rep.Bots, func(i, j int) bool {
		a, b := rep.Bots[i], rep.Bots[j]
		if a.Running != b.Running {
			return a.Running
		}
		if a.MemUsed != b.MemUsed {
			return a.MemUsed > b.MemUsed
		}
		return a.Name < b.Name
	})
	m.mu.Lock()
	m.botCache = botCache{at: time.Now(), out: rep}
	m.mu.Unlock()
	return rep, nil
}

type diskEntry struct {
	at    time.Time
	bytes int64
	busy  bool
}

// botDisk returns a bot's workspace size from a cache that is refreshed in the
// background, so listing many bots never waits for a directory walk.
func (m *Monitor) botDisk(id string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disks == nil {
		m.disks = map[string]*diskEntry{}
	}
	e := m.disks[id]
	if e == nil {
		e = &diskEntry{bytes: -1}
		m.disks[id] = e
	}
	if !e.busy && time.Since(e.at) > botDiskTTL {
		e.busy = true
		go func() {
			n, err := m.WorkspaceUsage(id)
			m.mu.Lock()
			defer m.mu.Unlock()
			if err == nil {
				e.bytes = n
			}
			e.at, e.busy = time.Now(), false
		}()
	}
	return e.bytes
}
