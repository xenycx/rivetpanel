package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const botA = "11111111-2222-3333-4444-555555555555"

func newStore(t *testing.T, now *time.Time, c Clock) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return *now }
	s.SetClock(c)
	return s
}

func readGz(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f) // multistream: every member
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("archive %s is not valid gzip: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestClockDays(t *testing.T) {
	utc := Clock{Loc: time.UTC}
	if d := utc.Day(time.Date(2026, 10, 4, 23, 59, 59, 0, time.UTC)); d != "2026-10-04" {
		t.Fatal(d)
	}
	if d := utc.Day(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)); d != "2026-10-05" {
		t.Fatal(d)
	}
	three := Clock{Loc: time.UTC, At: 3 * time.Hour}
	if d := three.Day(time.Date(2026, 10, 5, 2, 59, 0, 0, time.UTC)); d != "2026-10-04" {
		t.Fatalf("before the archive time a line belongs to the previous day: %s", d)
	}
	if e := three.End("2026-10-04"); !e.Equal(time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)) {
		t.Fatal(e)
	}
	if s := three.Start("2026-10-04"); !s.Equal(time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)) {
		t.Fatal(s)
	}
	tbs, _ := time.LoadLocation("Asia/Tbilisi") // UTC+4
	z := Clock{Loc: tbs}
	if d := z.Day(time.Date(2026, 10, 4, 21, 0, 0, 0, time.UTC)); d != "2026-10-05" {
		t.Fatalf("the configured time zone decides the day: %s", d)
	}
}

func TestRotateAtTheBoundaryAndGzipIntegrity(t *testing.T) {
	now := time.Date(2026, 10, 4, 23, 59, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	sc := BotScope(botA)
	if err := s.Append(sc, []Line{{At: now, Text: []byte("before midnight")}, {At: now.Add(30 * time.Second), Text: []byte("last second\n")}}); err != nil {
		t.Fatal(err)
	}
	if r := s.Rotate(); r.Archived != 0 {
		t.Fatalf("the open day was archived: %+v", r)
	}
	now = time.Date(2026, 10, 5, 0, 0, 1, 0, time.UTC)
	s.Append(sc, []Line{{At: now, Text: []byte("after midnight")}})
	r := s.Rotate()
	if r.Archived != 1 || len(r.Errors) > 0 {
		t.Fatalf("rotate = %+v", r)
	}
	arch := filepath.Join(s.Root, ArchiveDir, "bots", botA, "2026-10-04.log.gz")
	if got := readGz(t, arch); got != "before midnight\nlast second\n" {
		t.Fatalf("archive content = %q", got)
	}
	live := filepath.Join(s.Root, LiveDir, "bots", botA)
	if exists(filepath.Join(live, "2026-10-04.log")) {
		t.Fatal("source not removed after archiving")
	}
	if b, _ := os.ReadFile(filepath.Join(live, "2026-10-05.log")); string(b) != "after midnight\n" {
		t.Fatalf("live day = %q", b)
	}
	days, _ := s.Days(sc)
	if len(days) != 2 || days[0].Date != "2026-10-05" || days[0].Archived || !days[1].Archived {
		t.Fatalf("days = %+v", days)
	}
	f, _, err := s.Open(sc, "2026-10-04", true)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := s.Open(sc, "../../x", true); err != ErrNotFound {
		t.Fatal("path traversal in the date accepted")
	}
}

func TestRotateCatchesUpAndIsIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	// Three missed days (the panel was down) are all archived in one pass.
	for _, d := range []int{6, 7, 8} {
		at := time.Date(2026, 10, d, 10, 0, 0, 0, time.UTC)
		s.Append(PanelScope, []Line{{At: at, Text: []byte("day " + at.Format("02"))}})
	}
	if r := s.Rotate(); r.Archived != 3 {
		t.Fatalf("catch-up = %+v", r)
	}
	if r := s.Rotate(); r.Archived != 0 || len(r.Errors) > 0 {
		t.Fatalf("second pass did something: %+v", r)
	}
	// Late lines for an archived day become a second gzip member.
	s.Append(PanelScope, []Line{{At: time.Date(2026, 10, 8, 23, 0, 0, 0, time.UTC), Text: []byte("late")}})
	if r := s.Rotate(); r.Archived != 1 {
		t.Fatalf("late lines = %+v", r)
	}
	arch := filepath.Join(s.Root, ArchiveDir, PanelScope, "2026-10-08.log.gz")
	if got := readGz(t, arch); got != "day 08\nlate\n" {
		t.Fatalf("merged archive = %q", got)
	}

	// A crash after the archive was renamed into place but before the
	// sealed source was removed: the retry must not add it twice.
	live := filepath.Join(s.Root, LiveDir, PanelScope)
	sealed := "2026-10-09.log" + sealedTag + "abc"
	os.WriteFile(filepath.Join(live, sealed), []byte("once\n"), 0o600)
	if _, err := s.archiveSealed(PanelScope, "2026-10-09", sealed); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(live, sealed), []byte("once\n"), 0o600) // "not removed"
	os.WriteFile(filepath.Join(s.Root, ArchiveDir, PanelScope, "2026-10-09.log.gz.tmp-x"), []byte("junk"), 0o600)
	if r := s.Rotate(); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	if got := readGz(t, filepath.Join(s.Root, ArchiveDir, PanelScope, "2026-10-09.log.gz")); got != "once\n" {
		t.Fatalf("interrupted archive retried twice: %q", got)
	}
	if exists(filepath.Join(live, sealed)) || exists(filepath.Join(s.Root, ArchiveDir, PanelScope, "2026-10-09.log.gz.tmp-x")) {
		t.Fatal("leftovers of the interrupted pass remain")
	}
}

