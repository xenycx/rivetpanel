package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
)

type fakeConn struct {
	in    chan Incoming
	sent  chan Outgoing
	block chan struct{} // when non-nil, Send blocks until closed (a stalled client)
}

func newConn() *fakeConn {
	return &fakeConn{in: make(chan Incoming, 64), sent: make(chan Outgoing, 100000)}
}

func (c *fakeConn) Send(ctx context.Context, m Outgoing) error {
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.sent <- m
	return nil
}

func (c *fakeConn) Receive(ctx context.Context) (Incoming, error) {
	select {
	case m, ok := <-c.in:
		if !ok {
			return Incoming{}, io.EOF
		}
		return m, nil
	case <-ctx.Done():
		return Incoming{}, ctx.Err()
	}
}

type stdinRec struct {
	mu     sync.Mutex
	data   strings.Builder
	closed int
}

func (s *stdinRec) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Write(b)
}
func (s *stdinRec) Close() error   { s.mu.Lock(); s.closed++; s.mu.Unlock(); return nil }
func (s *stdinRec) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.data.String() }

type fakeSource struct {
	mu       sync.Mutex
	streams  map[string]*io.PipeWriter // container -> writer feeding Logs
	readers  map[string]*io.PipeReader
	logCalls []logCall
	stdin    *stdinRec
	attaches []string
	offline  bool // Logs fails like a disconnected remote node
}

type logCall struct {
	cid   string
	since time.Time
	tail  int
}

func newSource() *fakeSource {
	return &fakeSource{streams: map[string]*io.PipeWriter{}, readers: map[string]*io.PipeReader{}, stdin: &stdinRec{}}
}

func (f *fakeSource) container(cid string) *io.PipeWriter {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, ok := f.streams[cid]; ok {
		return w
	}
	r, w := io.Pipe()
	f.streams[cid], f.readers[cid] = w, r
	return w
}

func (f *fakeSource) Logs(ctx context.Context, cid string, since time.Time, tail int) (io.ReadCloser, error) {
	f.container(cid)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return nil, fmt.Errorf("%w: agent not connected", ErrSourceOffline)
	}
	f.logCalls = append(f.logCalls, logCall{cid, since, tail})
	return f.readers[cid], nil
}

func (f *fakeSource) AttachStdin(ctx context.Context, cid string) (io.WriteCloser, error) {
	f.mu.Lock()
	f.attaches = append(f.attaches, cid)
	f.mu.Unlock()
	return f.stdin, nil
}

func line(stream byte, ts time.Time, text string) []byte {
	return frame(stream, ts.UTC().Format(time.RFC3339Nano)+" "+text)
}

type botBox struct {
	mu sync.Mutex
	b  domain.Bot
}

func (x *botBox) refresh(context.Context) (domain.Bot, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.b, nil
}
func (x *botBox) set(f func(*domain.Bot)) { x.mu.Lock(); f(&x.b); x.mu.Unlock() }

func runningBot(cid string) *botBox {
	return &botBox{b: domain.Bot{ID: "bot1", OwnerID: "u1", DesiredState: "running", ObservedState: "running", ContainerID: &cid, Generation: 1, ObservedGeneration: 1}}
}

type session struct {
	svc    *Service
	conn   *fakeConn
	src    *fakeSource
	bus    *events.Bus
	cancel context.CancelFunc
	done   chan error
}

type cfg struct {
	box  *botBox
	src  *fakeSource
	opts Options
	p    Params
	svc  *Service
	conn *fakeConn
}

func start(t *testing.T, c cfg) *session {
	t.Helper()
	svc := c.svc
	if svc == nil {
		svc = &Service{Src: c.src, Bus: events.NewBus(), Opts: c.opts}
	}
	conn := c.conn
	if conn == nil {
		conn = newConn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := c.p
	p.BotID, p.Refresh = "bot1", c.box.refresh
	s := &session{svc: svc, conn: conn, src: c.src, bus: svc.Bus, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- svc.Run(ctx, conn, p) }()
	t.Cleanup(func() {
		cancel()
		c.src.mu.Lock()
		for _, w := range c.src.streams {
			w.Close()
		}
		c.src.mu.Unlock()
	})
	return s
}

func (s *session) next(t *testing.T, pred func(Outgoing) bool) Outgoing {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-s.conn.sent:
			if pred(m) {
				return m
			}
		case <-deadline:
			t.Fatal("timed out waiting for message")
		}
	}
}

func (s *session) drain() []Outgoing {
	var out []Outgoing
	for {
		select {
		case m := <-s.conn.sent:
			out = append(out, m)
		default:
			return out
		}
	}
}

