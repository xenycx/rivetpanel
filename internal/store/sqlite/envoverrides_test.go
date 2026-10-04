package sqlite

import (
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func TestEnvOverridesKeepEmptyValuesAndRemoveByName(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	err := db.PutEnvOverrides(ctx, []domain.Setting{
		{Key: "RIVET_SFTP_LISTEN", Value: ""}, // "switch it off": an empty value is an override, not a deletion
		{Key: "RIVET_MAX_BUILDS", Value: "3"},
		{Key: "RIVET_METRICS_TOKEN", Cipher: []byte{1}, Nonce: []byte{2}, KeyID: "k1"},
	}, nil, "admin", 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.EnvOverrides(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if v, ok := got["RIVET_SFTP_LISTEN"]; !ok || v.Value != "" || v.Cipher != nil {
		t.Fatalf("empty override lost: %+v", v)
	}
	if v := got["RIVET_METRICS_TOKEN"]; v.Value != "" || len(v.Cipher) != 1 || v.KeyID != "k1" {
		t.Fatalf("sealed override: %+v", v)
	}
	// Replacing and removing in one write.
	if err := db.PutEnvOverrides(ctx, []domain.Setting{{Key: "RIVET_MAX_BUILDS", Value: "4"}}, []string{"RIVET_SFTP_LISTEN"}, "admin", 20); err != nil {
		t.Fatal(err)
	}
	got, _ = db.EnvOverrides(ctx)
	if len(got) != 2 || got["RIVET_MAX_BUILDS"].Value != "4" {
		t.Fatalf("%+v", got)
	}
	// Sealed overrides are part of key verification and rotation.
	n := 0
	if err := db.WalkSealed(ctx, func(r SealedRow) error {
		if r.NS == "env" && r.Name == "RIVET_METRICS_TOKEN" {
			n++
		}
		return nil
	}); err != nil || n != 1 {
		t.Fatalf("WalkSealed saw the sealed override %d times (%v)", n, err)
	}
}

func TestTelemetryBucketsAverageAndKeepPeaks(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	db.EnsureLocalNode(ctx)
	add := func(at int64, cpu float64, mem int64, rx int64) {
		t.Helper()
		if err := db.InsertTelemetry(ctx, domain.Telemetry{NodeID: domain.LocalNodeID, SampledAtMS: at, CPUPercent: cpu, LogicalCPUs: 2,
			MemoryUsedBytes: mem, MemoryTotalBytes: 1000, DiskUsedBytes: 1, DiskTotalBytes: 10, NetRxBps: rx}); err != nil {
			t.Fatal(err)
		}
	}
	// Two 60-second buckets: [0,60000) holds three samples, [60000,120000) one.
	add(1_000, 10, 100, 100)
	add(20_000, 90, 300, 300)
	add(40_000, 20, 200, 200)
	add(70_000, 50, 400, 0)
	rows, err := db.ListTelemetryBuckets(ctx, domain.LocalNodeID, 0, 60_000)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%d %v", len(rows), err)
	}
	a, b := rows[0], rows[1]
	if a.SampledAtMS != 30_000 || a.Samples != 3 || a.CPUPercent != 40 || a.CPUMax != 90 || a.MemoryUsedBytes != 200 || a.MemoryMax != 300 || a.NetRxBps != 200 {
		t.Fatalf("first bucket: %+v", a)
	}
	if b.SampledAtMS != 90_000 || b.Samples != 1 || b.CPUPercent != 50 {
		t.Fatalf("second bucket: %+v", b)
	}
	if rows, _ := db.ListTelemetryBuckets(ctx, domain.LocalNodeID, 60_000, 60_000); len(rows) != 1 {
		t.Fatalf("since is exclusive of older samples: %d", len(rows))
	}
}
