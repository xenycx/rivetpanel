//go:build integration

package integration

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/blueprints"
	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/gamequery"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/service"
)

// TestMinecraftPaperServer installs a real Paper server from the PaperMC API,
// starts it, pings it and stops it gracefully. It needs internet access:
//
//	RIVET_TEST_DOCKER_HOST=unix:///var/run/docker.sock RIVET_TEST_GAMES=1 \
//	  go test -tags integration ./tests/integration -run Minecraft -v -timeout 20m
func TestMinecraftPaperServer(t *testing.T) {
	runMinecraft(t, "minecraft-paper", nil)
}

// TestMinecraftForgeServer exercises the install-script path: the Forge
// installer runs in a container and downloads the game libraries.
func TestMinecraftForgeServer(t *testing.T) {
	runMinecraft(t, "minecraft-forge", map[string]string{"MINECRAFT_VERSION": "1.20.1"})
}

func runMinecraft(t *testing.T, slug string, vars map[string]string) {
	if testing.Short() || !envOn("RIVET_TEST_GAMES") {
		t.Skip("set RIVET_TEST_GAMES=1 to download and run a real Minecraft server")
	}
	s := newStack(t, func(o *runner.Options) {
		o.Network = "bridge"
		o.BuildTimeout = 15 * time.Minute
		o.StopTimeout = 10 * time.Second
	})
	games := &service.GameService{Bots: s.bots, Store: s.db, Providers: &blueprint.Providers{}, Builtins: blueprints.FS}
	if err := games.SyncBuiltins(s.ctx); err != nil {
		t.Fatal(err)
	}
	b, err := games.CreateServer(s.ctx, s.user, service.GameServerInput{Name: "it-" + slug, Blueprint: slug, Variables: vars,
		MemoryBytes: 2 << 30, NanoCPUs: 2e9, Agreements: []string{"minecraft-eula"}})
	if err != nil {
		t.Fatal(err)
	}
	lastID = b.ID
	port := b.Allocations[0].Port
	t.Logf("server %s on port %d", b.ID, port)

	if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "running", 12*time.Minute)
	if b.InstallState != domain.InstallInstalled || b.ImageChoice == "" {
		t.Fatalf("install state %q image %q", b.InstallState, b.ImageChoice)
	}
	t.Logf("installed with %s", b.ImageChoice)
	s.waitLog(*b.ContainerID, ")! For help, type", 6*time.Minute)

	props := s.read(b, "server.properties")
	if !strings.Contains(props, "server-port="+strconv.Itoa(port)) {
		t.Fatalf("server.properties does not use the allocation:\n%s", props)
	}
	if eula := s.read(b, "eula.txt"); !strings.Contains(eula, "eula=true") {
		t.Fatalf("eula.txt = %q", eula)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	st, err := gamequery.MinecraftJava(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil || !st.Online || st.Max != 20 {
		t.Fatalf("server list ping: %+v %v", st, err)
	}
	t.Logf("ping: %s, %d/%d players, %q", st.Version, st.Players, st.Max, st.MOTD)

	// A graceful stop writes "stop" to the console: the world is saved and the
	// process exits by itself before the SIGTERM/SIGKILL fallback.
	cid := *b.ContainerID
	if _, err := s.bots.Stop(s.ctx, s.user, b.ID); err != nil {
		t.Fatal(err)
	}
	b = s.waitObserved(b.ID, "stopped", 3*time.Minute)
	logs := s.logs(cid)
	if !strings.Contains(logs, "Stopping server") || !strings.Contains(logs, "Saving") {
		t.Fatalf("server did not stop through its console:\n%s", tail(logs, 30))
	}
	if b.LastExitCode == nil || *b.LastExitCode != 0 {
		t.Fatalf("exit code %v", b.LastExitCode)
	}
}

func (s *stack) read(b domain.Bot, name string) string {
	s.t.Helper()
	data, err := s.ws.ReadFile(b.ID, name, 1<<20)
	if err != nil {
		s.t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func envOn(name string) bool { return os.Getenv(name) == "1" }
