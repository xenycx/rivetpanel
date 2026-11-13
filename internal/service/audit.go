package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// AuditStore is the persistence surface of the activity record.
type AuditStore interface {
	InsertAudit(ctx context.Context, e domain.AuditEvent) error
	ListAudit(ctx context.Context, f sqlite.AuditFilter) ([]domain.AuditEvent, error)
	PruneAudit(ctx context.Context, beforeMS int64, maxRows, batch int) (int64, error)
}

// Audit records who changed what. It is an activity record for the people
// using the panel, not tamper-proof evidence against a host administrator who
// can edit the database.
type Audit struct {
	Store     AuditStore
	Bots      *BotService
	Retention time.Duration // default 180 days
	MaxRows   int           // default 200,000
	Log       *slog.Logger
	Now       func() time.Time
}

func (a *Audit) now() int64 {
	if a.Now != nil {
		return a.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// Record stores an event; failures are logged, never returned.
func (a *Audit) Record(ctx context.Context, e domain.AuditEvent) {
	if a == nil {
		return
	}
	ctx, cancel := bg(ctx)
	defer cancel()
	if e.AtMS == 0 {
		e.AtMS = a.now()
	}
	if e.Outcome == "" {
		e.Outcome = "ok"
	}
	if e.Source == "" {
		e.Source = domain.SourcePerson
	}
	if err := a.Store.InsertAudit(ctx, e); err != nil && a.Log != nil {
		a.Log.Warn("audit: record", "action", e.Action, "err", err)
	}
}

// ForBot returns a bot's history to anyone with access to the bot.
func (a *Audit) ForBot(ctx context.Context, actor domain.User, botID string, before int64, limit int) ([]domain.AuditEvent, error) {
	if _, err := a.Bots.Authorize(ctx, actor, botID, permAny); err != nil {
		return nil, err
	}
	return a.Store.ListAudit(ctx, sqlite.AuditFilter{BotID: botID, BeforeID: before, Limit: limit})
}

// Visible returns what the actor may see across the panel: administrators see
// everything; others see events on bots they can access plus their own
// account's events.
func (a *Audit) Visible(ctx context.Context, actor domain.User, before int64, limit int) ([]domain.AuditEvent, error) {
	if actor.IsAdmin() {
		return a.Store.ListAudit(ctx, sqlite.AuditFilter{BeforeID: before, Limit: limit})
	}
	bots, err := a.Store.ListAudit(ctx, sqlite.AuditFilter{BotsOf: actor.ID, BeforeID: before, Limit: limit})
	if err != nil {
		return nil, err
	}
	own, err := a.Store.ListAudit(ctx, sqlite.AuditFilter{AccountOf: actor.ID, BeforeID: before, Limit: limit})
	if err != nil {
		return nil, err
	}
	return mergeAudit(bots, own, limit), nil
}

// mergeAudit merges two newest-first lists without duplicates.
func mergeAudit(a, b []domain.AuditEvent, limit int) []domain.AuditEvent {
	out := make([]domain.AuditEvent, 0, min(limit, len(a)+len(b)))
	i, j := 0, 0
	for len(out) < limit && (i < len(a) || j < len(b)) {
		switch {
		case j >= len(b) || (i < len(a) && a[i].ID > b[j].ID):
			out = append(out, a[i])
			i++
		case i >= len(a) || b[j].ID > a[i].ID:
			out = append(out, b[j])
			j++
		default: // same event in both lists
			out = append(out, a[i])
			i, j = i+1, j+1
		}
	}
	return out
}

// Prune enforces retention in bounded batches.
func (a *Audit) Prune(ctx context.Context) error {
	ret, rows := a.Retention, a.MaxRows
	if ret <= 0 {
		ret = 180 * 24 * time.Hour
	}
	if rows <= 0 {
		rows = 200_000
	}
	before := a.now() - ret.Milliseconds()
	for ctx.Err() == nil {
		n, err := a.Store.PruneAudit(ctx, before, rows, 1000)
		if err != nil || n < 1000 {
			return err
		}
	}
	return nil
}

// ActivityStore is the persistence surface of the unified timeline.
type ActivityStore interface {
	ListActivity(ctx context.Context, f sqlite.ActivityFilter) ([]domain.ActivityItem, error)
	ActivityStats(ctx context.Context, f sqlite.ActivityFilter, bucketMS int64, tzOffsetMin int) (domain.ActivityStats, error)
	ActivityPeople(ctx context.Context, f sqlite.ActivityFilter, limit int) ([]domain.Person, error)
}

// scope limits a filter to what the actor may see: administrators see
// everything; others see changes and work on apps they can access plus
// their own account's events.
func scope(actor domain.User, f sqlite.ActivityFilter) sqlite.ActivityFilter {
	if !actor.IsAdmin() {
		f.VisibleTo = actor.ID
	}
	return f
}

func (a *Audit) activity() (ActivityStore, error) {
	s, ok := a.Store.(ActivityStore)
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

// Timeline returns one page of the unified activity timeline.
func (a *Audit) Timeline(ctx context.Context, actor domain.User, f sqlite.ActivityFilter) ([]domain.ActivityItem, error) {
	s, err := a.activity()
	if err != nil {
		return nil, err
	}
	return s.ListActivity(ctx, scope(actor, f))
}

// TimelineStats summarizes a window of the timeline.
func (a *Audit) TimelineStats(ctx context.Context, actor domain.User, f sqlite.ActivityFilter, bucketMS int64, tzOffsetMin int) (domain.ActivityStats, error) {
	s, err := a.activity()
	if err != nil {
		return domain.ActivityStats{}, err
	}
	return s.ActivityStats(ctx, scope(actor, f), bucketMS, tzOffsetMin)
}

// TimelinePeople lists the accounts that appear in what the actor may see.
func (a *Audit) TimelinePeople(ctx context.Context, actor domain.User, f sqlite.ActivityFilter) ([]domain.Person, error) {
	s, err := a.activity()
	if err != nil {
		return nil, err
	}
	return s.ActivityPeople(ctx, scope(actor, f), 200)
}

// RecordScheduled records a scheduled action the panel ran on its owner's
// behalf. status is the schedule run status (ok, failed, denied, skipped,
// missed); msg is the run's message, which never contains secrets.
func (a *Audit) RecordScheduled(ctx context.Context, owner domain.User, bot domain.Bot, action, status, msg string) {
	if a == nil {
		return
	}
	outcome := "ok"
	switch status {
	case "failed", "missed":
		outcome = "failed"
	case "denied":
		outcome = "denied"
	}
	label := owner.Email
	target := clipService(status+": "+msg, 300)
	ev := domain.AuditEvent{Action: "scheduled." + action, Outcome: outcome, Source: domain.SourceSchedule,
		Target: &target, ActorLabel: &label}
	if owner.ID != "" {
		ev.ActorID = &owner.ID
	}
	if bot.ID != "" {
		ev.BotID, ev.BotName = &bot.ID, &bot.Name
	}
	a.Record(context.WithoutCancel(ctx), ev)
}

// crashRecordInterval bounds crash records per bot so a crash loop cannot
// flood the timeline.
const crashRecordInterval = time.Minute

// WatchCrashes records, as the panel, when an app crashes and when it gives
// up restarting. It returns when ctx ends.
func (a *Audit) WatchCrashes(ctx context.Context, bus *events.Bus) {
	sub := bus.SubscribeAll()
	defer sub.Close()
	prev := map[string]string{}    // bot id -> last observed state
	last := map[string]time.Time{} // bot id -> last crash record
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-sub.C:
			was, seen := prev[st.BotID]
			prev[st.BotID] = st.ObservedState
			if st.ObservedState != "failed" || !seen || was == "failed" {
				continue
			}
			var action string
			switch st.Reason {
			case domain.ReasonCrashBackoff:
				action = "bot.crash"
			case domain.ReasonGaveUp, domain.ReasonExitedNoRetry:
				action = "bot.gave_up"
			default:
				continue
			}
			now := time.UnixMilli(a.now())
			if t, ok := last[st.BotID]; ok && action == "bot.crash" && now.Sub(t) < crashRecordInterval {
				continue
			}
			last[st.BotID] = now
			ev := domain.AuditEvent{Action: action, Outcome: "failed", Source: domain.SourceSystem}
			id := st.BotID
			ev.BotID = &id
			if a.Bots != nil {
				if b, err := a.Bots.Store.GetBot(ctx, id); err == nil {
					ev.BotName = &b.Name
				}
			}
			if st.LastError != "" {
				t := clipService(st.LastError, 300)
				ev.Target = &t
			}
			a.Record(ctx, ev)
		}
	}
}
