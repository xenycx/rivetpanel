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

// personalWS returns an account's personal workspace id.
func personalWS(t *testing.T, e *env, email string) string {
	t.Helper()
	w, err := e.db.PersonalWorkspace(t.Context(), e.userID(email))
	if err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func quotaPath(ws string) string { return "/api/v1/admin/workspaces/" + ws + "/quota" }

// teamWS creates a team workspace owned by c and returns its id.
func teamWS(c *client, name string) string {
	var out struct{ Workspace struct{ ID string } }
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/workspaces", map[string]any{"name": name}), &out)
	return out.Workspace.ID
}

// The case limits were built for: one workspace may hold one Discord bot and
// nothing else, while every other workspace keeps the panel defaults.
func TestWorkspaceQuotaOneBotAndNothingElse(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("one@x.io", domain.RoleUser)
	ws := personalWS(t, e, "one@x.io")
	q := quotaPath(ws)

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
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "second", "runtime": "python"}, `workspace \"Personal\" allows at most 1 bots`)
	expect400(t, u, "POST", "/api/v1/games", newGame("Survival"), "does not allow game servers")
	expect400(t, u, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}, "does not allow sites")

	var cp struct {
		Bots      int               `json:"bots"`
		Limits    map[string]*int64 `json:"limits"`
		Usage     map[string]int64  `json:"usage"`
		Workspace struct{ ID string }
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/me/capacity", nil), &cp)
	if cp.Bots != 1 || *cp.Limits["bots"] != 1 || *cp.Limits["games"] != 0 || cp.Limits["apps"] != nil || cp.Usage["bots"] != 1 || cp.Workspace.ID != ws {
		t.Fatalf("capacity %+v", cp)
	}
	// Members read their workspace's limits; others cannot.
	u.mustStatus(200, "GET", "/api/v1/workspaces/"+ws+"/limits", nil)

	// Other workspaces keep the panel defaults (no per-kind limits, 3 sites).
	other := e.user("other@x.io", domain.RoleUser)
	other.mustStatus(404, "GET", "/api/v1/workspaces/"+ws+"/limits", nil)
	other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs"})
	other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs"})
	other.mustStatus(201, "POST", "/api/v1/games", newGame("Creative"))
	other.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"})

	// Nobody can hand the limited workspace a second app.
	var bot struct {
		ID string `json:"id"`
	}
	json.Unmarshal(other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "gift", "runtime": "nodejs"}), &bot)
	expect400(t, other, "POST", "/api/v1/bots/"+bot.ID+"/transfer", map[string]any{"email": "one@x.io"}, "personal workspace has no room")

	// Clearing the limits restores the defaults.
	admin.mustStatus(204, "DELETE", q, nil)
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "second", "runtime": "python"})
	other.mustStatus(200, "POST", "/api/v1/bots/"+bot.ID+"/transfer", map[string]any{"email": "one@x.io"})

	var actions []string
	rows, err := e.db.QueryContext(t.Context(), `SELECT action || ' ' || COALESCE(target, '') FROM audit_events
		WHERE action LIKE 'admin.workspace_quota%' AND outcome = 'ok' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		actions = append(actions, s)
	}
	rows.Close()
	want := []string{"admin.workspace_quota_set " + ws + " bots=1 games=0 web_apps=0 sites=0", "admin.workspace_quota_clear " + ws + " panel defaults"}
	if fmt.Sprint(actions) != fmt.Sprint(want) {
		t.Fatalf("audit %q", actions)
	}
}

// Workspace limits replace the panel default field by field, in both
// directions; Panel settings replace the environment's defaults.
func TestWorkspaceQuotaOverridesPanelDefaults(t *testing.T) {
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

	plain.mustStatus(201, "POST", "/api/v1/bots", bot("a"))
	plain.mustStatus(201, "POST", "/api/v1/bots", bot("b"))
	expect400(t, plain, "POST", "/api/v1/bots", bot("c"), "allows at most 2 apps (bots, game servers and web apps together) and 2 already exist")

	admin.mustStatus(200, "PUT", quotaPath(personalWS(t, e, "more@x.io")), map[string]any{"max_apps": 4, "memory_bytes": 2 << 30})
	for i := range 4 {
		more.mustStatus(201, "POST", "/api/v1/bots", bot(fmt.Sprint("m", i)))
	}
	expect400(t, more, "POST", "/api/v1/bots", bot("m4"), "allows at most 4 apps")

	admin.mustStatus(200, "PUT", quotaPath(personalWS(t, e, "less@x.io")), map[string]any{"memory_bytes": 200 << 20})
	less.mustStatus(201, "POST", "/api/v1/bots", bot("l1"))
	expect400(t, less, "POST", "/api/v1/bots", bot("l2"), "may use at most 200 MiB of memory; 128 MiB is already assigned")
	// The app count still comes from the default (2): the limits left it unset.
	var cp struct {
		Limits map[string]*int64 `json:"limits"`
	}
	json.Unmarshal(less.mustStatus(200, "GET", "/api/v1/me/capacity", nil), &cp)
	if *cp.Limits["apps"] != 2 || *cp.Limits["memory_bytes"] != 200<<20 || cp.Limits["bots"] != nil {
		t.Fatalf("limits %+v", cp.Limits)
	}

	// Panel settings replace the environment's default at once; null hands
	// it back.
	plain.mustStatus(403, "PUT", "/api/v1/admin/limits", map[string]any{"max_apps": 9})
	var d struct {
		Limits map[string]*int64 `json:"limits"`
		Source map[string]string `json:"source"`
	}
	json.Unmarshal(admin.mustStatus(200, "PUT", "/api/v1/admin/limits", map[string]any{"max_apps": 3, "max_sites": 0}), &d)
	if *d.Limits["max_apps"] != 3 || d.Source["max_apps"] != "panel" || d.Source["memory_bytes"] != "env" || *d.Limits["max_sites"] != 0 || *d.Limits["team_workspaces"] != 20 {
		t.Fatalf("defaults %+v", d)
	}
	plain.mustStatus(201, "POST", "/api/v1/bots", bot("c"))
	expect400(t, plain, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}, "does not allow sites")
	json.Unmarshal(admin.mustStatus(200, "PUT", "/api/v1/admin/limits", map[string]any{}), &d)
	if *d.Limits["max_apps"] != 2 || d.Source["max_apps"] != "env" || *d.Limits["max_sites"] != 3 {
		t.Fatalf("defaults after clearing %+v", d)
	}

	// Workspaces owned by administrators are exempt.
	for i := range 3 {
		admin.mustStatus(201, "POST", "/api/v1/bots", bot(fmt.Sprint("r", i)))
	}
}

// Two creations at once cannot both take the last place of a limit: the
// check runs inside the insert's transaction.
func TestWorkspaceQuotaConcurrentCreates(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("racer@x.io", domain.RoleUser)
	ws := personalWS(t, e, "racer@x.io")
	admin.mustStatus(200, "PUT", quotaPath(ws), map[string]any{"max_bots": 1, "max_sites": 1})

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
	use, err := e.db.WorkspaceUsage(t.Context(), ws)
	if err != nil || use.Bots != 1 || use.Sites != 1 {
		t.Fatalf("usage %+v %v", use, err)
	}
}

// Lowering limits below what a workspace holds stops and deletes nothing;
// it only refuses growth. Shrinking still works.
func TestWorkspaceQuotaLoweredBelowUsage(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("big@x.io", domain.RoleUser)
	ws := personalWS(t, e, "big@x.io")
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
	admin.mustStatus(200, "PUT", quotaPath(ws), map[string]any{"max_bots": 1, "memory_bytes": 256 << 20})

	b, err := e.db.GetBot(t.Context(), ids[0])
	if err != nil || b.DesiredState != domain.DesiredRunning {
		t.Fatalf("running bot touched: %+v %v", b.DesiredState, err)
	}
	if use, _ := e.db.WorkspaceUsage(t.Context(), ws); use.Bots != 3 {
		t.Fatalf("usage %+v", use)
	}
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "more", "runtime": "nodejs"}, "at most 1 bots and 3 already exist")
	expect400(t, u, "PATCH", "/api/v1/bots/"+ids[1], map[string]any{"memory_bytes": 512 << 20}, "may use at most 256 MiB of memory")
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+ids[1], map[string]any{"memory_bytes": 128 << 20})
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+ids[2], nil)
}

// A team workspace's limits hold every member's apps together, and the
// refusal names the workspace.
func TestWorkspaceQuotaSharedByMembers(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	owner := e.user("lead@x.io", domain.RoleUser)
	dev := e.user("dev@x.io", domain.RoleUser)
	ws := teamWS(owner, "Studio")
	owner.mustStatus(200, "PUT", "/api/v1/workspaces/"+ws+"/members", map[string]any{"email": "dev@x.io", "role": "developer"})
	admin.mustStatus(200, "PUT", quotaPath(ws), map[string]any{"max_apps": 2, "memory_bytes": 1 << 30})

	app := func(n string, mem int64) map[string]any {
		return map[string]any{"name": n, "runtime": "nodejs", "memory_bytes": mem, "workspace_id": ws}
	}
	owner.mustStatus(201, "POST", "/api/v1/bots", app("one", 512<<20))
	expect400(t, dev, "POST", "/api/v1/bots", app("big", 768<<20), `workspace \"Studio\" may use at most 1024 MiB of memory; 512 MiB is already assigned`)
	dev.mustStatus(201, "POST", "/api/v1/bots", app("two", 256<<20))
	expect400(t, dev, "POST", "/api/v1/bots", app("three", 64<<20), `workspace \"Studio\" allows at most 2 apps`)
	// The personal workspaces are separate.
	dev.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "mine", "runtime": "nodejs"})

	var v struct {
		Usage  map[string]int64  `json:"usage"`
		Limits map[string]*int64 `json:"limits"`
	}
	json.Unmarshal(dev.mustStatus(200, "GET", "/api/v1/workspaces/"+ws+"/limits", nil), &v)
	if v.Usage["apps"] != 2 || v.Usage["memory_bytes"] != 768<<20 || *v.Limits["apps"] != 2 {
		t.Fatalf("limits view %+v", v)
	}
	// The summary's memory figure is the one the limits count.
	var w struct {
		Workspace struct {
			MemoryBytes int64 `json:"memory_bytes"`
		}
	}
	json.Unmarshal(dev.mustStatus(200, "GET", "/api/v1/workspaces/"+ws, nil), &w)
	if w.Workspace.MemoryBytes != 768<<20 {
		t.Fatalf("summary memory %d", w.Workspace.MemoryBytes)
	}
	// The admin list shows which workspaces have their own limits.
	var list struct {
		Workspaces []struct {
			ID    string    `json:"id"`
			Quota *quotaDTO `json:"quota"`
		} `json:"workspaces"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/workspaces", nil), &list)
	custom := 0
	for _, x := range list.Workspaces {
		if x.Quota != nil {
			custom++
			if x.ID != ws || *x.Quota.MaxApps != 2 {
				t.Fatalf("list quota %+v", x)
			}
		}
	}
	if custom != 1 {
		t.Fatalf("%d workspaces with limits", custom)
	}
}

// Moving an app or a site into a full workspace is refused; moving out is not.
func TestWorkspaceQuotaRefusesMoveIntoFullWorkspace(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("mover@x.io", domain.RoleUser)
	full := teamWS(u, "Full")
	admin.mustStatus(200, "PUT", quotaPath(full), map[string]any{"max_apps": 1, "max_sites": 0})
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "in", "runtime": "nodejs", "workspace_id": full})

	var b, st struct {
		ID string `json:"id"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "out", "runtime": "nodejs"}), &b)
	expect400(t, u, "PUT", "/api/v1/bots/"+b.ID+"/workspace", map[string]any{"workspace_id": full}, `workspace \"Full\" allows at most 1 apps`)
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}), &st)
	expect400(t, u, "PATCH", "/api/v1/sites/"+st.ID, map[string]any{"workspace_id": full}, `workspace \"Full\" does not allow sites`)
	if use, _ := e.db.WorkspaceUsage(t.Context(), full); use.Apps != 1 || use.Sites != 0 {
		t.Fatalf("usage after refused moves %+v", use)
	}
	// Raising the limit lets the move through.
	admin.mustStatus(200, "PUT", quotaPath(full), map[string]any{"max_apps": 2})
	u.mustStatus(200, "PUT", "/api/v1/bots/"+b.ID+"/workspace", map[string]any{"workspace_id": full})
}

