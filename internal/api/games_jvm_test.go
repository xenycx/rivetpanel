package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/blueprints"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

type jvmOut struct {
	Supported      bool                  `json:"supported"`
	Args           string                `json:"args"`
	Default        string                `json:"default"`
	Java           int                   `json:"java"`
	HeapMiB        int64                 `json:"heap_mib"`
	PendingRestart bool                  `json:"pending_restart"`
	Presets        []blueprint.JVMPreset `json:"presets"`
}

// A server created before the JVM-arguments revision gets the setting (and
// Velocity's former G1 option as its value) through "Update"; the options are
// validated, audited and need the settings permission.
func TestGameJVMArgsAPI(t *testing.T) {
	e := newEnv(t)
	// Revision 1 of Velocity as shipped before JVM arguments existed.
	cur, err := blueprints.FS.ReadFile("minecraft-velocity.yaml")
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(cur), "{{SERVER_JVM_ARGS}} -jar", "-XX:+UseG1GC -jar", 1)
	old = strings.Replace(old, "  jvm_args: true\n  jvm_args_default: -XX:+UseG1GC\n", "", 1)
	spec, err := blueprint.Parse([]byte(old))
	if err != nil || spec.Startup.JVMArgs {
		t.Fatalf("old revision: %v", err)
	}
	if _, _, err := e.db.SaveBlueprintRevision(t.Context(), domain.Blueprint{Slug: spec.Slug, Name: spec.Name, Category: spec.Category,
		Description: spec.Description, Source: "builtin"}, old, blueprint.Hash([]byte(old)), 1); err != nil {
		t.Fatal(err)
	}
	g := withGames(t, e) // syncs the built-ins: Velocity gains revision 2
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, SecureCookies: true,
		Nodes: e.db, Games: g, Audit: &service.Audit{Store: e.db, Bots: e.bots}})
	u := e.user("proxy@example.com", domain.RoleUser)

	// Pin a server to revision 1 the way it was created before the update.
	var srv gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Proxy", "blueprint": "minecraft-velocity", "memory_bytes": 1 << 30}), &srv)
	bp, _ := e.db.GetBlueprint(t.Context(), "minecraft-velocity")
	if bp.CurrentRevision != 2 {
		t.Fatalf("revision %d", bp.CurrentRevision)
	}
	b, _ := e.db.GetBot(t.Context(), srv.ID)
	if b.JVMArgs != "-XX:+UseG1GC" {
		t.Fatalf("new server default %q", b.JVMArgs)
	}
	b.BlueprintRevision = 1
	if err := e.db.UpdateGameServer(t.Context(), b, 2); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SetJVMArgs(t.Context(), b.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	type detailOut struct {
		Startup         string `json:"startup"`
		UpdateAvailable bool   `json:"update_available"`
		JVM             jvmOut `json:"jvm"`
	}
	var d detailOut
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+srv.ID+"/game", nil), &d)
	if d.JVM.Supported || !d.UpdateAvailable || len(d.JVM.Presets) != 0 {
		t.Fatalf("old revision detail %+v", d)
	}
	if resp, body := u.do("PUT", "/api/v1/bots/"+srv.ID+"/game/jvm-args", map[string]string{"args": "-XX:+UseG1GC"}); resp.StatusCode != 400 || !strings.Contains(string(body), "update it") {
		t.Fatalf("old revision accepted JVM arguments: %d %s", resp.StatusCode, body)
	}

	// Update: the new command and Velocity's former G1 option as the value.
	var up gameOut
	json.Unmarshal(u.mustStatus(200, "POST", "/api/v1/bots/"+srv.ID+"/game/upgrade", nil), &up)
	if !strings.Contains(up.Argv[0], "-Xmx${SERVER_MEMORY}M ${SERVER_JVM_ARGS} -jar") {
		t.Fatalf("argv after update %q", up.Argv)
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+srv.ID+"/game", nil), &d)
	if !d.JVM.Supported || d.JVM.Args != "-XX:+UseG1GC" || d.JVM.Default != "-XX:+UseG1GC" || d.JVM.Java != 25 ||
		d.JVM.HeapMiB != 819 || len(d.JVM.Presets) != 3 || d.JVM.PendingRestart {
		t.Fatalf("detail after update %+v", d.JVM)
	}

	// Strict validation with readable errors.
	for in, want := range map[string]string{
		"-Xmx4G":              "heap size is set by the panel",
		"-Da=b; rm -rf /":     "may only use",
		"-Da=$(id)":           "may only use",
		"-jar other.jar":      "cannot be changed",
		"-XX:OnError=sh":      "runs a command",
		"--evil":              "not a JVM option",
		"-javaagent:../a.jar": "inside the server's files",
	} {
		resp, body := u.do("PUT", "/api/v1/bots/"+srv.ID+"/game/jvm-args", map[string]string{"args": in})
		if resp.StatusCode != 400 || !strings.Contains(string(body), want) {
			t.Errorf("%q: %d %s", in, resp.StatusCode, body)
		}
	}
	aikar := d.JVM.Presets[0].Args
	var saved struct {
		JVM jvmOut `json:"jvm"`
	}
	json.Unmarshal(u.mustStatus(200, "PUT", "/api/v1/bots/"+srv.ID+"/game/jvm-args", map[string]string{"args": "  " + strings.ReplaceAll(aikar, " ", "\t ") + " "}), &saved)
	if saved.JVM.Args != aikar {
		t.Fatalf("saved %q", saved.JVM.Args)
	}
	b, _ = e.db.GetBot(t.Context(), srv.ID)
	if b.JVMArgs != aikar || b.Generation != up.Generation {
		t.Fatalf("stored %q (generation %d → %d)", b.JVMArgs, up.Generation, b.Generation)
	}

	// A server running its current container shows "Restart to apply"; a
	// newer generation (start or restart) clears it.
	if _, err := e.db.Observe(t.Context(), sqlite.Observation{BotID: b.ID, Generation: b.Generation, State: "running", SettleGeneration: true, NowMS: 5}); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+srv.ID+"/game", nil), &d)
	if !d.JVM.PendingRestart {
		t.Fatal("no restart hint for options saved while running")
	}

	// Permission: another account cannot read or change them.
	other := e.user("someone@example.com", domain.RoleUser)
	if resp, _ := other.do("PUT", "/api/v1/bots/"+srv.ID+"/game/jvm-args", map[string]string{"args": ""}); resp.StatusCode != 404 {
		t.Fatalf("cross-account write: %d", resp.StatusCode)
	}

	// Audited without the value.
	events, err := e.db.ListAudit(t.Context(), sqlite.AuditFilter{BotID: srv.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, ev := range events {
		if ev.Action == "game.jvm_args" && ev.Target != nil {
			targets = append(targets, ev.Outcome+" "+*ev.Target)
		}
	}
	if !slices.Contains(targets, "ok preset: Aikar's flags") {
		t.Fatalf("audit %v", targets)
	}
}
