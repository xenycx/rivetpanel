// Package nodetx keeps workspace replacements on a node reversible until the
// control plane confirms the database change that belongs to them.
//
// Remote restores, remote GitHub deployments and remote AI file patches use
// the same lifecycle:
//
//	staged   (deployments only) the archive is fully unpacked and validated in
//	         a private staging directory; no workspace entry has changed yet
//	swapped  the new files are in place and the previous ones are kept aside
//	         under the filesystem journal
//	done     the panel committed (previous files deleted) or rolled back
//
// A transaction that the panel never completes expires to rollback (or, while
// staged, to abort). A node process that dies while a transaction is swapped
// is rolled back by filesystem.Manager.Recover on its next start, because the
// journal is only marked done by Finish.
package nodetx

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// DefaultTTL bounds how long an unconfirmed transaction stays open.
const DefaultTTL = 5 * time.Minute

var (
	// ErrBusy means another transaction for the same server is in progress.
	ErrBusy = errors.New("another file replacement is already pending for this server")
	// ErrNotFound means the transaction does not exist (never began, expired
	// or already completed with a result that is no longer remembered).
	ErrNotFound = errors.New("unknown or expired transaction")
	// ErrConflict means the request does not fit the transaction's state.
	ErrConflict = errors.New("the transaction was already completed differently or is not in the expected state")
	// ErrAborted ends a staged deployment that the panel decided not to apply.
	ErrAborted = errors.New("the staged deployment was abandoned")
)

type phase int

const (
	phaseStaged phase = iota
	phaseApplying
	phaseSwapped
)

type deployResult struct {
	files  int
	commit *filesystem.Commit
	err    error
}

type tx struct {
	botID  string
	phase  phase
	commit *filesystem.Commit
	closer io.Closer
	timer  *time.Timer
	// Staged deployments: the goroutine that owns the archive waits on decide
	// inside its pre-swap gate and reports the swap through result.
	decide chan bool
	result chan deployResult
}

type completed struct {
	botID  string
	commit bool
}

// Registry tracks the open transactions of one node. The zero value is not
// usable; call New.
type Registry struct {
	mu      sync.Mutex
	ttl     time.Duration
	byID    map[string]*tx
	byBot   map[string]string
	pending map[string]bool
	done    map[string]completed
	log     *slog.Logger
}

// New returns an empty registry; ttl <= 0 uses DefaultTTL.
func New(log *slog.Logger, ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Registry{ttl: ttl, byID: map[string]*tx{}, byBot: map[string]string{}, pending: map[string]bool{},
		done: map[string]completed{}, log: log}
}

func (r *Registry) reserve(botID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending[botID] || r.byBot[botID] != "" {
		return ErrBusy
	}
	r.pending[botID] = true
	return nil
}

func (r *Registry) release(botID string) {
	r.mu.Lock()
	delete(r.pending, botID)
	r.mu.Unlock()
}

// register publishes t under a new id and arms its expiry. Caller holds no lock.
func (r *Registry) register(t *tx) string {
	id := uuid.NewString()
	r.mu.Lock()
	delete(r.pending, t.botID)
	r.byID[id], r.byBot[t.botID] = t, id
	t.timer = time.AfterFunc(r.ttl, func() { r.expire(id) })
	r.mu.Unlock()
	return id
}

// BeginRestore swaps a workspace with restore (which must leave the previous
// contents journaled in the returned Commit) and returns the transaction id.
func (r *Registry) BeginRestore(botID string, restore func() (*filesystem.Commit, io.Closer, error)) (string, error) {
	return r.beginSwapped(botID, restore)
}

// BeginPatch swaps in an AI file patch (Workspace.ApplyPatch, which keeps
// the previous files journaled) and returns the transaction id. It shares
// the busy check, expiry and completion of every other transaction.
func (r *Registry) BeginPatch(botID string, patch func() (*filesystem.Commit, io.Closer, error)) (string, error) {
	return r.beginSwapped(botID, patch)
}

func (r *Registry) beginSwapped(botID string, restore func() (*filesystem.Commit, io.Closer, error)) (string, error) {
	if err := r.reserve(botID); err != nil {
		return "", err
	}
	commit, closer, err := restore()
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		r.release(botID)
		return "", err
	}
	return r.register(&tx{botID: botID, phase: phaseSwapped, commit: commit, closer: closer}), nil
}

