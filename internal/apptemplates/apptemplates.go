// Package apptemplates embeds the one-click app catalog: self-hosted web apps
// (Ghost, Vaultwarden, n8n, ...) offered as ready-made container stacks. Each
// template is a directory under data/ with
//
//	template.yaml   what the catalog shows and which variables it asks for
//	compose.yaml    a plain Compose file, installed through the same Compose
//	                import (and isolation policy) as a pasted file
//	files/          optional seed files: files/<service>/<path in the app's
//	                files>, for example files/app/volumes/config/settings.yml;
//	                a .tmpl suffix expands ${VAR} like the Compose file
//
// Templates are reviewed code shipped with the panel; their variables are
// filled in on the server, and generated secrets never leave it except as the
// sealed environment variables of the apps.
package apptemplates

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed all:data
var data embed.FS

// Template is one catalog entry.
type Template struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Tagline     string `yaml:"tagline" json:"tagline"`
	Description string `yaml:"description" json:"description"`
	Category    string `yaml:"category" json:"category"`
	Website     string `yaml:"website" json:"website"`
	// Web is the service that serves the app's web interface (nil for an
	// app used through its terminal only).
	Web *Web `yaml:"web" json:"web,omitempty"`
	// Console is the service (and command) people attach to in a terminal.
	Console *Console `yaml:"console" json:"console,omitempty"`
	Vars    []Var    `yaml:"vars" json:"vars"`
	// Notes are first-steps hints shown after install; they may use ${VAR}
	// of non-secret variables and the reserved APP_* variables.
	Notes []string `yaml:"notes" json:"notes"`
	// Sandbox lists extra Linux capabilities per service. Only capabilities
	// in the trusted allowlist are accepted; most templates need none.
	Sandbox map[string]Sandbox `yaml:"sandbox" json:"-"`
	// Filled from compose.yaml.
	Services    int   `yaml:"-" json:"services"`
	MemoryBytes int64 `yaml:"-" json:"memory_bytes"`
}

// Web is where the app's web interface listens.
type Web struct {
	Service string `yaml:"service" json:"service"`
	Port    int    `yaml:"port" json:"port"`
	// Health is a path that answers without signing in (default /).
	Health string `yaml:"health" json:"health,omitempty"`
	// Path is appended to the address for the Open button (default /).
	Path string `yaml:"path" json:"path,omitempty"`
}

// Console is a service people use through its terminal.
type Console struct {
	Service string   `yaml:"service" json:"service"`
	Command []string `yaml:"command" json:"command,omitempty"`
}

// Sandbox is a service's extra privileges.
type Sandbox struct {
	CapAdd []string `yaml:"cap_add"`
}

// Var is a value the installer fills in.
type Var struct {
	Name        string `yaml:"name" json:"name"`
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description" json:"description,omitempty"`
	Default     string `yaml:"default" json:"default,omitempty"`
	Required    bool   `yaml:"required" json:"required"`
	Secret      bool   `yaml:"secret" json:"secret"`
	// Generate makes the installer create a random value when none is given.
	Generate *Generate `yaml:"generate" json:"generate,omitempty"`
	// Credential marks a value people need to sign in (shown after install).
	Credential bool `yaml:"credential" json:"credential,omitempty"`
	// Advanced hides the field unless the advanced options are open.
	Advanced bool `yaml:"advanced" json:"advanced,omitempty"`
}

// Generate describes a random value.
type Generate struct {
	Kind   string `yaml:"kind" json:"kind"` // password | hex | alnum | base64url
	Length int    `yaml:"length" json:"length"`
}

// File is a seed file for one service.
type File struct {
	Service string
	// Path is relative to the service's app files (for example
	// volumes/config/settings.yml).
	Path string
	Data []byte
	// Expand reports whether ${VAR} in Data is expanded.
	Expand bool
}

// Reserved variables the installer always sets.
var Reserved = []string{"APP_URL", "APP_HOST", "APP_PORT", "APP_SCHEME", "APP_SECURE", "APP_ORIGIN", "STACK_NAME"}

// Categories in catalog order.
var Categories = []string{"publishing", "productivity", "dev", "monitoring", "reading", "media", "security", "ai"}

