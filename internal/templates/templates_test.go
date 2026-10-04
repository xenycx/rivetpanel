package templates

import (
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/runtimes"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

func TestTemplates(t *testing.T) {
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		t.Fatal(err)
	}
	list := List()
	if len(list) != 7 {
		t.Fatalf("templates = %d, want 7", len(list))
	}
	seen := map[string]bool{}
	for _, tp := range list {
		rt, ok := cat.Get(tp.Runtime)
		if !ok {
			t.Errorf("%s: runtime %q not in the catalog", tp.ID, tp.Runtime)
			continue
		}
		files, err := Files(tp.ID)
		if err != nil || len(files) == 0 {
			t.Errorf("%s: %v", tp.ID, err)
			continue
		}
		names := map[string]string{}
		for _, f := range files {
			if strings.Contains(f.Path, "..") || strings.HasPrefix(f.Path, "/") {
				t.Errorf("%s: unsafe path %q", tp.ID, f.Path)
			}
			names[f.Path] = string(f.Data)
		}
		// Every template must contain the file its runtime builds from, and must
		// read the token from the environment rather than embed one.
		for _, bf := range rt.BuildIfFiles {
			if _, ok := names[bf]; !ok {
				t.Errorf("%s: missing %s required by the %s build", tp.ID, bf, rt.ID)
			}
		}
		var all strings.Builder
		for _, c := range names {
			all.WriteString(c)
		}
		if !strings.Contains(all.String(), "DISCORD_TOKEN") {
			t.Errorf("%s: does not read DISCORD_TOKEN", tp.ID)
		}
		seen[tp.Runtime] = true
	}
	for _, rt := range []string{"nodejs", "python", "rust", "go", "java", "ruby"} {
		if !seen[rt] {
			t.Errorf("no template for %s", rt)
		}
	}
	for _, id := range []string{"../etc", "a/b", "", "DISCORDJS", "nope"} {
		if _, ok := Get(id); ok {
			t.Errorf("Get(%q) succeeded", id)
		}
	}
	if fs, _ := Files("discordjs"); func() bool {
		for _, f := range fs {
			if f.Path == "rivetpanel.js" {
				return false
			}
		}
		return true
	}() {
		t.Error("SDK snippet not included for discordjs")
	}
}

func TestTemplateSetupMetadata(t *testing.T) {
	for _, tp := range List() {
		if tp.Version < 1 || len(tp.Setup) == 0 || tp.TestedWith == "" || tp.FirstStart == "" {
			t.Errorf("%s: incomplete setup metadata: %+v", tp.ID, tp)
		}
		var token bool
		for _, e := range tp.Env {
			if e.Name == "DISCORD_TOKEN" && e.Required && e.Secret && e.Default == "" {
				token = true
			}
		}
		if !token {
			t.Errorf("%s: DISCORD_TOKEN must be declared as a required secret without a default", tp.ID)
		}
	}
}
