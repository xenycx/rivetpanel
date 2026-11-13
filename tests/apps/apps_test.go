//go:build apps

// Package apps installs one-click app templates on a running panel through
// its public API and checks that each one really works: every service
// reaches running, the web interface answers, it survives a restart, and it
// is removed cleanly.
//
// Run against a disposable panel (see tests/apps/README.md):
//
//	RIVET_APPS_PANEL=http://127.0.0.1:8080 RIVET_APPS_EMAIL=admin@example.com \
//	RIVET_APPS_PASSWORD=... RIVET_APPS=all|miniflux,ntfy \
//	go test -tags apps -count=1 -timeout 120m -v ./tests/apps
//
// RIVET_APPS_ADDRESS=site installs with a web address instead of a port and
// checks the app through the sites listener; RIVET_APPS_KEEP=1 leaves the
// apps installed for a look in the browser.
package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

type panel struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
}

func login(t *testing.T) *panel {
	base := os.Getenv("RIVET_APPS_PANEL")
	if base == "" {
		t.Skip("RIVET_APPS_PANEL not set")
	}
	jar, _ := cookiejar.New(nil)
	p := &panel{t: t, base: strings.TrimRight(base, "/"), hc: &http.Client{Jar: jar, Timeout: 120 * time.Second}}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	p.json("POST", "/api/v1/auth/login", map[string]string{"email": os.Getenv("RIVET_APPS_EMAIL"), "password": os.Getenv("RIVET_APPS_PASSWORD")}, 200, &out)
	p.csrf = out.CSRF
	return p
}

