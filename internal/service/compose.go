package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Compose import turns a Compose file into a stack of container apps. It is
// an import, not an execution of `docker compose`: every service becomes a
// container app under the panel's isolation policy, the options that would
// leave it (privileged mode, added capabilities, host namespaces, devices,
// the Docker socket) are refused, and options the panel cannot honour are
// reported as warnings before anything is created.

// MaxComposeBytes bounds an imported Compose file.
const MaxComposeBytes = 256 << 10

// ComposeImport is a Compose file to preview or import.
type ComposeImport struct {
	Name        string
	WorkspaceID string
	Compose     string
	// Env is an optional .env file used for ${VAR} interpolation.
	Env    string
	DryRun bool
	// Start starts every service once the stack is created.
	Start bool
}

// ComposeNote is one warning or error, for a service ("" = the whole file).
type ComposeNote struct {
	Service string `json:"service,omitempty"`
	Message string `json:"message"`
}

// ComposeServicePlan is what one service becomes.
type ComposeServicePlan struct {
	Service     string          `json:"service"`
	Name        string          `json:"name"`
	Image       string          `json:"image"`
	Command     []string        `json:"command"`
	Entrypoint  []string        `json:"entrypoint"`
	User        string          `json:"user"`
	EnvNames    []string        `json:"env"`
	Ports       []PlanPort      `json:"ports"`
	Volumes     []domain.Volume `json:"volumes"`
	Writable    []string        `json:"writable"`
	DependsOn   []string        `json:"depends_on"`
	MemoryBytes int64           `json:"memory_bytes"`
	NanoCPUs    int64           `json:"nano_cpus"`
	Offline     bool            `json:"network_disabled"`
	Restart     string          `json:"restart_policy"`

	env map[string]string
}

// PlanPort is a published port of a planned service.
type PlanPort struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip"`
}

// ComposePlan is the result of a preview or an import.
type ComposePlan struct {
	Name     string               `json:"name"`
	Services []ComposeServicePlan `json:"services"`
	Warnings []ComposeNote        `json:"warnings"`
	Errors   []ComposeNote        `json:"errors"`
	// MemoryBytes and NanoCPUs are the stack's total limits.
	MemoryBytes int64 `json:"memory_bytes"`
	NanoCPUs    int64 `json:"nano_cpus"`
	// Stack is set once an import created it.
	Stack *StackView `json:"stack,omitempty"`
}

func (p *ComposePlan) warn(svc, f string, a ...any) {
	p.Warnings = append(p.Warnings, ComposeNote{svc, fmt.Sprintf(f, a...)})
}

func (p *ComposePlan) fail(svc, f string, a ...any) {
	p.Errors = append(p.Errors, ComposeNote{svc, fmt.Sprintf(f, a...)})
}

// Keys a service may not use: each would leave the isolation every app gets.
var composeRefused = map[string]string{
	"privileged":          "privileged mode is never allowed",
	"cap_add":             "added capabilities are not allowed; containers run with none",
	"security_opt":        "security options are fixed by the panel",
	"devices":             "host devices cannot be passed to containers",
	"device_cgroup_rules": "host devices cannot be passed to containers",
	"sysctls":             "kernel settings cannot be changed",
	"userns_mode":         "the user namespace is fixed by the panel",
	"cgroup_parent":       "the control group is fixed by the panel",
	"cgroup":              "the control group is fixed by the panel",
	"runtime":             "the container runtime is fixed by the panel",
	"gpus":                "GPUs cannot be passed to containers",
	"isolation":           "the isolation mode is fixed by the panel",
	"extends":             "extends is not supported; paste the merged file (docker compose config)",
	"secrets":             "Compose secrets are not supported; put the values under Variables",
	"configs":             "Compose configs are not supported; put the files in a volume",
	"volumes_from":        "volumes_from is not supported; give each service its own volumes",
	"build":               "building images from a Dockerfile is not supported; push the image to a registry and use image:",
}

