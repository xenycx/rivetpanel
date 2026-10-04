//go:build integration

package integration

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/console"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
)

type chanConn struct {
	in    chan console.Incoming
	sent  chan console.Outgoing
	block chan struct{}
}

func newChanConn() *chanConn {
	return &chanConn{in: make(chan console.Incoming, 64), sent: make(chan console.Outgoing, 1<<16)}
}

func (c *chanConn) Send(ctx context.Context, m console.Outgoing) error {
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case c.sent <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *chanConn) Receive(ctx context.Context) (console.Incoming, error) {
	select {
	case m, ok := <-c.in:
		if !ok {
			return console.Incoming{}, io.EOF
		}
		return m, nil
	case <-ctx.Done():
		return console.Incoming{}, ctx.Err()
	}
}

type liveSession struct {
	conn *chanConn
	done chan error
	stop context.CancelFunc
}

func (s *stack) console(botID string, since time.Time, conn *chanConn, tail int) *liveSession {
	svc := &console.Service{Src: s.dk, Bus: events.NewBus(), Opts: console.Options{StatusPoll: 200 * time.Millisecond}}
	ctx, cancel := context.WithCancel(s.ctx)
	ls := &liveSession{conn: conn, done: make(chan error, 1), stop: cancel}
	go func() {
		ls.done <- svc.Run(ctx, conn, console.Params{
			BotID:    botID,
			CanStdin: true,
			Since:    since,
			Tail:     tail,
			Refresh:  func(ctx context.Context) (domain.Bot, error) { return s.bots.Get(ctx, s.user, botID) },
		})
	}()
	s.t.Cleanup(cancel)
	return ls
}

