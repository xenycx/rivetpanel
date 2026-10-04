// Package blueprint defines game-server blueprints: the images, installation,
// startup command, variables, configuration edits and ports of one kind of
// game server. A blueprint is plain YAML; revisions stored by the panel are
// immutable and every server pins the revision it runs.
package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

const (
	MaxSpecBytes  = 256 << 10
	maxVariables  = 40
	maxImages     = 12
	maxConfigs    = 16
	maxScriptLen  = 64 << 10
	maxStartupLen = 4096
)

var (
	slugRe     = lazyre.New(`^[a-z0-9][a-z0-9-]{1,63}$`)
	envNameRe  = lazyre.New(`^[A-Z][A-Z0-9_]{0,63}$`)
	imageRe    = lazyre.New(`^[a-z0-9]+([._/-][a-z0-9]+)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	templateRe = lazyre.New(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
	labelRe    = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9 ._+()-]{0,63}$`)
)

// Runtimes a blueprint may record for compatibility with the bots table.
var allowedRuntimes = map[string]bool{"java": true, "nodejs": true, "python": true, "go": true, "rust": true, "ruby": true}

// DefaultRuntime is recorded for blueprints that do not name a runtime (for
// example native SteamCMD servers). It is only a compatibility label for the
// bots table: game servers take their images, install and startup from the
// blueprint, never from the runtime catalog.
const DefaultRuntime = "go"

// MaxExtraPorts bounds ports.extra and the SERVER_PORT_<n> variables.
const MaxExtraPorts = 10

var extraPortVarRe = lazyre.New(`^SERVER_PORT_([1-9]|10)$`)

// IsSystemVariable reports whether the panel provides name at run time:
// a SystemVariables entry or SERVER_PORT_1 … SERVER_PORT_10 (the ports of
// the additional allocations, in ascending port order).
func IsSystemVariable(name string) bool {
	if _, ok := SystemVariables[name]; ok {
		return true
	}
	return extraPortVarRe.MatchString(name)
}

// Stop signals a blueprint may send instead of a console command.
var stopSignals = map[string]bool{"SIGINT": true, "SIGTERM": true, "SIGQUIT": true, "SIGHUP": true}

// SteamCMD defaults and bounds.
const (
	DefaultSteamCMDImage   = "steamcmd/steamcmd:latest"
	defaultSteamTimeoutMin = 60
	defaultSteamMaxSizeGB  = 40
)

var betaRe = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidBeta reports whether a rendered beta branch name is safe to pass to
// SteamCMD ("" = the default public branch).
func ValidBeta(b string) bool { return b == "" || betaRe.MatchString(b) }

// SystemVariables are provided by the panel at run time and cannot be
// declared by a blueprint.
var SystemVariables = map[string]string{
	"SERVER_MEMORY": "memory available to the server process, in MiB",
	"SERVER_IP":     "address the server should bind (0.0.0.0)",
	"SERVER_PORT":   "the primary allocation's port",
	"SERVER_ID":     "the server's UUID",
	JVMArgsVar:      "the server's extra JVM options (blueprints with startup.jvm_args)",
}

// Spec is one blueprint revision.
type Spec struct {
	Slug        string       `yaml:"slug" json:"slug"`
	Name        string       `yaml:"name" json:"name"`
	Category    string       `yaml:"category" json:"category"`
	Description string       `yaml:"description" json:"description"`
	Runtime     string       `yaml:"runtime" json:"runtime"`
	Images      []Image      `yaml:"images" json:"images"`
	JavaFrom    string       `yaml:"java_from" json:"java_from,omitempty"` // variable holding a Minecraft version, for automatic Java selection
	Startup     Startup      `yaml:"startup" json:"startup"`
	Variables   []Variable   `yaml:"variables" json:"variables"`
	Install     Install      `yaml:"install" json:"install"`
	ConfigFiles []ConfigFile `yaml:"config_files" json:"config_files"`
	Agreements  []Agreement  `yaml:"agreements" json:"agreements"`
	Resources   Resources    `yaml:"resources" json:"resources"`
	Ports       Ports        `yaml:"ports" json:"ports"`
	Query       string       `yaml:"query" json:"query,omitempty"` // "" | minecraft-java | steam
	// QueryAllocation picks the allocation queried: 0 = the primary one,
	// n = the n-th additional allocation (SERVER_PORT_<n>).
	QueryAllocation int `yaml:"query_allocation" json:"query_allocation,omitempty"`
	// Features turn on game-specific pages: minecraft-properties (a
	// server.properties editor) and minecraft-players (player management).
	Features []string `yaml:"features" json:"features"`
	// Addons, when set, offers a mod or plugin browser for the server.
	Addons *AddonSource `yaml:"addons" json:"addons,omitempty"`
}

