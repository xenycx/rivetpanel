package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/live"
	"github.com/xenycx/rivetpanel/internal/service"
)

func TestLiveTopic(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/tickets/x/messages": "tickets",
		"/api/v1/bots/abc/power":     "bots",
		"/api/v1/admin/users/1":      "admin/users",
		"/api/v1/auth/login":         "",
		"/api/v1/me/profile":         "me",
		"/healthz":                   "",
	} {
		if got, _ := liveTopic(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// A successful change publishes its topic with the tab that made it; a
// failed one and a read publish nothing; notifications reach only their
// recipients.
func TestLiveHints(t *testing.T) {
	e := newEnv(t)
	hub := live.NewHub()
	ns := &service.NotificationService{Store: e.db, Live: hub}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true, Live: hub,
		Notifications: ns, Tickets: &service.TicketService{Store: e.db, Bots: e.bots, Notices: ns}})
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	adminSub, _ := hub.Subscribe(e.userID("admin@x.io"))
	aliceSub, _ := hub.Subscribe(e.userID("alice@x.io"))

	alice.mustStatus(400, "POST", "/api/v1/tickets", map[string]any{"subject": "", "body": "x"})
	alice.mustStatus(200, "GET", "/api/v1/tickets", nil)
	if len(aliceSub.C) != 0 {
		t.Fatalf("hint for a failed change or a read: %+v", <-aliceSub.C)
	}
	resp, _ := alice.req("POST", "/api/v1/tickets", map[string]any{"subject": "Help", "body": "x"}, func(r *http.Request) {
		r.Header.Set(csrfHeader, alice.csrf)
		r.Header.Set(liveTabHeader, "tab-7")
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	// The new ticket notified staff (admin only), then the change itself.
	if ev := <-adminSub.C; ev.Topic != "notifications" {
		t.Fatalf("admin first hint: %+v", ev)
	}
	if ev := <-adminSub.C; ev.Topic != "tickets" || ev.Origin != "tab-7" {
		t.Fatalf("admin change hint: %+v", ev)
	}
	if ev := <-aliceSub.C; ev.Topic != "tickets" {
		t.Fatalf("alice hint: %+v", ev)
	}
	if len(aliceSub.C) != 0 {
		t.Fatalf("alice got someone else's notification hint: %+v", <-aliceSub.C)
	}
	_ = admin
}
