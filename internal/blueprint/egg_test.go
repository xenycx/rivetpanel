package blueprint

import (
	"os"
	"strings"
	"testing"
)

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestConvertEggV2(t *testing.T) {
	data, err := os.ReadFile("testdata/egg-v2-minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ConvertEgg(data)
	if err != nil {
		t.Fatal(err)
	}
	if d.Error != "" {
		t.Fatalf("draft does not validate: %s\n%s", d.Error, d.YAML)
	}
	s, err := Parse([]byte(d.YAML))
	if err != nil {
		t.Fatal(err)
	}
	if s.Slug != "egg-example-java-game" || s.Category != "Imported" || s.Runtime != DefaultRuntime || d.Format != "PTDL_v2" {
		t.Fatalf("identity: %+v", s)
	}
	if len(s.Images) != 2 || s.Images[0].Ref != "ghcr.io/example/java:17" || s.Images[0].Java != 17 || s.Images[1].Java != 21 {
		t.Fatalf("images: %+v", s.Images)
	}
	if want := "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar {{SERVER_JARFILE}} --port {{SERVER_PORT}} --loc"; s.Startup.Command != want {
		t.Fatalf("startup: %q", s.Startup.Command)
	}
	if s.Startup.Stop != "stop" || s.Startup.Done != ")! For help, type" {
		t.Fatalf("stop/done: %+v", s.Startup)
	}
	get := func(env string) Variable {
		v, ok := s.Variable(env)
		if !ok {
			t.Fatalf("variable %s missing", env)
		}
		return v
	}
	if v := get("SERVER_JARFILE"); v.Rules.Pattern != `^([\w\d._-]+)(\.jar)$` || !v.Editable || v.Check("x.jar") != nil || v.Check("x.zip") == nil {
		t.Fatalf("jar: %+v", v)
	}
	if v := get("MOTD"); v.Rules.MaxLen != 40 {
		t.Fatalf("motd: %+v", v)
	}
	if v := get("MAX_PLAYERS"); v.Rules.Type != "int" || *v.Rules.Min != 1 || *v.Rules.Max != 100 || v.Check("101") == nil {
		t.Fatalf("players: %+v", v)
	}
	if v := get("MODE"); v.Rules.Type != "enum" || len(v.Rules.Options) != 2 || v.Editable {
		t.Fatalf("mode: %+v", v)
	}
	if v := get("DEBUG"); v.Rules.Type != "enum" || v.Check("1") != nil || v.Check("yes") == nil {
		t.Fatalf("debug: %+v", v)
	}
	if v := get("LOOK"); v.Rules.Pattern != "" {
		t.Fatalf("lookahead pattern should be dropped: %+v", v)
	}
	if _, ok := s.Variable("SERVER_PORT"); ok {
		t.Fatal("system variable imported")
	}
	if _, ok := s.Variable("lowercase"); ok {
		t.Fatal("invalid env name imported")
	}
	if !strings.Contains(s.Install.Script, "exec bash -c '") || strings.Contains(s.Install.Script, "/mnt/server") ||
		!strings.Contains(s.Install.Script, `echo '\''it'\'''\''s'\''`) || strings.Contains(s.Install.Script, "\r") {
		t.Fatalf("install script: %q", s.Install.Script)
	}
	if s.Install.Image != "ghcr.io/example/installers:debian" {
		t.Fatalf("install image %q", s.Install.Image)
	}
	byPath := map[string][]ConfigFile{}
	for _, c := range s.ConfigFiles {
		byPath[c.Path] = append(byPath[c.Path], c)
	}
	if p := byPath["server.properties"]; len(p) != 1 || p[0].Set["server-port"] != "{{SERVER_PORT}}" || p[0].Set["server-ip"] != "0.0.0.0" ||
		p[0].Set["motd"] != "{{MOTD}}" || len(p[0].Set) != 3 {
		t.Fatalf("properties: %+v", p)
	}
	if p := byPath["settings.ini"]; len(p) != 2 || p[0].Section != "" || p[0].Set["Name"] != "{{MOTD}}" || p[1].Section != "Network" || p[1].Set["Port"] != "{{SERVER_PORT}}" {
		t.Fatalf("ini: %+v", p)
	}
	if p := byPath["start.cfg"]; len(p) != 1 || p[0].Replace[0].Match != "^port=" || p[0].Replace[0].With != "port={{SERVER_PORT}}" {
		t.Fatalf("file parser: %+v", p)
	}
	if _, ok := byPath["config.yml"]; ok {
		t.Fatal("yaml parser should not be imported")
	}
	if len(s.Agreements) != 1 || s.Agreements[0].ID != "minecraft-eula" {
		t.Fatalf("agreements: %+v", s.Agreements)
	}
	for _, w := range []string{
		"Not An Image!", "P_SERVER_LOCATION", "config.docker.interface", "yaml parser is not supported",
		"userInteraction", "first of 2", "package manager", "/mnt/server paths (2)", "file deny list",
		"java_version", "SERVER_PORT is provided by RivetPanel", "lowercase", "hidden from users",
		"RE2", `rule "uuid"`, "TOKEN is required", "resources or ports", "Egg author",
	} {
		if !hasWarning(d.Warnings, w) {
			t.Errorf("missing warning containing %q; got:\n%s", w, strings.Join(d.Warnings, "\n"))
		}
	}
}

func TestConvertEggV1(t *testing.T) {
	data, err := os.ReadFile("testdata/egg-v1-minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ConvertEgg(data)
	if err != nil || d.Error != "" {
		t.Fatalf("%v %s\n%s", err, d.Error, d.YAML)
	}
	s, _ := Parse([]byte(d.YAML))
	if len(s.Images) != 1 || s.Images[0].Ref != "quay.io/example/game:latest" || s.Startup.StopSignal != "SIGINT" || s.Startup.Stop != "" ||
		s.Startup.Done != "Ready" || !strings.Contains(s.Install.Script, "exec sh -c 'cd /workspace && echo ok'") || s.Install.Image != "alpine:3.20" {
		t.Fatalf("v1: %+v\n%s", s, d.YAML)
	}
	if hasWarning(d.Warnings, "Unknown egg field") {
		t.Fatalf("unexpected warnings: %v", d.Warnings)
	}
}

func TestConvertEggRefusesBadInput(t *testing.T) {
	big := `{"meta":{"version":"PTDL_v2"},"name":"x","description":"` + strings.Repeat("a", MaxEggBytes) + `"}`
	for name, in := range map[string]string{
		"empty":         "",
		"not json":      "{nope",
		"array":         "[1,2]",
		"trailing":      `{"meta":{"version":"PTDL_v2"},"name":"x"} {"more":1}`,
		"no version":    `{"name":"x"}`,
		"other version": `{"meta":{"version":"PTDL_v9"},"name":"x"}`,
		"no name":       `{"meta":{"version":"PTDL_v2"},"name":" "}`,
		"wrong types":   `{"meta":{"version":"PTDL_v2"},"name":"x","variables":"nope"}`,
		"too big":       big,
	} {
		if _, err := ConvertEgg([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestConvertEggNeverFetchesOrRunsAnything(t *testing.T) {
	// URLs inside an egg are copied as text at most; ConvertEgg has no
	// network or process access (it takes only bytes and returns a draft).
	d, err := ConvertEgg([]byte(`{"meta":{"version":"PTDL_v2","update_url":"http://127.0.0.1:1/never"},"name":"Net",
		"docker_images":{"x":"alpine:3.20"},"startup":"./run","config":{"stop":"end"},
		"scripts":{"installation":{"script":"curl http://127.0.0.1:1/x","container":"alpine:3.20","entrypoint":"ash"}},
		"variables":[],"unknown_thing":{"a":1}}`))
	if err != nil || d.Error != "" {
		t.Fatalf("%v %s", err, d.Error)
	}
	if !hasWarning(d.Warnings, `Unknown egg field "unknown_thing"`) {
		t.Fatalf("unknown field not reported: %v", d.Warnings)
	}
	if !strings.HasPrefix(d.YAML, "# Imported from the Pterodactyl egg \"Net\" (PTDL_v2).") {
		t.Fatalf("header: %q", d.YAML[:80])
	}
}

func TestSplitRulesAndPCRE(t *testing.T) {
	got := splitRules("required|regex:/^(a|b)$/i|max:3")
	if len(got) != 3 || got[1] != "regex:/^(a|b)$/i" {
		t.Fatalf("split: %q", got)
	}
	if p, ok := pcreToRE2("/^(a|b)$/i"); !ok || p != "(?i)^(a|b)$" {
		t.Fatalf("pcre: %q %v", p, ok)
	}
	for _, bad := range []string{"/(?=x)/", "/a/x", "nodelim", "/"} {
		if _, ok := pcreToRE2(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
