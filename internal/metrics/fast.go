package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/telemetry"
)

// HostPoint is one fast host sample, the shape of the live stream's
// `sample` event. CPU is 0-100 % of the whole host; memory and swap are bytes
// in use; network and disk are bytes per second.
type HostPoint struct {
	T         int64   `json:"t"`
	CPU       float64 `json:"cpu"`
	Mem       int64   `json:"mem"`
	Load1     float64 `json:"load1"`
	NetRx     float64 `json:"net_rx"`
	NetTx     float64 `json:"net_tx"`
	DiskRead  float64 `json:"disk_read"`
	DiskWrite float64 `json:"disk_write"`
	Swap      int64   `json:"swap"`
}

// HostReader is what the fast sampler reads (telemetry.ProcReader).
type HostReader interface {
	CPU() (busy, total uint64, err error)
	Memory() (used, total int64, err error)
	Counters() (telemetry.Counters, error)
}

// FastSampler reads the host every Interval into a ring buffer that the
// live stream and the spike detector share: any number of open charts cost
// one set of /proc reads.
type FastSampler struct {
	Reader   HostReader
	Interval time.Duration // default 2s
	Keep     time.Duration // default 15 minutes
	Now      func() time.Time

	mu     sync.Mutex
	ring   []HostPoint
	subs   map[chan HostPoint]struct{}
	primed bool
	busy   uint64
	total  uint64
	prev   telemetry.Counters
	prevAt time.Time
}

func (f *FastSampler) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *FastSampler) interval() time.Duration {
	if f.Interval <= 0 {
		return 2 * time.Second
	}
	return f.Interval
}

// Sample takes one reading. The first only sets the baselines (ok=false).
func (f *FastSampler) Sample() (HostPoint, bool) {
	busy, total, err := f.Reader.CPU()
	if err != nil {
		return HostPoint{}, false
	}
	now := f.now()
	p := HostPoint{T: now.UnixMilli()}
	p.Mem, _, _ = f.Reader.Memory()
	c, cerr := f.Reader.Counters()
	f.mu.Lock()
	primed, pb, pt, prev, prevAt := f.primed, f.busy, f.total, f.prev, f.prevAt
	f.primed, f.busy, f.total = true, busy, total
	if cerr == nil {
		f.prev, f.prevAt = c, now
	}
	f.mu.Unlock()
	if !primed {
		return p, false
	}
	if total > pt {
		p.CPU = min(max(float64(int64(busy)-int64(pb))/float64(total-pt)*100, 0), 100)
	}
	if cerr == nil {
		p.Load1, p.Swap = c.Load1, c.SwapUsed
		if dt := now.Sub(prevAt).Seconds(); dt > 0 && !prevAt.IsZero() {
			rate := func(cur, old uint64) float64 {
				if cur < old {
					return 0
				}
				return float64(cur-old) / dt
			}
			p.NetRx, p.NetTx = rate(c.NetRx, prev.NetRx), rate(c.NetTx, prev.NetTx)
			p.DiskRead, p.DiskWrite = rate(c.DiskRead, prev.DiskRead), rate(c.DiskWrite, prev.DiskWrite)
		}
	}
	return p, true
}

// Add appends a point to the ring and hands it to every subscriber.
// Subscribers that are behind miss the point rather than slowing the
// sampler.
func (f *FastSampler) Add(p HostPoint) {
	keep := f.Keep
	if keep <= 0 {
		keep = 15 * time.Minute
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ring = append(f.ring, p)
	cut := p.T - keep.Milliseconds()
	i := 0
	for i < len(f.ring) && f.ring[i].T < cut {
		i++
	}
	if i > 0 {
		f.ring = append(f.ring[:0], f.ring[i:]...)
	}
	for ch := range f.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

// Recent returns the points with T > sinceMS, oldest first.
func (f *FastSampler) Recent(sinceMS int64) []HostPoint {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []HostPoint
	for _, p := range f.ring {
		if p.T > sinceMS {
			out = append(out, p)
		}
	}
	return out
}

// Latest returns the newest point (ok=false before the first).
func (f *FastSampler) Latest() (HostPoint, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ring) == 0 {
		return HostPoint{}, false
	}
	return f.ring[len(f.ring)-1], true
}

// Subscribe returns a channel of new points and a function that ends the
// subscription.
func (f *FastSampler) Subscribe() (<-chan HostPoint, func()) {
	ch := make(chan HostPoint, 8)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[chan HostPoint]struct{}{}
	}
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			delete(f.subs, ch)
			f.mu.Unlock()
		})
	}
}

// Run samples until ctx ends, calling onPoint (if set) with every point
// after it was added.
func (f *FastSampler) Run(ctx context.Context, onPoint func(HostPoint)) {
	f.Sample()
	t := time.NewTicker(f.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if p, ok := f.Sample(); ok {
				f.Add(p)
				if onPoint != nil {
					onPoint(p)
				}
			}
		}
	}
}
