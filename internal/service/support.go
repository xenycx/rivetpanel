package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Support ticket limits.
const (
	ticketMaxActive   = 10    // open + pending tickets per account
	ticketMaxMessages = 500   // rows per ticket thread
	ticketBodyMax     = 10000 // characters per message
	ticketSubjectMax  = 150
	ticketExcerpt     = 300 // characters of a message carried by a notification
)

// TicketStore persists tickets.
type TicketStore interface {
	CreateTicket(ctx context.Context, t domain.Ticket, first domain.TicketMessage) (domain.Ticket, error)
	GetTicket(ctx context.Context, id string) (domain.Ticket, error)
	ListTickets(ctx context.Context, f domain.TicketFilter, withInternal bool) ([]domain.Ticket, error)
	CountActiveTickets(ctx context.Context, userID string) (int, error)
	TicketMessages(ctx context.Context, ticketID string, withInternal bool) ([]domain.TicketMessage, error)
	CountTicketMessages(ctx context.Context, ticketID string) (int, error)
	UpdateTicket(ctx context.Context, id string, ch domain.TicketChange, msgs []domain.TicketMessage, nowMS int64) error
	GetUserByID(ctx context.Context, id string) (domain.User, error)
}

// TicketService runs support tickets. Who sees what:
//
//   - The requester (the account that opened a ticket) sees its own tickets
//     only, never internal staff notes, and needs tickets.create. Workspace
//     members do not see each other's tickets, even about a shared bot.
//   - Staff hold tickets.view_all (read every ticket, internal notes
//     included) and/or tickets.manage (read and answer, internal notes,
//     status, priority, assignment). A delegated staff account (not a
//     built-in administrator) cannot see tickets of accounts it could not
//     manage (an administrator, or an account holding administration
//     permissions the staff account lacks): the same rule as account
//     management (manageable).
//   - An API client reaches tickets only with tickets.* in its permission
//     list and never when limited to some bots or workspaces.
type TicketService struct {
	Store   TicketStore
	Bots    *BotService
	Notices *NotificationService // nil: no notifications or email
	Now     func() time.Time
}

func (s *TicketService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// TicketInput opens a ticket.
type TicketInput struct {
	Subject  string
	Category string
	Priority string
	BotID    string
	Body     string
}

// TicketDetail is a ticket as one reader sees it.
type TicketDetail struct {
	Ticket   domain.Ticket
	Messages []domain.TicketMessage
	// Staff is true when the reader sees the ticket as staff (internal notes
	// included); Manage when it may answer as staff and change it.
	Staff  bool
	Manage bool
	// Requester is true when the reader opened the ticket.
	Requester bool
}

func scopedRefusal(actor domain.User) error {
	if actor.Client != nil && actor.Client.Scoped() {
		return &domain.ScopeError{What: "part of the API"}
	}
	return nil
}

func isStaff(actor domain.User) bool {
	return actor.Can(domain.PermTicketsViewAll) || actor.Can(domain.PermTicketsManage)
}

// staffOver reports whether actor may handle requester's tickets as staff.
func (s *TicketService) staffOver(ctx context.Context, actor domain.User, requesterID string, perm string) bool {
	if !actor.Can(perm) {
		return false
	}
	if actor.IsAdmin() {
		return true
	}
	req, err := s.Store.GetUserByID(ctx, requesterID)
	if err != nil {
		return false
	}
	return manageable(actor, req) == nil
}

// access loads a ticket and decides how actor sees it. A ticket the actor
// cannot see answers not found (its existence is not revealed).
func (s *TicketService) access(ctx context.Context, actor domain.User, id string) (domain.Ticket, TicketDetail, error) {
	if err := scopedRefusal(actor); err != nil {
		return domain.Ticket{}, TicketDetail{}, err
	}
	if u, err := uuid.Parse(id); err != nil || u.String() != id {
		return domain.Ticket{}, TicketDetail{}, domain.ErrNotFound
	}
	t, err := s.Store.GetTicket(ctx, id)
	if err != nil {
		return t, TicketDetail{}, err
	}
	d := TicketDetail{Ticket: t}
	d.Requester = t.UserID == actor.ID && actor.Can(domain.PermTicketsCreate)
	d.Manage = s.staffOver(ctx, actor, t.UserID, domain.PermTicketsManage)
	d.Staff = d.Manage || s.staffOver(ctx, actor, t.UserID, domain.PermTicketsViewAll)
	if !d.Requester && !d.Staff {
		return t, d, domain.ErrNotFound
	}
	return t, d, nil
}

func label(u domain.User) string {
	if u.DisplayName != "" {
		return clip(u.DisplayName, 100) + " <" + u.Email + ">"
	}
	return u.Email
}

func validBody(body string) (string, error) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return "", domain.Invalid("write a message")
	}
	if n := len([]rune(body)); n > ticketBodyMax {
		return "", domain.Invalid(fmt.Sprintf("the message is too long (%d characters; at most %d)", n, ticketBodyMax))
	}
	return body, nil
}

