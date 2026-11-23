package api

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
	"github.com/xenycx/rivetpanel/internal/service"
)

func metricsEnv(t *testing.T) (*env, *metrics.Metrics) {
	t.Helper()
	e := newEnv(t)
	m := &metrics.Metrics{Store: e.db, LocalNode: domain.LocalNodeID, LogicalCPUs: 2, Proc: metrics.ProcSampler{Root: t.TempDir()},
		Fast: &metrics.FastSampler{}}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		SecureCookies: true, Nodes: e.db, Metrics: m, Clients: &service.APIClientService{Store: e.db, Bots: e.bots}})
	return e, m
}

func TestMetricsNodeRoutesNeedSystemView(t *testing.T) {
	e, _ := metricsEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	now := time.Now().UnixMilli()
	for i := int64(0); i < 20; i++ {
		e.db.InsertTelemetry(t.Context(), domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: now - i*30_000, CPUPercent: 40, LogicalCPUs: 2,
			MemoryUsedBytes: 100, MemoryTotalBytes: 200, DiskUsedBytes: 1, DiskTotalBytes: 2, Load1: 0.5})
	}
	base := "/api/v1/nodes/" + domain.LocalNodeID
	q := "?start=" + strconv.FormatInt(now-600_000, 10) + "&end=" + strconv.FormatInt(now, 10)
	for _, p := range []string{"/series" + q, "/series/bots" + q, "/events" + q, "/spikes" + q, "/at?t=" + strconv.FormatInt(now, 10)} {
		user.mustStatus(403, "GET", base+p, nil)
		admin.mustStatus(200, "GET", base+p, nil)
	}
	// system.view alone is enough, although the other /nodes routes need
	// nodes.manage.
	viewer := e.user("viewer@example.com", domain.RoleUser)
	rid := admin.createRole("Host viewers", domain.PermSystemView)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("viewer@example.com"), map[string]any{"role": rid})
	viewer.mustStatus(200, "GET", base+"/series"+q, nil)
	viewer.mustStatus(403, "GET", base+"/history?range=1h", nil)
	admin.mustStatus(404, "GET", "/api/v1/nodes/nope/series"+q, nil)
	admin.mustStatus(400, "GET", base+"/series?start=5&end=1", nil)
	admin.mustStatus(400, "GET", base+"/series/bots"+q+"&metric=disk", nil)
	admin.mustStatus(400, "GET", base+"/at", nil)
	admin.mustStatus(400, "GET", base+"/processes?limit=0", nil)

	var s struct {
		StepMS   int64      `json:"step_ms"`
		Times    []int64    `json:"times"`
		CPU      []*float64 `json:"cpu"`
		Mem      []*float64 `json:"mem"`
		MemTotal int64      `json:"mem_total"`
		CPUs     int        `json:"cpus"`
	}
	resp, body := admin.do("GET", base+"/series"+q, nil)
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %v", resp.Header)
	}
	json.Unmarshal(body, &s)
	if s.StepMS != 30_000 || len(s.Times) == 0 || len(s.CPU) != len(s.Times) || len(s.Mem) != len(s.Times) || s.MemTotal != 200 || s.CPUs != 2 {
		t.Fatalf("%s", body)
	}
	filled := 0
	for _, v := range s.CPU {
		if v != nil {
			filled++
			if *v != 40 {
				t.Fatalf("%s", body)
			}
		}
	}
	if filled < 15 {
		t.Fatalf("%d filled: %s", filled, body)
	}
	// Incremental: only buckets after since.
	last := s.Times[len(s.Times)-2]
	var inc struct{ Times []int64 }
	json.Unmarshal(admin.mustStatus(200, "GET", base+"/series"+q+"&since="+strconv.FormatInt(last, 10), nil), &inc)
	if len(inc.Times) != 1 || inc.Times[0] <= last {
		t.Fatalf("since: %+v", inc)
	}

	var bs struct {
		Times        []int64          `json:"times"`
		Bots         []map[string]any `json:"bots"`
		Other        []*float64       `json:"other"`
		Unattributed []*float64       `json:"unattributed"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", base+"/series/bots"+q+"&metric=mem&top=3", nil), &bs)
	if bs.Bots == nil || len(bs.Other) != len(bs.Times) || len(bs.Unattributed) != len(bs.Times) {
		t.Fatalf("%+v", bs)
	}

	var ev struct{ Events []any }
	if err := json.Unmarshal(admin.mustStatus(200, "GET", base+"/events"+q, nil), &ev); err != nil || ev.Events == nil {
		t.Fatalf("events must be an array: %v", err)
	}
	var sp struct {
		ThresholdPct float64 `json:"threshold_pct"`
		Spikes       []any   `json:"spikes"`
	}
	if json.Unmarshal(admin.mustStatus(200, "GET", base+"/spikes"+q, nil), &sp); sp.Spikes == nil || sp.ThresholdPct != 80 {
		t.Fatalf("spikes must be an array with the threshold: %+v", sp)
	}
	var at struct {
		TMS  int64           `json:"t_ms"`
		Host *map[string]any `json:"host"`
		Bots []any           `json:"bots"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", base+"/at?t="+strconv.FormatInt(now-60_000, 10), nil), &at)
	if at.Host == nil || at.Bots == nil || at.TMS != now-60_000 {
		t.Fatalf("%+v", at)
	}
}