// AddonSource describes where mods or plugins come from and where they go.
type AddonSource struct {
	Source  string   `yaml:"source" json:"source"` // modrinth
	Kind    string   `yaml:"kind" json:"kind"`     // plugin | mod
	Loaders []string `yaml:"loaders" json:"loaders"`
	Dir     string   `yaml:"dir" json:"dir"`
}

var knownFeatures = map[string]bool{"minecraft-properties": true, "minecraft-players": true}

// HasFeature reports whether the blueprint enables a feature.
func (s Spec) HasFeature(f string) bool {
	for _, x := range s.Features {
		if x == f {
			return true
		}
	}
	return false
}

// Image is one selectable container image.
type Image struct {
	Label string `yaml:"label" json:"label"`
	Ref   string `yaml:"ref" json:"ref"`
	Java  int    `yaml:"java" json:"java,omitempty"` // Java major version the image provides
}

// Startup describes how the server process runs and stops.
type Startup struct {
	// Command runs with /bin/sh inside the container. {{VAR}} expands to the
	// variable's value (shell expansion of an environment variable, so values
	// are never re-parsed as shell code).
	Command string `yaml:"command" json:"command"`
	// Stop is written to the console to stop gracefully ("" = SIGTERM only).
	Stop string `yaml:"stop" json:"stop,omitempty"`
	// StopTimeoutSeconds bounds the graceful stop before SIGKILL.
	StopTimeoutSeconds int `yaml:"stop_timeout_seconds" json:"stop_timeout_seconds,omitempty"`
	// Done is a console substring that means the server finished starting.
	Done string `yaml:"done" json:"done,omitempty"`
	// StopSignal is sent to the server process to stop gracefully instead of
	// a console command (SIGINT, SIGTERM, SIGQUIT or SIGHUP). Stop and
	// StopSignal are mutually exclusive.
	StopSignal string `yaml:"stop_signal" json:"stop_signal,omitempty"`
	// JVMArgs offers a per-server "JVM arguments" setting. The command must
	// then contain {{SERVER_JVM_ARGS}} unquoted (right after `java`); the
	// panel validates the options strictly before they reach the shell.
	JVMArgs bool `yaml:"jvm_args" json:"jvm_args,omitempty"`
	// JVMArgsDefault are the JVM options new servers (and servers updated to
	// this revision that never set their own) start with.
	JVMArgsDefault string `yaml:"jvm_args_default" json:"jvm_args_default,omitempty"`
}

// Variable is one server setting exposed as an environment variable.
type Variable struct {
	Env         string    `yaml:"env" json:"env"`
	Name        string    `yaml:"name" json:"name"`
	Description string    `yaml:"description" json:"description,omitempty"`
	Default     string    `yaml:"default" json:"default"`
	Editable    bool      `yaml:"editable" json:"editable"`
	Reinstall   bool      `yaml:"reinstall" json:"reinstall,omitempty"` // changing it reinstalls the server
	Rules       Rules     `yaml:"rules" json:"rules"`
	Versions    *Provider `yaml:"versions" json:"versions,omitempty"` // offer a version list from a provider
}

