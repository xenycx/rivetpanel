package reposcan

import (
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/runtimes"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

// The nodejs runtime recipe and imported Node projects install dependencies
// the same way, including the fallback for a lockfile that no longer matches
// package.json.
func TestNodeRuntimeUsesTheSharedInstallScript(t *testing.T) {
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	rt, ok := cat.Get("nodejs")
	if !ok || len(rt.BuildArgv) != 3 || rt.BuildArgv[0] != "sh" || rt.BuildArgv[1] != "-c" {
		t.Fatalf("nodejs build_argv = %q", rt.BuildArgv)
	}
	if got, want := strings.TrimSpace(rt.BuildArgv[2]), NPMCleanInstall("--omit=dev"); got != want {
		t.Fatalf("nodejs recipe differs from NPMCleanInstall:\n%s\n---\n%s", got, want)
	}
}

func TestNPMCleanInstallFallsBackOnlyForLockfileDrift(t *testing.T) {
	s := NPMCleanInstall("--include=dev")
	for _, want := range []string{
		"npm ci --include=dev --no-audit --no-fund",
		"grep -q 'code EUSAGE'",
		"npm install --include=dev --no-audit --no-fund",
		`exit "$rc"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if !strings.Contains(webPresets[1].BuildCommand, "code EUSAGE") {
		t.Fatal("web presets do not use the shared install script")
	}
}
