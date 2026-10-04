package hostmon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
	"github.com/xenycx/rivetpanel/internal/telemetry"
)

type fakeBots []domain.Bot

func (f fakeBots) ListBots(context.Context, string) ([]domain.Bot, error) { return f, nil }

type fakeUsers struct{}

func (fakeUsers) ListUsers(context.Context) ([]domain.User, error) {
	return []domain.User{{ID: "u1", Email: "owner@example.com"}}, nil
}

type fakeStats struct {
	calls atomic.Int32
	gone  string // container that no longer exists
}

func (f *fakeStats) StreamStats(_ context.Context, id string, fn func(domain.ResourceSample) bool) error {
	f.calls.Add(1)
	if id == f.gone {
		return runner.ErrNoContainer
	}
	for i := 1; ; i++ {
		// The second frame carries the CPU delta, so the monitor must wait for it.
		if !fn(domain.ResourceSample{CPUCores: float64(i) / 2, MemUsedBytes: int64(i) * 100 << 20, MemLimitBytes: 256 << 20, PIDs: 7, NetRxBytes: 10, NetTxBytes: 20}) {
			return nil
		}
	}
}

func str(s string) *string { return &s }

func TestBotsMeasuresRunningBotsAndSortsByMemory(t *testing.T) {
	st := &fakeStats{gone: "c-gone"}
	started := int64(1_700_000_000_000)
	m := &Monitor{Store: fakeBots{
		{ID: "stopped", OwnerID: "u1", Name: "idle", Runtime: "nodejs", ObservedState: "stopped", DesiredState: domain.DesiredStopped, MemoryBytes: 128 << 20},
		{ID: "a", OwnerID: "u1", Name: "alpha", Runtime: "python", ObservedState: "running", DesiredState: domain.DesiredRunning, MemoryBytes: 256 << 20, NanoCPUs: 5e8, ContainerID: str("c-a"), LastStartedAtMS: &started},
		{ID: "gone", OwnerID: "u1", Name: "ghost", ObservedState: "running", DesiredState: domain.DesiredRunning, MemoryBytes: 64 << 20, ContainerID: str("c-gone")},
	}, Users: fakeUsers{}, Stats: st, WorkspaceUsage: func(id string) (int64, error) { return 1234, nil }}

	rep, err := m.Bots(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 3 || rep.Running != 2 || rep.MemReserved != (256+64)<<20 {
		t.Fatalf("%+v", rep)
	}
	first := rep.Bots[0]
	if first.ID != "a" || !first.Measured || first.CPUCores != 1 || first.MemUsed != 200<<20 || first.Owner != "owner@example.com" || first.CPULimit != 0.5 || first.StartedAtMS != started {
		t.Fatalf("the measured bot comes first and uses the second frame: %+v", first)
	}
	for _, b := range rep.Bots {
		if b.ID == "gone" && b.Measured {
			t.Fatal("a bot whose container vanished must not be reported as measured")
		}
		if b.ID == "stopped" && (b.Measured || b.Running) {
			t.Fatalf("%+v", b)
		}
	}
	if rep.CPUCores != 1 || rep.MemUsed != 200<<20 {
		t.Fatalf("totals only count measured bots: %+v", rep)
	}
	// Disk usage arrives from the background cache, never blocking the table.
	time.Sleep(100 * time.Millisecond)
	m.mu.Lock()
	m.botCache = botCache{}
	m.mu.Unlock()
	rep, _ = m.Bots(t.Context())
	if rep.Bots[0].DiskBytes != 1234 {
		t.Fatalf("disk = %d", rep.Bots[0].DiskBytes)
	}
}

func TestBotsAreCachedBriefly(t *testing.T) {
	st := &fakeStats{}
	m := &Monitor{Store: fakeBots{{ID: "a", Name: "a", ObservedState: "running", ContainerID: str("c")}}, Stats: st}
	m.Bots(t.Context())
	n := st.calls.Load()
	m.Bots(t.Context())
	m.Bots(t.Context())
	if st.calls.Load() != n {
		t.Fatalf("several viewers must share one round of reads: %d -> %d", n, st.calls.Load())
	}
}

type failingStore struct{}

func (failingStore) ListBots(context.Context, string) ([]domain.Bot, error) {
	return nil, errors.New("database is locked")
}

func TestBotsReportsStoreFailure(t *testing.T) {
	if _, err := (&Monitor{Store: failingStore{}}).Bots(t.Context()); err == nil {
		t.Fatal("expected the store error")
	}
}

func TestSnapshotStorageSizesAreComputedInTheBackground(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "bots")
	os.MkdirAll(filepath.Join(data, "x"), 0o755)
	os.WriteFile(filepath.Join(data, "x", "f"), make([]byte, 5000), 0o644)
	os.WriteFile(filepath.Join(root, "p.db"), make([]byte, 700), 0o644)
	m := &Monitor{Proc: telemetry.ProcReader{}, DBPath: filepath.Join(root, "p.db"), DataRoot: data, BackupDir: filepath.Join(root, "missing"),
		Started: time.Now().Add(-time.Minute), Version: "test"}

	s := m.Snapshot(t.Context())
	if s.Panel.DBBytes != 700 || s.Panel.Version != "test" || s.Panel.UptimeMS < 60_000 || s.Panel.PID != os.Getpid() {
		t.Fatalf("panel: %+v", s.Panel)
	}
	if s.Host.Cores < 1 || s.Memory.Total <= 0 {
		t.Fatalf("host: %+v %+v", s.Host, s.Memory)
	}
	var bots *Location
	for i := range s.Storage {
		if s.Storage[i].Key == "bots" {
			bots = &s.Storage[i]
		}
	}
	if bots == nil || bots.FSTotal <= 0 || bots.Device == 0 {
		t.Fatalf("filesystem facts: %+v", bots)
	}
	// The first answer must not wait for the walk; the next one has the size.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range m.Snapshot(t.Context()).Storage {
			if l.Key == "bots" && l.Bytes == 5000 && l.CountedAtMS > 0 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the workspace size was never counted")
}

func TestDockerFactsWhenTheRunnerIsOff(t *testing.T) {
	if f := (&Monitor{Proc: telemetry.ProcReader{}}).docker(t.Context()); f.Enabled {
		t.Fatalf("%+v", f)
	}
}
