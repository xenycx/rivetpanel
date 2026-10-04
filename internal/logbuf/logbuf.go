// Package logbuf keeps the panel's recent log lines in memory so an
// administrator can read them from the browser. It sits beside the normal
// log output (stderr, which systemd or Docker already collect); it does not
// replace it and does not survive a restart.
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// DefaultCapacity is how many lines are kept. At a few hundred bytes each this
// stays well under a megabyte.
const DefaultCapacity = 2000

const (
	maxMsg   = 2000 // runes
	maxValue = 1000 // runes
	maxAttrs = 24
)

// Attr is one key/value pair of a log line.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entry is one log line.
type Entry struct {
	Seq    int64  `json:"seq"`
	TimeMS int64  `json:"time_ms"`
	Level  string `json:"level"` // debug, info, warn, error
	Msg    string `json:"msg"`
	Attrs  []Attr `json:"attrs,omitempty"`
}

// Stats summarizes the buffer and everything seen since the panel started.
type Stats struct {
	Capacity  int   `json:"capacity"`
	Held      int   `json:"held"`
	OldestSeq int64 `json:"oldest_seq"`
	LatestSeq int64 `json:"latest_seq"`
	OldestMS  int64 `json:"oldest_ms"`
	Total     int64 `json:"total"`    // lines since start
	Errors    int64 `json:"errors"`   // error lines since start
	Warnings  int64 `json:"warnings"` // warning lines since start
	LastErrMS int64 `json:"last_error_ms"`
}

// Buffer is a fixed-size ring of the newest lines. The ring grows up to its
// capacity as lines arrive, so a quiet panel does not hold a full ring of
// empty entries.
type Buffer struct {
	mu       sync.Mutex
	capacity int
	ring     []Entry // len(ring) <= capacity; full once it has wrapped
	next     int     // index the next entry is written to
	size     int     // entries held
	seq      int64   // sequence of the newest entry
	errors   atomic.Int64
	warnings atomic.Int64
	lastErr  atomic.Int64
	total    atomic.Int64
}

// New returns a buffer holding up to capacity lines.
func New(capacity int) *Buffer {
	if capacity < 16 {
		capacity = DefaultCapacity
	}
	return &Buffer{capacity: capacity}
}

var secretKey = lazyre.New(`(?i)pass(word|wd)?|secret|token|authorization|cookie|api[_-]?key|setup[_-]?code|private|credential`)

// redacted reports whether the value of a key must not be kept.
func redacted(key string) bool { return secretKey.MatchString(key) }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

// Add stores one line and returns its sequence number.
func (b *Buffer) Add(e Entry) int64 {
	b.total.Add(1)
	switch e.Level {
	case "error":
		b.errors.Add(1)
		b.lastErr.Store(e.TimeMS)
	case "warn":
		b.warnings.Add(1)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	if len(b.ring) < b.capacity {
		// Still filling: next == size == len(ring).
		b.ring = append(b.ring, e)
		b.size++
		b.next = len(b.ring) % b.capacity
		return e.Seq
	}
	b.ring[b.next] = e
	b.next = (b.next + 1) % len(b.ring)
	return e.Seq
}

// Stats returns counters and the range currently held.
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := Stats{Capacity: b.capacity, Held: b.size, LatestSeq: b.seq, Total: b.total.Load(), Errors: b.errors.Load(),
		Warnings: b.warnings.Load(), LastErrMS: b.lastErr.Load()}
	if b.size > 0 {
		first := b.ring[(b.next-b.size+len(b.ring))%len(b.ring)]
		st.OldestSeq, st.OldestMS = first.Seq, first.TimeMS
	}
	return st
}

// Query selects lines.
type Query struct {
	After    int64  // only lines newer than this sequence number
	MinLevel string // debug, info, warn or error ("" = everything)
	Search   string // case-insensitive substring of the message or any attribute
	Limit    int    // at most this many, the newest ones (default 200, max 1000)
}

var rank = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

// Find returns matching lines oldest first.
func (b *Buffer) Find(q Query) []Entry {
	limit := q.Limit
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	min := rank[q.MinLevel]
	needle := strings.ToLower(strings.TrimSpace(q.Search))

	b.mu.Lock()
	snapshot := make([]Entry, 0, b.size)
	for i := 0; i < b.size; i++ {
		e := b.ring[(b.next-b.size+i+len(b.ring))%len(b.ring)]
		if e.Seq > q.After {
			snapshot = append(snapshot, e)
		}
	}
	b.mu.Unlock()

	out := make([]Entry, 0, min1(len(snapshot), limit))
	for i := len(snapshot) - 1; i >= 0 && len(out) < limit; i-- {
		e := snapshot[i]
		if rank[e.Level] < min {
			continue
		}
		if needle != "" && !matches(e, needle) {
			continue
		}
		out = append(out, e)
	}
	// Collected newest first; hand back oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func min1(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func matches(e Entry, needle string) bool {
	if strings.Contains(strings.ToLower(e.Msg), needle) {
		return true
	}
	for _, a := range e.Attrs {
		if strings.Contains(strings.ToLower(a.Key), needle) || strings.Contains(strings.ToLower(a.Value), needle) {
			return true
		}
	}
	return false
}

// Handler records every line in the buffer and passes it on to next.
func (b *Buffer) Handler(next slog.Handler) slog.Handler { return &handler{buf: b, next: next} }

type handler struct {
	buf    *Buffer
	next   slog.Handler
	attrs  []Attr // from WithAttrs
	prefix string // group path, "a.b."
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	e := Entry{TimeMS: r.Time.UnixMilli(), Level: levelName(r.Level), Msg: clip(r.Message, maxMsg)}
	if r.Time.IsZero() {
		e.TimeMS = time.Now().UnixMilli()
	}
	e.Attrs = append(e.Attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		e.Attrs = appendAttr(e.Attrs, h.prefix, a)
		return len(e.Attrs) < maxAttrs
	})
	h.buf.Add(e)
	return h.next.Handle(ctx, r)
}

func appendAttr(dst []Attr, prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			dst = appendAttr(dst, p, g)
		}
		return dst
	}
	key := prefix + a.Key
	val := a.Value.String()
	if redacted(key) {
		val = "[redacted]"
	}
	return append(dst, Attr{Key: key, Value: clip(val, maxValue)})
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]Attr(nil), h.attrs...)
	for _, a := range as {
		c.attrs = appendAttr(c.attrs, h.prefix, a)
	}
	c.next = h.next.WithAttrs(as)
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	c := *h
	if name != "" {
		c.prefix = h.prefix + name + "."
	}
	c.next = h.next.WithGroup(name)
	return &c
}
