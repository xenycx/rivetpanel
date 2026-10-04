package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Support tickets. The service decides who sees what (see
// service.TicketService); these handlers shape the answer for the reader:
// requesters never receive internal notes, the assignee or staff email
// addresses.

type ticketDTO struct {
	ID          string  `json:"id"`
	Number      int64   `json:"number"`
	Subject     string  `json:"subject"`
	Category    string  `json:"category"`
	Priority    string  `json:"priority"`
	Status      string  `json:"status"`
	BotID       *string `json:"bot_id"`
	BotName     string  `json:"bot_name"`
	CreatedAtMS int64   `json:"created_at_ms"`
	UpdatedAtMS int64   `json:"updated_at_ms"`
	ClosedAtMS  *int64  `json:"closed_at_ms"`
	Messages    int     `json:"messages"`
	// Staff view only.
	RequesterID    string  `json:"requester_id,omitempty"`
	RequesterEmail string  `json:"requester_email,omitempty"`
	AssigneeID     *string `json:"assignee_id,omitempty"`
	AssigneeEmail  string  `json:"assignee_email,omitempty"`
}

func toTicket(t domain.Ticket, staff bool) ticketDTO {
	d := ticketDTO{ID: t.ID, Number: t.Number, Subject: t.Subject, Category: t.Category, Priority: t.Priority, Status: t.Status,
		BotID: t.BotID, BotName: t.BotName, CreatedAtMS: t.CreatedAtMS, UpdatedAtMS: t.UpdatedAtMS, ClosedAtMS: t.ClosedAtMS, Messages: t.Messages}
	if staff {
		d.RequesterID, d.RequesterEmail, d.AssigneeID, d.AssigneeEmail = t.UserID, t.UserEmail, t.AssigneeID, t.AssigneeEmail
	}
	return d
}

type ticketMessageDTO struct {
	ID          string `json:"id"`
	Author      string `json:"author"`
	Mine        bool   `json:"mine"`
	Kind        string `json:"kind"`
	Staff       bool   `json:"staff"`
	Internal    bool   `json:"internal"`
	Body        string `json:"body"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

func toTicketMessage(m domain.TicketMessage, me string, staffView bool) ticketMessageDTO {
	author := m.AuthorLabel
	if m.Staff && !staffView {
		author = "Support team" // requesters do not see staff addresses
	}
	return ticketMessageDTO{ID: m.ID, Author: author, Mine: m.AuthorID != nil && *m.AuthorID == me, Kind: m.Kind,
		Staff: m.Staff, Internal: m.Internal, Body: m.Body, CreatedAtMS: m.CreatedAtMS}
}

func perUserLimit(name string, max int, per time.Duration, msg string) fiber.Handler {
	return newLimiter(limiter.Config{
		Max: max, Expiration: per,
		KeyGenerator: func(c fiber.Ctx) string { return name + ":" + currentUser(c).ID },
		LimitReached: func(c fiber.Ctx) error { return fiber.NewError(fiber.StatusTooManyRequests, msg) },
	})
}

func (s *panel) ticketRoutes(authed fiber.Router) {
	anyTicket := s.requireAnyPerm(domain.PermTicketsCreate, domain.PermTicketsViewAll, domain.PermTicketsManage)
	authed.Get("/tickets", anyTicket, s.listTickets)
	authed.Post("/tickets", s.requirePerm(domain.PermTicketsCreate),
		perUserLimit("ticket-create", 10, time.Hour, "too many new tickets; try again later"), s.createTicket)
	authed.Get("/tickets/staff", s.requirePerm(domain.PermTicketsManage), s.ticketStaff)
	authed.Get("/tickets/:tid", anyTicket, s.getTicket)
	authed.Post("/tickets/:tid/messages", anyTicket,
		perUserLimit("ticket-reply", 60, 10*time.Minute, "too many replies; try again in a few minutes"), s.replyTicket)
	authed.Patch("/tickets/:tid", anyTicket, s.updateTicket)
}

func (s *panel) listTickets(c fiber.Ctx) error {
	scope := c.Query("scope", service.TicketsMine)
	ts, err := s.tickets.List(c.Context(), currentUser(c), scope, c.Query("status"), c.Query("assigned") == "me")
	if err != nil {
		return err
	}
	staff := scope == service.TicketsAll
	out := make([]ticketDTO, len(ts))
	for i, t := range ts {
		out[i] = toTicket(t, staff)
	}
	return c.JSON(fiber.Map{"tickets": out})
}

func (s *panel) createTicket(c fiber.Ctx) error {
	var in struct {
		Subject  string `json:"subject"`
		Category string `json:"category"`
		Priority string `json:"priority"`
		BotID    string `json:"bot_id"`
		Body     string `json:"body"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	t, err := s.tickets.Create(c.Context(), currentUser(c), service.TicketInput{Subject: in.Subject, Category: in.Category,
		Priority: in.Priority, BotID: in.BotID, Body: in.Body})
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, fmt.Sprintf("#%d", t.Number))
	return c.Status(fiber.StatusCreated).JSON(toTicket(t, false))
}

