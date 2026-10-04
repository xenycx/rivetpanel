// Command rivet-agent runs servers on a remote node for a RivetPanel control
// plane. It connects out to the panel (no inbound port is needed), runs the
// node's containers with its local Docker, and serves files and consoles to
// the panel over that authenticated connection.
//
//	rivet-agent enroll --panel https://panel.example.com --token rvt_enroll_...
//	rivet-agent serve
//	rivet-agent doctor
//	rivet-agent version
//
// The agent has full control of the node's Docker daemon, which is
// root-equivalent on that host. Treat the node and its state directory
// (which holds the node's private key) accordingly.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/agentclient"
	"github.com/xenycx/rivetpanel/internal/agentnode"
	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/runtimes"
	"github.com/xenycx/rivetpanel/internal/telemetry"
	rtdefaults "github.com/xenycx/rivetpanel/runtimes"
)

var version = "dev"

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "enroll":
		err = enroll(os.Args[2:])
	case "serve":
		err = serve(log, os.Args[2:])
	case "doctor":
		err = doctor(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("rivet-agent", version, "protocol", agentproto.Version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rivet-agent:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  rivet-agent enroll --panel URL --token TOKEN [--dir DIR] [--connect HOST:PORT]
  rivet-agent serve [--dir DIR] [--data DIR] [--docker HOST] [--user UID:GID] [--network NAME] [--allow-shared-uid]
  rivet-agent doctor [--dir DIR]
  rivet-agent version`)
}

func dirFlag(fs *flag.FlagSet) *string {
	return fs.String("dir", env("RIVET_AGENT_DIR", "/var/lib/rivet-agent"), "state directory (node key, certificate, settings)")
}

func enroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	dir := dirFlag(fs)
	panel := fs.String("panel", "", "the panel's public address, for example https://panel.example.com")
	token := fs.String("token", "", "the one-use enrollment token from the panel")
	connect := fs.String("connect", "", "override the agent listener address the panel announces (HOST:PORT)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *panel == "" || *token == "" {
		return errors.New("--panel and --token are required")
	}
	if _, err := os.Stat(filepath.Join(*dir, agentclient.IdentityFile)); err == nil {
		return fmt.Errorf("%s already holds a node identity; remove it first to enroll again", *dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := agentclient.Enroll(ctx, nil, *panel, *token, *dir, *connect)
	if err != nil {
		return err
	}
	fmt.Printf("Enrolled as node %s. The agent connects to %s.\nStart it with: rivet-agent serve --dir %s\n", cfg.NodeID, cfg.Connect, *dir)
	return nil
}

func capabilities(ctx context.Context, dk *docker.Adapter, files *filesystem.Manager) agentproto.Capabilities {
	c := agentproto.Capabilities{CPUs: runtime.NumCPU(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if caps, err := dk.Capabilities(ctx); err == nil {
		c.DockerVersion, c.CgroupV2, c.MemoryLimit, c.CPUQuota, c.PidsLimit, c.Rootless = caps.ServerVersion, caps.CgroupV2,
			caps.MemoryLimit, caps.CPUQuota, caps.PidsLimit, caps.Rootless
		c.Problem = caps.Problem
		if err := caps.Validate(); err != nil && c.Problem == "" {
			c.Problem = err.Error()
		}
	} else {
		c.Problem = "docker is not reachable"
	}
	if total, free, err := files.Statfs(); err == nil {
		c.DiskBytes, c.DiskFreeBytes = total, free
	}
	c.MemoryBytes = memTotal()
	return c
}

func memTotal() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var kb int64
	if _, err := fmt.Sscanf(string(b), "MemTotal: %d kB", &kb); err != nil {
		return 0
	}
	return kb << 10
}

func serve(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dir := dirFlag(fs)
	data := fs.String("data", env("RIVET_AGENT_DATA", "/var/lib/rivet-agent/servers"), "where server files live")
	dockerHost := fs.String("docker", env("RIVET_AGENT_DOCKER_HOST", "unix:///var/run/docker.sock"), "Docker endpoint")
	user := fs.String("user", env("RIVET_AGENT_CONTAINER_USER", "65532:65532"), "uid:gid servers run as inside their containers")
	owner := fs.String("workspace-owner", env("RIVET_AGENT_WORKSPACE_OWNER", ""), "uid:gid that owns server files on the host (default: --user)")
	network := fs.String("network", env("RIVET_AGENT_NETWORK", "bridge"), "Docker network for servers")
	workers := fs.Int("workers", 2, "servers reconciled in parallel")
	allowShared := fs.Bool("allow-shared-uid", os.Getenv("RIVET_AGENT_ALLOW_SHARED_UID") == "1",
		"when the agent is neither root nor holds CAP_CHOWN, run servers as the agent's own uid:gid (the uid that owns the node key) instead of refusing to start")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := agentclient.LoadConfig(*dir)
	if err != nil {
		return fmt.Errorf("%w (run rivet-agent enroll first)", err)
	}
	if uid, _, err := runner.ParseUser(*user); err != nil {
		return err
	} else if uid == 0 && os.Getenv("RIVET_AGENT_ALLOW_ROOT_CONTAINER_USER") != "1" {
		return errors.New("refusing to run servers as root inside their containers (set RIVET_AGENT_ALLOW_ROOT_CONTAINER_USER=1 to override)")
	}
	if err := os.MkdirAll(*data, 0o750); err != nil {
		return err
	}
	absData, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	files, err := filesystem.NewManager(absData)
	if err != nil {
		return fmt.Errorf("server files: %w", err)
	}
	defer files.Close()
	// A restore keeps the previous workspace until the panel confirms its
	// database update. If the agent stopped in that interval, roll the
	// unconfirmed swap back before any runner can start the server.
	if rolled, err := files.Recover(); err != nil {
		return fmt.Errorf("recover interrupted workspace changes: %w", err)
	} else if len(rolled) > 0 {
		log.Warn("rolled back interrupted remote restores or deployments", "servers", rolled)
	}
	// Explicit flags or environment are kept; otherwise an agent that is not
	// root and cannot chown runs servers as its own uid:gid so their files
	// stay writable (still nonroot inside the container).
	userSet, ownerSet := os.Getenv("RIVET_AGENT_CONTAINER_USER") != "", os.Getenv("RIVET_AGENT_WORKSPACE_OWNER") != ""
	fs.Visit(func(f *flag.Flag) {
		userSet = userSet || f.Name == "user"
		ownerSet = ownerSet || f.Name == "workspace-owner"
	})
	ownership := runner.ChooseOwnership(*user, *owner, userSet, ownerSet, runner.DefaultOwnershipProbe(files.ProbeOwnership))
	// The shared uid also owns the node key and certificate: only a
	// development setup (RIVET_ENV=development) falls back automatically.
	production := strings.ToLower(strings.TrimSpace(os.Getenv("RIVET_ENV"))) != "development"
	ownership = runner.ApplySharedUIDPolicy(ownership, production, *allowShared)
	if err := ownership.RefusalError("the agent", "--allow-shared-uid (or RIVET_AGENT_ALLOW_SHARED_UID=1)", "--user and --workspace-owner"); err != nil {
		return err
	}
	*user, *owner = ownership.User, ownership.Owner
	if ownership.Fallback {
		log.Warn("the agent is not root and lacks CAP_CHOWN, so servers run as the agent's own user; they stay non-root inside "+
			"the container but share this uid, which owns the node key, on the host. For production run the agent as root (the systemd unit does) or pass --user",
			"user", ownership.User, "default", ownership.Default, "probe", ownership.Reason, "opted_in", ownership.OptedIn)
		if uid, gid, err := runner.ParseUser(ownership.Owner); err == nil {
			if _, failed, err := files.RepairOwnership(uid, gid); err == nil && len(failed) > 0 {
				log.Warn("some server folders belong to another user and cannot be repaired without root; run chown -R "+ownership.Owner+" on them",
					"servers", failed, "data", absData)
			}
		}
	}
	if uid, gid, err := runner.ParseUser(*owner); err == nil {
		files.SetOwner(uid, gid)
	}
	dk, err := docker.New(*dockerHost)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer dk.Close()
	cat, err := runtimes.Load(rtdefaults.FS)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	hostname, _ := os.Hostname()
	addonData := addons.DataRoot{Dir: filepath.Join(filepath.Dir(absData), "addons")}
	var (
		mu     sync.Mutex
		rn     *runner.Runner
		runErr = make(chan error, 1)
	)
	agent := agentclient.New(*dir, cfg, nil, log)
	installID := func() string {
		mu.Lock()
		defer mu.Unlock()
		return agent.Config.InstallID
	}
	startRunner := func() {
		mu.Lock()
		defer mu.Unlock()
		if rn != nil || agent.Config.InstallID == "" {
			return
		}
		r, err := runner.New(runner.Deps{Store: agent.Remote, Docker: dk, Env: agent.Remote, Workspaces: agentnode.Workspaces{Manager: files}, Catalog: cat, Log: log,
			Builds: agent.Remote}, runner.Options{NodeID: cfg.NodeID, InstallID: agent.Config.InstallID, User: *user, WorkspaceOwner: *owner,
			Network: *network, Workers: *workers, AddonData: addonData})
		if err != nil {
			runErr <- err
			return
		}
		rn = r
		go func() { runErr <- r.Run(ctx) }()
	}
	lazy := lazyRunner{get: func() *runner.Runner { mu.Lock(); defer mu.Unlock(); return rn }}
	// Isolated AI diagnostics run on this node's Docker with the same
	// sandbox and limits as on the panel; leftovers of an interrupted run are
	// removed now (orphan containers are swept by the runner).
	scratch := filepath.Join(filepath.Dir(absData), "ai-scratch")
	runner.CleanDiagnosticScratch(scratch, log)
	diags := agentDiagnostics{installID: installID, base: runner.Diagnostic{Docker: dk, Files: files, Catalog: cat, ScratchRoot: scratch,
		User: *user, NodeID: cfg.NodeID, Timeout: 10 * time.Minute, MemoryBytes: 768 << 20, NanoCPUs: 1e9, PidsLimit: 256, TmpfsBytes: 128 << 20}}
	probe := &hostProbe{reader: telemetry.ProcReader{}}
	node := agentnode.App(agentnode.Deps{Runner: lazy, Addons: lazy, AddonData: addonData, Diagnostics: diags, Telemetry: probe.read,
		Docker: dk, Files: files, MaxUpload: 4 << 30, InstallID: installID, Log: log})
	agent.Handler = node.Handler()
	agent.Hello = func() agentproto.Hello {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return agentproto.Hello{Protocol: agentproto.Version, AgentVersion: version, Hostname: hostname, Capabilities: capabilities(cctx, dk, files)}
	}
	agent.OnWelcome = func(w agentproto.Welcome) {
		mu.Lock()
		changed := agent.Config.InstallID != w.InstallID
		old := agent.Config.InstallID
		if changed {
			agent.Config.InstallID = w.InstallID
		}
		c := agent.Config
		mu.Unlock()
		if changed {
			if old != "" {
				log.Error("the panel installation changed; containers of the previous installation are left alone. Restart the agent.")
			}
			if err := agentclient.SaveConfig(*dir, c); err != nil {
				log.Error("save agent settings", "err", err)
			}
		}
		startRunner()
	}
	startRunner() // with a remembered installation, run even while offline
	log.Info("rivet-agent starting", "version", version, "node", cfg.NodeID, "panel", cfg.Connect, "data", absData)
	go func() { runErr <- agent.Run(ctx) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-runErr:
		return err
	}
}

// lazyRunner forwards to the runner once it exists (it starts after the
// first contact with the panel when the installation is not yet known).
type lazyRunner struct{ get func() *runner.Runner }

var errNotReady = errors.New("the agent's runner has not started yet")

func (l lazyRunner) Notify(id string) {
	if r := l.get(); r != nil {
		r.Notify(id)
	}
}
func (l lazyRunner) Purge(ctx context.Context, id string) error {
	if r := l.get(); r != nil {
		return r.Purge(ctx, id)
	}
	return errNotReady
}
func (l lazyRunner) Kill(ctx context.Context, id string) error {
	if r := l.get(); r != nil {
		return r.Kill(ctx, id)
	}
	return errNotReady
}
func (l lazyRunner) AddonStates(ctx context.Context, id string) (map[string]runner.AddonStatus, error) {
	if r := l.get(); r != nil {
		return r.AddonStates(ctx, id)
	}
	return nil, errNotReady
}
func (l lazyRunner) AddonLogs(ctx context.Context, id, kind string, lines int) (string, error) {
	if r := l.get(); r != nil {
		return r.AddonLogs(ctx, id, kind, lines)
	}
	return "", errNotReady
}
func (l lazyRunner) Status() runner.Status {
	if r := l.get(); r != nil {
		return r.Status()
	}
	return runner.Status{Ready: errNotReady}
}

// agentDiagnostics runs diagnostics once the panel installation is known, so
// the containers carry the labels the runner's orphan sweep recognizes.
type agentDiagnostics struct {
	installID func() string
	base      runner.Diagnostic
}

func (a agentDiagnostics) RunDiagnostic(ctx context.Context, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error) {
	id := a.installID()
	if id == "" {
		return domain.DiagnosticResult{}, errNotReady
	}
	d := a.base
	d.InstallID = id
	return d.RunDiagnostic(ctx, botID, runtimeID, argv)
}

// hostProbe reads this node's resources for the panel; CPU use is measured
// over the interval since the previous read.
type hostProbe struct {
	reader telemetry.ProcReader

	mu                  sync.Mutex
	primed              bool
	lastBusy, lastTotal uint64
}

func (p *hostProbe) read() agentproto.Telemetry {
	t := agentproto.Telemetry{SampledAtMS: time.Now().UnixMilli()}
	if busy, total, err := p.reader.CPU(); err == nil {
		p.mu.Lock()
		if p.primed && total > p.lastTotal {
			t.CPUPercent = min(max(float64(int64(busy)-int64(p.lastBusy))/float64(total-p.lastTotal)*100, 0), 100)
		}
		p.primed, p.lastBusy, p.lastTotal = true, busy, total
		p.mu.Unlock()
	}
	if n, err := p.reader.LogicalCPUs(); err == nil {
		t.LogicalCPUs = n
	} else {
		t.LogicalCPUs = runtime.NumCPU()
	}
	if used, total, err := p.reader.Memory(); err == nil {
		t.MemoryUsedBytes, t.MemoryTotalBytes = used, total
	}
	if c, err := p.reader.Counters(); err == nil {
		t.Load1 = c.Load1
	}
	return t
}

func doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dir := dirFlag(fs)
	dockerHost := fs.String("docker", env("RIVET_AGENT_DOCKER_HOST", "unix:///var/run/docker.sock"), "Docker endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ok := true
	check := func(name string, err error) {
		if err != nil {
			ok = false
			fmt.Printf("FAIL  %s: %v\n", name, err)
		} else {
			fmt.Printf("ok    %s\n", name)
		}
	}
	cfg, err := agentclient.LoadConfig(*dir)
	check("settings ("+filepath.Join(*dir, agentclient.ConfigFile)+")", err)
	if err != nil {
		return errors.New("not enrolled")
	}
	id := filepath.Join(*dir, agentclient.IdentityFile)
	st, err := os.Stat(id)
	if err == nil && st.Mode().Perm()&0o077 != 0 {
		err = fmt.Errorf("%s must not be readable by others (chmod 600)", id)
	}
	check("node key file permissions", err)
	pair, err := tls.LoadX509KeyPair(id, id)
	check("node certificate", err)
	if err == nil {
		leaf, _ := x509.ParseCertificate(pair.Certificate[0])
		left := time.Until(leaf.NotAfter)
		if left <= 0 {
			err = errors.New("expired: enroll again with a new token")
		} else {
			fmt.Printf("      expires %s (in %s)\n", leaf.NotAfter.UTC().Format(time.RFC3339), left.Round(time.Hour))
		}
		check("certificate validity", err)
	}
	conn, err := net.DialTimeout("tcp", cfg.Connect, 10*time.Second)
	if err == nil {
		conn.Close()
	}
	check("reach the panel at "+cfg.Connect, err)
	dk, err := docker.New(*dockerHost)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		caps, cerr := dk.Capabilities(ctx)
		cancel()
		if cerr == nil {
			cerr = caps.Validate()
		}
		err = cerr
		dk.Close()
	}
	check("docker at "+*dockerHost+" enforces memory, CPU and PID limits", err)
	if !ok {
		return errors.New("some checks failed")
	}
	return nil
}
