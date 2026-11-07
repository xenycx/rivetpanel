package runner

import (
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// A routable container joins the apps network under the alias the sites
// listener resolves, next to its stack network; capabilities outside the
// trusted list never reach the spec.
func TestApplyContainerRoutableAndCaps(t *testing.T) {
	r := &Runner{opts: Options{AppsNetwork: "rivetpanel-apps"}}
	bot := domain.Bot{ID: "b1", Container: &domain.ContainerConfig{StackID: "s1", Service: "web", Routable: true,
		CapAdd: []string{"SETUID", "SYS_ADMIN", "CHOWN"}}}
	s := ContainerSpec{Network: "bridge"}
	r.applyContainer(bot, &s, "", nil, nil)
	if len(s.Networks) != 2 || s.Networks[0] != "rivetpanel-apps" || s.Networks[1] != StackNetwork("s1") {
		t.Fatalf("networks %v", s.Networks)
	}
	if a := s.JoinAliases["rivetpanel-apps"]; len(a) != 1 || a[0] != AppNetworkAlias("b1") {
		t.Fatalf("apps alias %v", s.JoinAliases)
	}
	if a := s.JoinAliases[StackNetwork("s1")]; len(a) != 1 || a[0] != "web" {
		t.Fatalf("stack alias %v", s.JoinAliases)
	}
	if len(s.CapAdd) != 2 || s.CapAdd[0] != "CHOWN" || s.CapAdd[1] != "SETUID" {
		t.Fatalf("caps %v", s.CapAdd)
	}
	// Without network access a container is never routable.
	off := ContainerSpec{Network: "none"}
	r.applyContainer(bot, &off, "", nil, nil)
	for _, n := range off.Networks {
		if n == "rivetpanel-apps" {
			t.Fatal("an offline container joined the apps network")
		}
	}
	// Not routable: no apps network.
	plain := ContainerSpec{Network: "bridge"}
	bot.Container.Routable = false
	r.applyContainer(bot, &plain, "", nil, nil)
	if len(plain.Networks) != 1 || plain.Networks[0] != StackNetwork("s1") {
		t.Fatalf("networks %v", plain.Networks)
	}
}
