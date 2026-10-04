package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/pkgmgr"
)

func TestPackageManagerAPI(t *testing.T) {
	e := newEnv(t)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"objects":[{"package":{"name":"discord.js","version":"14.16.3","description":"lib"}}]}`))
	}))
	defer reg.Close()
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Files: fm,
		Registry: &pkgmgr.Registry{NPM: reg.URL}, SecureCookies: true})

	owner := e.user("own@x.io", domain.RoleUser)
	editor := e.user("ed@x.io", domain.RoleUser)
	viewer := e.user("vw@x.io", domain.RoleUser)
	id := owner.createBot("b") // runtime nodejs
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "ed@x.io", "permissions": domain.PermEditFiles})
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "vw@x.io", "permissions": domain.PermViewConsole})

	type resp struct {
		Supported bool
		File      string
		Exists    bool
		Deps      []pkgmgr.Dependency
	}
	var r resp
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/packages", nil), &r)
	if !r.Supported || r.File != "package.json" || r.Exists || len(r.Deps) != 0 {
		t.Fatalf("empty project: %+v", r)
	}
	viewer.mustStatus(403, "GET", base+"/packages", nil)

	// Adding to a project with no manifest creates a minimal package.json.
	json.Unmarshal(editor.mustStatus(200, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{
		{"action": "add", "name": "discord.js", "spec": "^14.0.0"}}}), &r)
	if !r.Exists || len(r.Deps) != 1 {
		t.Fatalf("%+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(e.dataDir, id, "package.json"))
	if !strings.Contains(string(b), `"discord.js": "^14.0.0"`) || !strings.Contains(string(b), `"private": true`) {
		t.Fatal(string(b))
	}

	// Client mistakes are 400s with a reason, not 500s; nothing is written.
	for _, bad := range []map[string]string{
		{"action": "add", "name": "discord.js", "spec": "1"},
		{"action": "add", "name": "x", "spec": "git+https://evil/x.git"},
		{"action": "remove", "name": "absent"},
	} {
		owner.mustStatus(400, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{bad}})
	}
	after, _ := os.ReadFile(filepath.Join(e.dataDir, id, "package.json"))
	if string(after) != string(b) {
		t.Fatal("a rejected edit changed the file")
	}

	// A broken manifest is reported, not overwritten.
	os.WriteFile(filepath.Join(e.dataDir, id, "package.json"), []byte("{oops"), 0o644)
	body := owner.mustStatus(400, "GET", base+"/packages", nil)
	if !strings.Contains(string(body), "package.json") {
		t.Fatal(string(body))
	}
	owner.mustStatus(400, "PUT", base+"/packages", map[string]any{"ops": []map[string]string{{"action": "add", "name": "x", "spec": "1"}}})
	if got, _ := os.ReadFile(filepath.Join(e.dataDir, id, "package.json")); string(got) != "{oops" {
		t.Fatal("broken manifest was overwritten")
	}

	var s struct{ Results []pkgmgr.Result }
	json.Unmarshal(editor.mustStatus(200, "GET", base+"/packages/search?q=discord", nil), &s)
	if len(s.Results) != 1 || s.Results[0].Name != "discord.js" {
		t.Fatal(s)
	}
	editor.mustStatus(400, "GET", base+"/packages/search?q=", nil)
	e.user("no@x.io", domain.RoleUser).mustStatus(404, "GET", base+"/packages/search?q=x", nil)
}
