package api

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// wsSource is a fake Docker log/stdin source.
type wsSource struct {
	mu     sync.Mutex
	pipes  map[string]*io.PipeWriter
	reads  map[string]*io.PipeReader
	stdin  strings.Builder
	closes int
}

func newWSSource() *wsSource {
	return &wsSource{pipes: map[string]*io.PipeWriter{}, reads: map[string]*io.PipeReader{}}
}

func (s *wsSource) writer(cid string) *io.PipeWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.pipes[cid]; ok {
		return w
	}
	r, w := io.Pipe()
	s.pipes[cid], s.reads[cid] = w, r
	return w
}

func (s *wsSource) Logs(_ context.Context, cid string, _ time.Time, _ int) (io.ReadCloser, error) {
	s.writer(cid)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[cid], nil
}

func (s *wsSource) AttachStdin(context.Context, string) (io.WriteCloser, error) {
	return stdinW{s}, nil
}

func (s *wsSource) stdinText() string { s.mu.Lock(); defer s.mu.Unlock(); return s.stdin.String() }

type stdinW struct{ s *wsSource }

func (w stdinW) Write(b []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.s.stdin.Write(b)
}
func (w stdinW) Close() error { w.s.mu.Lock(); w.s.closes++; w.s.mu.Unlock(); return nil }

func dockerFrame(stream byte, text string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(text)))
	return append(h, text...)
}

// serve starts the app on a real port and returns its host:port.
func serve(t *testing.T, e *env) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go e.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	t.Cleanup(func() {
		// A console WebSocket is hijacked from the HTTP server, so Shutdown
		// does not wait for it. Its handler keeps re-reading the session from
		// the database until it notices the client went away; wait for every
		// session to end so the database and temporary directory are not
		// closed and removed underneath it (this made the suite flaky).
		e.waitConsolesClosed(t)
		e.app.Shutdown()
	})
	return ln.Addr().String()
}

