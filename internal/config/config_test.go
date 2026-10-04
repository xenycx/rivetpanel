package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsProduction(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Production || c.DBPath != "/var/lib/rivetpanel/rivetpanel.db" || c.RunnerMode != RunnerLocal {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestDevelopmentUsesRelativePaths(t *testing.T) {
	c, err := Load(env(map[string]string{"RIVET_ENV": "development"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Production || c.DBPath != ".dev-data/rivetpanel.db" {
		t.Fatalf("unexpected dev config: %+v", c)
	}
}

func TestInvalid(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"env":    {"RIVET_ENV": "staging"},
		"listen": {"RIVET_LISTEN": "nonsense"},
		"port":   {"RIVET_LISTEN": "127.0.0.1:99999"},
		"runner": {"RIVET_RUNNER_MODE": "remote"},
		"conns":  {"RIVET_DB_MAX_CONNS": "0"},
		"dbpath": {"RIVET_DB_PATH": "/x/a?b"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestContainerPolicyValidation(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"root user":      {"RIVET_CONTAINER_USER": "0:0"},
		"root gid":       {"RIVET_CONTAINER_USER": "1000:0"},
		"non numeric":    {"RIVET_CONTAINER_USER": "nobody"},
		"host network":   {"RIVET_CONTAINER_NETWORK": "host"},
		"joined network": {"RIVET_CONTAINER_NETWORK": "container:abc"},
		"bad owner":      {"RIVET_WORKSPACE_OWNER": "x:y"},
		"workers":        {"RIVET_RUNNER_WORKERS": "0"},
		"build timeout":  {"RIVET_BUILD_TIMEOUT": "1s"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	c, err := Load(env(map[string]string{"RIVET_CONTAINER_USER": "0:0", "RIVET_ALLOW_ROOT_CONTAINER_USER": "1", "RIVET_WORKSPACE_OWNER": "1000:1000"}))
	if err != nil || !c.AllowRootUser || c.WorkspaceOwner != "1000:1000" || !c.ContainerUserSet || !c.WorkspaceOwnerSet {
		t.Fatal(err, c)
	}
	if c, _ := Load(env(nil)); c.ContainerUser != "65532:65532" || c.ContainerUserSet || c.WorkspaceOwnerSet || c.ContainerNetwork != "bridge" || c.DockerHost != "unix:///var/run/docker.sock" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestTelemetryConfig(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"too frequent": {"RIVET_TELEMETRY_INTERVAL": "5s"},
		"too rare":     {"RIVET_TELEMETRY_INTERVAL": "5m"},
		"retention":    {"RIVET_TELEMETRY_RETENTION": "1m"},
		"unparsable":   {"RIVET_TELEMETRY_INTERVAL": "soon"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	c, err := Load(env(nil))
	if err != nil || c.TelemetryInterval != 30*time.Second || c.TelemetryRetention != 7*24*time.Hour {
		t.Fatal(err, c.TelemetryInterval, c.TelemetryRetention)
	}
}

func TestOAuthConfigValidation(t *testing.T) {
	load := func(kv map[string]string) error {
		_, err := Load(func(k string) string { return kv[k] })
		return err
	}
	base := map[string]string{"RIVET_ENV": "development"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	if err := load(base); err != nil {
		t.Fatal("OAuth must be optional:", err)
	}
	cases := map[string]struct {
		env map[string]string
		ok  bool
	}{
		"id without secret":    {map[string]string{"RIVET_GITHUB_CLIENT_ID": "a"}, false},
		"missing public url":   {map[string]string{"RIVET_GITHUB_CLIENT_ID": "a", "RIVET_GITHUB_CLIENT_SECRET": "b"}, false},
		"path in public url":   {map[string]string{"RIVET_GITHUB_CLIENT_ID": "a", "RIVET_GITHUB_CLIENT_SECRET": "b", "RIVET_PUBLIC_URL": "https://p.example.com/panel"}, false},
		"http non-local":       {map[string]string{"RIVET_GITHUB_CLIENT_ID": "a", "RIVET_GITHUB_CLIENT_SECRET": "b", "RIVET_PUBLIC_URL": "http://p.example.com"}, false},
		"https ok":             {map[string]string{"RIVET_GITHUB_CLIENT_ID": "a", "RIVET_GITHUB_CLIENT_SECRET": "b", "RIVET_PUBLIC_URL": "https://panel.xenyc.ge/"}, true},
		"http localhost (dev)": {map[string]string{"RIVET_DISCORD_CLIENT_ID": "a", "RIVET_DISCORD_CLIENT_SECRET": "b", "RIVET_PUBLIC_URL": "http://localhost:8080"}, true},
	}
	for name, tc := range cases {
		if err := load(with(tc.env)); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
	c, _ := Load(func(k string) string { return with(cases["https ok"].env)[k] })
	if c.PublicURL != "https://panel.xenyc.ge" || !c.OAuthConfigured("github") || c.OAuthConfigured("discord") {
		t.Fatalf("%+v", c)
	}
}

func TestSitesDomains(t *testing.T) {
	base := map[string]string{"RIVET_SITES_LISTEN": "127.0.0.1:8081", "RIVET_SITES_BASE_URL": "https://sites.example.com",
		"RIVET_PUBLIC_URL": "https://panel.example.org"}
	with := func(v string) map[string]string {
		m := map[string]string{"RIVET_SITES_DOMAINS": v}
		for k, x := range base {
			m[k] = x
		}
		return m
	}
	c, err := Load(env(with(" Pages.Example.NET., ,other-sites.io")))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SitesDomains) != 2 || c.SitesDomains[0] != "pages.example.net" || c.SitesDomains[1] != "other-sites.io" {
		t.Fatalf("domains = %q", c.SitesDomains)
	}
	for _, bad := range []string{"example.com", "a.sites.example.com", "example.org", "x.panel.example.org", "nodot", "a_b.io", "pages.io,pages.io", "bücher.de"} {
		if _, err := Load(env(with(bad))); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
