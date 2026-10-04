package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func supportEnv(t *testing.T) (*env, *service.NotificationService) {
	e := newEnv(t)
	ns := &service.NotificationService{Store: e.db}
	e.bots.Notices = ns
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true,
		Clients:       &service.APIClientService{Store: e.db, Bots: e.bots},
		Notifications: ns, Tickets: &service.TicketService{Store: e.db, Bots: e.bots, Notices: ns}})
	return e, ns
}

type ticketView struct {
	Ticket   ticketDTO
	Messages []ticketMessageDTO
	Staff    bool
	Manage   bool
}

func openTicket(t *testing.T, c *client, body map[string]any) ticketDTO {
	t.Helper()
	var out ticketDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/tickets", body), &out)
	if out.ID == "" || out.Number == 0 {
		t.Fatalf("ticket: %+v", out)
	}
	return out
}

func inbox(t *testing.T, c *client) (ns []notificationDTO, unread int) {
	t.Helper()
	var out struct {
		Notifications []notificationDTO
		Unread        int
	}
	json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/notifications", nil), &out)
	return out.Notifications, out.Unread
}

// Requesters see only their own tickets and never internal notes; staff
// see everything they outrank; permissions are enforced on every route.
func TestTicketAuthorization(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	aliceBot := alice.createBot("alice-bot")
	bobBot := bob.createBot("bob-bot")

	// A bot the requester cannot access cannot be linked.
	alice.mustStatus(400, "POST", "/api/v1/tickets", map[string]any{"subject": "help", "body": "x", "bot_id": bobBot})
	alice.mustStatus(400, "POST", "/api/v1/tickets", map[string]any{"subject": " ", "body": "x"})
	alice.mustStatus(400, "POST", "/api/v1/tickets", map[string]any{"subject": "s", "body": "x", "priority": "panic"})
	tk := openTicket(t, alice, map[string]any{"subject": "My bot  will not start", "category": "technical", "priority": "high",
		"bot_id": aliceBot, "body": "It exits at once."})
	if tk.Subject != "My bot will not start" || tk.Status != "open" || tk.BotName != "alice-bot" || tk.RequesterEmail != "" {
		t.Fatalf("created: %+v", tk)
	}
	// The administrator (staff) was told.
	if ns, unread := inbox(t, admin); unread != 1 || !strings.Contains(ns[0].Title, "New ticket #") || ns[0].Link != "/support/"+tk.ID {
		t.Fatalf("staff inbox: %+v", ns)
	}

	// Another account: not found everywhere, not listed, no staff list.
	path := "/api/v1/tickets/" + tk.ID
	bob.mustStatus(404, "GET", path, nil)
	bob.mustStatus(404, "POST", path+"/messages", map[string]any{"body": "me too"})
	bob.mustStatus(404, "PATCH", path, map[string]any{"status": "closed"})
	bob.mustStatus(403, "GET", "/api/v1/tickets?scope=all", nil)
	bob.mustStatus(403, "GET", "/api/v1/tickets/staff", nil)
	var list struct{ Tickets []ticketDTO }
	json.Unmarshal(bob.mustStatus(200, "GET", "/api/v1/tickets", nil), &list)
	if len(list.Tickets) != 0 {
		t.Fatalf("bob sees: %+v", list.Tickets)
	}
	bob.mustStatus(404, "GET", "/api/v1/tickets/not-a-uuid", nil)

	// Staff answer publicly (the ticket waits for the requester) and add an
	// internal note.
	var v ticketView
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "Please send the log."}), &v)
	if v.Ticket.Status != "pending" || !v.Staff || !v.Manage || v.Ticket.RequesterEmail != "alice@x.io" {
		t.Fatalf("after staff reply: %+v", v)
	}
	admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "SECRET-NOTE suspect abuse", "internal": true})

	// The requester never sees the note or the staff address.
	raw := alice.mustStatus(200, "GET", path, nil)
	if strings.Contains(string(raw), "SECRET-NOTE") || strings.Contains(string(raw), "admin@x.io") {
		t.Fatalf("requester view leaks staff data: %s", raw)
	}
	json.Unmarshal(raw, &v)
	if len(v.Messages) != 2 || v.Messages[1].Author != "Support team" || v.Staff || v.Manage {
		t.Fatalf("requester view: %+v", v)
	}
	var mine struct{ Tickets []ticketDTO }
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/tickets", nil), &mine)
	if len(mine.Tickets) != 1 || mine.Tickets[0].Messages != 2 {
		t.Fatalf("requester list: %+v", mine.Tickets)
	}
	// Notified about the public reply, not about the note.
	ns, _ := inbox(t, alice)
	if len(ns) != 1 || !strings.Contains(ns[0].Title, "New reply") || strings.Contains(ns[0].Body, "SECRET") {
		t.Fatalf("requester inbox: %+v", ns)
	}

	// Requesters cannot write notes or change priority/assignment; a reply
	// reopens the ticket; they can close it, then cannot reply.
	alice.mustStatus(403, "POST", path+"/messages", map[string]any{"body": "x", "internal": true})
	alice.mustStatus(403, "PATCH", path, map[string]any{"priority": "urgent"})
	alice.mustStatus(403, "PATCH", path, map[string]any{"assignee_id": e.userID("alice@x.io")})
	json.Unmarshal(alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "Here it is."}), &v)
	if v.Ticket.Status != "open" {
		t.Fatalf("requester reply: %s", v.Ticket.Status)
	}
	alice.mustStatus(403, "PATCH", path, map[string]any{"status": "resolved"})
	alice.mustStatus(200, "PATCH", path, map[string]any{"status": "closed"})
	alice.mustStatus(400, "POST", path+"/messages", map[string]any{"body": "again"})

	// A role without tickets.create cannot open or read tickets.
	rid := admin.createRole("NoSupport", domain.PermBotsConsole)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("bob@x.io"), map[string]any{"role": rid})
	bob.mustStatus(403, "POST", "/api/v1/tickets", map[string]any{"subject": "s", "body": "b"})
	bob.mustStatus(403, "GET", "/api/v1/tickets", nil)

	// Delegated staff: answers tickets of plain users, never sees an
	// administrator's ticket; view-only staff read notes but cannot answer.
	adminTk := openTicket(t, admin, map[string]any{"subject": "Admin's own", "body": "internal matter"})
	agentRole := admin.createRole("Support agent", domain.PermTicketsManage, domain.PermTicketsCreate)
	readerRole := admin.createRole("Support reader", domain.PermTicketsViewAll)
	agent := e.user("agent@x.io", domain.RoleUser)
	reader := e.user("reader@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("agent@x.io"), map[string]any{"role": agentRole})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("reader@x.io"), map[string]any{"role": readerRole})
	var all struct{ Tickets []ticketDTO }
	json.Unmarshal(agent.mustStatus(200, "GET", "/api/v1/tickets?scope=all", nil), &all)
	if len(all.Tickets) != 1 || all.Tickets[0].ID != tk.ID || all.Tickets[0].Messages != 4 {
		t.Fatalf("agent queue: %+v", all.Tickets)
	}
	agent.mustStatus(404, "GET", "/api/v1/tickets/"+adminTk.ID, nil)
	agent.mustStatus(404, "POST", "/api/v1/tickets/"+adminTk.ID+"/messages", map[string]any{"body": "peek"})
	raw = reader.mustStatus(200, "GET", path, nil)
	if !strings.Contains(string(raw), "SECRET-NOTE") {
		t.Fatalf("view_all should include notes: %s", raw)
	}
	reader.mustStatus(403, "POST", path+"/messages", map[string]any{"body": "hi"})
	reader.mustStatus(403, "PATCH", path, map[string]any{"status": "open"})
	reader.mustStatus(403, "GET", "/api/v1/tickets/staff", nil)

	// Assignment: only to accounts that can answer the ticket; the assignee
	// is told; the event is internal.
	admin.mustStatus(400, "PATCH", path, map[string]any{"assignee_id": e.userID("alice@x.io")})
	admin.mustStatus(400, "PATCH", path, map[string]any{"assignee_id": e.userID("reader@x.io")})
	json.Unmarshal(admin.mustStatus(200, "PATCH", path, map[string]any{"assignee_id": e.userID("agent@x.io"), "status": "open"}), &v)
	if v.Ticket.AssigneeEmail != "agent@x.io" || v.Ticket.Status != "open" {
		t.Fatalf("assign: %+v", v.Ticket)
	}
	if ns, _ := inbox(t, agent); len(ns) == 0 || !strings.Contains(ns[0].Title, "assigned to you") {
		t.Fatalf("assignee inbox: %+v", ns)
	}
	raw = alice.mustStatus(200, "GET", path, nil)
	if strings.Contains(string(raw), "Assigned to") || strings.Contains(string(raw), "agent@x.io") {
		t.Fatalf("assignment visible to requester: %s", raw)
	}
	var staff struct{ Staff []struct{ Email string } }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/tickets/staff", nil), &staff)
	if len(staff.Staff) != 2 {
		t.Fatalf("staff: %+v", staff.Staff)
	}

	// Audited, including a refused attempt on another account's ticket.
	var n int
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'support.ticket_create' AND outcome = 'ok'`).Scan(&n)
	if n != 2 {
		t.Fatalf("create audit rows: %d", n)
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'support.ticket_reply' AND outcome = 'denied'`).Scan(&n)
	if n == 0 {
		t.Fatal("denied reply not audited")
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'support.ticket_reply' AND target LIKE '%internal note%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("internal note audit rows: %d", n)
	}
}

