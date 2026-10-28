package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/blueprints"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// newQuotaRig is a panel with bots, game servers, sites and auditing.
func newQuotaRig(t *testing.T) *siteRig {
	t.Helper()
	r := newSiteRig(t)
	e := r.e
	g := &service.GameService{Bots: e.bots, Store: e.db, Providers: &blueprint.Providers{}, Builtins: blueprints.FS,
		LocalPortFree: func(int) bool { return true }}
	if err := g.SyncBuiltins(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		SecureCookies: true, Nodes: e.db, MaxUpload: 2 << 20, Sites: r.svc, Games: g, Audit: &service.Audit{Store: e.db, Bots: e.bots}})
	return r
}

func newGame(name string) map[string]any {
	return map[string]any{"name": name, "blueprint": "minecraft-paper", "memory_bytes": 1 << 30,
		"variables": map[string]string{"MINECRAFT_VERSION": "1.21.11"}, "agreements": []string{"minecraft-eula"}}
}

func expect400(t *testing.T, c *client, method, path string, body any, msg string) {
	t.Helper()
	resp, b := c.do(method, path, body)
	if resp.StatusCode != 400 || !strings.Contains(string(b), msg) {
		t.Fatalf("%s %s: %d %s (want 400 %q)", method, path, resp.StatusCode, b, msg)
	}
}

// The case quotas were built for: one account may hold one Discord bot and
// nothing else, while every other account keeps the panel defaults.
func TestUserQuotaOneBotAndNothingElse(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("one@x.io", domain.RoleUser)
	id := e.userID("one@x.io")
	q := "/api/v1/admin/users/" + id + "/quota"

	u.mustStatus(403, "PUT", q, map[string]any{"max_bots": 5})
	u.mustStatus(403, "GET", q, nil)
	admin.mustStatus(400, "PUT", q, map[string]any{"max_bots": -1})
	var view struct {
		Quota    quotaDTO         `json:"quota"`
		Usage    map[string]int64 `json:"usage"`
		Defaults map[string]int64 `json:"defaults"`
		Exempt   bool             `json:"exempt"`
	}
	json.Unmarshal(admin.mustStatus(200, "PUT", q, map[string]any{"max_bots": 1, "max_games": 0, "max_web_apps": 0, "max_sites": 0}), &view)
	if view.Quota.MaxBots == nil || *view.Quota.MaxBots != 1 || view.Quota.MaxApps != nil || view.Defaults["max_sites"] != 3 || view.Exempt {
		t.Fatalf("view %+v", view)
	}

	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "discord", "runtime": "nodejs"})
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "second", "runtime": "python"}, "at most 1 bots")
	expect400(t, u, "POST", "/api/v1/games", newGame("Survival"), "does not allow game servers")
	expect400(t, u, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}, "does not allow sites")

	var cp struct {
		Bots   int               `json:"bots"`
		Limits map[string]*int64 `json:"limits"`
		Usage  map[string]int64  `json:"usage"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/me/capacity", nil), &cp)
	if cp.Bots != 1 || *cp.Limits["bots"] != 1 || *cp.Limits["games"] != 0 || cp.Limits["apps"] != nil || cp.Usage["bots"] != 1 {
		t.Fatalf("capacity %+v", cp)
	}

	// Other accounts keep the panel defaults (no per-kind limits, 3 sites).
	other := e.user("other@x.io", domain.RoleUser)
	other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs"})
	other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs"})
	other.mustStatus(201, "POST", "/api/v1/games", newGame("Creative"))
	other.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"})

	// Nobody can hand the limited account a second app.
	var bot struct {
		ID string `json:"id"`
	}
	json.Unmarshal(other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "gift", "runtime": "nodejs"}), &bot)
	expect400(t, other, "POST", "/api/v1/bots/"+bot.ID+"/transfer", map[string]any{"email": "one@x.io"}, "quota has no room")

	// Clearing the quota restores the defaults.
	admin.mustStatus(204, "DELETE", q, nil)
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "second", "runtime": "python"})
	other.mustStatus(200, "POST", "/api/v1/bots/"+bot.ID+"/transfer", map[string]any{"email": "one@x.io"})

	var actions []string
	rows, err := e.db.QueryContext(t.Context(), `SELECT action || ' ' || COALESCE(target, '') || ' ' || COALESCE(subject_user_id, '') FROM audit_events
		WHERE action LIKE 'admin.user_quota%' AND outcome = 'ok' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		actions = append(actions, s)
	}
	rows.Close()
	want := []string{"admin.user_quota_set bots=1 games=0 web_apps=0 sites=0 " + id, "admin.user_quota_clear panel defaults " + id}
	if fmt.Sprint(actions) != fmt.Sprint(want) {
		t.Fatalf("audit %q", actions)
	}
}

