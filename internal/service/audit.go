package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
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