// API clients reach tickets only with tickets.* in their list and never
// when limited to some bots; the notification inbox is session-only.
func TestTicketsAndNotificationsWithAPIClients(t *testing.T) {
	e, _ := supportEnv(t)
	alice := e.user("alice@x.io", domain.RoleUser)
	bot := alice.createBot("b")
	tk := openTicket(t, alice, map[string]any{"subject": "s", "body": "b"})

	tok, _ := mkClient(t, alice, map[string]any{"name": "support", "permissions": []string{domain.PermTicketsCreate}})
	wantStatus(t, e, tok, 200, "GET", "/api/v1/tickets", nil)
	wantStatus(t, e, tok, 200, "GET", "/api/v1/tickets/"+tk.ID, nil)
	wantStatus(t, e, tok, 201, "POST", "/api/v1/tickets/"+tk.ID+"/messages", map[string]any{"body": "from CI"})

	other, _ := mkClient(t, alice, map[string]any{"name": "power", "permissions": []string{domain.PermBotsPower}})
	wantStatus(t, e, other, 403, "GET", "/api/v1/tickets", nil)
	wantStatus(t, e, other, 403, "POST", "/api/v1/tickets", map[string]any{"subject": "s", "body": "b"})

	scoped, _ := mkClient(t, alice, map[string]any{"name": "scoped", "permissions": []string{domain.PermTicketsCreate}, "bot_ids": []string{bot}})
	wantStatus(t, e, scoped, 403, "GET", "/api/v1/tickets", nil)
	wantStatus(t, e, scoped, 403, "GET", "/api/v1/tickets/"+tk.ID, nil)

	wantStatus(t, e, tok, 403, "GET", "/api/v1/notifications", nil)
	wantStatus(t, e, tok, 403, "GET", "/api/v1/notifications/unread", nil)
	wantStatus(t, e, tok, 403, "PUT", "/api/v1/me/notification-prefs", map[string]any{"prefs": []any{}})
}

