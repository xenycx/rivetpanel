package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type collabView struct {
	Ticket   ticketDTO
	Messages []ticketMessageDTO
	Watchers []personDTO
	Staff    bool
	Manage   bool
}

func getView(t *testing.T, c *client, path string) (collabView, string) {
	t.Helper()
	raw := c.mustStatus(200, "GET", path, nil)
	var v collabView
	json.Unmarshal(raw, &v)
	return v, string(raw)
}

func lastMessage(v collabView) ticketMessageDTO { return v.Messages[len(v.Messages)-1] }

// Tickets have RT-000001 keys that also open them.
func TestTicketKeys(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	tk := openTicket(t, alice, map[string]any{"subject": "First", "body": "x"})
	if tk.Key != "RT-000001" {
		t.Fatalf("key: %q", tk.Key)
	}
	v, _ := getView(t, alice, "/api/v1/tickets/RT-000001")
	if v.Ticket.ID != tk.ID {
		t.Fatalf("by key: %+v", v.Ticket)
	}
	alice.mustStatus(200, "GET", "/api/v1/tickets/rt-1", nil)
	bob.mustStatus(404, "GET", "/api/v1/tickets/RT-000001", nil)
	alice.mustStatus(404, "GET", "/api/v1/tickets/RT-000002", nil)
	if ns, _ := inbox(t, admin); len(ns) == 0 || !strings.Contains(ns[0].Title, "New ticket RT-000001: First") {
		t.Fatalf("staff inbox: %+v", ns)
	}
}

// Authors edit and remove their own messages; staff may remove any; the
// first message stays; removal purges the text and its history; requesters
// never see staff edit history.
func TestTicketMessageEditAndRemove(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	tk := openTicket(t, alice, map[string]any{"subject": "Help", "body": "first message"})
	path := "/api/v1/tickets/" + tk.ID

	var v collabView
	json.Unmarshal(alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "my token is SECRET-123"}), &v)
	mine := lastMessage(v)
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "Staff answer"}), &v)
	staffMsg := lastMessage(v)
	first := v.Messages[0]
	mp := func(id string) string { return path + "/messages/" + id }

	// Only the author edits; others get a refusal or not found.
	admin.mustStatus(400, "PATCH", mp(mine.ID), map[string]any{"body": "hijack"})
	alice.mustStatus(400, "PATCH", mp(staffMsg.ID), map[string]any{"body": "hijack"})
	bob.mustStatus(404, "PATCH", mp(mine.ID), map[string]any{"body": "x"})
	alice.mustStatus(400, "PATCH", mp(mine.ID), map[string]any{"body": "  "})
	json.Unmarshal(alice.mustStatus(200, "PATCH", mp(mine.ID), map[string]any{"body": "my token is (removed)"}), &v)
	var edited ticketMessageDTO
	for _, m := range v.Messages {
		if m.ID == mine.ID {
			edited = m
		}
	}
	if edited.EditedAtMS == nil || edited.Body != "my token is (removed)" {
		t.Fatalf("edited: %+v", edited)
	}
	// History: the author and staff, never another account.
	var revs struct{ Revisions []struct{ Body string } }
	json.Unmarshal(alice.mustStatus(200, "GET", mp(mine.ID)+"/revisions", nil), &revs)
	if len(revs.Revisions) != 1 || revs.Revisions[0].Body != "my token is SECRET-123" {
		t.Fatalf("revisions: %+v", revs)
	}
	admin.mustStatus(200, "GET", mp(mine.ID)+"/revisions", nil)
	json.Unmarshal(admin.mustStatus(200, "PATCH", mp(staffMsg.ID), map[string]any{"body": "Staff answer, corrected"}), &v)
	alice.mustStatus(404, "GET", mp(staffMsg.ID)+"/revisions", nil)

	// The first message cannot be removed by anyone.
	alice.mustStatus(400, "DELETE", mp(first.ID), nil)
	admin.mustStatus(400, "DELETE", mp(first.ID), nil)
	// The requester cannot remove a staff message; the author can remove its own.
	alice.mustStatus(400, "DELETE", mp(staffMsg.ID), nil)
	alice.mustStatus(200, "DELETE", mp(mine.ID), nil)
	alice.mustStatus(400, "DELETE", mp(mine.ID), nil) // already removed
	alice.mustStatus(400, "PATCH", mp(mine.ID), map[string]any{"body": "back"})
	json.Unmarshal(alice.mustStatus(200, "GET", mp(mine.ID)+"/revisions", nil), &revs)
	if len(revs.Revisions) != 0 {
		t.Fatalf("history served after removal: %+v", revs)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM support_message_revisions WHERE message_id = ?`, mine.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("revisions kept after removal: %d", n)
	}
	_, raw := getView(t, admin, path)
	if strings.Contains(raw, "SECRET-123") || strings.Contains(raw, "(removed)\"") {
		t.Fatalf("removed text still served: %s", raw)
	}
	// Staff moderation: remove the requester's reply.
	json.Unmarshal(alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "rude words"}), &v)
	admin.mustStatus(200, "DELETE", mp(lastMessage(v).ID), nil)
	v, _ = getView(t, alice, path)
	if m := lastMessage(v); !m.Deleted || m.Body != "" || m.DeletedBy != "" {
		t.Fatalf("requester sees removed message as: %+v", m)
	}
	v, _ = getView(t, admin, path)
	if m := lastMessage(v); !m.Deleted || m.DeletedBy == "" {
		t.Fatalf("staff sees removed message as: %+v", m)
	}

	// Once closed, the requester can no longer edit its messages.
	json.Unmarshal(alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "late"}), &v)
	late := lastMessage(v)
	alice.mustStatus(200, "PATCH", path, map[string]any{"status": "closed"})
	alice.mustStatus(400, "PATCH", mp(late.ID), map[string]any{"body": "later"})

	for _, a := range []string{"support.message_edit", "support.message_delete"} {
		e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = ? AND outcome = 'ok'`, a).Scan(&n)
		if n == 0 {
			t.Fatalf("%s not audited", a)
		}
	}
}

