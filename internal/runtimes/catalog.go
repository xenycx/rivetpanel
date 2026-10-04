// Package runtimes loads the administrator-controlled runtime catalog.
package runtimes

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Allowed runtime IDs; must match the CHECK constraint on bots.runtime.
var validIDs = map[string]bool{"nodejs": true, "python": true, "rust": true, "go": true, "java": true, "ruby": true}

var (
	// repo[:tag][@sha256:digest] with no whitespace or shell metacharacters.
	imageRe   = regexp.MustCompile(`^[a-z0-9]+([._/-][a-z0-9]+)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	digestRe  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Defaults are the initial resource settings for new bots of a runtime.
type Defaults struct {
	MemoryBytes int64 `yaml:"memory_bytes"`
	NanoCPUs    int64 `yaml:"nano_cpus"`
	PidsLimit   int64 `yaml:"pids_limit"`
}

// Runtime is one approved language runtime.
type Runtime struct {
	ID                   string            `yaml:"id"`
	DisplayName          string            `yaml:"display_name"`
	Image                string            `yaml:"image"`
	BuilderImage         string            `yaml:"builder_image"`
	Digest               string            `yaml:"digest"`         // optional pinned digest of Image
	BuilderDigest        string            `yaml:"builder_digest"` // optional pinned digest of BuilderImage
	BuildArgv            []string          `yaml:"build_argv"`     // optional; empty means no build stage
	BuildIfFiles         []string          `yaml:"build_if_files"` // build only if one exists in the workspace; empty = always
	Env                  map[string]string `yaml:"env"`            // applied to build and run containers; bot variables override
	DefaultArgv          []string          `yaml:"default_argv"`
	AllowedCommands      []string          `yaml:"allowed_commands"`
	AllowWorkspaceBinary bool              `yaml:"allow_workspace_binary"`
	Defaults             Defaults          `yaml:"defaults"`
	MinMemoryBytes       int64             `yaml:"min_memory_bytes"`
	// BuildMemoryBytes raises the memory limit of the build container for
	// runtimes whose compilers need it (0 = the runner default). Builders get no
	// swap, so this is real RAM needed while building, not while running.
	BuildMemoryBytes int64 `yaml:"build_memory_bytes"`
	// DiagnosticCommands are administrator-approved direct-argv prefixes. The
	// AI may append safe workspace paths, but can never introduce a shell.
	DiagnosticCommands [][]string `yaml:"diagnostic_commands"`
	// BuildTimeout overrides the runner's build timeout for one stage (set
	// by the runner for game installs such as SteamCMD; never from YAML).
	BuildTimeout time.Duration `yaml:"-"`
}

// ImageRef is the execution image reference, pinned by digest when known.
func (r Runtime) ImageRef() string {
	if r.Digest != "" {
		base, _, _ := strings.Cut(r.Image, "@")
		return base + "@" + r.Digest
	}
	return r.Image
}

// BuilderRef is the builder image reference, pinned by digest when known.
func (r Runtime) BuilderRef() string {
	if r.BuilderDigest != "" {
		base, _, _ := strings.Cut(r.BuilderImage, "@")
		return base + "@" + r.BuilderDigest
	}
	return r.BuilderImage
}

// CommandAllowed reports whether argv0 may start a bot of this runtime.
func (r Runtime) CommandAllowed(argv0 string) bool {
	for _, c := range r.AllowedCommands {
		if c == argv0 {
			return true
		}
	}
	if r.AllowWorkspaceBinary && filepath.IsLocal(argv0) {
		return true
	}
	// "./app" is local too, but be explicit about the leading dot form.
	return r.AllowWorkspaceBinary && strings.HasPrefix(argv0, "./") && filepath.IsLocal(argv0[2:])
}

// Catalog is an immutable set of runtimes.
type Catalog struct{ byID map[string]Runtime }

// Load reads every *.yaml in fsys and validates it.
func Load(fsys fs.FS) (*Catalog, error) {
	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	c := &Catalog{byID: map[string]Runtime{}}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var r Runtime
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if _, dup := c.byID[r.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate runtime %q", f, r.ID)
		}
		c.byID[r.ID] = r
	}
	if len(c.byID) == 0 {
		return nil, fmt.Errorf("runtime catalog is empty")
	}
	return c, nil
}

func (r Runtime) validate() error {
	if !validIDs[r.ID] {
		return fmt.Errorf("unsupported runtime id %q", r.ID)
	}
	if r.DisplayName == "" {
		return fmt.Errorf("display_name required")
	}
	for _, img := range []string{r.Image, r.BuilderImage} {
		if !imageRe.MatchString(img) {
			return fmt.Errorf("invalid image reference %q", img)
		}
	}
	for _, d := range []string{r.Digest, r.BuilderDigest} {
		if d != "" && !digestRe.MatchString(d) {
			return fmt.Errorf("invalid digest %q", d)
		}
	}
	if len(r.DefaultArgv) == 0 {
		return fmt.Errorf("default_argv must be non-empty")
	}
	if len(r.BuildIfFiles) > 0 && len(r.BuildArgv) == 0 {
		return fmt.Errorf("build_if_files requires build_argv")
	}
	for _, f := range r.BuildIfFiles {
		if !filepath.IsLocal(f) {
			return fmt.Errorf("build_if_files entry %q must be a relative path inside the workspace", f)
		}
	}
	for k, v := range r.Env {
		if !envNameRe.MatchString(k) || strings.ContainsRune(v, 0) {
			return fmt.Errorf("invalid env entry %q", k)
		}
	}
	if len(r.AllowedCommands) == 0 && !r.AllowWorkspaceBinary {
		return fmt.Errorf("no allowed commands")
	}
	if !r.CommandAllowed(r.DefaultArgv[0]) {
		return fmt.Errorf("default_argv[0] %q is not an allowed command", r.DefaultArgv[0])
	}
	d := r.Defaults
	if d.MemoryBytes <= 0 || d.NanoCPUs <= 0 || d.PidsLimit < 1 || d.PidsLimit > 4096 {
		return fmt.Errorf("defaults out of range")
	}
	if r.BuildMemoryBytes < 0 || r.BuildMemoryBytes > 64<<30 {
		return fmt.Errorf("build_memory_bytes out of range")
	}
	for _, argv := range r.DiagnosticCommands {
		if len(argv) == 0 || len(argv) > 8 || strings.ContainsAny(argv[0], " \t\r\n") {
			return fmt.Errorf("invalid diagnostic command prefix")
		}
		for _, a := range argv {
			if strings.ContainsRune(a, 0) {
				return fmt.Errorf("invalid diagnostic argument")
			}
		}
	}
	if r.MinMemoryBytes <= 0 || d.MemoryBytes < r.MinMemoryBytes {
		return fmt.Errorf("min_memory_bytes invalid or above default memory")
	}
	return nil
}

// Get returns a runtime by ID.
func (c *Catalog) Get(id string) (Runtime, bool) { r, ok := c.byID[id]; return r, ok }

// List returns runtimes sorted by ID.
func (c *Catalog) List() []Runtime {
	out := make([]Runtime, 0, len(c.byID))
	for _, r := range c.byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