// Each account reads, marks and deletes only its own notifications.
func TestNotificationInboxIsolation(t *testing.T) {
	e, ns := supportEnv(t)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	ctx := context.Background()
	ns.Notify(ctx, e.userID("alice@x.io"), service.Notice{Category: domain.NotifyBotAlerts, Title: "one", Link: "/bots/x"})
	ns.Notify(ctx, e.userID("alice@x.io"), service.Notice{Category: domain.NotifyDeploys, Title: "two", Link: "https://evil.example/"})
	ns.Notify(ctx, e.userID("bob@x.io"), service.Notice{Category: domain.NotifyBotAlerts, Title: "bob's"})
	ns.Notify(ctx, e.userID("bob@x.io"), service.Notice{Category: "nonsense", Title: "dropped"})

	list, unread := inbox(t, alice)
	if len(list) != 2 || unread != 2 || list[0].Title != "two" || list[0].Link != "" || list[1].Link != "/bots/x" {
		t.Fatalf("alice: %d %+v", unread, list)
	}
	bobList, _ := inbox(t, bob)
	if len(bobList) != 1 {
		t.Fatalf("bob: %+v", bobList)
	}
	// Bob cannot touch Alice's rows.
	bob.mustStatus(404, "DELETE", "/api/v1/notifications/"+list[0].ID, nil)
	var marked struct{ Marked int }
	json.Unmarshal(bob.mustStatus(200, "POST", "/api/v1/notifications/read", map[string]any{"ids": []string{list[0].ID, list[1].ID}}), &marked)
	if marked.Marked != 0 {
		t.Fatalf("bob marked alice's: %d", marked.Marked)
	}
	alice.mustStatus(400, "POST", "/api/v1/notifications/read", map[string]any{})
	json.Unmarshal(alice.mustStatus(200, "POST", "/api/v1/notifications/read", map[string]any{"ids": []string{list[0].ID}}), &marked)
	if marked.Marked != 1 {
		t.Fatalf("marked %d", marked.Marked)
	}
	var u struct{ Unread int }
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/notifications/unread", nil), &u)
	if u.Unread != 1 {
		t.Fatalf("unread %d", u.Unread)
	}
	alice.mustStatus(200, "POST", "/api/v1/notifications/read", map[string]any{"all": true})
	alice.mustStatus(204, "DELETE", "/api/v1/notifications/"+list[1].ID, nil)
	alice.mustStatus(200, "POST", "/api/v1/notifications/clear", nil)
	if l, n := inbox(t, alice); len(l) != 0 || n != 0 {
		t.Fatalf("after clear: %+v", l)
	}
	if l, n := inbox(t, bob); len(l) != 1 || n != 1 {
		t.Fatalf("bob after alice cleared: %+v", l)
	}
}

