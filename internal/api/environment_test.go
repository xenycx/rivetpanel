package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type envVarJSON struct {
	Name     string `json:"name"`
	Editable bool   `json:"editable"`
	Secret   bool   `json:"secret"`
	Source   string `json:"source"`
	Value    string `json:"value"`
	Set      bool   `json:"set"`
	EnvValue string `json:"env_value"`
	EnvSet   bool   `json:"env_set"`
	Override bool   `json:"override"`
	Pending  bool   `json:"pending"`
	Running  string `json:"running"`
	Managed  string `json:"managed"`
}

type envViewJSON struct {
	Vars       []envVarJSON `json:"vars"`
	Pending    int          `json:"pending"`
	CanRestart bool         `json:"can_restart"`
	Rejected   string       `json:"rejected"`
}

func (v envViewJSON) get(t *testing.T, name string) envVarJSON {
	t.Helper()
	for _, x := range v.Vars {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("%s missing from the environment view", name)
	return envVarJSON{}
}

func envEnv(t *testing.T, base map[string]string, canRestart bool) (*env, *service.PanelEnvService, *atomic.Int32) {
	t.Helper()
	e := newEnv(t)
	var restarts atomic.Int32
	svc := &service.PanelEnvService{Store: e.db, Keys: e.bots.Keys, Base: func(n string) (string, bool) { v, ok := base[n]; return v, ok },
		CanRestart: canRestart, Restart: func() { restarts.Add(1) }}
	svc.Started(nil, "", nil)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Env: svc, SecureCookies: true})
	return e, svc, &restarts
}

