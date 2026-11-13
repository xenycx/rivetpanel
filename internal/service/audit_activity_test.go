package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

type memAudit struct {
	mu  sync.Mutex
	evs []domain.AuditEvent
}

func (m *memAudit) InsertAudit(_ context.Context, e domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evs = append(m.evs, e)
	return nil
}
func (m *memAudit) ListAudit(context.Context, sqlite.AuditFilter) ([]domain.AuditEvent, error) {
	return nil, nil
}
func (m *memAudit) PruneAudit(context.Context, int64, int, int) (int64, error) { return 0, nil }
func (m *memAudit) all() []domain.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.AuditEvent(nil), m.evs...)
}

func TestAuditRecordsScheduledRunsAsSchedule(t *testing.T) {
	st := &memAudit{}
	a := &Audit{Store: st}
	a.RecordScheduled(context.Background(), domain.User{ID: "u", Email: "u@x"}, domain.Bot{ID: "b", Name: "bot"}, "restart", "skipped", "the bot is stopped")
	a.RecordScheduled(context.Background(), domain.User{ID: "u", Email: "u@x"}, domain.Bot{ID: "b", Name: "bot"}, "backup", "missed", "the panel was not running")
	evs := st.all()
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	if e := evs[0]; e.Action != "scheduled.restart" || e.Source != domain.SourceSchedule || e.Outcome != "ok" || *e.Target != "skipped: the bot is stopped" || *e.ActorID != "u" || *e.BotName != "bot" {
		t.Fatalf("skipped run = %+v", e)
	}
	if e := evs[1]; e.Outcome != "failed" {
		t.Fatalf("missed run = %+v", e)
	}
}

func TestAuditWatchCrashesRecordsTransitionsOnce(t *testing.T) {
	st := &memAudit{}
	now := time.UnixMilli(1_000_000)
	a := &Audit{Store: st, Now: func() time.Time { return now }}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.WatchCrashes(ctx, bus)
	time.Sleep(20 * time.Millisecond)
	pub := func(state, reason string) {
		bus.Publish(events.Status{BotID: "b", ObservedState: state, Reason: reason, LastError: "exited with code 1"})
		time.Sleep(10 * time.Millisecond)
	}
	pub("failed", domain.ReasonCrashBackoff) // first sighting: state unknown before, not recorded
	pub("running", "")
	pub("failed", domain.ReasonCrashBackoff) // a crash
	pub("failed", domain.ReasonCrashBackoff) // still failed: not again
	pub("running", "")
	pub("failed", domain.ReasonCrashBackoff) // within a minute: throttled
	pub("running", "")
	pub("failed", domain.ReasonGaveUp) // giving up is always recorded
	deadline := time.Now().Add(time.Second)
	for len(st.all()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	evs := st.all()
	if len(evs) != 2 || evs[0].Action != "bot.crash" || evs[1].Action != "bot.gave_up" || evs[0].Source != domain.SourceSystem || evs[0].Outcome != "failed" {
		t.Fatalf("events = %+v", evs)
	}
}
