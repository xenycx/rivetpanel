package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/logbuf"
)

// hostSnapshot is the Host page's overview: machine, panel process, storage
// and Docker. Administrators only; it names paths and counts, never values.
func (s *panel) hostSnapshot(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(s.host.Snapshot(c.Context()))
}

// hostBots lists every bot with live resource use for the running ones.
func (s *panel) hostBots(c fiber.Ctx) error {
	rep, err := s.host.Bots(c.Context())
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(rep)
}

// historyRanges maps a range name to its window and the bucket width that keeps
// the response to a few hundred points.
var historyRanges = map[string]struct{ window, bucket time.Duration }{
	"1h":  {time.Hour, 30 * time.Second},
	"6h":  {6 * time.Hour, 2 * time.Minute},
	"24h": {24 * time.Hour, 6 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 30 * time.Minute},
	"30d": {30 * 24 * time.Hour, 2 * time.Hour},
}

type historyPoint struct {
	sampleDTO
	CPUMax    float64 `json:"cpu_max"`
	MemoryMax int64   `json:"memory_max"`
}

// nodeHistory returns downsampled samples for a chart range.
func (s *panel) nodeHistory(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	if _, err := s.nodes.GetNode(c.Context(), id); err != nil {
		return err
	}
	name := c.Query("range", "1h")
	r, ok := historyRanges[name]
	if !ok {
		return domain.Invalid("range must be one of 1h, 6h, 24h, 7d, 30d")
	}
	since := time.Now().Add(-r.window).UnixMilli()
	rows, err := s.nodes.ListTelemetryBuckets(c.Context(), id, since, r.bucket.Milliseconds())
	if err != nil {
		return err
	}
	out := make([]historyPoint, len(rows))
	for i, b := range rows {
		out[i] = historyPoint{toSample(b.Telemetry), b.CPUMax, b.MemoryMax}
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"node_id": id, "range": name, "since_ms": since, "bucket_ms": r.bucket.Milliseconds(), "points": out})
}

// panelLogs returns the panel's recent log lines from memory. ?after=<seq>
// returns only newer lines so the page can follow the log cheaply.
func (s *panel) panelLogs(c fiber.Ctx) error {
	q := logbuf.Query{MinLevel: c.Query("level"), Search: c.Query("q")}
	switch q.MinLevel {
	case "", "debug", "info", "warn", "error":
	default:
		return domain.Invalid("level must be debug, info, warn or error")
	}
	if len(q.Search) > 200 {
		return domain.Invalid("search text is too long")
	}
	if v := c.Query("after"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
			return domain.Invalid("after must be a non-negative number")
		}
		q.After = n
	}
	q.Limit = 200
	if c.Query("download") == "1" {
		q.Limit = 1000
	} else if v := c.Query("limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 || n > 1000 {
			return domain.Invalid("limit must be between 1 and 1000")
		}
		q.Limit = n
	}
	entries := s.logs.Find(q)
	c.Set(fiber.HeaderCacheControl, "no-store")
	if c.Query("download") == "1" {
		var b strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&b, "%s %-5s %s", time.UnixMilli(e.TimeMS).UTC().Format("2006-01-02T15:04:05.000Z"), strings.ToUpper(e.Level), e.Msg)
			for _, a := range e.Attrs {
				fmt.Fprintf(&b, " %s=%q", a.Key, a.Value)
			}
			b.WriteByte('\n')
		}
		c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="rivetpanel-log-`+time.Now().UTC().Format("20060102-150405")+`.log"`)
		return c.SendString(b.String())
	}
	return c.JSON(fiber.Map{"entries": entries, "stats": s.logs.Stats()})
}
