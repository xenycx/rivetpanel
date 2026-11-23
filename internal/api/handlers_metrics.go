package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/metrics"
)

// Monitoring routes (1.20.0): columnar host and per-app series, chart
// events, CPU spikes, "what happened at t", live process lists and a live
// host stream. Node routes need system.view; app routes need console access
// to that app.

const (
	maxHostStreams     = 32 // open live host streams across the panel
	hostStreamBeat     = 15 * time.Second
	hostStreamReauth   = 30 * time.Second
	hostStreamMaxAlive = 12 * time.Hour
	defaultSeriesSpan  = time.Hour
)

func (s *panel) metricsRoutes(r fiber.Router) {
	if s.metrics == nil {
		return
	}
	view := s.requirePerm(domain.PermSystemView)
	r.Get("/nodes/:id/series", view, s.nodeSeries)
	r.Get("/nodes/:id/series/bots", view, s.nodeBotsSeries)
	r.Get("/nodes/:id/series/stream", view, s.nodeSeriesStream)
	r.Get("/nodes/:id/events", view, s.nodeEvents)
	r.Get("/nodes/:id/spikes", view, s.nodeSpikes)
	r.Get("/nodes/:id/at", view, s.nodeAt)
	r.Get("/nodes/:id/processes", view, s.nodeProcesses)
	r.Get("/bots/:id/series", s.botSeries)
	r.Get("/bots/:id/processes", s.botProcesses)
}

// queryInt64 reads an optional integer query parameter.
func queryInt64(c fiber.Ctx, name string, def int64) (int64, error) {
	v := c.Query(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, domain.Invalid(name + " must be a non-negative integer")
	}
	return n, nil
}

// seriesQuery reads start, end, step, points and since (Unix ms). The
// default range is the last hour.
func seriesQuery(c fiber.Ctx) (metrics.SeriesQuery, error) {
	now := time.Now().UnixMilli()
	var q metrics.SeriesQuery
	var err error
	if q.End, err = queryInt64(c, "end", now); err != nil {
		return q, err
	}
	if q.Start, err = queryInt64(c, "start", q.End-defaultSeriesSpan.Milliseconds()); err != nil {
		return q, err
	}
	if q.Step, err = queryInt64(c, "step", 0); err != nil {
		return q, err
	}
	if q.Since, err = queryInt64(c, "since", 0); err != nil {
		return q, err
	}
	points, err := queryInt64(c, "points", 0)
	if err != nil {
		return q, err
	}
	if points > metrics.MaxPoints {
		return q, domain.Invalid(fmt.Sprintf("points can be at most %d", metrics.MaxPoints))
	}
	q.Points = int(points)
	return q, nil
}

func noStore(c fiber.Ctx) { c.Set(fiber.HeaderCacheControl, "no-store") }

func (s *panel) nodeSeries(c fiber.Ctx) error {
	q, err := seriesQuery(c)
	if err != nil {
		return err
	}
	out, err := s.metrics.SeriesFor(c.Context(), strings.Clone(c.Params("id")), q)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(out)
}

func (s *panel) nodeBotsSeries(c fiber.Ctx) error {
	q, err := seriesQuery(c)
	if err != nil {
		return err
	}
	top, err := queryInt64(c, "top", metrics.DefaultTop)
	if err != nil {
		return err
	}
	out, err := s.metrics.BotsSeriesFor(c.Context(), strings.Clone(c.Params("id")), q, int(top), c.Query("metric", "cpu"))
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(out)
}

// rangeQuery reads start and end (default: the last hour).
func rangeQuery(c fiber.Ctx) (int64, int64, error) {
	now := time.Now().UnixMilli()
	end, err := queryInt64(c, "end", now)
	if err != nil {
		return 0, 0, err
	}
	start, err := queryInt64(c, "start", end-defaultSeriesSpan.Milliseconds())
	if err != nil {
		return 0, 0, err
	}
	if end < start {
		return 0, 0, domain.Invalid("end must be after start")
	}
	if end-start > metrics.MaxWindow.Milliseconds() {
		return 0, 0, domain.Invalid("the range can be at most 400 days")
	}
	return start, end, nil
}

func (s *panel) nodeEvents(c fiber.Ctx) error {
	start, end, err := rangeQuery(c)
	if err != nil {
		return err
	}
	ev, err := s.metrics.Events(c.Context(), strings.Clone(c.Params("id")), start, end)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(fiber.Map{"events": ev})
}

