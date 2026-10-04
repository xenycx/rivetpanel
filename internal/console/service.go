package console

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
)

// Source is the Docker side of the console.
type Source interface {
	// Logs follows a container's output as Docker's multiplexed stream with
	// timestamps. since is exclusive-ish (the caller de-duplicates); tail is
	// the number of trailing lines when since is zero (0 = all).
	Logs(ctx context.Context, containerID string, since time.Time, tail int) (io.ReadCloser, error)
	// AttachStdin opens a write-only handle to the container's stdin. Closing
	// it must NOT close the process's stdin (the container runs with
	// OpenStdin and StdinOnce=false).
	AttachStdin(ctx context.Context, containerID string) (io.WriteCloser, error)
}

// Outgoing is a server-to-client message.
type Outgoing struct {
	Type               string `json:"type"` // log | status | dropped | error | node | pong
	Stream             Stream `json:"stream,omitempty"`
	TS                 string `json:"ts,omitempty"`
	Data               string `json:"data,omitempty"`
	Count              int64  `json:"count,omitempty"`
	Code               string `json:"code,omitempty"`
	Message            string `json:"message,omitempty"`
	DesiredState       string `json:"desired_state,omitempty"`
	ObservedState      string `json:"observed_state,omitempty"`
	Generation         int64  `json:"generation,omitempty"`
	ObservedGeneration int64  `json:"observed_generation,omitempty"`
	ExitCode           *int64 `json:"exit_code,omitempty"`
}

// Incoming is a client-to-server message.
type Incoming struct {
	Type string `json:"type"` // stdin | ping
	Data string `json:"data,omitempty"`
}

// Conn is the transport (a WebSocket in production).
type Conn interface {
	// Send writes one message, honoring a write deadline; an error ends the session.
	Send(ctx context.Context, m Outgoing) error
	// Receive blocks for the next client message.
	Receive(ctx context.Context) (Incoming, error)
}

// ErrSessionEnded is returned by Run when the server ends a session because
// the bot was removed or the user's access was revoked.
var ErrSessionEnded = errors.New("console session ended by server")

// ErrSourceOffline is wrapped by a Source whose node cannot be reached right
// now (a remote node's agent is disconnected). The stream keeps waiting for
// the same container instead of treating it as gone.
var ErrSourceOffline = errors.New("the server's node is offline")

// Params describe one console session.
type Params struct {
	BotID    string
	Refresh  func(ctx context.Context) (domain.Bot, error) // current, authorized view of the bot
	CanStdin bool                                          // the client asked for an input-capable session
	// AllowStdin re-authorizes input for every line, so losing the power
	// permission (or the session) stops input immediately. Nil allows.
	AllowStdin func(ctx context.Context) error
	Since      time.Time // resume point from a previous session
	Tail       int
}

// Options bound resource use per connection. Zero values get defaults.
type Options struct {
	QueueLen       int           // max queued outbound messages per client
	ChunkBytes     int           // max payload bytes per log message
	MaxStdinBytes  int           // max bytes per stdin message
	StdinRate      float64       // stdin messages per second
	StdinBurst     int           // stdin burst allowance
	StatusPoll     time.Duration // fallback poll while waiting for a container
	DropFlushEvery time.Duration
	AccessRecheck  time.Duration // how often authorization is re-validated
	Now            func() time.Time
}