// Reactions: on visible messages only, toggled, named for staff only.
func TestTicketReactions(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	tk := openTicket(t, alice, map[string]any{"subject": "Help", "body": "x"})
	path := "/api/v1/tickets/" + tk.ID
	var v collabView
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "answer"}), &v)
	answer := lastMessage(v)
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "note", "internal": true}), &v)
	note := lastMessage(v)

	alice.mustStatus(404, "PUT", path+"/messages/"+note.ID+"/reactions/thanks", nil)
	alice.mustStatus(400, "PUT", path+"/messages/"+answer.ID+"/reactions/love", nil)
	alice.mustStatus(200, "PUT", path+"/messages/"+answer.ID+"/reactions/thanks", nil)
	alice.mustStatus(200, "PUT", path+"/messages/"+answer.ID+"/reactions/thanks", nil) // idempotent
	admin.mustStatus(200, "PUT", path+"/messages/"+answer.ID+"/reactions/thanks", nil)
	v, _ = getView(t, alice, path)
	r := v.Messages[1].Reactions
	if len(r) != 1 || r[0].Count != 2 || !r[0].Mine || strings.Contains(strings.Join(r[0].Names, ","), "admin") {
		t.Fatalf("requester reactions: %+v", r)
	}
	v, _ = getView(t, admin, path)
	if r := v.Messages[1].Reactions; len(r) != 1 || len(r[0].Names) != 2 {
		t.Fatalf("staff reactions: %+v", r)
	}
	alice.mustStatus(200, "DELETE", path+"/messages/"+answer.ID+"/reactions/thanks", nil)
	v, _ = getView(t, alice, path)
	if r := v.Messages[1].Reactions; len(r) != 1 || r[0].Count != 1 || r[0].Mine {
		t.Fatalf("after unreact: %+v", r)
	}
}

// Mentions: internal notes only, staff who can read the ticket, notified.
func TestTicketMentions(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	agentRole := admin.createRole("Support agent", domain.PermTicketsManage, domain.PermTicketsCreate)
	agent := e.user("agent@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("agent@x.io"), map[string]any{"role": agentRole})
	tk := openTicket(t, alice, map[string]any{"subject": "Refund", "body": "x"})
	path := "/api/v1/tickets/" + tk.ID
	aid := e.userID("agent@x.io")

	admin.mustStatus(400, "POST", path+"/messages", map[string]any{"body": "@agent look", "mentions": []string{aid}})
	admin.mustStatus(400, "POST", path+"/messages", map[string]any{"body": "@alice", "internal": true, "mentions": []string{e.userID("alice@x.io")}})
	alice.mustStatus(403, "POST", path+"/messages", map[string]any{"body": "@agent", "internal": true, "mentions": []string{aid}})
	var v collabView
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "@agent can you check MENTION-NOTE", "internal": true,
		"mentions": []string{aid}}), &v)
	if m := lastMessage(v); len(m.Mentions) != 1 || m.Mentions[0] != aid {
		t.Fatalf("mentions: %+v", m)
	}
	ns, _ := inbox(t, agent)
	found := false
	for _, n := range ns {
		if strings.Contains(n.Title, "You were mentioned on RT-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent inbox: %+v", ns)
	}
	_, raw := getView(t, alice, path)
	if strings.Contains(raw, "MENTION-NOTE") || strings.Contains(raw, aid) {
		t.Fatalf("mention leaks to requester: %s", raw)
	}
}

