package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func statusEnv(t *testing.T) (*env, *service.StatusService) {
	e := newEnv(t)
	st := &service.StatusService{Store: e.db, Bots: e.bots}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true,
		Clients: &service.APIClientService{Store: e.db, Bots: e.bots}, Status: st})
	return e, st
}

type statusOut struct {
	Title      string
	Overall    string
	Components []statusComponentDTO
	Incidents  []statusIncidentDTO
}

func publicStatus(t *testing.T, c *client) (statusOut, string) {
	t.Helper()
	raw := c.mustStatus(200, "GET", "/api/v1/status", nil)
	var out statusOut
	json.Unmarshal(raw, &out)
	return out, string(raw)
}

func component(t *testing.T, c *client, body map[string]any) string {
	t.Helper()
	var out struct{ ID string }
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/status/manage/components", body), &out)
	return out.ID
}

func stateOf(s statusOut, id string) string {
	for _, c := range s.Components {
		if c.ID == id {
			return c.State
		}
	}
	return "absent"
}

// The public page and JSON expose only what the administrator selected,
// under the names the administrator chose, and nothing about the sources.
func TestStatusPagePublicExposure(t *testing.T) {
	e, _ := statusEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	anon := &client{e: e}
	shown := alice.createBot("secret-bot-name")
	hidden := alice.createBot("unselected-bot")
	nodes, _ := e.db.ListNodes(context.Background())

	anon.mustStatus(404, "GET", "/api/v1/status", nil)
	alice.mustStatus(403, "GET", "/api/v1/status/manage", nil)
	alice.mustStatus(403, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": true})
	anon.mustStatus(401, "GET", "/api/v1/status/manage", nil)

	admin.mustStatus(200, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": true, "title": "Acme status", "intro": "Live service health."})
	panelID := component(t, admin, map[string]any{"kind": "panel", "name": "Website and API"})
	botComp := component(t, admin, map[string]any{"kind": "bot", "ref_id": shown, "name": "Discord bot", "description": "Our community bot"})
	nodeComp := component(t, admin, map[string]any{"kind": "node", "ref_id": domain.LocalNodeID, "name": "EU region"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/components", map[string]any{"kind": "bot", "ref_id": shown, "name": "Again"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/components", map[string]any{"kind": "bot", "ref_id": "00000000-0000-0000-0000-000000000000", "name": "Ghost"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/components", map[string]any{"kind": "host", "name": "x"})

	s, raw := publicStatus(t, anon)
	if s.Title != "Acme status" || len(s.Components) != 3 {
		t.Fatalf("page: %s", raw)
	}
	for _, leak := range []string{shown, hidden, "secret-bot-name", "unselected-bot", "alice@x.io", "admin@x.io", domain.LocalNodeID,
		nodes[0].Name, "ref_id", "source", `"kind":"bot"`, `"kind":"node"`, `"kind":"panel"`, "127.0.0.1"} {
		if leak != "" && strings.Contains(raw, leak) {
			t.Fatalf("public status leaks %q: %s", leak, raw)
		}
	}
	if stateOf(s, panelID) != domain.StateOperational || stateOf(s, nodeComp) != domain.StateOperational {
		t.Fatalf("states: %s", raw)
	}
	// The bot is not running: it is shown as down; nothing says why.
	if stateOf(s, botComp) != domain.StateMajor || s.Overall != domain.StateMajor {
		t.Fatalf("bot state: %s", raw)
	}
	for _, c := range s.Components {
		if len(c.Days) != service.StatusRetentionDays {
			t.Fatalf("days: %d", len(c.Days))
		}
	}

	// The management view names the sources for the manager.
	var m struct{ Components []statusComponentDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/status/manage", nil), &m)
	if len(m.Components) != 3 || m.Components[1].Source == nil || m.Components[1].Source.Name != "secret-bot-name" {
		t.Fatalf("manage view: %+v", m.Components)
	}

	// A deleted bot keeps no trace on the public page beyond its public name.
	alice.mustStatus(204, "DELETE", "/api/v1/bots/"+shown, nil)
	s, raw = publicStatus(t, anon)
	if stateOf(s, botComp) != domain.StateUnknown || strings.Contains(raw, shown) {
		t.Fatalf("deleted bot: %s", raw)
	}

	// Disabling hides everything again.
	admin.mustStatus(200, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": false})
	anon.mustStatus(404, "GET", "/api/v1/status", nil)

	var n int
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'status.component_create' AND outcome = 'ok'`).Scan(&n)
	if n != 3 {
		t.Fatalf("status.component_create audited %d times", n)
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'status.config' AND outcome = 'denied'`).Scan(&n)
	if n != 1 {
		t.Fatalf("denied config audited %d times", n)
	}
}

// A delegated status manager publishes only sources it may see.
func TestStatusPageDelegatedSources(t *testing.T) {
	e, _ := statusEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	ops := e.user("ops@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": admin.createRole("Status",
		domain.PermStatusManage, domain.PermBotsCreate, domain.PermBotsConsole)})
	mine := ops.createBot("ops-bot")
	foreign := bob.createBot("bob-bot")

	var cands struct {
		Candidates []struct{ Kind, ID, Name string }
	}
	json.Unmarshal(ops.mustStatus(200, "GET", "/api/v1/status/manage/candidates", nil), &cands)
	for _, c := range cands.Candidates {
		if c.Kind == "node" || c.ID == foreign {
			t.Fatalf("candidate leaks: %+v", cands)
		}
	}
	ops.mustStatus(403, "POST", "/api/v1/status/manage/components", map[string]any{"kind": "node", "ref_id": domain.LocalNodeID, "name": "Node"})
	ops.mustStatus(400, "POST", "/api/v1/status/manage/components", map[string]any{"kind": "bot", "ref_id": foreign, "name": "Bob"})
	component(t, ops, map[string]any{"kind": "bot", "ref_id": mine, "name": "Ops bot"})
	bob.mustStatus(403, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "x", "message": "y"})
}

// Incidents and maintenance follow their lifecycle and change component
// states while they apply.
func TestStatusIncidentLifecycle(t *testing.T) {
	e, st := statusEnv(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }
	admin := e.user("admin@x.io", domain.RoleAdmin)
	anon := &client{e: e}
	admin.mustStatus(200, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": true})
	panelID := component(t, admin, map[string]any{"kind": "panel", "name": "API"})
	nodeID := component(t, admin, map[string]any{"kind": "node", "ref_id": domain.LocalNodeID, "name": "Region"})

	admin.mustStatus(400, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "No message"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "x", "message": "y", "status": "scheduled"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "x", "message": "y", "component_ids": []string{"nope"}})
	var inc statusIncidentDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "Slow API",
		"impact": "major", "component_ids": []string{panelID}, "message": "We are looking into slow responses."}), &inc)
	if inc.Status != "investigating" || len(inc.Updates) != 1 || inc.ResolvedAtMS != nil {
		t.Fatalf("created: %+v", inc)
	}
	s, _ := publicStatus(t, anon)
	if stateOf(s, panelID) != domain.StatePartial || stateOf(s, nodeID) != domain.StateOperational || s.Overall != domain.StatePartial {
		t.Fatalf("during incident: %+v", s)
	}
	path := "/api/v1/status/manage/incidents/" + inc.ID
	admin.mustStatus(400, "POST", path+"/updates", map[string]any{"status": "monitoring"})
	admin.mustStatus(400, "POST", path+"/updates", map[string]any{"status": "completed", "message": "wrong kind"})
	now = now.Add(time.Minute)
	admin.mustStatus(201, "POST", path+"/updates", map[string]any{"status": "monitoring", "message": "A fix is deployed."})
	now = now.Add(time.Minute)
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/updates", map[string]any{"status": "resolved", "message": "Resolved."}), &inc)
	if inc.Status != "resolved" || inc.ResolvedAtMS == nil || len(inc.Updates) != 3 || inc.Updates[0].Body != "Resolved." {
		t.Fatalf("resolved: %+v", inc)
	}
	s, _ = publicStatus(t, anon)
	if stateOf(s, panelID) != domain.StateOperational || len(s.Incidents) != 1 || s.Incidents[0].Components[0].Name != "API" {
		t.Fatalf("after resolve: %+v", s)
	}
	// Reopening clears the resolution time; a status change without text
	// gets a default timeline entry.
	json.Unmarshal(admin.mustStatus(200, "PATCH", path, map[string]any{"status": "identified"}), &inc)
	if inc.ResolvedAtMS != nil || len(inc.Updates) != 4 || !strings.Contains(inc.Updates[0].Body, "identified") {
		t.Fatalf("reopened: %+v", inc)
	}
	admin.mustStatus(201, "POST", path+"/updates", map[string]any{"status": "resolved", "message": "Done."})

	// Resolved incidents leave the public page after 14 days.
	now = now.AddDate(0, 0, 15)
	if s, _ = publicStatus(t, anon); len(s.Incidents) != 0 {
		t.Fatalf("old incident still shown: %+v", s.Incidents)
	}

	// Maintenance applies inside its window.
	start, end := now.Add(time.Hour), now.Add(3*time.Hour)
	admin.mustStatus(400, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "maintenance", "title": "No window", "message": "x"})
	admin.mustStatus(400, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "maintenance", "title": "Backwards", "message": "x",
		"starts_at_ms": end.UnixMilli(), "ends_at_ms": start.UnixMilli()})
	var mt statusIncidentDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "maintenance", "title": "Upgrade",
		"component_ids": []string{nodeID}, "starts_at_ms": start.UnixMilli(), "ends_at_ms": end.UnixMilli(), "message": "Planned upgrade."}), &mt)
	if mt.Status != "scheduled" || mt.Impact != "none" {
		t.Fatalf("maintenance: %+v", mt)
	}
	if s, _ = publicStatus(t, anon); stateOf(s, nodeID) != domain.StateOperational || s.Incidents[0].Active {
		t.Fatalf("before window: %+v", s)
	}
	now = start.Add(time.Minute)
	if s, _ = publicStatus(t, anon); stateOf(s, nodeID) != domain.StateMaintenance || !s.Incidents[0].Active || s.Overall != domain.StateMaintenance {
		t.Fatalf("in window: %+v", s)
	}
	admin.mustStatus(201, "POST", "/api/v1/status/manage/incidents/"+mt.ID+"/updates", map[string]any{"status": "completed", "message": "Finished early."})
	if s, _ = publicStatus(t, anon); stateOf(s, nodeID) != domain.StateOperational {
		t.Fatalf("after completion: %+v", s)
	}
	admin.mustStatus(204, "DELETE", "/api/v1/status/manage/incidents/"+mt.ID, nil)
	admin.mustStatus(404, "DELETE", "/api/v1/status/manage/incidents/"+mt.ID, nil)
}

