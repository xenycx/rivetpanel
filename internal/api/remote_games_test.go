package api

import (
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/modrinth"
)

// TestRemoteGameAddonInstall installs Modrinth plugins on a server that runs
// on an agent node: the panel verifies the whole download (host, size,
// SHA-512) before anything is sent, the node receives only verified files,
// the panel's own disk is never written, an offline node is refused before
// downloading, and the temporary spool is always removed.
func TestRemoteGameAddonInstall(t *testing.T) {
	e := newEnv(t)
	g := withGames(t, e)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	g.Files = fm
	spool := t.TempDir()
	g.SpoolDir = spool
	jar := []byte("PK remote plugin")
	sum := sha512.Sum512(jar)
	var downloads atomic.Int32
	var srv *httptest.Server
	mux := http.NewServeMux()
	version := func(id, file, hash string, size int) {
		mux.HandleFunc("/v2/version/"+id, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"id":%q,"project_id":"AbCd1234","loaders":["paper"],"files":[{"url":"%s/p.jar","filename":%q,"primary":true,"size":%d,"hashes":{"sha512":%q}}]}`,
				id, srv.URL, file, size, hash)
		})
	}
	version("Ver00001", "Plugin-1.0.jar", fmt.Sprintf("%x", sum), len(jar))
	version("Bad00001", "Bad-1.0.jar", fmt.Sprintf("%x", sha512.Sum512([]byte("something else"))), len(jar))
	version("Big00001", "Big-1.0.jar", fmt.Sprintf("%x", sum), modrinth.MaxFileBytes+1)
	mux.HandleFunc("/v2/version/Far00001", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"Far00001","project_id":"AbCd1234","loaders":["paper"],"files":[{"url":"https://evil.example/p.jar","filename":"Far.jar","primary":true,"hashes":{"sha512":"%x"}}]}`, sum)
	})
	mux.HandleFunc("/p.jar", func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); w.Write(jar) })
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	g.Modrinth = &modrinth.Client{HTTP: srv.Client(), Base: srv.URL, AllowedHosts: []string{host}}

	u := e.user("rp@example.com", domain.RoleUser)
	var out gameOut
	json.Unmarshal(u.mustStatus(201, "POST", "/api/v1/games", map[string]any{"name": "Remote plugins", "blueprint": "minecraft-paper",
		"memory_bytes": 1 << 30, "agreements": []string{"minecraft-eula"}}), &out)
	base := "/api/v1/bots/" + out.ID
	node := newFakeNodeFiles(t)
	placeOnRemoteNode(t, e, out.ID, node)
	panelDecoy(t, fm, out.ID, "plugins/Plugin-1.0.jar")
	install := func(status int, ver, want string) {
		t.Helper()
		resp, body := u.do("POST", base+"/game/addons", map[string]string{"project": "AbCd1234", "version": ver})
		if resp.StatusCode != status || !strings.Contains(string(body), want) {
			t.Fatalf("install %s: %d %s", ver, resp.StatusCode, body)
		}
	}

	// Offline: refused before anything is downloaded.
	node.online = false
	install(400, "Ver00001", "offline")
	node.online = true
	if downloads.Load() != 0 {
		t.Fatal("an offline node's install downloaded the file")
	}

	// Verified and sent to the node; the panel's copy is untouched.
	install(201, "Ver00001", "plugins/Plugin-1.0.jar")
	if got, ok := node.read(t, out.ID, "plugins/Plugin-1.0.jar"); !ok || got != string(jar) {
		t.Fatalf("node plugin = %q %v", got, ok)
	}
	if got := panelFile(t, fm, out.ID, "plugins/Plugin-1.0.jar"); got != "PANEL-LOCAL" {
		t.Fatalf("the panel's disk was written: %q", got)
	}

	// A checksum mismatch, an oversized file and a host outside the CDN
	// allowlist never reach the node.
	node.takeOps()
	install(400, "Bad00001", "checksum")
	install(400, "Big00001", "nothing was installed")
	install(400, "Far00001", "nothing was installed")
	if ops := node.takeOps(); len(ops) != 0 {
		t.Fatalf("unverified files reached the node: %v", ops)
	}
	for _, p := range []string{"plugins/Bad-1.0.jar", "plugins/Big-1.0.jar", "plugins/Far.jar"} {
		if _, ok := node.read(t, out.ID, p); ok {
			t.Fatalf("%s was installed", p)
		}
	}
	if ents, _ := os.ReadDir(spool); len(ents) != 0 {
		t.Fatalf("spool not cleaned: %d files", len(ents))
	}

	// Without file permission nothing is downloaded or written.
	other := e.user("ro@example.com", domain.RoleUser)
	if resp, _ := other.do("POST", base+"/game/addons", map[string]string{"project": "AbCd1234", "version": "Ver00001"}); resp.StatusCode != 404 {
		t.Fatalf("cross-account install: %d", resp.StatusCode)
	}
}
