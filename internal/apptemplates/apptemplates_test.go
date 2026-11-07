package apptemplates

import (
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Every template directory must load: a broken one would silently vanish
// from the catalog.
func TestEveryTemplateLoads(t *testing.T) {
	ids := IDs()
	if len(ids) == 0 {
		t.Fatal("no templates")
	}
	for _, id := range ids {
		if _, err := Load(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	if len(List()) != len(ids) {
		t.Fatalf("List has %d of %d templates", len(List()), len(ids))
	}
}

var anyRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)`)

// Each ${VAR} of a template is declared or reserved, each declared variable
// is used, and images are pinned (no :latest, no untagged image).
func TestTemplateVariablesAndImages(t *testing.T) {
	for _, tpl := range List() {
		compose, _ := Compose(tpl.ID)
		files, err := Files(tpl.ID)
		if err != nil {
			t.Fatalf("%s files: %v", tpl.ID, err)
		}
		text := compose
		for _, f := range files {
			if f.Expand {
				text += "\n" + string(f.Data)
			}
		}
		declared := map[string]bool{}
		for _, v := range tpl.Vars {
			declared[v.Name] = true
		}
		used := map[string]bool{}
		for _, m := range anyRef.FindAllStringSubmatch(text, -1) {
			used[m[1]] = true
			if !declared[m[1]] && !isReserved(m[1]) {
				t.Errorf("%s: ${%s} is neither declared nor reserved", tpl.ID, m[1])
			}
		}
		for name := range declared {
			if !used[name] {
				t.Errorf("%s: %s is declared but never used", tpl.ID, name)
			}
		}
		var doc struct {
			Services map[string]map[string]any `yaml:"services"`
		}
		if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		webPublished := false
		for name, svc := range doc.Services {
			img, _ := svc["image"].(string)
			last := img[strings.LastIndex(img, "/")+1:]
			if !strings.Contains(last, ":") || strings.HasSuffix(img, ":latest") {
				t.Errorf("%s/%s: image %q must be pinned to a version tag", tpl.ID, name, img)
			}
			for _, k := range []string{"privileged", "cap_add", "build", "network_mode", "pid", "ipc", "devices"} {
				if _, ok := svc[k]; ok {
					t.Errorf("%s/%s: %s is not allowed in a template", tpl.ID, name, k)
				}
			}
			if ports, ok := svc["ports"].([]any); ok {
				for _, p := range ports {
					ps, _ := p.(string)
					if !strings.HasPrefix(ps, "${APP_PORT}:") {
						t.Errorf("%s/%s: port %v must be published as ${APP_PORT}:<port>", tpl.ID, name, p)
					}
					if tpl.Web != nil && name == tpl.Web.Service && ps == "${APP_PORT}:"+itoa(tpl.Web.Port) {
						webPublished = true
					}
				}
			}
		}
		if tpl.Web != nil && !webPublished {
			t.Errorf("%s: the web service must publish ${APP_PORT}:%d", tpl.ID, tpl.Web.Port)
		}
	}
}

func itoa(n int) string {
	b, _ := yaml.Marshal(n)
	return strings.TrimSpace(string(b))
}

func TestRandom(t *testing.T) {
	for kind, alpha := range alphabets {
		v, err := Random(Generate{Kind: kind, Length: 32})
		if err != nil || len(v) != 32 {
			t.Fatalf("%s: %q %v", kind, v, err)
		}
		for _, r := range v {
			if !strings.ContainsRune(alpha, r) {
				t.Fatalf("%s: %q has %q", kind, v, r)
			}
		}
	}
	a, _ := Random(Generate{Kind: "password", Length: 20})
	b, _ := Random(Generate{Kind: "password", Length: 20})
	if a == b {
		t.Fatal("two passwords are equal")
	}
	if _, err := Random(Generate{Kind: "nope", Length: 20}); err == nil {
		t.Fatal("unknown generator accepted")
	}
}

func TestGetRejectsPaths(t *testing.T) {
	for _, id := range []string{"../x", "Miniflux", "a/b", ""} {
		if _, ok := Get(id); ok {
			t.Fatalf("%q accepted", id)
		}
	}
}
