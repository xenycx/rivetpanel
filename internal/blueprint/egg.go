package blueprint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Pterodactyl egg import. An egg is untrusted input: it is size-limited,
// decoded as exactly one JSON value, nothing it references is fetched, and
// everything that cannot be mapped is reported as a warning for the
// administrator to review. The result is only a draft: it is saved through
// the normal blueprint import (Parse and validation) once reviewed, and its
// install script runs like any blueprint script, in the sandboxed builder
// container as the unprivileged server user.

// MaxEggBytes bounds an uploaded egg.
const MaxEggBytes = 512 << 10

// EggDraft is the result of converting an egg.
type EggDraft struct {
	YAML     string   `json:"yaml"`
	Warnings []string `json:"warnings"`
	// Error is set when the draft does not validate as a blueprint yet; the
	// administrator can fix the YAML before saving.
	Error  string `json:"error,omitempty"`
	Format string `json:"format"` // PTDL_v1 | PTDL_v2
	Name   string `json:"name"`
}

type eggVariable struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	EnvVariable  string          `json:"env_variable"`
	DefaultValue json.RawMessage `json:"default_value"`
	UserViewable json.RawMessage `json:"user_viewable"`
	UserEditable json.RawMessage `json:"user_editable"`
	Rules        json.RawMessage `json:"rules"`
	FieldType    string          `json:"field_type"`
}

type eggScripts struct {
	Installation struct {
		Script     string `json:"script"`
		Container  string `json:"container"`
		Entrypoint string `json:"entrypoint"`
	} `json:"installation"`
}

type egg struct {
	Meta struct {
		Version string `json:"version"`
	} `json:"meta"`
	Name         string          `json:"name"`
	Author       string          `json:"author"`
	Description  string          `json:"description"`
	Features     []string        `json:"features"`
	DockerImages json.RawMessage `json:"docker_images"`
	Image        string          `json:"image"` // PTDL_v1
	FileDenylist []string        `json:"file_denylist"`
	Startup      string          `json:"startup"`
	Config       struct {
		Files   json.RawMessage `json:"files"`
		Startup json.RawMessage `json:"startup"`
		Logs    json.RawMessage `json:"logs"`
		Stop    string          `json:"stop"`
	} `json:"config"`
	Scripts   eggScripts    `json:"scripts"`
	Variables []eggVariable `json:"variables"`
}

// Top-level keys of an egg that are understood (or deliberately ignored).
var eggKnownKeys = map[string]bool{"_comment": true, "meta": true, "exported_at": true, "name": true, "author": true,
	"description": true, "features": true, "docker_images": true, "image": true, "file_denylist": true, "startup": true,
	"config": true, "scripts": true, "variables": true, "uuid": true, "update_url": true}

type eggConverter struct {
	warn []string
	vars map[string]bool
}

func (c *eggConverter) warnf(format string, args ...any) {
	if len(c.warn) < 200 {
		c.warn = append(c.warn, fmt.Sprintf(format, args...))
	}
}

