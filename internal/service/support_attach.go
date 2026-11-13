package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Attachments and the support desk settings (automatic reply, usual
// response time). Enforced here, by the panel application:
//
//   - Anyone who may use tickets uploads files; an upload stays private to
//     its uploader until it is sent with a message the uploader writes.
//   - A sent attachment is readable by whoever reads its message: internal
//     notes' files only by staff. A removed message's files are deleted.
//   - The stored type comes from the bytes (never the file name or the
//     browser), and only the types below are accepted.

// SetSupportDesk keeps SupportSettings as JSON.
const SetSupportDesk = "support_desk"

const (
	autoReplyMax     = 4000
	responseHoursMax = 24 * 60 // 60 days
	autoReplyLabel   = "Automatic reply"
)

// DefaultAutoReply is the automatic reply offered before anyone edits it.
const DefaultAutoReply = "Hi {name}, thanks for getting in touch. We received your ticket **{ticket}** and someone from the support team will answer {response_time}.\n\n" +
	"While you wait, add anything that helps us reproduce the problem: what you did, what you expected, and screenshots or logs."

// attachmentTypes are the accepted sniffed types and the type each is stored as.
var attachmentTypes = map[string]string{
	"image/png":                    "image/png",
	"image/jpeg":                   "image/jpeg",
	"image/gif":                    "image/gif",
	"image/webp":                   "image/webp",
	"application/pdf":              "application/pdf",
	"text/plain; charset=utf-8":    "text/plain",
	"text/plain; charset=utf-16be": "text/plain",
	"text/plain; charset=utf-16le": "text/plain",
	"application/zip":              "application/zip",
	"application/x-gzip":           "application/gzip",
	"video/mp4":                    "video/mp4",
	"video/webm":                   "video/webm",
}

// SupportSettings returns the desk settings (defaults when unset).
func (s *TicketService) SupportSettings(ctx context.Context) domain.SupportSettings {
	out := domain.SupportSettings{AutoReplyBody: DefaultAutoReply}
	set, err := s.Store.Settings(ctx)
	if err != nil {
		return out
	}
	if v := set[SetSupportDesk].Value; v != "" {
		var j struct {
			AutoReply     bool   `json:"auto_reply"`
			AutoReplyBody string `json:"auto_reply_body"`
			ResponseHours int    `json:"response_hours"`
		}
		if json.Unmarshal([]byte(v), &j) == nil {
			out.AutoReply, out.ResponseHours = j.AutoReply, j.ResponseHours
			if strings.TrimSpace(j.AutoReplyBody) != "" {
				out.AutoReplyBody = j.AutoReplyBody
			}
		}
	}
	return out
}

// PutSupportSettings changes the desk settings (staff who manage tickets).
func (s *TicketService) PutSupportSettings(ctx context.Context, actor domain.User, in domain.SupportSettings) (domain.SupportSettings, error) {
	if err := scopedRefusal(actor); err != nil {
		return in, err
	}
	if !actor.Can(domain.PermTicketsManage) {
		return in, domain.Denied(actor, domain.PermTicketsManage)
	}
	in.AutoReplyBody = strings.TrimSpace(strings.ReplaceAll(in.AutoReplyBody, "\r\n", "\n"))
	if in.AutoReply && in.AutoReplyBody == "" {
		return in, domain.Invalid("write the automatic reply, or turn it off")
	}
	if n := len([]rune(in.AutoReplyBody)); n > autoReplyMax {
		return in, domain.Invalid(fmt.Sprintf("the automatic reply is too long (%d characters; at most %d)", n, autoReplyMax))
	}
	if in.ResponseHours < 0 || in.ResponseHours > responseHoursMax {
		return in, domain.Invalid(fmt.Sprintf("the usual response time must be between 0 and %d hours", responseHoursMax))
	}
	b, _ := json.Marshal(map[string]any{"auto_reply": in.AutoReply, "auto_reply_body": in.AutoReplyBody, "response_hours": in.ResponseHours})
	if err := s.Store.PutSettings(ctx, []domain.Setting{{Key: SetSupportDesk, Value: string(b)}}, s.now().UnixMilli()); err != nil {
		return in, err
	}
	return s.SupportSettings(ctx), nil
}

// ResponseTime words a usual response time for a sentence: "within 4
// hours", "within 2 days", or "as soon as possible" when none is set.
func ResponseTime(hours int) string {
	switch {
	case hours <= 0:
		return "as soon as possible"
	case hours == 1:
		return "within an hour"
	case hours < 48 && hours%24 != 0:
		return "within " + strconv.Itoa(hours) + " hours"
	case hours == 24:
		return "within a day"
	default:
		return "within " + strconv.Itoa((hours+23)/24) + " days"
	}
}

// autoReply is the automatic reply to a new ticket, or false when off.
func (s *TicketService) autoReply(ctx context.Context, requester domain.User, t domain.Ticket, nowMS int64) (domain.TicketMessage, bool) {
	set := s.SupportSettings(ctx)
	if !set.AutoReply || strings.TrimSpace(set.AutoReplyBody) == "" {
		return domain.TicketMessage{}, false
	}
	name := strings.TrimSpace(requester.DisplayName)
	if name == "" {
		name = "there"
	}
	body := strings.NewReplacer("{name}", name, "{ticket}", domain.TicketKey(t.Number), "{subject}", t.Subject,
		"{response_time}", ResponseTime(set.ResponseHours)).Replace(set.AutoReplyBody)
	if body, err := validBody(body); err == nil {
		return domain.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorLabel: autoReplyLabel, Kind: "message", Staff: true,
			Auto: true, Body: body, CreatedAtMS: nowMS + 1}, true
	}
	return domain.TicketMessage{}, false
}

