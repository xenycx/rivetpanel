package sitehost

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type ipResolver struct{ ip string }

func (r ipResolver) ContainerIP(context.Context, string, string) (string, error) { return r.ip, nil }

func appSite(t *testing.T, upstream http.Handler, state string, maxBody int64) (*Handler, string) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	p, _ := strconv.Atoi(port)
	h := &Handler{
		Sites: &fakeRouter{route: domain.SiteRoute{SiteID: "s", Mode: domain.SiteModeApp, AppID: "a", ContainerID: "c1",
			AppState: state, AppPort: p, MaxBody: maxBody}},
		Proxy: &Proxy{Apps: ipResolver{"127.0.0.1"}, Network: "apps", Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			ClientIPHeader: "CF-Connecting-IP"},
	}
	return h, up.URL
}

func echo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Host", r.Host)
	w.Header().Set("X-Got-For", r.Header.Get("X-Forwarded-For"))
	w.Header().Set("X-Got-Real", r.Header.Get("X-Real-IP"))
	w.Header().Set("X-Got-Proto", r.Header.Get("X-Forwarded-Proto"))
	w.Header().Set("X-Got-Rivet", r.Header.Get("X-Rivet-Secret"))
	b, _ := io.ReadAll(r.Body)
	_, _ = w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " " + string(b)))
}

func TestProxyForwardsAndSanitises(t *testing.T) {
	h, _ := appSite(t, http.HandlerFunc(echo), "running", 0)

	// From an untrusted peer: spoofed headers are replaced by the peer.
	r := httptest.NewRequest(http.MethodPost, "http://map.example.com/api/x?q=1", strings.NewReader("body"))
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Rivet-Secret", "x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "POST /api/x?q=1 body" {
		t.Fatalf("proxied: %d %q", w.Code, w.Body.String())
	}
	got := w.Header()
	if got.Get("X-Host") != "map.example.com" || got.Get("X-Got-For") != "203.0.113.9" || got.Get("X-Got-Real") != "203.0.113.9" ||
		got.Get("X-Got-Proto") != "http" || got.Get("X-Got-Rivet") != "" {
		t.Fatalf("untrusted peer headers: %v", got)
	}
	if got.Get("X-Content-Type-Options") != "" {
		t.Error("the listener must not add headers to an app's responses")
	}

	// From the trusted TLS proxy: its client header and scheme are believed.
	r = httptest.NewRequest(http.MethodGet, "http://map.example.com/", nil)
	r.RemoteAddr = "10.1.2.3:4444"
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-Got-For") != "198.51.100.7" || w.Header().Get("X-Got-Proto") != "https" {
		t.Fatalf("trusted peer headers: %v", w.Header())
	}

	// Without the client header, the right-most untrusted X-Forwarded-For.
	h.Proxy.ClientIPHeader = ""
	r = httptest.NewRequest(http.MethodGet, "http://map.example.com/", nil)
	r.RemoteAddr = "10.1.2.3:4444"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.8, 10.9.9.9")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-Got-For") != "198.51.100.8" {
		t.Fatalf("x-forwarded-for chain: %v", w.Header())
	}
}

func TestProxyLimitsAndStates(t *testing.T) {
	h, _ := appSite(t, http.HandlerFunc(echo), "running", 8)
	r := httptest.NewRequest(http.MethodPost, "http://a.example.com/", strings.NewReader("0123456789"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared length over the limit: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "http://a.example.com/", io.MultiReader(strings.NewReader("0123456789")))
	r.ContentLength = -1
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed body over the limit: %d", w.Code)
	}

	for state, want := range map[string]string{"building": "Deploying", "starting": "Deploying", "stopped": "not running", "failed": "not running"} {
		h, _ := appSite(t, http.HandlerFunc(echo), state, 0)
		r := httptest.NewRequest(http.MethodGet, "http://a.example.com/", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %q", state, w.Code, w.Body.String())
		}
		if want == "Deploying" && (w.Header().Get("Retry-After") == "" || w.Header().Get("Refresh") == "") {
			t.Errorf("%s: deploying page must ask to retry: %v", state, w.Header())
		}
	}

	// Nothing listening: a 502 after the (shortened) retry window.
	old := dialRetryFor
	dialRetryFor = 300 * time.Millisecond
	defer func() { dialRetryFor = old }()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	h = &Handler{Sites: &fakeRouter{route: domain.SiteRoute{Mode: domain.SiteModeApp, ContainerID: "c2", AppState: "running", AppPort: port}},
		Proxy: &Proxy{Apps: ipResolver{"127.0.0.1"}}}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://a.example.com/", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("no upstream: %d", w.Code)
	}
}

// Through a real listener: WebSocket upgrades and server-sent events.
func TestProxyUpgradeAndStreaming(t *testing.T) {
	upstream := http.NewServeMux()
	upstream.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "no upgrade", 400)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo:" + line)
		_ = rw.Flush()
	})
	upstream.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	h, _ := appSite(t, upstream, "running", 0)
	front := httptest.NewServer(h)
	defer front.Close()
	addr := strings.TrimPrefix(front.URL, "http://")

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET /ws HTTP/1.1\r\nHost: a.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", res, err)
	}
	_, _ = io.WriteString(c, "hello\n")
	if line, _ := br.ReadString('\n'); line != "echo:hello\n" {
		t.Fatalf("websocket echo: %q", line)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+"/events", nil)
	req.Host = "a.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: one\n" {
		t.Fatalf("event stream not flushed: %q %v", line, err)
	}
}