// Rules validate a variable's value. Empty rules accept up to 256 characters.
type Rules struct {
	Type    string   `yaml:"type" json:"type,omitempty"` // string (default) | int | bool | enum
	Pattern string   `yaml:"pattern" json:"pattern,omitempty"`
	Min     *int64   `yaml:"min" json:"min,omitempty"`
	Max     *int64   `yaml:"max" json:"max,omitempty"`
	Options []string `yaml:"options" json:"options,omitempty"`
	MaxLen  int      `yaml:"max_len" json:"max_len,omitempty"`
}

// Provider names a first-party download provider.
type Provider struct {
	Name    string `yaml:"provider" json:"provider"`
	Project string `yaml:"project" json:"project,omitempty"`
}

// Install is run once before the first start, and again on reinstall.
type Install struct {
	// Download fetches the server software with a first-party provider; it
	// runs in the panel (or agent) before Script, with checksums verified.
	Download *Download `yaml:"download" json:"download,omitempty"`
	// SteamCMD downloads or updates a Steam app anonymously into the
	// server's files, in a SteamCMD container, before Script runs.
	SteamCMD *SteamCMD `yaml:"steamcmd" json:"steamcmd,omitempty"`
	// Image and Script run in a container as the unprivileged server user
	// with network access and the server's files at /workspace.
	Image  string `yaml:"image" json:"image,omitempty"` // "" = the server's runtime image
	Script string `yaml:"script" json:"script,omitempty"`
}

// SteamCMD installs a Steam app with an anonymous login. No Steam account
// credentials are ever stored or accepted.
type SteamCMD struct {
	AppID int64 `yaml:"app_id" json:"app_id"`
	// Beta is a branch name template ("" or a value rendering to "" = the
	// default public branch). Password-protected branches are not supported.
	Beta string `yaml:"beta" json:"beta,omitempty"`
	// Validate verifies every installed file (slower; repairs damage).
	Validate bool `yaml:"validate" json:"validate,omitempty"`
	// Image runs SteamCMD ("" = DefaultSteamCMDImage).
	Image string `yaml:"image" json:"image,omitempty"`
	// TimeoutMinutes bounds the download (default 60, at most 240).
	TimeoutMinutes int `yaml:"timeout_minutes" json:"timeout_minutes,omitempty"`
	// MaxSizeGB stops the download when the server's files grow beyond it
	// (default 40, at most 500). It is a watchdog, not a disk quota.
	MaxSizeGB int `yaml:"max_size_gb" json:"max_size_gb,omitempty"`
}

// Download is one provider download.
type Download struct {
	Provider `yaml:",inline"`
	Version  string `yaml:"version" json:"version"` // template, e.g. {{MINECRAFT_VERSION}}
	Build    string `yaml:"build" json:"build,omitempty"`
	Dest     string `yaml:"dest" json:"dest"` // template, workspace-relative
}

// ConfigFile edits a file in the workspace before every start.
type ConfigFile struct {
	Path   string `yaml:"path" json:"path"`
	Format string `yaml:"format" json:"format"` // properties | ini | lines
	// Section is the INI section the keys belong to (format ini; "" = the
	// keys before the first section header). Use one entry per section.
	Section string            `yaml:"section" json:"section,omitempty"`
	Set     map[string]string `yaml:"set" json:"set,omitempty"`
	Replace []LineReplace     `yaml:"replace" json:"replace,omitempty"`
	Create  bool              `yaml:"create" json:"create,omitempty"` // create the file when missing
}

// LineReplace replaces lines matching a regular expression (format lines).
type LineReplace struct {
	Match string `yaml:"match" json:"match"`
	With  string `yaml:"with" json:"with"`
}

// Agreement must be accepted when the server is created, and then writes a file.
type Agreement struct {
	ID      string `yaml:"id" json:"id"`
	Text    string `yaml:"text" json:"text"`
	URL     string `yaml:"url" json:"url,omitempty"`
	File    string `yaml:"file" json:"file"`
	Content string `yaml:"content" json:"content"`
}

