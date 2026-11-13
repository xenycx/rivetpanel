package sqlite

import (
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestActivityTimelineMergesFiltersAndPages(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	db.EnsureLocalNode(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x','h',1,1), ('v','v@x','h',1,1)`)
	for _, id := range []string{"b1", "b2"} {
		if err := db.CreateBot(ctx, domain.Bot{ID: id, OwnerID: "u", NodeID: domain.LocalNodeID, Name: "app-" + id, Runtime: "nodejs", ImageRef: "i",
			Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
			t.Fatal(err)
		}
	}
	s := func(v string) *string { return &v }
	audit := func(at int64, actor, bot, action, outcome, source string) {
		t.Helper()
		e := domain.AuditEvent{AtMS: at, Action: action, Outcome: outcome, Source: source}
		if actor != "" {
			e.ActorID, e.ActorLabel = s(actor), s(actor+"@x")
		}
		if bot != "" {
			e.BotID, e.BotName = s(bot), s("app-"+bot)
		}
		if err := db.InsertAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	audit(1000, "u", "b1", "bot.start", "ok", "person")
	audit(2000, "u", "b1", "env.set", "ok", "person")
	audit(3000, "u", "b1", "scheduled.restart", "ok", "schedule")
	audit(3000, "", "b2", "bot.crash", "failed", "system")
	audit(4000, "v", "", "account.sign_in", "ok", "person") // v's own account event
	op := func(id, bot, kind, trigger, status string, at int64, actor *string) {
		t.Helper()
		if _, err := db.InsertOperation(ctx, domain.Operation{ID: id, BotID: bot, Kind: kind, Trigger: trigger, ActorID: actor, Status: "queued", CreatedAtMS: at}); err != nil {
			t.Fatal(err)
		}
		if status != "queued" {
			if err := db.FinishOperation(ctx, id, status, nil, nil, nil, 0, at+10); err != nil {
				t.Fatal(err)
			}
		}
	}
	op("o1", "b1", "deploy", "push", "succeeded", 2500, nil)
	op("o2", "b2", "build", "manual", "failed", 3000, s("u"))
	op("o3", "b1", "backup", "schedule", "queued", 5000, nil)

	keys := func(items []domain.ActivityItem) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.Key
		}
		return out
	}
	all, err := db.ListActivity(ctx, ActivityFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 8 {
		t.Fatalf("all = %v", keys(all))
	}
	for i := 1; i < len(all); i++ {
		if !ActivityAfter(all[i].AtMS, all[i].Key, all[i-1].AtMS, all[i-1].Key) {
			t.Fatalf("not ordered at %d: %v", i, keys(all))
		}
	}
	if all[0].Key != "o:o3" || all[0].Source != "schedule" || all[0].Outcome != "running" || all[0].Category != "backups" {
		t.Fatalf("newest = %+v", all[0])
	}

	// Paging with a cursor through items that share a timestamp loses nothing.
	var paged []domain.ActivityItem
	f := ActivityFilter{Limit: 3}
	for {
		page, err := db.ListActivity(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page...)
		if len(page) < f.Limit {
			break
		}
		f.BeforeMS, f.BeforeKey = page[len(page)-1].AtMS, page[len(page)-1].Key
	}
	if len(paged) != len(all) {
		t.Fatalf("paged = %v, all = %v", keys(paged), keys(all))
	}

	check := func(name string, f ActivityFilter, want ...string) {
		t.Helper()
		f.Limit = 50
		got, err := db.ListActivity(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		if g := keys(got); len(g) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, g, want)
		} else {
			for i := range g {
				if g[i] != want[i] {
					t.Fatalf("%s: got %v, want %v", name, g, want)
				}
			}
		}
	}
	a := func(id int) string { return "a:" + pad(id) }
	check("automatic", ActivityFilter{Sources: []string{"schedule", "push", "system", "ai"}}, "o:o3", a(4), a(3), "o:o1")
	check("people", ActivityFilter{Sources: []string{"person"}}, a(5), "o:o2", a(2), a(1))
	check("failed", ActivityFilter{Outcomes: []string{"failed"}}, "o:o2", a(4))
	check("power", ActivityFilter{Categories: []string{"power"}}, a(4), a(3), a(1))
	check("deploys", ActivityFilter{Categories: []string{"deploys"}}, "o:o2", "o:o1")
	check("app", ActivityFilter{Apps: []string{"b2"}}, "o:o2", a(4))
	check("actor", ActivityFilter{Actors: []string{"u"}}, "o:o2", a(3), a(2), a(1))
	check("window", ActivityFilter{FromMS: 2000, ToMS: 3000}, "o:o1", a(2))
	check("search", ActivityFilter{Query: "app-b2"}, "o:o2", a(4))
	check("work only", ActivityFilter{Types: []string{"work"}}, "o:o3", "o:o2", "o:o1")
	// v sees only their own account event: they have no access to the apps.
	check("visibility", ActivityFilter{VisibleTo: "v"}, a(5))

	st, err := db.ActivityStats(ctx, ActivityFilter{FromMS: 0, ToMS: 6000}, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 8 || len(st.Buckets) != 6 || st.Buckets[3].People != 1 || st.Buckets[3].Automatic != 2 || st.Buckets[3].Failed != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if st.BySource["person"] != 4 || st.ByCategory["power"] != 3 || st.ByOutcome["running"] != 1 {
		t.Fatalf("by = %v %v %v", st.BySource, st.ByCategory, st.ByOutcome)
	}
	if len(st.People) != 2 || st.People[0].Person.ID != "u" || st.People[0].Count != 3 || st.People[0].Failed != 1 {
		t.Fatalf("people = %+v", st.People)
	}
	if len(st.Apps) != 2 || st.Apps[0].ID != "b1" || st.Apps[0].Count != 5 {
		t.Fatalf("apps = %+v", st.Apps)
	}
	if st.Week[3][0] != 8 { // 1970-01-01 00:00 UTC was a Thursday
		t.Fatalf("week = %v", st.Week)
	}
	ps, err := db.ActivityPeople(ctx, ActivityFilter{}, 10)
	if err != nil || len(ps) != 2 || ps[0].ID != "u" {
		t.Fatalf("people = %+v, %v", ps, err)
	}
}

func pad(id int) string {
	s := "00000000000000000000"
	n := []byte(s)
	for i := len(n) - 1; id > 0; i-- {
		n[i] = byte('0' + id%10)
		id /= 10
	}
	return string(n)
}

// Audit events can carry no app or site name: add-on queries never recorded
// one, and deletes look the name up after the row is gone. The summary must
// still answer, falling back to the live name or an empty one.
func TestActivityStatsToleratesMissingNames(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	db.EnsureLocalNode(ctx)
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x','h',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateBot(ctx, domain.Bot{ID: "b1", OwnerID: "u", NodeID: domain.LocalNodeID, Name: "live-name", Runtime: "nodejs", ImageRef: "i",
		Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	s := func(v string) *string { return &v }
	for _, e := range []domain.AuditEvent{
		{AtMS: 1000, ActorID: s("u"), Action: "addon.query", BotID: s("b1"), Outcome: "ok", Source: "person"},
		{AtMS: 1100, ActorID: s("u"), Action: "bot.delete", BotID: s("gone"), Outcome: "ok", Source: "person"},
		{AtMS: 1200, ActorID: s("u"), Action: "site.delete", SiteID: s("gone-site"), Outcome: "ok", Source: "person"},
		{AtMS: 1300, ActorID: s("u"), Action: "account.sign_in", Outcome: "ok", Source: "person"},
	} {
		if err := db.InsertAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	st, err := db.ActivityStats(ctx, ActivityFilter{FromMS: 0, ToMS: 2000}, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, a := range st.Apps {
		names[a.ID] = a.Name
	}
	if st.Total != 4 || len(st.Apps) != 3 || names["b1"] != "live-name" || names["gone"] != "" || names["gone-site"] != "" {
		t.Fatalf("stats = %+v", st)
	}

	// The 0071 backfill fills the names that the live rows still know.
	if _, err := db.ExecContext(ctx, `UPDATE audit_events SET bot_name = (SELECT substr(name, 1, 64) FROM bots WHERE bots.id = audit_events.bot_id)
		WHERE bot_name IS NULL AND bot_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	var got *string
	db.QueryRowContext(ctx, `SELECT bot_name FROM audit_events WHERE action = 'addon.query'`).Scan(&got)
	if got == nil || *got != "live-name" {
		t.Fatalf("backfilled name = %v", got)
	}
}