// BeginDeploy runs deploy (typically Workspace.DeployTarGzCommit) until it
// reaches its pre-swap gate: the archive is then fully read, validated and
// staged, and BeginDeploy returns the transaction id with no workspace entry
// changed. Apply lets the swap proceed; Complete(false) abandons it.
//
// deploy must call gate exactly once, after staging and before the first
// workspace change, and must return the gate's error unchanged. closer is
// closed when the transaction ends.
func (r *Registry) BeginDeploy(botID string, closer io.Closer, deploy func(gate func() error) (int, *filesystem.Commit, error)) (string, error) {
	if err := r.reserve(botID); err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return "", err
	}
	t := &tx{botID: botID, phase: phaseStaged, closer: closer, decide: make(chan bool, 1), result: make(chan deployResult, 1)}
	staged := make(chan struct{})
	backstop := 2 * r.ttl
	go func() {
		gated := false
		n, c, err := deploy(func() error {
			gated = true
			close(staged)
			select {
			case ok := <-t.decide:
				if ok {
					return nil
				}
				return ErrAborted
			case <-time.After(backstop): // the registry's own expiry normally fires first
				return ErrAborted
			}
		})
		if err == nil && !gated {
			err = errors.New("nodetx: deployment finished without reaching its gate")
			if c != nil {
				_ = c.Rollback()
				c = nil
			}
		}
		t.result <- deployResult{files: n, commit: c, err: err}
	}()
	select {
	case <-staged:
		return r.register(t), nil
	case res := <-t.result:
		if closer != nil {
			_ = closer.Close()
		}
		r.release(botID)
		if res.err == nil {
			res.err = errors.New("nodetx: deployment finished without reaching its gate")
		}
		return "", res.err
	}
}

// Apply swaps a staged deployment into place and returns its file count. The
// previous files stay journaled until Complete. A failed swap is rolled back
// by the filesystem layer and ends the transaction.
func (r *Registry) Apply(botID, id string) (int, error) {
	r.mu.Lock()
	t := r.byID[id]
	if t == nil || t.botID != botID {
		r.mu.Unlock()
		return 0, ErrNotFound
	}
	if t.phase != phaseStaged {
		r.mu.Unlock()
		return 0, ErrConflict
	}
	t.phase = phaseApplying
	t.timer.Stop()
	r.mu.Unlock()

	t.decide <- true
	res := <-t.result
	r.mu.Lock()
	if res.err != nil {
		delete(r.byID, id)
		delete(r.byBot, botID)
		r.mu.Unlock()
		if t.closer != nil {
			_ = t.closer.Close()
		}
		return 0, res.err
	}
	t.phase, t.commit = phaseSwapped, res.commit
	t.timer = time.AfterFunc(r.ttl, func() { r.expire(id) })
	r.mu.Unlock()
	return res.files, nil
}

// take removes a transaction that is not mid-swap.
func (r *Registry) take(botID, id string) (*tx, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.byID[id]
	if t == nil || t.botID != botID {
		return nil, ErrNotFound
	}
	if t.phase == phaseApplying {
		return nil, ErrConflict
	}
	delete(r.byID, id)
	delete(r.byBot, botID)
	if t.timer != nil {
		t.timer.Stop()
	}
	return t, nil
}

// finish ends t: commit keeps the new files, otherwise the previous ones
// return (a staged deployment is simply abandoned).
func finish(t *tx, commit bool) error {
	defer func() {
		if t.closer != nil {
			_ = t.closer.Close()
		}
	}()
	if t.phase == phaseStaged {
		t.decide <- false
		res := <-t.result
		if res.commit != nil { // the gate refused, so this cannot happen; be safe
			return res.commit.Rollback()
		}
		return nil
	}
	if commit {
		return t.commit.Finish()
	}
	return t.commit.Rollback()
}

// Complete commits or rolls back a transaction. Committing a deployment that
// was never applied is refused. Repeating the same outcome for a recently
// completed transaction succeeds, so a lost response can be retried.
func (r *Registry) Complete(botID, id string, commit bool) error {
	t, err := r.take(botID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			r.mu.Lock()
			d, ok := r.done[id]
			r.mu.Unlock()
			if ok && d.botID == botID {
				if d.commit == commit {
					return nil
				}
				return ErrConflict
			}
		}
		return err
	}
	if commit && t.phase == phaseStaged {
		// Put it back: the caller must Apply first.
		r.mu.Lock()
		r.byID[id], r.byBot[botID] = t, id
		t.timer = time.AfterFunc(r.ttl, func() { r.expire(id) })
		r.mu.Unlock()
		return ErrConflict
	}
	if err := finish(t, commit); err != nil {
		return err
	}
	r.mu.Lock()
	r.done[id] = completed{botID: botID, commit: commit}
	r.mu.Unlock()
	time.AfterFunc(r.ttl, func() {
		r.mu.Lock()
		delete(r.done, id)
		r.mu.Unlock()
	})
	return nil
}

// expire rolls back (or abandons) a transaction the panel never completed.
func (r *Registry) expire(id string) {
	r.mu.Lock()
	t := r.byID[id]
	if t == nil || t.phase == phaseApplying {
		r.mu.Unlock()
		return
	}
	delete(r.byID, id)
	delete(r.byBot, t.botID)
	r.mu.Unlock()
	if err := finish(t, false); err != nil {
		r.log.Error("roll back an unconfirmed workspace replacement", "bot", t.botID, "err", err)
	} else {
		r.log.Warn("rolled back a workspace replacement the panel never confirmed", "bot", t.botID)
	}
}

// Open reports how many transactions are open (for tests and status).
func (r *Registry) Open() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID) + len(r.pending)
}
