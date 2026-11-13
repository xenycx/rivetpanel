package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Collaboration on tickets: editing and removing messages, reactions,
// mentions, labels, saved replies and watchers. Every rule here is enforced
// by the application on top of access(): requesters never reach internal
// notes, labels, watchers, saved replies or staff edit history.

const (
	ticketMaxMentions     = 10
	ticketMaxLabels       = 200
	ticketLabelsPerTicket = 10
	ticketMaxSavedReplies = 300
	savedReplyTitleMax    = 80
	labelNameMax          = 32
	removedBody           = "[removed]"
)

// without returns ids minus drop.
func without(ids, drop []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(drop, id) {
			out = append(out, id)
		}
	}
	return out
}

// followers are the staff who hear about a ticket's activity: its assignee
// and its watchers (still allowed to see it).
func (s *TicketService) followers(ctx context.Context, t domain.Ticket) []string {
	var out []string
	if t.AssigneeID != nil {
		out = append(out, *t.AssigneeID)
	}
	ws, err := s.Store.TicketWatchers(ctx, t.ID)
	if err != nil {
		return out
	}
	for _, w := range ws {
		if slices.Contains(out, w.ID) {
			continue
		}
		u, err := s.Store.GetUserByID(ctx, w.ID)
		if err != nil || u.Disabled {
			continue
		}
		if s.staffOver(ctx, u, t.UserID, domain.PermTicketsViewAll) || s.staffOver(ctx, u, t.UserID, domain.PermTicketsManage) {
			out = append(out, w.ID)
		}
	}
	return out
}

// mentioned validates the accounts an internal note mentions: staff who may
// read this ticket. Mentions on a reply the requester sees are refused.
func (s *TicketService) mentioned(ctx context.Context, t domain.Ticket, internal bool, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if !internal {
		return nil, domain.Invalid("mentions are for internal notes only")
	}
	if len(ids) > ticketMaxMentions {
		return nil, domain.Invalid(fmt.Sprintf("mention at most %d people", ticketMaxMentions))
	}
	var out []string
	for _, id := range ids {
		if slices.Contains(out, id) {
			continue
		}
		u, err := s.Store.GetUserByID(ctx, id)
		if err != nil || u.Disabled || !(s.staffOver(ctx, u, t.UserID, domain.PermTicketsViewAll) || s.staffOver(ctx, u, t.UserID, domain.PermTicketsManage)) {
			return nil, domain.Invalid("you can mention only support staff who can read this ticket")
		}
		out = append(out, id)
	}
	return out, nil
}

// message loads a thread row the actor may read on a ticket it may read.
func (s *TicketService) message(ctx context.Context, actor domain.User, ticketID, msgID string) (domain.Ticket, TicketDetail, domain.TicketMessage, error) {
	t, d, err := s.access(ctx, actor, ticketID)
	if err != nil {
		return t, d, domain.TicketMessage{}, err
	}
	if !validUUID(msgID) {
		return t, d, domain.TicketMessage{}, domain.ErrNotFound
	}
	m, err := s.Store.GetTicketMessage(ctx, msgID)
	if err != nil || m.TicketID != t.ID || (m.Internal && !d.Staff) {
		return t, d, m, domain.ErrNotFound
	}
	return t, d, m, nil
}

func isAuthor(m domain.TicketMessage, actor domain.User) bool {
	return m.AuthorID != nil && *m.AuthorID == actor.ID
}

// EditMessage replaces the body of the actor's own reply or note. The
// earlier body is kept as a revision. A requester cannot edit once the
// ticket is closed; a staff message needs tickets.manage to edit.
func (s *TicketService) EditMessage(ctx context.Context, actor domain.User, ticketID, msgID, body string) error {
	t, d, m, err := s.message(ctx, actor, ticketID, msgID)
	if err != nil {
		return err
	}
	if m.Kind != "message" || m.DeletedAtMS != nil {
		return domain.Invalid("this message cannot be edited")
	}
	if !isAuthor(m, actor) {
		return domain.Invalid("you can edit only your own messages")
	}
	if m.Staff && !d.Manage {
		return domain.Denied(actor, domain.PermTicketsManage)
	}
	if !m.Staff && t.Status == domain.TicketClosed {
		return domain.Invalid("this ticket is closed, so its messages cannot be edited; open a new ticket if you need more help")
	}
	body, err = validBody(body)
	if err != nil {
		return err
	}
	if body == m.Body {
		return nil
	}
	return s.Store.EditTicketMessage(ctx, m.ID, body, domain.TicketRevision{ID: uuid.NewString()}, actor.ID, s.now().UnixMilli())
}