// Resources are creation defaults and minimums.
type Resources struct {
	MemoryMB    int64   `yaml:"memory_mb" json:"memory_mb"`
	MinMemoryMB int64   `yaml:"min_memory_mb" json:"min_memory_mb"`
	CPUs        float64 `yaml:"cpus" json:"cpus"`
	Pids        int64   `yaml:"pids" json:"pids"`
	// HeapPercent is the share of the memory limit passed as SERVER_MEMORY
	// (the rest is left for the JVM, threads and native memory).
	HeapPercent int `yaml:"heap_percent" json:"heap_percent,omitempty"`
}

// Ports configure allocations.
type Ports struct {
	Default int `yaml:"default" json:"default,omitempty"` // preferred first port when allocating automatically
	Extra   int `yaml:"extra" json:"extra,omitempty"`     // additional allocations created with the server
	// Container, when set, is the port the server listens on inside its
	// container; the primary allocation is published to it and SERVER_PORT is
	// this value. Otherwise the allocation's port is used on both sides.
	Container int `yaml:"container" json:"container,omitempty"`
	// Contiguous asks automatic allocation for consecutive ports (primary,
	// primary+1, …), for games that derive a query port from the game port.
	// Ports chosen later by people are not forced to be consecutive.
	Contiguous bool `yaml:"contiguous" json:"contiguous,omitempty"`
}

// Parse decodes and validates one blueprint revision.
func Parse(b []byte) (Spec, error) {
	var s Spec
	if len(b) == 0 || len(b) > MaxSpecBytes {
		return s, fmt.Errorf("blueprint must be 1 to %d bytes", MaxSpecBytes)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("blueprint YAML: %w", err)
	}
	s.normalize()
	return s, s.Validate()
}

// Hash is the SHA-256 of a revision's YAML.
func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Spec) normalize() {
	if s.Resources.HeapPercent == 0 {
		s.Resources.HeapPercent = 85
	}
	if s.Startup.StopTimeoutSeconds == 0 {
		s.Startup.StopTimeoutSeconds = 60
	}
	if s.Resources.Pids == 0 {
		s.Resources.Pids = 1024
	}
	if s.Runtime == "" {
		s.Runtime = DefaultRuntime
	}
	if st := s.Install.SteamCMD; st != nil {
		if st.Image == "" {
			st.Image = DefaultSteamCMDImage
		}
		if st.TimeoutMinutes == 0 {
			st.TimeoutMinutes = defaultSteamTimeoutMin
		}
		if st.MaxSizeGB == 0 {
			st.MaxSizeGB = defaultSteamMaxSizeGB
		}
	}
	for i := range s.Variables {
		if s.Variables[i].Rules.Type == "" {
			s.Variables[i].Rules.Type = "string"
		}
	}
}