func ofType(typ string) func(Outgoing) bool { return func(m Outgoing) bool { return m.Type == typ } }
func logWith(sub string) func(Outgoing) bool {
	return func(m Outgoing) bool { return m.Type == "log" && strings.Contains(m.Data, sub) }
}
func errCode(code string) func(Outgoing) bool {
	return func(m Outgoing) bool { return m.Type == "error" && m.Code == code }
}

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func TestStreamsStdoutAndStderrWithIdentityAndInitialStatus(t *testing.T) {
	src := newSource()
	s := start(t, cfg{box: runningBot("c1"), src: src, p: Params{Tail: 50}})
	st := s.next(t, ofType("status"))
	if st.ObservedState != "running" || st.DesiredState != "running" {
		t.Fatalf("%+v", st)
	}
	w := src.container("c1")
	go w.Write(append(line(1, t0, "out line\n"), line(2, t0.Add(time.Second), "err line\n")...))
	a := s.next(t, logWith("out line"))
	b := s.next(t, logWith("err line"))
	if a.Stream != Stdout || b.Stream != Stderr || a.TS == "" || b.TS <= a.TS {
		t.Fatalf("%+v %+v", a, b)
	}
	src.mu.Lock()
	first := src.logCalls[0]
	src.mu.Unlock()
	if first.cid != "c1" || first.tail != 50 || !first.since.IsZero() {
		t.Fatalf("logs call: %+v", first)
	}
}

// A stalled client must (a) never block the reader of Docker's stream, (b) use
// bounded memory, and (c) be told how much output it missed. The accounting is
// exact: delivered + dropped == produced.
func TestSlowClientIsBoundedAndNeverBlocksTheDockerStream(t *testing.T) {
	src := newSource()
	conn := newConn()
	conn.block = make(chan struct{}) // the client has stopped reading
	s := start(t, cfg{box: runningBot("c1"), src: src, opts: Options{QueueLen: 16, DropFlushEvery: 20 * time.Millisecond}, conn: conn})
	w := src.container("c1")

	const produced = 5000
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < produced; i++ {
			// io.Pipe writes block until the consumer reads: if the console
			// coupled its Docker reader to the slow client this would hang.
			if _, err := w.Write(line(1, t0.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("m%d\n", i))); err != nil {
				return
			}
		}
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("Docker stream blocked by a stalled client")
	}
	close(conn.block) // client resumes
	var delivered, dropped int64
	deadline := time.After(5 * time.Second)
	for delivered+dropped < produced {
		select {
		case m := <-conn.sent:
			switch m.Type {
			case "log":
				delivered++
			case "dropped":
				dropped += m.Count
			}
		case <-deadline:
			t.Fatalf("accounting incomplete: delivered=%d dropped=%d of %d", delivered, dropped, produced)
		}
	}
	if delivered+dropped != produced || dropped == 0 || delivered > 16+2 {
		t.Fatalf("delivered=%d dropped=%d produced=%d", delivered, dropped, produced)
	}
	_ = s
}

