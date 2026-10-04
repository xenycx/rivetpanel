// Package diag runs read-only health probes for administrators (the
// Diagnostics page and "rivetpanel doctor"). Probes have deadlines, run one at a
// time, never execute anything user-supplied, and report only an allowlist of
// facts: no tokens, environment values, configuration files or logs.
package diag

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Info Status = "info"
)

// Check is one finding. Fix is the next action, in plain words.
type Check struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Report is the result of one run.
type Report struct {
	GeneratedAtMS int64   `json:"generated_at_ms"`
	Version       string  `json:"version"`
	GoVersion     string  `json:"go_version"`
	UptimeMS      int64   `json:"uptime_ms"`
	Checks        []Check `json:"checks"`
}

// Probe produces checks; it must respect ctx.
type Probe func(ctx context.Context) []Check

// Run executes probes sequentially, each with its own deadline, so a hung
// dependency shows up as one failed check instead of a hung page.
func Run(ctx context.Context, version string, started time.Time, probes []Probe) Report {
	r := Report{GeneratedAtMS: time.Now().UnixMilli(), Version: version, GoVersion: runtime.Version(),
		UptimeMS: time.Since(started).Milliseconds()}
	for _, p := range probes {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		done := make(chan []Check, 1)
		go func() { done <- p(pctx) }()
		select {
		case cs := <-done:
			r.Checks = append(r.Checks, cs...)
		case <-pctx.Done():
			r.Checks = append(r.Checks, Check{ID: "timeout", Group: "Panel", Title: "A check did not finish", Status: Warn,
				Detail: "One probe exceeded its 3 second deadline and was skipped."})
		}
		cancel()
	}
	return r
}

// Bytes formats a byte count for check details.
func Bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Summary counts checks by status.
func (r Report) Summary() (fail, warn int) {
	for _, c := range r.Checks {
		switch c.Status {
		case Fail:
			fail++
		case Warn:
			warn++
		}
	}
	return
}