func TestPruneByAgeAndSize(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	for d := 1; d <= 30; d++ {
		at := time.Date(2026, 10, d, 1, 0, 0, 0, time.UTC)
		s.Append(PanelScope, []Line{{At: at, Text: bytes.Repeat([]byte("x"), 100)}})
		s.Append(BotScope(botA), []Line{{At: at, Text: []byte("bot")}})
	}
	s.Rotate()
	r := s.Prune(Retention{Days: 7})
	// Today is the 31st: the 24th..30th stay (7 days), 23 days x 2 scopes go.
	if r.Deleted != 46 || len(r.Errors) > 0 {
		t.Fatalf("age prune = %+v", r)
	}
	days, _ := s.Days(PanelScope)
	if len(days) != 7 || days[len(days)-1].Date != "2026-10-24" {
		t.Fatalf("kept days = %+v", days)
	}
	u := s.Usage()
	r = s.Prune(Retention{Days: 7, MaxBytes: u.ArchivedBytes / 2})
	if r.Deleted == 0 || s.Usage().ArchivedBytes > u.ArchivedBytes/2 {
		t.Fatalf("size prune = %+v usage %+v", r, s.Usage())
	}
	days, _ = s.Days(PanelScope)
	if len(days) == 0 || days[0].Date != "2026-10-30" {
		t.Fatalf("the newest days must be kept: %+v", days)
	}
	if err := s.RemoveScope(BotScope(botA)); err != nil || exists(filepath.Join(s.Root, ArchiveDir, "bots", botA)) {
		t.Fatal("bot logs not removed", err)
	}
	if s.RemoveScope("bots/../../etc") == nil || s.RemoveScope(PanelScope) == nil {
		t.Fatal("invalid scope accepted")
	}
}

// frames builds Docker's multiplexed stream with timestamps.
func frames(lines ...string) []byte {
	var b bytes.Buffer
	for _, l := range lines {
		hdr := [8]byte{1}
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(l)))
		b.Write(hdr[:])
		b.WriteString(l)
	}
	return b.Bytes()
}

