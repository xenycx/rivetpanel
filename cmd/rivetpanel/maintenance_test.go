package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenanceRunsTasksOnTheirIntervalsOneAtATime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var running, overlap, fast, slow atomic.Int32
	task := func(n *atomic.Int32) func(context.Context) error {
		return func(context.Context) error {
			if running.Add(1) > 1 {
				overlap.Add(1)
			}
			n.Add(1)
			time.Sleep(time.Millisecond)
			running.Add(-1)
			return errors.New("logged and ignored")
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runMaintenance(ctx, nil, []maintenanceTask{
			{name: "fast", every: 20 * time.Millisecond, run: task(&fast)},
			{name: "slow", every: time.Hour, run: task(&slow)},
		}, nil)
	}()
	// The timer never waits less than a second between rounds, so allow
	// for two rounds after the first.
	time.Sleep(2500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not stop with its context")
	}
	if slow.Load() != 1 {
		t.Fatalf("hourly task ran %d times, want once at start", slow.Load())
	}
	if fast.Load() < 3 {
		t.Fatalf("frequent task ran %d times", fast.Load())
	}
	if overlap.Load() != 0 {
		t.Fatal("tasks overlapped")
	}
}
