package main

import (
	"context"
	"log/slog"
	"time"
)

// maintenanceTask is periodic housekeeping (pruning old rows).
type maintenanceTask struct {
	name  string
	every time.Duration
	run   func(context.Context) error
}

// runMaintenance runs every task once at start and then on its own interval,
// one task at a time from a single goroutine. Separate loops per task cost a
// goroutine each and, at start, opened several database connections at once;
// each SQLite connection keeps about 1 MiB (its parsed schema and cache) for
// the life of the process.
func runMaintenance(ctx context.Context, log *slog.Logger, tasks []maintenanceTask, now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	next := make([]time.Time, len(tasks)) // zero: due now
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		var soonest time.Time
		for i, t := range tasks {
			if t.every <= 0 {
				continue
			}
			if !now().Before(next[i]) {
				if err := t.run(ctx); err != nil && ctx.Err() == nil && log != nil {
					log.Warn(t.name, "err", err)
				}
				if ctx.Err() != nil {
					return
				}
				next[i] = now().Add(t.every)
			}
			if soonest.IsZero() || next[i].Before(soonest) {
				soonest = next[i]
			}
		}
		if soonest.IsZero() {
			return // nothing scheduled
		}
		timer.Reset(max(soonest.Sub(now()), time.Second))
	}
}
