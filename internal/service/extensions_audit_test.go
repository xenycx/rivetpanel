package service

import (
	"context"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

type auditCapture struct{ events []domain.AuditEvent }

func (c *auditCapture) InsertAudit(_ context.Context, e domain.AuditEvent) error {
	c.events = append(c.events, e)
	return nil
}
func (c *auditCapture) ListAudit(context.Context, sqlite.AuditFilter) ([]domain.AuditEvent, error) {
	return nil, nil
}
func (c *auditCapture) PruneAudit(context.Context, int64, int, int) (int64, error) { return 0, nil }

// Add-on queries record the app's name like every other app action; without
// it the activity summary could not name the app.
func TestAddonQueryAuditRecordsAppName(t *testing.T) {
	store := &auditCapture{}
	s := &ExtensionService{Audit: &Audit{Store: store}}
	s.recordQuery(context.Background(), domain.User{ID: "u", Email: "u@x"}, domain.Bot{ID: "b1", Name: "my-app"}, "postgres", "read",
		"0123456789abcdef", nil)
	if len(store.events) != 1 {
		t.Fatalf("events = %d", len(store.events))
	}
	e := store.events[0]
	if e.Action != "addon.query" || e.BotID == nil || *e.BotID != "b1" || e.BotName == nil || *e.BotName != "my-app" {
		t.Fatalf("event = %+v", e)
	}
}
