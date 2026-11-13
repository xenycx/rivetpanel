package api

import (
	"fmt"
	"slices"
	"strconv"
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
	Key         string  `json:"key"` // RT-000042
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
	RequesterID    string     `json:"requester_id,omitempty"`
	RequesterEmail string     `json:"requester_email,omitempty"`
	AssigneeID     *string    `json:"assignee_id,omitempty"`
	AssigneeEmail  string     `json:"assignee_email,omitempty"`
	Requester      *personDTO `json:"requester,omitempty"`
	Assignee       *personDTO `json:"assignee,omitempty"`
	Labels         []string   `json:"labels,omitempty"`
	Watching       bool       `json:"watching,omitempty"`
}

// personDTO is how an account appears on a ticket (staff views, and the
// reader's own messages).
type personDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatar_url"`
	LogoVariant int64  `json:"logo_variant"`
}

func toPerson(p domain.Person) *personDTO {
	if p.ID == "" {
		return nil
	}
	d := &personDTO{ID: p.ID, Name: p.DisplayName, Email: p.Email, LogoVariant: p.LogoVariant}
	if d.Name == "" {
		d.Name = p.Email
	}
	if p.HasAvatar {
		d.AvatarURL = "/api/v1/users/" + p.ID + "/avatar?v=" + strconv.FormatInt(p.AvatarAtMS, 10)
	}
	return d
}