// Validate checks every field. It never contacts the network.
func (s Spec) Validate() error {
	switch {
	case !slugRe.MatchString(s.Slug):
		return fmt.Errorf("slug must be 2-64 lowercase letters, digits or dashes")
	case strings.TrimSpace(s.Name) == "" || len(s.Name) > 80:
		return fmt.Errorf("name must be 1-80 characters")
	case strings.TrimSpace(s.Category) == "" || len(s.Category) > 40:
		return fmt.Errorf("category must be 1-40 characters")
	case len(s.Description) > 1000:
		return fmt.Errorf("description must be at most 1000 characters")
	case !allowedRuntimes[s.Runtime]:
		return fmt.Errorf("runtime must be one of java, nodejs, python, go, rust, ruby")
	case len(s.Images) == 0 || len(s.Images) > maxImages:
		return fmt.Errorf("between 1 and %d images are required", maxImages)
	case strings.TrimSpace(s.Startup.Command) == "" || len(s.Startup.Command) > maxStartupLen:
		return fmt.Errorf("startup.command must be 1-%d characters", maxStartupLen)
	case strings.ContainsAny(s.Startup.Command, "\x00\n\r"):
		return fmt.Errorf("startup.command must be a single line")
	case strings.ContainsAny(s.Startup.Stop, "\x00\n\r") || len(s.Startup.Stop) > 128:
		return fmt.Errorf("startup.stop must be one line of at most 128 characters")
	case s.Startup.StopSignal != "" && !stopSignals[s.Startup.StopSignal]:
		return fmt.Errorf("startup.stop_signal must be SIGINT, SIGTERM, SIGQUIT or SIGHUP")
	case s.Startup.StopSignal != "" && s.Startup.Stop != "":
		return fmt.Errorf("startup.stop and startup.stop_signal cannot both be set")
	case s.Startup.StopTimeoutSeconds < 1 || s.Startup.StopTimeoutSeconds > 600:
		return fmt.Errorf("startup.stop_timeout_seconds must be 1-600")
	case len(s.Startup.Done) > 200:
		return fmt.Errorf("startup.done must be at most 200 characters")
	case len(s.Variables) > maxVariables:
		return fmt.Errorf("at most %d variables", maxVariables)
	case len(s.ConfigFiles) > maxConfigs:
		return fmt.Errorf("at most %d config files", maxConfigs)
	case s.Resources.MemoryMB < 64 || s.Resources.MemoryMB > 1<<20:
		return fmt.Errorf("resources.memory_mb must be 64 MiB or more")
	case s.Resources.MinMemoryMB < 0 || s.Resources.MinMemoryMB > s.Resources.MemoryMB:
		return fmt.Errorf("resources.min_memory_mb must not exceed memory_mb")
	case s.Resources.CPUs <= 0 || s.Resources.CPUs > 256:
		return fmt.Errorf("resources.cpus must be above 0")
	case s.Resources.Pids < 16 || s.Resources.Pids > 4096:
		return fmt.Errorf("resources.pids must be 16-4096")
	case s.Resources.HeapPercent < 10 || s.Resources.HeapPercent > 100:
		return fmt.Errorf("resources.heap_percent must be 10-100")
	case s.Ports.Default != 0 && (s.Ports.Default < 1024 || s.Ports.Default > 65535):
		return fmt.Errorf("ports.default must be 1024-65535")
	case s.Ports.Container != 0 && (s.Ports.Container < 1 || s.Ports.Container > 65535):
		return fmt.Errorf("ports.container must be 1-65535")
	case s.Ports.Extra < 0 || s.Ports.Extra > MaxExtraPorts:
		return fmt.Errorf("ports.extra must be 0-%d", MaxExtraPorts)
	case s.Query != "" && s.Query != "minecraft-java" && s.Query != "steam":
		return fmt.Errorf("query must be empty, minecraft-java or steam")
	case s.QueryAllocation < 0 || s.QueryAllocation > s.Ports.Extra:
		return fmt.Errorf("query_allocation must be 0 (primary) or the number of an additional allocation (at most ports.extra)")
	}
	labels := map[string]bool{}
	for _, im := range s.Images {
		if !labelRe.MatchString(im.Label) || labels[im.Label] {
			return fmt.Errorf("image labels must be unique, 1-64 plain characters")
		}
		labels[im.Label] = true
		if !imageRe.MatchString(im.Ref) {
			return fmt.Errorf("invalid image reference %q", im.Ref)
		}
		if im.Java < 0 || im.Java > 99 {
			return fmt.Errorf("image %q: java must be 0-99", im.Label)
		}
	}
	vars := map[string]bool{}
	for _, v := range s.Variables {
		if err := v.validate(); err != nil {
			return err
		}
		if vars[v.Env] {
			return fmt.Errorf("variable %s is declared twice", v.Env)
		}
		vars[v.Env] = true
	}
	known := func(name string) bool { return IsSystemVariable(name) || vars[name] }
	for _, tpl := range []string{s.Startup.Command} {
		if err := checkRefs(tpl, known); err != nil {
			return fmt.Errorf("startup.command: %w", err)
		}
	}
	usesJVMArgs := strings.Contains(StartupCommand(s.Startup.Command), "${"+JVMArgsVar+"}")
	switch {
	case s.Startup.JVMArgs && !usesJVMArgs:
		return fmt.Errorf("startup.jvm_args needs {{%s}} in startup.command", JVMArgsVar)
	case !s.Startup.JVMArgs && usesJVMArgs:
		return fmt.Errorf("startup.command uses {{%s}}; set startup.jvm_args: true", JVMArgsVar)
	case !s.Startup.JVMArgs && s.Startup.JVMArgsDefault != "":
		return fmt.Errorf("startup.jvm_args_default needs startup.jvm_args: true")
	}
	if norm, err := CheckJVMArgs(s.Startup.JVMArgsDefault); err != nil {
		return fmt.Errorf("startup.jvm_args_default: %w", err)
	} else if norm != s.Startup.JVMArgsDefault {
		return fmt.Errorf("startup.jvm_args_default must be options separated by single spaces")
	}
	if s.JavaFrom != "" && !vars[s.JavaFrom] {
		return fmt.Errorf("java_from names an undeclared variable")
	}
	if d := s.Install.Download; d != nil {
		if _, ok := providers[d.Name]; !ok {
			return fmt.Errorf("install.download: unknown provider %q", d.Name)
		}
		for _, t := range []string{d.Version, d.Build, d.Dest} {
			if err := checkRefs(t, known); err != nil {
				return fmt.Errorf("install.download: %w", err)
			}
		}
		if d.Dest == "" || d.Version == "" {
			return fmt.Errorf("install.download needs version and dest")
		}
	}
	if s.Install.Image != "" && !imageRe.MatchString(s.Install.Image) {
		return fmt.Errorf("invalid install image %q", s.Install.Image)
	}
	if len(s.Install.Script) > maxScriptLen || strings.ContainsRune(s.Install.Script, 0) {
		return fmt.Errorf("install.script must be at most %d bytes", maxScriptLen)
	}
	if st := s.Install.SteamCMD; st != nil {
		switch {
		case st.AppID < 1 || st.AppID > 1<<32-1:
			return fmt.Errorf("install.steamcmd.app_id must be a Steam app id (1-4294967295)")
		case len(st.Beta) > 200 || strings.ContainsAny(st.Beta, "\x00\n\r"):
			return fmt.Errorf("install.steamcmd.beta must be one short line")
		case !imageRe.MatchString(st.Image):
			return fmt.Errorf("invalid install.steamcmd image %q", st.Image)
		case st.TimeoutMinutes < 5 || st.TimeoutMinutes > 240:
			return fmt.Errorf("install.steamcmd.timeout_minutes must be 5-240")
		case st.MaxSizeGB < 1 || st.MaxSizeGB > 500:
			return fmt.Errorf("install.steamcmd.max_size_gb must be 1-500")
		}
		if err := checkRefs(st.Beta, known); err != nil {
			return fmt.Errorf("install.steamcmd.beta: %w", err)
		}
		if !strings.Contains(st.Beta, "{{") && !ValidBeta(st.Beta) {
			return fmt.Errorf("install.steamcmd.beta must be a branch name of letters, digits, dots, dashes or underscores")
		}
	}
	if s.Install.Download == nil && s.Install.Script == "" && s.Install.SteamCMD == nil {
		return fmt.Errorf("install needs a download, SteamCMD, a script, or a combination")
	}
	for _, c := range s.ConfigFiles {
		if err := c.validate(known); err != nil {
			return err
		}
	}
	for _, f := range s.Features {
		if !knownFeatures[f] {
			return fmt.Errorf("unknown feature %q", f)
		}
	}
	if a := s.Addons; a != nil {
		if a.Source != "modrinth" || (a.Kind != "plugin" && a.Kind != "mod") || len(a.Loaders) == 0 || len(a.Loaders) > 8 {
			return fmt.Errorf("addons needs source modrinth, kind plugin or mod, and 1-8 loaders")
		}
		for _, l := range a.Loaders {
			if !regexp.MustCompile(`^[a-z0-9_-]{1,32}$`).MatchString(l) {
				return fmt.Errorf("addons: invalid loader %q", l)
			}
		}
		if err := checkPath(a.Dir); err != nil {
			return fmt.Errorf("addons: %w", err)
		}
	}
	ids := map[string]bool{}
	for _, a := range s.Agreements {
		if !slugRe.MatchString(a.ID) || ids[a.ID] {
			return fmt.Errorf("agreement ids must be unique slugs")
		}
		ids[a.ID] = true
		if strings.TrimSpace(a.Text) == "" || len(a.Text) > 300 || len(a.URL) > 300 || len(a.Content) > 4096 {
			return fmt.Errorf("agreement %s: text 1-300 characters, content at most 4096", a.ID)
		}
		if a.URL != "" && !strings.HasPrefix(a.URL, "https://") {
			return fmt.Errorf("agreement %s: url must be https", a.ID)
		}
		if err := checkPath(a.File); err != nil {
			return fmt.Errorf("agreement %s: %w", a.ID, err)
		}
	}
	return nil
}

