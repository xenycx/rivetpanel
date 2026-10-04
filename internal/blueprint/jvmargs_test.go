package blueprint

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/blueprints"
)

func TestCheckJVMArgsAcceptsPresetsAndCommonOptions(t *testing.T) {
	for _, java := range []int{0, 17, 21, 23, 25} {
		for _, heap := range []int64{512, 12 * 1024, 16 * 1024} {
			for _, p := range JVMPresets(java, heap) {
				norm, err := CheckJVMArgs(p.Args)
				if err != nil || norm != p.Args {
					t.Errorf("preset %s (java %d, heap %d): %q %v", p.ID, java, heap, norm, err)
				}
			}
		}
	}
	ok := []string{
		"",
		"   ",
		"-XX:+UseG1GC  -XX:MaxGCPauseMillis=130\t-Dfile.encoding=UTF-8",
		"-Xss1M -Xlog:gc:file=logs/gc.log -Xshare:off",
		"-Dterminal.jline=false -Dpaper.disableWatchdog -Dlog4j2.formatMsgNoLookups=true",
		"--add-opens=java.base/java.lang=ALL-UNNAMED --add-modules=jdk.incubator.vector --enable-preview",
		"-javaagent:plugins/agent.jar -javaagent:/workspace/lib/a.jar=port=9000",
		"-XX:ErrorFile=./hs_err_pid%p.log -ea -server -verbose:gc",
	}
	for _, s := range ok {
		if _, err := CheckJVMArgs(s); err != nil {
			t.Errorf("%q refused: %v", s, err)
		}
	}
	if n, _ := CheckJVMArgs("  -XX:+UseG1GC \t -Da=b "); n != "-XX:+UseG1GC -Da=b" {
		t.Errorf("normalized %q", n)
	}
}

func TestCheckJVMArgsRefusesInjectionAndManagedOptions(t *testing.T) {
	bad := map[string]string{
		"-Xmx4G":                             "heap size",
		"-XX:+UseG1GC -Xms2G":                "heap size",
		"-XX:MaxRAMPercentage=90":            "heap size",
		"-XX:MaxHeapSize=8g":                 "heap size",
		"-jar evil.jar":                      "server jar",
		"-cp /tmp":                           "class path",
		"--module-path=/x":                   "class path",
		"-Xbootclasspath/a:x.jar":            "class path",
		"-agentpath:/tmp/x.so":               "native agents",
		"-agentlib:jdwp=transport=dt_socket": "native agents",
		"-XX:OnError=sh":                     "runs a command",
		"-XX:OnOutOfMemoryError=kill":        "runs a command",
		"-XX:VMOptionsFile=opts":             "reads options",
		"-Da=b;rm":                           "contains",
		"-Da=b && reboot":                    "contains",
		"-Da=b|nc":                           "contains",
		"-Da=$(id)":                          "contains",
		"-Da=${HOME}":                        "contains",
		"-Da=`id`":                           "contains",
		"-Da='x'":                            "contains",
		`-Da="x"`:                            "contains",
		"-Da=b >out":                         "contains",
		"-Da=b <in":                          "contains",
		"-Xlog:gc*":                          "contains",
		"-Da=?":                              "contains",
		"-Da=[ab]":                           "contains",
		"-Da=~":                              "contains",
		`-Da=\x`:                             "contains",
		"-Da=b\n-Dc=d":                       "one line",
		"rm -rf /":                           "not a JVM option",
		"@argfile":                           "not a JVM option",
		"-XX:Bad Name":                       "not a",
		"-XX:+":                              "not a valid",
		"--illegal-thing=1":                  "not a JVM option",
		"-javaagent:../x.jar":                "inside the server's files",
		"-javaagent:/etc/x.jar":              "inside the server's files",
		"-javaagent:/workspace/../x.jar":     "inside the server's files",
		"-javaagent:agent.so":                "inside the server's files",
		"-D=x":                               "system property",
	}
	for in, want := range bad {
		_, err := CheckJVMArgs(in)
		if err == nil {
			t.Errorf("%q accepted", in)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %q does not mention %q", in, err, want)
		}
	}
	if _, err := CheckJVMArgs(strings.Repeat("-Da=b ", 400)); err == nil {
		t.Error("over-long arguments accepted")
	}
}

