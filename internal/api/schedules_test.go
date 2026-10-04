package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func scheduleEnv(t *testing.T) (*env, *service.Scheduler, *atomicClock) {
	e := newEnv(t)
	clk := &atomicClock{}
	clk.set(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	sch := &service.Scheduler{Store: e.db, Bots: e.bots, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clk.now}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Schedules: sch, SecureCookies: true})
	return e, sch, clk
}

type atomicClock struct{ ms int64 }

func (c *atomicClock) set(t time.Time)     { c.ms = t.UnixMilli() }
func (c *atomicClock) now() time.Time      { return time.UnixMilli(c.ms) }
func (c *atomicClock) add(d time.Duration) { c.ms += d.Milliseconds() }

func TestSchedulesRunWithCurrentPermissions(t *testing.T) {
	e, sch, clk := scheduleEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	sub := e.user("sub@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id

	// Validation: bad spec, bad zone, unknown action.
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "start", "spec": "every day"})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "start", "spec": "0 3 * * *", "timezone": "Mars/Olympus"})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "shell", "spec": "0 3 * * *"})
	owner.mustStatus(400, "POST", base+"/schedules", map[string]any{"action": "backup", "spec": "0 3 * * *"}) // backups not enabled here

	// A power-only sub-user may schedule restarts.
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermPower})
	var sc scheduleDTO
	json.Unmarshal(sub.mustStatus(201, "POST", base+"/schedules", map[string]any{"action": "start", "spec": "0 11 * * *", "timezone": "Europe/Berlin"}), &sc)
	if !sc.Enabled || sc.NextRunMS == nil || len(sc.Upcoming) != 3 || !sc.CanEdit {
		t.Fatalf("created: %+v", sc)
	}
	// 11:00 Berlin (UTC+1 in March before DST) is 10:00 UTC; the clock is at 10:00 already, so next day.
	if got := time.UnixMilli(*sc.NextRunMS).UTC(); got != time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("next run %v", got)
	}

	// Due: runs as the sub-user.
	before := e.rec.count()
	clk.set(time.Date(2026, 3, 2, 10, 0, 30, 0, time.UTC))
	sch.RunDue(context.Background())
	if e.rec.count() != before+1 {
		t.Fatalf("start not requested")
	}
	var l struct{ Schedules []scheduleDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/schedules", nil), &l)
	if s := l.Schedules[0]; s.LastStatus == nil || *s.LastStatus != "ok" || s.CanEdit != true {
		t.Fatalf("after run: %+v", s)
	}

	// The grant loses power: the next run is denied and the schedule pauses.
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "sub@x.io", "permissions": domain.PermViewConsole})
	sub.mustStatus(403, "DELETE", base+"/schedules/"+sc.ID, nil)
	clk.set(time.Date(2026, 3, 3, 10, 0, 10, 0, time.UTC))
	sch.RunDue(context.Background())
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/schedules", nil), &l)
	if s := l.Schedules[0]; s.LastStatus == nil || *s.LastStatus != "denied" || s.Enabled {
		t.Fatalf("after revoke: %+v", s)
	}
	if e.rec.count() != before+1 {
		t.Fatal("denied schedule still acted")
	}
	// The owner (full control) can still manage it.
	owner.mustStatus(204, "DELETE", base+"/schedules/"+sc.ID, nil)
}

func TestSchedulesDoNotCatchUpAfterDowntime(t *testing.T) {
	e, sch, clk := scheduleEnv(t)
	owner := e.user("owner@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id
	var sc scheduleDTO
	json.Unmarshal(owner.mustStatus(201, "POST", base+"/schedules", map[string]any{"action": "restart", "spec": "*/10 * * * *"}), &sc)

	// A stopped bot is not started by a scheduled restart.
	owner.mustStatus(200, "POST", base+"/schedules/"+sc.ID+"/run", nil)
	var l0 struct{ Schedules []scheduleDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/schedules", nil), &l0)
	if s := l0.Schedules[0]; s.LastStatus == nil || *s.LastStatus != "skipped" {
		t.Fatalf("restart of a stopped bot: %+v", s)
	}

	// The panel was down for three hours: one "missed" record, no burst of runs.
	before := e.rec.count()
	clk.add(3 * time.Hour)
	sch.RunDue(context.Background())
	sch.RunDue(context.Background())
	if e.rec.count() != before {
		t.Fatalf("missed runs were executed")
	}
	var l struct{ Schedules []scheduleDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/schedules", nil), &l)
	s := l.Schedules[0]
	if s.LastStatus == nil || *s.LastStatus != "missed" || !s.Enabled || s.NextRunMS == nil || *s.NextRunMS <= clk.ms {
		t.Fatalf("after downtime: %+v", s)
	}

	// Pausing clears the next run; previewing validates.
	owner.mustStatus(200, "PATCH", base+"/schedules/"+sc.ID, map[string]any{"enabled": false})
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/schedules", nil), &l)
	if l.Schedules[0].NextRunMS != nil {
		t.Fatal("paused schedule shows a next run")
	}
	owner.mustStatus(400, "GET", "/api/v1/schedules/preview?spec=0+0+30+2+*", nil)
	var p struct{ Upcoming []int64 }
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/schedules/preview?spec=@daily&timezone=UTC", nil), &p)
	if len(p.Upcoming) != 5 {
		t.Fatalf("preview: %+v", p)
	}
	// Strangers see nothing.
	stranger := e.user("s@x.io", domain.RoleUser)
	stranger.mustStatus(404, "GET", base+"/schedules", nil)
}