// Keys that are accepted but have no effect here, with why.
var composeIgnored = map[string]string{
	"container_name": "", "hostname": "", "domainname": "", "labels": "", "logging": "",
	"networks":    "",
	"links":       "",
	"expose":      "",
	"stdin_open":  "",
	"tty":         "",
	"read_only":   "",
	"cap_drop":    "",
	"pull_policy": "",
	"stop_signal": "", "stop_grace_period": "", "oom_kill_disable": "", "oom_score_adj": "",
	"mem_reservation": "", "memswap_limit": "", "cpu_shares": "", "cpu_quota": "", "cpu_period": "",
	"working_dir":    "the image's working directory is used",
	"healthcheck":    "health checks are not imported; the Health tab can probe a published port",
	"extra_hosts":    "extra host names are not supported",
	"dns":            "custom DNS servers are not supported",
	"dns_search":     "custom DNS search domains are not supported",
	"external_links": "external links are not supported",
	"ulimits":        "ulimits are fixed by the panel",
	"init":           "an init process is not added",
	"platform":       "the image's default platform is used",
	"shm_size":       "the shared memory size is Docker's default",
	"pids_limit":     "the process limit is set by the panel",
	"env_file":       "env files are not read; paste their contents into the .env box or add the values under Variables",
	"annotations":    "", "attach": "", "scale": "",
}

// Keys the importer maps onto a container app.
var composeHandled = map[string]bool{"image": true, "command": true, "entrypoint": true, "environment": true, "ports": true,
	"volumes": true, "tmpfs": true, "depends_on": true, "user": true, "restart": true, "mem_limit": true, "cpus": true,
	"deploy": true, "pid": true, "ipc": true, "network_mode": true, "profiles": true}

var composeTopIgnored = map[string]bool{"version": true, "name": true, "services": true, "volumes": true, "networks": true}

// composeInterpolate expands ${VAR}, ${VAR:-default}, ${VAR-default},
// ${VAR:?err}, ${VAR?err} and $VAR from vars; $$ is a literal $. An unset
// variable without a default becomes empty, as in Compose.
var interpRe = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?+])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

func composeInterpolate(s string, vars map[string]string, missing map[string]bool) string {
	if !strings.Contains(s, "$") {
		return s
	}
	return interpRe.ReplaceAllStringFunc(s, func(m string) string {
		if m == "$$" {
			return "$"
		}
		sm := interpRe.FindStringSubmatch(m)
		name, op, arg := sm[1], sm[2], sm[3]
		if name == "" {
			name = sm[4]
		}
		v, set := vars[name]
		switch op {
		case ":-":
			if v == "" {
				return arg
			}
		case "-":
			if !set {
				return arg
			}
		case ":+":
			if v != "" {
				return arg
			}
			return ""
		case "+":
			if set {
				return arg
			}
			return ""
		}
		if !set {
			missing[name] = true
		}
		return v
	})
}

// interpolateNode expands variables in every scalar of a parsed file.
func interpolateNode(n *yaml.Node, vars map[string]string, missing map[string]bool) {
	if n.Kind == yaml.ScalarNode && n.Tag != "!!binary" {
		n.Value = composeInterpolate(n.Value, vars, missing)
	}
	for _, c := range n.Content {
		interpolateNode(c, vars, missing)
	}
}

// parseDotEnv reads KEY=VALUE lines (comments, blank lines and an optional
// "export " prefix allowed; matching quotes are removed).
func parseDotEnv(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

// shellWords splits a command string the way Compose does: on whitespace,
// with single and double quotes and backslash escapes; no shell features.
func shellWords(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in := false
	var quote rune
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc, in = false, true
		case r == '\\' && quote != '\'':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 || esc {
		return nil, errors.New("unterminated quote or escape")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

// parseBytes reads Compose memory sizes: 512m, 1g, 1.5gb, 1024k, 1048576.
func parseBytes(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "b")
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
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("%q is not a size such as 512m or 1g", s)
	}
	return int64(f * mult), nil
}

func scalars(n *yaml.Node) ([]string, bool) {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}, true
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return nil, false
			}
			out = append(out, c.Value)
		}
		return out, true
	}
	return nil, false
}

func mapping(n *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}