// RemoveMessage blanks a reply or note: its author may remove it (the same
// rules as editing), and staff who manage tickets may remove any message.
// The first message of a ticket stays (close the ticket instead).
func (s *TicketService) RemoveMessage(ctx context.Context, actor domain.User, ticketID, msgID string) error {
	t, d, m, err := s.message(ctx, actor, ticketID, msgID)
	if err != nil {
		return err
	}
	if m.Kind != "message" || m.DeletedAtMS != nil {
		return domain.Invalid("this message cannot be removed")
	}
	if first, err := s.Store.FirstTicketMessageID(ctx, t.ID); err == nil && first == m.ID {
		return domain.Invalid("the first message of a ticket cannot be removed; close the ticket instead")
	}
	switch {
	case d.Manage:
	case isAuthor(m, actor) && !m.Staff:
		if t.Status == domain.TicketClosed {
			return domain.Invalid("this ticket is closed, so its messages cannot be removed")
		}
	default:
		if isAuthor(m, actor) {
			return domain.Denied(actor, domain.PermTicketsManage)
		}
		return domain.Invalid("you can remove only your own messages")
	}
	return s.Store.RemoveTicketMessage(ctx, m.ID, actor.ID, s.now().UnixMilli())
}

// MessageRevisions returns the earlier bodies of a message to its author
// and to staff.
func (s *TicketService) MessageRevisions(ctx context.Context, actor domain.User, ticketID, msgID string) ([]domain.TicketRevision, error) {
	_, d, m, err := s.message(ctx, actor, ticketID, msgID)
	if err != nil {
		return nil, err
	}
	if !d.Staff && !isAuthor(m, actor) {
		return nil, domain.ErrNotFound
	}
	return s.Store.TicketMessageRevisions(ctx, m.ID)
}

// React adds or removes the actor's reaction to a message it can read.
func (s *TicketService) React(ctx context.Context, actor domain.User, ticketID, msgID, kind string, on bool) error {
	_, _, m, err := s.message(ctx, actor, ticketID, msgID)
	if err != nil {
		return err
	}
	if !slices.Contains(domain.TicketReactionKinds, kind) {
		return domain.Invalid("unknown reaction")
	}
	if m.Kind != "message" || m.DeletedAtMS != nil {
		return domain.Invalid("you cannot react to this message")
	}
	return s.Store.SetTicketReaction(ctx, m.ID, actor.ID, kind, on, s.now().UnixMilli())
}

// ---- Watchers ----

// Watch starts (on) or stops following a ticket. Staff only.
func (s *TicketService) Watch(ctx context.Context, actor domain.User, ticketID string, on bool) error {
	t, d, err := s.access(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if !d.Staff {
		return domain.ErrNotFound
	}
	return s.Store.SetTicketWatcher(ctx, t.ID, actor.ID, on, s.now().UnixMilli())
}

// Watched returns the ids of tickets the actor watches.
func (s *TicketService) Watched(ctx context.Context, actor domain.User) (map[string]bool, error) {
	if !isStaff(actor) {
		return map[string]bool{}, nil
	}
	return s.Store.WatchedTicketIDs(ctx, actor.ID)
}

// ---- Labels ----

func (s *TicketService) staffOnly(actor domain.User, perm string) error {
	if err := scopedRefusal(actor); err != nil {
		return err
	}
	if perm != "" {
		if !actor.Can(perm) {
			return domain.Denied(actor, perm)
		}
		return nil
	}
	if !isStaff(actor) {
		return domain.ErrForbidden
	}
	return nil
}

// Labels lists every label (staff).
func (s *TicketService) Labels(ctx context.Context, actor domain.User) ([]domain.TicketLabel, error) {
	if err := s.staffOnly(actor, ""); err != nil {
		return nil, err
	}
	return s.Store.ListTicketLabels(ctx)
}

func cleanTicketLabel(name, color string) (string, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len([]rune(name)) > labelNameMax {
		return "", "", domain.Invalid(fmt.Sprintf("a label name must be 1 to %d characters", labelNameMax))
	}
	if color == "" {
		color = "slate"
	}
	if !slices.Contains(domain.TicketLabelColors, color) {
		return "", "", domain.Invalid("choose a color: " + strings.Join(domain.TicketLabelColors, ", "))
	}
	return name, color, nil
}

// CreateLabel adds a label (tickets.manage).
func (s *TicketService) CreateLabel(ctx context.Context, actor domain.User, name, color string) (domain.TicketLabel, error) {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return domain.TicketLabel{}, err
	}
	name, color, err := cleanTicketLabel(name, color)
	if err != nil {
		return domain.TicketLabel{}, err
	}
	ls, err := s.Store.ListTicketLabels(ctx)
	if err != nil {
		return domain.TicketLabel{}, err
	}
	if len(ls) >= ticketMaxLabels {
		return domain.TicketLabel{}, domain.Invalid(fmt.Sprintf("there are already %d labels; remove one first", len(ls)))
	}
	l := domain.TicketLabel{ID: uuid.NewString(), Name: name, Color: color, CreatedAtMS: s.now().UnixMilli()}
	if err := s.Store.CreateTicketLabel(ctx, l); err != nil {
		if err == domain.ErrConflict {
			return l, domain.Invalid("a label with this name already exists")
		}
		return l, err
	}
	return l, nil
}

