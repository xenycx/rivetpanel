package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// asContainer turns the rig's bot into a container app with cfg.
func (g *rig) asContainer(cfg domain.ContainerConfig) {
	g.t.Helper()
	ctx := context.Background()
	if _, err := g.db.ExecContext(ctx, `UPDATE bots SET kind = 'container', runtime = 'image', image_ref = 'docker.io/library/nginx:1.27',
		argv_json = '[]' WHERE id = ?`, botID); err != nil {
		g.t.Fatal(err)
	}
	if err := g.db.PutContainerConfig(ctx, botID, cfg); err != nil {
		g.t.Fatal(err)
	}
}

func TestContainerAppRunsItsImageInsideTheIsolationPolicy(t *testing.T) {
	g := newRig(t)
	g.asContainer(domain.ContainerConfig{Writable: []string{"/var/cache/nginx"},
		Volumes: []domain.Volume{{Name: "html", Path: "/usr/share/nginx/html"}}})
	g.desire("running", false)
	g.pass()
	g.wantState("running")
	s := g.fd.created[0]
	if !s.KeepImageEntrypoint || len(s.Argv) != 0 || s.WorkspaceHostPath != "" {
		t.Fatalf("image entrypoint/command/workdir not kept: %+v", s)
	}
	if len(s.Mounts) != 1 || s.Mounts[0].Target != "/usr/share/nginx/html" ||
		s.Mounts[0].Source != filepath.Join(g.dir, "bots", botID, VolumesDir, "html") {
		t.Fatalf("mounts %+v", s.Mounts)
	}
	if fi, err := os.Stat(s.Mounts[0].Source); err != nil || !fi.IsDir() {
		t.Fatalf("volume folder not created: %v", err)
	}
	if len(s.ExtraTmpfs) != 1 || s.ExtraTmpfs[0] != "/var/cache/nginx" {
		t.Fatalf("tmpfs %v", s.ExtraTmpfs)
	}
	for _, e := range s.Env {
		if strings.HasPrefix(e, "HOME=") || strings.HasPrefix(e, "NODE_ENV=") {
			t.Fatalf("runtime defaults leaked into a container app: %v", s.Env)
		}
	}
	if !strings.Contains(strings.Join(s.Env, "\n"), "DISCORD_TOKEN=s3cret") {
		t.Fatalf("own variables missing: %v", s.Env)
	}
	if !strings.Contains(s.Image, "@sha256:") || s.Image == "" {
		t.Fatalf("image not resolved: %q", s.Image)
	}
}

func TestContainerImagePolicyIsCheckedAtStart(t *testing.T) {
	g := newRig(t, func(o *Options) {
		o.CheckImage = func(ref string) error { return errors.New("images from docker.io are not allowed") }
	})
	g.asContainer(domain.ContainerConfig{})
	g.desire("running", false)
	g.pass()
	b := g.wantState("failed")
	if !strings.Contains(deref(b.LastError), "not allowed") || len(g.fd.created) != 0 || len(g.fd.resolved) != 0 {
		t.Fatalf("refused image was pulled or run: %v created=%d resolved=%d", deref(b.LastError), len(g.fd.created), len(g.fd.resolved))
	}
}

func TestStackServiceWaitsForDependenciesAndJoinsStackNetwork(t *testing.T) {
	g := newRig(t, func(o *Options) { o.Network = "bridge" })
	ctx := context.Background()
	if _, err := g.db.ExecContext(ctx, `INSERT INTO workspaces (id, name, owner_id, personal, created_at_ms, updated_at_ms) VALUES ('w1', 'w', 'u', 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := g.db.CreateStack(ctx, domain.Stack{ID: "st1", WorkspaceID: "w1", OwnerID: "u", Name: "s", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	g.asContainer(domain.ContainerConfig{StackID: "st1", Service: "web", DependsOn: []string{"db"}})
	// The dependency exists but is not running.
	if err := g.db.CreateBot(ctx, domain.Bot{ID: "22222222-2222-4333-8444-555555555555", OwnerID: "u", NodeID: domain.LocalNodeID, Name: "db",
		Kind: domain.KindContainer, Runtime: "image", ImageRef: "docker.io/library/postgres:16", Argv: []string{}, MemoryBytes: 256 << 20,
		NanoCPUs: 5e8, PidsLimit: 128, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := g.db.PutContainerConfig(ctx, "22222222-2222-4333-8444-555555555555", domain.ContainerConfig{StackID: "st1", Service: "db"}); err != nil {
		t.Fatal(err)
	}
	g.desire("running", false)
	if d := g.pass(); d != dependencyPoll {
		t.Fatalf("delay %v, want the dependency poll", d)
	}
	b := g.bot()
	if b.ObservedState != "starting" || deref(b.StateReason) != domain.ReasonWaitingDeps || !strings.Contains(deref(b.LastError), "db") || len(g.fd.created) != 0 {
		t.Fatalf("not waiting: %+v created=%d", b, len(g.fd.created))
	}
	if _, err := g.db.ExecContext(ctx, `UPDATE bots SET observed_state = 'running' WHERE id = '22222222-2222-4333-8444-555555555555'`); err != nil {
		t.Fatal(err)
	}
	g.pass()
	g.wantState("running")
	s := g.fd.created[0]
	net := StackNetwork("st1")
	if !g.fd.networks[net] || len(s.Networks) != 1 || s.Networks[0] != net || s.JoinAliases[net][0] != "web" {
		t.Fatalf("stack network: networks=%v spec=%v aliases=%v", g.fd.networks, s.Networks, s.JoinAliases)
	}
	if joined := g.fd.joined[deref(g.bot().ContainerID)]; len(joined) != 1 || joined[0] != net {
		t.Fatalf("joined %v", joined)
	}
}

func TestOfflineStackServiceUsesOnlyItsStackNetwork(t *testing.T) {
	g := newRig(t) // the rig's default network is none
	ctx := context.Background()
	if _, err := g.db.ExecContext(ctx, `INSERT INTO workspaces (id, name, owner_id, personal, created_at_ms, updated_at_ms) VALUES ('w1', 'w', 'u', 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := g.db.CreateStack(ctx, domain.Stack{ID: "st1", WorkspaceID: "w1", OwnerID: "u", Name: "s", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	g.asContainer(domain.ContainerConfig{StackID: "st1", Service: "db"})
	g.desire("running", false)
	g.pass()
	g.wantState("running")
	s := g.fd.created[0]
	if s.Network != StackNetwork("st1") || len(s.NetworkAliases) != 1 || s.NetworkAliases[0] != "db" || len(s.Networks) != 0 {
		t.Fatalf("offline service networking: %q %v %v", s.Network, s.NetworkAliases, s.Networks)
	}
}