// Labels and saved replies: staff only, managed with tickets.manage, never
// shown to requesters.
func TestTicketLabelsAndSavedReplies(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	readerRole := admin.createRole("Support reader", domain.PermTicketsViewAll)
	reader := e.user("reader@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("reader@x.io"), map[string]any{"role": readerRole})
	tk := openTicket(t, alice, map[string]any{"subject": "Bug", "body": "x"})
	path := "/api/v1/tickets/" + tk.ID

	alice.mustStatus(403, "GET", "/api/v1/tickets/labels", nil)
	alice.mustStatus(403, "POST", "/api/v1/tickets/labels", map[string]any{"name": "bug"})
	reader.mustStatus(403, "POST", "/api/v1/tickets/labels", map[string]any{"name": "bug"})
	admin.mustStatus(400, "POST", "/api/v1/tickets/labels", map[string]any{"name": "bug", "color": "pink"})
	var l labelDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/tickets/labels", map[string]any{"name": " VIP-LABEL ", "color": "warn"}), &l)
	if l.Name != "VIP-LABEL" || l.Color != "warn" {
		t.Fatalf("label: %+v", l)
	}
	admin.mustStatus(400, "POST", "/api/v1/tickets/labels", map[string]any{"name": "vip-label"})
	reader.mustStatus(200, "GET", "/api/v1/tickets/labels", nil)
	reader.mustStatus(403, "PUT", path+"/labels", map[string]any{"labels": []string{l.ID}})
	admin.mustStatus(400, "PUT", path+"/labels", map[string]any{"labels": []string{"00000000-0000-0000-0000-000000000000"}})
	v := collabView{}
	json.Unmarshal(admin.mustStatus(200, "PUT", path+"/labels", map[string]any{"labels": []string{l.ID}}), &v)
	if len(v.Ticket.Labels) != 1 || v.Ticket.Labels[0] != l.ID {
		t.Fatalf("ticket labels: %+v", v.Ticket)
	}
	var all struct{ Tickets []ticketDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/tickets?scope=all", nil), &all)
	if len(all.Tickets) != 1 || len(all.Tickets[0].Labels) != 1 {
		t.Fatalf("queue labels: %+v", all.Tickets)
	}
	for _, p := range []string{path, "/api/v1/tickets"} {
		raw := string(alice.mustStatus(200, "GET", p, nil))
		if strings.Contains(raw, l.ID) || strings.Contains(raw, "\"labels\"") {
			t.Fatalf("labels leak to requester at %s: %s", p, raw)
		}
	}
	admin.mustStatus(204, "DELETE", "/api/v1/tickets/labels/"+l.ID, nil)
	v, _ = getView(t, admin, path)
	if len(v.Ticket.Labels) != 0 {
		t.Fatalf("deleted label still on ticket: %+v", v.Ticket.Labels)
	}

	// Saved replies.
	alice.mustStatus(403, "GET", "/api/v1/tickets/replies", nil)
	reader.mustStatus(403, "GET", "/api/v1/tickets/replies", nil)
	admin.mustStatus(400, "POST", "/api/v1/tickets/replies", map[string]any{"title": "", "body": "x"})
	var r savedReplyDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/tickets/replies", map[string]any{"title": "Restart first", "body": "Please restart the bot."}), &r)
	json.Unmarshal(admin.mustStatus(200, "PATCH", "/api/v1/tickets/replies/"+r.ID, map[string]any{"title": "Restart", "body": "Restart it."}), &r)
	var rs struct{ Replies []savedReplyDTO }
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/tickets/replies", nil), &rs)
	if len(rs.Replies) != 1 || rs.Replies[0].Title != "Restart" || rs.Replies[0].Body != "Restart it." {
		t.Fatalf("replies: %+v", rs)
	}
	admin.mustStatus(204, "DELETE", "/api/v1/tickets/replies/"+r.ID, nil)
	admin.mustStatus(404, "DELETE", "/api/v1/tickets/replies/"+r.ID, nil)
}

// Watchers: staff follow tickets and hear about requester replies;
// requesters never see watchers.
func TestTicketWatchers(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	readerRole := admin.createRole("Support reader", domain.PermTicketsViewAll)
	reader := e.user("reader@x.io", domain.RoleUser)
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("reader@x.io"), map[string]any{"role": readerRole})
	tk := openTicket(t, alice, map[string]any{"subject": "Watch me", "body": "x"})
	path := "/api/v1/tickets/" + tk.ID

	alice.mustStatus(403, "PUT", path+"/watch", nil)
	var v collabView
	json.Unmarshal(reader.mustStatus(200, "PUT", path+"/watch", nil), &v)
	if !v.Ticket.Watching || len(v.Watchers) != 1 {
		t.Fatalf("watch: %+v", v)
	}
	// Assign so that requester replies go to followers only.
	admin.mustStatus(200, "PATCH", path, map[string]any{"assignee_id": e.userID("admin@x.io")})
	alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "any news?"})
	ns, _ := inbox(t, reader)
	found := false
	for _, n := range ns {
		if strings.Contains(n.Title, "Reply from the requester on RT-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("watcher inbox: %+v", ns)
	}
	var all struct{ Tickets []ticketDTO }
	json.Unmarshal(reader.mustStatus(200, "GET", "/api/v1/tickets?scope=all", nil), &all)
	if len(all.Tickets) != 1 || !all.Tickets[0].Watching {
		t.Fatalf("queue watching: %+v", all.Tickets)
	}
	raw := string(alice.mustStatus(200, "GET", path, nil))
	if strings.Contains(raw, "watchers") || strings.Contains(raw, "reader@x.io") {
		t.Fatalf("watchers leak to requester: %s", raw)
	}
	reader.mustStatus(200, "DELETE", path+"/watch", nil)
	v, _ = getView(t, reader, path)
	if v.Ticket.Watching || len(v.Watchers) != 0 {
		t.Fatalf("unwatch: %+v", v)
	}
}

