package service

import (
	"context"
	"sync"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Coordinator gives each bot at most one workspace-changing operation at a
// time (deployment, restore, backup). It is in-process state: RivetPanel is a
// single process that owns its data directory. Exclusive claims (deploy,
// restore) also block file edits, environment edits and Start, so nothing
// changes the workspace underneath them; Stop, Kill, Delete and Unlink always
// win and cancel the claim instead of waiting behind it.
type Coordinator struct {
	mu     sync.Mutex
	claims map[string]*Claim
}

// Claim is an active reservation. Release it exactly once (extra calls are no-ops).
type Claim struct {
	What      string // "A deployment", "A restore", "A backup"
	Exclusive bool
	bot       string
	c         *Coordinator
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
}

// Claim reserves the bot. The returned context is cancelled when a newer,
// overriding action preempts the claim.
func (c *Coordinator) Claim(ctx context.Context, botID, what string, exclusive bool) (*Claim, context.Context, error) {
	if c == nil {
		return &Claim{}, ctx, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claims == nil {
		c.claims = map[string]*Claim{}
	}
	if cur := c.claims[botID]; cur != nil {
		return nil, nil, &domain.BusyError{What: cur.What}
	}
	cctx, cancel := context.WithCancel(ctx)
	cl := &Claim{What: what, Exclusive: exclusive, bot: botID, c: c, cancel: cancel, done: make(chan struct{})}
	c.claims[botID] = cl
	return cl, cctx, nil
}

// Release ends the claim.
func (cl *Claim) Release() {
	if cl == nil || cl.c == nil {
		return
	}
	cl.once.Do(func() {
		cl.c.mu.Lock()
		if cl.c.claims[cl.bot] == cl {
			delete(cl.c.claims, cl.bot)
		}
		cl.c.mu.Unlock()
		cl.cancel()
		close(cl.done)
	})
}

// Blocked returns a BusyError when an exclusive claim prevents changing the
// bot's files, environment or run state; nil otherwise.
func (c *Coordinator) Blocked(botID string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur := c.claims[botID]; cur != nil && cur.Exclusive {
		return &domain.BusyError{What: cur.What}
	}
	return nil
}

// Active reports the running claim, if any (for display).
func (c *Coordinator) Active(botID string) (what string, ok bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur := c.claims[botID]; cur != nil {
		return cur.What, true
	}
	return "", false
}

// Preempt cancels the bot's claim (if any) and waits until its holder
// releases it or ctx ends.
func (c *Coordinator) Preempt(ctx context.Context, botID string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	cur := c.claims[botID]
	c.mu.Unlock()
	if cur == nil {
		return nil
	}
	cur.cancel()
	select {
	case <-cur.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