// cleanFileName keeps a file's base name, printable and at most 200 characters.
func cleanFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}

// Upload stores a pending attachment of the actor.
func (s *TicketService) Upload(ctx context.Context, actor domain.User, name string, data []byte) (domain.TicketAttachment, error) {
	if err := scopedRefusal(actor); err != nil {
		return domain.TicketAttachment{}, err
	}
	if !actor.Can(domain.PermTicketsCreate) && !isStaff(actor) {
		return domain.TicketAttachment{}, domain.Denied(actor, domain.PermTicketsCreate)
	}
	if len(data) == 0 {
		return domain.TicketAttachment{}, domain.Invalid("the file is empty")
	}
	if len(data) > domain.TicketAttachmentMax {
		return domain.TicketAttachment{}, domain.Invalid(fmt.Sprintf("the file is too large (at most %d MB)", domain.TicketAttachmentMax>>20))
	}
	ct, ok := attachmentTypes[http.DetectContentType(data)]
	if !ok {
		return domain.TicketAttachment{}, domain.Invalid("this kind of file cannot be attached; send pictures, PDF, text or log files, ZIP or GZIP archives, or MP4/WebM videos")
	}
	now := s.now().UnixMilli()
	if err := s.Store.PrunePendingTicketAttachments(ctx, now-domain.TicketAttachmentPendingTTL); err != nil {
		return domain.TicketAttachment{}, err
	}
	n, err := s.Store.CountPendingTicketAttachments(ctx, actor.ID)
	if err != nil {
		return domain.TicketAttachment{}, err
	}
	if n >= domain.TicketAttachmentsPending {
		return domain.TicketAttachment{}, domain.Invalid("too many files are waiting to be sent; send or remove some first")
	}
	a := domain.TicketAttachment{ID: uuid.NewString(), UploaderID: actor.ID, Name: cleanFileName(name), ContentType: ct,
		Size: int64(len(data)), Data: data, CreatedAtMS: now}
	if err := s.Store.PutTicketAttachment(ctx, a); err != nil {
		return a, err
	}
	a.Data = nil
	return a, nil
}

// DiscardUpload deletes one of the actor's unsent uploads.
func (s *TicketService) DiscardUpload(ctx context.Context, actor domain.User, id string) error {
	if err := scopedRefusal(actor); err != nil {
		return err
	}
	if !validUUID(id) {
		return domain.ErrNotFound
	}
	return s.Store.DeletePendingTicketAttachment(ctx, id, actor.ID)
}

// Attachment returns a sent file of a ticket, with its bytes, for a reader
// of its message.
func (s *TicketService) Attachment(ctx context.Context, actor domain.User, ticketID, id string) (domain.TicketAttachment, error) {
	t, d, err := s.access(ctx, actor, ticketID)
	if err != nil {
		return domain.TicketAttachment{}, err
	}
	if !validUUID(id) {
		return domain.TicketAttachment{}, domain.ErrNotFound
	}
	a, err := s.Store.GetTicketAttachment(ctx, t.ID, id)
	if err != nil {
		return a, err
	}
	m, err := s.Store.GetTicketMessage(ctx, a.MessageID)
	if err != nil {
		return domain.TicketAttachment{}, err
	}
	if (m.Internal && !d.Staff) || m.DeletedAtMS != nil {
		return domain.TicketAttachment{}, domain.ErrNotFound
	}
	return a, nil
}

// pendingFor checks the uploads a new message carries: the actor's own,
// unsent, at most TicketAttachmentsPerMsg.
func (s *TicketService) pendingFor(ctx context.Context, actor domain.User, ids []string) ([]domain.TicketAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	uniq := []string{}
	for _, id := range ids {
		if !seen[id] && validUUID(id) {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) > domain.TicketAttachmentsPerMsg {
		return nil, domain.Invalid(fmt.Sprintf("attach at most %d files to one message", domain.TicketAttachmentsPerMsg))
	}
	as, err := s.Store.PendingTicketAttachments(ctx, actor.ID, uniq)
	if err != nil {
		return nil, err
	}
	if len(as) != len(uniq) || len(uniq) != len(ids) {
		return nil, domain.Invalid("an attachment is no longer available; attach the file again")
	}
	// Keep the order the files were attached in.
	at := make(map[string]int, len(uniq))
	for i, id := range uniq {
		at[id] = i
	}
	out := make([]domain.TicketAttachment, len(as))
	for _, a := range as {
		out[at[a.ID]] = a
	}
	return out, nil
}

// bodyWithFiles is the message body, or a line naming the files when only
// files are sent.
func bodyWithFiles(body string, files []domain.TicketAttachment) string {
	if strings.TrimSpace(body) != "" || len(files) == 0 {
		return body
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return "Attached " + strings.Join(names, ", ")
}
