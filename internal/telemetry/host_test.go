package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func hostFixture(t *testing.T) ProcReader {
	t.Helper()
	proc, sys := t.TempDir(), t.TempDir()
	write := func(root, name, body string) {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(proc, "stat", statWith(300, 600, 100, 50, 4))
	write(proc, "loadavg", "0.52 1.25 2.50 1/300 4242\n")
	write(proc, "meminfo", "MemTotal: 8000000 kB\nMemFree: 1000000 kB\nMemAvailable: 5000000 kB\nBuffers: 200000 kB\nCached: 1500000 kB\nSReclaimable: 300000 kB\nSwapTotal: 2000000 kB\nSwapFree: 1500000 kB\n")
	write(proc, "net/dev", "Inter-|   Receive |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n"+
		"    lo: 9999 1 0 0 0 0 0 0 9999 1 0 0 0 0 0 0\n"+
		"  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n"+
		"docker0: 5000 10 0 0 0 0 0 0 5000 20 0 0 0 0 0 0\n"+
		"veth12ab: 7000 10 0 0 0 0 0 0 7000 20 0 0 0 0 0 0\n")
	write(proc, "diskstats", "   8       0 sda 100 0 2000 0 50 0 4000 0 0 0 0 0 0 0 0 0 0\n"+
		"   8       1 sda1 90 0 1800 0 40 0 3000 0 0 0 0 0 0 0 0 0 0\n"+
		"   7       0 loop0 5 0 80 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"+
		" 253       0 dm-0 90 0 1800 0 40 0 3000 0 0 0 0 0 0 0 0 0 0\n")
	os.MkdirAll(filepath.Join(sys, "block", "sda"), 0o755)
	os.MkdirAll(filepath.Join(sys, "block", "loop0"), 0o755)
	return ProcReader{Root: proc, SysRoot: sys}
}

func TestCountersSkipVirtualInterfacesAndPartitions(t *testing.T) {
	c, err := hostFixture(t).Counters()
	if err != nil {
		t.Fatal(err)
	}
	if c.Load1 != 0.52 || c.Load5 != 1.25 || c.Load15 != 2.5 {
		t.Fatalf("load %+v", c)
	}
	if c.NetRx != 1000 || c.NetTx != 2000 { // loopback, docker0 and veth are not host traffic
		t.Fatalf("net rx=%d tx=%d", c.NetRx, c.NetTx)
	}
	if c.DiskRead != 2000*512 || c.DiskWrite != 4000*512 { // whole device only
		t.Fatalf("disk read=%d write=%d", c.DiskRead, c.DiskWrite)
	}
	if c.SwapTotal != 2000000*1024 || c.SwapUsed != 500000*1024 {
		t.Fatalf("swap %+v", c)
	}
}

func TestMemInfoCachedIncludesReclaimable(t *testing.T) {
	m, err := hostFixture(t).MemInfo()
	if err != nil || m.Cached != (1500000+300000)*1024 || m.Buffers != 200000*1024 || m.Available != 5000000*1024 {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMissingFilesLeaveCountersZeroInsteadOfFailing(t *testing.T) {
	c, err := (ProcReader{Root: t.TempDir(), SysRoot: t.TempDir()}).Counters()
	if err != nil || c != (Counters{}) {
		t.Fatalf("%+v %v", c, err)
	}
}

type countingReader struct {
	fakeReader
	c Counters
}

func (r *countingReader) Counters() (Counters, error) { return r.c, nil }

func TestSamplerTurnsCountersIntoRates(t *testing.T) {
	db, s, _ := setup(t)
	ctx := context.Background()
	r := &countingReader{fakeReader: fakeReader{busy: 100, total: 1000, cpus: 4}, c: Counters{Load1: 1.5, SwapUsed: 10, SwapTotal: 20, NetRx: 1000, NetTx: 5000, DiskRead: 0, DiskWrite: 0}}
	s.Reader = r
	s.SampleOnce(ctx) // primes CPU
	s.Now = func() time.Time { return time.Unix(1_800_000_030, 0) }
	r.busy, r.total = 200, 1400
	s.SampleOnce(ctx) // first stored sample: counters have no predecessor, so rates are 0
	s.Now = func() time.Time { return time.Unix(1_800_000_060, 0) }
	r.busy, r.total = 300, 1800
	r.c.NetRx, r.c.NetTx, r.c.DiskRead, r.c.DiskWrite = 1000+30*100, 5000+30*2000, 30*512, 60*512
	s.SampleOnce(ctx)
	s.Now = func() time.Time { return time.Unix(1_800_000_090, 0) }
	r.busy, r.total = 400, 2200
	r.c.NetRx = 10 // counter went backwards (interface reset)
	s.SampleOnce(ctx)

	rows, _ := db.ListTelemetry(ctx, domain.LocalNodeID, 0, 10)
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].NetRxBps != 0 || rows[0].Load1 != 1.5 || rows[0].SwapTotalBytes != 20 {
		t.Fatalf("first: %+v", rows[0])
	}
	if rows[1].NetRxBps != 100 || rows[1].NetTxBps != 2000 || rows[1].DiskReadBps != 512 || rows[1].DiskWriteBps != 1024 {
		t.Fatalf("rates: %+v", rows[1])
	}
	if rows[2].NetRxBps != 0 {
		t.Fatalf("a reset counter must give 0, not a negative or huge rate: %+v", rows[2])
	}
}

func TestInfoAndSelfAreReadable(t *testing.T) {
	p := hostFixture(t)
	os.WriteFile(filepath.Join(p.Root, "uptime"), []byte("3600.50 7000.00\n"), 0o644)
	os.WriteFile(filepath.Join(p.Root, "cpuinfo"), []byte("processor\t: 0\nmodel name\t: Test CPU @ 3GHz\n"), 0o644)
	info := p.Info()
	if info.UptimeSec != 3600 || info.CPUModel != "Test CPU @ 3GHz" || info.LogicalCPU != 4 || info.Arch == "" {
		t.Fatalf("%+v", info)
	}
	self := (ProcReader{}).Self()
	if self.PID != os.Getpid() || self.Goroutines < 1 || self.HeapBytes == 0 {
		t.Fatalf("%+v", self)
	}
}
