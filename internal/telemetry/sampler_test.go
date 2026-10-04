package telemetry

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

func fixture(t *testing.T, stat, meminfo string) ProcReader {
	t.Helper()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
	os.WriteFile(filepath.Join(d, "meminfo"), []byte(meminfo), 0o644)
	return ProcReader{Root: d}
}

const meminfo = "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4000000 kB\nBuffers: 1 kB\n"

func statWith(user, idle, iowait, guest uint64, cpus int) string {
	s := "cpu  " + u(user) + " 0 0 " + u(idle) + " " + u(iowait) + " 0 0 0 " + u(guest) + " 0\n"
	for i := 0; i < cpus; i++ {
		s += "cpu" + u(uint64(i)) + " 1 2 3 4 5 6 7 8 9 10\n"
	}
	return s + "intr 1\nctxt 2\n"
}

func u(n uint64) string { return strconv.FormatUint(n, 10) }

func TestProcReaderParses(t *testing.T) {
	p := fixture(t, statWith(300, 600, 100, 50, 8), meminfo)
	busy, total, err := p.CPU()
	if err != nil || total != 1000 || busy != 300 { // guest columns are not double counted; iowait counts as idle
		t.Fatalf("busy=%d total=%d err=%v", busy, total, err)
	}
	if n, err := p.LogicalCPUs(); err != nil || n != 8 {
		t.Fatalf("cpus=%d err=%v", n, err)
	}
	used, tot, err := p.Memory()
	if err != nil || tot != 16000000*1024 || used != 12000000*1024 {
		t.Fatalf("mem used=%d total=%d err=%v", used, tot, err)
	}
	if du, dt, err := (ProcReader{}).Disk(t.TempDir()); err != nil || dt <= 0 || du < 0 || du > dt {
		t.Fatalf("disk %d/%d %v", du, dt, err)
	}
}

func TestProcReaderRejectsGarbage(t *testing.T) {
	p := fixture(t, "nothing useful\n", "MemTotal: 5 kB\n")
	if _, _, err := p.CPU(); err == nil {
		t.Fatal("expected cpu error")
	}
	if _, err := p.LogicalCPUs(); err == nil {
		t.Fatal("expected cpu count error")
	}
	if _, _, err := p.Memory(); err == nil {
		t.Fatal("expected memory error (no MemAvailable)")
	}
}

type fakeReader struct {
	busy, total uint64
	cpus        int
}

func (f *fakeReader) CPU() (uint64, uint64, error)      { return f.busy, f.total, nil }
func (f *fakeReader) LogicalCPUs() (int, error)         { return f.cpus, nil }
func (f *fakeReader) Memory() (int64, int64, error)     { return 4 << 30, 16 << 30, nil }
func (f *fakeReader) Disk(string) (int64, int64, error) { return 10 << 30, 100 << 30, nil }

func setup(t *testing.T) (*sqlite.DB, *Sampler, *fakeReader) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Migrate(ctx, migrations.FS)
	db.EnsureLocalNode(ctx)
	fr := &fakeReader{busy: 100, total: 1000, cpus: 4}
	now := time.Unix(1_800_000_000, 0)
	s := &Sampler{Store: db, Reader: fr, NodeID: domain.LocalNodeID, DiskPath: "/", Interval: 30 * time.Second,
		Retention: 7 * 24 * time.Hour, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	return db, s, fr
}

func TestFirstSampleOnlyPrimesAndCPUIsNormalizedAcrossAllCPUs(t *testing.T) {
	db, s, fr := setup(t)
	ctx := context.Background()
	if stored, err := s.SampleOnce(ctx); err != nil || stored {
		t.Fatalf("baseline must not store: %v %v", stored, err)
	}
	// 4 CPUs, 100 jiffies/CPU elapsed = 400 total; 100 busy => 25% of ALL CPUs.
	fr.busy, fr.total = 200, 1400
	s.Now = func() time.Time { return time.Unix(1_800_000_030, 0) }
	if stored, err := s.SampleOnce(ctx); err != nil || !stored {
		t.Fatal(stored, err)
	}
	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 0, 10)
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	r := rows[0]
	if r.CPUPercent < 24.99 || r.CPUPercent > 25.01 || r.LogicalCPUs != 4 || r.MemoryUsedBytes != 4<<30 ||
		r.MemoryTotalBytes != 16<<30 || r.DiskTotalBytes != 100<<30 || r.SampledAtMS != 1_800_000_030_000 {
		t.Fatalf("%+v", r)
	}
}