func TestJVMPresets(t *testing.T) {
	small, large := AikarFlags(4096), AikarFlags(16*1024)
	if !strings.Contains(small, "-XX:G1HeapRegionSize=8M") || !strings.Contains(small, "-XX:G1NewSizePercent=30") {
		t.Errorf("small heap: %s", small)
	}
	for _, want := range []string{"-XX:G1HeapRegionSize=16M", "-XX:G1NewSizePercent=40", "-XX:G1MaxNewSizePercent=50",
		"-XX:G1ReservePercent=15", "-XX:InitiatingHeapOccupancyPercent=20"} {
		if !strings.Contains(large, want) {
			t.Errorf("large heap lacks %s: %s", want, large)
		}
	}
	if !strings.Contains(ZGCFlags(21), "-XX:+ZGenerational") || strings.Contains(ZGCFlags(25), "ZGenerational") {
		t.Errorf("ZGC flags: %s / %s", ZGCFlags(21), ZGCFlags(25))
	}
	ps := JVMPresets(25, 2048)
	if len(ps) != 3 || ps[0].ID != "aikar" || ps[1].MinJava != 21 || ps[2].Args != "" {
		t.Fatalf("presets %+v", ps)
	}
}

const jvmMinimal = `slug: java-test
name: Java test
category: Tests
runtime: java
images: [{label: "Java 21", ref: "eclipse-temurin:21-jre-alpine", java: 21}]
startup:
  command: "java -Xmx{{SERVER_MEMORY}}M {{SERVER_JVM_ARGS}} -jar server.jar"
  jvm_args: true
install: {script: "echo hi"}
resources: {memory_mb: 1024, cpus: 1}
`

func TestBlueprintJVMArgsFlag(t *testing.T) {
	if _, err := Parse([]byte(jvmMinimal)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"flag without placeholder": strings.Replace(jvmMinimal, "{{SERVER_JVM_ARGS}} ", "", 1),
		"placeholder without flag": strings.Replace(jvmMinimal, "  jvm_args: true\n", "", 1),
		"declared as variable":     jvmMinimal + "variables: [{env: SERVER_JVM_ARGS, name: x, default: ''}]\n",
		"unsafe default":           strings.Replace(jvmMinimal, "jvm_args: true", "jvm_args: true\n  jvm_args_default: '-Da=$(id)'", 1),
		"heap default":             strings.Replace(jvmMinimal, "jvm_args: true", "jvm_args: true\n  jvm_args_default: -Xmx1G", 1),
		"default without flag":     strings.Replace(strings.Replace(jvmMinimal, "jvm_args: true", "jvm_args_default: -XX:+UseG1GC", 1), "{{SERVER_JVM_ARGS}} ", "", 1),
	}
	for name, y := range bad {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Every built-in Minecraft type takes JVM arguments right after the heap
// options, before the jar or launcher arguments.
func TestBuiltinJavaBlueprintsTakeJVMArgs(t *testing.T) {
	all, err := LoadBuiltin(blueprints.FS)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, b := range all {
		if !strings.HasPrefix(b.Spec.Slug, "minecraft-") {
			continue
		}
		n++
		st := b.Spec.Startup
		if !st.JVMArgs || !strings.HasPrefix(st.Command, "java -Xms128M -Xmx{{SERVER_MEMORY}}M {{SERVER_JVM_ARGS}} ") {
			t.Errorf("%s: %q (jvm_args %v)", b.Spec.Slug, st.Command, st.JVMArgs)
		}
		if strings.Contains(st.Command, "-XX:+Use") {
			t.Errorf("%s: a collector in the command conflicts with presets that choose another", b.Spec.Slug)
		}
	}
	if n != 8 {
		t.Fatalf("%d Minecraft types", n)
	}
}

// The rendered command splits the options into separate arguments and never
// re-parses them as shell code.
func TestJVMArgsRenderAsSeparateWords(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	_, argv := StartupArgv("printf [%s] -Xmx{{SERVER_MEMORY}}M {{SERVER_JVM_ARGS}} -jar x.jar")
	if !strings.Contains(argv[0], " ${SERVER_JVM_ARGS} ") {
		t.Fatalf("argv %q", argv[0])
	}
	run := func(args string) string {
		cmd := exec.Command("/bin/sh", "-c", argv[0])
		cmd.Dir = t.TempDir()
		cmd.Env = []string{"PATH=/usr/bin:/bin", "SERVER_MEMORY=512", "SERVER_JVM_ARGS=" + args}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	args, err := CheckJVMArgs(AikarFlags(1024))
	if err != nil {
		t.Fatal(err)
	}
	want := "[-Xmx512M]"
	for _, a := range strings.Fields(args) {
		want += "[" + a + "]"
	}
	if got := run(args); got != want+"[-jar][x.jar]" {
		t.Fatalf("got %s", got)
	}
	if got := run(""); got != "[-Xmx512M][-jar][x.jar]" {
		t.Fatalf("empty: %s", got)
	}
}