func readView(t *testing.T, raw []byte) envViewJSON {
	t.Helper()
	var v envViewJSON
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEnvironmentEditorOverridesAndPendingRestart(t *testing.T) {
	e, _, _ := envEnv(t, map[string]string{"RIVET_SFTP_LISTEN": "0.0.0.0:2022", "RIVET_MAX_BUILDS": "2"}, false)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	user := e.user("user@example.com", domain.RoleUser)
	user.mustStatus(403, "GET", "/api/v1/admin/environment", nil)
	user.mustStatus(403, "PUT", "/api/v1/admin/environment", map[string]any{"set": map[string]string{"RIVET_MAX_BUILDS": "3"}})

	v := readView(t, admin.mustStatus(200, "GET", "/api/v1/admin/environment", nil))
	if b := v.get(t, "RIVET_MAX_BUILDS"); b.Source != "environment" || b.Value != "2" || !b.Editable || b.Override || b.Pending {
		t.Fatalf("environment-provided variable: %+v", b)
	}
	if b := v.get(t, "RIVET_BACKUP_KEEP"); b.Source != "default" || b.EnvSet {
		t.Fatalf("default variable: %+v", b)
	}
	if b := v.get(t, "RIVET_LISTEN"); b.Editable {
		t.Fatalf("a boot variable must not be editable: %+v", b)
	}
	if b := v.get(t, "RIVET_PUBLIC_URL"); b.Editable || b.Managed == "" {
		t.Fatalf("a variable owned by Panel settings must point there: %+v", b)
	}

	// Override one value and switch off another the environment turns on.
	v = readView(t, admin.mustStatus(200, "PUT", "/api/v1/admin/environment", map[string]any{
		"set": map[string]string{"RIVET_MAX_BUILDS": "3", "RIVET_SFTP_LISTEN": ""}}))
	if b := v.get(t, "RIVET_MAX_BUILDS"); b.Source != "panel" || b.Value != "3" || b.EnvValue != "2" || !b.Override || !b.Pending || b.Running != "2" {
		t.Fatalf("overridden variable: %+v", b)
	}
	if b := v.get(t, "RIVET_SFTP_LISTEN"); b.Source != "panel" || b.Value != "" || b.EnvValue != "0.0.0.0:2022" || !b.Pending {
		t.Fatalf("an empty override must beat the environment: %+v", b)
	}
	if v.Pending != 2 {
		t.Fatalf("pending = %d", v.Pending)
	}

	// Resetting returns to the environment value, and nothing is pending.
	v = readView(t, admin.mustStatus(200, "PUT", "/api/v1/admin/environment", map[string]any{"unset": []string{"RIVET_MAX_BUILDS", "RIVET_SFTP_LISTEN"}}))
	if b := v.get(t, "RIVET_MAX_BUILDS"); b.Source != "environment" || b.Value != "2" || b.Override || b.Pending || v.Pending != 0 {
		t.Fatalf("after reset: %+v pending %d", b, v.Pending)
	}
}

func TestEnvironmentEditorRefusesBadChangesAndStoresNothing(t *testing.T) {
	e, _, _ := envEnv(t, nil, false)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	put := func(want int, set map[string]string) {
		t.Helper()
		admin.mustStatus(want, "PUT", "/api/v1/admin/environment", map[string]any{"set": set})
	}
	put(400, map[string]string{"RIVET_LISTEN": "0.0.0.0:80"})                      // boot variable
	put(400, map[string]string{"RIVET_PUBLIC_URL": "https://x.example.com"})       // owned by Panel settings
	put(400, map[string]string{"RIVET_NOT_A_THING": "1"})                          // unknown
	put(400, map[string]string{"RIVET_MAX_BUILDS": "many"})                        // not a number
	put(400, map[string]string{"RIVET_MAX_BUILDS": "99"})                          // fails validation
	put(400, map[string]string{"RIVET_PORT_PUBLIC_BIND": "yes"})                   // not 0 or 1
	put(400, map[string]string{"RIVET_NODE_MEMORY_BYTES": "1048576"})              // smaller than the largest bot
	put(400, map[string]string{"RIVET_METRICS_TOKEN": "too-short"})                // fails validation
	put(400, map[string]string{"RIVET_MAX_BUILDS": "2", "RIVET_BACKUP_KEEP": "0"}) // one bad value refuses the whole save
	admin.mustStatus(400, "PUT", "/api/v1/admin/environment", map[string]any{})
	var n int
	e.db.QueryRow(`SELECT count(*) FROM env_overrides`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d overrides stored by refused requests", n)
	}
}

func TestEnvironmentSecretsAreSealedAndNeverReturned(t *testing.T) {
	e, _, _ := envEnv(t, nil, false)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	const token = "m3trics-token-0123456789-abcdef"
	raw := admin.mustStatus(200, "PUT", "/api/v1/admin/environment", map[string]any{"set": map[string]string{"RIVET_METRICS_TOKEN": token}})
	if strings.Contains(string(raw), token) {
		t.Fatal("the response contains the secret")
	}
	if b := readView(t, raw).get(t, "RIVET_METRICS_TOKEN"); !b.Secret || !b.Set || b.Value != "" || b.Source != "panel" {
		t.Fatalf("secret in view: %+v", b)
	}
	if strings.Contains(string(admin.mustStatus(200, "GET", "/api/v1/admin/environment", nil)), token) {
		t.Fatal("a later read contains the secret")
	}
	var value *string
	var cipher []byte
	e.db.QueryRow(`SELECT value, secret_cipher FROM env_overrides WHERE name = 'RIVET_METRICS_TOKEN'`).Scan(&value, &cipher)
	if value != nil || len(cipher) == 0 || strings.Contains(string(cipher), token) {
		t.Fatalf("the secret must be stored sealed: value=%v cipher=%d bytes", value, len(cipher))
	}
	// And it is readable by the same keys at the next start.
	got, unreadable, err := (&service.PanelEnvService{Store: e.db, Keys: e.bots.Keys}).Overrides(t.Context())
	if err != nil || len(unreadable) != 0 || got["RIVET_METRICS_TOKEN"] != token {
		t.Fatalf("overrides at start: %v %v %v", got, unreadable, err)
	}
}

func TestEnvironmentRestartOnlyWhenSupervised(t *testing.T) {
	e, _, restarts := envEnv(t, nil, false)
	admin := e.user("admin@example.com", domain.RoleAdmin)
	admin.mustStatus(400, "POST", "/api/v1/admin/environment/restart", nil)
	time.Sleep(700 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("restarted although no supervisor was detected")
	}

	e, _, restarts = envEnv(t, nil, true)
	admin = e.user("admin@example.com", domain.RoleAdmin)
	e.user("user@example.com", domain.RoleUser).mustStatus(403, "POST", "/api/v1/admin/environment/restart", nil)
	admin.mustStatus(202, "POST", "/api/v1/admin/environment/restart", nil)
	deadline := time.Now().Add(3 * time.Second)
	for restarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Fatalf("restart calls = %d", restarts.Load())
	}
}
