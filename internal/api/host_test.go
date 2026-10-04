package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/hostmon"
	"github.com/xenycx/rivetpanel/internal/logbuf"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

func hostEnv(t *testing.T) (*env, *logbuf.Buffer) {
	t.Helper()
	e := newEnv(t)
	logs := logbuf.New(64)
	dir := t.TempDir()
	mon := &hostmon.Monitor{Proc: telemetry.ProcReader{}, Store: e.db, Users: e.db, Logs: logs, Version: "test", Started: time.Now(),
		DBPath: filepath.Join(dir, "p.db"), DataRoot: dir, BackupDir: dir}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, SecureCookies: true,
		Nodes: e.db, Host: mon, Logs: logs})
	return e, logs
}

func TestHostRoutesAreAdminOnly(t *testing.T) {
	e, _ := hostEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	for _, p := range []string{"/api/v1/admin/host", "/api/v1/admin/host/bots", "/api/v1/admin/logs", "/api/v1/nodes/" + domain.LocalNodeID + "/history?range=1h"} {
		user.mustStatus(403, "GET", p, nil)
		admin.mustStatus(200, "GET", p, nil)
	}
	var snap struct {
		Panel struct {
			Version string `json:"version"`
			PID     int    `json:"pid"`
		} `json:"panel"`
		Host    struct{ Cores int } `json:"host"`
		Storage []struct{ Key string }
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/host", nil), &snap)
	if snap.Panel.Version != "test" || snap.Panel.PID != os.Getpid() || len(snap.Storage) < 3 {
		t.Fatalf("%+v", snap)
	}
}

func TestNodeHistoryRangesAreBoundedAndValidated(t *testing.T) {
	e, _ := hostEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	now := time.Now().UnixMilli()
	for i := int64(0); i < 100; i++ {
		e.db.InsertTelemetry(t.Context(), domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: now - i*30_000, CPUPercent: float64(i % 100), LogicalCPUs: 2,
			MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2, Load1: 0.5, NetRxBps: 10})
	}
	admin.mustStatus(400, "GET", "/api/v1/nodes/"+domain.LocalNodeID+"/history?range=1y", nil)
	admin.mustStatus(404, "GET", "/api/v1/nodes/nope/history?range=1h", nil)
	var out struct {
		Range    string `json:"range"`
		BucketMS int64  `json:"bucket_ms"`
		Points   []struct {
			CPUPercent float64 `json:"cpu_percent"`
			CPUMax     float64 `json:"cpu_max"`
			Load1      float64 `json:"load1"`
			NetRxBps   int64   `json:"net_rx_bps"`
		}
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/nodes/"+domain.LocalNodeID+"/history?range=24h", nil), &out)
	if out.Range != "24h" || out.BucketMS != 6*60_000 || len(out.Points) == 0 || len(out.Points) > 20 {
		t.Fatalf("a day of 30 second samples must collapse into 6 minute buckets: %+v", out)
	}
	for _, p := range out.Points {
		if p.CPUMax < p.CPUPercent || p.Load1 != 0.5 || p.NetRxBps != 10 {
			t.Fatalf("%+v", p)
		}
	}
}

func TestPanelLogsFilterFollowAndDownload(t *testing.T) {
	e, logs := hostEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	l := slog.New(logs.Handler(slog.NewJSONHandler(io.Discard, nil)))
	l.Info("listening", "addr", ":8080")
	l.Warn("disk low", "free_mb", 90)
	l.Error("runner stopped", "err", "docker unreachable", "api_key", "sk-secret")

	type res struct {
		Entries []logbuf.Entry `json:"entries"`
		Stats   logbuf.Stats   `json:"stats"`
	}
	get := func(q string) res {
		var r res
		json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/logs"+q, nil), &r)
		return r
	}
	if r := get(""); len(r.Entries) != 3 || r.Stats.Errors != 1 || r.Stats.Warnings != 1 {
		t.Fatalf("%+v", r)
	}
	if r := get("?level=warn"); len(r.Entries) != 2 {
		t.Fatalf("level filter: %+v", r.Entries)
	}
	if r := get("?q=UNREACHABLE"); len(r.Entries) != 1 || r.Entries[0].Level != "error" {
		t.Fatalf("search: %+v", r.Entries)
	}
	first := get("").Entries[0].Seq
	if r := get("?after=" + strconv.FormatInt(first, 10)); len(r.Entries) != 2 {
		t.Fatalf("follow: %+v", r.Entries)
	}
	admin.mustStatus(400, "GET", "/api/v1/admin/logs?level=loud", nil)
	admin.mustStatus(400, "GET", "/api/v1/admin/logs?after=x", nil)
	admin.mustStatus(400, "GET", "/api/v1/admin/logs?limit=5000", nil)

	raw := string(admin.mustStatus(200, "GET", "/api/v1/admin/logs?download=1", nil))
	if !strings.Contains(raw, "ERROR runner stopped") || !strings.Contains(raw, `err="docker unreachable"`) || strings.Contains(raw, "sk-secret") {
		t.Fatalf("download:\n%s", raw)
	}
}
