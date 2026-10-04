package logbuf

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func logger(b *Buffer) *slog.Logger {
	return slog.New(b.Handler(slog.NewJSONHandler(io.Discard, nil)))
}

func TestKeepsNewestLinesAndNumbersThem(t *testing.T) {
	b := New(16)
	l := logger(b)
	for i := 0; i < 40; i++ {
		l.Info("line", "n", i)
	}
	st := b.Stats()
	if st.Held != 16 || st.LatestSeq != 40 || st.OldestSeq != 25 || st.Total != 40 {
		t.Fatalf("%+v", st)
	}
	got := b.Find(Query{Limit: 1000})
	if len(got) != 16 || got[0].Seq != 25 || got[15].Seq != 40 {
		t.Fatalf("order/range: first %d last %d n %d", got[0].Seq, got[len(got)-1].Seq, len(got))
	}
	after := b.Find(Query{After: 38})
	if len(after) != 2 || after[0].Seq != 39 {
		t.Fatalf("after: %+v", after)
	}
	last3 := b.Find(Query{Limit: 3})
	if len(last3) != 3 || last3[0].Seq != 38 || last3[2].Seq != 40 {
		t.Fatalf("limit keeps the newest, oldest first: %+v", last3)
	}
}

func TestLevelFilterSearchAndCounters(t *testing.T) {
	b := New(64)
	l := logger(b)
	l.Info("starting", "addr", ":8080")
	l.Warn("disk almost full", "free_mb", 90)
	l.Error("runner stopped", "err", "docker: connection refused")
	if n := len(b.Find(Query{MinLevel: "warn"})); n != 2 {
		t.Fatalf("warn+ = %d", n)
	}
	if n := len(b.Find(Query{MinLevel: "error"})); n != 1 {
		t.Fatalf("error = %d", n)
	}
	if r := b.Find(Query{Search: "CONNECTION refused"}); len(r) != 1 || r[0].Level != "error" {
		t.Fatalf("search in attrs: %+v", r)
	}
	if r := b.Find(Query{Search: "addr"}); len(r) != 1 {
		t.Fatalf("search in attr keys: %+v", r)
	}
	st := b.Stats()
	if st.Errors != 1 || st.Warnings != 1 || st.LastErrMS == 0 {
		t.Fatalf("%+v", st)
	}
}

func TestSecretsAreNotKept(t *testing.T) {
	b := New(16)
	l := logger(b).With("api_key", "sk-live-123").WithGroup("req")
	l.Warn("FIRST-RUN SETUP", "setup_code", "ABCD-EFGH", "Authorization", "Bearer abc", "path", "/api")
	e := b.Find(Query{})[0]
	for _, a := range e.Attrs {
		if strings.Contains(a.Value, "sk-live") || strings.Contains(a.Value, "ABCD") || strings.Contains(a.Value, "Bearer") {
			t.Fatalf("secret kept: %+v", a)
		}
	}
	var path string
	for _, a := range e.Attrs {
		if a.Key == "req.path" {
			path = a.Value
		}
	}
	if path != "/api" {
		t.Fatalf("group prefix lost: %+v", e.Attrs)
	}
}

func TestLongValuesAreClipped(t *testing.T) {
	b := New(16)
	logger(b).Info(strings.Repeat("m", 5000), "v", strings.Repeat("é", 5000))
	e := b.Find(Query{})[0]
	if len([]rune(e.Msg)) > maxMsg+1 || len([]rune(e.Attrs[0].Value)) > maxValue+1 {
		t.Fatalf("not clipped: %d %d", len(e.Msg), len(e.Attrs[0].Value))
	}
}

// The ring grows with the lines it receives instead of reserving its whole
// capacity up front, and behaves the same before and after it fills.
func TestRingGrowsOnDemand(t *testing.T) {
	b := New(16)
	if len(b.ring) != 0 || b.Stats().Capacity != 16 {
		t.Fatalf("new buffer holds %d entries, capacity %d", len(b.ring), b.Stats().Capacity)
	}
	l := logger(b)
	for i := 0; i < 5; i++ {
		l.Info("line", "n", i)
	}
	if st := b.Stats(); st.Held != 5 || st.OldestSeq != 1 || st.LatestSeq != 5 || len(b.ring) != 5 {
		t.Fatalf("partly filled: %+v (ring %d)", st, len(b.ring))
	}
	got := b.Find(Query{Limit: 100})
	if len(got) != 5 || got[0].Seq != 1 || got[4].Seq != 5 {
		t.Fatalf("partly filled order: %+v", got)
	}
	for i := 5; i < 17; i++ { // fill exactly, then wrap once
		l.Info("line", "n", i)
	}
	if st := b.Stats(); st.Held != 16 || st.OldestSeq != 2 || st.LatestSeq != 17 || len(b.ring) != 16 {
		t.Fatalf("wrapped: %+v (ring %d)", st, len(b.ring))
	}
}
