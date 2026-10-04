package logarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/console"
)

// Source streams a container's output in Docker's multiplexed format with
// timestamps and follows it (the console's source: Docker locally, the
// agent for a remote node).
type Source interface {
	Logs(ctx context.Context, containerID string, since time.Time, tail int) (io.ReadCloser, error)
}

// RangeSource returns the output between two instants and ends there
// (Docker's since/until without follow). Local Docker implements it.
type RangeSource interface {
	LogRange(ctx context.Context, containerID string, since, until time.Time) (io.ReadCloser, error)
}

// Target is one bot whose console output is captured.
type Target struct {
	BotID, NodeID, ContainerID string
}

// Capturer copies new console output of every running bot into the bots'
// day files. A per-bot cursor (the timestamp of the last copied line) makes
// passes incremental and lets a pass after a restart resume where the last
// one ended, for as long as Docker still holds the lines.
type Capturer struct {
	Store     *Store
	Targets   func(ctx context.Context) ([]Target, error)
	SourceFor func(nodeID string) Source // nil source: the bot is skipped (node offline)
	// Idle ends a followed stream that is quiet for this long (it has sent
	// everything up to now); default 2s.
	Idle time.Duration
	// PerBot bounds one bot's capture; default 30s.
	PerBot time.Duration
	// MaxBytes bounds what one pass copies for one bot; default 32 MiB. The
	// rest follows in the next pass.
	MaxBytes int64
	Workers  int // default 4
	Now      func() time.Time
}

const cursorFile = ".cursor"

func (c *Capturer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Pass captures every target once. It returns how many lines were copied and
// the per-bot errors (a failing bot never stops the others).
func (c *Capturer) Pass(ctx context.Context) (int, []error) {
	// Low on disk: leave the lines in Docker (the cursor stays) and catch up
	// once there is room again, rather than filling the disk further.
	if c.Store.LowDisk() {
		return 0, []error{fmt.Errorf("console capture skipped: %w", ErrLowDisk)}
	}
	targets, err := c.Targets(ctx)
	if err != nil {
		return 0, []error{err}
	}
	workers := c.Workers
	if workers <= 0 {
		workers = 4
	}
	until := c.now()
	var (
		mu    sync.Mutex
		lines int
		errs  []error
		wg    sync.WaitGroup
	)
	ch := make(chan Target)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range ch {
				n, err := c.captureOne(ctx, t, until)
				mu.Lock()
				lines += n
				if err != nil && len(errs) < 20 {
					errs = append(errs, fmt.Errorf("bot %s: %w", t.BotID, err))
				}
				mu.Unlock()
			}
		}()
	}
	for _, t := range targets {
		if !botIDRe.MatchString(t.BotID) || t.ContainerID == "" {
			continue
		}
		select {
		case ch <- t:
		case <-ctx.Done():
		}
	}
	close(ch)
	wg.Wait()
	return lines, errs
}

func (c *Capturer) cursorPath(botID string) string {
	return filepath.Join(c.Store.liveDir(BotScope(botID)), cursorFile)
}

// Cursor returns the instant of the last captured line of a bot (zero when
// nothing was captured yet).
func (c *Capturer) Cursor(botID string) time.Time {
	b, err := os.ReadFile(c.cursorPath(botID))
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	return t
}

func (c *Capturer) setCursor(botID string, t time.Time) error {
	p := c.cursorPath(botID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *Capturer) captureOne(ctx context.Context, t Target, until time.Time) (int, error) {
	var src Source
	if c.SourceFor != nil {
		src = c.SourceFor(t.NodeID)
	}
	if src == nil {
		return 0, nil
	}
	per := c.PerBot
	if per <= 0 {
		per = 30 * time.Second
	}
	idle := c.Idle
	if idle <= 0 {
		idle = 2 * time.Second
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, per)
	defer cancel()

	clock := c.Store.Clock()
	cursor := c.Cursor(t.BotID)
	since := cursor
	if since.IsZero() {
		// First capture of this bot: start at the beginning of today, not
		// at whatever old output Docker still holds.
		since = clock.Start(clock.Day(until))
	}
	if !since.Before(until) {
		return 0, nil
	}
	var (
		rc     io.ReadCloser
		err    error
		follow bool
	)
	if rs, ok := src.(RangeSource); ok {
		rc, err = rs.LogRange(ctx, t.ContainerID, since, until)
	} else {
		follow = true
		rc, err = src.Logs(ctx, t.ContainerID, since, 0)
	}
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	frames := make(chan console.Frame, 64)
	decErr := make(chan error, 1)
	go func() {
		defer close(frames)
		dec := console.NewDecoder(rc, true)
		for {
			f, err := dec.Next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					decErr <- err
				}
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		batch     []Line
		batchSize int
		total     int64
		lines     int
		last      = cursor
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := c.Store.Append(BotScope(t.BotID), batch); err != nil {
			return err
		}
		lines += len(batch)
		batch, batchSize = batch[:0], 0
		return c.setCursor(t.BotID, last)
	}
	timer := time.NewTimer(idle)
	defer timer.Stop()
loop:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				break loop
			}
			if f.Time.IsZero() || !f.Time.After(cursor) || !f.Time.After(since.Add(-time.Nanosecond)) {
				continue
			}
			if !f.Time.Before(until) {
				break loop // produced after this pass started: next pass
			}
			text := strings.TrimRight(string(f.Data), "\r\n")
			line := f.Time.In(clock.loc()).Format(time.RFC3339Nano) + " " + string(f.Stream) + " " + text
			batch = append(batch, Line{At: f.Time, Text: []byte(line)})
			batchSize += len(line)
			total += int64(len(line))
			last = f.Time
			if batchSize >= 1<<20 {
				if err := flush(); err != nil {
					return lines, err
				}
			}
			if total >= maxBytes {
				break loop
			}
			if follow {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
			}
		case <-timer.C:
			if follow {
				break loop // quiet: everything up to now was sent
			}
			timer.Reset(idle)
		case <-ctx.Done():
			break loop
		}
	}
	cancel()
	if err := flush(); err != nil {
		return lines, err
	}
	select {
	case err := <-decErr:
		if ctx.Err() == nil {
			return lines, err
		}
	default:
	}
	return lines, nil
}

// Start returns the instant day begins.
func (c Clock) Start(day string) time.Time {
	d, err := time.ParseInLocation(dateFmt, day, c.loc())
	if err != nil {
		return time.Time{}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, c.loc()).Add(c.At)
}
