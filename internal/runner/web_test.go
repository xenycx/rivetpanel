package runner

import (
	"slices"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestWebRuntimeSpec(t *testing.T) {
	g := newRig(t)
	g.r.opts.AppsNetwork = "rivetpanel-apps"
	bot := g.bot()
	rt, _ := g.r.cat.Get("nodejs")
	plain := g.r.runtimeSpec(bot, rt, "img", "/h", nil)
	if plain.Network != g.r.opts.Network || len(plain.Mounts) != 0 || len(plain.NetworkAliases) != 0 {
		t.Fatalf("a bot must not be changed by app settings: %+v", plain)
	}

	bot.Kind = domain.KindWeb
	bot.NetworkDisabled = true // ignored: an app is always reachable
	bot.Ports = []domain.BotPort{{HostIP: "127.0.0.1", HostPort: 20001, ContainerPort: 3000, Protocol: "tcp"}}
	s := g.r.runtimeSpec(bot, rt, "img", "/h", map[string]string{"PORT": "8080", "HOST": "::"})
	if s.Network != "rivetpanel-apps" || !slices.Equal(s.NetworkAliases, []string{AppNetworkAlias(bot.ID)}) {
		t.Fatalf("network = %q aliases = %v", s.Network, s.NetworkAliases)
	}
	if len(s.Ports) != 0 {
		t.Fatalf("an app never publishes ports: %v", s.Ports)
	}
	if len(s.Mounts) != 1 || s.Mounts[0].Source != "/h/.rivet-data" || s.Mounts[0].Target != DataMount {
		t.Fatalf("data mount = %+v", s.Mounts)
	}
	for _, want := range []string{"PORT=8080", "HOST=::", "DATA_DIR=/data", "NODE_ENV=production"} {
		if !slices.Contains(s.Env, want) {
			t.Errorf("env %v lacks %s (the app's own variables win over the defaults)", s.Env, want)
		}
	}
	if s.SpecHash == plain.SpecHash {
		t.Fatal("the app spec must hash differently")
	}

	bot.Addons = []domain.BotAddon{{Kind: "postgres"}}
	if s := g.r.runtimeSpec(bot, rt, "img", "/h", nil); s.Network != "rivetpanel-apps" || !slices.Equal(s.Networks, []string{AddonNetwork(bot.ID)}) {
		t.Fatalf("an app with add-ons joins both networks: %q %v", s.Network, s.Networks)
	}
}

func TestPublicBuildVars(t *testing.T) {
	for name, want := range map[string]bool{"VITE_API": true, "PUBLIC_URL": true, "NEXT_PUBLIC_X": true, "DATABASE_URL": false, "API_KEY": false} {
		if PublicBuildVar(name) != want {
			t.Errorf("%s: got %v", name, !want)
		}
	}
}
