// Package events is a small in-process publish/subscribe bus for bot status
// changes. Subscribers get a bounded, latest-wins channel so a stalled
// consumer can never block publishers or grow memory.
package events

import "sync"

// Status is a snapshot of a bot's lifecycle state. LastError is generic text
// produced by the runner and never contains secrets.
type Status struct {
	BotID              string
	DesiredState       string
	ObservedState      string
	Generation         int64
	ObservedGeneration int64
	ExitCode           *int64
	LastError          string
	Reason             string // domain.Reason* of the observation, when known
}

// Sub is one subscription. Read from C; call Close when done.
type Sub struct {
	C    chan Status
	bus  *Bus
	bot  string
	once sync.Once
}

// Close unsubscribes; safe to call more than once.
func (s *Sub) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs[s.bot], s)
		if len(s.bus.subs[s.bot]) == 0 {
			delete(s.bus.subs, s.bot)
		}
		s.bus.mu.Unlock()
	})
}

// Bus fans status updates out to subscribers of a bot.
type Bus struct {
	mu   sync.Mutex
	subs map[string]map[*Sub]struct{}
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[string]map[*Sub]struct{}{}} }

// Subscribe registers for one bot's updates. The channel buffers a few
// statuses; when full, the oldest is discarded (only the latest state matters).
func (b *Bus) Subscribe(botID string) *Sub {
	s := &Sub{C: make(chan Status, 4), bus: b, bot: botID}
	b.mu.Lock()
	if b.subs[botID] == nil {
		b.subs[botID] = map[*Sub]struct{}{}
	}
	b.subs[botID][s] = struct{}{}
	b.mu.Unlock()
	return s
}

// SubscribeAll registers for every bot's updates (used by alerting).
func (b *Bus) SubscribeAll() *Sub { return b.Subscribe("") }

// Publish delivers a status to the bot's subscribers and to SubscribeAll
// subscribers without blocking. A nil bus discards it, which keeps callers free
// of nil checks.
func (b *Bus) Publish(st Status) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, key := range []string{st.BotID, ""} {
		for s := range b.subs[key] {
			for {
				select {
				case s.C <- st:
				default:
					select {
					case <-s.C: // drop the oldest and retry
						continue
					default:
					}
				}
				break
			}
		}
	}
}

// Subscribers returns the number of active subscriptions for a bot (for tests/metrics).
func (b *Bus) Subscribers(botID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[botID])
}
