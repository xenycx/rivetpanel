package main

import (
	"os"
	"strings"
	"testing"
)

func TestGodebugSets(t *testing.T) {
	for _, c := range []struct {
		env  string
		want bool
	}{
		{"", false},
		{"madvdontneed=1", false},
		{"disablethp=1", true},
		{"gctrace=1, disablethp=0", true},
		{"disablethpx=1", false},
	} {
		if got := godebugSets(c.env, "disablethp"); got != c.want {
			t.Errorf("godebugSets(%q) = %v, want %v", c.env, got, c.want)
		}
	}
}

// The prctl is per process; run it here (the test binary is a throwaway
// process) and check the kernel reports THP disabled for it.
func TestDisableTransparentHugePages(t *testing.T) {
	t.Setenv("GODEBUG", "")
	if !disableTransparentHugePages() {
		t.Skip("kernel refused PR_SET_THP_DISABLE")
	}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skip(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "THP_enabled:") {
			if strings.TrimSpace(strings.TrimPrefix(l, "THP_enabled:")) != "0" {
				t.Fatalf("THP still enabled: %q", l)
			}
			return
		}
	}
	t.Skip("kernel does not report THP_enabled")
}

func TestDisableTHPRespectsGodebug(t *testing.T) {
	t.Setenv("GODEBUG", "disablethp=0")
	if disableTransparentHugePages() {
		t.Fatal("prctl applied although GODEBUG sets disablethp")
	}
}
