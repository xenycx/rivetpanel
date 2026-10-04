//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const cgroupLine = `mem=%s cpu=%s pids=%s ro=%s sock=%s ws=%s`

type lang struct {
	runtime string
	files   map[string]string
	prepare func(t *testing.T, s *stack, wsPath string) // optional out-of-band step
}

var langs = []lang{
	{runtime: "nodejs", files: map[string]string{"index.js": `
const fs = require('fs');
const r = p => { try { return fs.readFileSync(p, 'utf8').trim(); } catch (e) { return 'ERR'; } };
let ro = 'true'; try { fs.writeFileSync('/probe', 'x'); ro = 'false'; } catch (e) {}
let ws = 'false'; try { fs.writeFileSync('/workspace/written.txt', 'x'); ws = 'true'; } catch (e) {}
console.log('SMOKE lang=nodejs token=' + process.env.DISCORD_TOKEN + ' mem=' + r('/sys/fs/cgroup/memory.max') +
  ' cpu=' + r('/sys/fs/cgroup/cpu.max') + ' pids=' + r('/sys/fs/cgroup/pids.max') + ' ro=' + ro +
  ' sock=' + fs.existsSync('/var/run/docker.sock') + ' ws=' + ws);
setInterval(() => {}, 1000);
`}},
	{runtime: "python", files: map[string]string{"main.py": `
import os, time
def r(p):
    try: return open(p).read().strip()
    except Exception: return 'ERR'
def w(p):
    try:
        open(p, 'w').write('x'); return True
    except Exception: return False
print('SMOKE lang=python token=%s mem=%s cpu=%s pids=%s ro=%s sock=%s ws=%s' % (
    os.environ.get('DISCORD_TOKEN'), r('/sys/fs/cgroup/memory.max'), r('/sys/fs/cgroup/cpu.max'),
    r('/sys/fs/cgroup/pids.max'), str(not w('/probe')).lower(), str(os.path.exists('/var/run/docker.sock')).lower(),
    str(w('/workspace/written.txt')).lower()))
time.sleep(3600)
`}},
	{runtime: "ruby", files: map[string]string{"main.rb": `
$stdout.sync = true
def r(p); File.read(p).strip; rescue; 'ERR'; end
def w(p); File.write(p, 'x'); true; rescue; false; end
puts "SMOKE lang=ruby token=#{ENV['DISCORD_TOKEN']} mem=#{r('/sys/fs/cgroup/memory.max')} cpu=#{r('/sys/fs/cgroup/cpu.max')} pids=#{r('/sys/fs/cgroup/pids.max')} ro=#{!w('/probe')} sock=#{File.exist?('/var/run/docker.sock')} ws=#{w('/workspace/written.txt')}"
sleep
`}},
	{runtime: "go", files: map[string]string{
		"go.mod": "module smoke\n\ngo 1.24\n",
		"main.go": `package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func r(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "ERR"
	}
	return strings.TrimSpace(string(b))
}

func main() {
	ro := os.WriteFile("/probe", []byte("x"), 0o644) != nil
	ws := os.WriteFile("/workspace/written.txt", []byte("x"), 0o644) == nil
	_, serr := os.Stat("/var/run/docker.sock")
	fmt.Printf("SMOKE lang=go token=%s mem=%s cpu=%s pids=%s ro=%v sock=%v ws=%v\n", os.Getenv("DISCORD_TOKEN"),
		r("/sys/fs/cgroup/memory.max"), r("/sys/fs/cgroup/cpu.max"), r("/sys/fs/cgroup/pids.max"), ro, serr == nil, ws)
	time.Sleep(time.Hour)
}
`}},
	{runtime: "rust", files: map[string]string{
		"Cargo.toml": "[package]\nname = \"smoke\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/main.rs": `use std::fs;
fn r(p: &str) -> String { fs::read_to_string(p).map(|s| s.trim().to_string()).unwrap_or("ERR".into()) }
fn main() {
    let ro = fs::write("/probe", "x").is_err();
    let ws = fs::write("/workspace/written.txt", "x").is_ok();
    let sock = std::path::Path::new("/var/run/docker.sock").exists();
    println!("SMOKE lang=rust token={} mem={} cpu={} pids={} ro={} sock={} ws={}",
        std::env::var("DISCORD_TOKEN").unwrap_or_default(), r("/sys/fs/cgroup/memory.max"),
        r("/sys/fs/cgroup/cpu.max"), r("/sys/fs/cgroup/pids.max"), ro, sock, ws);
    loop { std::thread::sleep(std::time::Duration::from_secs(3600)); }
}
`}},
	{runtime: "java", files: map[string]string{"Main.java": `import java.nio.file.*;
public class Main {
    static String r(String p) { try { return Files.readString(Path.of(p)).trim(); } catch (Exception e) { return "ERR"; } }
    static boolean w(String p) { try { Files.writeString(Path.of(p), "x"); return true; } catch (Exception e) { return false; } }
    public static void main(String[] a) throws Exception {
        System.out.println("SMOKE lang=java token=" + System.getenv("DISCORD_TOKEN") + " mem=" + r("/sys/fs/cgroup/memory.max") +
            " cpu=" + r("/sys/fs/cgroup/cpu.max") + " pids=" + r("/sys/fs/cgroup/pids.max") + " ro=" + !w("/probe") +
            " sock=" + Files.exists(Path.of("/var/run/docker.sock")) + " ws=" + w("/workspace/written.txt"));
        Thread.sleep(3600000);
    }
}
`}, prepare: func(t *testing.T, s *stack, ws string) {
		// The java recipe ships a prebuilt jar; compile it with the (approved) JDK image.
		rt, _ := s.cat.Get("java")
		args := []string{"run", "--rm", "--network", "none"}
		if os.Getenv("RIVET_TEST_ROOTLESS") != "1" {
			// A rootful daemon would create root-owned files the unprivileged test
			// process cannot chown; run as the test user (rootless maps root to us).
			args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-e", "HOME=/w")
		}
		args = append(args, "-v", ws+":/w", "-w", "/w", rt.BuilderImage,
			"sh", "-c", "javac Main.java && printf 'Main-Class: Main\\n' > m.txt && jar cfm app.jar m.txt Main.class")
		cmd := exec.Command("docker", args...)
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+os.Getenv("RIVET_TEST_DOCKER_HOST"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("java build: %v\n%s", err, out)
		}
	}},
}