func TestMetricsBotRoutesAuthorizePerBot(t *testing.T) {
	e, _ := metricsEnv(t)
	owner := e.user("own@example.com", domain.RoleUser)
	other := e.user("other@example.com", domain.RoleUser)
	id := owner.createBot("b")
	now := time.Now()
	e.db.InsertContainerSamples(t.Context(), []domain.ContainerSample{
		{BotID: id, NodeID: domain.LocalNodeID, SampledAtMS: now.Add(-time.Minute).UnixMilli(), CPUCores: 0.25, MemoryBytes: 1 << 20, PIDs: 4},
	})
	var out struct {
		Times    []int64    `json:"times"`
		CPU      []*float64 `json:"cpu"`
		PIDs     []*float64 `json:"pids"`
		BlkRead  []*float64 `json:"blk_read"`
		MemLimit int64      `json:"mem_limit"`
	}
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/bots/"+id+"/series", nil), &out)
	if len(out.Times) == 0 || len(out.CPU) != len(out.Times) || len(out.BlkRead) != len(out.Times) || out.MemLimit == 0 {
		t.Fatalf("%+v", out)
	}
	found := false
	for i, v := range out.CPU {
		if v != nil && *v == 0.25 && out.PIDs[i] != nil && *out.PIDs[i] == 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("sample missing: %+v", out)
	}
	if resp, _ := other.do("GET", "/api/v1/bots/"+id+"/series", nil); resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Fatalf("another account: %d", resp.StatusCode)
	}
	owner.mustStatus(409, "GET", "/api/v1/bots/"+id+"/processes", nil) // not running
	if resp, _ := other.do("GET", "/api/v1/bots/"+id+"/processes", nil); resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Fatalf("another account: %d", resp.StatusCode)
	}
}

func TestMetricsHostStream(t *testing.T) {
	e, m := metricsEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	addr := serve(t, e)
	open := func(c *client) *http.Response {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/nodes/"+domain.LocalNodeID+"/series/stream", nil)
		req.Header.Set("Cookie", sessionCookie+"="+c.cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	if resp := open(user); resp.StatusCode != 403 {
		t.Fatalf("user: %d", resp.StatusCode)
	}
	resp := open(admin)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	r := bufio.NewReader(resp.Body)
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, ":") {
		t.Fatalf("first line %q", line)
	}
	// Points added to the shared sampler reach every open stream.
	go func() {
		for i := 0; i < 50; i++ {
			m.Fast.Add(metrics.HostPoint{T: time.Now().UnixMilli(), CPU: 42, Mem: 7})
			time.Sleep(20 * time.Millisecond)
		}
	}()
	done := make(chan struct{})
	timer := time.AfterFunc(5*time.Second, func() { resp.Body.Close() })
	defer timer.Stop()
	go func() {
		defer close(done)
		var event string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Error(err)
				return
			}
			if rest, ok := strings.CutPrefix(line, "event: "); ok {
				event = strings.TrimSpace(rest)
			}
			if rest, ok := strings.CutPrefix(line, "data: "); ok {
				var p metrics.HostPoint
				if err := json.Unmarshal([]byte(rest), &p); err != nil || event != "sample" || p.CPU != 42 || p.Mem != 7 {
					t.Errorf("%q %q %v", event, rest, err)
				}
				return
			}
		}
	}()
	<-done
}