// ConvertEgg converts a Pterodactyl egg (PTDL_v1 or PTDL_v2 JSON) into a
// blueprint draft. It returns an error only when the input is not a usable
// egg at all; mapping problems are warnings.
func ConvertEgg(data []byte) (EggDraft, error) {
	if len(data) == 0 || len(data) > MaxEggBytes {
		return EggDraft{}, fmt.Errorf("the egg must be 1 byte to %d KiB of JSON", MaxEggBytes>>10)
	}
	if !json.Valid(data) {
		return EggDraft{}, errors.New("the egg is not valid JSON")
	}
	var top map[string]json.RawMessage
	if err := strictDecode(data, &top); err != nil {
		return EggDraft{}, errors.New("the egg must be a JSON object")
	}
	var e egg
	if err := strictDecode(data, &e); err != nil {
		return EggDraft{}, fmt.Errorf("the egg has an unexpected structure: %v", err)
	}
	c := &eggConverter{vars: map[string]bool{}}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !eggKnownKeys[k] {
			c.warnf("Unknown egg field %q was ignored.", clip(k, 40))
		}
	}
	format := e.Meta.Version
	switch format {
	case "PTDL_v1", "PTDL_v2":
	case "":
		return EggDraft{}, errors.New("this is not a Pterodactyl egg export (meta.version is missing)")
	default:
		return EggDraft{}, fmt.Errorf("unsupported egg format %q (PTDL_v1 and PTDL_v2 are supported)", clip(format, 20))
	}
	if strings.TrimSpace(e.Name) == "" {
		return EggDraft{}, errors.New("the egg has no name")
	}

	s := Spec{
		Slug:        eggSlug(e.Name),
		Name:        clip(oneLineText(e.Name), 80),
		Category:    "Imported",
		Description: clip(oneLineText(e.Description), 1000),
		Resources:   Resources{MemoryMB: 2048, CPUs: 2, Pids: 1024},
	}
	c.warnf("Eggs do not declare resources or ports: memory 2048 MiB, 2 CPUs and one automatically chosen port were set. Adjust resources and ports (default, extra) to the game.")
	if e.Author != "" {
		c.warnf("Egg author: %s. Review the install script before saving; it runs with internet access in the builder container.", clip(oneLineText(e.Author), 80))
	}
	s.Images = c.images(e)
	c.variables(e, &s)
	s.Startup = c.startup(e)
	s.Install = c.install(e)
	s.ConfigFiles = c.configFiles(e.Config.Files)
	c.features(e, &s)
	if len(e.FileDenylist) > 0 {
		c.warnf("The egg's file deny list (%d entries) is not supported: users can edit every file of their server.", len(e.FileDenylist))
	}
	if len(e.Config.Logs) > 0 && !isEmptyJSON(e.Config.Logs) {
		c.warnf("The egg's log configuration (deprecated in Pterodactyl) was ignored.")
	}
	// Every {{VAR}} must now be known.
	s.Startup.Command = strings.TrimSpace(c.fixRefs("startup command", s.Startup.Command))

	out, err := marshalSpec(s, e.Name, format)
	if err != nil {
		return EggDraft{}, err
	}
	d := EggDraft{YAML: out, Warnings: c.warn, Format: format, Name: s.Name}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if _, err := Parse([]byte(out)); err != nil {
		d.Error = err.Error()
	}
	return d, nil
}

// strictDecode decodes exactly one JSON value with no trailing data.
func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

func isEmptyJSON(r json.RawMessage) bool {
	t := strings.TrimSpace(string(r))
	if t == "" || t == "null" || t == "{}" || t == "[]" || t == `""` || t == `"{}"` || t == `"[]"` {
		return true
	}
	return false
}

// jsonish decodes a field that eggs store either as a JSON value or as a
// string containing JSON (config.files, config.startup).
func jsonish(r json.RawMessage, v any) error {
	if isEmptyJSON(r) {
		return nil
	}
	var str string
	if json.Unmarshal(r, &str) == nil {
		if len(str) > MaxEggBytes {
			return errors.New("too large")
		}
		return strictDecode([]byte(str), v)
	}
	return strictDecode(r, v)
}

var slugCleanRe = regexp.MustCompile(`[^a-z0-9]+`)

func eggSlug(name string) string {
	s := strings.Trim(slugCleanRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "game"
	}
	s = "egg-" + s
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return s
}