// TestLanguageSmoke builds (when the recipe has a build stage) and runs a tiny
// program for every runtime, then checks from INSIDE the container that the
// environment arrived, limits are applied by the kernel, the root filesystem is
// read-only, the workspace is writable, and no Docker socket is present.
func TestLanguageSmoke(t *testing.T) {
	only := os.Getenv("RIVET_TEST_ONLY")
	for _, l := range langs {
		l := l
		if only != "" && !strings.Contains(only, l.runtime) {
			continue
		}
		t.Run(l.runtime, func(t *testing.T) {
			s := newStack(t)
			b := s.createBot(l.runtime)
			for name, content := range l.files {
				s.write(b, name, content)
			}
			if l.prepare != nil {
				p, err := s.ws.Path(b.ID)
				if err != nil {
					t.Fatal(err)
				}
				l.prepare(t, s, p)
			}
			if err := s.bots.SetEnv(s.ctx, s.user, b.ID, map[string]string{"DISCORD_TOKEN": "smoke-token"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.bots.Start(s.ctx, s.user, b.ID); err != nil {
				t.Fatal(err)
			}
			cur := s.waitObserved(b.ID, "running", 12*time.Minute)
			out := s.waitLog(*cur.ContainerID, "SMOKE lang="+l.runtime, 60*time.Second)
			line := ""
			for _, ln := range strings.Split(out, "\n") {
				if strings.HasPrefix(ln, "SMOKE ") {
					line = ln
				}
			}
			cpu := fmt.Sprintf("%d 100000", b.NanoCPUs/10000)
			for _, want := range []string{
				"token=smoke-token",
				"mem=" + fmt.Sprint(b.MemoryBytes),
				"cpu=" + cpu,
				"pids=" + fmt.Sprint(b.PidsLimit),
				"ro=true", "sock=false", "ws=true",
			} {
				if !strings.Contains(line, want) {
					t.Errorf("%s: %q missing %q", l.runtime, line, want)
				}
			}
			// The workspace write is visible on the host.
			p, _ := s.ws.Path(b.ID)
			if _, err := os.Stat(p + "/written.txt"); err != nil {
				t.Errorf("workspace write not visible on host: %v", err)
			}
			// Builders must not survive.
			for _, c := range s.containers(b.ID) {
				if c.Role() == "builder" {
					t.Error("builder container left behind")
				}
			}
			t.Log(line)
		})
	}
}