func (o *Options) defaults() {
	if o.QueueLen == 0 {
		o.QueueLen = 128
	}
	if o.ChunkBytes == 0 {
		o.ChunkBytes = 8 << 10
	}
	if o.MaxStdinBytes == 0 {
		o.MaxStdinBytes = 4 << 10
	}
	if o.StdinRate == 0 {
		o.StdinRate = 20
	}
	if o.StdinBurst == 0 {
		o.StdinBurst = 40
	}
	if o.StatusPoll == 0 {
		o.StatusPoll = time.Second
	}
	if o.DropFlushEvery == 0 {
		o.DropFlushEvery = time.Second
	}
	if o.AccessRecheck == 0 {
		o.AccessRecheck = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Service runs console sessions.
type Service struct {
	Src  Source
	Bus  *events.Bus
	Opts Options
	// SourceFor, when set, picks the source for a server's node (remote
	// nodes stream through their agent); nil uses Src for every server.
	SourceFor func(nodeID string) Source

	initOnce sync.Once
	mu       sync.Mutex
	writers  map[string]uint64 // bot id -> session id holding stdin
	nextID   uint64
}

func (s *Service) source(nodeID string) Source {
	if s.SourceFor != nil {
		if src := s.SourceFor(nodeID); src != nil {
			return src
		}
	}
	if s.Src == nil {
		return noSource{}
	}
	return s.Src
}

// noSource answers when nothing can run the server's containers.
type noSource struct{}

func (noSource) Logs(context.Context, string, time.Time, int) (io.ReadCloser, error) {
	return nil, errors.New("no runner for this server's node")
}
func (noSource) AttachStdin(context.Context, string) (io.WriteCloser, error) {
	return nil, errors.New("no runner for this server's node")
}

func (s *Service) init() {
	s.initOnce.Do(func() {
		s.Opts.defaults()
		s.writers = map[string]uint64{}
	})
}

func (s *Service) acquireWriter(bot string, sess uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, held := s.writers[bot]; held && cur != sess {
		return false
	}
	s.writers[bot] = sess
	return true
}

func (s *Service) releaseWriter(bot string, sess uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers[bot] == sess {
		delete(s.writers, bot)
	}
}

// Run serves one session until the client disconnects, the context ends, or
// the client is too slow to keep up (a send fails or times out). The bot keeps
// running regardless of how the session ends.
func (s *Service) Run(ctx context.Context, conn Conn, p Params) error {
	s.init()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	s.nextID++
	sess := s.nextID
	s.mu.Unlock()
	defer s.releaseWriter(p.BotID, sess)

	out := make(chan Outgoing, s.Opts.QueueLen)
	var dropped atomic.Int64
	// enqueue never blocks: a slow client can neither stall the Docker reader
	// nor grow memory. When the queue is full the NEWEST message is dropped and
	// counted; the writer reports the count so the client knows output was lost.
	enqueue := func(m Outgoing) {
		select {
		case out <- m:
		default:
			dropped.Add(1)
		}
	}

	errc := make(chan error, 3)
	wdone := make(chan error, 1)
	drain := make(chan struct{})
	finish := make(chan error, 1) // the bot vanished or access was revoked
	go func() { wdone <- s.writeLoop(ctx, conn, out, &dropped, drain) }()
	go func() { errc <- s.readLoop(ctx, conn, p, sess, enqueue) }()
	go s.streamLoop(ctx, p, enqueue, finish)
	go s.statusLoop(ctx, p, enqueue)
	go s.accessLoop(ctx, p, enqueue, finish)

	select {
	case err := <-errc:
		return err
	case err := <-wdone:
		return err
	case err := <-finish:
		// Let the writer flush what is queued (including the explanation), then stop.
		close(drain)
		select {
		case <-wdone:
		case <-time.After(2 * time.Second):
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) writeLoop(ctx context.Context, conn Conn, out <-chan Outgoing, dropped *atomic.Int64, drain <-chan struct{}) error {
	flush := time.NewTicker(s.Opts.DropFlushEvery)
	defer flush.Stop()
	noticeIfDropped := func() error {
		if n := dropped.Swap(0); n > 0 {
			return conn.Send(ctx, Outgoing{Type: "dropped", Count: n})
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-out:
			if err := noticeIfDropped(); err != nil {
				return err
			}
			if err := conn.Send(ctx, m); err != nil {
				return err
			}
		case <-flush.C:
			if err := noticeIfDropped(); err != nil {
				return err
			}
		case <-drain:
			for {
				select {
				case m := <-out:
					if err := conn.Send(ctx, m); err != nil {
						return err
					}
				default:
					return noticeIfDropped()
				}
			}
		}
	}
}

// bucket is a token bucket for stdin.
type bucket struct {
	tokens float64
	max    float64
	rate   float64
	last   time.Time
}

func (b *bucket) allow(now time.Time) bool {
	if !b.last.IsZero() {
		b.tokens = min(b.max, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Service) readLoop(ctx context.Context, conn Conn, p Params, sess uint64, enqueue func(Outgoing)) error {
	limiter := &bucket{tokens: float64(s.Opts.StdinBurst), max: float64(s.Opts.StdinBurst), rate: s.Opts.StdinRate}
	var stdin io.WriteCloser
	var stdinCID string
	defer func() {
		if stdin != nil {
			stdin.Close() // detaches only; the process's stdin stays open
		}
	}()
	fail := func(code, msg string) { enqueue(Outgoing{Type: "error", Code: code, Message: msg}) }

	for {
		in, err := conn.Receive(ctx)
		if err != nil {
			return err
		}
		switch in.Type {
		case "ping":
			enqueue(Outgoing{Type: "pong"})
		case "stdin":
			switch {
			case !p.CanStdin || (p.AllowStdin != nil && p.AllowStdin(ctx) != nil):
				fail("forbidden", "you are not allowed to send input to this bot")
				continue
			case len(in.Data) == 0 || len(in.Data) > s.Opts.MaxStdinBytes || !utf8.ValidString(in.Data):
				fail("invalid_input", "stdin messages must be valid UTF-8 of 1-"+itoa(s.Opts.MaxStdinBytes)+" bytes")
				continue
			case !limiter.allow(s.Opts.Now()):
				fail("rate_limited", "sending input too fast")
				continue
			case !s.acquireWriter(p.BotID, sess):
				fail("stdin_busy", "another session is currently sending input to this bot")
				continue
			}
			bot, err := p.Refresh(ctx)
			if err != nil || bot.ContainerID == nil || bot.DesiredState != domain.DesiredRunning || bot.ObservedState != "running" {
				fail("not_running", "the bot is not running")
				continue
			}
			if stdin == nil || stdinCID != *bot.ContainerID {
				if stdin != nil {
					stdin.Close()
					stdin = nil
				}
				w, err := s.source(bot.NodeID).AttachStdin(ctx, *bot.ContainerID)
				if err != nil {
					fail("attach_failed", "could not attach to the bot's input")
					continue
				}
				stdin, stdinCID = w, *bot.ContainerID
			}
			if _, err := io.WriteString(stdin, in.Data); err != nil {
				stdin.Close()
				stdin = nil
				fail("write_failed", "could not deliver input")
			}
		default:
			fail("unknown_message", "unknown message type")
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (s *Service) statusOf(b domain.Bot) Outgoing {
	m := Outgoing{Type: "status", DesiredState: b.DesiredState, ObservedState: b.ObservedState,
		Generation: b.Generation, ObservedGeneration: b.ObservedGeneration, ExitCode: b.LastExitCode}
	if b.LastError != nil {
		m.Message = *b.LastError
	}
	return m
}

func (s *Service) statusLoop(ctx context.Context, p Params, enqueue func(Outgoing)) {
	sub := s.Bus.Subscribe(p.BotID)
	defer sub.Close()
	if b, err := p.Refresh(ctx); err == nil {
		enqueue(s.statusOf(b))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-sub.C:
			enqueue(Outgoing{Type: "status", DesiredState: st.DesiredState, ObservedState: st.ObservedState,
				Generation: st.Generation, ObservedGeneration: st.ObservedGeneration, ExitCode: st.ExitCode, Message: st.LastError})
		}
	}
}

// endIfRevoked ends the session when the bot is gone or the user's access was
// revoked (logout, expiry, disabled account). It reports whether it did.
func endIfRevoked(err error, enqueue func(Outgoing), finish chan<- error) bool {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		enqueue(Outgoing{Type: "error", Code: "gone", Message: "the bot no longer exists"})
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, domain.ErrForbidden):
		enqueue(Outgoing{Type: "error", Code: "unauthorized", Message: "your session is no longer valid"})
	default:
		return false
	}
	select {
	case finish <- ErrSessionEnded:
	default:
	}
	return true
}

// accessLoop re-checks authorization periodically so a quiet, healthy stream
// cannot outlive a logout or account change.
func (s *Service) accessLoop(ctx context.Context, p Params, enqueue func(Outgoing), finish chan<- error) {
	t := time.NewTicker(s.Opts.AccessRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.Refresh(ctx); err != nil && endIfRevoked(err, enqueue, finish) {
				return
			}
		}
	}
}

// streamLoop follows the bot's output across container restarts. Delivery is
// at-least-once at reconnect boundaries: Docker offers no durable cursor, so
// the resume point is the last timestamp sent; lines stamped at or before it
// are skipped, and lines that share that exact nanosecond may be repeated or
// (rarely) missed. Output produced while no container existed does not exist.
func (s *Service) streamLoop(ctx context.Context, p Params, enqueue func(Outgoing), finish chan<- error) {
	last := p.Since
	tail := p.Tail
	var finished string // container whose stream already ended
	offline := false    // a node_offline notice was sent and not yet resolved
	wait := func() bool {
		t := time.NewTimer(s.Opts.StatusPoll)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	for ctx.Err() == nil {
		bot, err := p.Refresh(ctx)
		if err != nil {
			if endIfRevoked(err, enqueue, finish) {
				return
			}
			if !wait() {
				return
			}
			continue
		}
		if bot.ContainerID == nil || *bot.ContainerID == finished {
			if !wait() {
				return
			}
			continue
		}
		cid := *bot.ContainerID
		t := tail
		if !last.IsZero() {
			t = 0
		}
		rc, err := s.source(bot.NodeID).Logs(ctx, cid, last, t)
		if errors.Is(err, ErrSourceOffline) {
			// The container may well still run on a disconnected node: say so
			// once and reattach from the last timestamp when it is back.
			if !offline {
				enqueue(Outgoing{Type: "error", Code: "node_offline", Message: "the server's node is offline; output resumes when its agent reconnects"})
				offline = true
			}
			if !wait() {
				return
			}
			continue
		}
		if err != nil {
			finished = cid // container vanished; wait for the runner to publish a new one
			if !wait() {
				return
			}
			continue
		}
		if offline {
			enqueue(Outgoing{Type: "node", Code: "online"})
			offline = false
		}
		s.pump(ctx, rc, &last, enqueue)
		rc.Close()
		tail = 0
		// The stream ended: either the container stopped or the connection to
		// Docker broke. Only give up on this container once it is no longer
		// running; otherwise reattach from the last timestamp.
		if !wait() {
			return
		}
		if cur, err := p.Refresh(ctx); err == nil && cur.ContainerID != nil && *cur.ContainerID == cid && cur.ObservedState == "running" {
			continue
		}
		finished = cid
	}
}

func (s *Service) pump(ctx context.Context, rc io.Reader, last *time.Time, enqueue func(Outgoing)) {
	dec := NewDecoder(rc, true)
	for ctx.Err() == nil {
		f, err := dec.Next()
		if err != nil {
			return
		}
		if !f.Time.IsZero() {
			if !last.IsZero() && !f.Time.After(*last) {
				continue // already delivered before the reconnect
			}
			*last = f.Time
		}
		ts := ""
		if !f.Time.IsZero() {
			ts = f.Time.UTC().Format(time.RFC3339Nano)
		}
		for data := f.Data; len(data) > 0; {
			n := min(len(data), s.Opts.ChunkBytes)
			// Do not split a multi-byte UTF-8 sequence across messages.
			for n < len(data) && n > 0 && !utf8.RuneStart(data[n]) {
				n--
			}
			if n == 0 {
				n = min(len(data), s.Opts.ChunkBytes)
			}
			enqueue(Outgoing{Type: "log", Stream: f.Stream, TS: ts, Data: string(sanitize(data[:n]))})
			data = data[n:]
		}
	}
}

// sanitize makes arbitrary bytes safe for JSON text frames by replacing
// invalid UTF-8 with U+FFFD (log output is display data, not a binary channel).
func sanitize(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	return []byte(string([]rune(string(b))))
}

// Limiter caps concurrent console connections globally, per user and per bot.
type Limiter struct {
	mu                      sync.Mutex
	global, perUser, perBot int
	nGlobal                 int
	nUser, nBot             map[string]int
}

// NewLimiter creates a limiter; zero limits default to 64/8/4.
func NewLimiter(global, perUser, perBot int) *Limiter {
	if global == 0 {
		global = 64
	}
	if perUser == 0 {
		perUser = 8
	}
	if perBot == 0 {
		perBot = 4
	}
	return &Limiter{global: global, perUser: perUser, perBot: perBot, nUser: map[string]int{}, nBot: map[string]int{}}
}

// Active is the number of console connections holding a slot.
func (l *Limiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nGlobal
}

// Acquire reserves a slot and returns its release func, or false when a limit is hit.
func (l *Limiter) Acquire(userID, botID string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nGlobal >= l.global || l.nUser[userID] >= l.perUser || l.nBot[botID] >= l.perBot {
		return nil, false
	}
	l.nGlobal++
	l.nUser[userID]++
	l.nBot[botID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.nGlobal--
			if l.nUser[userID]--; l.nUser[userID] == 0 {
				delete(l.nUser, userID)
			}
			if l.nBot[botID]--; l.nBot[botID] == 0 {
				delete(l.nBot, botID)
			}
		})
	}, true
}

// CanReadLogs is the authorization decision for viewing output. It is
// deliberately separate from CanWriteStdin so the two can diverge.
func CanReadLogs(u domain.User, b domain.Bot) bool { return u.IsAdmin() || b.OwnerID == u.ID }

// CanWriteStdin authorizes sending input to the bot process.
func CanWriteStdin(u domain.User, b domain.Bot) bool { return u.IsAdmin() || b.OwnerID == u.ID }