// Create opens a ticket for the actor.
func (s *TicketService) Create(ctx context.Context, actor domain.User, in TicketInput) (domain.Ticket, error) {
	if err := scopedRefusal(actor); err != nil {
		return domain.Ticket{}, err
	}
	if !actor.Can(domain.PermTicketsCreate) {
		return domain.Ticket{}, domain.Denied(actor, domain.PermTicketsCreate)
	}
	subject := strings.Join(strings.Fields(in.Subject), " ")
	if subject == "" || len([]rune(subject)) > ticketSubjectMax {
		return domain.Ticket{}, domain.Invalid(fmt.Sprintf("the subject must be 1 to %d characters", ticketSubjectMax))
	}
	if in.Category == "" {
		in.Category = "general"
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if !slices.Contains(domain.TicketCategories, in.Category) {
		return domain.Ticket{}, domain.Invalid("choose a category: " + strings.Join(domain.TicketCategories, ", "))
	}
	if !slices.Contains(domain.TicketPriorities, in.Priority) {
		return domain.Ticket{}, domain.Invalid("choose a priority: " + strings.Join(domain.TicketPriorities, ", "))
	}
	body, err := validBody(in.Body)
	if err != nil {
		return domain.Ticket{}, err
	}
	now := s.now().UnixMilli()
	t := domain.Ticket{ID: uuid.NewString(), UserID: actor.ID, UserEmail: actor.Email, Subject: subject, Category: in.Category,
		Priority: in.Priority, Status: domain.TicketOpen, CreatedAtMS: now, UpdatedAtMS: now}
	if in.BotID != "" {
		// Only a bot or server the requester can reach right now.
		b, err := s.Bots.Get(ctx, actor, in.BotID)
		if err != nil {
			var se *domain.ScopeError
			if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) || errors.As(err, &se) {
				return domain.Ticket{}, domain.Invalid("choose a bot or server you can access")
			}
			return domain.Ticket{}, err
		}
		t.BotID, t.BotName = &b.ID, clip(b.Name, 190)
	}
	n, err := s.Store.CountActiveTickets(ctx, actor.ID)
	if err != nil {
		return domain.Ticket{}, err
	}
	if n >= ticketMaxActive {
		return domain.Ticket{}, domain.Invalid(fmt.Sprintf("you already have %d open tickets; wait for an answer or close one first", n))
	}
	first := domain.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorID: &actor.ID, AuthorLabel: label(actor),
		Kind: "message", Body: body, CreatedAtMS: now}
	t, err = s.Store.CreateTicket(ctx, t, first)
	if err != nil {
		return t, err
	}
	s.notifyStaff(ctx, actor, t, fmt.Sprintf("New ticket #%d: %s", t.Number, t.Subject), clip(body, ticketExcerpt))
	return t, nil
}

// ListScope chooses which tickets List returns.
const (
	TicketsMine = "mine" // tickets the actor opened
	TicketsAll  = "all"  // every ticket the actor may see as staff
)

// List returns tickets. status is "", "active" or one status; assignedToMe
// narrows the staff view to the actor's assignments.
func (s *TicketService) List(ctx context.Context, actor domain.User, scope, status string, assignedToMe bool) ([]domain.Ticket, error) {
	if err := scopedRefusal(actor); err != nil {
		return nil, err
	}
	if status != "" && status != "active" && !slices.Contains(domain.TicketStatuses, status) {
		return nil, domain.Invalid("unknown status")
	}
	f := domain.TicketFilter{Status: status}
	switch scope {
	case TicketsAll:
		if !isStaff(actor) {
			return nil, domain.ErrForbidden
		}
		if assignedToMe {
			f.AssigneeID = actor.ID
		}
		ts, err := s.Store.ListTickets(ctx, f, true)
		if err != nil {
			return nil, err
		}
		if actor.IsAdmin() {
			return ts, nil
		}
		// A delegated staff account sees only requesters it outranks.
		out := []domain.Ticket{}
		ok := map[string]bool{}
		for _, t := range ts {
			v, seen := ok[t.UserID]
			if !seen {
				v = s.staffOver(ctx, actor, t.UserID, domain.PermTicketsViewAll) || s.staffOver(ctx, actor, t.UserID, domain.PermTicketsManage)
				ok[t.UserID] = v
			}
			if v {
				out = append(out, t)
			}
		}
		return out, nil
	default:
		if !actor.Can(domain.PermTicketsCreate) {
			return nil, domain.Denied(actor, domain.PermTicketsCreate)
		}
		f.UserID = actor.ID
		return s.Store.ListTickets(ctx, f, false)
	}
}