func (l *liveSession) expect(t *testing.T, what string, pred func(console.Outgoing) bool) console.Outgoing {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case m := <-l.conn.sent:
			if pred(m) {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func logLine(stream console.Stream, sub string) func(console.Outgoing) bool {
	return func(m console.Outgoing) bool {
		return m.Type == "log" && m.Stream == stream && strings.Contains(m.Data, sub)
	}
}

func (l *liveSession) anyLog(sub string) bool {
	for {
		select {
		case m := <-l.conn.sent:
			if m.Type == "log" && strings.Contains(m.Data, sub) {
				return true
			}
		default:
			return false
		}
	}
}

const echoBot = `
process.stdin.setEncoding('utf8');
let buf = '';
process.stdin.on('data', d => {
  buf += d; let i;
  while ((i = buf.indexOf('\n')) >= 0) {
    const l = buf.slice(0, i); buf = buf.slice(i + 1);
    console.log('ECHO:' + l); console.error('ERRECHO:' + l);
  }
});
process.stdin.on('end', () => console.log('STDIN-EOF'));
console.log('READY');
setInterval(() => {}, 1000);
`

func TestConsoleLiveLogsStdinReconnectAndRestart(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", echoBot)
	s.bots.Start(s.ctx, s.user, b.ID)
	s.waitObserved(b.ID, "running", 5*time.Minute)

	// Session 1: output, stream identity and stdin round trip.
	c1 := newChanConn()
	s1 := s.console(b.ID, time.Time{}, c1, 100)
	s1.expect(t, "READY on stdout", logLine(console.Stdout, "READY"))
	c1.in <- console.Incoming{Type: "stdin", Data: "hello\n"}
	out := s1.expect(t, "ECHO on stdout", logLine(console.Stdout, "ECHO:hello"))
	s1.expect(t, "ERRECHO on stderr", logLine(console.Stderr, "ERRECHO:hello"))

	// Browser closes: the bot and its stdin survive.
	resume, _ := time.Parse(time.RFC3339Nano, out.TS)
	close(c1.in)
	<-s1.done
	time.Sleep(2 * time.Second)
	if s.get(b.ID).ObservedState != "running" {
		t.Fatal("bot stopped when the console disconnected")
	}
	if cid := s.get(b.ID).ContainerID; cid == nil || strings.Contains(s.logs(*cid), "STDIN-EOF") {
		t.Fatal("stdin was closed by the disconnect")
	}

	// Session 2 resumes after the last seen timestamp: no replay of old lines, stdin still works.
	c2 := newChanConn()
	s2 := s.console(b.ID, resume, c2, 100)
	c2.in <- console.Incoming{Type: "stdin", Data: "again\n"}
	s2.expect(t, "ECHO:again", logLine(console.Stdout, "ECHO:again"))
	if s2.anyLog("READY") || s2.anyLog("ECHO:hello") {
		t.Fatal("lines from before the resume point were delivered again")
	}

	// A restart replaces the container; the same session follows to the new one.
	old := *s.get(b.ID).ContainerID
	s.bots.Restart(s.ctx, s.user, b.ID)
	s2.expect(t, "READY from replacement container", logLine(console.Stdout, "READY"))
	if *s.get(b.ID).ContainerID == old {
		// The restart may not have been observed yet; wait for it.
		s.waitFor("replacement observed", time.Minute, func() bool { c := s.get(b.ID).ContainerID; return c != nil && *c != old })
	}
	c2.in <- console.Incoming{Type: "stdin", Data: "after-restart\n"}
	s2.expect(t, "stdin reaches the new container", logLine(console.Stdout, "ECHO:after-restart"))
	s2.stop()
}

func TestConsoleStdinDeniedForReadOnlySessionAndSingleWriter(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	s.write(b, "index.js", echoBot)
	s.bots.Start(s.ctx, s.user, b.ID)
	s.waitObserved(b.ID, "running", 5*time.Minute)

	svc := &console.Service{Src: s.dk, Bus: events.NewBus()}
	run := func(canStdin bool) *chanConn {
		conn := newChanConn()
		ctx, cancel := context.WithCancel(s.ctx)
		t.Cleanup(cancel)
		go svc.Run(ctx, conn, console.Params{BotID: b.ID, CanStdin: canStdin,
			Refresh: func(ctx context.Context) (domain.Bot, error) { return s.bots.Get(ctx, s.user, b.ID) }})
		return conn
	}
	ro, w1, w2 := run(false), run(true), run(true)
	ro.in <- console.Incoming{Type: "stdin", Data: "sneaky\n"}
	deadline := time.After(20 * time.Second)
	for got := false; !got; {
		select {
		case m := <-ro.sent:
			got = m.Type == "error" && m.Code == "forbidden"
		case <-deadline:
			t.Fatal("read-only session was not refused")
		}
	}
	w1.in <- console.Incoming{Type: "stdin", Data: "first\n"}
	cid := *s.get(b.ID).ContainerID
	s.waitLog(cid, "ECHO:first", 30*time.Second)
	w2.in <- console.Incoming{Type: "stdin", Data: "second\n"}
	for got := false; !got; {
		select {
		case m := <-w2.sent:
			got = m.Type == "error" && m.Code == "stdin_busy"
		case <-time.After(20 * time.Second):
			t.Fatal("second writer was not refused")
		}
	}
	if l := s.logs(cid); strings.Contains(l, "sneaky") || strings.Contains(l, "ECHO:second") {
		t.Fatal("refused input reached the process")
	}
}

func TestConsoleSlowClientAgainstNoisyBotIsBounded(t *testing.T) {
	s := newStack(t)
	b := s.createBot("nodejs")
	const lines = 20000
	s.write(b, "index.js", `
for (let i = 0; i < `+"20000"+`; i++) console.log('L' + i);
console.log('DONE');
setInterval(() => {}, 1000);
`)
	conn := newChanConn()
	conn.block = make(chan struct{}) // the client is stalled from the start
	s.bots.Start(s.ctx, s.user, b.ID)
	cid := ""
	s.waitFor("noisy bot running", 5*time.Minute, func() bool {
		c := s.get(b.ID)
		if c.ContainerID != nil {
			cid = *c.ContainerID
		}
		return c.ObservedState == "running"
	})
	// The bot finishes its output regardless of the stalled console client.
	s.waitLog(cid, "DONE", 60*time.Second)
	s.console(b.ID, time.Time{}, conn, 0) // tail 0 = replay everything
	time.Sleep(3 * time.Second)           // the console drains Docker's stream while the client is stalled
	close(conn.block)

	var delivered, dropped int64
	deadline := time.After(60 * time.Second)
	for delivered+dropped < lines+1 {
		select {
		case m := <-conn.sent:
			switch m.Type {
			case "log":
				delivered++
			case "dropped":
				dropped += m.Count
			}
		case <-deadline:
			t.Fatalf("accounting incomplete: delivered=%d dropped=%d", delivered, dropped)
		}
	}
	// Every produced line is either delivered or reported as dropped, and the
	// stalled client only ever held a bounded queue's worth.
	if delivered+dropped != lines+1 || dropped == 0 || delivered > 128+2 {
		t.Fatalf("delivered=%d dropped=%d want total %d", delivered, dropped, lines+1)
	}
	t.Logf("delivered=%d dropped=%d", delivered, dropped)
}
