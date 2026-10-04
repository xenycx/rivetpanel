package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// usageStatsFake answers one frame per call with the values set for a container.
type usageStatsFake struct {
	mu sync.Mutex
	m  map[string]domain.ResourceSample
}

func (f *usageStatsFake) set(id string, s domain.ResourceSample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]domain.ResourceSample{}
	}
	f.m[id] = s
}

func (f *usageStatsFake) StreamStats(_ context.Context, id string, fn func(domain.ResourceSample) bool) error {
	f.mu.Lock()
	s, ok := f.m[id]
	f.mu.Unlock()
	if ok {
		fn(s)
	}
	return nil
}

type usageClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *usageClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *usageClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func usageEnv(t *testing.T, start time.Time) (*env, *service.UsageService, *usageStatsFake, *usageClock) {
	e := newEnv(t)
	clk := &usageClock{t: start}
	st := &usageStatsFake{}
	u := &service.UsageService{Store: e.db, Bots: e.bots, LocalNode: domain.LocalNodeID, Now: clk.now,
		StatsFor: func(string) service.UsageStats { return st }}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true,
		Clients: &service.APIClientService{Store: e.db, Bots: e.bots}, Usage: u})
	return e, u, st, clk
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.ExecContext(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func usageRows(t *testing.T, e *env, bot string, res int64) []domain.UsageBucket {
	t.Helper()
	rows, err := e.db.ListBotUsage(context.Background(), bot, res, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

type usageReport struct {
	Res    int64 `json:"resolution_s"`
	Points []struct {
		T       int64
		CPU     *float64
		Mem     *int64
		NetRx   *int64 `json:"net_rx"`
		Uptime  *float64
		Crashes int64
		Starts  int64
	}
	Events []struct {
		T           int64
		DeploysOK   int64  `json:"deploys_ok"`
		DeployAvgMS *int64 `json:"deploy_avg_ms"`
	}
	Totals struct {
		CPUAvg        *float64 `json:"cpu_avg"`
		MemMax        *int64   `json:"mem_max"`
		NetRx         int64    `json:"net_rx"`
		Uptime        *float64 `json:"uptime"`
		Crashes       int64
		Starts        int64
		DeploysOK     int64  `json:"deploys_ok"`
		DeploysFailed int64  `json:"deploys_failed"`
		DeployAvgMS   *int64 `json:"deploy_avg_ms"`
		BackupsOK     int64  `json:"backups_ok"`
		BackupsFailed int64  `json:"backups_failed"`
		BackupBytes   int64  `json:"backup_bytes"`
	}
}

// The collector turns one-minute passes into exact 5-minute rows, and the
// rollups into hourly and daily rows that equal the sum of their parts,
// however often they run.
func TestUsageCollectorAndRollups(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 10, 0, 10, 0, time.UTC)
	e, u, st, clk := usageEnv(t, t0)
	alice := e.user("alice@x.io", domain.RoleUser)
	bot := alice.createBot("worker")
	e.exec(`UPDATE bots SET desired_state = 'running', observed_state = 'running', container_id = 'c1', last_started_at_ms = 1 WHERE id = ?`, bot)

	// Five passes in the 10:00 bucket; network counters grow by 1000 a pass.
	for i := range 5 {
		st.set("c1", domain.ResourceSample{CPUCores: 0.5 + float64(i)*0.1, MemUsedBytes: 100 << 20, MemLimitBytes: 256 << 20,
			NetRxBytes: int64(1000 * (i + 1)), NetTxBytes: int64(10 * (i + 1))})
		if i == 2 {
			// Two crashes and a restart between passes 2 and 3.
			e.exec(`UPDATE bots SET restart_count = 2, last_started_at_ms = 2 WHERE id = ?`, bot)
		}
		if i == 4 {
			e.exec(`UPDATE bots SET observed_state = 'failed' WHERE id = ?`, bot)
		}
		if _, err := u.Collect(ctx); err != nil {
			t.Fatal(err)
		}
		clk.add(time.Minute)
	}
	if rows := usageRows(t, e, bot, 300); len(rows) != 0 {
		t.Fatalf("bucket written before it closed: %+v", rows)
	}
	// The open bucket is already visible in the 1h range.
	var live usageReport
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/usage?range=1h", nil), &live)
	if live.Totals.CPUAvg == nil || live.Totals.Crashes != 2 {
		t.Fatalf("live bucket missing: %+v", live.Totals)
	}

	// The first pass of 10:05 writes the 10:00 bucket.
	e.exec(`UPDATE bots SET observed_state = 'running' WHERE id = ?`, bot)
	flushed, err := u.Collect(ctx)
	if err != nil || !flushed {
		t.Fatal(flushed, err)
	}
	rows := usageRows(t, e, bot, 300)
	if len(rows) != 1 {
		t.Fatalf("5m rows: %+v", rows)
	}
	r := rows[0]
	if r.BucketMS != t0.Truncate(5*time.Minute).UnixMilli() || r.Samples != 5 || r.Wanted != 5 || r.Up != 4 || r.Measured != 4 ||
		r.NetRx != 3000 || r.NetTx != 30 || r.Crashes != 2 || r.Starts != 1 || r.MemMax != 100<<20 || r.MemLimit != 256<<20 {
		t.Fatalf("5m row: %+v", r)
	}
	if got := r.CPUSum / float64(r.Measured); got < 0.649 || got > 0.651 {
		t.Fatalf("cpu avg %v", got)
	}

	// Deployments and backups come from the operations table.
	base := t0.UnixMilli()
	e.exec(`INSERT INTO bot_backups (id, bot_id, kind, status, file_name, size_bytes, created_at_ms) VALUES ('bk1', ?, 'manual', 'ready', 'bk1.tar.gz', 1234, ?)`, bot, base)
	for _, op := range []struct {
		id, kind, status, ref string
		start, end            int64
	}{
		{"op1", "deploy", "succeeded", "", base, base + 30_000},
		{"op2", "deploy", "failed", "", base + 60_000, base + 70_000},
		{"op3", "backup", "succeeded", "bk1", base, base + 5_000},
		{"op4", "backup", "failed", "", base, base + 1_000},
		{"op5", "build", "succeeded", "", base, base + 1_000},
	} {
		var ref any
		if op.ref != "" {
			ref = op.ref
		}
		e.exec(`INSERT INTO operations (id, bot_id, kind, trigger, status, source_ref, created_at_ms, started_at_ms, finished_at_ms)
			VALUES (?, ?, ?, 'manual', ?, ?, ?, ?, ?)`, op.id, bot, op.kind, op.status, ref, op.start, op.start, op.end)
	}
	for range 3 { // idempotent
		if err := u.Rollup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, res := range []int64{3600, 86400} {
		rows := usageRows(t, e, bot, res)
		if len(rows) != 1 {
			t.Fatalf("res %d rows: %+v", res, rows)
		}
		h := rows[0]
		if h.Samples != 5 || h.Up != 4 || h.NetRx != 3000 || h.Crashes != 2 || h.CPUSum != r.CPUSum || h.CPUMax != r.CPUMax ||
			h.DeploysOK != 1 || h.DeploysFailed != 1 || h.DeployMS != 40_000 || h.BackupsOK != 1 || h.BackupsFailed != 1 || h.BackupBytes != 1234 {
			t.Fatalf("res %d row: %+v", res, h)
		}
	}

	// More data in the same hour is recomputed, not added twice.
	for range 5 {
		clk.add(time.Minute)
		if _, err := u.Collect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	if h := usageRows(t, e, bot, 3600)[0]; h.Samples != 10 || h.DeploysOK != 1 {
		t.Fatalf("hour after second bucket: %+v", h)
	}

	// Range queries.
	var rep usageReport
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/usage?range=24h", nil), &rep)
	if rep.Res != 300 || len(rep.Points) != 289 && len(rep.Points) != 288 {
		t.Fatalf("24h points: res %d n %d", rep.Res, len(rep.Points))
	}
	if rep.Totals.DeploysOK != 1 || rep.Totals.DeploysFailed != 1 || *rep.Totals.DeployAvgMS != 20_000 || rep.Totals.BackupBytes != 1234 ||
		rep.Totals.Crashes != 2 || rep.Totals.Starts != 1 || rep.Totals.NetRx < 3000 || rep.Totals.Uptime == nil {
		t.Fatalf("24h totals: %+v", rep.Totals)
	}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/usage?range=7d", nil), &rep)
	if rep.Res != 3600 || len(rep.Points) < 168 || len(rep.Points) > 169 {
		t.Fatalf("7d points: res %d n %d", rep.Res, len(rep.Points))
	}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/bots/"+bot+"/usage?range=90d", nil), &rep)
	if rep.Res != 86400 || rep.Totals.DeploysOK != 1 {
		t.Fatalf("90d: res %d %+v", rep.Res, rep.Totals)
	}
	alice.mustStatus(400, "GET", "/api/v1/bots/"+bot+"/usage?range=2y", nil)

	resp, body := alice.do("GET", "/api/v1/bots/"+bot+"/usage?range=24h&format=csv", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(string(body), "time,cpu_cores_avg,") || !strings.Contains(string(body), "deploys_succeeded") {
		t.Fatalf("csv: %d %s %.200s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

// Rows older than their tier's retention are removed; newer ones stay.
func TestUsageRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	e, u, _, _ := usageEnv(t, now)
	alice := e.user("alice@x.io", domain.RoleUser)
	bot := alice.createBot("b")
	day := 24 * time.Hour
	add := func(res int64, age time.Duration) {
		at := now.Add(-age).UnixMilli()
		at -= at % (res * 1000)
		if err := e.db.AddUsage(ctx, []domain.UsageBucket{{BotID: bot, Res: res, BucketMS: at, Samples: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	add(300, 4*day)
	add(300, 2*day)
	add(3600, 36*day)
	add(3600, 34*day)
	add(86400, 401*day)
	add(86400, 399*day)
	nodes, _ := e.db.ListNodes(ctx)
	for _, n := range []struct {
		res int64
		age time.Duration
	}{{3600, 36 * day}, {3600, day}, {86400, 401 * day}, {86400, 10 * day}} {
		e.exec(`INSERT INTO node_usage (node_id, res, bucket_ms, samples) VALUES (?, ?, ?, 1)`, nodes[0].ID, n.res, now.Add(-n.age).UnixMilli())
	}
	if err := u.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	for _, res := range []int64{300, 3600, 86400} {
		if n := len(usageRows(t, e, bot, res)); n != 1 {
			t.Fatalf("res %d kept %d rows", res, n)
		}
	}
	var n int
	e.db.QueryRowContext(ctx, `SELECT count(*) FROM node_usage`).Scan(&n)
	if n != 2 {
		t.Fatalf("node rows kept: %d", n)
	}
	// Deleting a bot removes its rows.
	e.exec(`DELETE FROM bots WHERE id = ?`, bot)
	e.db.QueryRowContext(ctx, `SELECT count(*) FROM bot_usage`).Scan(&n)
	if n != 0 {
		t.Fatalf("rows of a deleted bot: %d", n)
	}
}

type overviewOut struct {
	Scope      string
	UsersTotal int64 `json:"users_total"`
	Bots       map[string]struct{ Total, Running int64 }
	TopCPU     []struct {
		BotID string `json:"bot_id"`
		Name  string
		Owner string
	} `json:"top_cpu"`
	Nodes   []struct{ ID string }
	Tickets *struct{ Opened int64 }
}

// Bot analytics follow bot access; the overview needs analytics.view, and a
// delegated viewer or an API client never sees what it could not reach.
func TestUsageAuthorization(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	e, u, _, _ := usageEnv(t, now)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	analyst := e.user("analyst@x.io", domain.RoleUser)
	adminBot := admin.createBot("admin-secret-bot")
	aliceBot := alice.createBot("alice-bot")
	aliceOther := alice.createBot("alice-other")
	at := now.Add(-2 * time.Hour).UnixMilli()
	at -= at % 300_000
	for _, b := range []string{adminBot, aliceBot, aliceOther} {
		if err := e.db.AddUsage(ctx, []domain.UsageBucket{{BotID: b, Res: 300, BucketMS: at, Samples: 5, Wanted: 5, Up: 5, Measured: 5,
			CPUSum: 2.5, CPUMax: 0.6, MemSum: 5 << 20, MemMax: 1 << 20, NetRx: 100}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Rollup(ctx); err != nil {
		t.Fatal(err)
	}

	// Per bot: the owner yes, another account sees nothing (404), the
	// overview is refused without analytics.view.
	alice.mustStatus(200, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=24h", nil)
	bob.mustStatus(404, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=24h", nil)
	alice.mustStatus(404, "GET", "/api/v1/bots/"+adminBot+"/usage?range=24h", nil)
	alice.mustStatus(403, "GET", "/api/v1/admin/analytics", nil)
	// A share without console access is refused; with it, allowed.
	alice.mustStatus(200, "PUT", "/api/v1/bots/"+aliceBot+"/users", map[string]any{"email": "bob@x.io", "permissions": domain.PermPower})
	bob.mustStatus(403, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=24h", nil)
	alice.mustStatus(200, "PUT", "/api/v1/bots/"+aliceBot+"/users", map[string]any{"email": "bob@x.io", "permissions": domain.PermViewConsole})
	bob.mustStatus(200, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=24h", nil)
	bob.mustStatus(404, "GET", "/api/v1/bots/"+aliceOther+"/usage?range=24h", nil)
	(&client{e: e}).mustStatus(401, "GET", "/api/v1/admin/analytics", nil)

	// The administrator sees everything, with owners.
	var ov overviewOut
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/analytics?range=7d", nil), &ov)
	if ov.Scope != "all" || ov.UsersTotal != 4 || ov.Bots["bot"].Total != 3 || len(ov.TopCPU) != 3 || len(ov.Nodes) == 0 || ov.Tickets == nil {
		t.Fatalf("admin overview: %+v", ov)
	}
	if ov.TopCPU[0].Owner == "" {
		t.Fatalf("admin sees no owners: %+v", ov.TopCPU)
	}
	admin.mustStatus(400, "GET", "/api/v1/admin/analytics?range=1y", nil)

	// A delegated analyst: no administrator data, no nodes, tickets or owners
	// without those permissions.
	rid := admin.createRole("Analysts", domain.PermAnalyticsView)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("analyst@x.io"), map[string]any{"role": rid})
	raw := analyst.mustStatus(200, "GET", "/api/v1/admin/analytics?range=7d", nil)
	ov = overviewOut{}
	json.Unmarshal(raw, &ov)
	if ov.Scope != "delegated" || ov.UsersTotal != 3 || ov.Bots["bot"].Total != 2 || len(ov.TopCPU) != 2 || ov.Nodes != nil || ov.Tickets != nil {
		t.Fatalf("delegated overview: %s", raw)
	}
	if strings.Contains(string(raw), "admin-secret-bot") || strings.Contains(string(raw), adminBot) || strings.Contains(string(raw), "@x.io") {
		t.Fatalf("delegated overview leaks: %s", raw)
	}
	resp, body := analyst.do("GET", "/api/v1/admin/analytics?range=24h&format=csv", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "time,cpu_cores,") || strings.Contains(string(body), "tickets_opened") {
		t.Fatalf("csv: %d %.200s", resp.StatusCode, body)
	}
	// Without bot access the analyst still cannot open a bot's own analytics.
	analyst.mustStatus(404, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=24h", nil)

	// With node and ticket access, those sections appear.
	rid2 := admin.createRole("Ops analysts", domain.PermAnalyticsView, domain.PermNodesManage, domain.PermTicketsViewAll)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("analyst@x.io"), map[string]any{"role": rid2})
	ov = overviewOut{}
	json.Unmarshal(analyst.mustStatus(200, "GET", "/api/v1/admin/analytics?range=7d", nil), &ov)
	if len(ov.Nodes) == 0 || ov.Tickets == nil || len(ov.TopCPU) != 2 {
		t.Fatalf("ops analyst overview: %+v", ov)
	}

	// API clients: an administrator's client is never an administrator.
	tok, _ := mkClient(t, admin, map[string]any{"name": "dash", "permissions": []string{domain.PermAnalyticsView}})
	ov = overviewOut{}
	raw = wantStatus(t, e, tok, 200, "GET", "/api/v1/admin/analytics?range=7d", nil)
	json.Unmarshal(raw, &ov)
	if ov.Scope != "delegated" || strings.Contains(string(raw), "admin-secret-bot") || len(ov.TopCPU) != 2 {
		t.Fatalf("admin client overview: %s", raw)
	}
	noPerm, _ := mkClient(t, admin, map[string]any{"name": "other", "permissions": []string{domain.PermBotsPower}})
	wantStatus(t, e, noPerm, 403, "GET", "/api/v1/admin/analytics", nil)
	// A client scoped to one bot reaches that bot's analytics only, and no
	// panel-wide data.
	scoped, _ := mkClient(t, alice, map[string]any{"name": "one", "permissions": []string{domain.PermBotsConsole}, "bot_ids": []string{aliceBot}})
	wantStatus(t, e, scoped, 200, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=1h", nil)
	wantStatus(t, e, scoped, 403, "GET", "/api/v1/bots/"+aliceOther+"/usage?range=1h", nil)
	scopedAdmin, _ := mkClient(t, admin, map[string]any{"name": "s", "permissions": []string{domain.PermAnalyticsView, domain.PermBotsConsole}, "bot_ids": []string{adminBot}})
	wantStatus(t, e, scopedAdmin, 403, "GET", "/api/v1/admin/analytics?range=7d", nil)
	wantStatus(t, e, scopedAdmin, 200, "GET", "/api/v1/bots/"+adminBot+"/usage?range=1h", nil)
	// A client without console access cannot read resource use.
	power, _ := mkClient(t, alice, map[string]any{"name": "power", "permissions": []string{domain.PermBotsPower}})
	wantStatus(t, e, power, 403, "GET", "/api/v1/bots/"+aliceBot+"/usage?range=1h", nil)
}
