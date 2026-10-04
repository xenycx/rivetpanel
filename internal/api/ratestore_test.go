package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

func TestRateStoreViewsAreSeparateAndExpire(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newRateStore()
	s.now = func() time.Time { return now }
	s.interval = time.Hour // the test drives sweepOnce itself
	a, b := s.view(), s.view()
	_ = a.Set("ip", []byte("1"), time.Minute)
	_ = b.Set("ip", []byte("2"), 0)
	if v, _ := a.Get("ip"); string(v) != "1" {
		t.Fatalf("view a: %q", v)
	}
	if v, _ := b.Get("ip"); string(v) != "2" {
		t.Fatalf("view b: %q", v)
	}
	now = now.Add(2 * time.Minute)
	if v, _ := a.Get("ip"); v != nil {
		t.Fatalf("expired entry returned %q", v)
	}
	if v, _ := b.Get("ip"); string(v) != "2" {
		t.Fatalf("entry without expiry lost: %q", v)
	}
	_ = a.Reset()
	if v, _ := b.Get("ip"); string(v) != "2" {
		t.Fatal("resetting one view cleared another")
	}
	_ = b.Delete("ip")
	if s.sweepOnce() {
		t.Fatal("sweeper keeps running on an empty store")
	}
	s.mu.Lock()
	running := s.sweeping
	s.mu.Unlock()
	if running {
		t.Fatal("sweeper still marked running")
	}
}

func TestRateStoreSweeperOnlyRunsWithEntries(t *testing.T) {
	s := newRateStore()
	s.interval = 5 * time.Millisecond
	s.mu.Lock()
	idle := !s.sweeping
	s.mu.Unlock()
	if !idle {
		t.Fatal("sweeper running before any entry")
	}
	_ = s.view().Set("k", []byte("v"), time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		done := !s.sweeping && len(s.data) == 0
		s.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("sweeper did not drop the expired entry and stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Two limiters on the shared store keep separate counters, as each did with
// its own Fiber store.
func TestLimitersOnSharedStoreAreIndependent(t *testing.T) {
	app := fiber.New()
	app.Get("/a", newLimiter(limiter.Config{Max: 1, Expiration: time.Minute}), func(c fiber.Ctx) error { return c.SendStatus(204) })
	app.Get("/b", newLimiter(limiter.Config{Max: 1, Expiration: time.Minute}), func(c fiber.Ctx) error { return c.SendStatus(204) })
	code := func(p string) int {
		resp, err := app.Test(httptest.NewRequest("GET", p, nil))
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if c := code("/a"); c != 204 {
		t.Fatalf("first /a: %d", c)
	}
	if c := code("/b"); c != 204 {
		t.Fatalf("first /b after /a: %d (limiters share counters)", c)
	}
	if c := code("/a"); c != 429 {
		t.Fatalf("second /a: %d, want 429", c)
	}
}
