package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func queryBot(kinds ...string) domain.Bot {
	b := domain.Bot{ID: "11111111-1111-1111-1111-111111111111", NodeID: domain.LocalNodeID}
	for _, k := range kinds {
		b.Addons = append(b.Addons, domain.BotAddon{Kind: k})
	}
	return b
}

func TestAddonQueryPostgresReadOnly(t *testing.T) {
	f := newFake()
	var spec ContainerSpec
	f.onCreate = func(s ContainerSpec) { spec = s }
	f.tail = "id,name\n1,alpha\n2,\"be,ta\"\n"
	q := &AddonQuerier{Docker: f}
	res, err := q.Query(context.Background(), queryBot("postgres"), "postgres", "s3cret", AddonQuery{Mode: "read", Query: "select id, name from t; -- \"$(rm -rf /)\""})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || len(res.Rows) != 2 || res.Rows[1][1] != "be,ta" {
		t.Fatalf("%+v", res)
	}
	env := strings.Join(spec.Env, "\n")
	if !strings.Contains(env, "default_transaction_read_only=on") || !strings.Contains(env, "PGPASSWORD=s3cret") {
		t.Fatalf("env: %s", env)
	}
	// The query travels in the environment, never in the command line.
	if strings.Contains(strings.Join(spec.Entrypoint, " "), "select") || spec.Network != AddonNetwork(queryBot().ID) || spec.WorkspaceHostPath != "" || spec.MemoryBytes > 128<<20 {
		t.Fatalf("%+v", spec)
	}
	if len(f.conts) != 0 {
		t.Fatal("the client container was not removed")
	}

	// Writes drop the read-only option.
	q.Query(context.Background(), queryBot("postgres"), "postgres", "s3cret", AddonQuery{Mode: "write", Query: "delete from t"})
	if strings.Contains(strings.Join(spec.Env, "\n"), "read_only") {
		t.Fatal("write mode kept read-only")
	}
}

func TestAddonQueryGuards(t *testing.T) {
	f := newFake()
	q := &AddonQuerier{Docker: f}
	ctx := context.Background()
	cases := []struct {
		kind string
		q    AddonQuery
	}{
		{"postgres", AddonQuery{Mode: "read", Query: " "}},
		{"redis", AddonQuery{Mode: "read", Args: []string{"FLUSHALL"}}},
		{"redis", AddonQuery{Mode: "write", Args: []string{"CONFIG", "SET", "dir", "/tmp"}}},
		{"mongodb", AddonQuery{Mode: "read", Op: "delete", DB: "app", Coll: "users"}},
		{"mongodb", AddonQuery{Mode: "read", Op: "find", DB: "app", Coll: "../x"}},
	}
	for _, c := range cases {
		if _, err := q.Query(ctx, queryBot(c.kind), c.kind, "pw", c.q); err == nil {
			t.Errorf("%s %+v accepted", c.kind, c.q)
		}
	}
	if _, err := q.Query(ctx, queryBot("redis"), "postgres", "pw", AddonQuery{Mode: "read", Query: "select 1"}); err == nil {
		t.Error("queried an add-on the app does not have")
	}
	if len(f.specs) != 0 {
		t.Fatal("a refused query started a container")
	}
}

func TestAddonQueryErrorsHidePassword(t *testing.T) {
	f := newFake()
	f.onCreate = func(s ContainerSpec) {}
	f.tail = "FATAL: password authentication failed for user bot (s3cret)"
	f.builderExit = 2
	q := &AddonQuerier{Docker: f}
	f.onStart = func(id string) {
		f.mu.Lock()
		f.conts[id].ExitCode = 2
		f.mu.Unlock()
	}
	_, err := q.Query(context.Background(), queryBot("postgres"), "postgres", "s3cret", AddonQuery{Mode: "read", Query: "select 1"})
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "•••") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseMongo(t *testing.T) {
	var res AddonQueryResult
	if err := parseMongo(`noise\n@@RIVET@@[{"_id":"1","n":2}]`, &res); err != nil || len(res.Rows) != 1 || !strings.Contains(res.Rows[0][0], `"n": 2`) {
		t.Fatalf("%+v %v", res, err)
	}
}
