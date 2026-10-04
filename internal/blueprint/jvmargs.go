package blueprint

import (
	"fmt"
	"path"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// JVMArgsVar is the panel-provided variable holding a server's extra JVM
// options. A blueprint opts in with startup.jvm_args and places
// {{SERVER_JVM_ARGS}} unquoted in its command (right after `java`), so the
// shell splits the options into separate words.
const JVMArgsVar = "SERVER_JVM_ARGS"

// MaxJVMArgsLen bounds a server's JVM options.
const MaxJVMArgsLen = 2048

const maxJVMTokenLen = 512

// The shell splits ${SERVER_JVM_ARGS} into words and would expand glob
// characters, so every option is limited to characters that have no meaning
// to the shell after expansion: no quotes, $, `, ;, &, |, <, >, *, ?, [, ], ~,
// braces, parentheses, backslashes or line breaks.
var (
	jvmTokenRe   = lazyre.New(`^[A-Za-z0-9._:+=,/@%-]+$`)
	jvmXXFlagRe  = lazyre.New(`^-XX:[+-][A-Za-z][A-Za-z0-9_]{0,127}$`)
	jvmXXValueRe = lazyre.New(`^-XX:([A-Za-z][A-Za-z0-9_]{0,127})=[A-Za-z0-9._:+,/@%=-]+$`)
	jvmXRe       = lazyre.New(`^-X[a-z][A-Za-z0-9._:+=,/@%-]*$`)
	jvmDRe       = lazyre.New(`^-D[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}(=[A-Za-z0-9._:+=,/@%-]*)?$`)
	jvmModuleRe  = lazyre.New(`^--(add-opens|add-exports|add-reads|add-modules|enable-native-access)=[A-Za-z0-9._/=,-]+$`)
	jvmAssertRe  = lazyre.New(`^-(ea|da|esa|dsa|enableassertions|disableassertions)(:[A-Za-z0-9._]+(\.\.\.)?)?$`)
	jvmVerboseRe = lazyre.New(`^-verbose:(gc|class|module|jni)$`)
)

// -XX options that size the heap. The panel sets the heap from the server's
// memory limit (SERVER_MEMORY).
var jvmHeapOptions = map[string]bool{
	"MaxHeapSize": true, "InitialHeapSize": true, "MinHeapSize": true, "MaxRAM": true,
	"MaxRAMPercentage": true, "InitialRAMPercentage": true, "MinRAMPercentage": true,
	"MaxRAMFraction": true, "InitialRAMFraction": true, "MinRAMFraction": true,
}

// -XX options that run commands or read more options from a file.
var jvmRefusedXX = map[string]string{
	"OnError":            "runs a command",
	"OnOutOfMemoryError": "runs a command",
	"Flags":              "reads options from a file",
	"VMOptionsFile":      "reads options from a file",
}

// CheckJVMArgs validates a server's extra JVM options and returns them
// normalized (single spaces). "" means none. Every option must be a known
// JVM option shape; the heap size, class path, main jar and native agents
// cannot be changed.
func CheckJVMArgs(s string) (string, error) {
	if len(s) > MaxJVMArgsLen {
		return "", fmt.Errorf("JVM arguments must be at most %d characters", MaxJVMArgsLen)
	}
	if strings.ContainsAny(s, "\n\r\x00") {
		return "", fmt.Errorf("JVM arguments must be on one line")
	}
	tokens := strings.Fields(s)
	for _, t := range tokens {
		if err := checkJVMToken(t); err != nil {
			return "", err
		}
	}
	return strings.Join(tokens, " "), nil
}

func checkJVMToken(t string) error {
	if len(t) > maxJVMTokenLen {
		return fmt.Errorf("%.40q… is longer than %d characters", t, maxJVMTokenLen)
	}
	if !jvmTokenRe.MatchString(t) {
		for _, r := range t {
			if !strings.ContainsRune("._:+=,/@%-", r) && !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
				return fmt.Errorf("%q contains %q; JVM arguments may only use letters, digits and . _ : + = , / @ %% -", t, r)
			}
		}
	}
	if !strings.HasPrefix(t, "-") {
		return fmt.Errorf("%q is not a JVM option (options start with -)", t)
	}
	low := strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "-Xmx") || strings.HasPrefix(t, "-Xms"):
		return fmt.Errorf("%s: the heap size is set by the panel from the server's memory limit; change the memory instead", t)
	case t == "-jar" || t == "-cp" || t == "-classpath" || t == "--class-path" || strings.HasPrefix(t, "--class-path=") ||
		t == "-m" || t == "--module" || strings.HasPrefix(t, "--module=") || t == "-p" || t == "--module-path" ||
		strings.HasPrefix(t, "--module-path=") || strings.HasPrefix(t, "--upgrade-module-path") || strings.HasPrefix(t, "-Xbootclasspath"):
		return fmt.Errorf("%s: the server jar and class path are set by the server type and cannot be changed here", t)
	case strings.HasPrefix(low, "-agentlib") || strings.HasPrefix(low, "-agentpath"):
		return fmt.Errorf("%s: native agents are not allowed; use -javaagent with a jar in the server's files", t)
	case strings.HasPrefix(t, "-javaagent:"):
		return checkJavaAgent(t)
	case strings.HasPrefix(t, "-XX:"):
		name := strings.TrimPrefix(t, "-XX:")
		name = strings.TrimLeft(name, "+-")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if jvmHeapOptions[name] {
			return fmt.Errorf("%s: the heap size is set by the panel from the server's memory limit; change the memory instead", t)
		}
		if why, ok := jvmRefusedXX[name]; ok {
			return fmt.Errorf("%s is not allowed (it %s)", t, why)
		}
		if jvmXXFlagRe.MatchString(t) || jvmXXValueRe.MatchString(t) {
			return nil
		}
		return fmt.Errorf("%q is not a valid -XX option (-XX:+Name, -XX:-Name or -XX:Name=value)", t)
	case strings.HasPrefix(t, "-D"):
		if jvmDRe.MatchString(t) {
			return nil
		}
		return fmt.Errorf("%q is not a valid system property (-Dname=value)", t)
	case strings.HasPrefix(t, "-X"):
		if jvmXRe.MatchString(t) {
			return nil
		}
	case strings.HasPrefix(t, "--"):
		if jvmModuleRe.MatchString(t) || t == "--enable-preview" {
			return nil
		}
	case t == "-server" || jvmAssertRe.MatchString(t) || jvmVerboseRe.MatchString(t):
		return nil
	}
	return fmt.Errorf("%q is not a JVM option RivetPanel accepts (allowed: -XX:…, -X…, -D…, --add-opens=…, --add-exports=…, --add-modules=…, --enable-preview, -javaagent:<jar in the server's files>)", t)
}

