package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

type fakeStats struct{ got chan string }

func (f fakeStats) StreamStats(ctx context.Context, id string, fn func(domain.ResourceSample) bool) error {
	select {
	case f.got <- id:
	default:
	}
	// Like Docker, keep emitting frames until the consumer stops accepting them.
	for ctx.Err() == nil {
		if !fn(domain.ResourceSample{CPUCores: 0.25, MemUsedBytes: 100 << 20, MemLimitBytes: 256 << 20, PIDs: 7, NetRxBytes: 5, NetTxBytes: 9}) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ctx.Err()
}

func readEvent(t *testing.T, r *bufio.Reader) gaugeEvent {
	t.Helper()
	deadline := time.AfterFunc(5*time.Second, func() { t.Error("timed out waiting for an event") })
	defer deadline.Stop()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if rest, ok := strings.CutPrefix(line, "data: "); ok {
			var ev gaugeEvent
			if err := json.Unmarshal([]byte(rest), &ev); err != nil {
				t.Fatal(err)
			}
			return ev
		}
	}
}

func TestStatsStream(t *testing.T) {
	e := newEnv(t)
	fs := fakeStats{got: make(chan string, 4)}
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Files: fm, Stats: fs, SecureCookies: true})
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	os.WriteFile(filepath.Join(e.dataDir, id, "data.bin"), make([]byte, 4096), 0o644)
	addr := serve(t, e)

	open := func(c *client, bot string) (*http.Response, *bufio.Reader) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/bots/"+bot+"/stats/stream", nil)
		req.Header.Set("Cookie", sessionCookie+"="+c.cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp, bufio.NewReader(resp.Body)
	}

	// Stopped bot: idle events with limits and disk usage, no container stream.
	resp, r := open(owner, id)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	ev := readEvent(t, r)
	if ev.Running || ev.MemLimitBytes != 256<<20 || ev.CPULimitCores != 0.5 || ev.DiskUsedBytes != 4096 || ev.DiskTotal == 0 {
		t.Fatalf("idle event: %+v", ev)
	}
	resp.Body.Close()

	// Running bot: relays the Docker stream; CPU is reported against the bot's limit.
	e.db.Exec(`UPDATE bots SET container_id = 'cid1', desired_state = 'running', observed_state = 'running',
		generation = 1, observed_generation = 1 WHERE id = ?`, id)
	resp, r = open(owner, id)
	ev = readEvent(t, r)
	if !ev.Running || ev.CPUCores != 0.25 || ev.CPUPercent != 50 || ev.MemUsedBytes != 100<<20 || ev.PIDs != 7 || ev.NetTxBytes != 9 {
		t.Fatalf("running event: %+v", ev)
	}
	if got := <-fs.got; got != "cid1" {
		t.Fatalf("streamed container %q", got)
	}
	resp.Body.Close()

	// Authorization: strangers get 404, sub-users need the console permission.
	stranger := e.user("st@x.io", domain.RoleUser)
	if resp, _ := open(stranger, id); resp.StatusCode != 404 {
		t.Fatalf("stranger: %d", resp.StatusCode)
	}
	sub := e.user("sub@x.io", domain.RoleUser)
	owner.mustStatus(200, "PUT", "/api/v1/bots/"+id+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermEditFiles})
	if resp, _ := open(sub, id); resp.StatusCode != 403 {
		t.Fatalf("sub-user without console permission: %d", resp.StatusCode)
	}
}
