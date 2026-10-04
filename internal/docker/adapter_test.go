package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"github.com/xenycx/rivetpanel/internal/runner"
)

func spec() runner.ContainerSpec {
	return runner.ContainerSpec{
		Name: "rivetpanel-x-runtime", Role: runner.RoleRuntime, BotID: "x", NodeID: "n", Generation: 7, SpecHash: "abc",
		Image: "node@sha256:" + strings.Repeat("a", 64), Argv: []string{"node", "index.js", "--flag=$(id)"},
		Env: []string{"A=b"}, WorkspaceHostPath: "/var/lib/rivetpanel/bots/x", User: "65532:65532", Network: "bridge",
		MemoryBytes: 256 << 20, NanoCPUs: 5e8, PidsLimit: 64, TmpfsBytes: 64 << 20, OpenStdin: true,
	}
}

func TestHostConfigEnforcesIsolationPolicy(t *testing.T) {
	h := HostConfig(spec())
	if h.Privileged || !h.ReadonlyRootfs || len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" || len(h.CapAdd) != 0 {
		t.Fatalf("isolation: %+v", h)
	}
	if len(h.SecurityOpt) != 1 || h.SecurityOpt[0] != "no-new-privileges" {
		t.Fatalf("security opts: %v (default seccomp must be retained, never unconfined)", h.SecurityOpt)
	}
	r := h.Resources
	if r.Memory != 256<<20 || r.MemorySwap != r.Memory || r.NanoCPUs != 5e8 || r.PidsLimit == nil || *r.PidsLimit != 64 {
		t.Fatalf("resources: %+v", r)
	}
	if h.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Fatal("docker restart policy must be disabled; the runner owns restarts")
	}
	if len(h.PortBindings) != 0 || h.PublishAllPorts || h.PidMode != "" || h.IpcMode != "" && h.IpcMode != "private" || h.UsernsMode != "" || h.CgroupParent != "" {
		t.Fatalf("host namespaces/ports must not be exposed: %+v", h)
	}
	if len(h.Devices) != 0 || len(h.Binds) != 0 || len(h.Mounts) != 1 {
		t.Fatalf("only the managed workspace may be mounted: %+v", h)
	}
	m := h.Mounts[0]
	if m.Type != mount.TypeBind || m.Source != "/var/lib/rivetpanel/bots/x" || m.Target != "/workspace" || m.ReadOnly || m.BindOptions.CreateMountpoint {
		t.Fatalf("mount: %+v", m)
	}
	if h.LogConfig.Type != "json-file" || h.LogConfig.Config["max-size"] == "" || h.LogConfig.Config["max-file"] == "" {
		t.Fatalf("logging must be bounded: %+v", h.LogConfig)
	}
	if tmp := h.Tmpfs["/tmp"]; !strings.Contains(tmp, "nosuid") || !strings.Contains(tmp, "size=67108864") {
		t.Fatalf("tmpfs: %q", tmp)
	}
	if h.AutoRemove {
		t.Fatal("containers are removed by the runner, not AutoRemove")
	}
}

func TestContainerConfigUsesExecFormAndLabels(t *testing.T) {
	c := ContainerConfig(spec())
	if strings.Join(c.Entrypoint, "|") != "node|index.js|--flag=$(id)" || len(c.Cmd) != 0 {
		t.Fatalf("argv must be passed verbatim in exec form: %v %v", c.Entrypoint, c.Cmd)
	}
	if c.Tty || !c.OpenStdin || c.StdinOnce || c.AttachStdin || c.User != "65532:65532" || c.WorkingDir != "/workspace" {
		t.Fatalf("%+v", c)
	}
	for k, v := range map[string]string{
		runner.LabelManaged: "true", runner.LabelBot: "x", runner.LabelNode: "n",
		runner.LabelRole: "runtime", runner.LabelGeneration: "7", runner.LabelSpec: "abc",
	} {
		if c.Labels[k] != v {
			t.Errorf("label %s = %q want %q", k, c.Labels[k], v)
		}
	}
	if len(c.ExposedPorts) != 0 {
		t.Fatal("no ports may be exposed")
	}
}