func (v Variable) validate() error {
	if !envNameRe.MatchString(v.Env) {
		return fmt.Errorf("variable %q: env must be UPPER_SNAKE_CASE", v.Env)
	}
	if IsSystemVariable(v.Env) || strings.HasPrefix(v.Env, "RIVET_") {
		return fmt.Errorf("variable %s is reserved", v.Env)
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 80 || len(v.Description) > 500 {
		return fmt.Errorf("variable %s: name 1-80 characters, description at most 500", v.Env)
	}
	switch v.Rules.Type {
	case "string", "int", "bool":
	case "enum":
		if len(v.Rules.Options) == 0 || len(v.Rules.Options) > 100 {
			return fmt.Errorf("variable %s: enum needs 1-100 options", v.Env)
		}
	default:
		return fmt.Errorf("variable %s: unknown rule type %q", v.Env, v.Rules.Type)
	}
	if v.Rules.Pattern != "" {
		if _, err := regexp.Compile(v.Rules.Pattern); err != nil {
			return fmt.Errorf("variable %s: invalid pattern", v.Env)
		}
	}
	if v.Versions != nil {
		if _, ok := providers[v.Versions.Name]; !ok {
			return fmt.Errorf("variable %s: unknown version provider %q", v.Env, v.Versions.Name)
		}
	}
	if err := v.Check(v.Default); err != nil {
		return fmt.Errorf("variable %s: default value: %w", v.Env, err)
	}
	return nil
}

// Check validates a value against the variable's rules.
func (v Variable) Check(val string) error {
	maxLen := v.Rules.MaxLen
	if maxLen <= 0 || maxLen > 4096 {
		maxLen = 256
	}
	if len(val) > maxLen || strings.ContainsAny(val, "\x00\n\r") {
		return fmt.Errorf("must be one line of at most %d characters", maxLen)
	}
	switch v.Rules.Type {
	case "int":
		var n int64
		if _, err := fmt.Sscan(val, &n); err != nil || fmt.Sprint(n) != val {
			return fmt.Errorf("must be a whole number")
		}
		if v.Rules.Min != nil && n < *v.Rules.Min {
			return fmt.Errorf("must be at least %d", *v.Rules.Min)
		}
		if v.Rules.Max != nil && n > *v.Rules.Max {
			return fmt.Errorf("must be at most %d", *v.Rules.Max)
		}
	case "bool":
		if val != "true" && val != "false" {
			return fmt.Errorf("must be true or false")
		}
	case "enum":
		for _, o := range v.Rules.Options {
			if o == val {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(v.Rules.Options, ", "))
	}
	if v.Rules.Pattern != "" && !regexp.MustCompile(v.Rules.Pattern).MatchString(val) {
		return fmt.Errorf("has an invalid format")
	}
	return nil
}

func (c ConfigFile) validate(known func(string) bool) error {
	if err := checkPath(c.Path); err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	if c.Section != "" && c.Format != "ini" {
		return fmt.Errorf("config file %s: section is only for format ini", c.Path)
	}
	switch c.Format {
	case "ini":
		if len(c.Replace) > 0 {
			return fmt.Errorf("config file %s: replace is only for format lines", c.Path)
		}
		if len(c.Section) > 128 || strings.ContainsAny(c.Section, "[]\n\r\x00") {
			return fmt.Errorf("config file %s: invalid section %q", c.Path, c.Section)
		}
		for k, v := range c.Set {
			if strings.TrimSpace(k) != k || k == "" || strings.ContainsAny(k, "=[;#\n\r\x00") || len(k) > 128 {
				return fmt.Errorf("config file %s: invalid key %q", c.Path, k)
			}
			if err := checkRefs(v, known); err != nil {
				return fmt.Errorf("config file %s: %w", c.Path, err)
			}
		}
	case "properties":
		if len(c.Replace) > 0 {
			return fmt.Errorf("config file %s: replace is only for format lines", c.Path)
		}
		for k, v := range c.Set {
			if k == "" || strings.ContainsAny(k, "=:\n\r ") || len(k) > 128 {
				return fmt.Errorf("config file %s: invalid key %q", c.Path, k)
			}
			if err := checkRefs(v, known); err != nil {
				return fmt.Errorf("config file %s: %w", c.Path, err)
			}
		}
	case "lines":
		if len(c.Set) > 0 {
			return fmt.Errorf("config file %s: set is only for format properties", c.Path)
		}
		for _, r := range c.Replace {
			if _, err := regexp.Compile(r.Match); err != nil || r.Match == "" {
				return fmt.Errorf("config file %s: invalid match", c.Path)
			}
			if strings.ContainsAny(r.With, "\n\r") {
				return fmt.Errorf("config file %s: replacement must be one line", c.Path)
			}
			if err := checkRefs(r.With, known); err != nil {
				return fmt.Errorf("config file %s: %w", c.Path, err)
			}
		}
	default:
		return fmt.Errorf("config file %s: format must be properties, ini or lines", c.Path)
	}
	return nil
}

func checkPath(p string) error {
	if p == "" || len(p) > 200 || strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) || path.Clean(p) != p ||
		p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "{{") {
		return fmt.Errorf("path %q must be relative to the server's files", p)
	}
	return nil
}