// UpdateLabel renames or recolors a label (tickets.manage).
func (s *TicketService) UpdateLabel(ctx context.Context, actor domain.User, id, name, color string) (domain.TicketLabel, error) {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return domain.TicketLabel{}, err
	}
	name, color, err := cleanTicketLabel(name, color)
	if err != nil {
		return domain.TicketLabel{}, err
	}
	l := domain.TicketLabel{ID: id, Name: name, Color: color}
	if err := s.Store.UpdateTicketLabel(ctx, l); err != nil {
		if err == domain.ErrConflict {
			return l, domain.Invalid("a label with this name already exists")
		}
		return l, err
	}
	return l, nil
}

// DeleteLabel removes a label from every ticket (tickets.manage).
func (s *TicketService) DeleteLabel(ctx context.Context, actor domain.User, id string) error {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return err
	}
	return s.Store.DeleteTicketLabel(ctx, id)
}

// SetLabels replaces a ticket's labels (staff who manage it). Labels are
// staff-only and not recorded in the requester-visible thread.
func (s *TicketService) SetLabels(ctx context.Context, actor domain.User, ticketID string, ids []string) (domain.Ticket, error) {
	t, d, err := s.access(ctx, actor, ticketID)
	if err != nil {
		return t, err
	}
	if !d.Manage {
		return t, domain.Denied(actor, domain.PermTicketsManage)
	}
	if len(ids) > ticketLabelsPerTicket {
		return t, domain.Invalid(fmt.Sprintf("a ticket can carry at most %d labels", ticketLabelsPerTicket))
	}
	if err := s.Store.SetTicketLabels(ctx, t.ID, ids); err != nil {
		if err == domain.ErrNotFound {
			return t, domain.Invalid("choose existing labels")
		}
		return t, err
	}
	return s.Store.GetTicket(ctx, t.ID)
}

// ---- Saved replies ----

// SavedReplies lists the team's saved replies (tickets.manage).
func (s *TicketService) SavedReplies(ctx context.Context, actor domain.User) ([]domain.SavedReply, error) {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return nil, err
	}
	return s.Store.ListSavedReplies(ctx)
}

// PutSavedReply creates (id "") or changes a saved reply (tickets.manage).
func (s *TicketService) PutSavedReply(ctx context.Context, actor domain.User, id, title, body string) (domain.SavedReply, error) {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return domain.SavedReply{}, err
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len([]rune(title)) > savedReplyTitleMax {
		return domain.SavedReply{}, domain.Invalid(fmt.Sprintf("the title must be 1 to %d characters", savedReplyTitleMax))
	}
	body, err := validBody(body)
	if err != nil {
		return domain.SavedReply{}, err
	}
	r := domain.SavedReply{ID: id, Title: title, Body: body, CreatedBy: actor.ID, UpdatedAtMS: s.now().UnixMilli()}
	if id == "" {
		n, err := s.Store.CountSavedReplies(ctx)
		if err != nil {
			return r, err
		}
		if n >= ticketMaxSavedReplies {
			return r, domain.Invalid(fmt.Sprintf("there are already %d saved replies; remove one first", n))
		}
		r.ID = uuid.NewString()
	} else {
		cur, err := s.Store.GetSavedReply(ctx, id)
		if err != nil {
			return r, err
		}
		r.CreatedBy = cur.CreatedBy
	}
	return r, s.Store.PutSavedReply(ctx, r)
}

// DeleteSavedReply removes a saved reply (tickets.manage).
func (s *TicketService) DeleteSavedReply(ctx context.Context, actor domain.User, id string) error {
	if err := s.staffOnly(actor, domain.PermTicketsManage); err != nil {
		return err
	}
	return s.Store.DeleteSavedReply(ctx, id)
}
