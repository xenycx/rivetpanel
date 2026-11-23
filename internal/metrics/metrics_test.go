package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/docker"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

const (
	cidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func statLine(pid int, comm string, ticks, start, rssPages int) string {
	return fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 %d 1000 %d 0 0\n", pid, comm, pid, pid, ticks, start, rssPages)
}

// procFixture builds a proc root with a host process, two processes in
// container A (one with a secret on its command line) and one in B.
func procFixture(t *testing.T) string {
	root := t.TempDir()
	write(t, filepath.Join(root, "stat"), "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 100 0 100 800 0 0 0 0 0 0\n")
	procs := []struct {
		pid           int
		comm, cmd, cg string
	}{
		{1, "systemd", "/sbin/init\x00", "0::/init.scope\n"},
		{42, "node (worker)", "node\x00index.js\x00--token\x00s3cr3t-value\x00", "0::/system.slice/docker-" + cidA + ".scope\n"},
		{43, "sh", "sh\x00-c\x00echo PASSWORD=hunter22\x00", "0::/docker/" + cidA + "\n"},
		{77, "java", "java\x00-jar\x00server.jar\x00", "12:cpu,cpuacct:/docker/" + cidB + "\n"},
		{88, "kworker/0:1", "", "0::/\n"},
	}
	for _, p := range procs {
		dir := filepath.Join(root, fmt.Sprint(p.pid))
		write(t, filepath.Join(dir, "stat"), statLine(p.pid, p.comm, 100, 5, 256))
		write(t, filepath.Join(dir, "cmdline"), p.cmd)
		write(t, filepath.Join(dir, "cgroup"), p.cg)
	}
	return root
}

func TestProcSamplerReadsCPUContainersAndCommandLines(t *testing.T) {
	root := procFixture(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		// During the gap: pid 42 used 50 ticks (0.5 s), pid 77 20 ticks,
		// pid 43 was replaced by a new process (other start time).
		os.WriteFile(filepath.Join(root, "42", "stat"), []byte(statLine(42, "node (worker)", 150, 5, 256)), 0o644)
		os.WriteFile(filepath.Join(root, "77", "stat"), []byte(statLine(77, "java", 120, 5, 1024)), 0o644)
		os.WriteFile(filepath.Join(root, "43", "stat"), []byte(statLine(43, "sh", 900, 6, 1)), 0o644)
		os.WriteFile(filepath.Join(root, "stat"), []byte("cpu  150 0 120 830 0 0 0 0 0 0\n"), 0o644)
	}()
	ps, err := ProcSampler{Root: root, Gap: 500 * time.Millisecond}.Sample(context.Background(), Filter{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if ps.Total != 5 || len(ps.Processes) != 3 {
		t.Fatalf("%+v", ps)
	}
	top := ps.Processes[0]
	if top.PID != 42 || top.Comm != "node (worker)" || top.ContainerID != cidA || top.CPUPct < 80 || top.CPUPct > 101 {
		t.Fatalf("busiest: %+v", top)
	}
	if ps.Processes[1].PID != 77 || ps.Processes[1].ContainerID != cidB || ps.Processes[1].RSSBytes != 1024*int64(os.Getpagesize()) {
		t.Fatalf("second (cgroup v1 path): %+v", ps.Processes[1])
	}
	if ps.Processes[2].CPUPct != 0 {
		t.Fatalf("a reused pid must not count the old process's time: %+v", ps.Processes[2])
	}
	if top.Cmd != "node index.js --token s3cr3t-value" {
		t.Fatalf("cmd = %q", top.Cmd)
	}
	if math.Abs(ps.HostCPUPct-(70.0/100*100)) > 0.01 {
		t.Fatalf("host cpu = %v", ps.HostCPUPct)
	}

	// Filtering by container reads every cgroup.
	one, err := ProcSampler{Root: root, Gap: time.Millisecond}.Sample(context.Background(), Filter{Container: cidA})
	if err != nil || len(one.Processes) != 2 || one.Containers != 2 {
		t.Fatalf("%v %+v", err, one)
	}
	for _, p := range one.Processes {
		if p.ContainerID != cidA {
			t.Fatalf("%+v", p)
		}
	}
	if !(ProcSampler{Root: root}).SeesContainers(map[string]bool{cidB: true}) || (ProcSampler{Root: root}).SeesContainers(map[string]bool{"c": true}) {
		t.Fatal("visibility check")
	}
	if _, err := (ProcSampler{Root: t.TempDir()}).Sample(context.Background(), Filter{}); err == nil {
		t.Fatal("an empty proc root must be an error")
	}
}

func TestContainerOfAndKernelThreads(t *testing.T) {
	if id := ContainerOf([]byte("0::/system.slice/docker-" + cidA + ".scope\n")); id != cidA {
		t.Fatal(id)
	}
	if id := ContainerOf([]byte("0::/user.slice/user-1000.slice/session-2.scope\n")); id != "" {
		t.Fatal(id)
	}
	root := procFixture(t)
	if c := (ProcSampler{Root: root}).cmdline(88, "kworker/0:1"); c != "[kworker/0:1]" {
		t.Fatal(c)
	}
}

func TestRedactCmd(t *testing.T) {
	red := ai.NewRedactor([]string{"tok-abcdef"})
	for in, want := range map[string]string{
		"node bot.js --token s3cr3t":                "node bot.js --token [REDACTED]",
		"python app.py API_KEY=xyz --port 80":       "python app.py API_KEY=[REDACTED] --port 80",
		"psql postgres://admin:hunter2@db:5432/app": "psql postgres://admin:[REDACTED]@db:5432/app",
		"node run.js tok-abcdef":                    "node run.js [REDACTED]",
		"java -Ddb.password='a b' -jar x.jar":       "java -Ddb.password=[REDACTED] -jar x.jar",
		"python auth.py --port 8080":                "python auth.py --port 8080",
	} {
		if got := RedactCmd(in, red); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 500)
	if got := RedactCmd(long, nil); len([]rune(got)) != MaxCmdLen {
		t.Fatalf("cut to %d runes", len([]rune(got)))
	}
}

func TestCgroupReader(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cgroup.controllers"), "cpu memory io pids\n")
	dir := filepath.Join(root, "system.slice", "docker-"+cidA+".scope")
	write(t, filepath.Join(dir, "cpu.stat"), "usage_usec 2500000\nuser_usec 2000000\n")
	write(t, filepath.Join(dir, "memory.current"), "1000000\n")
	write(t, filepath.Join(dir, "memory.stat"), "anon 600000\ninactive_file 300000\n")
	write(t, filepath.Join(dir, "memory.max"), "max\n")
	write(t, filepath.Join(dir, "pids.current"), "7\n")
	write(t, filepath.Join(dir, "io.stat"), "8:0 rbytes=100 wbytes=200 rios=1 wios=2\n259:0 rbytes=10 wbytes=20 rios=1 wios=1\n")
	netdev := filepath.Join(root, "netdev")
	write(t, netdev, "Inter-|   Receive\n face |bytes\n    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n  eth0: 500 5 0 0 0 0 0 0 700 7 0 0 0 0 0 0\n")
	cg := &CgroupReader{Root: root}
	if !cg.Available() {
		t.Fatal("cgroup v2 root not detected")
	}
	c, err := cg.Read(cidA, netdev)
	if err != nil {
		t.Fatal(err)
	}
	if c.CPUUsec != 2_500_000 || c.MemBytes != 700_000 || c.MemLimit != 0 || c.PIDs != 7 || c.ReadBytes != 110 || c.WriteBytes != 220 ||
		!c.HasNet || c.NetRx != 500 || c.NetTx != 700 {
		t.Fatalf("%+v", c)
	}
	if _, err := cg.Read(cidB, ""); err != ErrNoCgroup {
		t.Fatalf("missing container: %v", err)
	}
	// The cgroupfs driver layout.
	write(t, filepath.Join(root, "docker", cidB, "cpu.stat"), "usage_usec 1\n")
	if c, err := cg.Read(cidB, ""); err != nil || c.CPUUsec != 1 || c.HasNet {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestSpikeDetector(t *testing.T) {
	d := SpikeDetector{Threshold: 80}
	t0 := time.Unix(1000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	steps := []struct {
		s    int
		cpu  float64
		want SpikeEvent
	}{
		{0, 85, SpikeNone},    // one reading is not a spike
		{5, 50, SpikeNone},    // reset
		{10, 90, SpikeNone},   // first above
		{15, 95, SpikeOpened}, // second consecutive: opens at 10 s
		{20, 75, SpikeNone},   // above threshold-10: still open
		{25, 60, SpikeNone},   // below 70 from 25 s
		{30, 72, SpikeNone},   // back above 70: the close timer resets
		{35, 60, SpikeNone},   // below again from 35 s
		{60, 50, SpikeNone},   // 25 s below
		{65, 50, SpikeClosed}, // 30 s below: closed at 35 s
		{70, 99, SpikeNone},
	}
	for _, st := range steps {
		if got := d.Observe(at(st.s), st.cpu); got != st.want {
			t.Fatalf("at %ds cpu %v: got %v want %v", st.s, st.cpu, got, st.want)
		}
		if st.want == SpikeOpened && !d.StartedAt().Equal(at(10)) {
			t.Fatalf("started at %v", d.StartedAt())
		}
	}
	if !d.EndedAt().Equal(at(35)) || d.Open() {
		t.Fatalf("ended at %v open=%v", d.EndedAt(), d.Open())
	}
}

func TestMakeGridAndColumns(t *testing.T) {
	g, err := MakeGrid(0, 3_600_000, 0, 0, 0, 30*time.Second)
	if err != nil || g.Step != 30_000 || len(g.Times) != 120 || g.Times[0] != 0 {
		t.Fatalf("an hour at 600 points is limited by the 30 s raw interval: %v %+v", err, g.Step)
	}
	g, _ = MakeGrid(10_000, 7*86_400_000, 0, 0, 100, 30*time.Second)
	if g.Step != 6_048_000 || g.Times[0] != 0 {
		t.Fatalf("step %d first %d", g.Step, g.Times[0])
	}
	inc, _ := MakeGrid(0, 3_600_000, 60_000, 1_800_000, 0, 30*time.Second)
	if len(inc.Times) != 29 || inc.Times[0] != 1_860_000 {
		t.Fatalf("since returns buckets after it: %d %v", len(inc.Times), inc.Times[:1])
	}
	if _, err := MakeGrid(5, 5, 0, 0, 0, 0); err == nil {
		t.Fatal("empty range")
	}
	if _, err := MakeGrid(0, 401*86_400_000, 0, 0, 0, 0); err == nil {
		t.Fatal("too long")
	}
	b, _ := json.Marshal(Col{1.23456, math.NaN(), 2e9})
	if string(b) != "[1.235,null,2000000000]" {
		t.Fatal(string(b))
	}
}

func openDB(t *testing.T) *sqlite.DB {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	db.EnsureLocalNode(ctx)
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x','h',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct{ id, cid string }{{"b1", cidA}, {"b2", cidB}} {
		if err := db.CreateBot(ctx, domain.Bot{ID: b.id, OwnerID: "u", NodeID: domain.LocalNodeID, Name: "app-" + b.id, Runtime: "nodejs",
			ImageRef: "i", Argv: []string{"node"}, MemoryBytes: 256 << 20, NanoCPUs: 1e9, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE bots SET container_id = ?, desired_state = 'running', observed_state = 'running' WHERE id = ?`, b.cid, b.id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type fakeDocker struct {
	counters map[string]docker.StatsCounters
	top      map[string][]docker.TopProcess
}

func (f *fakeDocker) StatsCounters(_ context.Context, id string) (docker.StatsCounters, error) {
	c, ok := f.counters[id]
	if !ok {
		return c, os.ErrNotExist
	}
	return c, nil
}
func (f *fakeDocker) ContainerTop(_ context.Context, id string) ([]docker.TopProcess, error) {
	return f.top[id], nil
}
func (f *fakeDocker) ContainerPID(context.Context, string) (int, error) { return 0, nil }

func TestCollectContainersSeriesAndAttribution(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)
	clock := now
	// b1 is read from the cgroup files, b2 (no cgroup directory) through Docker.
	cgRoot := t.TempDir()
	write(t, filepath.Join(cgRoot, "cgroup.controllers"), "cpu\n")
	cgDir := filepath.Join(cgRoot, "system.slice", "docker-"+cidA+".scope")
	setCG := func(usec int, rbytes int) {
		write(t, filepath.Join(cgDir, "cpu.stat"), fmt.Sprintf("usage_usec %d\n", usec))
		write(t, filepath.Join(cgDir, "memory.current"), "1000\n")
		write(t, filepath.Join(cgDir, "io.stat"), fmt.Sprintf("8:0 rbytes=%d wbytes=0\n", rbytes))
	}
	setCG(0, 0)
	dk := &fakeDocker{counters: map[string]docker.StatsCounters{
		cidA: {NetRx: 0, NetTx: 0},
		cidB: {CPUUsageNS: 0, MemUsed: 500, NetRx: 1000},
	}, top: map[string][]docker.TopProcess{
		cidA: {{PID: 10, CPUPct: 5, Comm: "node", Args: "node a.js --password hunter2"}},
		cidB: {{PID: 20, CPUPct: 50, Comm: "java", Args: "java -jar s.jar"}},
	}}
	m := &Metrics{Store: db, LocalNode: domain.LocalNodeID, Cgroup: &CgroupReader{Root: cgRoot}, Docker: dk, LogicalCPUs: 4,
		Proc: ProcSampler{Root: t.TempDir()}, Now: func() time.Time { return clock }}
	if err := m.CollectContainers(ctx, false); err != nil {
		t.Fatal(err)
	}
	// 15 s later: b1 used 15 s of CPU (1 core) and read 1500 bytes; b2 used 7.5 s.
	clock = clock.Add(15 * time.Second)
	setCG(15_000_000, 1500)
	dk.counters[cidA] = docker.StatsCounters{NetRx: 300}
	dk.counters[cidB] = docker.StatsCounters{CPUUsageNS: 7_500_000_000, MemUsed: 500, NetRx: 2500}
	if err := m.CollectContainers(ctx, false); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListContainerBuckets(ctx, domain.LocalNodeID, "", 0, clock.UnixMilli()+1, 1000)
	if err != nil || len(rows) != 2 {
		t.Fatalf("the first pass only sets baselines: %v %+v", err, rows)
	}
	for _, r := range rows {
		switch r.BotID {
		case "b1":
			if r.CPUCores != 1 || r.BlkReadBps != 100 || r.MemoryBytes != 1000 || r.NetRxBps != 20 {
				t.Fatalf("cgroup sample: %+v", r)
			}
		case "b2":
			if r.CPUCores != 0.5 || r.NetRxBps != 100 || r.MemoryBytes != 500 {
				t.Fatalf("docker sample: %+v", r)
			}
		}
	}

	// Host samples: 50 % of 4 CPUs = 2 cores; apps use 1.5.
	if err := db.InsertTelemetry(ctx, domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: clock.UnixMilli(), CPUPercent: 50,
		LogicalCPUs: 4, MemoryUsedBytes: 4000, MemoryTotalBytes: 8000, DiskTotalBytes: 1, DiskUsedBytes: 1}); err != nil {
		t.Fatal(err)
	}
	start, end := clock.Add(-time.Minute).UnixMilli(), clock.Add(time.Minute).UnixMilli()
	bs, err := m.BotsSeries(ctx, domain.LocalNodeID, start, end, 30_000, 1, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	i := -1
	for k, tm := range bs.Times {
		if tm <= clock.UnixMilli() && clock.UnixMilli() < tm+bs.StepMS {
			i = k
		}
	}
	if len(bs.Bots) != 1 || bs.Bots[0].BotID != "b1" || bs.Bots[0].Name != "app-b1" || bs.Bots[0].Values[i] != 1 ||
		bs.Other[i] != 0.5 || bs.Unattributed[i] != 0.5 || bs.Source != "samples" {
		t.Fatalf("%+v (i=%d)", bs, i)
	}
	if !math.IsNaN(bs.Other[0]) {
		t.Fatalf("no data is a gap, not zero: %v", bs.Other)
	}
	if _, err := m.BotsSeries(ctx, domain.LocalNodeID, start, end, 0, 0, "disk"); err == nil {
		t.Fatal("unknown metric")
	}
	if _, err := m.Series(ctx, "nope", start, end, 0); err == nil {
		t.Fatal("unknown node")
	}
	s, err := m.Series(ctx, domain.LocalNodeID, start, end, 0)
	if err != nil || s.CPUs != 4 || s.MemTotal != 8000 || s.CPU[i] != 50 {
		t.Fatalf("%v %+v", err, s)
	}
	one, err := m.BotSeries(ctx, "b2", start, end, 0)
	if err != nil || one.MemLimit != 256<<20 || one.CPULimit != 1 {
		t.Fatalf("%v %+v", err, one)
	}

	// No host /proc in the (empty) proc root: Docker's process lists,
	// attributed and redacted.
	snap, err := m.LocalSnapshot(ctx, 10, "")
	if err != nil || snap.Source != domain.ProcSourceDockerTop || len(snap.Items) != 2 {
		t.Fatalf("%v %+v", err, snap)
	}
	if snap.Items[0].BotID != "b2" || snap.Items[0].BotName != "app-b2" || snap.Items[1].Cmd != "node a.js --password [REDACTED]" {
		t.Fatalf("%+v", snap.Items)
	}
	bp, err := m.BotProcesses(ctx, "b1")
	if err != nil || len(bp.Items) != 1 || bp.Items[0].PID != 10 {
		t.Fatalf("%v %+v", err, bp)
	}

	at, err := m.At(ctx, domain.LocalNodeID, clock.UnixMilli(), 60_000)
	if err != nil || at.Host == nil || at.Host.CPU != 50 || len(at.Bots) != 2 || at.Bots[0].BotID != "b1" || at.Bots[0].SharePct != 50 {
		t.Fatalf("%v %+v", err, at)
	}
	if n, err := m.Prune(ctx); err != nil || n != 0 {
		t.Fatalf("nothing is old yet: %d %v", n, err)
	}
}

func TestSpikeRecordedWithAppsAndProcesses(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	clock := time.Now()
	dk := &fakeDocker{top: map[string][]docker.TopProcess{cidA: {{PID: 10, CPUPct: 180, Comm: "node", Args: "node busy.js"}}}}
	m := &Metrics{Store: db, LocalNode: domain.LocalNodeID, Docker: dk, LogicalCPUs: 2, Proc: ProcSampler{Root: t.TempDir()},
		Fast: &FastSampler{}, Now: func() time.Time { return clock }}
	m.detector = SpikeDetector{Threshold: m.threshold()}
	m.mu.Lock()
	m.last = map[string]domain.ContainerSample{"b1": {BotID: "b1", NodeID: domain.LocalNodeID, CPUCores: 1.8}}
	m.mu.Unlock()
	m.spikeWork = make(chan func(context.Context), 32)
	feed := func(sec int, cpu float64) {
		p := HostPoint{T: clock.Add(time.Duration(sec) * time.Second).UnixMilli(), CPU: cpu}
		m.Fast.Add(p)
		m.onFastPoint(p)
		for {
			select {
			case fn := <-m.spikeWork:
				fn(ctx)
				continue
			default:
			}
			break
		}
	}
	feed(0, 95)
	feed(5, 97)
	sp, _ := db.ListSpikes(ctx, domain.LocalNodeID, 0, clock.Add(time.Hour).UnixMilli())
	if len(sp) != 1 || sp[0].EndedAtMS != nil || len(sp[0].TopBots) != 1 || sp[0].TopBots[0].Name != "app-b1" || len(sp[0].TopProcesses) != 1 {
		t.Fatalf("open spike: %+v", sp)
	}
	for s := 10; s <= 60; s += 5 {
		feed(s, 20)
	}
	sp, _ = db.ListSpikes(ctx, domain.LocalNodeID, 0, clock.Add(time.Hour).UnixMilli())
	if len(sp) != 1 || sp[0].EndedAtMS == nil || sp[0].PeakCPUPct != 97 || sp[0].AvgCPUPct != 97 || sp[0].TopBots[0].SharePct != 92.78 {
		t.Fatalf("closed spike: %+v", sp)
	}
	ev, _ := m.Events(ctx, domain.LocalNodeID, 0, clock.Add(time.Hour).UnixMilli())
	if len(ev) != 1 || ev[0].Kind != "spike" {
		t.Fatalf("%+v", ev)
	}
	snap, ok, _ := db.NearestProcessSnapshot(ctx, domain.LocalNodeID, clock.UnixMilli(), 60_000)
	if !ok || snap.Reason != domain.SnapshotSpike {
		t.Fatalf("%+v", snap)
	}
}
