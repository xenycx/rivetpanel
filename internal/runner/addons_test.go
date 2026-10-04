package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
)

func addonRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	g := newRig(t, func(o *Options) { o.AddonData = addons.DataRoot{Dir: filepath.Join(dir, "addons")} })
	ctx := context.Background()
	if err := g.db.CreateBotAddon(ctx, domain.BotAddon{BotID: botID, Kind: "postgres", MemoryBytes: 256 << 20}, nil, 3); err != nil {
		t.Fatal(err)
	}
	if err := g.db.CreateBotAddon(ctx, domain.BotAddon{BotID: botID, Kind: "redis", MemoryBytes: 64 << 20}, nil, 4); err != nil {
		t.Fatal(err)
	}
	g.env[domain.AddonPasswordVar("postgres")] = "pgpass"
	g.env["DB_PASSWORD"] = "${POSTGRES_PASSWORD}"
	return g
}

func TestAddonsStartBeforeBotOnPrivateNetwork(t *testing.T) {
	g := addonRig(t)
	g.desire("running", false)
	g.pass()
	g.wantState("running")
	net := AddonNetwork(botID)
	if !g.fd.networks[net] {
		t.Fatal("private network not created")
	}
	var pg, bot *ContainerSpec
	for i, s := range g.fd.created {
		switch {
		case s.AddonKind == "postgres":
			pg = &g.fd.created[i]
		case s.Role == RoleRuntime:
			bot = &g.fd.created[i]
		}
	}
	if pg == nil || bot == nil || len(g.fd.created) != 3 {
		t.Fatalf("created %+v", g.fd.created)
	}
	if g.fd.created[len(g.fd.created)-1].Role != RoleRuntime {
		t.Fatal("the bot must be created after its add-ons")
	}
	// The add-on: private network only, aliased by kind, hardened like a bot.
	if pg.Network != net || len(pg.NetworkAliases) != 1 || pg.NetworkAliases[0] != "postgres" || pg.WorkspaceHostPath != "" ||
		!pg.KeepImageEntrypoint || len(pg.Mounts) != 1 || pg.Mounts[0].Target != "/var/lib/postgresql/data" || pg.User == "" {
		t.Fatalf("postgres spec %+v", pg)
	}
	if st, err := os.Stat(pg.Mounts[0].Source); err != nil || !st.IsDir() {
		t.Fatalf("data dir: %v", err)
	}
	if !strings.Contains(strings.Join(pg.Env, " "), "POSTGRES_PASSWORD=pgpass") || strings.Contains(pg.SpecHash, "pgpass") {
		t.Fatal("password handling wrong")
	}
	// The bot: keeps its own network and joins the private one after create.
	if bot.Network != "none" && bot.Network != g.r.opts.Network {
		t.Fatalf("bot network %q", bot.Network)
	}
	env := strings.Join(bot.Env, "\n")
	for _, want := range []string{"DATABASE_URL=postgres://bot:pgpass@postgres:5432/bot?sslmode=disable", "REDIS_URL=redis://redis:6379", "DB_PASSWORD=pgpass"} {
		if !strings.Contains(env, want) {
			t.Errorf("bot env missing %s", want)
		}
	}
	if strings.Contains(env, "RIVET_ADDON_") {
		t.Error("internal add-on password variable reached the bot")
	}
	b := g.bot()
	if j := g.fd.joined[*b.ContainerID]; len(j) != 1 || j[0] != net {
		t.Fatalf("bot joined %v", j)
	}
}

func TestAddonsReusedAcrossRestartAndRemovedWithBot(t *testing.T) {
	g := addonRig(t)
	g.desire("running", false)
	g.pass()
	g.desire("stopped", false)
	g.pass()
	g.wantState("stopped")
	for _, c := range g.fd.conts {
		if c.Live() {
			t.Fatalf("%s still running after stop", c.Name)
		}
	}
	created := len(g.fd.created)
	g.desire("running", false)
	g.pass()
	g.wantState("running")
	// Only the bot container is new: the add-ons are started again, not recreated.
	if len(g.fd.created) != created+1 {
		t.Fatalf("created %d more containers", len(g.fd.created)-created)
	}
	dataDir := filepath.Dir(g.fd.created[0].Mounts[0].Source)
	if err := g.r.Purge(context.Background(), botID); err != nil {
		t.Fatal(err)
	}
	if len(g.fd.conts) != 0 || g.fd.networks[AddonNetwork(botID)] {
		t.Fatalf("leftovers: %d containers, networks %v", len(g.fd.conts), g.fd.networks)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("add-on data not removed: %v", err)
	}
}

func TestUnhealthyAddonFailsStartWithoutCreatingBot(t *testing.T) {
	g := addonRig(t)
	g.fd.health = "starting"
	g.advance(0)
	// The health wait uses the runner clock: jump past the deadline on sleep.
	g.r.opts.Now = func() time.Time { g.now = g.now.Add(addonReadyTimeout); return g.now }
	g.desire("running", false)
	g.pass()
	b := g.wantState("failed")
	if !strings.Contains(deref(b.LastError), "did not become ready") {
		t.Fatalf("error %q", deref(b.LastError))
	}
	for _, s := range g.fd.created {
		if s.Role == RoleRuntime {
			t.Fatal("bot created although its add-on was not ready")
		}
	}
}

func TestCustomBuildCommandAlwaysBuilds(t *testing.T) {
	g := newRig(t)
	if _, err := g.db.ExecContext(context.Background(), `UPDATE bots SET build_command = 'cd sub && go build -o /workspace/app .' WHERE id = ?`, botID); err != nil {
		t.Fatal(err)
	}
	g.desire("running", false)
	g.pass()
	var builder *ContainerSpec
	for i, s := range g.fd.created {
		if s.Role == RoleBuilder {
			builder = &g.fd.created[i]
		}
	}
	if builder == nil {
		t.Fatal("no build ran (nodejs only builds with package.json, a custom command always does)")
	}
	if len(builder.Argv) != 3 || builder.Argv[0] != "sh" || !strings.HasSuffix(builder.Argv[2], "cd sub && go build -o /workspace/app .") ||
		!strings.HasPrefix(builder.Argv[2], "set -e") {
		t.Fatalf("argv %q", builder.Argv)
	}
	if strings.Contains(strings.Join(builder.Env, " "), "s3cret") {
		t.Fatal("builder received a bot secret")
	}
}

func TestExpandRefs(t *testing.T) {
	got := expandRefs(map[string]string{
		"A": "${POSTGRES_PASSWORD}", "B": "x-${OTHER}-y", "C": "$NOT_BRACED ${MISSING}", "OTHER": "o", "SELF": "${SELF}", "LOOP": "${C}",
	}, map[string]string{"POSTGRES_PASSWORD": "pw"})
	want := map[string]string{"A": "pw", "B": "x-o-y", "C": "$NOT_BRACED ${MISSING}", "OTHER": "o", "SELF": "${SELF}", "LOOP": "${C}"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