// Preferences switch categories off; the node category is only for
// accounts that manage nodes.
func TestNotificationPreferences(t *testing.T) {
	e, ns := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	var prefs struct {
		Categories []service.PrefView
	}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/me/notification-prefs", nil), &prefs)
	for _, c := range prefs.Categories {
		if c.Name == domain.NotifyNodes {
			t.Fatal("node category offered to a plain user")
		}
		if !c.InPanel {
			t.Fatalf("default off: %+v", c)
		}
	}
	alice.mustStatus(400, "PUT", "/api/v1/me/notification-prefs", map[string]any{"prefs": []map[string]any{{"category": "bogus", "in_panel": true}}})
	alice.mustStatus(200, "PUT", "/api/v1/me/notification-prefs", map[string]any{"prefs": []map[string]any{{"category": domain.NotifyTickets, "in_panel": false, "email": false}}})

	tk := openTicket(t, alice, map[string]any{"subject": "s", "body": "b"})
	admin.mustStatus(201, "POST", "/api/v1/tickets/"+tk.ID+"/messages", map[string]any{"body": "answer"})
	if l, _ := inbox(t, alice); len(l) != 0 {
		t.Fatalf("switched-off category delivered: %+v", l)
	}
	// Other categories still arrive; a node notice skips plain users.
	ns.NotifyHolders(context.Background(), domain.PermNodesManage, service.Notice{Category: domain.NotifyNodes, Title: "node down"})
	if l, _ := inbox(t, alice); len(l) != 0 {
		t.Fatalf("plain user got node notice: %+v", l)
	}
	if l, _ := inbox(t, admin); len(l) == 0 || l[0].Title != "node down" {
		t.Fatalf("admin node notice: %+v", l)
	}
}

// Producers fan out to the right accounts: sharing, invitations accepted,
// node offline/online (holders of nodes.manage only, after the grace
// period), announcements in the inbox.
func TestNotificationFanOut(t *testing.T) {
	e, ns := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	nodeRole := admin.createRole("Node ops", domain.PermNodesManage)
	ops := e.user("ops@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("ops@x.io"), map[string]any{"role": nodeRole})

	b := alice.createBot("shared-bot")
	alice.mustStatus(200, "PUT", "/api/v1/bots/"+b+"/users", map[string]any{"email": "bob@x.io", "permissions": int(domain.PermViewConsole)})
	if l, _ := inbox(t, bob); len(l) != 1 || l[0].Title != "alice@x.io shared shared-bot with you" || l[0].Link != "/bots/"+b {
		t.Fatalf("share notice: %+v", l)
	}
	if l, _ := inbox(t, alice); len(l) != 0 {
		t.Fatalf("actor notified about own action: %+v", l)
	}

	online := false
	w := &service.NodeNotices{Notices: ns, Grace: 20 * time.Millisecond, Online: func(string) bool { return online },
		Name: func(context.Context, string) string { return "edge-1" }}
	ctx := context.Background()
	// A quick reconnect sends nothing.
	w.Disconnected(ctx, "n1")
	w.Connected(ctx, "n1")
	time.Sleep(60 * time.Millisecond)
	if l, _ := inbox(t, admin); len(l) != 0 {
		t.Fatalf("flap notified: %+v", l)
	}
	w.Disconnected(ctx, "n1")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if l, _ := inbox(t, ops); len(l) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no offline notice")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l, _ := inbox(t, admin); len(l) != 1 || l[0].Title != "Node edge-1 is offline" {
		t.Fatalf("admin offline notice: %+v", l)
	}
	if l, _ := inbox(t, bob); len(l) != 1 { // only the share notice
		t.Fatalf("plain user got node notice: %+v", l)
	}
	online = true
	w.Connected(ctx, "n1")
	if l, _ := inbox(t, ops); len(l) != 2 || l[0].Title != "Node edge-1 is back online" {
		t.Fatalf("back online: %+v", l)
	}

	// Announcements reach the inbox (no email set up: in-panel only).
	n, err := ns.Announce(ctx, mustUser(t, e, "admin@x.io"), "Maintenance tonight", "<p>From <b>22:00</b>.</p>", service.AudienceAll)
	if err != nil || n != 4 {
		t.Fatalf("announce: %d %v", n, err)
	}
	if l, _ := inbox(t, alice); len(l) != 1 || l[0].Body != "From 22:00." || l[0].Category != domain.NotifyAnnouncements {
		t.Fatalf("announcement: %+v", l)
	}
	if _, err := ns.Announce(ctx, mustUser(t, e, "alice@x.io"), "x", "y", service.AudienceAll); err == nil {
		t.Fatal("plain user announced")
	}
}