type rangeSrc struct{ data []byte }

func (r rangeSrc) Logs(context.Context, string, time.Time, int) (io.ReadCloser, error) {
	panic("the range source must be preferred")
}
func (r rangeSrc) LogRange(context.Context, string, time.Time, time.Time) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data)), nil
}

// followSrc never ends its stream, like a followed remote console.
type followSrc struct{ data []byte }

func (f followSrc) Logs(ctx context.Context, _ string, _ time.Time, _ int) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write(f.data)
		<-ctx.Done()
		pw.CloseWithError(ctx.Err())
	}()
	return pr, nil
}

func TestCaptureIsIncrementalAndRoutesLinesToTheirDay(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 10, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	data := frames(
		"2026-10-04T23:00:00.000000001Z yesterday before first capture\n", // a first capture starts at the beginning of today
		"2026-10-05T00:00:01.000000000Z one\n",
		"2026-10-05T00:05:00.000000000Z two\n",
		"2026-10-05T00:20:00.000000000Z future\n", // after this pass began
	)
	src := rangeSrc{data}
	c := &Capturer{Store: s, Now: func() time.Time { return now },
		Targets:   func(context.Context) ([]Target, error) { return []Target{{BotID: botA, ContainerID: "c1"}}, nil },
		SourceFor: func(string) Source { return src }}
	n, errs := c.Pass(context.Background())
	if len(errs) > 0 || n != 2 {
		t.Fatalf("first pass: %d lines, %v", n, errs)
	}
	// Second pass: the cursor skips what was already copied.
	now = time.Date(2026, 10, 5, 0, 30, 0, 0, time.UTC)
	n, _ = c.Pass(context.Background())
	if n != 1 {
		t.Fatalf("second pass copied %d lines, want only the new one", n)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, LiveDir, "bots", botA, "2026-10-05.log"))
	got := string(b)
	if strings.Count(got, "\n") != 3 || !strings.Contains(got, "2026-10-05T00:00:01Z stdout one\n") || strings.Contains(got, "yesterday") {
		t.Fatalf("captured = %q", got)
	}

	// A followed stream that never ends stops when it goes quiet.
	s2 := newStore(t, &now, Clock{Loc: time.UTC})
	c2 := &Capturer{Store: s2, Now: func() time.Time { return now }, Idle: 50 * time.Millisecond,
		Targets:   c.Targets,
		SourceFor: func(string) Source { return followSrc{frames("2026-10-05T00:01:00Z hello\n")} }}
	done := make(chan int)
	go func() { n, _ := c2.Pass(context.Background()); done <- n }()
	select {
	case n := <-done:
		if n != 1 {
			t.Fatalf("follow capture = %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a followed stream hung the capture")
	}
	// An offline node (no source) is skipped without error.
	c2.SourceFor = func(string) Source { return nil }
	if _, errs := c2.Pass(context.Background()); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestSinkWritesPanelDayFiles(t *testing.T) {
	now := time.Now()
	s := newStore(t, &now, Clock{})
	var sink Sink
	sink.Write([]byte("before attach\n")) // dropped
	ctx, cancel := context.WithCancel(context.Background())
	sink.SetEnabled(true)
	sink.Attach(ctx, s)
	sink.Write([]byte(`{"msg":"hello"}` + "\n"))
	sink.Flush()
	sink.SetEnabled(false)
	sink.Write([]byte("disabled\n"))
	cancel()
	sink.Flush()
	b, _ := os.ReadFile(filepath.Join(s.Root, LiveDir, PanelScope, Clock{}.Day(time.Now())+".log"))
	if string(b) != `{"msg":"hello"}`+"\n" {
		t.Fatalf("panel log file = %q", b)
	}
}

func TestDayCapWritesOneMarkerAndDropsTheRest(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	s.SetLimits(Limits{DayMaxBytes: 1 << 20})
	line := bytes.Repeat([]byte("y"), 1023) // 1 KiB with the newline
	var batch []Line
	for range 700 {
		batch = append(batch, Line{At: now, Text: line})
	}
	for range 3 { // 2100 KiB offered against a 1 MiB cap
		if err := s.Append(BotScope(botA), batch); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(s.Root, LiveDir, "bots", botA, "2026-10-04.log")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), capMarker); n != 1 {
		t.Fatalf("want exactly one marker line, got %d", n)
	}
	if int64(len(b)) > 1<<20+512 {
		t.Fatalf("file is %d bytes, over the cap", len(b))
	}
	if !strings.HasSuffix(string(b), "\n") || !strings.HasPrefix(strings.Split(string(b), "\n")[1024], capMarker) {
		t.Fatal("the cap must keep whole lines and end with the marker")
	}
	days, _ := s.Days(BotScope(botA))
	if len(days) != 1 || !days[0].Capped {
		t.Fatalf("days = %+v, want the live day reported as capped", days)
	}

	// A restart (fresh store state) still knows the day is capped.
	s2 := newStore(t, &now, Clock{Loc: time.UTC})
	s2.Root = s.Root
	s2.SetLimits(Limits{DayMaxBytes: 1 << 20})
	if err := s2.Append(BotScope(botA), batch[:1]); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(p); len(b2) != len(b) {
		t.Fatal("lines were appended after the marker")
	}
	// Other days and other scopes are unaffected.
	if err := s2.Append(PanelScope, batch[:2]); err != nil {
		t.Fatal(err)
	}
	if d, _ := s2.Days(PanelScope); len(d) != 1 || d[0].Capped || d[0].Bytes != 2048 {
		t.Fatalf("panel days = %+v", d)
	}
}

