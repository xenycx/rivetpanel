package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestSteamCMDScript(t *testing.T) {
	st := blueprint.SteamCMD{AppID: 896660, Validate: true, MaxSizeGB: 10, TimeoutMinutes: 30, Image: blueprint.DefaultSteamCMDImage}
	s, err := SteamCMDScript(st, "public-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"+login anonymous +app_update 896660 -beta public-test validate +quit",
		"+force_install_dir /workspace",
		"LIMIT_KB=10485760",
		"export HOME=/workspace/.steamcmd",
		"Success! App '896660'",
		"/workspace/.steam/sdk$a/steamclient.so",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "+login anonymous +app_update 896660 +quit") {
		t.Error("beta/validate missing")
	}
	for _, beta := range []string{"x +login me pass", "$(id)", "a;b", "-beta"} {
		if _, err := SteamCMDScript(st, beta); err == nil {
			t.Errorf("beta %q accepted", beta)
		}
	}
	if _, err := SteamCMDScript(blueprint.SteamCMD{AppID: 0}, ""); err == nil {
		t.Error("app id 0 accepted")
	}
	rt, err := steamRuntime(st, "")
	if err != nil || rt.BuilderImage != blueprint.DefaultSteamCMDImage || rt.BuildTimeout.Minutes() != 30 || rt.BuildArgv[0] != "/bin/sh" {
		t.Fatalf("runtime: %+v %v", rt, err)
	}
}

func TestACFValue(t *testing.T) {
	acf := []byte("\"AppState\"\n{\n\t\"appid\"\t\t\"896660\"\n\t\"buildid\"\t\t\"20481234\"\n\t\"name\"\t\t\"x y\"\n}\n")
	if v := ACFValue(acf, "buildid"); v != "20481234" {
		t.Fatalf("buildid = %q", v)
	}
	if v := ACFValue(acf, "name"); v != "" {
		t.Fatalf("name with spaces should not parse: %q", v)
	}
	if v := ACFValue([]byte("\"buildid\" \"12;rm\""), "buildid"); v != "" {
		t.Fatalf("unsafe value returned: %q", v)
	}
}

func TestGameEnvExtraPortsAndInstallEnv(t *testing.T) {
	spec, err := blueprint.Parse([]byte(`slug: steam-test
name: Steam test
category: Tests
images: [{label: "SteamCMD", ref: "steamcmd/steamcmd:latest"}]
startup: {command: "./server -port {{SERVER_PORT}} -query {{SERVER_PORT_1}} {{NAME}}"}
variables:
  - {env: NAME, name: Name, default: "x", editable: true}
install: {steamcmd: {app_id: 1007}, script: "echo $NAME"}
resources: {memory_mb: 1024, cpus: 1}
ports: {extra: 2}
`))
	if err != nil {
		t.Fatal(err)
	}
	bot := domain.Bot{ID: "b1", MemoryBytes: 1 << 30, Allocations: []domain.Allocation{
		{Port: 30010}, {Port: 30000, Primary: true}, {Port: 30005},
	}}
	env := gameEnv(spec, bot)
	if env["SERVER_PORT"] != "30000" || env["SERVER_PORT_1"] != "30005" || env["SERVER_PORT_2"] != "30010" {
		t.Fatalf("ports: %v", env)
	}
	vars := gameVars(spec, bot, map[string]string{"NAME": "mine", "RIVET_AGREEMENT_X": "accepted", "OTHER": "secret"})
	ie := installEnv(spec, vars)
	if ie["NAME"] != "mine" || ie["SERVER_PORT_1"] != "30005" {
		t.Fatalf("install env: %v", ie)
	}
	if _, ok := ie["RIVET_AGREEMENT_X"]; ok {
		t.Fatal("hidden agreement passed to the install script")
	}
	if _, ok := ie["OTHER"]; ok {
		t.Fatal("undeclared variable passed to the install script")
	}
}

func TestGameEnvJVMArgs(t *testing.T) {
	spec, err := blueprint.Parse([]byte(`slug: java-test
name: Java test
category: Tests
runtime: java
images: [{label: "Java 21", ref: "eclipse-temurin:21-jre-alpine", java: 21}]
startup: {command: "java -Xmx{{SERVER_MEMORY}}M {{SERVER_JVM_ARGS}} -jar server.jar", jvm_args: true}
install: {script: "echo hi"}
resources: {memory_mb: 1024, cpus: 1}
`))
	if err != nil {
		t.Fatal(err)
	}
	bot := domain.Bot{ID: "b1", MemoryBytes: 1 << 30, JVMArgs: "-XX:+UseG1GC  -Da=b"}
	if env := gameEnv(spec, bot); env["SERVER_JVM_ARGS"] != "-XX:+UseG1GC -Da=b" {
		t.Fatalf("jvm args: %q", env["SERVER_JVM_ARGS"])
	}
	// A value that fails validation (written around the service) is dropped.
	bot.JVMArgs = "-Da=$(id)"
	if env := gameEnv(spec, bot); env["SERVER_JVM_ARGS"] != "" {
		t.Fatalf("unsafe value passed: %q", env["SERVER_JVM_ARGS"])
	}
	// The panel's value wins over a variable of the same name in the server's
	// own environment.
	bot.JVMArgs = ""
	vars := gameVars(spec, bot, map[string]string{"SERVER_JVM_ARGS": "-Da=$(id)"})
	if vars["SERVER_JVM_ARGS"] != "" {
		t.Fatalf("own variable leaked: %q", vars["SERVER_JVM_ARGS"])
	}
	if l := envList(map[string]string{"SERVER_JVM_ARGS": "-Da=$(id)"}, gameEnv(spec, bot)); !slices.Contains(l, "SERVER_JVM_ARGS=") {
		t.Fatalf("container env %v", l)
	}
	// Types without the setting do not get the variable.
	spec.Startup.JVMArgs = false
	if _, ok := gameEnv(spec, bot)["SERVER_JVM_ARGS"]; ok {
		t.Fatal("variable set for a type without JVM arguments")
	}
}
