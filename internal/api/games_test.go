package api

import (
	"context"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/blueprints"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/modrinth"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// withGames rebuilds the env's app with game servers enabled. Ports are
// always free so the tests do not depend on the host.
func withGames(t *testing.T, e *env) *service.GameService {
	t.Helper()
	g := &service.GameService{Bots: e.bots, Store: e.db, Providers: &blueprint.Providers{}, Builtins: blueprints.FS,
		LocalPortFree: func(int) bool { return true }}
	if err := g.SyncBuiltins(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, SecureCookies: true,
		Nodes: e.db, Games: g})
	return g
}

type gameOut struct {
	ID           string     `json:"id"`
	NodeID       string     `json:"node_id"`
	Kind         string     `json:"kind"`
	InstallState string     `json:"install_state"`
	ImageChoice  string     `json:"image_choice"`
	Generation   int64      `json:"generation"`
	Allocations  []allocDTO `json:"allocations"`
	Argv         []string   `json:"argv"`
	Entrypoint   []string   `json:"entrypoint"`
}

func TestGameServerLifecycleAPI(t *testing.T) {
	e := newEnv(t)
	withGames(t, e)
	u := e.user("player@example.com", domain.RoleUser)

	var list struct {
		Blueprints []blueprintDTO `json:"blueprints"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/blueprints", nil), &list)
	if len(list.Blueprints) < 8 {
		t.Fatalf("blueprints = %d", len(list.Blueprints))
	}
	for _, b := range list.Blueprints {
		if b.Slug == "minecraft-forge" && !strings.Contains(b.Spec.Install.Script, "(script)") {
			t.Fatal("install scripts are summarized in views")
		}
	}

	create := map[string]any{"name": "Survival", "blueprint": "minecraft-paper", "memory_bytes": 1 << 30,
		"variables": map[string]string{"MINECRAFT_VERSION": "1.21.11"}}
	if resp, body := u.do("POST", "/api/v1/games", create); resp.StatusCode != 400 || !strings.Contains(string(body), "End User License") {
		t.Fatalf("created without the EULA: %d %s", resp.StatusCode, body)
	}
	create["variables"] = map[string]string{"MINECRAFT_VERSION": "1.21.11; rm -rf /"}
	create["agreements"] = []string{"minecraft-eula"}
	if resp, _ := u.do("POST", "/api/v1/games", create); resp.StatusCode != 400 {
		t.Fatalf("invalid version accepted: %d", resp.StatusCode)
	}
	create["variables"] = map[string]string{"MINECRAFT_VERSION": "1.21.11"}
	var g gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", create), &g)
	if g.Kind != "game" || g.InstallState != "pending" || len(g.Allocations) != 1 || !g.Allocations[0].Primary || g.Allocations[0].Port != 25565 {
		t.Fatalf("created %+v", g)
	}
	if strings.Join(g.Entrypoint, " ") != "/bin/sh -c" || !strings.Contains(g.Argv[0], "${SERVER_MEMORY}") {
		t.Fatalf("startup %v %v", g.Entrypoint, g.Argv)
	}
	// The accepted EULA is recorded as a hidden variable and cannot be removed.
	if resp, _ := u.do("DELETE", "/api/v1/bots/"+g.ID+"/env/RIVET_AGREEMENT_MINECRAFT_EULA", nil); resp.StatusCode == 204 {
		t.Fatal("agreement record removed")
	}

	var detail struct {
		Variables []gameVariableDTO `json:"variables"`
		Startup   string            `json:"startup"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots/"+g.ID+"/game", nil), &detail)
	if len(detail.Variables) != 3 || detail.Variables[0].Env != "MINECRAFT_VERSION" || detail.Variables[0].Value != "1.21.11" {
		t.Fatalf("variables %+v", detail.Variables)
	}

	// The list carries each server's allocations, for its join address.
	var listed struct {
		Bots []gameOut `json:"bots"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/bots", nil), &listed)
	if len(listed.Bots) != 1 || len(listed.Bots[0].Allocations) != 1 || listed.Bots[0].Allocations[0].Port != 25565 {
		t.Fatalf("listed %+v", listed.Bots)
	}

	// Join addresses: any account reads the nodes' public addresses only.
	var hosts struct {
		Hosts map[string]string `json:"hosts"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/game-hosts", nil), &hosts)
	if len(hosts.Hosts) != 0 {
		t.Fatalf("hosts without public addresses: %v", hosts.Hosts)
	}
	addr := "play.example.com"
	if err := e.db.UpdateNode(t.Context(), g.NodeID, sqlite.NodeUpdate{PublicAddress: &addr}, 1); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/game-hosts", nil), &hosts)
	if hosts.Hosts[g.NodeID] != addr || len(hosts.Hosts) != 1 {
		t.Fatalf("hosts %v (node %s)", hosts.Hosts, g.NodeID)
	}

	// A second server gets the next port.
	var g2 gameOut
	create["name"] = "Creative"
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", create), &g2)
	if g2.Allocations[0].Port != 25566 {
		t.Fatalf("second server port %d", g2.Allocations[0].Port)
	}

	// Settings: rules apply; a version change marks the server for reinstall.
	e.db.SetInstallState(t.Context(), g.ID, domain.InstallInstalled, nil, 1)
	if resp, _ := u.do("PUT", "/api/v1/bots/"+g.ID+"/game/variables", map[string]any{"variables": map[string]string{"SERVER_JARFILE": "../evil"}}); resp.StatusCode != 400 {
		t.Fatalf("invalid jar name accepted: %d", resp.StatusCode)
	}
	if resp, _ := u.do("PUT", "/api/v1/bots/"+g.ID+"/game/variables", map[string]any{"variables": map[string]string{"NOT_DECLARED": "x"}}); resp.StatusCode != 400 {
		t.Fatalf("undeclared setting accepted: %d", resp.StatusCode)
	}
	var after gameOut
	json.Unmarshal(u.mustStatus(200, "PUT", "/api/v1/bots/"+g.ID+"/game/variables", map[string]any{"variables": map[string]string{"MINECRAFT_VERSION": "1.21.10"}}), &after)
	if after.InstallState != "pending" {
		t.Fatalf("version change did not schedule a reinstall: %+v", after)
	}

	// Allocations: add, refuse removing the primary, remove the extra.
	var allocs struct {
		Allocations []allocDTO `json:"allocations"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/bots/"+g.ID+"/allocations", nil), &allocs)
	if len(allocs.Allocations) != 2 {
		t.Fatalf("allocations %+v", allocs)
	}
	var primary, extra string
	for _, a := range allocs.Allocations {
		if a.Primary {
			primary = a.ID
		} else {
			extra = a.ID
		}
	}
	if resp, _ := u.do("DELETE", "/api/v1/bots/"+g.ID+"/allocations/"+primary, nil); resp.StatusCode != 400 {
		t.Fatalf("primary removed: %d", resp.StatusCode)
	}
	u.mustStatus(204, "PUT", "/api/v1/bots/"+g.ID+"/allocations/"+extra+"/primary", nil)
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+g.ID+"/allocations/"+primary, nil)

	// Startup cannot be edited through the generic update.
	if resp, _ := u.do("PATCH", "/api/v1/bots/"+g.ID, map[string]any{"argv": []string{"sh"}}); resp.StatusCode != 400 {
		t.Fatalf("startup edited directly: %d", resp.StatusCode)
	}
	// Reinstall and image choice need a stopped server and valid labels.
	u.mustStatus(200, "POST", "/api/v1/bots/"+g.ID+"/game/reinstall", nil)
	if resp, _ := u.do("PUT", "/api/v1/bots/"+g.ID+"/game/image", map[string]string{"image": "Java 99"}); resp.StatusCode != 400 {
		t.Fatalf("unknown image accepted: %d", resp.StatusCode)
	}
	var img gameOut
	json.Unmarshal(u.mustStatus(200, "PUT", "/api/v1/bots/"+g.ID+"/game/image", map[string]string{"image": "Java 21"}), &img)
	if img.ImageChoice != "Java 21" {
		t.Fatalf("image %+v", img)
	}

	// Another account sees nothing.
	other := e.user("other@example.com", domain.RoleUser)
	if resp, _ := other.do("GET", "/api/v1/bots/"+g.ID+"/game", nil); resp.StatusCode != 404 {
		t.Fatalf("cross-account read: %d", resp.StatusCode)
	}
	if resp, _ := other.do("PUT", "/api/v1/bots/"+g.ID+"/game/variables", map[string]any{"variables": map[string]string{"MINECRAFT_VERSION": "1.20.1"}}); resp.StatusCode != 404 {
		t.Fatalf("cross-account write: %d", resp.StatusCode)
	}
	// Deleting the server returns its allocations to the pool.
	u.mustStatus(204, "DELETE", "/api/v1/bots/"+g2.ID, nil)
	pool, _ := e.db.ListAllocations(t.Context(), "")
	for _, a := range pool {
		if a.Port == 25566 && (a.BotID != nil || a.Primary) {
			t.Fatalf("allocation not released: %+v", a)
		}
	}
}

func TestBlueprintAdministration(t *testing.T) {
	e := newEnv(t)
	withGames(t, e)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	custom := `slug: custom-game
name: Custom
category: Tests
runtime: java
images: [{label: "Java 21", ref: "eclipse-temurin:21-jre-alpine", java: 21}]
startup: {command: "java -jar server.jar"}
install: {script: "echo installing"}
resources: {memory_mb: 512, cpus: 1}
`
	if resp, _ := user.do("POST", "/api/v1/admin/blueprints", map[string]string{"yaml": custom}); resp.StatusCode != 403 {
		t.Fatalf("user imported a blueprint: %d", resp.StatusCode)
	}
	if resp, _ := admin.do("POST", "/api/v1/admin/blueprints", map[string]string{"yaml": strings.Replace(custom, "slug: custom-game", "slug: minecraft-paper", 1)}); resp.StatusCode != 400 {
		t.Fatalf("custom upload took over a built-in slug: %d", resp.StatusCode)
	}
	var imp struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/blueprints", map[string]string{"yaml": custom}), &imp)
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/blueprints", map[string]string{"yaml": strings.Replace(custom, "Custom", "Custom v2", 1)}), &imp)
	if imp.Revision != 2 {
		t.Fatalf("revision %d", imp.Revision)
	}
	resp, body := admin.do("GET", "/api/v1/admin/blueprints/custom-game/export?rev=1", nil)
	if resp.StatusCode != 200 || string(body) != custom {
		t.Fatalf("export: %d %q", resp.StatusCode, body)
	}
	admin.mustStatus(204, "PATCH", "/api/v1/admin/blueprints/"+imp.ID, map[string]bool{"enabled": false})
	if resp, _ := user.do("GET", "/api/v1/blueprints/custom-game", nil); resp.StatusCode != 404 {
		t.Fatalf("disabled blueprint visible to users: %d", resp.StatusCode)
	}
	if resp, _ := admin.do("DELETE", "/api/v1/admin/blueprints/minecraft-paper", nil); resp.StatusCode == 204 {
		t.Fatal("built-in blueprint deleted")
	}
	admin.mustStatus(204, "DELETE", "/api/v1/admin/blueprints/"+imp.ID, nil)

	// Allocation pool administration.
	var created struct {
		Allocations []allocDTO `json:"allocations"`
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/allocations", map[string]string{"ip": "0.0.0.0", "ports": "30000-30002,30005"}), &created)
	if len(created.Allocations) != 4 {
		t.Fatalf("allocations %+v", created)
	}
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/allocations", map[string]string{"ports": "30000-30001"}), &created)
	if len(created.Allocations) != 0 {
		t.Fatalf("duplicate ports created: %+v", created)
	}
	if resp, _ := admin.do("POST", "/api/v1/admin/allocations", map[string]string{"ports": "80"}); resp.StatusCode != 400 {
		t.Fatal("privileged port accepted")
	}
	// A new server takes from the pool before creating ports automatically.
	var g gameOut
	json.Unmarshal(user.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Pool", "blueprint": "minecraft-vanilla",
		"agreements": []string{"minecraft-eula"}}), &g)
	if g.Allocations[0].Port < 30000 || g.Allocations[0].Port > 30005 {
		t.Fatalf("pool not used: %+v", g.Allocations)
	}
	if resp, _ := admin.do("DELETE", "/api/v1/admin/allocations/"+g.Allocations[0].ID, nil); resp.StatusCode != 400 {
		t.Fatalf("assigned allocation deleted: %d", resp.StatusCode)
	}
}

func TestGameAddonInstall(t *testing.T) {
	e := newEnv(t)
	g := withGames(t, e)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	g.Files = fm
	jar := []byte("PK plugin")
	sum := sha512.Sum512(jar)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/version/Ver00001", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"Ver00001","project_id":"AbCd1234","loaders":["paper"],"game_versions":["1.21.11"],
			"files":[{"url":"%s/p.jar","filename":"Plugin-1.0.jar","primary":true,"size":%d,"hashes":{"sha512":"%x"}}],
			"dependencies":[{"project_id":"Dep00001","dependency_type":"required"}]}`, srv.URL, len(jar), sum)
	})
	mux.HandleFunc("/v2/version/Fab00001", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"Fab00001","project_id":"AbCd1234","loaders":["fabric"],"files":[{"url":"%s/p.jar","filename":"m.jar","primary":true,"hashes":{"sha512":"%x"}}]}`, srv.URL, sum)
	})
	mux.HandleFunc("/p.jar", func(w http.ResponseWriter, r *http.Request) { w.Write(jar) })
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	g.Modrinth = &modrinth.Client{HTTP: srv.Client(), Base: srv.URL, AllowedHosts: []string{host}}

	u := e.user("p@example.com", domain.RoleUser)
	var srvOut gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Plugins", "blueprint": "minecraft-paper",
		"memory_bytes": 1 << 30, "agreements": []string{"minecraft-eula"}}), &srvOut)
	base := "/api/v1/bots/" + srvOut.ID
	if resp, body := u.do("POST", base+"/game/addons", map[string]string{"project": "AbCd1234", "version": "Fab00001"}); resp.StatusCode != 400 || !strings.Contains(string(body), "loader") {
		t.Fatalf("a Fabric mod installed into Paper: %d %s", resp.StatusCode, body)
	}
	var res struct {
		Path     string   `json:"path"`
		Required []string `json:"required"`
	}
	json.Unmarshal(u.mustStatus(201, "POST", base+"/game/addons", map[string]string{"project": "AbCd1234", "version": "Ver00001"}), &res)
	if res.Path != "plugins/Plugin-1.0.jar" || len(res.Required) != 1 {
		t.Fatalf("install %+v", res)
	}
	got, err := fm.ReadFile(srvOut.ID, "plugins/Plugin-1.0.jar", 1<<20)
	if err != nil || string(got) != string(jar) {
		t.Fatalf("plugin file %q %v", got, err)
	}
	other := e.user("o@example.com", domain.RoleUser)
	if resp, _ := other.do("POST", base+"/game/addons", map[string]string{"project": "AbCd1234", "version": "Ver00001"}); resp.StatusCode != 404 {
		t.Fatalf("cross-account install: %d", resp.StatusCode)
	}
}

// Ports published by any running container on the host (another panel, any
// other tool) are never handed out, pool ports included; a start onto one is
// refused with the holder's name, and the server can move to a free port.
func TestDockerPublishedPortsAreSkipped(t *testing.T) {
	e := newEnv(t)
	withGames(t, e)
	var mu sync.Mutex
	pubs := []runner.PublishedPort{{HostIP: "0.0.0.0", HostPort: 25577, Proto: "tcp", Container: "other-panel-runtime"}}
	e.bots.LocalPublished = func(context.Context) ([]runner.PublishedPort, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]runner.PublishedPort(nil), pubs...), nil
	}
	publish := func(port int, name string) {
		mu.Lock()
		pubs = append(pubs, runner.PublishedPort{HostIP: "0.0.0.0", HostPort: port, Proto: "tcp", Container: name})
		mu.Unlock()
	}
	u := e.user("proxy@example.com", domain.RoleUser)
	admin := e.user("ports-admin@example.com", domain.RoleAdmin)
	create := map[string]any{"name": "Proxy", "blueprint": "minecraft-velocity", "memory_bytes": 512 << 20}
	var g gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", create), &g)
	if g.Allocations[0].Port != 25578 {
		t.Fatalf("got port %d, want 25578 (25577 is published by another container)", g.Allocations[0].Port)
	}

	// Administrators adding pool ports get busy ones skipped and named.
	var added struct {
		Allocations []allocDTO `json:"allocations"`
		Skipped     []struct {
			Port   int    `json:"port"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	publish(25580, "someone-else")
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/admin/allocations", map[string]any{"ports": "25579-25581"}), &added)
	if len(added.Allocations) != 2 || len(added.Skipped) != 1 || added.Skipped[0].Port != 25580 || !strings.Contains(added.Skipped[0].Reason, "someone-else") {
		t.Fatalf("admin add: %+v", added)
	}
	if resp, body := admin.do("POST", "/api/v1/admin/allocations", map[string]any{"ports": "25577"}); resp.StatusCode != 400 || !strings.Contains(string(body), "other-panel-runtime") {
		t.Fatalf("busy single port: %d %s", resp.StatusCode, body)
	}

	// A pool port taken on the host since it was added is skipped too.
	publish(25579, "late-comer")
	var g2 gameOut
	create["name"] = "Proxy 2"
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", create), &g2)
	if p := g2.Allocations[0].Port; p != 25581 {
		t.Fatalf("second server got %d, want the free pool port 25581", p)
	}

	// The host takes the first server's port: Start is refused. The holder
	// can be another tenant's container, so only an administrator sees its
	// name.
	publish(25578, "rivetpanel-other-runtime")
	base := "/api/v1/bots/" + g.ID
	if resp, body := u.do("POST", base+"/start", nil); resp.StatusCode != 400 || !strings.Contains(string(body), "Port 25578 is already used by another container") ||
		strings.Contains(string(body), "rivetpanel-other-runtime") {
		t.Fatalf("start onto a busy port: %d %s", resp.StatusCode, body)
	}
	if resp, body := admin.do("POST", base+"/start", nil); resp.StatusCode != 400 || !strings.Contains(string(body), "Port 25578 is already used by container rivetpanel-other-runtime") {
		t.Fatalf("admin start onto a busy port: %d %s", resp.StatusCode, body)
	}
	// One click moves it to a free port and returns the old one to the pool.
	var moved gameOut
	json.Unmarshal(u.mustStatus(200, "POST", base+"/allocations/pick-free", nil), &moved)
	if len(moved.Allocations) != 1 || !moved.Allocations[0].Primary || moved.Allocations[0].Port != 25582 || moved.Generation <= g.Generation {
		t.Fatalf("after pick-free: %+v", moved)
	}
	u.mustStatus(202, "POST", base+"/start", nil)
	// The runner then hits a port taken meanwhile: one click moves the
	// waiting server without stopping it first, and it starts again.
	b, _ := e.db.GetBot(t.Context(), g.ID)
	e.db.Observe(t.Context(), sqlite.Observation{BotID: g.ID, Generation: b.Generation, State: "failed", SettleGeneration: true,
		Reason: domain.ReasonPortConflict, LastError: "Port 25582 is already used by container late. Choose another port in Network or stop that container.", NowMS: 1})
	publish(25582, "late")
	json.Unmarshal(u.mustStatus(200, "POST", base+"/allocations/pick-free", nil), &moved)
	if after, _ := e.db.GetBot(t.Context(), g.ID); after.DesiredState != domain.DesiredRunning || after.Generation <= b.Generation ||
		moved.Allocations[0].Port == 25582 || moved.Allocations[0].Port == 25580 {
		t.Fatalf("pick-free while blocked: %+v desired=%s", moved.Allocations, after.DesiredState)
	}
}
