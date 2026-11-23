package sqlite

import (
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestContainerSamplesBucketsAveragesAndPrune(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	node := domain.LocalNodeID
	var in []domain.ContainerSample
	// b1: 1 core every 15 s for 2 minutes; b2: 0.5 core; one sample on another node.
	for i := int64(0); i < 8; i++ {
		at := 60_000 + i*15_000
		in = append(in, domain.ContainerSample{BotID: "b1", NodeID: node, SampledAtMS: at, CPUCores: 1, MemoryBytes: 100, NetRxBps: 10, PIDs: 3},
			domain.ContainerSample{BotID: "b2", NodeID: node, SampledAtMS: at, CPUCores: 0.5, MemoryBytes: 50, BlkWriteBps: 4})
	}
	in = append(in, domain.ContainerSample{BotID: "b1", NodeID: "other", SampledAtMS: 60_000, CPUCores: 9})
	if err := db.InsertContainerSamples(ctx, in); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListContainerBuckets(ctx, node, "", 0, 200_000, 60_000)
	if err != nil {
		t.Fatal(err)
	}
	// 60-120 s and 120-180 s buckets, two apps each.
	if len(rows) != 4 || rows[0].BucketMS != 60_000 || rows[2].BucketMS != 120_000 {
		t.Fatalf("buckets: %+v", rows)
	}
	for _, r := range rows {
		if (r.BotID == "b1" && (r.CPUCores != 1 || r.PIDs != 3 || r.NetRxBps != 10)) || (r.BotID == "b2" && (r.CPUCores != 0.5 || r.BlkWriteBps != 4)) {
			t.Fatalf("other node leaked or wrong mean: %+v", r)
		}
	}
	one, err := db.ListContainerBuckets(ctx, node, "b2", 0, 200_000, 30_000)
	if err != nil || len(one) != 4 || one[0].BotID != "b2" {
		t.Fatalf("one app: %v %+v", err, one)
	}
	avg, err := db.ContainerAverages(ctx, node, 60_000, 90_000)
	if err != nil || len(avg) != 2 || avg[0].BotID != "b1" {
		t.Fatalf("averages (busiest first): %v %+v", err, avg)
	}
	subs, err := db.ListMetricSubjects(ctx)
	if err != nil || len(subs) != 2 || subs[0].Name != "b1" {
		t.Fatalf("subjects: %v %+v", err, subs)
	}
	n, err := db.PruneContainerSamples(ctx, 90_000, 3)
	if err != nil || n != 3 {
		t.Fatalf("pruned %d %v", n, err)
	}
	for {
		n, err := db.PruneContainerSamples(ctx, 90_000, 3)
		if err != nil {
			t.Fatal(err)
		}
		if n < 3 {
			break
		}
	}
	left, _ := db.ListContainerBuckets(ctx, node, "", 0, 90_000, 1000)
	if len(left) != 0 {
		t.Fatalf("old samples left: %+v", left)
	}
}

func TestProcessSnapshotsNearestAndPrune(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	node := domain.LocalNodeID
	for _, at := range []int64{60_000, 120_000, 180_000} {
		if _, err := db.InsertProcessSnapshot(ctx, domain.ProcessSnapshot{NodeID: node, SampledAtMS: at, Reason: domain.SnapshotPeriodic,
			HostCPUPct: 50, Items: []domain.ProcessEntry{{PID: int(at / 1000), Comm: "node", Cmd: "node index.js", CPUPct: 99, BotID: "b1"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.InsertProcessSnapshot(ctx, domain.ProcessSnapshot{NodeID: node, SampledAtMS: 1, Reason: "nope"}); err == nil {
		t.Fatal("an unknown reason must be refused")
	}
	s, ok, err := db.NearestProcessSnapshot(ctx, node, 130_000, 60_000)
	if err != nil || !ok || s.SampledAtMS != 120_000 || len(s.Items) != 1 || s.Items[0].PID != 120 || s.Source != domain.ProcSourceProc {
		t.Fatalf("nearest: %v %v %+v", ok, err, s)
	}
	if _, ok, _ := db.NearestProcessSnapshot(ctx, node, 1_000_000, 60_000); ok {
		t.Fatal("nothing within the distance")
	}
	if _, ok, _ := db.NearestProcessSnapshot(ctx, "other", 120_000, 60_000); ok {
		t.Fatal("snapshots are per node")
	}
	if n, err := db.PruneProcessSnapshots(ctx, 150_000, 100); err != nil || n != 2 {
		t.Fatalf("pruned %d %v", n, err)
	}
}

func TestLoadSpikesLifecycle(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	node := domain.LocalNodeID
	id, err := db.InsertSpike(ctx, domain.LoadSpike{NodeID: node, StartedAtMS: 100_000, PeakCPUPct: 90, AvgCPUPct: 85})
	if err != nil {
		t.Fatal(err)
	}
	open, err := db.ListSpikes(ctx, node, 500_000, 600_000)
	if err != nil || len(open) != 1 || open[0].EndedAtMS != nil || open[0].TopBots == nil {
		t.Fatalf("an open spike overlaps every later range: %v %+v", err, open)
	}
	end := int64(160_000)
	sp := domain.LoadSpike{ID: id, NodeID: node, StartedAtMS: 100_000, EndedAtMS: &end, PeakCPUPct: 97, AvgCPUPct: 88,
		TopBots:      []domain.SpikeBot{{BotID: "b1", Name: "one", CPUCores: 1.5, SharePct: 75}},
		TopProcesses: []domain.ProcessEntry{{PID: 7, Comm: "java", CPUPct: 150}}}
	if err := db.UpdateSpike(ctx, sp); err != nil {
		t.Fatal(err)
	}
	got, _ := db.ListSpikes(ctx, node, 150_000, 150_000)
	if len(got) != 1 || *got[0].EndedAtMS != end || got[0].TopBots[0].Name != "one" || got[0].TopProcesses[0].Comm != "java" {
		t.Fatalf("%+v", got)
	}
	if got, _ := db.ListSpikes(ctx, node, 170_000, 200_000); len(got) != 0 {
		t.Fatalf("ended before the range: %+v", got)
	}
	// A spike left open by a restart is closed at start-up.
	if _, err := db.InsertSpike(ctx, domain.LoadSpike{NodeID: node, StartedAtMS: 300_000}); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseOpenSpikes(ctx, node, 310_000); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ListSpikes(ctx, node, 0, 1_000_000)
	if len(got) != 2 || got[1].EndedAtMS == nil || *got[1].EndedAtMS != 310_000 {
		t.Fatalf("%+v", got)
	}
	if n, err := db.PruneSpikes(ctx, 200_000, 10); err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
}

func TestTelemetryRangeAndMetricEvents(t *testing.T) {
	db := open(t)
	seed(t, db)
	ctx := context.Background()
	node := domain.LocalNodeID
	for i := int64(0); i < 10; i++ {
		if err := db.InsertTelemetry(ctx, domain.Telemetry{NodeID: node, SampledAtMS: i * 30_000, CPUPercent: float64(i * 10), LogicalCPUs: 4,
			MemoryUsedBytes: 10, MemoryTotalBytes: 100, DiskUsedBytes: 1, DiskTotalBytes: 2}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListTelemetryRange(ctx, node, 60_000, 180_000, 60_000)
	if err != nil || len(rows) != 2 || rows[0].SampledAtMS != 60_000 || rows[0].CPUPercent != 25 || rows[0].CPUMax != 30 {
		t.Fatalf("buckets start at multiples of the step and exclude the end: %v %+v", err, rows)
	}
	if v, _ := db.OldestTelemetry(ctx, node); v != 0 {
		t.Fatalf("oldest = %d", v)
	}

	b1, crash := "b1", "killed: out of memory"
	for _, a := range []domain.AuditEvent{
		{AtMS: 1000, BotID: &b1, Action: "bot.start", Outcome: "ok", Source: "person"},
		{AtMS: 2000, BotID: &b1, Action: "bot.crash", Outcome: "failed", Target: &crash, Source: "system"},
		{AtMS: 3000, BotID: &b1, Action: "env.set", Outcome: "ok", Source: "person"},
	} {
		if err := db.InsertAudit(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operations (id, bot_id, kind, trigger, status, created_at_ms, finished_at_ms)
		VALUES ('o1', 'b1', 'deploy', 'manual', 'succeeded', 4000, 5000), ('o2', 'b1', 'build', 'manual', 'succeeded', 4000, 5000)`); err != nil {
		t.Fatal(err)
	}
	end := int64(7000)
	if _, err := db.InsertSpike(ctx, domain.LoadSpike{NodeID: node, StartedAtMS: 6000, EndedAtMS: &end, PeakCPUPct: 95,
		TopBots: []domain.SpikeBot{{BotID: "b1", Name: "b1"}}}); err != nil {
		t.Fatal(err)
	}
	ev, err := db.ListMetricEvents(ctx, node, "", 0, 10_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := ""
	for _, e := range ev {
		kinds += e.Kind + ","
	}
	if kinds != "start,oom,deploy,spike," || ev[3].BotName != "b1" {
		t.Fatalf("events: %s %+v", kinds, ev)
	}
	if ev, _ := db.ListMetricEvents(ctx, "other", "", 0, 10_000, 0); len(ev) != 0 {
		t.Fatalf("events are per node: %+v", ev)
	}
}