func TestCPUClampedAndZeroDeltaSafe(t *testing.T) {
	db, s, fr := setup(t)
	ctx := context.Background()
	s.SampleOnce(ctx)
	s.Now = func() time.Time { return time.Unix(1_800_000_030, 0) }
	fr.busy, fr.total = 100, 1000 // no elapsed time at all
	if _, err := s.SampleOnce(ctx); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Unix(1_800_000_060, 0) }
	fr.busy, fr.total = 5000, 1100 // counter weirdness: busy > total => clamp to 100
	if _, err := s.SampleOnce(ctx); err != nil {
		t.Fatalf("out-of-range input must be clamped, not rejected by CHECK: %v", err)
	}
	s.Now = func() time.Time { return time.Unix(1_800_000_090, 0) }
	fr.busy, fr.total = 10, 1200 // counters went backwards (reset): clamp to 0
	if _, err := s.SampleOnce(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 0, 10)
	for _, r := range rows {
		if r.CPUPercent < 0 || r.CPUPercent > 100 {
			t.Fatalf("out of range: %+v", r)
		}
	}
	if len(rows) != 3 || rows[0].CPUPercent != 0 || rows[1].CPUPercent != 100 || rows[2].CPUPercent != 0 {
		t.Fatalf("%+v", rows)
	}
}

func TestRunningBotsCounted(t *testing.T) {
	db, s, fr := setup(t)
	ctx := context.Background()
	db.ExecContext(ctx, `INSERT INTO users (id,email,password_hash,created_at_ms,updated_at_ms) VALUES ('u','u@x.io','h',1,1)`)
	for i, st := range []string{"running", "running", "stopped"} {
		db.CreateBot(ctx, domain.Bot{ID: string(rune('a' + i)), OwnerID: "u", NodeID: domain.LocalNodeID, Name: "n", Runtime: "nodejs",
			ImageRef: "i", Argv: []string{"node"}, MemoryBytes: 1, NanoCPUs: 1, PidsLimit: 1, CreatedAtMS: 1, UpdatedAtMS: 1})
		db.ExecContext(ctx, `UPDATE bots SET observed_state = ? WHERE id = ?`, st, string(rune('a'+i)))
	}
	s.SampleOnce(ctx)
	fr.busy, fr.total = 200, 1400
	s.Now = func() time.Time { return time.Unix(1_800_000_030, 0) }
	s.SampleOnce(ctx)
	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 0, 10)
	if len(rows) != 1 || rows[0].RunningBots != 2 {
		t.Fatalf("%+v", rows)
	}
}

func TestRetentionPrunesInBoundedBatches(t *testing.T) {
	db, s, _ := setup(t)
	ctx := context.Background()
	now := s.Now()
	old := now.Add(-8 * 24 * time.Hour).UnixMilli()
	fresh := now.Add(-time.Hour).UnixMilli()
	tx, _ := db.BeginTx(ctx, nil)
	for i := 0; i < 2500; i++ {
		tx.ExecContext(ctx, `INSERT INTO node_telemetry (node_id, sampled_at_ms, cpu_percent, logical_cpus, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, running_bots) VALUES (?,?,1,1,1,2,1,2,0)`, domain.LocalNodeID, old+int64(i))
	}
	for i := 0; i < 10; i++ {
		tx.ExecContext(ctx, `INSERT INTO node_telemetry (node_id, sampled_at_ms, cpu_percent, logical_cpus, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, running_bots) VALUES (?,?,1,1,1,2,1,2,0)`, domain.LocalNodeID, fresh+int64(i))
	}
	tx.Commit()

	// A single batch removes at most `batch` rows.
	n, err := db.PruneTelemetry(ctx, now.Add(-7*24*time.Hour).UnixMilli(), 1000)
	if err != nil || n != 1000 {
		t.Fatalf("first batch deleted %d (%v)", n, err)
	}
	s.Batch = 1000
	total, err := s.Prune(ctx)
	if err != nil || total != 1500 {
		t.Fatalf("remaining old rows deleted = %d (%v)", total, err)
	}
	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 0, 100000)
	if len(rows) != 10 {
		t.Fatalf("fresh rows lost or old rows kept: %d", len(rows))
	}
}

func TestPruneStopsWhenContextCancelled(t *testing.T) {
	db, s, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	old := s.Now().Add(-9 * 24 * time.Hour).UnixMilli()
	tx, _ := db.BeginTx(ctx, nil)
	for i := 0; i < 300; i++ {
		tx.ExecContext(ctx, `INSERT INTO node_telemetry (node_id, sampled_at_ms, cpu_percent, logical_cpus, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, running_bots) VALUES (?,?,1,1,1,2,1,2,0)`, domain.LocalNodeID, old+int64(i))
	}
	tx.Commit()
	s.Batch = 100
	cancel()
	if _, err := s.Prune(ctx); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestListTelemetryReturnsNewestWindowAscending(t *testing.T) {
	db, s, _ := setup(t)
	ctx := context.Background()
	_ = s
	for i := 1; i <= 10; i++ {
		db.InsertTelemetry(ctx, domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: int64(i * 1000), CPUPercent: 1, LogicalCPUs: 1,
			MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2})
	}
	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 2000, 3)
	if len(rows) != 3 || rows[0].SampledAtMS != 8000 || rows[2].SampledAtMS != 10000 {
		t.Fatalf("%+v", rows)
	}
	// duplicate timestamps are ignored rather than failing the sampler
	if err := db.InsertTelemetry(ctx, domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: 10000, CPUPercent: 1, LogicalCPUs: 1,
		MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2}); err != nil {
		t.Fatal(err)
	}
}
