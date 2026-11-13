package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/live"
)

// Live updates. Every successful change made through the API publishes a
// topic hint (the first path segment: "tickets", "bots", "admin/users");
// bot status changes publish "bots" and new notifications publish
// "notifications" to their recipients only. Open tabs follow GET /live (a
// Server-Sent Events stream) and re-fetch what they show. Hints carry no
// data, so nothing reaches a tab that its own API calls would not return.

// liveTabHeader names the browser tab that made a change; that tab ignores
// its own hints.
const liveTabHeader = "X-Tab-Id"

// liveTopic is the hint a changing request publishes, or "" for none.
func liveTopic(path string) (topic string, self bool) {
	p := strings.TrimPrefix(path, "/api/v1/")
	if p == path {
		return "", false
	}
	seg := strings.Split(p, "/")
	switch seg[0] {
	case "", "auth", "live", "telemetry", "setup", "public":
		return "", false
	case "me", "notifications":
		return seg[0], true // only the account's other tabs care
	case "admin":
		if len(seg) > 1 && seg[1] != "" {
			return "admin/" + seg[1], false
		}
		return "admin", false
	}
	return seg[0], false
}

// liveMW publishes a hint after every successful change.
func (s *panel) liveMW(c fiber.Ctx) error {
	err := c.Next()
	if s.live == nil || err != nil {
		return err
	}
	switch c.Method() {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
	default:
		return nil
	}
	if st := c.Response().StatusCode(); st >= 400 {
		return nil
	}
	topic, self := liveTopic(c.Path())
	if topic == "" {
		return nil
	}
	e := live.Event{Topic: topic, Origin: clip(strings.Clone(c.Get(liveTabHeader)), 64)}
	if self {
		e.Users = []string{currentUser(c).ID}
	}
	s.live.Publish(e)
	return nil
}

// liveStream serves the change hints for the signed-in account. The session
// is re-checked every minute so a logout or revocation ends the stream.
func (s *panel) liveStream(c fiber.Ctx) error {
	if s.live == nil {
		return fiber.ErrNotFound
	}
	user := currentUser(c)
	sub, ok := s.live.Subscribe(user.ID)
	if !ok {
		return fiber.NewError(fiber.StatusTooManyRequests, "too many live views open")
	}
	token := currentToken(c)
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set("X-Accel-Buffering", "no")
	return c.SendStreamWriter(func(w *bufio.Writer) {
		defer sub.Close()
		ctx, cancel := context.WithCancel(s.baseCtx)
		defer cancel()
		write := func(format string, a ...any) bool {
			if _, err := fmt.Fprintf(w, format, a...); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		if !write("retry: 3000\nevent: ready\ndata: {}\n\n") {
			return
		}
		beat := time.NewTicker(25 * time.Second)
		defer beat.Stop()
		auth := time.NewTicker(time.Minute)
		defer auth.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-auth.C:
				if token != "" {
					if _, err := s.auth.Authenticate(ctx, token); err != nil {
						return
					}
				}
			case <-beat.C:
				if !write(": keep-alive\n\n") {
					return
				}
			case e := <-sub.C:
				b, _ := json.Marshal(e)
				if !write("event: change\ndata: %s\n\n", b) {
					return
				}
			}
		}
	})
}

// relayBotStatus turns bot status changes (starts, stops, crashes seen by
// the runner) into "bots" hints, at most a few per second.
func (s *panel) relayBotStatus() {
	if s.live == nil || s.bots == nil || s.bots.Bus == nil {
		return
	}
	sub := s.bots.Bus.SubscribeAll()
	go func() {
		defer sub.Close()
		var last time.Time
		pending := false
		flush := time.NewTicker(300 * time.Millisecond)
		defer flush.Stop()
		for {
			select {
			case <-s.baseCtx.Done():
				return
			case <-sub.C:
				if time.Since(last) > 300*time.Millisecond {
					s.live.Publish(live.Event{Topic: "bots"})
					last = time.Now()
				} else {
					pending = true
				}
			case <-flush.C:
				if pending {
					s.live.Publish(live.Event{Topic: "bots"})
					last, pending = time.Now(), false
				}
			}
		}
	}()
}
