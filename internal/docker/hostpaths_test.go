package docker

import (
	"testing"

	"github.com/xenycx/rivetpanel/internal/runner"
)

func TestPathMapTranslate(t *testing.T) {
	m := PathMap{
		{Container: "/var/lib/rivetpanel", Host: "/var/lib/docker/volumes/rivetpanel-data/_data"},
		{Container: "/var/lib/rivetpanel/workspaces", Host: "/srv/rivet/workspaces"},
	}
	cases := map[string]string{
		"/var/lib/rivetpanel/addons/x":           "/var/lib/docker/volumes/rivetpanel-data/_data/addons/x",
		"/var/lib/rivetpanel/workspaces/abc":     "/srv/rivet/workspaces/abc",
		"/var/lib/rivetpanel":                    "/var/lib/docker/volumes/rivetpanel-data/_data",
		"/var/lib/rivetpanelx/abc":               "/var/lib/rivetpanelx/abc",
		"/opt/elsewhere":                         "/opt/elsewhere",
		"/var/lib/rivetpanel/workspaces/../ai-x": "/var/lib/docker/volumes/rivetpanel-data/_data/ai-x",
	}
	for in, want := range cases {
		if got := m.Translate(in); got != want {
			t.Errorf("Translate(%q) = %q, want %q", in, got, want)
		}
	}
	if !m.Covers("/var/lib/rivetpanel/x") || m.Covers("/tmp/x") {
		t.Fatal("Covers wrong")
	}
	if m.Identity() || !(PathMap{{"/a", "/a"}}).Identity() {
		t.Fatal("Identity wrong")
	}
}

func TestCreateTranslatesBindSources(t *testing.T) {
	a := &Adapter{}
	a.SetPathMap(PathMap{{Container: "/data", Host: "/host/data"}})
	in := runner.ContainerSpec{WorkspaceHostPath: "/data/workspaces/b", Mounts: []runner.Mount{{Source: "/data/addons/b/redis", Target: "/data"}}}
	out := a.translateSpec(in)
	if out.WorkspaceHostPath != "/host/data/workspaces/b" || out.Mounts[0].Source != "/host/data/addons/b/redis" {
		t.Fatalf("translated spec = %+v", out)
	}
	if in.Mounts[0].Source != "/data/addons/b/redis" {
		t.Fatal("the caller's spec was modified")
	}
	if hc := HostConfig(out); hc.Mounts[0].Source != "/host/data/workspaces/b" {
		t.Fatalf("host config mounts = %+v", hc.Mounts)
	}
}
