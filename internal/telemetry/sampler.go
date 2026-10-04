// Package telemetry samples host resource usage and enforces bounded retention.
package telemetry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Store persists samples.
type Store interface {
	InsertTelemetry(ctx context.Context, s domain.Telemetry) error
	PruneTelemetry(ctx context.Context, beforeMS int64, batch int) (int64, error)
	CountRunningBots(ctx context.Context, nodeID string) (int, error)
}

// Reader reads host counters; tests substitute fixtures.
type Reader interface {
	// CPU returns cumulative busy and total jiffies across all CPUs.
	CPU() (busy, total uint64, err error)
	LogicalCPUs() (int, error)
	Memory() (used, total int64, err error)
	Disk(path string) (used, total int64, err error)
}

// ProcReader reads Linux /proc (Root is normally "/proc") and /sys (SysRoot).
type ProcReader struct{ Root, SysRoot string }

func (p ProcReader) root() string {
	if p.Root == "" {
		return "/proc"
	}
	return p.Root
}

func (p ProcReader) CPU() (busy, total uint64, err error) {
	f, err := os.Open(filepath.Join(p.root(), "stat"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var v []uint64
		for _, s := range fields[1:] {
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("parse /proc/stat: %w", err)
			}
			v = append(v, n)
		}
		// user nice system idle iowait irq softirq steal guest guest_nice
		// guest time is already included in user/nice, so only the first 8 count.
		for i := 0; i < len(v) && i < 8; i++ {
			total += v[i]
		}
		idle := v[3]
		if len(v) > 4 {
			idle += v[4] // iowait
		}
		return total - idle, total, nil
	}
	return 0, 0, errors.New("no aggregate cpu line in /proc/stat")
}

func (p ProcReader) LogicalCPUs() (int, error) {
	f, err := os.Open(filepath.Join(p.root(), "stat"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "cpu") && len(l) > 3 && l[3] >= '0' && l[3] <= '9' {
			n++
		}
	}
	if n == 0 {
		return 0, errors.New("no per-cpu lines in /proc/stat")
	}
	return n, nil
}

func (p ProcReader) Memory() (used, total int64, err error) {
	f, err := os.Open(filepath.Join(p.root(), "meminfo"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var avail int64 = -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(rest)
		if len(fs) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fs[0], 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			avail = kb * 1024
		}
	}
	if total <= 0 || avail < 0 {
		return 0, 0, errors.New("MemTotal/MemAvailable missing from /proc/meminfo")
	}
	return min(max(total-avail, 0), total), total, nil
}

func (ProcReader) Disk(path string) (used, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	total = int64(st.Blocks) * bs
	used = (int64(st.Blocks) - int64(st.Bfree)) * bs
	if total <= 0 {
		return 0, 0, errors.New("filesystem reports zero size")
	}
	return min(max(used, 0), total), total, nil
}

// Sampler periodically records node telemetry and prunes old rows.
type Sampler struct {
	Store     Store
	Reader    Reader
	NodeID    string
	DiskPath  string
	Interval  time.Duration // 30-60s
	Retention time.Duration
	Batch     int // prune batch size
	Log       *slog.Logger
	Now       func() time.Time

	primed              bool
	lastBusy, lastTotal uint64
	lastCounters        Counters
	lastCountersAt      time.Time
	haveCounters        bool
}

func (s *Sampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SampleOnce takes one sample. The first call only establishes the CPU
// baseline and stores nothing (a percentage needs two readings).
func (s *Sampler) SampleOnce(ctx context.Context) (stored bool, err error) {
	busy, total, err := s.Reader.CPU()
	if err != nil {
		return false, err
	}
	prevBusy, prevTotal, primed := s.lastBusy, s.lastTotal, s.primed
	s.lastBusy, s.lastTotal, s.primed = busy, total, true
	if !primed {
		return false, nil
	}
	pct := 0.0
	if dt := int64(total) - int64(prevTotal); dt > 0 {
		pct = float64(int64(busy)-int64(prevBusy)) / float64(dt) * 100
	}
	pct = min(max(pct, 0), 100)

	cpus, err := s.Reader.LogicalCPUs()
	if err != nil {
		return false, err
	}
	mu, mt, err := s.Reader.Memory()
	if err != nil {
		return false, err
	}
	du, dt, err := s.Reader.Disk(s.DiskPath)
	if err != nil {
		return false, err
	}
	running, err := s.Store.CountRunningBots(ctx, s.NodeID)
	if err != nil {
		return false, err
	}
	t := domain.Telemetry{
		NodeID: s.NodeID, SampledAtMS: s.now().UnixMilli(), CPUPercent: pct, LogicalCPUs: cpus,
		MemoryUsedBytes: mu, MemoryTotalBytes: mt, DiskUsedBytes: du, DiskTotalBytes: dt, RunningBots: running,
	}
	s.addCounters(&t)
	err = s.Store.InsertTelemetry(ctx, t)
	return err == nil, err
}

// addCounters fills load, swap and the network and disk rates. The cumulative
// counters need two readings, so the first sample after a start has rates of
// zero. A counter that went backwards (interface reset, reboot) also yields 0.
func (s *Sampler) addCounters(t *domain.Telemetry) {
	hr, ok := s.Reader.(HostReader)
	if !ok {
		return
	}
	c, err := hr.Counters()
	if err != nil {
		return
	}
	now := s.now()
	t.Load1, t.SwapUsedBytes, t.SwapTotalBytes = c.Load1, c.SwapUsed, c.SwapTotal
	if s.haveCounters {
		if dt := now.Sub(s.lastCountersAt).Seconds(); dt > 0 {
			rate := func(cur, prev uint64) int64 {
				if cur < prev {
					return 0
				}
				return int64(float64(cur-prev) / dt)
			}
			t.NetRxBps, t.NetTxBps = rate(c.NetRx, s.lastCounters.NetRx), rate(c.NetTx, s.lastCounters.NetTx)
			t.DiskReadBps, t.DiskWriteBps = rate(c.DiskRead, s.lastCounters.DiskRead), rate(c.DiskWrite, s.lastCounters.DiskWrite)
		}
	}
	s.lastCounters, s.lastCountersAt, s.haveCounters = c, now, true
}

// Prune deletes samples older than the retention window in bounded batches and
// returns the number removed. It yields between batches so it never monopolizes
// the single SQLite writer.
func (s *Sampler) Prune(ctx context.Context) (int64, error) {
	batch := s.Batch
	if batch <= 0 {
		batch = 1000
	}
	cutoff := s.now().Add(-s.Retention).UnixMilli()
	var total int64
	for {
		n, err := s.Store.PruneTelemetry(ctx, cutoff, batch)
		total += n
		if err != nil || n < int64(batch) {
			return total, err
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Run samples every Interval until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	if _, err := s.SampleOnce(ctx); err != nil && ctx.Err() == nil {
		s.Log.Warn("telemetry baseline", "err", err)
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SampleOnce(ctx); err != nil && ctx.Err() == nil {
				s.Log.Warn("telemetry sample", "err", err)
			}
			// Prune roughly every 10 minutes at the default interval, and once at start.
			if ticks%20 == 0 {
				if n, err := s.Prune(ctx); err != nil && ctx.Err() == nil {
					s.Log.Warn("telemetry prune", "err", err)
				} else if n > 0 {
					s.Log.Info("telemetry pruned", "rows", n)
				}
			}
			ticks++
		}
	}
}