func TestSampleFromStats(t *testing.T) {
	var s container.StatsResponse
	s.CPUStats.CPUUsage.TotalUsage, s.PreCPUStats.CPUUsage.TotalUsage = 2_000_000_000, 1_000_000_000
	s.CPUStats.SystemUsage, s.PreCPUStats.SystemUsage = 40_000_000_000, 38_000_000_000
	s.CPUStats.OnlineCPUs = 4
	s.MemoryStats.Usage, s.MemoryStats.Limit = 300<<20, 512<<20
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 100 << 20}
	s.PidsStats.Current = 9
	s.Networks = map[string]container.NetworkStats{"eth0": {RxBytes: 10, TxBytes: 20}, "eth1": {RxBytes: 1, TxBytes: 2}}
	got := SampleFromStats(s)
	// cpuDelta/sysDelta*online = 1e9/2e9*4 = 2 cores; page cache is excluded from memory.
	if got.CPUCores != 2 || got.MemUsedBytes != 200<<20 || got.MemLimitBytes != 512<<20 || got.PIDs != 9 || got.NetRxBytes != 11 || got.NetTxBytes != 22 {
		t.Fatalf("%+v", got)
	}
	// The first frame has no previous sample: CPU must be 0, never negative/NaN.
	var first container.StatsResponse
	first.CPUStats.CPUUsage.TotalUsage, first.CPUStats.SystemUsage, first.CPUStats.OnlineCPUs = 5, 5, 2
	if c := SampleFromStats(first).CPUCores; c != 0 {
		t.Fatalf("first frame cpu = %v", c)
	}
}

func TestConfigMapsEntrypointPortsAndNetwork(t *testing.T) {
	base := runner.ContainerSpec{Image: "img@sha256:x", Argv: []string{"node", "index.js"}, Network: "bridge", PidsLimit: 64, TmpfsBytes: 1 << 20}
	// Without an entrypoint the argv IS the exec-form entrypoint (no shell, empty Cmd).
	cfg := ContainerConfig(base)
	if len(cfg.Entrypoint) != 2 || cfg.Entrypoint[0] != "node" || len(cfg.Cmd) != 0 || len(cfg.ExposedPorts) != 0 {
		t.Fatalf("%+v", cfg)
	}
	if hc := HostConfig(base); len(hc.PortBindings) != 0 || hc.NetworkMode != "bridge" || !hc.ReadonlyRootfs {
		t.Fatalf("no ports must be published by default: %+v", hc)
	}
	// With an entrypoint, argv become its arguments.
	base.Entrypoint = []string{"node", "--inspect=0"}
	base.Ports = []runner.PortBinding{{HostIP: "127.0.0.1", HostPort: 20080, ContainerPort: 8080, Proto: "tcp"}, {HostIP: "127.0.0.1", HostPort: 20081, ContainerPort: 9000, Proto: "udp"}}
	cfg = ContainerConfig(base)
	if cfg.Entrypoint[1] != "--inspect=0" || len(cfg.Cmd) != 2 || cfg.Cmd[0] != "node" {
		t.Fatalf("%+v", cfg)
	}
	if _, ok := cfg.ExposedPorts["8080/tcp"]; !ok {
		t.Fatalf("exposed: %v", cfg.ExposedPorts)
	}
	hc := HostConfig(base)
	b := hc.PortBindings["8080/tcp"]
	if len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "20080" || len(hc.PortBindings["9000/udp"]) != 1 {
		t.Fatalf("bindings: %v", hc.PortBindings)
	}
	// Isolation defaults hold regardless of the new options.
	if hc.Privileged || len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || hc.RestartPolicy.Name != "no" && hc.RestartPolicy.Name != "" {
		t.Fatalf("isolation regressed: %+v", hc)
	}
	base.Network = "none"
	base.Ports = nil
	if HostConfig(base).NetworkMode != "none" {
		t.Fatal("network none not applied")
	}
}