// Samples are counted per UTC day, become 90 daily bars with uptime, and
// counts older than the retention period are pruned.
func TestStatusSamplingAndRetention(t *testing.T) {
	e, st := statusEnv(t)
	ctx := context.Background()
	day0 := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	now := day0
	st.Now = func() time.Time { return now }
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bot := alice.createBot("b")
	admin.mustStatus(200, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": true})
	panelID := component(t, admin, map[string]any{"kind": "panel", "name": "API"})
	botID := component(t, admin, map[string]any{"kind": "bot", "ref_id": bot, "name": "Bot"})

	// Day 0: 3 samples, panel up, bot down.
	for range 3 {
		if err := st.Sample(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(5 * time.Minute)
	}
	// Day 1: an incident on the panel for 1 of 4 samples.
	now = day0.AddDate(0, 0, 1)
	var inc statusIncidentDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/status/manage/incidents", map[string]any{"kind": "incident", "title": "Down",
		"impact": "critical", "component_ids": []string{panelID}, "message": "Down."}), &inc)
	st.Sample(ctx)
	admin.mustStatus(201, "POST", "/api/v1/status/manage/incidents/"+inc.ID+"/updates", map[string]any{"status": "resolved", "message": "Up."})
	for range 3 {
		now = now.Add(5 * time.Minute)
		st.Sample(ctx)
	}
	days, _ := e.db.StatusDays(ctx, 0)
	if len(days) != 4 {
		t.Fatalf("days: %+v", days)
	}
	p, err := st.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Components {
		last, prev := c.Days[len(c.Days)-1], c.Days[len(c.Days)-2]
		switch c.Component.ID {
		case panelID:
			if prev.State != domain.StateOperational || *prev.Uptime != 100 || last.State != domain.StateMajor || *last.Uptime != 75 ||
				*c.Uptime != 85.71 || c.Days[0].State != "none" || c.Days[0].Uptime != nil {
				t.Fatalf("panel bars: %+v %+v uptime %v", prev, last, *c.Uptime)
			}
		case botID:
			if *c.Uptime != 0 || last.State != domain.StateMajor {
				t.Fatalf("bot bars: %+v", c)
			}
		}
		if c.Days[len(c.Days)-1].Date != "2026-07-02" {
			t.Fatalf("last day %s", c.Days[len(c.Days)-1].Date)
		}
	}

	// 89 days later day 0 falls out of the window and is pruned; day 1 stays.
	now = day0.AddDate(0, 0, 90)
	if err := st.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	days, _ = e.db.StatusDays(ctx, 0)
	for _, d := range days {
		if d.Day == day0.Unix()/86400 {
			t.Fatalf("day 0 not pruned: %+v", days)
		}
	}
	if len(days) != 4 {
		t.Fatalf("after prune: %+v", days)
	}
	// Deleting a component drops its history.
	admin.mustStatus(204, "DELETE", "/api/v1/status/manage/components/"+botID, nil)
	days, _ = e.db.StatusDays(ctx, 0)
	if len(days) != 2 {
		t.Fatalf("after delete: %+v", days)
	}
}

// The public endpoints are rate-limited per client address.
func TestStatusAndHelpRateLimited(t *testing.T) {
	e, _ := statusEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	admin.mustStatus(200, "PUT", "/api/v1/status/manage/config", map[string]any{"enabled": true})
	anon := &client{e: e}
	got429 := false
	for range 70 {
		if resp, _ := anon.do("GET", "/api/v1/status", nil); resp.StatusCode == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("status endpoint not rate limited")
	}
}
