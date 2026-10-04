package logarchive

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Sink is an io.Writer for the panel's own log handler: every Write is one
// complete log line. Lines are buffered in memory and written to the "panel"
// day files by a background flusher, so logging never waits for the disk.
// Until Attach is called (and while disabled) lines are dropped; the normal
// log output (stderr) is unaffected either way.
type Sink struct {
	mu      sync.Mutex
	buf     []Line
	size    int
	dropped int64

	store   atomic.Pointer[Store]
	enabled atomic.Bool
	wake    chan struct{}
	once    sync.Once
}

// maxSinkBuffer bounds the memory used when the disk is slow.
const maxSinkBuffer = 4 << 20

// Write buffers one line. It never fails and never blocks on I/O.
func (s *Sink) Write(p []byte) (int, error) {
	if s.store.Load() == nil || !s.enabled.Load() {
		return len(p), nil
	}
	line := Line{At: time.Now(), Text: append([]byte(nil), p...)}
	s.mu.Lock()
	if s.size+len(p) > maxSinkBuffer {
		s.dropped++
		s.mu.Unlock()
		return len(p), nil
	}
	s.buf = append(s.buf, line)
	s.size += len(p)
	full := s.size >= 256<<10
	s.mu.Unlock()
	if full {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

// SetEnabled turns writing panel log files on or off.
func (s *Sink) SetEnabled(on bool) { s.enabled.Store(on) }

// Enabled reports whether panel log lines are written to files.
func (s *Sink) Enabled() bool { return s.enabled.Load() }

// Attach starts writing to store until ctx ends (a final flush runs then).
func (s *Sink) Attach(ctx context.Context, store *Store) {
	s.once.Do(func() {
		s.wake = make(chan struct{}, 1)
		s.store.Store(store)
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					s.Flush()
					return
				case <-t.C:
				case <-s.wake:
				}
				s.Flush()
			}
		}()
	})
}

// Flush writes the buffered lines.
func (s *Sink) Flush() {
	st := s.store.Load()
	if st == nil {
		return
	}
	s.mu.Lock()
	lines := s.buf
	s.buf, s.size = nil, 0
	s.mu.Unlock()
	if len(lines) > 0 {
		_ = st.Append(PanelScope, lines) // a failed write is not logged: that would loop
	}
}

// Dropped is how many lines were dropped because the buffer was full.
func (s *Sink) Dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}