// checkJavaAgent allows -javaagent:<jar>[=options] when the jar is inside
// the server's files (/workspace, the working directory).
func checkJavaAgent(t string) error {
	rest := strings.TrimPrefix(t, "-javaagent:")
	p := rest
	if i := strings.IndexByte(rest, '='); i >= 0 {
		p = rest[:i]
	}
	bad := fmt.Errorf("%s: the agent must be a .jar inside the server's files (for example -javaagent:plugins/agent.jar)", t)
	if !strings.HasSuffix(strings.ToLower(p), ".jar") {
		return bad
	}
	if strings.HasPrefix(p, "/") {
		if !strings.HasPrefix(p, "/workspace/") {
			return bad
		}
		p = strings.TrimPrefix(p, "/workspace/")
	}
	if p == "" || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return bad
	}
	return nil
}

// JVMPreset is a one-click set of JVM options offered on the Startup page.
type JVMPreset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Args    string `json:"args"`
	MinJava int    `json:"min_java,omitempty"`
	Note    string `json:"note,omitempty"`
}

// aikarLargeHeapMiB is the heap above which Aikar's flags use the larger G1
// region and reserve settings (12 GB in the published guidance).
const aikarLargeHeapMiB = 12 * 1024

// AikarFlags returns Aikar's widely used G1 tuning for Minecraft servers.
// heapMiB is the heap the panel passes as SERVER_MEMORY; above 12 GB the
// documented large-heap values are used.
func AikarFlags(heapMiB int64) string {
	newSize, maxNew, region, reserve, ihop := "30", "40", "8M", "20", "15"
	if heapMiB > aikarLargeHeapMiB {
		newSize, maxNew, region, reserve, ihop = "40", "50", "16M", "15", "20"
	}
	return strings.Join([]string{
		"-XX:+UseG1GC",
		"-XX:+ParallelRefProcEnabled",
		"-XX:MaxGCPauseMillis=200",
		"-XX:+UnlockExperimentalVMOptions",
		"-XX:+DisableExplicitGC",
		"-XX:+AlwaysPreTouch",
		"-XX:G1NewSizePercent=" + newSize,
		"-XX:G1MaxNewSizePercent=" + maxNew,
		"-XX:G1HeapRegionSize=" + region,
		"-XX:G1ReservePercent=" + reserve,
		"-XX:G1HeapWastePercent=5",
		"-XX:G1MixedGCCountTarget=4",
		"-XX:InitiatingHeapOccupancyPercent=" + ihop,
		"-XX:G1MixedGCLiveThresholdPercent=90",
		"-XX:G1RSetUpdatingPauseTimePercent=5",
		"-XX:SurvivorRatio=32",
		"-XX:+PerfDisableSharedMem",
		"-XX:MaxTenuringThreshold=1",
		"-Dusing.aikars.flags=https://mcflags.emc.gs",
		"-Daikars.new.flags=true",
	}, " ")
}

// ZGCFlags returns generational ZGC options for the given Java version:
// Java 21-23 need -XX:+ZGenerational, from Java 24 it is the only mode (the
// option is ignored with a warning). java 0 = unknown.
func ZGCFlags(java int) string {
	args := []string{"-XX:+UseZGC"}
	if java >= 21 && java < 24 {
		args = append(args, "-XX:+ZGenerational")
	}
	return strings.Join(append(args, "-XX:+AlwaysPreTouch", "-XX:+DisableExplicitGC", "-XX:+PerfDisableSharedMem"), " ")
}

// JVMPresets are the presets for a server with the given Java version
// (0 = unknown) and heap in MiB.
func JVMPresets(java int, heapMiB int64) []JVMPreset {
	aikarNote := "G1 tuning for Minecraft servers from Aikar's published guidance."
	if heapMiB > aikarLargeHeapMiB {
		aikarNote += " Uses the values for heaps above 12 GB."
	}
	return []JVMPreset{
		{ID: "aikar", Name: "Aikar's flags", Args: AikarFlags(heapMiB), MinJava: 8, Note: aikarNote},
		{ID: "zgc", Name: "ZGC (Java 21+, large heaps)", Args: ZGCFlags(java), MinJava: 21,
			Note: "Generational ZGC: very short pauses, best with large heaps (8 GB or more); uses more memory than G1."},
		{ID: "none", Name: "None", Args: ""},
	}
}