// Get returns a ticket with the thread the actor may read.
func (s *TicketService) Get(ctx context.Context, actor domain.User, id string) (TicketDetail, error) {
	t, d, err := s.access(ctx, actor, id)
	if err != nil {
		return d, err
	}
	d.Messages, err = s.Store.TicketMessages(ctx, t.ID, d.Staff)
	return d, err
}

// Reply adds a message (internal: a staff note the requester never sees).
// A requester's reply reopens a pending or resolved ticket; a staff reply
// marks an open ticket pending (waiting for the requester).
func (s *TicketService) Reply(ctx context.Context, actor domain.User, id, body string, internal bool) (domain.TicketMessage, error) {
	t, d, err := s.access(ctx, actor, id)
	if err != nil {
		return domain.TicketMessage{}, err
	}
	body, err = validBody(body)
	if err != nil {
		return domain.TicketMessage{}, err
	}
	// The requester answers as the requester, even when it is also staff.
	asStaff := d.Manage && !(t.UserID == actor.ID && !internal)
	if !asStaff && !d.Requester {
		return domain.TicketMessage{}, domain.Denied(actor, domain.PermTicketsManage)
	}
	if internal && !d.Manage {
		return domain.TicketMessage{}, domain.Denied(actor, domain.PermTicketsManage)
	}
	if !asStaff && t.Status == domain.TicketClosed {
		return domain.TicketMessage{}, domain.Invalid("this ticket is closed; open a new ticket")
	}
	n, err := s.Store.CountTicketMessages(ctx, t.ID)
	if err != nil {
		return domain.TicketMessage{}, err
	}
	if n >= ticketMaxMessages {
		return domain.TicketMessage{}, domain.Invalid("this ticket has too many messages; open a new ticket")
	}
	now := s.now().UnixMilli()
	m := domain.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorID: &actor.ID, AuthorLabel: label(actor),
		Kind: "message", Staff: asStaff, Internal: internal, Body: body, CreatedAtMS: now}
	var ch domain.TicketChange
	switch {
	case internal:
	case asStaff && t.Status == domain.TicketOpen:
		ch.Status = ptr(domain.TicketPending)
	case !asStaff && (t.Status == domain.TicketPending || t.Status == domain.TicketResolved):
		ch.Status = ptr(domain.TicketOpen)
	}
	if err := s.Store.UpdateTicket(ctx, t.ID, ch, []domain.TicketMessage{m}, now); err != nil {
		return m, err
	}
	title := fmt.Sprintf("#%d %s", t.Number, t.Subject)
	switch {
	case internal:
		if t.AssigneeID != nil {
			s.notify(ctx, actor, []string{*t.AssigneeID}, "Internal note on "+title, clip(body, ticketExcerpt), t)
		}
	case asStaff:
		s.notify(ctx, actor, []string{t.UserID}, "New reply on your ticket "+title, clip(body, ticketExcerpt), t)
	default:
		if t.AssigneeID != nil {
			s.notify(ctx, actor, []string{*t.AssigneeID}, "Reply from the requester on "+title, clip(body, ticketExcerpt), t)
		} else {
			s.notifyStaff(ctx, actor, t, "Reply from the requester on "+title, clip(body, ticketExcerpt))
		}
	}
	return m, nil
}

func ptr[T any](v T) *T { return &v }

// TicketUpdate changes a ticket's state. Nil fields stay as they are;
// Assignee "" unassigns.
type TicketUpdate struct {
	Status   *string
	Priority *string
	Assignee *string
}