func toTicket(t domain.Ticket, staff bool) ticketDTO {
	d := ticketDTO{ID: t.ID, Number: t.Number, Key: domain.TicketKey(t.Number), Subject: t.Subject, Category: t.Category, Priority: t.Priority, Status: t.Status,
		BotID: t.BotID, BotName: t.BotName, CreatedAtMS: t.CreatedAtMS, UpdatedAtMS: t.UpdatedAtMS, ClosedAtMS: t.ClosedAtMS, Messages: t.Messages}
	if staff {
		d.RequesterID, d.RequesterEmail, d.AssigneeID, d.AssigneeEmail = t.UserID, t.UserEmail, t.AssigneeID, t.AssigneeEmail
		d.Requester, d.Assignee = toPerson(t.Requester), toPerson(t.Assignee)
		d.Labels = t.LabelIDs
		if d.Labels == nil {
			d.Labels = []string{}
		}
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
	// Person is the author's picture and name; null for staff seen by a
	// requester (shown as the support team).
	Person     *personDTO    `json:"person"`
	EditedAtMS *int64        `json:"edited_at_ms"`
	Deleted    bool          `json:"deleted"`
	DeletedBy  string        `json:"deleted_by,omitempty"` // staff view only
	Mentions   []string      `json:"mentions,omitempty"`   // staff view only
	Reactions  []reactionDTO `json:"reactions"`
}

type reactionDTO struct {
	Kind  string   `json:"kind"`
	Count int      `json:"count"`
	Mine  bool     `json:"mine"`
	Names []string `json:"names"`
}

func toTicketMessage(m domain.TicketMessage, me string, staffView bool) ticketMessageDTO {
	author := m.AuthorLabel
	hide := m.Staff && !staffView
	if hide {
		author = "Support team" // requesters do not see staff addresses
	}
	d := ticketMessageDTO{ID: m.ID, Author: author, Mine: m.AuthorID != nil && *m.AuthorID == me, Kind: m.Kind,
		Staff: m.Staff, Internal: m.Internal, Body: m.Body, CreatedAtMS: m.CreatedAtMS, EditedAtMS: m.EditedAtMS,
		Deleted: m.DeletedAtMS != nil, Reactions: []reactionDTO{}}
	if !hide {
		d.Person = toPerson(m.Author)
	}
	if d.Deleted {
		d.Body = ""
	}
	if staffView {
		d.DeletedBy = m.DeletedBy
		d.Mentions = m.Mentions
	}
	for _, r := range m.Reactions {
		i := slices.IndexFunc(d.Reactions, func(x reactionDTO) bool { return x.Kind == r.Kind })
		if i < 0 {
			d.Reactions = append(d.Reactions, reactionDTO{Kind: r.Kind, Names: []string{}})
			i = len(d.Reactions) - 1
		}
		x := &d.Reactions[i]
		x.Count++
		if r.UserID == me {
			x.Mine = true
		}
		switch {
		case r.UserID == me:
			x.Names = append(x.Names, "You")
		case staffView:
			x.Names = append(x.Names, r.Label)
		case r.Staff:
			if !slices.Contains(x.Names, "Support team") {
				x.Names = append(x.Names, "Support team")
			}
		}
	}
	return d
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
	authed.Get("/tickets/labels", s.requireAnyPerm(domain.PermTicketsViewAll, domain.PermTicketsManage), s.listTicketLabels)
	authed.Post("/tickets/labels", s.requirePerm(domain.PermTicketsManage), s.createTicketLabel)
	authed.Patch("/tickets/labels/:lid", s.requirePerm(domain.PermTicketsManage), s.updateTicketLabel)
	authed.Delete("/tickets/labels/:lid", s.requirePerm(domain.PermTicketsManage), s.deleteTicketLabel)
	authed.Get("/tickets/replies", s.requirePerm(domain.PermTicketsManage), s.listSavedReplies)
	authed.Post("/tickets/replies", s.requirePerm(domain.PermTicketsManage), s.putSavedReply)
	authed.Patch("/tickets/replies/:rid", s.requirePerm(domain.PermTicketsManage), s.putSavedReply)
	authed.Delete("/tickets/replies/:rid", s.requirePerm(domain.PermTicketsManage), s.deleteSavedReply)
	authed.Get("/tickets/:tid", anyTicket, s.getTicket)
	authed.Post("/tickets/:tid/messages", anyTicket,
		perUserLimit("ticket-reply", 60, 10*time.Minute, "too many replies; try again in a few minutes"), s.replyTicket)
	authed.Patch("/tickets/:tid", anyTicket, s.updateTicket)
	editLimit := perUserLimit("ticket-edit", 60, 10*time.Minute, "too many changes; try again in a few minutes")
	authed.Patch("/tickets/:tid/messages/:mid", anyTicket, editLimit, s.editTicketMessage)
	authed.Delete("/tickets/:tid/messages/:mid", anyTicket, editLimit, s.removeTicketMessage)
	authed.Get("/tickets/:tid/messages/:mid/revisions", anyTicket, s.ticketMessageRevisions)
	reactLimit := perUserLimit("ticket-react", 120, 10*time.Minute, "too many reactions; try again in a few minutes")
	authed.Put("/tickets/:tid/messages/:mid/reactions/:kind", anyTicket, reactLimit, s.reactTicketMessage)
	authed.Delete("/tickets/:tid/messages/:mid/reactions/:kind", anyTicket, reactLimit, s.reactTicketMessage)
	authed.Put("/tickets/:tid/watch", s.requireAnyPerm(domain.PermTicketsViewAll, domain.PermTicketsManage), s.watchTicket)
	authed.Delete("/tickets/:tid/watch", s.requireAnyPerm(domain.PermTicketsViewAll, domain.PermTicketsManage), s.watchTicket)
	authed.Put("/tickets/:tid/labels", s.requirePerm(domain.PermTicketsManage), s.setTicketLabels)
}

func (s *panel) listTickets(c fiber.Ctx) error {
	scope := c.Query("scope", service.TicketsMine)
	ts, err := s.tickets.List(c.Context(), currentUser(c), scope, c.Query("status"), c.Query("assigned") == "me")
	if err != nil {
		return err
	}
	staff := scope == service.TicketsAll
	watched := map[string]bool{}
	if staff {
		if watched, err = s.tickets.Watched(c.Context(), currentUser(c)); err != nil {
			return err
		}
	}
	out := make([]ticketDTO, len(ts))
	for i, t := range ts {
		out[i] = toTicket(t, staff)
		out[i].Watching = watched[t.ID]
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
	c.Locals(keyAuditTarget, domain.TicketKey(t.Number))
	return c.Status(fiber.StatusCreated).JSON(toTicket(t, false))
}

func (s *panel) ticketDetail(c fiber.Ctx, d service.TicketDetail) error {
	u := currentUser(c)
	msgs := make([]ticketMessageDTO, len(d.Messages))
	for i, m := range d.Messages {
		msgs[i] = toTicketMessage(m, u.ID, d.Staff)
	}
	t := toTicket(d.Ticket, d.Staff)
	out := fiber.Map{"ticket": t, "messages": msgs, "staff": d.Staff, "manage": d.Manage, "requester": d.Requester}
	if d.Staff {
		ws := make([]*personDTO, 0, len(d.Watchers))
		for _, w := range d.Watchers {
			ws = append(ws, toPerson(w))
		}
		t.Watching = d.Watching
		out["ticket"], out["watchers"] = t, ws
	}
	return c.JSON(out)
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
		Body     string   `json:"body"`
		Internal bool     `json:"internal"`
		Mentions []string `json:"mentions"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("tid"))
	u := currentUser(c)
	if _, err := s.tickets.Reply(c.Context(), u, id, in.Body, in.Internal, in.Mentions...); err != nil {
		return err
	}
	d, err := s.tickets.Get(c.Context(), u, id)
	if err != nil {
		return err
	}
	t := domain.TicketKey(d.Ticket.Number)
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
		Subject  *string `json:"subject"`
		Category *string `json:"category"`
		BotID    *string `json:"bot_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("tid"))
	u := currentUser(c)
	t, err := s.tickets.Update(c.Context(), u, id, service.TicketUpdate{Status: in.Status, Priority: in.Priority, Assignee: in.Assignee,
		Subject: in.Subject, Category: in.Category, BotID: in.BotID})
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
	if in.Subject != nil || in.Category != nil || in.BotID != nil {
		parts = append(parts, "details")
	}
	c.Locals(keyAuditTarget, domain.TicketKey(t.Number)+" "+strings.Join(parts, ", "))
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
		AvatarURL   string `json:"avatar_url"`
		LogoVariant int64  `json:"logo_variant"`
	}
	out := make([]staffDTO, len(us))
	for i, u := range us {
		d := toUser(u)
		out[i] = staffDTO{u.ID, u.Email, u.DisplayName, d.AvatarURL, u.LogoVariant}
	}
	return c.JSON(fiber.Map{"staff": out})
}

// ---- Messages: edit, remove, history, reactions ----

func (s *panel) ticketAfterChange(c fiber.Ctx, tid string) error {
	d, err := s.tickets.Get(c.Context(), currentUser(c), tid)
	if err != nil {
		return err
	}
	return s.ticketDetail(c, d)
}

func (s *panel) editTicketMessage(c fiber.Ctx) error {
	var in struct {
		Body string `json:"body"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	tid := strings.Clone(c.Params("tid"))
	if err := s.tickets.EditMessage(c.Context(), currentUser(c), tid, strings.Clone(c.Params("mid")), in.Body); err != nil {
		return err
	}
	return s.ticketAfterChange(c, tid)
}

func (s *panel) removeTicketMessage(c fiber.Ctx) error {
	tid := strings.Clone(c.Params("tid"))
	if err := s.tickets.RemoveMessage(c.Context(), currentUser(c), tid, strings.Clone(c.Params("mid"))); err != nil {
		return err
	}
	return s.ticketAfterChange(c, tid)
}

func (s *panel) ticketMessageRevisions(c fiber.Ctx) error {
	rs, err := s.tickets.MessageRevisions(c.Context(), currentUser(c), strings.Clone(c.Params("tid")), strings.Clone(c.Params("mid")))
	if err != nil {
		return err
	}
	type revDTO struct {
		ID          string `json:"id"`
		Body        string `json:"body"`
		Editor      string `json:"editor"`
		CreatedAtMS int64  `json:"created_at_ms"`
	}
	out := make([]revDTO, len(rs))
	for i, r := range rs {
		out[i] = revDTO{r.ID, r.Body, r.EditorLabel, r.CreatedAtMS}
	}
	return c.JSON(fiber.Map{"revisions": out})
}

func (s *panel) reactTicketMessage(c fiber.Ctx) error {
	tid := strings.Clone(c.Params("tid"))
	if err := s.tickets.React(c.Context(), currentUser(c), tid, strings.Clone(c.Params("mid")), strings.Clone(c.Params("kind")), c.Method() == fiber.MethodPut); err != nil {
		return err
	}
	return s.ticketAfterChange(c, tid)
}

func (s *panel) watchTicket(c fiber.Ctx) error {
	tid := strings.Clone(c.Params("tid"))
	if err := s.tickets.Watch(c.Context(), currentUser(c), tid, c.Method() == fiber.MethodPut); err != nil {
		return err
	}
	return s.ticketAfterChange(c, tid)
}

// ---- Labels ----

type labelDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Tickets int    `json:"tickets"`
}

func (s *panel) listTicketLabels(c fiber.Ctx) error {
	ls, err := s.tickets.Labels(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]labelDTO, len(ls))
	for i, l := range ls {
		out[i] = labelDTO{l.ID, l.Name, l.Color, l.Tickets}
	}
	return c.JSON(fiber.Map{"labels": out})
}

type labelIn struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *panel) createTicketLabel(c fiber.Ctx) error {
	var in labelIn
	if err := decode(c, &in); err != nil {
		return err
	}
	l, err := s.tickets.CreateLabel(c.Context(), currentUser(c), in.Name, in.Color)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, l.Name)
	return c.Status(fiber.StatusCreated).JSON(labelDTO{l.ID, l.Name, l.Color, 0})
}

func (s *panel) updateTicketLabel(c fiber.Ctx) error {
	var in labelIn
	if err := decode(c, &in); err != nil {
		return err
	}
	l, err := s.tickets.UpdateLabel(c.Context(), currentUser(c), strings.Clone(c.Params("lid")), in.Name, in.Color)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, l.Name)
	return c.JSON(labelDTO{l.ID, l.Name, l.Color, 0})
}

func (s *panel) deleteTicketLabel(c fiber.Ctx) error {
	if err := s.tickets.DeleteLabel(c.Context(), currentUser(c), strings.Clone(c.Params("lid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) setTicketLabels(c fiber.Ctx) error {
	var in struct {
		Labels []string `json:"labels"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	tid := strings.Clone(c.Params("tid"))
	t, err := s.tickets.SetLabels(c.Context(), currentUser(c), tid, in.Labels)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, fmt.Sprintf("%s labels=%d", domain.TicketKey(t.Number), len(t.LabelIDs)))
	return s.ticketAfterChange(c, tid)
}

// ---- Saved replies ----

type savedReplyDTO struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (s *panel) listSavedReplies(c fiber.Ctx) error {
	rs, err := s.tickets.SavedReplies(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]savedReplyDTO, len(rs))
	for i, r := range rs {
		out[i] = savedReplyDTO{r.ID, r.Title, r.Body, r.UpdatedAtMS}
	}
	return c.JSON(fiber.Map{"replies": out})
}

func (s *panel) putSavedReply(c fiber.Ctx) error {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	r, err := s.tickets.PutSavedReply(c.Context(), currentUser(c), strings.Clone(c.Params("rid")), in.Title, in.Body)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, r.Title)
	status := fiber.StatusOK
	if c.Params("rid") == "" {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(savedReplyDTO{r.ID, r.Title, r.Body, r.UpdatedAtMS})
}

func (s *panel) deleteSavedReply(c fiber.Ctx) error {
	if err := s.tickets.DeleteSavedReply(c.Context(), currentUser(c), strings.Clone(c.Params("rid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