func mappingKeys(n *yaml.Node) []string {
	var out []string
	if n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// Writable folders well-known images need with a read-only root filesystem.
var knownWritable = map[string][]string{
	"library/nginx":    {"/var/cache/nginx", "/var/run"},
	"library/postgres": {"/var/run/postgresql"},
	"library/mysql":    {"/var/run/mysqld"},
	"library/mariadb":  {"/run/mysqld"},
	"library/httpd":    {"/usr/local/apache2/logs"},
	"library/mongo":    {"/data/configdb"},
}

var volumeSanitizeRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

func sanitizeVolumeName(s string) string {
	s = volumeSanitizeRe.ReplaceAllString(strings.ToLower(strings.Trim(s, "./")), "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		s = "data"
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// PlanCompose parses a Compose file and works out the stack it becomes,
// without creating anything.
func (c *ContainerService) PlanCompose(ctx context.Context, actor domain.User, in ComposeImport) (ComposePlan, error) {
	plan := ComposePlan{Services: []ComposeServicePlan{}, Warnings: []ComposeNote{}, Errors: []ComposeNote{}}
	if len(in.Compose) > MaxComposeBytes {
		return plan, domain.Invalid("the Compose file is larger than 256 KiB")
	}
	if strings.TrimSpace(in.Compose) == "" {
		return plan, domain.Invalid("paste a Compose file (compose.yaml or docker-compose.yml)")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(in.Compose), &doc); err != nil {
		return plan, domain.Invalid("the Compose file is not valid YAML: " + err.Error())
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return plan, domain.Invalid("the Compose file must be a mapping with a services: section")
	}
	vars := parseDotEnv(in.Env)
	missing := map[string]bool{}
	interpolateNode(&doc, vars, missing)
	root := doc.Content[0]
	top := mapping(root)
	for _, k := range mappingKeys(root) {
		switch {
		case strings.HasPrefix(k, "x-") || composeTopIgnored[k]:
		case k == "include":
			plan.fail("", "include is not supported; paste the merged file (docker compose config)")
		case k == "secrets" || k == "configs":
			plan.fail("", "top-level %s are not supported; use Variables or volumes", k)
		default:
			plan.warn("", "%s is ignored", k)
		}
	}
	plan.Name = strings.TrimSpace(in.Name)
	if plan.Name == "" && top["name"] != nil {
		plan.Name = top["name"].Value
	}
	if plan.Name == "" {
		plan.Name = "stack"
	}
	if _, err := validateName(plan.Name); err != nil {
		return plan, err
	}
	if len(mapping(top["networks"])) > 0 {
		plan.warn("", "networks are ignored: every service of the stack shares one private network and reaches the others by service name")
	}
	declaredVolumes := mapping(top["volumes"])
	for name, v := range declaredVolumes {
		if ext := mapping(v)["external"]; ext != nil && ext.Value == "true" {
			plan.warn("", "volume %s is external; the stack gets its own empty folder instead", name)
		}
	}
	services := top["services"]
	names := mappingKeys(services)
	if len(names) == 0 {
		return plan, domain.Invalid("the Compose file has no services")
	}
	sort.Strings(names)
	svcs := mapping(services)
	active := map[string]bool{}
	for _, name := range names {
		if mapping(svcs[name])["profiles"] != nil {
			plan.warn(name, "skipped: it belongs to a profile, which Compose does not start by default")
			continue
		}
		active[name] = true
	}
	if len(active) > maxStackServices {
		return plan, domain.Invalid(fmt.Sprintf("a stack can have at most %d services", maxStackServices))
	}
	s := c.Bots
	lo, hi := s.Limits.PortMin, s.Limits.PortMax
	if lo == 0 && hi == 0 {
		lo, hi = 20000, 29999
	}
	used, err := c.Store.UsedBotHostPorts(ctx)
	if err != nil {
		return plan, err
	}
	if s.LocalPublished != nil {
		if pub, err := s.LocalPublished(ctx); err == nil {
			for _, p := range pub {
				used[p.HostPort] = true
			}
		}
	}
	nextFree := func() int {
		for p := lo; p <= hi; p++ {
			if !used[p] {
				used[p] = true
				return p
			}
		}
		return 0
	}
	lim := s.LimitsFor(ctx, s.LocalNode)
	volumeUsers := map[string][]string{}
	publicWarned := false
	for _, name := range names {
		if !active[name] {
			continue
		}
		if !serviceNameRe.MatchString(name) {
			plan.fail(name, "service names must start with a letter or digit and use letters, digits, dots, dashes or underscores")
			continue
		}
		svc := svcs[name]
		if svc.Kind != yaml.MappingNode {
			plan.fail(name, "the service must be a mapping")
			continue
		}
		m := mapping(svc)
		sp := ComposeServicePlan{Service: name, Name: plan.Name + "-" + name, Command: []string{}, Entrypoint: []string{},
			EnvNames: []string{}, Ports: []PlanPort{}, Volumes: []domain.Volume{}, Writable: []string{}, DependsOn: []string{},
			Restart: domain.RestartOnFailure, env: map[string]string{}}
		if len([]rune(sp.Name)) > 64 {
			sp.Name = string([]rune(sp.Name)[:64])
		}
		for _, k := range mappingKeys(svc) {
			if why, ok := composeRefused[k]; ok {
				if k == "privileged" && m[k].Value != "true" {
					continue
				}
				plan.fail(name, "%s: %s", k, why)
				continue
			}
			if why, ok := composeIgnored[k]; ok {
				if why != "" {
					plan.warn(name, "%s is ignored: %s", k, why)
				}
				continue
			}
			if !composeHandled[k] && !strings.HasPrefix(k, "x-") {
				plan.warn(name, "%s is not supported and is ignored", k)
			}
		}
		for _, k := range []string{"pid", "ipc", "network_mode"} {
			v := m[k]
			if v == nil {
				continue
			}
			if k == "network_mode" && v.Value == "none" {
				sp.Offline = true
				continue
			}
			if k == "network_mode" && v.Value == "bridge" {
				continue
			}
			plan.fail(name, "%s: %s is not allowed; services share their stack's private network", k, v.Value)
		}
		// Image.
		if m["image"] == nil || strings.TrimSpace(m["image"].Value) == "" {
			if m["build"] == nil {
				plan.fail(name, "the service has no image")
			}
			continue
		}
		img, err := c.Policy.Normalize(m["image"].Value)
		if err != nil {
			plan.fail(name, "%s", err.Error())
			continue
		}
		sp.Image = img
		// Command and entrypoint.
		for key, dst := range map[string]*[]string{"command": &sp.Command, "entrypoint": &sp.Entrypoint} {
			n := m[key]
			if n == nil {
				continue
			}
			if n.Kind == yaml.ScalarNode {
				words, err := shellWords(n.Value)
				if err != nil {
					plan.fail(name, "%s: %v", key, err)
					continue
				}
				*dst = words
			} else if v, ok := scalars(n); ok {
				*dst = v
			} else {
				plan.fail(name, "%s must be a string or a list of strings", key)
			}
		}
		if err := checkContainerCommand(sp.Command, sp.Entrypoint); err != nil {
			plan.fail(name, "%s", err.Error())
		}
		// Environment.
		if env := m["environment"]; env != nil {
			if env.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(env.Content); i += 2 {
					k, v := env.Content[i].Value, env.Content[i+1]
					if v.Tag == "!!null" {
						if val, ok := vars[k]; ok {
							sp.env[k] = val
						} else {
							plan.warn(name, "%s has no value; set it under Variables", k)
						}
						continue
					}
					sp.env[k] = v.Value
				}
			} else if list, ok := scalars(env); ok {
				for _, kv := range list {
					k, v, has := strings.Cut(kv, "=")
					if !has {
						if val, ok := vars[k]; ok {
							sp.env[k] = val
						} else {
							plan.warn(name, "%s has no value; set it under Variables", k)
						}
						continue
					}
					sp.env[k] = v
				}
			} else {
				plan.fail(name, "environment must be a mapping or a list of KEY=VALUE")
			}
		}
		for k, v := range sp.env {
			if err := validateEnvName(k, false); err != nil {
				plan.fail(name, "%s", err.Error())
				continue
			}
			if err := validateEnvValue(v); err != nil {
				plan.fail(name, "%s: %s", k, err.Error())
			}
			sp.EnvNames = append(sp.EnvNames, k)
		}
		sort.Strings(sp.EnvNames)
		// User.
		if u := m["user"]; u != nil {
			val := u.Value
			if !strings.Contains(val, ":") && numericUserRe.MatchString(val+":"+val) {
				val = val + ":" + val
			}
			if cu, err := checkContainerUser(val); err != nil {
				plan.warn(name, "user %s is ignored (%s); it runs as the panel's container user", u.Value, strings.TrimSuffix(err.Error(), "."))
			} else {
				sp.User = cu
			}
		}
		// Restart policy.
		if r := m["restart"]; r != nil && (r.Value == "no" || r.Value == "\"no\"") {
			sp.Restart = domain.RestartNever
		}
		// Resources.
		mem, cpu := int64(512<<20), int64(1e9)
		if v := m["mem_limit"]; v != nil {
			if n, err := parseBytes(v.Value); err == nil {
				mem = n
			} else {
				plan.warn(name, "mem_limit: %v; using 512 MiB", err)
			}
		}
		if v := m["cpus"]; v != nil {
			if f, err := strconv.ParseFloat(v.Value, 64); err == nil && f > 0 {
				cpu = int64(f * 1e9)
			}
		}
		if dep := m["deploy"]; dep != nil {
			dm := mapping(dep)
			if r := dm["replicas"]; r != nil && r.Value != "1" {
				plan.warn(name, "replicas are not supported; one container runs")
			}
			limits := mapping(mapping(dm["resources"])["limits"])
			if v := limits["memory"]; v != nil {
				if n, err := parseBytes(v.Value); err == nil {
					mem = n
				}
			}
			if v := limits["cpus"]; v != nil {
				if f, err := strconv.ParseFloat(v.Value, 64); err == nil && f > 0 {
					cpu = int64(f * 1e9)
				}
			}
		}
		if mem > lim.MaxMemoryBytes {
			plan.warn(name, "memory lowered from %d MiB to this node's maximum of %d MiB", mem>>20, lim.MaxMemoryBytes>>20)
			mem = lim.MaxMemoryBytes
		}
		mem = max(mem, max(lim.MinMemoryBytes, 16<<20))
		cpu = min(max(cpu, lim.MinNanoCPUs), lim.MaxNanoCPUs)
		sp.MemoryBytes, sp.NanoCPUs = mem, cpu
		// Volumes and tmpfs.
		addWritable := func(p string) {
			p, _, _ = strings.Cut(p, ":")
			if cp, err := checkContainerPath(p, "tmpfs path"); err != nil {
				plan.warn(name, "%s", err.Error())
			} else if cp != "/tmp" {
				sp.Writable = append(sp.Writable, cp)
			}
		}
		if t := m["tmpfs"]; t != nil {
			if list, ok := scalars(t); ok {
				for _, p := range list {
					addWritable(p)
				}
			}
		}
		addVolume := func(kind, src, dst string) {
			switch {
			case strings.Contains(src, "docker.sock") || dst == "/var/run/docker.sock" || dst == "/run/docker.sock":
				plan.fail(name, "the Docker socket cannot be mounted: it would control the host")
				return
			case kind == "tmpfs":
				addWritable(dst)
				return
			}
			cp, err := checkContainerPath(path.Clean(dst), "mount target")
			if err != nil {
				plan.fail(name, "%s", err.Error())
				return
			}
			var vname string
			switch {
			case kind == "bind" && strings.HasPrefix(src, "/"):
				plan.warn(name, "host folder %s is not mounted at %s: apps cannot reach the host's files", src, cp)
				return
			case kind == "bind" || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~"):
				vname = sanitizeVolumeName(src)
				plan.warn(name, "%s becomes the volume %s, which starts empty: upload its files under Files → volumes/%s", src, vname, vname)
			case src == "":
				vname = sanitizeVolumeName(cp)
			default:
				vname = sanitizeVolumeName(src)
				volumeUsers[vname] = append(volumeUsers[vname], name)
			}
			sp.Volumes = append(sp.Volumes, domain.Volume{Name: vname, Path: cp})
		}
		if vols := m["volumes"]; vols != nil && vols.Kind == yaml.SequenceNode {
			for _, v := range vols.Content {
				if v.Kind == yaml.MappingNode {
					vm := mapping(v)
					get := func(k string) string {
						if vm[k] != nil {
							return vm[k].Value
						}
						return ""
					}
					kind := get("type")
					if kind == "" {
						kind = "volume"
					}
					if kind == "npipe" || kind == "cluster" {
						plan.warn(name, "%s mounts are not supported", kind)
						continue
					}
					addVolume(kind, get("source"), get("target"))
					continue
				}
				parts := strings.Split(v.Value, ":")
				switch len(parts) {
				case 1:
					addVolume("volume", "", parts[0])
				default:
					src := parts[0]
					kind := "volume"
					if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") {
						kind = "bind"
					}
					addVolume(kind, src, parts[1])
				}
			}
		}
		// Known images that write to their own filesystem.
		if named, err := reference.ParseNormalizedNamed(sp.Image); err == nil {
			for _, p := range knownWritable[reference.Path(named)] {
				covered := false
				for _, v := range sp.Volumes {
					if v.Path == p || strings.HasPrefix(p, v.Path+"/") {
						covered = true
					}
				}
				for _, w := range sp.Writable {
					if w == p {
						covered = true
					}
				}
				if !covered {
					sp.Writable = append(sp.Writable, p)
				}
			}
		}
		if cfg, err := checkContainerConfig(domain.ContainerConfig{User: sp.User, Writable: sp.Writable, Volumes: sp.Volumes}); err != nil {
			plan.fail(name, "%s", err.Error())
		} else {
			sp.Writable, sp.Volumes = cfg.Writable, cfg.Volumes
		}
		// Ports.
		if ports := m["ports"]; ports != nil && ports.Kind == yaml.SequenceNode {
			for _, pn := range ports.Content {
				var target, published int
				proto, ip := "tcp", ""
				if pn.Kind == yaml.MappingNode {
					pm := mapping(pn)
					if pm["target"] != nil {
						target, _ = strconv.Atoi(pm["target"].Value)
					}
					if pm["published"] != nil {
						published, _ = strconv.Atoi(pm["published"].Value)
					}
					if pm["protocol"] != nil {
						proto = pm["protocol"].Value
					}
					if pm["host_ip"] != nil {
						ip = pm["host_ip"].Value
					}
				} else {
					spec := pn.Value
					if i := strings.LastIndex(spec, "/"); i >= 0 {
						proto, spec = spec[i+1:], spec[:i]
					}
					if strings.Contains(spec, "-") || strings.Contains(spec, "[") {
						plan.warn(name, "port %s is skipped: port ranges and IPv6 addresses are not supported", pn.Value)
						continue
					}
					parts := strings.Split(spec, ":")
					var err error
					switch len(parts) {
					case 1:
						target, err = strconv.Atoi(parts[0])
					case 2:
						if published, err = strconv.Atoi(parts[0]); err == nil {
							target, err = strconv.Atoi(parts[1])
						}
					case 3:
						ip = parts[0]
						if parts[1] != "" {
							published, err = strconv.Atoi(parts[1])
						}
						if err == nil {
							target, err = strconv.Atoi(parts[2])
						}
					default:
						err = errors.New("bad port")
					}
					if err != nil {
						plan.warn(name, "port %s is skipped: it is not HOST:CONTAINER", pn.Value)
						continue
					}
				}
				if target < 1 || target > 65535 || (proto != "tcp" && proto != "udp") {
					plan.warn(name, "port %v is skipped: it is not a TCP or UDP port", pn.Value)
					continue
				}
				if published == 0 && pn.Kind == yaml.ScalarNode && !strings.Contains(pn.Value, ":") {
					// Container-only: reachable inside the stack already.
					continue
				}
				host := published
				if host < lo || host > hi || used[host] {
					host = nextFree()
					if host == 0 {
						plan.fail(name, "no free host port is left in %d-%d", lo, hi)
						continue
					}
					if published != 0 {
						plan.warn(name, "port %d is published on host port %d instead (allowed: %d-%d, unused)", published, host, lo, hi)
					}
				} else {
					used[host] = true
				}
				switch {
				case ip == "127.0.0.1":
				case s.Limits.PortPublicBind:
					ip = "0.0.0.0"
				default:
					if ip != "" && ip != "0.0.0.0" {
						plan.warn(name, "host address %s is replaced with 127.0.0.1", ip)
					} else if !publicWarned {
						plan.warn("", "ports are published on 127.0.0.1 only (public binding is off on this panel); put a reverse proxy in front of them")
						publicWarned = true
					}
					ip = "127.0.0.1"
				}
				sp.Ports = append(sp.Ports, PlanPort{ContainerPort: target, HostPort: host, Protocol: proto, HostIP: ip})
			}
		}
		if len(sp.Ports) > 16 {
			plan.fail(name, "at most 16 published ports")
		}
		if sp.Offline && len(sp.Ports) > 0 {
			plan.warn(name, "network_mode: none publishes no ports; its ports are dropped")
			sp.Ports = []PlanPort{}
		}
		// Dependencies.
		if d := m["depends_on"]; d != nil {
			var deps []string
			if d.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(d.Content); i += 2 {
					deps = append(deps, d.Content[i].Value)
					if cond := mapping(d.Content[i+1])["condition"]; cond != nil && cond.Value != "service_started" {
						plan.warn(name, "depends_on %s: %s is treated as service_started (it waits until %s runs)", d.Content[i].Value, cond.Value, d.Content[i].Value)
					}
				}
			} else if list, ok := scalars(d); ok {
				deps = list
			}
			for _, dep := range deps {
				if !active[dep] {
					plan.fail(name, "depends_on names %s, which is not a service of this file (or is in a profile)", dep)
					continue
				}
				sp.DependsOn = append(sp.DependsOn, dep)
			}
		}
		plan.MemoryBytes += sp.MemoryBytes
		plan.NanoCPUs += sp.NanoCPUs
		plan.Services = append(plan.Services, sp)
	}
	for v, users := range volumeUsers {
		if len(users) > 1 {
			plan.warn("", "volume %s is used by %s; each service gets its own copy, they do not share files", v, strings.Join(users, ", "))
		}
	}
	if cyc := dependencyCycle(plan.Services); cyc != "" {
		plan.fail("", "depends_on has a cycle: %s", cyc)
	}
	if len(missing) > 0 {
		var m []string
		for k := range missing {
			m = append(m, k)
		}
		sort.Strings(m)
		plan.warn("", "not set, used as empty: %s (add them to the .env box)", strings.Join(m, ", "))
	}
	wsID, err := s.creatableWorkspace(ctx, actor, in.WorkspaceID)
	if err != nil {
		return plan, err
	}
	if len(plan.Services) > 0 {
		added := domain.WorkspaceUsage{Apps: int64(len(plan.Services)), Bots: int64(len(plan.Services)), Memory: plan.MemoryBytes, CPU: plan.NanoCPUs}
		if err := s.checkWorkspace(ctx, actor, wsID, added); err != nil {
			plan.fail("", "%s", err.Error())
		}
	}
	return plan, nil
}

