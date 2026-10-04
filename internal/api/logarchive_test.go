package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/logarchive"
	"github.com/xenycx/rivetpanel/internal/service"
)

func logArchiveEnv(t *testing.T) (*env, *service.LogArchiveService) {
	t.Helper()
	e := newEnv(t)
	files, err := logarchive.New(filepath.Join(t.TempDir(), "logs-root"))
	if err != nil {
		t.Fatal(err)
	}
	la := &service.LogArchiveService{Store: e.db, Files: files, Bots: e.bots, Defaults: service.DefaultLogSettings(7 * 24 * time.Hour)}
	if err := la.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog, SecureCookies: true,
		Nodes: e.db, Files: e.bots.Workspaces.(*filesystem.Manager), MaxUpload: 2 << 20, LogArchive: la, Audit: &service.Audit{Store: e.db, Bots: e.bots}})
	return e, la
}

func TestLogArchiveSettingsAPI(t *testing.T) {
	e, la := logArchiveEnv(t)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)

	user.mustStatus(403, "GET", "/api/v1/admin/log-archive", nil)
	user.mustStatus(403, "PUT", "/api/v1/admin/log-archive", map[string]any{"retention_days": 3})
	user.mustStatus(403, "POST", "/api/v1/admin/log-archive/run", nil)

	var v service.LogArchiveView
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/log-archive", nil), &v)
	if v.Settings.ArchiveTime != "00:00" || v.Settings.RetentionDays != 30 || v.Settings.Metrics.Usage1dDays != 400 || v.Bounds["retention_days"].Max != 3650 || v.CurrentDay == "" ||
		v.Settings.MaxDayMB != 256 || la.Files.Limits().DayMaxBytes != 256<<20 {
		t.Fatalf("defaults = %+v", v)
	}
	for _, bad := range []map[string]any{
		{"archive_time": "24:00"}, {"archive_time": "7:5"}, {"timezone": "Mars/Olympus"},
		{"retention_days": 0}, {"retention_days": 5000}, {"max_archive_mb": -1}, {"max_day_mb": 0}, {"capture_minutes": 0},
		{"metrics": map[string]any{"telemetry_hours": 0}}, {"metrics": map[string]any{"status_days": 30}},
		{"metrics": map[string]any{"usage_5m_days": 10, "usage_1h_days": 5}},
	} {
		admin.mustStatus(400, "PUT", "/api/v1/admin/log-archive", bad)
	}
	json.Unmarshal(admin.mustStatus(200, "PUT", "/api/v1/admin/log-archive", map[string]any{
		"archive_time": "03:30", "timezone": "Europe/Berlin", "retention_days": 14, "max_archive_mb": 512, "max_day_mb": 64,
		"metrics": map[string]any{"telemetry_hours": 48, "usage_1d_days": 90},
	}), &v)
	if v.Settings.ArchiveTime != "03:30" || v.Settings.Timezone != "Europe/Berlin" || v.Settings.RetentionDays != 14 || v.Settings.Metrics.TelemetryHours != 48 || v.Settings.Metrics.Usage1dDays != 90 || v.Settings.Metrics.Usage5mDays != 3 {
		t.Fatalf("updated = %+v", v.Settings)
	}
	if m := la.Metrics(); m.TelemetryHours != 48 || m.Usage1dDays != 90 {
		t.Fatalf("metrics retention not applied live: %+v", m)
	}
	if v.Settings.MaxDayMB != 64 || la.Files.Limits().DayMaxBytes != 64<<20 {
		t.Fatalf("day cap not applied: %+v %+v", v.Settings, la.Files.Limits())
	}
	if c := la.Files.Clock(); c.At != 3*time.Hour+30*time.Minute || c.Loc.String() != "Europe/Berlin" {
		t.Fatalf("clock not applied: %+v", c)
	}
	// Saved settings survive a restart.
	la2 := &service.LogArchiveService{Store: e.db, Defaults: service.DefaultLogSettings(7 * 24 * time.Hour)}
	la2.Load(context.Background())
	if la2.Settings().RetentionDays != 14 || la2.Settings().MaxDayMB != 64 {
		t.Fatal("settings not persisted")
	}

	// system.view reads the settings (read-only) but cannot change them or run the job.
	monitor := e.user("monitor@example.com", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("monitor@example.com"), map[string]any{"role": admin.createRole("Monitor", domain.PermSystemView)})
	monitor.mustStatus(200, "GET", "/api/v1/admin/log-archive", nil)
	monitor.mustStatus(403, "PUT", "/api/v1/admin/log-archive", map[string]any{"retention_days": 3})
	monitor.mustStatus(403, "POST", "/api/v1/admin/log-archive/run", nil)

	var run service.LogArchiveRun
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/admin/log-archive/run", nil), &run)
	if !run.OK || run.Trigger != "manual" || run.FinishedAtMS == 0 {
		t.Fatalf("run = %+v", run)
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/log-archive", nil), &v)
	if v.LastRun == nil || v.LastRun.Trigger != "manual" {
		t.Fatalf("last run not reported: %+v", v.LastRun)
	}
}

func TestBotLogDaysNeedConsoleAccess(t *testing.T) {
	e, la := logArchiveEnv(t)
	owner := e.user("owner@example.com", domain.RoleUser)
	other := e.user("other@example.com", domain.RoleUser)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	bot := owner.createBot("logbot")
	la.Files.SetClock(logarchive.Clock{Loc: time.UTC})
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	la.Files.Append(logarchive.BotScope(bot), []logarchive.Line{{At: yesterday, Text: []byte("old line")}, {At: time.Now(), Text: []byte("today line")}})
	la.Files.Rotate()

	other.mustStatus(404, "GET", "/api/v1/bots/"+bot+"/logs/days", nil)
	day := yesterday.Format("2006-01-02")
	other.mustStatus(404, "GET", "/api/v1/bots/"+bot+"/logs/days/"+day, nil)
	other.mustStatus(403, "GET", "/api/v1/admin/logs/days", nil)

	var out struct {
		Days       []logarchive.Day `json:"days"`
		CurrentDay string           `json:"current_day"`
	}
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/logs/days", nil), &out)
	if len(out.Days) != 2 || !out.Days[1].Archived || out.Days[0].Archived || out.CurrentDay != out.Days[0].Date {
		t.Fatalf("days = %+v", out)
	}
	resp, body := owner.do("GET", "/api/v1/bots/"+bot+"/logs/days/"+day, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/gzip" || !strings.Contains(resp.Header.Get("Content-Disposition"), day+".log.gz") {
		t.Fatalf("archive download: %d %v", resp.StatusCode, resp.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "old line\n" {
		t.Fatalf("archive = %q", b)
	}
	resp, body = owner.do("GET", "/api/v1/bots/"+bot+"/logs/days/"+out.CurrentDay, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || string(body) != "today line\n" {
		t.Fatalf("live day: %d %q", resp.StatusCode, body)
	}
	owner.mustStatus(404, "GET", "/api/v1/bots/"+bot+"/logs/days/"+day+"?archived=0", nil)
	owner.mustStatus(400, "GET", "/api/v1/bots/"+bot+"/logs/days/not-a-date", nil)
	admin.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/logs/days", nil)
	admin.mustStatus(200, "GET", "/api/v1/admin/logs/days", nil)
}