// A quota replaces the panel default field by field, in both directions.
func TestUserQuotaOverridesPanelDefaults(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	e.bots.Limits.MaxBotsPerUser, e.bots.Limits.UserMemoryBytes = 2, 1<<30
	admin := e.user("root@x.io", domain.RoleAdmin)
	more := e.user("more@x.io", domain.RoleUser)
	less := e.user("less@x.io", domain.RoleUser)
	plain := e.user("plain@x.io", domain.RoleUser)
	bot := func(n string) map[string]any {
		return map[string]any{"name": n, "runtime": "nodejs", "memory_bytes": 128 << 20}
	}

	// Defaults only: the messages accounts saw before quotas existed.
	plain.mustStatus(201, "POST", "/api/v1/bots", bot("a"))
	plain.mustStatus(201, "POST", "/api/v1/bots", bot("b"))
	expect400(t, plain, "POST", "/api/v1/bots", bot("c"), "your account can have at most 2 bots; delete one first")

	admin.mustStatus(200, "PUT", "/api/v1/admin/users/"+e.userID("more@x.io")+"/quota", map[string]any{"max_apps": 4, "memory_bytes": 2 << 30})
	for i := range 4 {
		more.mustStatus(201, "POST", "/api/v1/bots", bot(fmt.Sprint("m", i)))
	}
	expect400(t, more, "POST", "/api/v1/bots", bot("m4"), "quota allows at most 4 apps")

	admin.mustStatus(200, "PUT", "/api/v1/admin/users/"+e.userID("less@x.io")+"/quota", map[string]any{"memory_bytes": 200 << 20})
	less.mustStatus(201, "POST", "/api/v1/bots", bot("l1"))
	expect400(t, less, "POST", "/api/v1/bots", bot("l2"), "memory quota is 200 MiB; 128 MiB is already assigned")
	// The app count still comes from the default (2): the quota left it unset.
	var cp struct {
		Limits map[string]*int64 `json:"limits"`
	}
	json.Unmarshal(less.mustStatus(200, "GET", "/api/v1/me/capacity", nil), &cp)
	if *cp.Limits["apps"] != 2 || *cp.Limits["memory_bytes"] != 200<<20 || cp.Limits["bots"] != nil {
		t.Fatalf("limits %+v", cp.Limits)
	}
	// Administrators are exempt.
	for i := range 3 {
		admin.mustStatus(201, "POST", "/api/v1/bots", bot(fmt.Sprint("r", i)))
	}
}

// Two creations at once cannot both take the last place of a quota: the
// check runs inside the insert's transaction.
func TestUserQuotaConcurrentCreates(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("racer@x.io", domain.RoleUser)
	admin.mustStatus(200, "PUT", "/api/v1/admin/users/"+e.userID("racer@x.io")+"/quota", map[string]any{"max_bots": 1, "max_sites": 1})

	for _, c := range []struct {
		path string
		body func(int) any
	}{
		{"/api/v1/bots", func(i int) any { return map[string]any{"name": fmt.Sprint("b", i), "runtime": "nodejs"} }},
		{"/api/v1/sites", func(i int) any { return map[string]string{"name": fmt.Sprint("site ", i)} }},
	} {
		var wg sync.WaitGroup
		var mu sync.Mutex
		codes := map[int]int{}
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, _ := u.do("POST", c.path, c.body(i))
				mu.Lock()
				codes[resp.StatusCode]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		if codes[201] != 1 || codes[400] != 7 {
			t.Fatalf("%s: %v", c.path, codes)
		}
	}
	use, err := e.db.AccountUsage(t.Context(), e.userID("racer@x.io"))
	if err != nil || use.Bots != 1 || use.Sites != 1 {
		t.Fatalf("usage %+v %v", use, err)
	}
}

// Lowering a quota below what an account holds stops and deletes nothing;
// it only refuses growth. Shrinking still works.
func TestUserQuotaLoweredBelowUsage(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("big@x.io", domain.RoleUser)
	var ids []string
	for i := range 3 {
		var b struct {
			ID string `json:"id"`
		}
		json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": fmt.Sprint("b", i), "runtime": "nodejs", "memory_bytes": 256 << 20}), &b)
		ids = append(ids, b.ID)
	}
	if _, _, err := e.db.SetDesired(t.Context(), ids[0], domain.DesiredRunning, false, 1); err != nil {
		t.Fatal(err)
	}
	admin.mustStatus(200, "PUT", "/api/v1/admin/users/"+e.userID("big@x.io")+"/quota", map[string]any{"max_bots": 1, "memory_bytes": 256 << 20})

	b, err := e.db.GetBot(t.Context(), ids[0])
	if err != nil || b.DesiredState != domain.DesiredRunning {
		t.Fatalf("running bot touched: %+v %v", b.DesiredState, err)
	}
	if use, _ := e.db.AccountUsage(t.Context(), e.userID("big@x.io")); use.Bots != 3 {
		t.Fatalf("usage %+v", use)
	}
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "more", "runtime": "nodejs"}, "at most 1 bots and 3 already exist")
	expect400(t, u, "PATCH", "/api/v1/bots/"+ids[1], map[string]any{"memory_bytes": 512 << 20}, "memory quota")
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+ids[1], map[string]any{"memory_bytes": 128 << 20})
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+ids[2], nil)
}