func TestLowDiskRefusesWritesAndSkipsCapture(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	free := uint64(10 << 20)
	s.FreeSpace = func(string) (uint64, error) { return free, nil }
	s.SetLimits(Limits{MinFreeBytes: 1 << 30})
	if err := s.Append(PanelScope, []Line{{At: now, Text: []byte("x")}}); !errors.Is(err, ErrLowDisk) {
		t.Fatalf("append on a full disk = %v", err)
	}
	c := &Capturer{Store: s, Now: func() time.Time { return now },
		Targets: func(context.Context) ([]Target, error) {
			t.Fatal("capture must not start on a full disk")
			return nil, nil
		}}
	if n, errs := c.Pass(context.Background()); n != 0 || len(errs) != 1 || !errors.Is(errs[0], ErrLowDisk) {
		t.Fatalf("pass = %d %v", n, errs)
	}
	free = 2 << 30
	s.SetLimits(Limits{MinFreeBytes: 1<<30 + 1}) // a change re-checks at once
	if err := s.Append(PanelScope, []Line{{At: now, Text: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
}

func TestSizeCapCountsLiveFiles(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s := newStore(t, &now, Clock{Loc: time.UTC})
	for d := 1; d <= 5; d++ {
		s.Append(PanelScope, []Line{{At: time.Date(2026, 10, d, 1, 0, 0, 0, time.UTC), Text: bytes.Repeat([]byte("x"), 100)}})
	}
	s.Rotate()
	u := s.Usage()
	// Today's live file alone is as large as all archives: with a cap of
	// the archive total, archives must make room for it.
	s.Append(PanelScope, []Line{{At: now, Text: bytes.Repeat([]byte("z"), int(u.ArchivedBytes))}})
	r := s.Prune(Retention{MaxBytes: u.ArchivedBytes + u.ArchivedBytes/2})
	if r.Deleted == 0 {
		t.Fatalf("live bytes were not counted: %+v usage %+v", r, s.Usage())
	}
	if after := s.Usage(); after.ArchivedBytes+after.LiveBytes > u.ArchivedBytes+u.ArchivedBytes/2 {
		t.Fatalf("over the cap after prune: %+v", after)
	}
}