func mustUser(t *testing.T, e *env, email string) domain.User {
	t.Helper()
	u, err := e.db.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Retention: each account keeps its newest rows; old rows are pruned.
func TestNotificationRetention(t *testing.T) {
	e, _ := supportEnv(t)
	e.user("alice@x.io", domain.RoleUser)
	id := e.userID("alice@x.io")
	now := time.Now()
	ns := &service.NotificationService{Store: e.db, Now: func() time.Time { return now }}
	ctx := context.Background()
	for i := 0; i < 205; i++ {
		now = now.Add(time.Millisecond)
		ns.Notify(ctx, id, service.Notice{Category: domain.NotifyBotAlerts, Title: "n"})
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM notifications WHERE user_id = ?`, id).Scan(&n)
	if n != 200 {
		t.Fatalf("kept %d", n)
	}
	now = now.Add(service.NotificationRetention + time.Hour)
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyBotAlerts, Title: "fresh"})
	// The new row already pushed the oldest one out (200 kept).
	if pruned, err := ns.Prune(ctx); err != nil || pruned != 199 {
		t.Fatalf("pruned %d %v", pruned, err)
	}
	e.db.QueryRow(`SELECT count(*) FROM notifications WHERE user_id = ?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("left %d", n)
	}
}

// Notification email goes only to verified addresses, follows the
// per-category switch and, for alert categories, the "Alert emails" switch.
func TestNotificationEmail(t *testing.T) {
	e, fm, ms := mailEnv(t)
	admin := e.user("a@x.io", domain.RoleAdmin)
	configureMail(t, admin, nil)
	u := e.user("u@x.io", domain.RoleUser)
	id := e.userID("u@x.io")
	ns := &service.NotificationService{Store: e.db, Mail: ms, Settings: ms.Settings}
	ctx := context.Background()

	// New accounts start unverified: in-panel only.
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyTickets, Title: "Reply on #1", Body: "hello", Link: "/support/x"})
	fm.none(t)
	if err := e.db.SetEmailVerified(ctx, id, true, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyTickets, Title: "Reply on #1", Body: "hello", Link: "/support/x"})
	if m := fm.next(t); m["to"] != "u@x.io" || !strings.Contains(m["text"], "https://panel.example.com/support/x") {
		t.Fatalf("ticket email: %v", m)
	}
	// Per-category switch.
	if err := ns.SetPrefs(ctx, mustUser(t, e, "u@x.io"), []domain.NotificationPref{{Category: domain.NotifyTickets, InPanel: true}}); err != nil {
		t.Fatal(err)
	}
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyTickets, Title: "again"})
	fm.none(t)
	// Alert categories also follow the master switch.
	u.mustStatus(200, "PUT", "/api/v1/me/email-alerts", map[string]bool{"enabled": false})
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyDeploys, Title: "Deployed"})
	fm.none(t)
	u.mustStatus(200, "PUT", "/api/v1/me/email-alerts", map[string]bool{"enabled": true})
	ns.Notify(ctx, id, service.Notice{Category: domain.NotifyDeploys, Title: "✅ Deployed bot"})
	if m := fm.next(t); m["subject"] != "Deployed bot" {
		t.Fatalf("alert email: %v", m)
	}
}