// CPU counts every app's CPU limit; raising one past the limit is refused.
func TestWorkspaceQuotaCPU(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("cpu@x.io", domain.RoleUser)
	admin.mustStatus(200, "PUT", quotaPath(personalWS(t, e, "cpu@x.io")), map[string]any{"nano_cpus": 1_500_000_000})
	var b struct {
		ID string `json:"id"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "a", "runtime": "nodejs", "nano_cpus": 1_000_000_000}), &b)
	expect400(t, u, "POST", "/api/v1/bots", map[string]any{"name": "b", "runtime": "nodejs", "nano_cpus": 1_000_000_000},
		"may use at most 1.5 CPU cores; 1 are already assigned and this change needs 1 more")
	u.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "c", "runtime": "nodejs", "nano_cpus": 500_000_000})
	expect400(t, u, "PATCH", "/api/v1/bots/"+b.ID, map[string]any{"nano_cpus": 1_250_000_000}, "at most 1.5 CPU cores")
	u.mustStatus(200, "PATCH", "/api/v1/bots/"+b.ID, map[string]any{"nano_cpus": 500_000_000})
}

// Each workspace gets the defaults, so the number of team workspaces an
// account may own is a setting too; 0 allows none.
func TestTeamWorkspaceLimitSetting(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	admin := e.user("root@x.io", domain.RoleAdmin)
	u := e.user("teams@x.io", domain.RoleUser)
	admin.mustStatus(200, "PUT", "/api/v1/admin/limits", map[string]any{"team_workspaces": 1})
	teamWS(u, "One")
	expect400(t, u, "POST", "/api/v1/workspaces", map[string]any{"name": "Two"}, "at most 1 team workspaces")
	admin.mustStatus(200, "PUT", "/api/v1/admin/limits", map[string]any{"team_workspaces": 0})
	expect400(t, u, "POST", "/api/v1/workspaces", map[string]any{"name": "Two"}, "does not allow creating team workspaces")
	teamWS(admin, "Admins are not limited")
}