// Update changes status, priority or assignee. Staff with tickets.manage may
// change everything; the requester may only close its ticket or reopen a
// resolved one. Every change is recorded in the thread (assignment as an
// internal event).
func (s *TicketService) Update(ctx context.Context, actor domain.User, id string, in TicketUpdate) (domain.Ticket, error) {
	t, d, err := s.access(ctx, actor, id)
	if err != nil {
		return t, err
	}
	now := s.now().UnixMilli()
	var ch domain.TicketChange
	var msgs []domain.TicketMessage
	event := func(body string, internal bool) {
		msgs = append(msgs, domain.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorID: &actor.ID, AuthorLabel: label(actor),
			Kind: "event", Staff: d.Manage, Internal: internal, Body: body, CreatedAtMS: now})
	}
	if in.Status != nil && *in.Status != t.Status {
		st := *in.Status
		if !slices.Contains(domain.TicketStatuses, st) {
			return t, domain.Invalid("unknown status")
		}
		requesterMove := d.Requester && (st == domain.TicketClosed || (st == domain.TicketOpen && t.Status == domain.TicketResolved))
		if !d.Manage && !requesterMove {
			return t, domain.Denied(actor, domain.PermTicketsManage)
		}
		ch.Status = &st
		event("Status changed from "+t.Status+" to "+st, false)
	}
	if in.Priority != nil && *in.Priority != t.Priority {
		if !d.Manage {
			return t, domain.Denied(actor, domain.PermTicketsManage)
		}
		if !slices.Contains(domain.TicketPriorities, *in.Priority) {
			return t, domain.Invalid("unknown priority")
		}
		ch.Priority = in.Priority
		event("Priority changed from "+t.Priority+" to "+*in.Priority, false)
	}
	var newAssignee *domain.User
	if in.Assignee != nil {
		if !d.Manage {
			return t, domain.Denied(actor, domain.PermTicketsManage)
		}
		cur := ""
		if t.AssigneeID != nil {
			cur = *t.AssigneeID
		}
		if *in.Assignee != cur {
			if *in.Assignee == "" {
				var none *string
				ch.AssigneeID = &none
				event("Unassigned", true)
			} else {
				a, err := s.Store.GetUserByID(ctx, *in.Assignee)
				if err != nil || a.Disabled || !s.staffOver(ctx, a, t.UserID, domain.PermTicketsManage) {
					return t, domain.Invalid("choose a staff account that can answer this ticket")
				}
				aid := &a.ID
				ch.AssigneeID = &aid
				newAssignee = &a
				event("Assigned to "+label(a), true)
			}
		}
	}
	if len(msgs) == 0 {
		return t, nil
	}
	if err := s.Store.UpdateTicket(ctx, t.ID, ch, msgs, now); err != nil {
		return t, err
	}
	title := fmt.Sprintf("#%d %s", t.Number, t.Subject)
	if ch.Status != nil && actor.ID != t.UserID {
		s.notify(ctx, actor, []string{t.UserID}, "Your ticket "+title+" is now "+*ch.Status, "", t)
	}
	if newAssignee != nil {
		s.notify(ctx, actor, []string{newAssignee.ID}, "Ticket "+title+" was assigned to you", "", t)
	}
	return s.Store.GetTicket(ctx, t.ID)
}

// Staff lists the accounts a ticket can be assigned to (holders of
// tickets.manage), for staff who manage tickets.
func (s *TicketService) Staff(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if err := scopedRefusal(actor); err != nil {
		return nil, err
	}
	if !actor.Can(domain.PermTicketsManage) {
		return nil, domain.ErrForbidden
	}
	if s.Notices == nil {
		return nil, nil
	}
	return s.Notices.HoldersOf(ctx, domain.PermTicketsManage), nil
}

func (s *TicketService) notify(ctx context.Context, actor domain.User, to []string, title, body string, t domain.Ticket) {
	if s.Notices == nil {
		return
	}
	s.Notices.NotifyMany(ctx, to, actor.ID, Notice{Category: domain.NotifyTickets, Title: title, Body: body, Link: "/support/" + t.ID})
}

// notifyStaff tells every account that can answer the ticket.
func (s *TicketService) notifyStaff(ctx context.Context, actor domain.User, t domain.Ticket, title, body string) {
	if s.Notices == nil {
		return
	}
	var to []string
	for _, u := range s.Notices.HoldersOf(ctx, domain.PermTicketsManage) {
		if s.staffOver(ctx, u, t.UserID, domain.PermTicketsManage) {
			to = append(to, u.ID)
		}
	}
	s.notify(ctx, actor, to, title, body, t)
}
