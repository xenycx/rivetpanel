package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// A 1×1 PNG.
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

func uploadFile(t *testing.T, c *client, name string, data []byte, want int) attachmentDTO {
	t.Helper()
	resp, b := c.raw("POST", "/api/v1/tickets/attachments?name="+name, data, "application/octet-stream")
	if resp.StatusCode != want {
		t.Fatalf("upload %s: %d %s", name, resp.StatusCode, b)
	}
	var a attachmentDTO
	json.Unmarshal(b, &a)
	return a
}

// Files go with the message that lists them, are read by whoever reads the
// message (internal notes' files by staff only), cannot be borrowed by
// another account and are served so they cannot run.
func TestTicketAttachments(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	bob := e.user("bob@x.io", domain.RoleUser)

	// Types come from the bytes: an HTML page named .png is refused.
	uploadFile(t, alice, "evil.png", []byte("<html><script>alert(1)</script></html>"), 400)
	uploadFile(t, alice, "empty.txt", nil, 400)
	shot := uploadFile(t, alice, "../shot%22.png", tinyPNG, 201)
	if !shot.Image || shot.ContentType != "image/png" || shot.Name != "shot.png" || shot.URL != "" {
		t.Fatalf("upload: %+v", shot)
	}
	logf := uploadFile(t, alice, "app.log", []byte("panic: boom\n"), 201)
	// Larger than the panel's 1 MiB request cap, within the 10 MiB file limit.
	big := append(append([]byte{}, tinyPNG...), make([]byte, 3<<20)...)
	bigA := uploadFile(t, alice, "big.png", big, 201)
	alice.mustStatus(204, "DELETE", "/api/v1/tickets/attachments/"+bigA.ID, nil)

	// Another account cannot send or discard alice's uploads.
	bob.mustStatus(400, "POST", "/api/v1/tickets", map[string]any{"subject": "s", "body": "x", "attachments": []string{shot.ID}})
	bob.mustStatus(404, "DELETE", "/api/v1/tickets/attachments/"+shot.ID, nil)

	// Files alone make a message.
	tk := openTicket(t, alice, map[string]any{"subject": "Crash", "body": "", "attachments": []string{shot.ID, logf.ID}})
	path := "/api/v1/tickets/" + tk.ID
	var v ticketView
	json.Unmarshal(alice.mustStatus(200, "GET", path, nil), &v)
	first := v.Messages[0]
	if len(first.Attachments) != 2 || !strings.HasPrefix(first.Body, "Attached shot.png, app.log") || first.Attachments[0].URL == "" {
		t.Fatalf("first message: %+v", first)
	}
	// An upload is sent once.
	alice.mustStatus(400, "POST", path+"/messages", map[string]any{"body": "again", "attachments": []string{shot.ID}})

	// Pictures inline, everything else downloads; nothing runs.
	resp, body := alice.raw("GET", first.Attachments[0].URL, nil, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(body, tinyPNG) {
		t.Fatalf("image: %d %v", resp.StatusCode, resp.Header)
	}
	resp, _ = alice.raw("GET", first.Attachments[1].URL, nil, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("log: %d %v", resp.StatusCode, resp.Header)
	}
	if r, _ := bob.raw("GET", first.Attachments[0].URL, nil, ""); r.StatusCode != 404 {
		t.Fatalf("bob reads alice's file: %d", r.StatusCode)
	}
	if r, _ := admin.raw("GET", first.Attachments[0].URL, nil, ""); r.StatusCode != 200 {
		t.Fatalf("staff read: %d", r.StatusCode)
	}

	// An internal note's file: staff only.
	secret := uploadFile(t, admin, "notes.txt", []byte("internal"), 201)
	json.Unmarshal(admin.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "see file", "internal": true, "attachments": []string{secret.ID}}), &v)
	note := v.Messages[len(v.Messages)-1]
	if len(note.Attachments) != 1 {
		t.Fatalf("note: %+v", note)
	}
	if r, _ := alice.raw("GET", note.Attachments[0].URL, nil, ""); r.StatusCode != 404 {
		t.Fatalf("requester reads a note's file: %d", r.StatusCode)
	}

	// Removing a message deletes its files.
	reply := uploadFile(t, alice, "more.png", tinyPNG, 201)
	json.Unmarshal(alice.mustStatus(201, "POST", path+"/messages", map[string]any{"body": "one more", "attachments": []string{reply.ID}}), &v)
	last := v.Messages[len(v.Messages)-1]
	alice.mustStatus(200, "DELETE", path+"/messages/"+last.ID, nil)
	if r, _ := alice.raw("GET", last.Attachments[0].URL, nil, ""); r.StatusCode != 404 {
		t.Fatalf("removed message's file: %d", r.StatusCode)
	}

	// Discarding a pending upload.
	gone := uploadFile(t, alice, "gone.png", tinyPNG, 201)
	alice.mustStatus(204, "DELETE", "/api/v1/tickets/attachments/"+gone.ID, nil)
	alice.mustStatus(400, "POST", path+"/messages", map[string]any{"body": "x", "attachments": []string{gone.ID}})
}

// The automatic reply answers a new ticket on the team's behalf, fills its
// placeholders, leaves the ticket waiting for staff and is not counted as a
// reply.
func TestSupportAutoReply(t *testing.T) {
	e, _ := supportEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)

	var set supportSettingsDTO
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/tickets/settings", nil), &set)
	if set.AutoReply || set.AutoReplyBody != "" || set.MaxFiles != domain.TicketAttachmentsPerMsg {
		t.Fatalf("defaults for a requester: %+v", set)
	}
	alice.mustStatus(403, "PUT", "/api/v1/tickets/settings", map[string]any{"auto_reply": true, "auto_reply_body": "x"})
	admin.mustStatus(400, "PUT", "/api/v1/tickets/settings", map[string]any{"auto_reply": true, "auto_reply_body": " "})
	admin.mustStatus(400, "PUT", "/api/v1/tickets/settings", map[string]any{"response_hours": -1})
	json.Unmarshal(admin.mustStatus(200, "PUT", "/api/v1/tickets/settings", map[string]any{"auto_reply": true,
		"auto_reply_body": "Thanks {name}! {ticket} ({subject}) gets an answer {response_time}.", "response_hours": 48}), &set)
	if !set.AutoReply || set.ResponseTime != "within 2 days" {
		t.Fatalf("saved: %+v", set)
	}
	set = supportSettingsDTO{}
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/tickets/settings", nil), &set)
	if set.ResponseHours != 48 || set.AutoReplyBody != "" {
		t.Fatalf("requester view: %+v", set)
	}

	tk := openTicket(t, alice, map[string]any{"subject": "Help", "body": "please"})
	var v ticketView
	raw := alice.mustStatus(200, "GET", "/api/v1/tickets/"+tk.ID, nil)
	json.Unmarshal(raw, &v)
	if len(v.Messages) != 2 || !v.Messages[1].Auto || !v.Messages[1].Staff || v.Messages[1].Author != "Support team" ||
		v.Messages[1].Body != "Thanks there! "+domain.TicketKey(tk.Number)+" (Help) gets an answer within 2 days." || v.Ticket.Status != "open" {
		t.Fatalf("auto reply: %s", raw)
	}
	var mine struct{ Tickets []ticketDTO }
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/tickets", nil), &mine)
	if mine.Tickets[0].Messages != 1 {
		t.Fatalf("auto reply counted: %+v", mine.Tickets[0])
	}
}