func TestSlowClientThatNeverRecoversIsDisconnected(t *testing.T) {
	src := newSource()
	conn := newConn()
	failing := &failingConn{fakeConn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &Service{Src: src, Bus: events.NewBus()}
	done := make(chan error, 1)
	box := runningBot("c1")
	go func() { done <- svc.Run(ctx, failing, Params{BotID: "bot1", Refresh: box.refresh}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected write error to end the session")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session survived a failed write")
	}
}

type failingConn struct{ *fakeConn }

func (f *failingConn) Send(context.Context, Outgoing) error { return errors.New("write timeout") }

func TestReconnectResumesFromTimestampWithoutDuplicates(t *testing.T) {
	src := newSource()
	// Docker replays everything at/after "since"; the console must skip lines it already delivered.
	s := start(t, cfg{box: runningBot("c1"), src: src, p: Params{Since: t0.Add(2 * time.Second)}})
	w := src.container("c1")
	go w.Write(append(append(append(
		line(1, t0, "old1\n"),
		line(1, t0.Add(2*time.Second), "boundary\n")...),
		line(1, t0.Add(3*time.Second), "new1\n")...),
		line(1, t0.Add(4*time.Second), "new2\n")...))
	s.next(t, logWith("new1"))
	s.next(t, logWith("new2"))
	for _, m := range s.drain() {
		if m.Type == "log" && (strings.Contains(m.Data, "old1") || strings.Contains(m.Data, "boundary")) {
			t.Fatalf("duplicate delivered: %+v", m)
		}
	}
	src.mu.Lock()
	call := src.logCalls[0]
	src.mu.Unlock()
	if !call.since.Equal(t0.Add(2*time.Second)) || call.tail != 0 {
		t.Fatalf("resume must use since and no tail: %+v", call)
	}
}

func TestFollowsBotAcrossContainerReplacement(t *testing.T) {
	src := newSource()
	box := runningBot("c1")
	s := start(t, cfg{box: box, src: src, opts: Options{StatusPoll: 20 * time.Millisecond}})
	w1 := src.container("c1")
	go w1.Write(line(1, t0, "from first\n"))
	s.next(t, logWith("from first"))

	// The container dies; the runner creates a replacement.
	w1.Close()
	box.set(func(b *domain.Bot) { c := "c2"; b.ContainerID = &c; b.Generation = 2 })
	w2 := src.container("c2")
	go w2.Write(line(1, t0.Add(time.Second), "from second\n"))
	s.next(t, logWith("from second"))

	src.mu.Lock()
	defer src.mu.Unlock()
	last := src.logCalls[len(src.logCalls)-1]
	if last.cid != "c2" || !last.since.Equal(t0) {
		t.Fatalf("second attach should resume after the last timestamp: %+v", last)
	}
}

func TestReattachesWhenStreamBreaksButContainerStillRuns(t *testing.T) {
	src := newSource()
	box := runningBot("c1")
	s := start(t, cfg{box: box, src: src, opts: Options{StatusPoll: 20 * time.Millisecond}})
	w := src.container("c1")
	go w.Write(line(1, t0, "one\n"))
	s.next(t, logWith("one"))
	// Simulate a broken Docker connection: the pipe is replaced for the next attach.
	src.mu.Lock()
	r, nw := io.Pipe()
	src.readers["c1"], src.streams["c1"] = r, nw
	src.mu.Unlock()
	w.Close()
	go nw.Write(line(1, t0.Add(time.Second), "two\n"))
	s.next(t, logWith("two"))
}

// A remote node going offline must not make the console give up on the
// container: it reports the outage once and reattaches when the node is back.
func TestOfflineNodeKeepsWaitingForTheSameContainer(t *testing.T) {
	src := newSource()
	box := runningBot("c1")
	s := start(t, cfg{box: box, src: src, opts: Options{StatusPoll: 20 * time.Millisecond}})
	w := src.container("c1")
	go w.Write(line(1, t0, "one\n"))
	s.next(t, logWith("one"))
	src.mu.Lock()
	src.offline = true
	r, nw := io.Pipe()
	src.readers["c1"], src.streams["c1"] = r, nw
	src.mu.Unlock()
	w.Close() // the agent connection dropped
	s.next(t, errCode("node_offline"))
	time.Sleep(100 * time.Millisecond) // several polls while offline
	for _, m := range s.drain() {
		if m.Type == "error" {
			t.Fatalf("offline reported more than once: %+v", m)
		}
	}
	src.mu.Lock()
	src.offline = false
	src.mu.Unlock()
	s.next(t, func(m Outgoing) bool { return m.Type == "node" && m.Code == "online" })
	go nw.Write(line(1, t0.Add(time.Second), "two\n"))
	s.next(t, logWith("two"))
	src.mu.Lock()
	last := src.logCalls[len(src.logCalls)-1]
	src.mu.Unlock()
	if last.cid != "c1" || !last.since.Equal(t0) {
		t.Fatalf("reattach = %+v, want c1 since the last delivered line", last)
	}
}

func TestStatusEventsAreForwarded(t *testing.T) {
	src := newSource()
	svc := &Service{Src: src, Bus: events.NewBus()}
	s := start(t, cfg{box: runningBot("c1"), src: src, svc: svc})
	s.next(t, ofType("status"))
	waitSubscribed(t, svc.Bus)
	code := int64(137)
	svc.Bus.Publish(events.Status{BotID: "bot1", DesiredState: "running", ObservedState: "failed", Generation: 3, ExitCode: &code, LastError: "killed: out of memory"})
	m := s.next(t, func(m Outgoing) bool { return m.Type == "status" && m.ObservedState == "failed" })
	if m.ExitCode == nil || *m.ExitCode != 137 || m.Message != "killed: out of memory" || m.Generation != 3 {
		t.Fatalf("%+v", m)
	}
}

func waitSubscribed(t *testing.T, b *events.Bus) {
	t.Helper()
	for i := 0; i < 200 && b.Subscribers("bot1") == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if b.Subscribers("bot1") == 0 {
		t.Fatal("status loop never subscribed")
	}
}

func TestStdinDeliveredToRunningBotOnly(t *testing.T) {
	src := newSource()
	box := runningBot("c1")
	s := start(t, cfg{box: box, src: src, p: Params{CanStdin: true}})
	s.conn.in <- Incoming{Type: "stdin", Data: "hello\n"}
	s.conn.in <- Incoming{Type: "stdin", Data: "world\n"}
	waitFor(t, func() bool { return src.stdin.String() == "hello\nworld\n" })
	if len(src.attaches) != 1 {
		t.Fatalf("attached %d times", len(src.attaches))
	}
	box.set(func(b *domain.Bot) { b.ObservedState = "failed" })
	s.conn.in <- Incoming{Type: "stdin", Data: "late\n"}
	s.next(t, errCode("not_running"))
	if strings.Contains(src.stdin.String(), "late") {
		t.Fatal("input delivered to a bot that is not running")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestStdinRequiresSeparateAuthorization(t *testing.T) {
	src := newSource()
	s := start(t, cfg{box: runningBot("c1"), src: src, p: Params{CanStdin: false}})
	s.conn.in <- Incoming{Type: "stdin", Data: "x"}
	s.next(t, errCode("forbidden"))
	if src.stdin.String() != "" || len(src.attaches) != 0 {
		t.Fatal("input reached the bot without stdin permission")
	}
	// Reading logs still works for a read-only session.
	go src.container("c1").Write(line(1, t0, "visible\n"))
	s.next(t, logWith("visible"))

	owner := domain.User{ID: "u1"}
	other := domain.User{ID: "u2"}
	admin := domain.User{ID: "a", Role: domain.RoleAdmin}
	b := domain.Bot{OwnerID: "u1"}
	if !CanReadLogs(owner, b) || !CanWriteStdin(owner, b) || !CanReadLogs(admin, b) || !CanWriteStdin(admin, b) ||
		CanReadLogs(other, b) || CanWriteStdin(other, b) {
		t.Fatal("authorization functions wrong")
	}
}

func TestSingleWriterAcrossSessions(t *testing.T) {
	src := newSource()
	svc := &Service{Src: src, Bus: events.NewBus()}
	box := runningBot("c1")
	a := start(t, cfg{box: box, src: src, svc: svc, p: Params{CanStdin: true}})
	b := start(t, cfg{box: box, src: src, svc: svc, p: Params{CanStdin: true}})
	a.conn.in <- Incoming{Type: "stdin", Data: "from-a\n"}
	waitFor(t, func() bool { return strings.Contains(src.stdin.String(), "from-a") })
	b.conn.in <- Incoming{Type: "stdin", Data: "from-b\n"}
	b.next(t, errCode("stdin_busy"))
	if strings.Contains(src.stdin.String(), "from-b") {
		t.Fatal("second writer got through")
	}
	// Closing A releases the writer role; the bot keeps running.
	close(a.conn.in)
	<-a.done
	waitFor(t, func() bool {
		b.conn.in <- Incoming{Type: "stdin", Data: "from-b\n"}
		time.Sleep(10 * time.Millisecond)
		return strings.Contains(src.stdin.String(), "from-b")
	})
	if src.stdin.closed == 0 {
		t.Fatal("stdin handle of the closed session was not detached")
	}
}

func TestStdinValidationAndRateLimit(t *testing.T) {
	src := newSource()
	now := t0
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	s := start(t, cfg{box: runningBot("c1"), src: src, p: Params{CanStdin: true},
		opts: Options{StdinBurst: 3, StdinRate: 1, MaxStdinBytes: 8, Now: clock}})
	s.conn.in <- Incoming{Type: "stdin", Data: ""}
	s.next(t, errCode("invalid_input"))
	s.conn.in <- Incoming{Type: "stdin", Data: "123456789"}
	s.next(t, errCode("invalid_input"))
	s.conn.in <- Incoming{Type: "stdin", Data: "\xff\xfe"}
	s.next(t, errCode("invalid_input"))
	s.conn.in <- Incoming{Type: "nonsense"}
	s.next(t, errCode("unknown_message"))

	for i := 0; i < 3; i++ { // burst of 3 passes
		s.conn.in <- Incoming{Type: "stdin", Data: "x"}
	}
	waitFor(t, func() bool { return src.stdin.String() == "xxx" })
	s.conn.in <- Incoming{Type: "stdin", Data: "x"}
	s.next(t, errCode("rate_limited"))
	mu.Lock()
	now = now.Add(2 * time.Second) // tokens refill
	mu.Unlock()
	s.conn.in <- Incoming{Type: "stdin", Data: "y"}
	waitFor(t, func() bool { return strings.HasSuffix(src.stdin.String(), "y") })
	s.conn.in <- Incoming{Type: "ping"}
	s.next(t, ofType("pong"))
}

func TestSessionEndCleansUp(t *testing.T) {
	src := newSource()
	svc := &Service{Src: src, Bus: events.NewBus()}
	s := start(t, cfg{box: runningBot("c1"), src: src, svc: svc, p: Params{CanStdin: true}})
	s.next(t, ofType("status"))
	waitSubscribed(t, svc.Bus)
	close(s.conn.in) // browser disconnects
	select {
	case err := <-s.done:
		if !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after disconnect")
	}
	waitFor(t, func() bool { return svc.Bus.Subscribers("bot1") == 0 })
}

func TestBotDeletedDuringSession(t *testing.T) {
	src := newSource()
	box := &botBox{b: domain.Bot{ID: "bot1"}}
	var calls atomic.Int32
	refresh := func(ctx context.Context) (domain.Bot, error) {
		if calls.Add(1) > 1 {
			return domain.Bot{}, domain.ErrNotFound
		}
		return box.b, nil
	}
	svc := &Service{Src: src, Bus: events.NewBus(), Opts: Options{StatusPoll: 10 * time.Millisecond}}
	conn := newConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx, conn, Params{BotID: "bot1", Refresh: refresh})
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-conn.sent:
			if m.Type == "error" && m.Code == "gone" {
				return
			}
		case <-deadline:
			t.Fatal("no 'gone' notice")
		}
	}
}