func (s *panel) nodeSpikes(c fiber.Ctx) error {
	start, end, err := rangeQuery(c)
	if err != nil {
		return err
	}
	out, err := s.metrics.Spikes(c.Context(), strings.Clone(c.Params("id")), start, end)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(out)
}

func (s *panel) nodeAt(c fiber.Ctx) error {
	if c.Query("t") == "" {
		return domain.Invalid("t is required (Unix milliseconds)")
	}
	t, err := queryInt64(c, "t", 0)
	if err != nil {
		return err
	}
	window, err := queryInt64(c, "window", 60_000)
	if err != nil {
		return err
	}
	out, err := s.metrics.At(c.Context(), strings.Clone(c.Params("id")), t, window)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(out)
}

func processLimit(c fiber.Ctx) (int, error) {
	n, err := queryInt64(c, "limit", metrics.PeriodicTop)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > 100 {
		return 0, domain.Invalid("limit must be between 1 and 100")
	}
	return int(n), nil
}

func (s *panel) nodeProcesses(c fiber.Ctx) error {
	limit, err := processLimit(c)
	if err != nil {
		return err
	}
	snap, err := s.metrics.Processes(c.Context(), strings.Clone(c.Params("id")), limit)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(processesDTO(snap))
}

func processesDTO(s domain.ProcessSnapshot) fiber.Map {
	items := s.Items
	if items == nil {
		items = []domain.ProcessEntry{}
	}
	return fiber.Map{"sampled_at_ms": s.SampledAtMS, "source": s.Source, "host_cpu_pct": s.HostCPUPct, "items": items}
}

func (s *panel) botSeries(c fiber.Ctx) error {
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermViewConsole)
	if err != nil {
		return err
	}
	q, err := seriesQuery(c)
	if err != nil {
		return err
	}
	out, err := s.metrics.BotSeriesFor(c.Context(), b.ID, q)
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(out)
}

func (s *panel) botProcesses(c fiber.Ctx) error {
	b, err := s.bots.Authorize(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.PermViewConsole)
	if err != nil {
		return err
	}
	snap, err := s.metrics.BotProcesses(c.Context(), b.ID)
	if errors.Is(err, metrics.ErrNotRunning) {
		return fiber.NewError(fiber.StatusConflict, "the app is not running, so it has no processes")
	}
	if err != nil {
		return err
	}
	noStore(c)
	return c.JSON(processesDTO(snap))
}

// nodeSeriesStream serves live host points as Server-Sent Events (event
// `sample`) with a comment heartbeat every 15 seconds. Viewers of the
// panel's node share one sampler; the session and permission are
// re-checked regularly so a logout or revocation ends the stream.
func (s *panel) nodeSeriesStream(c fiber.Ctx) error {
	node := strings.Clone(c.Params("id"))
	if _, err := s.nodes.GetNode(c.Context(), node); err != nil {
		return err
	}
	select {
	case s.streams <- struct{}{}:
	default:
		return fiber.NewError(fiber.StatusTooManyRequests, "too many live views open")
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	ch, stop, err := s.metrics.Stream(ctx, node)
	if err != nil {
		cancel()
		<-s.streams
		return err
	}
	token := currentToken(c)
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("X-Accel-Buffering", "no")
	return c.SendStreamWriter(func(w *bufio.Writer) {
		defer func() { stop(); cancel(); <-s.streams }()
		write := func(b string) bool {
			if _, err := w.WriteString(b); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		if !write(": live\n\n") {
			return
		}
		beat := time.NewTicker(hostStreamBeat)
		defer beat.Stop()
		reauth := time.NewTicker(hostStreamReauth)
		defer reauth.Stop()
		deadline := time.After(hostStreamMaxAlive)
		for {
			select {
			case <-ctx.Done():
				return
			case <-deadline:
				return
			case <-reauth.C:
				if token != "" {
					u, err := s.auth.Authenticate(ctx, token)
					if err != nil || !u.Can(domain.PermSystemView) {
						return
					}
				}
			case <-beat.C:
				if !write(": ping\n\n") {
					return
				}
			case p, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(p)
				if !write("event: sample\ndata: " + string(b) + "\n\n") {
					return
				}
			}
		}
	})
}