func checkRefs(tpl string, known func(string) bool) error {
	for _, m := range templateRe.FindAllStringSubmatch(tpl, -1) {
		if !known(m[1]) {
			return fmt.Errorf("unknown variable {{%s}}", m[1])
		}
	}
	return nil
}

// Variable returns the declared variable named env.
func (s Spec) Variable(env string) (Variable, bool) {
	for _, v := range s.Variables {
		if v.Env == env {
			return v, true
		}
	}
	return Variable{}, false
}

// Image returns the image with the given label.
func (s Spec) Image(label string) (Image, bool) {
	for _, im := range s.Images {
		if im.Label == label {
			return im, true
		}
	}
	return Image{}, false
}

// ImageForJava picks the image with the lowest Java version at or above need,
// or the newest one when none qualifies; need 0 means the first image.
func (s Spec) ImageForJava(need int) Image {
	if need <= 0 {
		return s.Images[0] // unknown requirement: the blueprint's preferred image
	}
	best, newest := -1, 0
	for i, im := range s.Images {
		if im.Java > s.Images[newest].Java {
			newest = i
		}
		if im.Java >= need && (best < 0 || im.Java < s.Images[best].Java) {
			best = i
		}
	}
	if best < 0 {
		return s.Images[newest]
	}
	return s.Images[best]
}

// Defaults returns every declared variable's default value.
func (s Spec) Defaults() map[string]string {
	out := make(map[string]string, len(s.Variables))
	for _, v := range s.Variables {
		out[v.Env] = v.Default
	}
	return out
}