func oneLineText(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ", "\x00", "").Replace(s)
	return strings.TrimSpace(s)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

var labelCleanRe = regexp.MustCompile(`[^A-Za-z0-9 ._+()-]+`)
var javaLabelRe = regexp.MustCompile(`(?i)java[ _-]?([0-9]{1,2})\b`)

func (c *eggConverter) images(e egg) []Image {
	type pair struct{ label, ref string }
	var list []pair
	if !isEmptyJSON(e.DockerImages) {
		var m map[string]string
		var arr []string
		switch {
		case json.Unmarshal(e.DockerImages, &m) == nil:
			labels := make([]string, 0, len(m))
			for l := range m {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			for _, l := range labels {
				list = append(list, pair{l, m[l]})
			}
		case json.Unmarshal(e.DockerImages, &arr) == nil:
			for _, r := range arr {
				list = append(list, pair{r, r})
			}
		default:
			c.warnf("docker_images has an unexpected format and was ignored.")
		}
	}
	if len(list) == 0 && e.Image != "" {
		list = append(list, pair{e.Image, e.Image})
	}
	var out []Image
	seen := map[string]bool{}
	for _, p := range list {
		ref := strings.TrimSpace(p.ref)
		if !imageRe.MatchString(ref) {
			c.warnf("Image %q is not a valid image reference and was skipped.", clip(ref, 120))
			continue
		}
		if len(out) == maxImages {
			c.warnf("Only the first %d images were kept.", maxImages)
			break
		}
		label := strings.TrimSpace(labelCleanRe.ReplaceAllString(p.label, " "))
		if label == p.ref || label == "" || !labelRe.MatchString(label) {
			label = imageLabel(ref)
		}
		label = clip(label, 64)
		for base, i := label, 2; seen[label] || !labelRe.MatchString(label); i++ {
			label = clip(base, 58) + " " + strconv.Itoa(i)
		}
		seen[label] = true
		im := Image{Label: label, Ref: ref}
		if m := javaLabelRe.FindStringSubmatch(p.label + " " + ref); m != nil {
			im.Java, _ = strconv.Atoi(m[1])
		}
		out = append(out, im)
	}
	if len(out) == 0 {
		c.warnf("The egg has no usable image; a placeholder image (debian:bookworm-slim) was set. Choose the right image before saving.")
		out = []Image{{Label: "Debian", Ref: "debian:bookworm-slim"}}
	}
	return out
}

func imageLabel(ref string) string {
	l := ref
	if i := strings.LastIndex(l, "/"); i >= 0 {
		l = l[i+1:]
	}
	l = strings.Split(l, "@")[0]
	l = strings.TrimSpace(labelCleanRe.ReplaceAllString(l, " "))
	l = strings.ReplaceAll(l, ":", " ")
	if l == "" || !labelRe.MatchString(l) {
		l = "Image"
	}
	return l
}

func rawString(r json.RawMessage) (string, bool) {
	if len(r) == 0 || string(r) == "null" {
		return "", true
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s, true
	}
	var n json.Number
	if json.Unmarshal(r, &n) == nil {
		return n.String(), true
	}
	var b bool
	if json.Unmarshal(r, &b) == nil {
		return strconv.FormatBool(b), true
	}
	return "", false
}

func rawBool(r json.RawMessage, def bool) bool {
	s, ok := rawString(r)
	if !ok || s == "" {
		return def
	}
	switch strings.ToLower(s) {
	case "1", "true":
		return true
	}
	return false
}

func (c *eggConverter) variables(e egg, s *Spec) {
	for i, v := range e.Variables {
		if len(s.Variables) == maxVariables {
			c.warnf("Only the first %d variables were kept; %d more were dropped.", maxVariables, len(e.Variables)-i)
			break
		}
		env := strings.TrimSpace(v.EnvVariable)
		if !envNameRe.MatchString(env) {
			c.warnf("Variable %q has an environment name RivetPanel cannot use (UPPER_SNAKE_CASE required) and was skipped.", clip(env, 64))
			continue
		}
		if IsSystemVariable(env) || strings.HasPrefix(env, "RIVET_") {
			c.warnf("Variable %s is provided by RivetPanel itself and was not imported.", env)
			continue
		}
		if c.vars[env] {
			c.warnf("Variable %s is declared twice; the second one was skipped.", env)
			continue
		}
		def, ok := rawString(v.DefaultValue)
		if !ok {
			c.warnf("Variable %s has a non-text default; it was set to empty.", env)
		}
		def = strings.ReplaceAll(def, "\r", "")
		if strings.Contains(def, "\n") {
			def = strings.ReplaceAll(def, "\n", " ")
			c.warnf("Variable %s: line breaks were removed from the default value.", env)
		}
		name := clip(oneLineText(v.Name), 80)
		if name == "" {
			name = env
		}
		desc := oneLineText(v.Description)
		if len(desc) > 500 {
			desc = clip(desc, 500)
			c.warnf("Variable %s: the description was shortened to 500 characters.", env)
		}
		bv := Variable{Env: env, Name: name, Description: desc, Default: def, Editable: rawBool(v.UserEditable, false)}
		if !rawBool(v.UserViewable, true) {
			if bv.Editable {
				c.warnf("Variable %s is hidden but editable in the egg; RivetPanel shows every variable to people who can open the Startup page.", env)
			} else {
				c.warnf("Variable %s is hidden from users in the egg; RivetPanel shows it read-only on the Startup page. Do not keep secrets in it.", env)
			}
		}
		rules, _ := rawString(v.Rules)
		bv.Rules = c.rules(env, rules, def)
		if len(def) > 256 && bv.Rules.MaxLen < len(def) {
			bv.Rules.MaxLen = min(4096, len(def))
		}
		if err := bv.Check(def); err != nil {
			c.warnf("Variable %s: its default value %q does not pass the mapped rules (%v); the rules were relaxed to plain text.", env, clip(def, 60), err)
			bv.Rules = Rules{Type: "string", MaxLen: bv.Rules.MaxLen}
			if err := bv.Check(def); err != nil {
				c.warnf("Variable %s: the default value is unusable (%v) and was cleared.", env, err)
				bv.Default = ""
			}
		}
		s.Variables = append(s.Variables, bv)
		c.vars[env] = true
	}
}

// rules maps Laravel validation rules (used by eggs) to RivetPanel rules.
func (c *eggConverter) rules(env, raw, def string) Rules {
	r := Rules{Type: "string"}
	if strings.TrimSpace(raw) == "" {
		return r
	}
	parts := splitRules(raw)
	has := map[string]string{}
	for _, p := range parts {
		name, arg, _ := strings.Cut(p, ":")
		has[strings.ToLower(strings.TrimSpace(name))] = arg
	}
	_, nullable := has["nullable"]
	_, required := has["required"]
	var minN, maxN *int64
	num := func(s string) *int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil
		}
		return &n
	}
	if a, ok := has["min"]; ok {
		minN = num(a)
	}
	if a, ok := has["max"]; ok {
		maxN = num(a)
	}
	if a, ok := has["between"]; ok {
		lo, hi, _ := strings.Cut(a, ",")
		minN, maxN = num(lo), num(hi)
	}
	for name, arg := range has {
		switch name {
		case "required", "nullable", "string", "sometimes", "min", "max", "between", "integer", "numeric", "boolean", "in", "regex", "alpha_dash", "alpha_num", "url":
		default:
			c.warnf("Variable %s: the rule %q is not supported and was ignored.", env, clip(name+optArg(arg), 60))
		}
	}
	switch {
	case has["in"] != "" || keyIn(has, "in"):
		opts := strings.Split(has["in"], ",")
		for i := range opts {
			opts[i] = strings.TrimSpace(opts[i])
		}
		if nullable {
			opts = append(opts, "")
		}
		if len(opts) > 100 {
			c.warnf("Variable %s: more than 100 choices; kept as free text.", env)
			break
		}
		r.Type, r.Options = "enum", opts
		return r
	case keyIn(has, "boolean"):
		r.Type, r.Options = "enum", []string{"1", "0", "true", "false"}
		if nullable {
			r.Options = append(r.Options, "")
		}
		return r
	case keyIn(has, "integer"):
		if nullable || (def == "" && !required) {
			r.Pattern = `^(-?[0-9]+)?$`
			if minN != nil || maxN != nil {
				c.warnf("Variable %s: the number range cannot be checked for an optional number and was not applied.", env)
			}
			return r
		}
		r.Type, r.Min, r.Max = "int", minN, maxN
		return r
	case keyIn(has, "numeric"):
		r.Pattern = `^-?[0-9]+(\.[0-9]+)?$`
		if nullable {
			r.Pattern = `^(-?[0-9]+(\.[0-9]+)?)?$`
		}
		if minN != nil || maxN != nil {
			c.warnf("Variable %s: the numeric range is not checked (decimal numbers).", env)
		}
		return r
	}
	if maxN != nil && *maxN > 0 {
		r.MaxLen = int(min(*maxN, 4096))
	}
	if re, ok := has["regex"]; ok || keyIn(has, "regex") {
		if p, ok := pcreToRE2(re); ok {
			if nullable {
				p = "^$|" + p
			}
			r.Pattern = p
		} else {
			c.warnf("Variable %s: the pattern %q cannot be used (RivetPanel uses RE2 syntax) and was not applied.", env, clip(re, 80))
		}
	} else if keyIn(has, "alpha_dash") {
		r.Pattern = `^[A-Za-z0-9_-]*$`
	} else if keyIn(has, "alpha_num") {
		r.Pattern = `^[A-Za-z0-9]*$`
	} else if keyIn(has, "url") {
		r.Pattern = `^(https?://\S+)?$`
	}
	if minN != nil && *minN > 0 && r.Pattern == "" {
		r.Pattern = fmt.Sprintf(`^(.{%d,})?$`, min(*minN, 1000))
		if required {
			r.Pattern = fmt.Sprintf(`^.{%d,}$`, min(*minN, 1000))
		}
	} else if required && r.Pattern == "" && def != "" {
		r.Pattern = `\S`
	}
	if required && def == "" {
		c.warnf("Variable %s is required by the egg but has no default; the server will start with it empty until someone sets it.", env)
	}
	return r
}

