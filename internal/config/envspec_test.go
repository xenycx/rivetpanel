package config

import (
	"sort"
	"strings"
	"testing"
)

// Every variable Load reads must be described in Vars, and every described
// variable must be read: the administration page lists exactly this set.
func TestVarsMatchWhatLoadReads(t *testing.T) {
	read := map[string]bool{}
	_, _ = LoadLookup(func(name string) (string, bool) { read[name] = true; return "", false })
	var missing, stale []string
	for name := range read {
		if _, ok := Spec(name); !ok {
			missing = append(missing, name)
		}
	}
	for _, v := range Vars {
		if !read[v.Name] {
			stale = append(stale, v.Name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("read by Load but not described in Vars: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("described in Vars but never read by Load: %v", stale)
	}
	seen := map[string]bool{}
	groups := map[string]bool{}
	for _, g := range Groups {
		groups[g] = true
	}
	for _, v := range Vars {
		if seen[v.Name] {
			t.Errorf("%s is listed twice", v.Name)
		}
		seen[v.Name] = true
		if !strings.HasPrefix(v.Name, "RIVET_") || v.Description == "" || !groups[v.Group] {
			t.Errorf("%s: needs the prefix, a description and a known group", v.Name)
		}
		if v.Kind == KindEnum && len(v.Options) == 0 {
			t.Errorf("%s: an enum needs options", v.Name)
		}
	}
}

func TestOverlayPrecedence(t *testing.T) {
	base := func(name string) (string, bool) {
		if name == "RIVET_SFTP_LISTEN" {
			return "0.0.0.0:2022", true
		}
		return "", false
	}
	ov := map[string]string{
		"RIVET_SFTP_LISTEN": "",        // explicit empty beats the environment
		"RIVET_LISTEN":      ":9",      // boot variable: never overridden
		"RIVET_MAX_BUILDS":  " 3 ",     // trimmed
		"RIVET_UNKNOWN":     "ignored", // not described
	}
	look := Overlay(base, ov)
	if v, ok := look("RIVET_SFTP_LISTEN"); !ok || v != "" {
		t.Fatalf("explicit empty override = %q, %v", v, ok)
	}
	if _, ok := look("RIVET_LISTEN"); ok {
		t.Fatal("a boot variable must not be overridable")
	}
	if v, _ := look("RIVET_MAX_BUILDS"); v != "3" {
		t.Fatalf("override = %q", v)
	}
	if _, ok := look("RIVET_UNKNOWN"); ok {
		t.Fatal("an undescribed variable must not be overridable")
	}
	cfg, err := LoadLookup(look)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SFTPListen != "" || cfg.MaxBuilds != 3 {
		t.Fatalf("sftp %q builds %d", cfg.SFTPListen, cfg.MaxBuilds)
	}
}

func TestCheckValue(t *testing.T) {
	cases := []struct {
		name, value string
		ok          bool
	}{
		{"RIVET_MAX_BUILDS", "2", true},
		{"RIVET_MAX_BUILDS", "two", false},
		{"RIVET_BUILD_TIMEOUT", "15m", true},
		{"RIVET_BUILD_TIMEOUT", "15", false},
		{"RIVET_PORT_PUBLIC_BIND", "true", false},
		{"RIVET_PORT_PUBLIC_BIND", "1", true},
		{"RIVET_RUNNER_MODE", "remote", false},
		{"RIVET_RUNNER_MODE", "none", true},
		{"RIVET_SITES_LISTEN", "", true},
		{"RIVET_SITES_LISTEN", "a\nb", false},
	}
	for _, c := range cases {
		spec, _ := Spec(c.name)
		if err := CheckValue(spec, c.value); (err == nil) != c.ok {
			t.Errorf("%s=%q: err=%v", c.name, c.value, err)
		}
	}
}
