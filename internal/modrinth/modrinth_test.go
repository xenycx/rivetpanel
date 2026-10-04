package modrinth

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// FakeServer serves the API subset used and one jar.
func FakeServer(t *testing.T, jar []byte) (*Client, *httptest.Server) {
	t.Helper()
	sum := sha512.Sum512(jar)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("facets"), "categories:paper") {
			http.Error(w, "missing loader facet", 400)
			return
		}
		fmt.Fprint(w, `{"hits":[{"project_id":"AbCd1234","slug":"luckperms","title":"LuckPerms","description":"Permissions","downloads":5}],"total_hits":1}`)
	})
	ver := func(w http.ResponseWriter, host string) {
		fmt.Fprintf(w, `{"id":"Ver00001","project_id":"AbCd1234","version_number":"5.4","loaders":["paper","bukkit"],"game_versions":["1.21.11"],
			"files":[{"url":"%s/data/x/LuckPerms-Bukkit-5.4.jar","filename":"LuckPerms-Bukkit-5.4.jar","primary":true,"size":%d,"hashes":{"sha512":"%s"}}],
			"dependencies":[{"project_id":"Dep00001","dependency_type":"required"}]}`, host, len(jar), hex.EncodeToString(sum[:]))
	}
	mux.HandleFunc("/v2/project/AbCd1234/version", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("["))
		ver(w, srv.URL)
		w.Write([]byte("]"))
	})
	mux.HandleFunc("/v2/version/Ver00001", func(w http.ResponseWriter, r *http.Request) { ver(w, srv.URL) })
	mux.HandleFunc("/data/x/LuckPerms-Bukkit-5.4.jar", func(w http.ResponseWriter, r *http.Request) { w.Write(jar) })
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &Client{HTTP: srv.Client(), Base: srv.URL, AllowedHosts: []string{u.Host}}, srv
}

func TestSearchVersionsAndVerifiedFetch(t *testing.T) {
	jar := []byte("PK luckperms")
	c, _ := FakeServer(t, jar)
	ctx := context.Background()
	hits, total, err := c.Search(ctx, "luck", []string{"paper", "bukkit"}, "1.21.11", 0)
	if err != nil || total != 1 || hits[0].Slug != "luckperms" {
		t.Fatalf("search %+v %v", hits, err)
	}
	vs, err := c.Versions(ctx, "AbCd1234", []string{"paper"}, "1.21.11")
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions %v %v", vs, err)
	}
	f, ok := vs[0].PrimaryFile()
	if !ok || !ValidFilename(f.Filename) {
		t.Fatalf("file %+v", f)
	}
	var buf bytes.Buffer
	if err := c.Fetch(ctx, f, &buf); err != nil || !bytes.Equal(buf.Bytes(), jar) {
		t.Fatalf("fetch: %v", err)
	}
	bad := f
	bad.Hashes = map[string]string{"sha512": strings.Repeat("0", 128)}
	if err := c.Fetch(ctx, bad, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered file accepted: %v", err)
	}
	other := f
	other.URL = "https://evil.example/x.jar"
	if err := c.Fetch(ctx, other, &bytes.Buffer{}); err == nil {
		t.Fatal("download from an unexpected host")
	}
	if _, err := c.Versions(ctx, "../etc", nil, ""); err != ErrNotFound {
		t.Fatalf("path-like project accepted: %v", err)
	}
}

func TestFilenames(t *testing.T) {
	for name, ok := range map[string]bool{"LuckPerms-Bukkit-5.4.jar": true, "fabric-api-0.116+1.21.1.jar": true, "../x.jar": false,
		"a/b.jar": false, "x.sh": false, ".hidden.jar": false} {
		if ValidFilename(name) != ok {
			t.Errorf("%q: %v", name, !ok)
		}
	}
}