// dependencyCycle returns a description of a depends_on cycle, or "".
func dependencyCycle(svcs []ComposeServicePlan) string {
	deps := map[string][]string{}
	for _, s := range svcs {
		deps[s.Service] = s.DependsOn
	}
	state := map[string]int{}
	var path []string
	var visit func(string) string
	visit = func(n string) string {
		switch state[n] {
		case 1:
			return strings.Join(append(path, n), " → ")
		case 2:
			return ""
		}
		state[n] = 1
		path = append(path, n)
		for _, d := range deps[n] {
			if c := visit(d); c != "" {
				return c
			}
		}
		path = path[:len(path)-1]
		state[n] = 2
		return ""
	}
	for _, s := range svcs {
		if c := visit(s.Service); c != "" {
			return c
		}
	}
	return ""
}

// ImportCompose creates a stack from a Compose file. With DryRun (or when
// the plan has errors) nothing is created and the plan is returned.
func (c *ContainerService) ImportCompose(ctx context.Context, actor domain.User, in ComposeImport) (ComposePlan, error) {
	if !actor.Can(domain.PermBotsCreate) {
		return ComposePlan{}, domain.ErrForbidden
	}
	plan, err := c.PlanCompose(ctx, actor, in)
	if err != nil || in.DryRun || len(plan.Errors) > 0 {
		return plan, err
	}
	s := c.Bots
	wsID, err := s.creatableWorkspace(ctx, actor, in.WorkspaceID)
	if err != nil {
		return plan, err
	}
	now := s.now()
	st := domain.Stack{ID: uuid.NewString(), WorkspaceID: wsID, OwnerID: actor.ID, Name: plan.Name, ComposeYAML: in.Compose,
		CreatedAtMS: now, UpdatedAtMS: now}
	if err := c.Store.CreateStack(ctx, st); err != nil {
		return plan, err
	}
	var created []domain.Bot
	undo := func() {
		for _, b := range created {
			s.discardNew(ctx, b)
		}
		_ = c.Store.DeleteStack(context.WithoutCancel(ctx), st.ID)
	}
	yes := true
	for _, sp := range plan.Services {
		ports := make([]PortInput, 0, len(sp.Ports))
		for _, p := range sp.Ports {
			ports = append(ports, PortInput{ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol, HostIP: p.HostIP})
		}
		b, err := c.create(ctx, actor, ContainerInput{Name: sp.Name, WorkspaceID: wsID, Image: sp.Image, Command: sp.Command,
			Entrypoint: sp.Entrypoint, User: sp.User, Writable: sp.Writable, Volumes: sp.Volumes, Ports: ports, Env: sp.env,
			MemoryBytes: sp.MemoryBytes, NanoCPUs: sp.NanoCPUs, NetworkDisabled: sp.Offline, RestartNever: sp.Restart == domain.RestartNever},
			domain.ContainerConfig{StackID: st.ID, Service: sp.Service, DependsOn: sp.DependsOn}, &yes)
		if err != nil {
			undo()
			return plan, fmt.Errorf("service %s: %w", sp.Service, err)
		}
		created = append(created, b)
	}
	if in.Start {
		for _, b := range created {
			_, _ = s.Start(ctx, actor, b.ID)
		}
	}
	v, err := c.GetStack(ctx, actor, st.ID)
	if err != nil {
		return plan, err
	}
	plan.Stack = &v
	return plan, nil
}