func (p *panel) do(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, p.base+path, rd)
	req.Header.Set("Origin", p.base)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.csrf != "" {
		req.Header.Set("X-CSRF-Token", p.csrf)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func (p *panel) json(method, path string, body any, want int, out any) {
	p.t.Helper()
	code, b, err := p.do(method, path, body)
	if err != nil || code != want {
		p.t.Fatalf("%s %s: %d %v %s", method, path, code, err, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			p.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

type service struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ObservedState string  `json:"observed_state"`
	LastError     *string `json:"last_error"`
	Container     struct {
		Service string `json:"service"`
	} `json:"container"`
}

type stack struct {
	ID       string    `json:"id"`
	URL      string    `json:"url"`
	Services []service `json:"services"`
	Template *struct {
		Web *struct {
			Health string `json:"health"`
		} `json:"web"`
		ConsoleBotID string `json:"console_bot_id"`
		Console      *struct {
			Command []string `json:"command"`
		} `json:"console"`
	} `json:"template"`
}

type tmpl struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Web       *struct {
		Port int `json:"port"`
	} `json:"web"`
}

var (
	resultsMu sync.Mutex
	results   = map[string]string{}
)

func record(id, outcome string) {
	resultsMu.Lock()
	results[id] = outcome
	resultsMu.Unlock()
}

func TestApps(t *testing.T) {
	p := login(t)
	var list struct {
		Templates []tmpl `json:"templates"`
	}
	p.json("GET", "/api/v1/app-templates", nil, 200, &list)
	want := os.Getenv("RIVET_APPS")
	if want == "" {
		want = "all"
	}
	pick := map[string]bool{}
	for _, id := range strings.Split(want, ",") {
		pick[strings.TrimSpace(id)] = true
	}
	t.Cleanup(func() {
		ids := make([]string, 0, len(results))
		for id := range results {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var b strings.Builder
		b.WriteString("\n| App | Result |\n| --- | --- |\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "| %s | %s |\n", id, results[id])
		}
		t.Log(b.String())
	})
	for _, tp := range list.Templates {
		if !pick["all"] && !pick[tp.ID] {
			continue
		}
		tp := tp
		t.Run(tp.ID, func(t *testing.T) {
			t.Parallel()
			if !tp.Available {
				record(tp.ID, "unavailable: "+tp.Reason)
				t.Fatalf("unavailable: %s", tp.Reason)
			}
			res := checkApp(t, p, tp.ID, tp.Web != nil)
			record(tp.ID, res)
		})
	}
}

// checkApp installs, checks, restarts, checks and removes one app; it
// returns a one-line outcome.
func checkApp(t *testing.T, root *panel, id string, hasWeb bool) string {
	p := *root
	p.t = t
	address := os.Getenv("RIVET_APPS_ADDRESS")
	if address == "" || !hasWeb {
		address = "port" // terminal-only apps have nothing to put at a web address
	}
	name := "t-" + id
	body := map[string]any{"name": name, "address": address}
	if address == "site" {
		body["slug"] = "t-" + id
	}
	var out struct {
		Stack stack  `json:"stack"`
		URL   string `json:"url"`
	}
	start := time.Now()
	code, b, err := p.do("POST", "/api/v1/app-templates/"+id+"/install", body)
	if err != nil || code != 201 {
		record(id, fmt.Sprintf("FAIL install: %d %s", code, b))
		t.Fatalf("install: %d %v %s", code, err, b)
	}
	json.Unmarshal(b, &out)
	st := out.Stack
	if os.Getenv("RIVET_APPS_KEEP") != "1" {
		t.Cleanup(func() {
			if code, b, err := p.do("DELETE", "/api/v1/stacks/"+st.ID, nil); err != nil || code != 204 {
				t.Errorf("delete: %d %v %s", code, err, b)
			}
		})
	}
	fail := func(stage string, f string, a ...any) string {
		msg := fmt.Sprintf(f, a...)
		t.Errorf("%s: %s", stage, msg)
		return "FAIL " + stage + ": " + msg
	}
	if err := waitRunning(&p, st.ID, 20*time.Minute); err != nil {
		return fail("start", "%v", err)
	}
	ready := time.Since(start).Round(time.Second)
	var cur stack
	p.json("GET", "/api/v1/stacks/"+st.ID, nil, 200, &cur)
	check := func() error {
		if cur.Template != nil && cur.Template.Web != nil {
			return waitHTTP(cur.URL+cur.Template.Web.Health, 8*time.Minute)
		}
		if cur.Template != nil && cur.Template.ConsoleBotID != "" {
			return terminalEcho(&p, cur.Template.ConsoleBotID)
		}
		return nil
	}
	if err := check(); err != nil {
		return fail("check", "%v", err)
	}
	p.json("POST", "/api/v1/stacks/"+st.ID+"/restart", nil, 200, nil)
	time.Sleep(3 * time.Second)
	if err := waitRunning(&p, st.ID, 10*time.Minute); err != nil {
		return fail("restart", "%v", err)
	}
	if err := check(); err != nil {
		return fail("after restart", "%v", err)
	}
	return fmt.Sprintf("ok (%s, running in %s)", cur.URL, ready)
}

func waitRunning(p *panel, id string, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last string
	var failedSince time.Time
	for time.Now().Before(deadline) {
		var st stack
		code, b, err := p.do("GET", "/api/v1/stacks/"+id, nil)
		if err == nil && code == 200 && json.Unmarshal(b, &st) == nil {
			all := len(st.Services) > 0
			var states []string
			for _, s := range st.Services {
				e := ""
				if s.LastError != nil {
					e = " (" + *s.LastError + ")"
				}
				states = append(states, s.Container.Service+"="+s.ObservedState+e)
				all = all && s.ObservedState == "running"
			}
			last = strings.Join(states, ", ")
			failed := false
			for _, s := range st.Services {
				failed = failed || s.ObservedState == "failed" || s.ObservedState == "crashed"
			}
			switch {
			case !failed:
				failedSince = time.Time{}
			case failedSince.IsZero():
				failedSince = time.Now()
			case time.Since(failedSince) > 4*time.Minute:
				return fmt.Errorf("a service keeps failing: %s", last)
			}
			if all {
				// Still running a few seconds later: not a crash loop.
				time.Sleep(8 * time.Second)
				p.do("GET", "/api/v1/stacks/"+id, nil)
				code, b, _ = p.do("GET", "/api/v1/stacks/"+id, nil)
				var again stack
				if json.Unmarshal(b, &again) == nil {
					ok := true
					for _, s := range again.Services {
						ok = ok && s.ObservedState == "running"
					}
					if ok {
						return nil
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("not running after %s: %s", d, last)
}

func waitHTTP(u string, d time.Duration) error {
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		resp, err := hc.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("GET %s: %s", u, last)
}

// terminalEcho opens a terminal in a console app and checks a command runs.
func terminalEcho(p *panel, botID string) error {
	u, _ := url.Parse(p.base)
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	cmd, _ := json.Marshal([]string{"sh"})
	wsURL := fmt.Sprintf("%s://%s/api/v1/bots/%s/terminal?cols=80&rows=24&cmd=%s", scheme, u.Host, botID, url.QueryEscape(string(cmd)))
	h := http.Header{"Origin": {p.base}}
	var cookies []string
	for _, c := range p.hc.Jar.Cookies(u) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	h.Set("Cookie", strings.Join(cookies, "; "))
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err != nil {
		if resp != nil {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("terminal: %v %s", err, b)
		}
		return fmt.Errorf("terminal: %v", err)
	}
	defer conn.Close()
	marker := fmt.Sprintf("ok-%d", time.Now().UnixNano()%100000)
	conn.WriteMessage(websocket.BinaryMessage, []byte("echo "+marker+"\n"))
	deadline := time.Now().Add(20 * time.Second)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		seen.Write(msg)
		if strings.Count(seen.String(), marker) >= 2 { // the echo of the typed line, then the output
			return nil
		}
	}
	return fmt.Errorf("terminal did not run the command; saw %q", seen.String())
}
