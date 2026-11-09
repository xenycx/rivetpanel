package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLiteQueryReadOnlyMount(t *testing.T) {
	f := newFake()
	var spec ContainerSpec
	f.onCreate = func(s ContainerSpec) { spec = s }
	f.tail = "@@RIVET@@{\"columns\":[\"id\",\"name\"],\"rows\":[[\"1\",\"a\"]],\"truncated\":false}\n"
	q := &AddonQuerier{Docker: f, User: "1000:1000"}
	res, err := q.QuerySQLite(context.Background(), queryBot(), "/data/bots/x", "volumes/data/vault.db", 10001, 10001,
		AddonQuery{Mode: "read", Query: "select id, name from t; -- \"$(rm -rf /)\""})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || len(res.Rows) != 1 || res.Rows[0][1] != "a" {
		t.Fatalf("%+v", res)
	}
	// Reads see the app's files only through a read-only mount, run as the
	// file's owner, without a network; the query is never on the command line.
	if len(spec.Mounts) != 1 || !spec.Mounts[0].ReadOnly || spec.Mounts[0].Source != "/data/bots/x" || spec.Mounts[0].Target != sqliteFiles {
		t.Fatalf("mounts: %+v", spec.Mounts)
	}
	if spec.User != "10001:10001" || spec.Network != "none" || spec.WorkspaceHostPath != "" || spec.MemoryBytes > 128<<20 {
		t.Fatalf("%+v", spec)
	}
	env := strings.Join(spec.Env, "\n")
	if !strings.Contains(env, "RIVET_MODE=read") || !strings.Contains(env, "RIVET_FILE=volumes/data/vault.db") || strings.Contains(strings.Join(spec.Entrypoint, " "), "select id") {
		t.Fatalf("env: %s", env)
	}
	if len(f.conts) != 0 {
		t.Fatal("the client container was not removed")
	}

	// A confirmed change gets a writable mount; a root-owned file does not
	// make the client run as root.
	q.QuerySQLite(context.Background(), queryBot(), "/data/bots/x", "app.db", 0, 0, AddonQuery{Mode: "write", Query: "delete from t"})
	if spec.Mounts[0].ReadOnly || !strings.Contains(strings.Join(spec.Env, "\n"), "RIVET_MODE=write") || spec.User != "1000:1000" {
		t.Fatalf("%+v", spec)
	}
}

func TestSQLiteQueryGuards(t *testing.T) {
	f := newFake()
	q := &AddonQuerier{Docker: f}
	ctx := context.Background()
	for _, c := range []struct{ host, rel, query string }{
		{"/h", "../other/app.db", "select 1"},
		{"/h", "/etc/app.db", "select 1"},
		{"/h", "", "select 1"},
		{"", "app.db", "select 1"},
		{"/h", "app.db", "  "},
	} {
		if _, err := q.QuerySQLite(ctx, queryBot(), c.host, c.rel, 1000, 1000, AddonQuery{Mode: "read", Query: c.query}); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if len(f.specs) != 0 {
		t.Fatal("a refused query started a container")
	}
}

func TestSQLiteQueryError(t *testing.T) {
	f := newFake()
	f.tail = "attempt to write a readonly database\n"
	f.onStart = func(id string) {
		f.mu.Lock()
		f.conts[id].ExitCode = 1
		f.mu.Unlock()
	}
	q := &AddonQuerier{Docker: f}
	_, err := q.QuerySQLite(context.Background(), queryBot(), "/h", "app.db", 1000, 1000, AddonQuery{Mode: "read", Query: "delete from t"})
	if err == nil || !strings.Contains(err.Error(), "Run as change") {
		t.Fatalf("err = %v", err)
	}
}

func TestMountReadOnlyKeepsSpecHash(t *testing.T) {
	b, _ := json.Marshal(Mount{Source: "/a", Target: "/b"})
	if string(b) != `{"Source":"/a","Target":"/b"}` {
		t.Fatalf("a writable mount must hash as before: %s", b)
	}
}