func (s *panel) ticketDetail(c fiber.Ctx, d service.TicketDetail) error {
	u := currentUser(c)
	msgs := make([]ticketMessageDTO, len(d.Messages))
	for i, m := range d.Messages {
		msgs[i] = toTicketMessage(m, u.ID, d.Staff)
	}
	t := toTicket(d.Ticket, d.Staff)
	return c.JSON(fiber.Map{"ticket": t, "messages": msgs, "staff": d.Staff, "manage": d.Manage, "requester": d.Requester})
}

func (s *panel) getTicket(c fiber.Ctx) error {
	d, err := s.tickets.Get(c.Context(), currentUser(c), strings.Clone(c.Params("tid")))
	if err != nil {
		return err
	}
	return s.ticketDetail(c, d)
}

func (s *panel) replyTicket(c fiber.Ctx) error {
	var in struct {
		Body     string `json:"body"`
		Internal bool   `json:"internal"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("tid"))
	u := currentUser(c)
	if _, err := s.tickets.Reply(c.Context(), u, id, in.Body, in.Internal); err != nil {
		return err
	}
	d, err := s.tickets.Get(c.Context(), u, id)
	if err != nil {
		return err
	}
	t := fmt.Sprintf("#%d", d.Ticket.Number)
	if in.Internal {
		t += " (internal note)"
	}
	c.Locals(keyAuditTarget, t)
	c.Status(fiber.StatusCreated)
	return s.ticketDetail(c, d)
}

func (s *panel) updateTicket(c fiber.Ctx) error {
	var in struct {
		Status   *string `json:"status"`
		Priority *string `json:"priority"`
		Assignee *string `json:"assignee_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("tid"))
	u := currentUser(c)
	t, err := s.tickets.Update(c.Context(), u, id, service.TicketUpdate{Status: in.Status, Priority: in.Priority, Assignee: in.Assignee})
	if err != nil {
		return err
	}
	var parts []string
	if in.Status != nil {
		parts = append(parts, "status="+t.Status)
	}
	if in.Priority != nil {
		parts = append(parts, "priority="+t.Priority)
	}
	if in.Assignee != nil {
		parts = append(parts, "assignee="+t.AssigneeEmail)
	}
	c.Locals(keyAuditTarget, fmt.Sprintf("#%d %s", t.Number, strings.Join(parts, ", ")))
	d, err := s.tickets.Get(c.Context(), u, id)
	if err != nil {
		return err
	}
	return s.ticketDetail(c, d)
}

func (s *panel) ticketStaff(c fiber.Ctx) error {
	us, err := s.tickets.Staff(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	type staffDTO struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	out := make([]staffDTO, len(us))
	for i, u := range us {
		out[i] = staffDTO{u.ID, u.Email, u.DisplayName}
	}
	return c.JSON(fiber.Map{"staff": out})
}