func (e *env) waitConsolesClosed(t *testing.T) {
	t.Helper()
	if e.consoleLimit == nil {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.consoleLimit.Active() > 0 {
		if time.Now().After(deadline) {
			t.Errorf("%d console session(s) still open after the test", e.consoleLimit.Active())
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) runningBot(c *client, cid string) string {
	id := c.createBot("b")
	e.db.Exec(`UPDATE bots SET container_id = ?, desired_state = 'running', observed_state = 'running', generation = 1, observed_generation = 1 WHERE id = ?`, cid, id)
	return id
}

func dial(t *testing.T, addr, path string, c *client, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if c != nil && c.cookie != "" {
		h.Set("Cookie", sessionCookie+"="+c.cookie)
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	d := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	return d.Dial("ws://"+addr+path, h)
}

func readMsg(t *testing.T, ws *websocket.Conn, pred func(m map[string]any) bool) map[string]any {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m map[string]any
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if pred(m) {
			return m
		}
	}
}

func typ(s string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == s }
}

func TestConsoleHandshakeRejections(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	mallory := e.user("mallory@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	path := "/api/v1/bots/" + id + "/console"

	status := func(c *client, origin, p string) int {
		_, resp, err := dial(t, addr, p, c, origin)
		if err == nil {
			t.Fatalf("handshake unexpectedly succeeded for %s", p)
		}
		return resp.StatusCode
	}
	if got := status(nil, "", path); got != 401 {
		t.Errorf("anonymous: %d", got)
	}
	if got := status(mallory, "", path); got != 404 {
		t.Errorf("other user must not learn the bot exists: %d", got)
	}
	if got := status(alice, "https://evil.example", path); got != 403 {
		t.Errorf("foreign origin: %d", got)
	}
	if got := status(alice, "", "/api/v1/bots/not-a-uuid/console"); got != 404 {
		t.Errorf("bad id: %d", got)
	}
	if got := status(alice, "", path+"?tail=99999"); got != 400 {
		t.Errorf("bad tail: %d", got)
	}
	if got := status(alice, "", path+"?since=yesterday"); got != 400 {
		t.Errorf("bad since: %d", got)
	}
	// plain HTTP GET (no upgrade) is refused
	if resp, _ := alice.do("GET", path, nil); resp.StatusCode != 426 {
		t.Errorf("non-upgrade: %d", resp.StatusCode)
	}
}

func TestConsoleConnectionCaps(t *testing.T) {
	e := newEnv(t) // per-bot cap is 2
	alice := e.user("alice@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	path := "/api/v1/bots/" + id + "/console"
	var conns []*websocket.Conn
	for i := 0; i < 2; i++ {
		ws, _, err := dial(t, addr, path, alice, "")
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, ws)
	}
	if _, resp, err := dial(t, addr, path, alice, ""); err == nil || resp.StatusCode != 429 {
		t.Fatalf("third connection: %v %v", err, resp)
	}
	conns[0].Close() // releasing a slot admits a new connection
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws, _, err := dial(t, addr, path, alice, "")
		if err == nil {
			ws.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot never released")
		}
		time.Sleep(50 * time.Millisecond)
	}
	conns[1].Close()
}

func TestConsoleStreamsLogsAcceptsStdinAndKeepsBotAlive(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console", alice, "")
	if err != nil {
		t.Fatal(err)
	}
	st := readMsg(t, ws, typ("status"))
	if st["observed_state"] != "running" {
		t.Fatalf("%v", st)
	}
	w := e.src.writer("c1")
	go w.Write(append(dockerFrame(1, "2026-09-30T10:00:00.000000001Z hello stdout\n"), dockerFrame(2, "2026-09-30T10:00:01Z boom stderr\n")...))
	a := readMsg(t, ws, func(m map[string]any) bool { return m["type"] == "log" && m["stream"] == "stdout" })
	b := readMsg(t, ws, func(m map[string]any) bool { return m["type"] == "log" && m["stream"] == "stderr" })
	if a["data"] != "hello stdout\n" || b["data"] != "boom stderr\n" {
		t.Fatalf("%v %v", a, b)
	}

	ws.WriteJSON(map[string]string{"type": "stdin", "data": "!ping\n"})
	deadline := time.Now().Add(3 * time.Second)
	for e.src.stdinText() != "!ping\n" {
		if time.Now().After(deadline) {
			t.Fatal("stdin not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// oversized frames are refused by the transport read limit and end the session
	ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"stdin","data":"`+strings.Repeat("x", 20000)+`"}`))
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}

	// Disconnecting the browser never stops the bot.
	var state string
	e.db.QueryRow(`SELECT observed_state FROM bots WHERE id = ?`, id).Scan(&state)
	if state != "running" || e.rec.count() != 0 {
		t.Fatalf("console disconnect affected the bot: %s notified=%d", state, e.rec.count())
	}
}

func TestConsoleStdinCanBeDisabledPerConnection(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console?stdin=0", alice, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.WriteJSON(map[string]string{"type": "stdin", "data": "nope\n"})
	m := readMsg(t, ws, typ("error"))
	if m["code"] != "forbidden" || e.src.stdinText() != "" {
		t.Fatalf("%v stdin=%q", m, e.src.stdinText())
	}
}

func TestConsoleAdminMayObserveOthersBots(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console", admin, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readMsg(t, ws, typ("status"))
}

func TestConsoleEndsWhenSessionIsRevoked(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console", alice, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readMsg(t, ws, typ("status"))
	alice.mustStatus(204, "POST", "/api/v1/auth/logout", nil)
	m := readMsg(t, ws, typ("error"))
	if m["code"] != "unauthorized" {
		t.Fatalf("%v", m)
	}
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				t.Fatalf("expected policy-violation close, got %v", err)
			}
			break
		}
	}
}

func TestConsoleForwardsStatusChangesFromLifecycleActions(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	id := alice.createBot("b")
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console", alice, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readMsg(t, ws, typ("status"))
	for i := 0; i < 100 && e.bus.Subscribers(id) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	alice.mustStatus(202, "POST", "/api/v1/bots/"+id+"/start", nil)
	m := readMsg(t, ws, func(m map[string]any) bool { return m["type"] == "status" && m["desired_state"] == "running" })
	if m["generation"] != float64(1) {
		t.Fatalf("%v", m)
	}
}

func TestConsoleStdinFollowsCurrentPermission(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@example.com", domain.RoleUser)
	bob := e.user("bob@example.com", domain.RoleUser)
	id := e.runningBot(alice, "c1")
	alice.mustStatus(200, "PUT", "/api/v1/bots/"+id+"/users", map[string]any{"email": "bob@example.com", "permissions": domain.PermViewConsole | domain.PermPower})
	addr := serve(t, e)
	ws, _, err := dial(t, addr, "/api/v1/bots/"+id+"/console", bob, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	readMsg(t, ws, typ("status"))
	ws.WriteJSON(map[string]string{"type": "stdin", "data": "one\n"})
	deadline := time.Now().Add(3 * time.Second)
	for e.src.stdinText() != "one\n" {
		if time.Now().After(deadline) {
			t.Fatal("first line not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The owner removes the power permission but keeps console viewing.
	alice.mustStatus(200, "PUT", "/api/v1/bots/"+id+"/users", map[string]any{"email": "bob@example.com", "permissions": domain.PermViewConsole})
	ws.WriteJSON(map[string]string{"type": "stdin", "data": "two\n"})
	m := readMsg(t, ws, typ("error"))
	if m["code"] != "forbidden" || e.src.stdinText() != "one\n" {
		t.Fatalf("input after losing power: %v stdin=%q", m, e.src.stdinText())
	}
	// The viewing session itself continues.
	go e.src.writer("c1").Write(dockerFrame(1, "2026-09-30T10:00:02Z still here\n"))
	if l := readMsg(t, ws, typ("log")); l["data"] != "still here\n" {
		t.Fatalf("%v", l)
	}
}
