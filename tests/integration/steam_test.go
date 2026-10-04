//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
)

// steamTestBlueprint installs the small "Steamworks SDK Redist" app (1007,
// about 110 MB, anonymous) with SteamCMD, then runs a shell that stands in
// for a game server and stops on SIGINT.
const steamTestBlueprint = `slug: it-steam-redist
name: Steam redist test
category: Tests
images: [{label: "SteamCMD", ref: "steamcmd/steamcmd:latest"}]
startup:
  command: sh -c 'echo "ports $SERVER_PORT $SERVER_PORT_1 name $NAME"; trap "echo stopping-gracefully; exit 0" INT; echo ready; while :; do sleep 1; done'
  stop_signal: SIGINT
  stop_timeout_seconds: 30
variables:
  - {env: NAME, name: Name, default: "redist", editable: true}
install:
  steamcmd: {app_id: 1007, timeout_minutes: 20, max_size_gb: 2}
  script: 'test -f linux64/steamclient.so && test -f .steam/sdk64/steamclient.so && echo "installed $NAME $SERVER_PORT_1" > installed.txt'
resources: {memory_mb: 512, cpus: 1}
ports: {default: 27100, extra: 1, contiguous: true}
`

// TestSteamCMDInstall downloads a real Steam app anonymously:
//
//	RIVET_TEST_DOCKER_HOST=unix:///var/run/docker.sock RIVET_TEST_STEAM=1 \
//	  go test -tags integration ./tests/integration -run SteamCMD -v -timeout 30m
func TestSteamCMDInstall(t *testing.T) {
	if testing.Short() || !envOn("RIVET_TEST_STEAM") {
		t.Skip("set RIVET_TEST_STEAM=1 to download a real Steam app with SteamCMD")
	}
	s := newStack(t, func(o *runner.Options) { o.Network = "bridge"; o.StopTimeout = 10 * time.Second })
	games := &service.GameService{Bots: s.bots, Store: s.db, Providers: &blueprint.Providers{}}
	spec, err := blueprint.Parse([]byte(steamTestBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.db.SaveBlueprintRevision(s.ctx, domain.Blueprint{Slug: spec.Slug, Name: spec.Name, Category: spec.Category, Source: "custom"},
		steamTestBlueprint, blueprint.Hash([]byte(steamTestBlueprint)), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	b, err := games.CreateServer(s.ctx, s.user, service.GameServerInput{Name: "it-steam", Blueprint: spec.Slug,
		Variables: map[string]string{"NAME": "probe"}, MemoryBytes: 512 << 20, NanoCPUs: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	lastID = b.ID
	if len(b.Allocations) != 2 {
		t.Fatalf("allocations %+v", b.Allocations)
	}
	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "running", 20*time.Minute)
	if b.InstallState != domain.InstallInstalled || !strings.HasPrefix(b.InstalledVersion, "build ") {
		t.Fatalf("install state %q version %q", b.InstallState, b.InstalledVersion)
	}
	t.Logf("installed %s", b.InstalledVersion)
	primary, extra := b.Allocations[0].Port, b.Allocations[1].Port
	if !b.Allocations[0].Primary {
		primary, extra = extra, primary
	}
	if extra != primary+1 {
		t.Fatalf("ports are not consecutive: %+v", b.Allocations)
	}
	if got := s.read(b, "installed.txt"); !strings.HasPrefix(got, "installed probe ") {
		t.Fatalf("install script output %q", got)
	}
	out := s.waitLog(*b.ContainerID, "ready", time.Minute)
	if !strings.Contains(out, fmt.Sprintf("ports %d %d name probe", primary, extra)) {
		t.Fatalf("runtime env:\n%s", out)
	}
	cid := *b.ContainerID
	if _, err := s.bots.Stop(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "stopped", 2*time.Minute)
	if !strings.Contains(s.logs(cid), "stopping-gracefully") {
		t.Fatalf("SIGINT was not delivered:\n%s", tail(s.logs(cid), 20))
	}
	if b.LastExitCode == nil || *b.LastExitCode != 0 {
		t.Fatalf("exit code %v", b.LastExitCode)
	}
}
