package api

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

// Every rate limiter keeps its counters in one shared in-memory store.
//
// Fiber's default gives each limiter its own store with its own cleanup
// goroutine that wakes every second; with about twenty limiters that was
// twenty goroutines and twenty wake-ups a second in an idle panel. Here
// expired entries are skipped on read and swept once a minute by a single
// goroutine that only runs while the store holds entries.

const rateSweepInterval = time.Minute

var sharedRates = newRateStore()

// newLimiter is limiter.New with the shared store. Each call gets its own key
// space, so limiters never share counters (as with Fiber's default).
func newLimiter(cfg limiter.Config) fiber.Handler {
	if cfg.Storage == nil {
		cfg.Storage = sharedRates.view()
	}
	return limiter.New(cfg)
}

type rateEntry struct {
	val []byte
	exp int64 // unix nanoseconds; 0 = never expires
}

type rateStore struct {
	mu       sync.Mutex
	data     map[string]rateEntry
	sweeping bool // a sweeper goroutine is running
	views    atomic.Uint64
	now      func() time.Time
	interval time.Duration
}

func newRateStore() *rateStore {
	return &rateStore{data: map[string]rateEntry{}, now: time.Now, interval: rateSweepInterval}
}

// view returns a fiber.Storage over a key space of its own.
func (s *rateStore) view() fiber.Storage {
	return &rateView{s: s, prefix: strconv.FormatUint(s.views.Add(1), 36) + "\x00"}
}

func (s *rateStore) get(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok {
		return nil
	}
	if e.exp != 0 && e.exp <= s.now().UnixNano() {
		delete(s.data, key)
		return nil
	}
	return append([]byte(nil), e.val...)
}

func (s *rateStore) set(key string, val []byte, ttl time.Duration) {
	if key == "" || len(val) == 0 {
		return
	}
	e := rateEntry{val: append([]byte(nil), val...)}
	if ttl > 0 {
		e.exp = s.now().Add(ttl).UnixNano()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = e
	if !s.sweeping {
		s.sweeping = true
		go s.sweep()
	}
}

func (s *rateStore) delete(key string) {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
}

func (s *rateStore) reset(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.data, k)
		}
	}
}

// sweep drops expired entries every interval and exits once the store is
// empty; the next set starts it again.
func (s *rateStore) sweep() {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for range t.C {
		if !s.sweepOnce() {
			return
		}
	}
}

// sweepOnce removes expired entries and reports whether the sweeper should
// keep running (entries remain).
func (s *rateStore) sweepOnce() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixNano()
	for k, e := range s.data {
		if e.exp != 0 && e.exp <= now {
			delete(s.data, k)
		}
	}
	if len(s.data) == 0 {
		s.sweeping = false
		return false
	}
	return true
}

// rateView is one limiter's key space in the shared store.
type rateView struct {
	s      *rateStore
	prefix string
}

func (v *rateView) GetWithContext(_ context.Context, key string) ([]byte, error) {
	return v.s.get(v.prefix + key), nil
}

func (v *rateView) Get(key string) ([]byte, error) { return v.s.get(v.prefix + key), nil }

func (v *rateView) SetWithContext(_ context.Context, key string, val []byte, exp time.Duration) error {
	v.s.set(v.prefix+key, val, exp)
	return nil
}

func (v *rateView) Set(key string, val []byte, exp time.Duration) error {
	v.s.set(v.prefix+key, val, exp)
	return nil
}

func (v *rateView) DeleteWithContext(_ context.Context, key string) error {
	v.s.delete(v.prefix + key)
	return nil
}

func (v *rateView) Delete(key string) error { v.s.delete(v.prefix + key); return nil }

func (v *rateView) ResetWithContext(context.Context) error { v.s.reset(v.prefix); return nil }

func (v *rateView) Reset() error { v.s.reset(v.prefix); return nil }

func (v *rateView) Close() error { return nil }