// StackView is a stack with the services the actor can see.
type StackView struct {
	domain.Stack
	Services []domain.Bot
	// Role is the actor's workspace role.
	Role string
}

// ListStacks returns the stacks in workspaces the actor belongs to.
func (c *ContainerService) ListStacks(ctx context.Context, actor domain.User) ([]StackView, error) {
	all, err := c.Store.ListStacks(ctx)
	if err != nil {
		return nil, err
	}
	out := []StackView{}
	for _, st := range all {
		v, err := c.view(ctx, actor, st)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *ContainerService) view(ctx context.Context, actor domain.User, st domain.Stack) (StackView, error) {
	role, err := c.Bots.workspaceRole(ctx, actor, st.WorkspaceID)
	if err != nil {
		return StackView{}, err
	}
	ids, err := c.Store.StackMembers(ctx, st.ID)
	if err != nil {
		return StackView{}, err
	}
	v := StackView{Stack: st, Services: []domain.Bot{}, Role: role}
	v.ComposeYAML = ""
	for _, id := range ids {
		if b, err := c.Bots.load(ctx, actor, id, false); err == nil {
			v.Services = append(v.Services, b)
		}
	}
	return v, nil
}

// GetStack returns one stack, its Compose file included.
func (c *ContainerService) GetStack(ctx context.Context, actor domain.User, id string) (StackView, error) {
	st, err := c.Store.GetStack(ctx, id)
	if err != nil {
		return StackView{}, err
	}
	v, err := c.view(ctx, actor, st)
	if err != nil {
		return v, err
	}
	v.ComposeYAML = st.ComposeYAML
	return v, nil
}

// StackPower starts, stops or restarts every service the actor may power.
// Services start in any order; each waits for the services it depends on.
func (c *ContainerService) StackPower(ctx context.Context, actor domain.User, id, action string) (StackView, error) {
	v, err := c.GetStack(ctx, actor, id)
	if err != nil {
		return v, err
	}
	var errs []string
	for _, b := range v.Services {
		var err error
		switch action {
		case "start":
			_, err = c.Bots.Start(ctx, actor, b.ID)
		case "stop":
			_, err = c.Bots.Stop(ctx, actor, b.ID)
		case "restart":
			_, err = c.Bots.Restart(ctx, actor, b.ID)
		default:
			return v, domain.Invalid("action must be start, stop or restart")
		}
		if err != nil {
			errs = append(errs, b.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return v, domain.Invalid(strings.Join(errs, "; "))
	}
	return c.GetStack(ctx, actor, id)
}

// RenameStack renames a stack (workspace developers and above).
func (c *ContainerService) RenameStack(ctx context.Context, actor domain.User, id, name string) (StackView, error) {
	st, err := c.Store.GetStack(ctx, id)
	if err != nil {
		return StackView{}, err
	}
	if _, err := c.Bots.requireWorkspaceRole(ctx, actor, st.WorkspaceID, domain.WorkspaceDeveloper); err != nil {
		return StackView{}, err
	}
	if name, err = validateName(name); err != nil {
		return StackView{}, err
	}
	if err := c.Store.RenameStack(ctx, id, name, c.Bots.now()); err != nil {
		return StackView{}, err
	}
	return c.GetStack(ctx, actor, id)
}

// DeleteStack deletes every service of a stack, then the stack. Every
// service must be deletable by the actor.
func (c *ContainerService) DeleteStack(ctx context.Context, actor domain.User, id string) error {
	st, err := c.Store.GetStack(ctx, id)
	if err != nil {
		return err
	}
	if _, err := c.Bots.requireWorkspaceRole(ctx, actor, st.WorkspaceID, domain.WorkspaceAdmin); err != nil {
		return err
	}
	ids, err := c.Store.StackMembers(ctx, id)
	if err != nil {
		return err
	}
	for _, bid := range ids {
		if err := c.Bots.Delete(ctx, actor, bid); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return c.Store.DeleteStack(ctx, id)
}
