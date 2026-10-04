package blueprint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/blueprints"
)

func TestBuiltinCatalogParses(t *testing.T) {
	all, err := LoadBuiltin(blueprints.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 8 {
		t.Fatalf("only %d built-in blueprints", len(all))
	}
	steam := 0
	for _, b := range all {
		if b.Spec.Install.Download == nil && b.Spec.Install.SteamCMD == nil {
			t.Errorf("%s: built-ins download through a verified provider or SteamCMD", b.Spec.Slug)
		}
		if strings.HasPrefix(b.Spec.Category, "Minecraft") && b.Spec.Category != "Minecraft Proxy" && len(b.Spec.Agreements) == 0 {
			t.Errorf("%s: Minecraft servers must ask for the EULA", b.Spec.Slug)
		}
		// A fixed container port must be the one the server listens on:
		// Velocity writes bind 0.0.0.0:25565 into a new velocity.toml, so the
		// proxy is told the port on its command line.
		if b.Spec.Ports.Container != 0 && b.Spec.Slug == "minecraft-velocity" && !strings.Contains(b.Spec.Startup.Command, "--port {{SERVER_PORT}}") {
			t.Errorf("%s: the proxy must listen on the mapped container port", b.Spec.Slug)
		}
		if b.Spec.Slug == "minecraft-velocity" && b.Spec.ImageForJava(0).Java < 25 {
			t.Errorf("%s: without a known requirement the newest Java must come first (Velocity 4 needs Java 25)", b.Spec.Slug)
		}
		if st := b.Spec.Install.SteamCMD; st != nil {
			steam++
			if b.Spec.Startup.Stop == "" && b.Spec.Startup.StopSignal == "" {
				t.Errorf("%s: SteamCMD built-ins must stop gracefully", b.Spec.Slug)
			}
			if st.Image != DefaultSteamCMDImage || b.Spec.Runtime != DefaultRuntime {
				t.Errorf("%s: defaults not applied: %+v %q", b.Spec.Slug, st, b.Spec.Runtime)
			}
			if b.Spec.Ports.Extra < 1 {
				t.Errorf("%s: Steam games use a second port", b.Spec.Slug)
			}
		}
	}
	if steam < 3 {
		t.Fatalf("only %d SteamCMD built-ins", steam)
	}
}

const steamMinimal = `slug: steam-test
name: Steam test
category: Tests
images: [{label: "SteamCMD", ref: "steamcmd/steamcmd:latest"}]
startup: {command: "./server -port {{SERVER_PORT}} -query {{SERVER_PORT_1}}", stop_signal: SIGINT}
variables:
  - {env: BRANCH, name: Branch, default: "", editable: true, rules: {pattern: '^([a-z]+)?$'}}
install:
  steamcmd: {app_id: 1007, beta: "{{BRANCH}}"}
resources: {memory_mb: 1024, cpus: 1}
ports: {default: 27015, extra: 1, contiguous: true}
query: steam
query_allocation: 1
`

func TestSteamCMDBlueprints(t *testing.T) {
	s, err := Parse([]byte(steamMinimal))
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Install.SteamCMD; st.TimeoutMinutes != 60 || st.MaxSizeGB != 40 || st.Image != DefaultSteamCMDImage {
		t.Fatalf("defaults: %+v", st)
	}
	bad := map[string]string{
		"app id zero":        strings.Replace(steamMinimal, "app_id: 1007", "app_id: 0", 1),
		"app id too big":     strings.Replace(steamMinimal, "app_id: 1007", "app_id: 4294967296", 1),
		"beta injection":     strings.Replace(steamMinimal, `beta: "{{BRANCH}}"`, `beta: "x +login someone"`, 1),
		"beta unknown var":   strings.Replace(steamMinimal, `{{BRANCH}}"}`, `{{NOPE}}"}`, 1),
		"credentials field":  strings.Replace(steamMinimal, "app_id: 1007,", "app_id: 1007, username: me,", 1),
		"timeout":            strings.Replace(steamMinimal, "app_id: 1007,", "app_id: 1007, timeout_minutes: 600,", 1),
		"size":               strings.Replace(steamMinimal, "app_id: 1007,", "app_id: 1007, max_size_gb: 9999,", 1),
		"bad signal":         strings.Replace(steamMinimal, "stop_signal: SIGINT", "stop_signal: SIGKILL", 1),
		"stop and signal":    strings.Replace(steamMinimal, "stop_signal: SIGINT", "stop_signal: SIGINT, stop: quit", 1),
		"query port missing": strings.Replace(steamMinimal, "query_allocation: 1", "query_allocation: 2", 1),
		"unknown query":      strings.Replace(steamMinimal, "query: steam", "query: gamespy", 1),
		"extra port var":     strings.Replace(steamMinimal, "env: BRANCH", "env: SERVER_PORT_2", 1),
	}
	for name, y := range bad {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, ok := range []string{"", "public-test", "beta_2.1"} {
		if !ValidBeta(ok) {
			t.Errorf("ValidBeta(%q) = false", ok)
		}
	}
	for _, no := range []string{"-x", "a b", "a;b", "a\nb", "$(id)", strings.Repeat("a", 65)} {
		if ValidBeta(no) {
			t.Errorf("ValidBeta(%q) = true", no)
		}
	}
}

func TestApplyINI(t *testing.T) {
	in := "; comment\nTop=1\n[Server]\nName=old\nKeep=yes\n[Other]\nName=other\n"
	out, err := ApplyConfig([]byte(in), ConfigFile{Format: "ini", Section: "Server", Set: map[string]string{"Name": "{{N}}", "Port": "7"}},
		map[string]string{"N": "new\nline"})
	if err != nil {
		t.Fatal(err)
	}
	want := "; comment\nTop=1\n[Server]\nName=newline\nKeep=yes\nPort=7\n[Other]\nName=other\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
	out, _ = ApplyConfig([]byte(in), ConfigFile{Format: "ini", Set: map[string]string{"Top": "2", "New": "x"}}, nil)
	if want := "; comment\nTop=2\nNew=x\n[Server]\nName=old\nKeep=yes\n[Other]\nName=other\n"; string(out) != want {
		t.Fatalf("top level: got %q", out)
	}
	out, _ = ApplyConfig([]byte("A=1\n"), ConfigFile{Format: "ini", Section: "New", Set: map[string]string{"B": "2"}}, nil)
	if want := "A=1\n\n[New]\nB=2\n"; string(out) != want {
		t.Fatalf("new section: got %q", out)
	}
	out, _ = ApplyConfig(nil, ConfigFile{Format: "ini", Set: map[string]string{"B": "2"}}, nil)
	if string(out) != "B=2\n" {
		t.Fatalf("empty file: got %q", out)
	}
}

const minimal = `slug: test-game
name: Test
category: Tests
runtime: java
images: [{label: "Java 21", ref: "eclipse-temurin:21-jre-alpine", java: 21}]
startup: {command: "java -Xmx{{SERVER_MEMORY}}M -jar {{JAR}}"}
variables:
  - {env: JAR, name: Jar, default: server.jar, editable: true, rules: {pattern: '^[a-z.]+$'}}
install: {script: "echo hi"}
resources: {memory_mb: 1024, cpus: 1}
`

func TestValidationRejectsUnsafeBlueprints(t *testing.T) {
	if _, err := Parse([]byte(minimal)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"unknown field":     minimal + "surprise: 1\n",
		"unknown variable":  strings.Replace(minimal, "{{JAR}}", "{{NOPE}}", 1),
		"multiline startup": strings.Replace(minimal, `command: "java`, `command: "echo\njava`, 1),
		"reserved variable": strings.Replace(minimal, "env: JAR", "env: SERVER_PORT", 1),
		"bad default":       strings.Replace(minimal, "default: server.jar", "default: Server.JAR", 1),
		"bad image":         strings.Replace(minimal, "eclipse-temurin:21-jre-alpine", "evil image;rm", 1),
		"escaping path":     minimal + "config_files: [{path: ../../etc/passwd, format: lines, replace: [{match: x, with: y}]}]\n",
		"absolute path":     minimal + "config_files: [{path: /etc/passwd, format: properties, set: {a: b}}]\n",
		"bad agreement url": minimal + "agreements: [{id: eula, text: ok, url: 'http://x', file: eula.txt, content: x}]\n",
		"unknown provider":  strings.Replace(minimal, `install: {script: "echo hi"}`, `install: {download: {provider: nope, version: x, dest: a.jar}}`, 1),
		"no install":        strings.Replace(minimal, `install: {script: "echo hi"}`, `install: {}`, 1),
	}
	for name, y := range bad {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVariableRules(t *testing.T) {
	min, max := int64(1), int64(10)
	v := Variable{Env: "N", Name: "n", Rules: Rules{Type: "int", Min: &min, Max: &max}}
	for val, ok := range map[string]bool{"5": true, "0": false, "11": false, "05": false, "x": false, "": false} {
		if (v.Check(val) == nil) != ok {
			t.Errorf("int %q: ok=%v", val, !ok)
		}
	}
	e := Variable{Env: "E", Name: "e", Rules: Rules{Type: "enum", Options: []string{"a", "b"}}}
	if e.Check("a") != nil || e.Check("c") == nil {
		t.Error("enum rules")
	}
	s := Variable{Env: "S", Name: "s", Rules: Rules{Type: "string"}}
	if s.Check("one\ntwo") == nil || s.Check(strings.Repeat("x", 300)) == nil {
		t.Error("strings must be one bounded line")
	}
}

func TestStartupUsesEnvironmentExpansion(t *testing.T) {
	entry, argv := StartupArgv("java -Xmx{{SERVER_MEMORY}}M -jar {{ SERVER_JARFILE }} nogui")
	if strings.Join(entry, " ") != "/bin/sh -c" || argv[0] != "exec java -Xmx${SERVER_MEMORY}M -jar ${SERVER_JARFILE} nogui" {
		t.Fatalf("%v %v", entry, argv)
	}
}

func TestApplyProperties(t *testing.T) {
	in := "#Minecraft server properties\nmotd=Hello\nserver-port=25565\nserver-ip = 1.2.3.4\n"
	out, err := ApplyConfig([]byte(in), ConfigFile{Format: "properties", Set: map[string]string{
		"server-port": "{{SERVER_PORT}}", "server-ip": "", "query.port": "{{SERVER_PORT}}"}},
		map[string]string{"SERVER_PORT": "25570\nevil=1"})
	if err != nil {
		t.Fatal(err)
	}
	want := "#Minecraft server properties\nmotd=Hello\nserver-port=25570evil=1\nserver-ip=\nquery.port=25570evil=1\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
	created, _ := ApplyConfig(nil, ConfigFile{Format: "properties", Set: map[string]string{"server-port": "1"}}, nil)
	if string(created) != "server-port=1\n" {
		t.Fatalf("created %q", created)
	}
}

func TestApplyLines(t *testing.T) {
	in := "config-version = \"2.7\"\nbind = \"0.0.0.0:25577\"\nmotd = \"x\"\n"
	out, err := ApplyConfig([]byte(in), ConfigFile{Format: "lines", Replace: []LineReplace{{Match: `^bind\s*=`, With: `bind = "0.0.0.0:{{SERVER_PORT}}"`}}},
		map[string]string{"SERVER_PORT": "25600"})
	if err != nil || !strings.Contains(string(out), `bind = "0.0.0.0:25600"`) || !strings.Contains(string(out), `motd = "x"`) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestImageForJava(t *testing.T) {
	s := Spec{Images: []Image{{Label: "25", Java: 25}, {Label: "21", Java: 21}, {Label: "17", Java: 17}, {Label: "8", Java: 8}}}
	for need, want := range map[int]string{0: "25", 8: "8", 16: "17", 17: "17", 21: "21", 22: "25", 99: "25"} {
		if got := s.ImageForJava(need).Label; got != want {
			t.Errorf("java %d: got %s want %s", need, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := [][2]string{{"1.21.10", "1.21.9"}, {"1.21", "1.21-rc1"}, {"26.1", "1.21.11"}, {"1.20.4", "1.20"}}
	for _, c := range cases {
		if compareVersions(c[0], c[1]) <= 0 || compareVersions(c[1], c[0]) >= 0 {
			t.Errorf("%s should be newer than %s", c[0], c[1])
		}
	}
	vs := newestFirst([]string{"1.20.1", "1.21.11", "1.21.11-rc3", "1.21.9"}, true)
	if len(vs) != 3 || vs[0].ID != "1.21.11" || vs[2].ID != "1.20.1" {
		t.Fatalf("%+v", vs)
	}
}

func TestNeoForgeGameVersion(t *testing.T) {
	n := neoForge{}
	for in, want := range map[string]string{"21.1.209": "1.21.1", "20.4.237": "1.20.4", "21.0.1": "1.21", "26.3.0.45": "26.3", "26.1.2.3": "26.1.2"} {
		if got := n.gameVersion("", in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

// fakeProviders serves the subset of each provider API that is used.
func fakeProviders(t *testing.T, jar []byte) *Providers {
	t.Helper()
	sum := sha256.Sum256(jar)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/mc/game/version_manifest_v2.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"latest":{"release":"1.21.11"},"versions":[{"id":"1.21.11","type":"release","url":"%s/v/1.21.11.json"},{"id":"1.21.11-rc1","type":"snapshot","url":"x"}]}`, srv.URL)
	})
	mux.HandleFunc("/v/1.21.11.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"javaVersion":{"majorVersion":21},"downloads":{"server":{"sha1":"%x","size":%d,"url":"%s/jar"}}}`, sha1Sum(jar), len(jar), srv.URL)
	})
	mux.HandleFunc("/v3/projects/paper", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":{"1.21":["1.21.11","1.21.11-rc3","1.21.9"],"1.20":["1.20.6"]}}`)
	})
	mux.HandleFunc("/v3/projects/paper/versions/1.21.11/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"downloads":{"server:default":{"name":"paper.jar","size":%d,"url":"%s/jar","checksums":{"sha256":"%s"}}}}`, len(jar), srv.URL, hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("/v3/projects/velocity", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":{"4.0.0":["4.2.1-SNAPSHOT","4.2.0"],"3.0.0":["3.5.1"]}}`)
	})
	mux.HandleFunc("/v3/projects/velocity/versions/4.2.0", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":{"id":"4.2.0","java":{"version":{"minimum":25}}},"builds":[30]}`)
	})
	mux.HandleFunc("/v3/projects/velocity/versions/4.2.0/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"downloads":{"server:default":{"name":"velocity.jar","size":%d,"url":"%s/jar","checksums":{"sha256":"%s"}}}}`, len(jar), srv.URL, hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("/jar", func(w http.ResponseWriter, r *http.Request) { w.Write(jar) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ep := Endpoints{Mojang: srv.URL, Paper: srv.URL, Purpur: srv.URL, Fabric: srv.URL, Forge: srv.URL, ForgeMaven: srv.URL, NeoForge: srv.URL}
	return &Providers{HTTP: srv.Client(), Endpoints: ep}
}

func TestProvidersResolveAndVerify(t *testing.T) {
	jar := []byte("PK fake server jar")
	p := fakeProviders(t, jar)
	ctx := context.Background()

	vs, err := p.Versions(ctx, "papermc", "paper")
	if err != nil || len(vs) != 3 || vs[0].ID != "1.21.11" {
		t.Fatalf("paper versions %+v %v", vs, err)
	}
	a, err := p.Resolve(ctx, Download{Provider: Provider{Name: "papermc", Project: "paper"}, Version: "{{V}}"}, map[string]string{"V": "latest"})
	if err != nil || a.Version != "1.21.11" || a.SHA256 == "" {
		t.Fatalf("paper resolve %+v %v", a, err)
	}
	var buf bytes.Buffer
	if _, err := p.Fetch(ctx, a, &buf); err != nil || !bytes.Equal(buf.Bytes(), jar) {
		t.Fatalf("fetch %v", err)
	}
	a.SHA256 = strings.Repeat("0", 64)
	if _, err := p.Fetch(ctx, a, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered download accepted: %v", err)
	}

	v, err := p.Resolve(ctx, Download{Provider: Provider{Name: "minecraft-vanilla"}, Version: "latest"}, nil)
	if err != nil || v.SHA1 == "" || v.JavaHint != 21 {
		t.Fatalf("vanilla %+v %v", v, err)
	}
	if got := p.JavaFor(ctx, "papermc", "paper", "1.21.11"); got != 21 {
		t.Fatalf("java for 1.21.11 = %d", got)
	}
	if got := p.JavaFor(ctx, "papermc", "velocity", "3.4.0"); got != 0 {
		t.Fatalf("proxies have no Minecraft version: %d", got)
	}
	if _, err := p.Resolve(ctx, Download{Provider: Provider{Name: "papermc", Project: "paper"}, Version: "../../etc"}, nil); err == nil {
		t.Fatal("path-like version accepted")
	}
	if _, err := p.Resolve(ctx, Download{Provider: Provider{Name: "minecraft-vanilla"}, Version: "9.9.9"}, nil); err != ErrUnknownVersion {
		t.Fatalf("unknown version: %v", err)
	}
}

func sha1Sum(b []byte) []byte {
	h := newSHA1()
	h.Write(b)
	return h.Sum(nil)
}