func TestLargeFramesAreChunkedOnRuneBoundaries(t *testing.T) {
	src := newSource()
	s := start(t, cfg{box: runningBot("c1"), src: src, opts: Options{ChunkBytes: 10}})
	text := strings.Repeat("é", 12) + "\n" // 2-byte runes: a naive split would cut them
	go src.container("c1").Write(line(1, t0, text))
	var got strings.Builder
	for got.Len() < len(text) {
		m := s.next(t, ofType("log"))
		if len(m.Data) > 10 || strings.ContainsRune(m.Data, '\uFFFD') {
			t.Fatalf("bad chunk %q", m.Data)
		}
		got.WriteString(m.Data)
	}
	if got.String() != text {
		t.Fatalf("reassembled %q", got.String())
	}
}

func TestBinaryOutputIsSanitized(t *testing.T) {
	src := newSource()
	s := start(t, cfg{box: runningBot("c1"), src: src})
	go src.container("c1").Write(line(1, t0, "ok\xff\xfe\n"))
	m := s.next(t, logWith("ok"))
	if !strings.Contains(m.Data, "\uFFFD") {
		t.Fatalf("%q", m.Data)
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, 2, 2)
	r1, ok1 := l.Acquire("u1", "b1")
	r2, ok2 := l.Acquire("u1", "b2")
	_, ok3 := l.Acquire("u1", "b3") // per-user limit
	if !ok1 || !ok2 || ok3 {
		t.Fatal(ok1, ok2, ok3)
	}
	r3, ok4 := l.Acquire("u2", "b1")
	_, ok5 := l.Acquire("u3", "b1") // per-bot limit (b1 has 2)
	if !ok4 || ok5 {
		t.Fatal(ok4, ok5)
	}
	_, ok6 := l.Acquire("u4", "b9") // global limit (3 in use)
	if ok6 {
		t.Fatal("global limit not enforced")
	}
	r1()
	r1() // idempotent
	if _, ok := l.Acquire("u4", "b9"); !ok {
		t.Fatal("slot not released")
	}
	r2()
	r3()
}

func TestRevokedAccessEndsAQuietStream(t *testing.T) {
	src := newSource()
	var revoked atomic.Bool
	box := runningBot("c1")
	refresh := func(ctx context.Context) (domain.Bot, error) {
		if revoked.Load() {
			return domain.Bot{}, domain.ErrUnauthorized
		}
		return box.refresh(ctx)
	}
	svc := &Service{Src: src, Bus: events.NewBus(), Opts: Options{AccessRecheck: 20 * time.Millisecond}}
	conn := newConn()
	done := make(chan error, 1)
	go func() { done <- svc.Run(context.Background(), conn, Params{BotID: "bot1", Refresh: refresh}) }()
	time.Sleep(60 * time.Millisecond) // stream is open and silent
	revoked.Store(true)
	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionEnded) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("silent stream outlived revoked access")
	}
	found := false
	for len(conn.sent) > 0 {
		if m := <-conn.sent; m.Type == "error" && m.Code == "unauthorized" {
			found = true
		}
	}
	if !found {
		t.Fatal("client was not told why the session ended")
	}
}
