// Package live fans out "something changed" hints to the panel's open
// browser tabs. A hint names a topic ("tickets", "bots", "notifications")
// and never carries data: a tab that cares re-fetches through the normal
// API, so every permission check still applies to what it sees.
//
// Subscribers get a bounded channel; when it is full the oldest hint is
// dropped (a later hint asks for the same re-fetch), so a stalled tab can
// never block a publisher or grow memory.
package live

import "sync"

// Event is one change hint.
type Event struct {
	Topic string `json:"topic"`
	// Origin is the browser tab that made the change ("" for changes the
	// panel made itself); that tab already shows it and ignores the hint.
	Origin string `json:"origin,omitempty"`
	// Users limits the hint to these accounts; nil sends it to everyone
	// signed in.
	Users []string `json:"-"`
}

// Sub is one open stream. Read from C; call Close when done.
type Sub struct {
	C    chan Event
	user string
	hub  *Hub
	once sync.Once
}

// Close unsubscribes; safe to call more than once.
func (s *Sub) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s)
		s.hub.perUser[s.user]--
		if s.hub.perUser[s.user] <= 0 {
			delete(s.hub.perUser, s.user)
		}
		s.hub.mu.Unlock()
	})
}

// Hub delivers hints to the subscribers they are meant for.
type Hub struct {
	mu      sync.Mutex
	subs    map[*Sub]struct{}
	perUser map[string]int
	// MaxPerUser caps open streams per account (0: 20).
	MaxPerUser int
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[*Sub]struct{}{}, perUser: map[string]int{}} }

// Subscribe opens a stream for an account; false when it has too many open.
func (h *Hub) Subscribe(userID string) (*Sub, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	max := h.MaxPerUser
	if max <= 0 {
		max = 20
	}
	if h.perUser[userID] >= max {
		return nil, false
	}
	s := &Sub{C: make(chan Event, 32), user: userID, hub: h}
	h.subs[s] = struct{}{}
	h.perUser[userID]++
	return s, true
}

// Publish delivers a hint without blocking. A nil hub discards it.
func (h *Hub) Publish(e Event) {
	if h == nil || e.Topic == "" {
		return
	}
	var only map[string]bool
	if e.Users != nil {
		only = make(map[string]bool, len(e.Users))
		for _, u := range e.Users {
			only[u] = true
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if only != nil && !only[s.user] {
			continue
		}
		for {
			select {
			case s.C <- e:
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

// Open reports the number of open streams (tests, metrics).
func (h *Hub) Open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