func keyIn(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func optArg(a string) string {
	if a == "" {
		return ""
	}
	return ":" + a
}

// splitRules splits "required|regex:/a|b/|max:3" on pipes outside a regex.
func splitRules(raw string) []string {
	var out []string
	for raw != "" {
		raw = strings.TrimLeft(raw, "|")
		if strings.HasPrefix(raw, "regex:") && len(raw) > 7 {
			delim := raw[6]
			end := strings.LastIndexByte(raw, delim)
			if end > 6 {
				// The regex runs to its closing delimiter (and flags).
				stop := end + 1
				for stop < len(raw) && raw[stop] != '|' {
					stop++
				}
				out = append(out, raw[:stop])
				raw = raw[stop:]
				continue
			}
		}
		i := strings.IndexByte(raw, '|')
		if i < 0 {
			out = append(out, raw)
			break
		}
		out = append(out, raw[:i])
		raw = raw[i:]
	}
	return out
}

// pcreToRE2 converts a PHP regular expression (/pattern/flags) to Go syntax.
func pcreToRE2(re string) (string, bool) {
	re = strings.TrimSpace(re)
	if len(re) < 2 {
		return "", false
	}
	delim := re[0]
	end := strings.LastIndexByte(re, delim)
	if end <= 0 {
		return "", false
	}
	body, flags := re[1:end], re[end+1:]
	prefix := ""
	for _, f := range flags {
		switch f {
		case 'i', 'm', 's':
			prefix += string(f)
		case 'u', 'D':
		default:
			return "", false
		}
	}
	if prefix != "" {
		body = "(?" + prefix + ")" + body
	}
	if len(body) > 1000 {
		return "", false
	}
	if _, err := regexp.Compile(body); err != nil {
		return "", false
	}
	return body, true
}

// Pterodactyl placeholders that have a RivetPanel equivalent.
var eggPlaceholders = map[string]string{
	"server.build.default.port": "{{SERVER_PORT}}",
	"server.build.default.ip":   "0.0.0.0",
	"server.build.memory":       "{{SERVER_MEMORY}}",
	"server.uuid":               "{{SERVER_ID}}",
	"P_SERVER_UUID":             "{{SERVER_ID}}",
}

var eggRefRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}`)

// mapValue rewrites a config value's placeholders. ok is false when the
// value references something with no equivalent.
func (c *eggConverter) mapValue(v string) (string, bool) {
	ok := true
	out := eggRefRe.ReplaceAllStringFunc(v, func(m string) string {
		name := eggRefRe.FindStringSubmatch(m)[1]
		if r, found := eggPlaceholders[name]; found {
			return r
		}
		for _, p := range []string{"server.build.env.", "env."} {
			if strings.HasPrefix(name, p) {
				name = strings.TrimPrefix(name, p)
				break
			}
		}
		if c.vars[name] || IsSystemVariable(name) {
			return "{{" + name + "}}"
		}
		ok = false
		return m
	})
	return out, ok
}

// fixRefs keeps {{VAR}} references RivetPanel knows, mapping Pterodactyl's
// own ones, and removes (with a warning) the rest.
func (c *eggConverter) fixRefs(where, tpl string) string {
	return eggRefRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := eggRefRe.FindStringSubmatch(m)[1]
		if c.vars[name] || IsSystemVariable(name) {
			return "{{" + name + "}}"
		}
		if r, ok := eggPlaceholders[name]; ok {
			return r
		}
		c.warnf("The %s uses %s, which RivetPanel does not provide; it was removed.", where, clip(m, 60))
		return ""
	})
}

func (c *eggConverter) startup(e egg) Startup {
	st := Startup{StopTimeoutSeconds: 60}
	cmd := strings.TrimSpace(strings.ReplaceAll(e.Startup, "\r", ""))
	if strings.Contains(cmd, "\n") {
		cmd = strings.Join(strings.Fields(cmd), " ")
		c.warnf("The startup command had line breaks; they were joined into one line.")
	}
	if cmd == "" {
		cmd = "echo 'The egg has no startup command'; exit 1"
		c.warnf("The egg has no startup command; a placeholder that exits was set.")
	}
	if len(cmd) > maxStartupLen {
		cmd = cmd[:maxStartupLen]
		c.warnf("The startup command was shortened to %d characters.", maxStartupLen)
	}
	if strings.Contains(cmd, "{{SERVER_MEMORY}}") {
		c.warnf("SERVER_MEMORY is %d%% of the server's memory limit in RivetPanel (the full limit in Pterodactyl); adjust resources.heap_percent if needed.", 85)
	}
	st.Command = cmd
	stop := strings.TrimSpace(e.Config.Stop)
	switch {
	case stop == "^C" || stop == "^^C":
		st.StopSignal = "SIGINT"
	case strings.HasPrefix(stop, "^"):
		c.warnf("The stop signal %q is not supported; the server is stopped with SIGTERM.", clip(stop, 20))
	case stop != "" && (len(stop) > 128 || strings.ContainsAny(stop, "\x00\r\n")):
		c.warnf("The stop command is longer than one line of 128 characters and was not imported; the server is stopped with SIGTERM.")
	default:
		st.Stop = stop
	}
	var cfg struct {
		Done            json.RawMessage `json:"done"`
		UserInteraction []any           `json:"userInteraction"`
	}
	if err := jsonish(e.Config.Startup, &cfg); err != nil {
		c.warnf("config.startup could not be read (%v) and was ignored.", err)
		return st
	}
	var done string
	var many []string
	switch {
	case len(cfg.Done) == 0 || string(cfg.Done) == "null":
	case json.Unmarshal(cfg.Done, &done) == nil:
	case json.Unmarshal(cfg.Done, &many) == nil && len(many) > 0:
		done = many[0]
		if len(many) > 1 {
			c.warnf("Only the first of %d \"started\" markers was kept (%q).", len(many), clip(done, 60))
		}
	default:
		c.warnf("config.startup.done has an unexpected format and was ignored.")
	}
	done = oneLineText(done)
	if len(done) > 200 {
		done = clip(done, 200)
	}
	st.Done = done
	if len(cfg.UserInteraction) > 0 {
		c.warnf("config.startup.userInteraction (%d entries) is not supported and was ignored.", len(cfg.UserInteraction))
	}
	return st
}

func (c *eggConverter) install(e egg) Install {
	in := Install{}
	sc := e.Scripts.Installation
	script := strings.ReplaceAll(sc.Script, "\r\n", "\n")
	script = strings.ReplaceAll(script, "\r", "\n")
	if strings.TrimSpace(script) == "" {
		c.warnf("The egg has no install script; a no-op script was set. Upload the server files yourself or edit the install step.")
		in.Script = "echo 'Nothing to install'"
		return in
	}
	if n := strings.Count(script, "/mnt/server"); n > 0 {
		script = strings.ReplaceAll(script, "/mnt/server", "/workspace")
		c.warnf("The install script's /mnt/server paths (%d) were changed to /workspace, where RivetPanel mounts the server's files.", n)
	}
	for _, pm := range []string{"apt-get ", "apt ", "apk ", "yum ", "dnf ", "pacman "} {
		if strings.Contains(script, pm) {
			c.warnf("The install script calls a package manager (%s). Install scripts run as the unprivileged server user, not root, so package installs will fail; use an install image that already has the tools.", strings.TrimSpace(pm))
			break
		}
	}
	if strings.Contains(script, "\x00") {
		script = strings.ReplaceAll(script, "\x00", "")
	}
	shell := "sh"
	switch strings.TrimSpace(sc.Entrypoint) {
	case "bash", "/bin/bash", "/usr/bin/bash":
		shell = "bash"
	case "", "sh", "ash", "/bin/sh", "/bin/ash":
	default:
		c.warnf("The install entrypoint %q is not supported; the script runs with sh.", clip(sc.Entrypoint, 40))
	}
	// The runner runs the script with `sh -c "set -e\n<script>"`; hand the
	// egg's script to its own shell unchanged so its semantics (no -e) stay.
	wrapped := "# Imported from a Pterodactyl egg; runs with " + shell + " like the original.\n" +
		"exec " + shell + " -c '" + strings.ReplaceAll(script, "'", `'\''`) + "' egg-install\n"
	if len(wrapped) > maxScriptLen {
		c.warnf("The install script is longer than %d KiB and was replaced by a placeholder.", maxScriptLen>>10)
		wrapped = "echo 'The imported install script was too long'; exit 1"
	}
	in.Script = wrapped
	if img := strings.TrimSpace(sc.Container); img != "" {
		if imageRe.MatchString(img) {
			in.Image = img
		} else {
			c.warnf("The install container %q is not a valid image reference; the server's image is used instead.", clip(img, 120))
		}
	}
	return in
}

func (c *eggConverter) configFiles(raw json.RawMessage) []ConfigFile {
	var files map[string]struct {
		Parser string                     `json:"parser"`
		Find   map[string]json.RawMessage `json:"find"`
	}
	if err := jsonish(raw, &files); err != nil {
		c.warnf("config.files could not be read (%v) and was ignored.", err)
		return nil
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []ConfigFile
	add := func(cf ConfigFile) {
		if len(out) == maxConfigs {
			c.warnf("Only %d configuration file edits are supported; %s was not imported.", maxConfigs, cf.Path)
			return
		}
		out = append(out, cf)
	}
	for _, p := range paths {
		f := files[p]
		if checkPath(p) != nil {
			c.warnf("Config file %q is not a plain relative path and was not imported.", clip(p, 120))
			continue
		}
		keys := make([]string, 0, len(f.Find))
		for k := range f.Find {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		values := map[string]string{}
		for _, k := range keys {
			var v string
			if err := json.Unmarshal(f.Find[k], &v); err != nil {
				c.warnf("%s: the edit of %q uses a find-and-replace object, which is not supported; it was not imported.", p, clip(k, 60))
				continue
			}
			mv, ok := c.mapValue(v)
			if !ok {
				c.warnf("%s: the value of %q (%q) uses a placeholder RivetPanel does not provide; it was not imported.", p, clip(k, 60), clip(v, 80))
				continue
			}
			if strings.ContainsAny(mv, "\n\r") {
				c.warnf("%s: the value of %q has line breaks; it was not imported.", p, clip(k, 60))
				continue
			}
			values[k] = mv
		}
		if len(values) == 0 {
			continue
		}
		switch f.Parser {
		case "properties":
			cf := ConfigFile{Path: p, Format: "properties", Create: false, Set: map[string]string{}}
			for k, v := range values {
				if k == "" || strings.ContainsAny(k, "=:\n\r ") || len(k) > 128 {
					c.warnf("%s: key %q cannot be set in a properties file and was not imported.", p, clip(k, 60))
					continue
				}
				cf.Set[k] = v
			}
			if len(cf.Set) > 0 {
				add(cf)
			}
		case "ini":
			sections := map[string]map[string]string{}
			for k, v := range values {
				sec, key := "", k
				if i := strings.IndexByte(k, '.'); i >= 0 {
					sec, key = k[:i], k[i+1:]
				}
				if key == "" || strings.ContainsAny(key, "=[;#\n\r") || strings.TrimSpace(key) != key || len(key) > 128 ||
					strings.ContainsAny(sec, "[]\n\r") || len(sec) > 128 {
					c.warnf("%s: INI key %q cannot be mapped and was not imported.", p, clip(k, 60))
					continue
				}
				if sections[sec] == nil {
					sections[sec] = map[string]string{}
				}
				sections[sec][key] = v
			}
			names := make([]string, 0, len(sections))
			for n := range sections {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				add(ConfigFile{Path: p, Format: "ini", Section: n, Set: sections[n]})
			}
			c.warnf("%s: INI keys were split into section and key at the first dot; check keys that contain dots themselves.", p)
		case "file":
			cf := ConfigFile{Path: p, Format: "lines"}
			for _, k := range keys {
				v, ok := values[k]
				if !ok {
					continue
				}
				cf.Replace = append(cf.Replace, LineReplace{Match: "^" + regexp.QuoteMeta(k), With: v})
			}
			add(cf)
		case "yaml", "yml", "json", "xml":
			c.warnf("%s: the %s parser is not supported yet; its %d edits were not imported. Set these values in the file or with startup variables.", p, f.Parser, len(values))
		default:
			c.warnf("%s: unknown parser %q; its edits were not imported.", p, clip(f.Parser, 20))
		}
	}
	return out
}

func (c *eggConverter) features(e egg, s *Spec) {
	for _, f := range e.Features {
		switch f {
		case "eula":
			s.Agreements = append(s.Agreements, Agreement{ID: "minecraft-eula", Text: "I accept the Minecraft End User License Agreement.",
				URL: "https://aka.ms/MinecraftEULA", File: "eula.txt", Content: "eula=true\n"})
			c.warnf("Feature \"eula\": a Minecraft EULA agreement was added (people accept it when creating a server). Remove it if the game is not Minecraft.")
		default:
			c.warnf("Feature %q is not supported and was ignored.", clip(f, 40))
		}
	}
}

// marshalSpec renders a draft as YAML without empty optional fields.
func marshalSpec(s Spec, eggName, format string) (string, error) {
	var node yaml.Node
	if err := node.Encode(s); err != nil {
		return "", err
	}
	prune(&node, false)
	var buf bytes.Buffer
	buf.WriteString("# Imported from the Pterodactyl egg " + strconv.Quote(clip(oneLineText(eggName), 80)) + " (" + format + ").\n")
	buf.WriteString("# Review the warnings, the install script, images, ports and resources before saving.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

// Struct fields whose empty value may be omitted. Map entries (config
// "set") are never pruned: an empty value there is meaningful.
var prunable = map[string]bool{"description": true, "java_from": true, "stop": true, "done": true, "stop_signal": true,
	"query": true, "query_allocation": true, "features": true, "addons": true, "agreements": true, "config_files": true,
	"download": true, "steamcmd": true, "image": true, "script": true, "pattern": true, "min": true, "max": true,
	"options": true, "max_len": true, "versions": true, "reinstall": true, "editable": false, "section": true,
	"replace": true, "set": true, "create": true, "url": true, "build": true, "project": true, "java": true,
	"heap_percent": true, "min_memory_mb": true, "default": false, "extra": true, "container": true, "contiguous": true,
	"stop_timeout_seconds": false, "pids": false, "runtime": true}

func prune(n *yaml.Node, inMap bool) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			prune(c, false)
		}
	case yaml.MappingNode:
		isStruct := !inMap
		var kept []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if isStruct && prunable[k.Value] && emptyNode(v) {
				continue
			}
			prune(v, k.Value == "set")
			kept = append(kept, k, v)
		}
		n.Content = kept
	}
}

func emptyNode(v *yaml.Node) bool {
	switch v.Kind {
	case yaml.ScalarNode:
		return v.Tag == "!!null" || v.Value == "" || v.Value == "0" || v.Value == "false"
	case yaml.SequenceNode, yaml.MappingNode:
		return len(v.Content) == 0
	}
	return false
}