// Details: the requester while open or pending (linking only apps it can
// reach), staff any time (unlinking only); every change is recorded.
func TestTicketDetailsEdit(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)
	aliceBot := alice.createBot("alice-bot")
	bobBot := bob.createBot("bob-bot")
	tk := openTicket(t, alice, map[string]any{"subject": "Old subject", "body": "x"})
	path := "/api/v1/tickets/" + tk.ID

	alice.mustStatus(400, "PATCH", path, map[string]any{"subject": " "})
	alice.mustStatus(400, "PATCH", path, map[string]any{"category": "weather"})
	alice.mustStatus(400, "PATCH", path, map[string]any{"bot_id": bobBot})
	var v collabView
	json.Unmarshal(alice.mustStatus(200, "PATCH", path, map[string]any{"subject": "New  subject", "category": "technical", "bot_id": aliceBot}), &v)
	if v.Ticket.Subject != "New subject" || v.Ticket.Category != "technical" || v.Ticket.BotName != "alice-bot" {
		t.Fatalf("requester details: %+v", v.Ticket)
	}
	events := 0
	for _, m := range v.Messages {
		if m.Kind == "event" {
			events++
		}
	}
	if events != 3 {
		t.Fatalf("events: %+v", v.Messages)
	}
	admin.mustStatus(400, "PATCH", path, map[string]any{"bot_id": bobBot})
	json.Unmarshal(admin.mustStatus(200, "PATCH", path, map[string]any{"bot_id": "", "status": "resolved"}), &v)
	if v.Ticket.BotID != nil || v.Ticket.BotName != "" {
		t.Fatalf("unlink: %+v", v.Ticket)
	}
	// Resolved: the requester can no longer change details; staff still can.
	alice.mustStatus(400, "PATCH", path, map[string]any{"subject": "Again"})
	admin.mustStatus(200, "PATCH", path, map[string]any{"subject": "Staff subject"})
	bob.mustStatus(404, "PATCH", path, map[string]any{"subject": "nope"})
}