var (
	idRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	envNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	varRefRe  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)`)
)

func isReserved(name string) bool {
	for _, r := range Reserved {
		if r == name {
			return true
		}
	}
	return false
}

// List returns every valid template, sorted by name.
func List() []Template {
	ents, _ := fs.ReadDir(data, "data")
	out := make([]Template, 0, len(ents))
	for _, e := range ents {
		if t, err := Load(e.Name()); err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// IDs returns every template directory, valid or not (for tests).
func IDs() []string {
	ents, _ := fs.ReadDir(data, "data")
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// Get returns a template by id.
func Get(id string) (Template, bool) {
	if !idRe.MatchString(id) {
		return Template{}, false
	}
	t, err := Load(id)
	return t, err == nil
}

// Load reads and checks one template.
func Load(id string) (Template, error) {
	if !idRe.MatchString(id) {
		return Template{}, fmt.Errorf("template id %q is not valid", id)
	}
	b, err := fs.ReadFile(data, path.Join("data", id, "template.yaml"))
	if err != nil {
		return Template{}, err
	}
	var t Template
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		return Template{}, fmt.Errorf("template %s: %w", id, err)
	}
	if t.ID != id {
		return Template{}, fmt.Errorf("template %s: id is %q", id, t.ID)
	}
	if strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Tagline) == "" {
		return Template{}, fmt.Errorf("template %s: name and tagline are required", id)
	}
	okCat := false
	for _, c := range Categories {
		okCat = okCat || c == t.Category
	}
	if !okCat {
		return Template{}, fmt.Errorf("template %s: unknown category %q", id, t.Category)
	}
	seen := map[string]bool{}
	for _, v := range t.Vars {
		switch {
		case !envNameRe.MatchString(v.Name):
			return Template{}, fmt.Errorf("template %s: variable name %q", id, v.Name)
		case isReserved(v.Name):
			return Template{}, fmt.Errorf("template %s: %s is set by the installer", id, v.Name)
		case seen[v.Name]:
			return Template{}, fmt.Errorf("template %s: %s is declared twice", id, v.Name)
		case v.Secret && v.Default != "":
			return Template{}, fmt.Errorf("template %s: secret %s cannot have a default", id, v.Name)
		case strings.TrimSpace(v.Label) == "":
			return Template{}, fmt.Errorf("template %s: %s has no label", id, v.Name)
		}
		if g := v.Generate; g != nil {
			if _, ok := alphabets[g.Kind]; !ok {
				return Template{}, fmt.Errorf("template %s: %s: unknown generator %q", id, v.Name, g.Kind)
			}
			if g.Length < 12 || g.Length > 64 {
				return Template{}, fmt.Errorf("template %s: %s: generated length must be 12-64", id, v.Name)
			}
		}
		seen[v.Name] = true
	}
	secret := map[string]bool{}
	for _, v := range t.Vars {
		secret[v.Name] = v.Secret
	}
	for _, n := range t.Notes {
		for _, m := range varRefRe.FindAllStringSubmatch(n, -1) {
			if secret[m[1]] {
				return Template{}, fmt.Errorf("template %s: a note shows the secret %s", id, m[1])
			}
		}
	}
	for svc, sb := range t.Sandbox {
		for _, c := range sb.CapAdd {
			if !trustedCaps[c] {
				return Template{}, fmt.Errorf("template %s: %s: capability %s is not allowed", id, svc, c)
			}
		}
	}
	compose, err := Compose(id)
	if err != nil {
		return Template{}, err
	}
	svcs, mem, err := resources(compose)
	if err != nil {
		return Template{}, fmt.Errorf("template %s: %w", id, err)
	}
	t.Services, t.MemoryBytes = len(svcs), mem
	if t.Web != nil {
		if !svcs[t.Web.Service] || t.Web.Port < 1 || t.Web.Port > 65535 {
			return Template{}, fmt.Errorf("template %s: web service %q:%d", id, t.Web.Service, t.Web.Port)
		}
		if t.Web.Health == "" {
			t.Web.Health = "/"
		}
		if t.Web.Path == "" {
			t.Web.Path = "/"
		}
	}
	if t.Console != nil && !svcs[t.Console.Service] {
		return Template{}, fmt.Errorf("template %s: console service %q", id, t.Console.Service)
	}
	if t.Web == nil && t.Console == nil {
		return Template{}, fmt.Errorf("template %s: needs a web service or a console", id)
	}
	for svc := range t.Sandbox {
		if !svcs[svc] {
			return Template{}, fmt.Errorf("template %s: sandbox names unknown service %q", id, svc)
		}
	}
	if t.Vars == nil {
		t.Vars = []Var{}
	}
	if t.Notes == nil {
		t.Notes = []string{}
	}
	return t, nil
}

// trustedCaps mirrors domain.TrustedCapabilities (kept separate so this
// package has no dependencies; a test checks they match).
var trustedCaps = map[string]bool{"CHOWN": true, "SETUID": true, "SETGID": true, "DAC_OVERRIDE": true, "FOWNER": true}

// TrustedCaps returns the capability allowlist templates are checked against.
func TrustedCaps() map[string]bool { return trustedCaps }

// Compose returns a template's Compose file.
func Compose(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("template id %q is not valid", id)
	}
	b, err := fs.ReadFile(data, path.Join("data", id, "compose.yaml"))
	return string(b), err
}

// Files returns a template's seed files.
func Files(id string) ([]File, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("template id %q is not valid", id)
	}
	root := path.Join("data", id, "files")
	var out []File
	err := fs.WalkDir(data, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return fs.SkipDir // no seed files
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, root+"/")
		svc, file, ok := strings.Cut(rel, "/")
		if !ok {
			return fmt.Errorf("template %s: seed file %s is not under a service folder", id, rel)
		}
		b, err := fs.ReadFile(data, p)
		if err != nil {
			return err
		}
		f := File{Service: svc, Path: file, Data: b}
		if strings.HasSuffix(file, ".tmpl") {
			f.Path, f.Expand = strings.TrimSuffix(file, ".tmpl"), true
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

// resources reads the services of a Compose file and their memory limits
// (512 MiB when unset, the import's default).
func resources(compose string) (map[string]bool, int64, error) {
	var doc struct {
		Services map[string]struct {
			MemLimit string `yaml:"mem_limit"`
			Deploy   struct {
				Resources struct {
					Limits struct {
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"deploy"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, 0, fmt.Errorf("compose.yaml: %w", err)
	}
	if len(doc.Services) == 0 {
		return nil, 0, fmt.Errorf("compose.yaml has no services")
	}
	svcs := map[string]bool{}
	var total int64
	for name, s := range doc.Services {
		svcs[name] = true
		v := s.MemLimit
		if v == "" {
			v = s.Deploy.Resources.Limits.Memory
		}
		n := int64(512 << 20)
		if v != "" {
			if b, err := parseSize(v); err == nil {
				n = b
			}
		}
		total += n
	}
	return svcs, total, nil
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "b")
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1<<10, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1<<20, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "g"):
		mult, s = 1<<30, strings.TrimSuffix(s, "g")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(f * mult), nil
}
