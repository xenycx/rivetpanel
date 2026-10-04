package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Public status page. The public DTOs below are the whole contract: they
// carry the administrator-chosen component names and descriptions, states,
// uptime and incident text. Component sources (node or bot ids and names),
// owners, addresses, errors and incident authors are never included.

type statusDayDTO struct {
	Date   string   `json:"date"`
	State  string   `json:"state"`
	Uptime *float64 `json:"uptime"`
}

type statusComponentDTO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	State       string         `json:"state"`
	Uptime      *float64       `json:"uptime"`
	Days        []statusDayDTO `json:"days"`
	// Management view only.
	Position int           `json:"position"`
	Source   *statusSource `json:"source,omitempty"`
}

type statusSource struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing"`
}

type statusUpdateDTO struct {
	Status      string `json:"status"`
	Body        string `json:"body"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

type statusIncidentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type statusIncidentDTO struct {
	ID           string              `json:"id"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	Impact       string              `json:"impact"`
	Status       string              `json:"status"`
	Active       bool                `json:"active"` // maintenance in its window / in progress
	StartsAtMS   *int64              `json:"starts_at_ms"`
	EndsAtMS     *int64              `json:"ends_at_ms"`
	CreatedAtMS  int64               `json:"created_at_ms"`
	UpdatedAtMS  int64               `json:"updated_at_ms"`
	ResolvedAtMS *int64              `json:"resolved_at_ms"`
	Components   []statusIncidentRef `json:"components"`
	Updates      []statusUpdateDTO   `json:"updates"`
}

func toStatusIncident(i domain.StatusIncident, names map[string]string, now int64) statusIncidentDTO {
	d := statusIncidentDTO{ID: i.ID, Kind: i.Kind, Title: i.Title, Impact: i.Impact, Status: i.Status, StartsAtMS: i.StartsAtMS,
		EndsAtMS: i.EndsAtMS, CreatedAtMS: i.CreatedAtMS, UpdatedAtMS: i.UpdatedAtMS, ResolvedAtMS: i.ResolvedAtMS,
		Components: []statusIncidentRef{}, Updates: []statusUpdateDTO{}}
	d.Active = i.Kind == domain.MaintenanceKind && !i.Closed() && (i.Status == domain.MaintenanceActive ||
		(i.StartsAtMS != nil && *i.StartsAtMS <= now && (i.EndsAtMS == nil || now < *i.EndsAtMS)))
	for _, c := range i.ComponentIDs {
		if n, ok := names[c]; ok {
			d.Components = append(d.Components, statusIncidentRef{c, n})
		}
	}
	for _, u := range i.Updates {
		d.Updates = append(d.Updates, statusUpdateDTO{u.Status, u.Body, u.CreatedAtMS})
	}
	return d
}

func statusPageJSON(p service.StatusPage, sources map[string]service.StatusSource) fiber.Map {
	names := map[string]string{}
	comps := make([]statusComponentDTO, 0, len(p.Components))
	for _, c := range p.Components {
		names[c.Component.ID] = c.Component.Name
		d := statusComponentDTO{ID: c.Component.ID, Name: c.Component.Name, Description: c.Component.Description, State: c.State,
			Uptime: c.Uptime, Days: make([]statusDayDTO, len(c.Days))}
		for i, x := range c.Days {
			d.Days[i] = statusDayDTO{x.Date, x.State, x.Uptime}
		}
		if sources != nil {
			src := sources[c.Component.ID]
			d.Position, d.Source = c.Component.Position, &statusSource{src.Kind, src.ID, src.Name, src.Missing}
		}
		comps = append(comps, d)
	}
	incs := make([]statusIncidentDTO, 0, len(p.Incidents))
	for _, i := range p.Incidents {
		incs = append(incs, toStatusIncident(i, names, p.UpdatedMS))
	}
	return fiber.Map{"title": p.Config.Title, "intro": p.Config.Intro, "overall": p.Overall, "components": comps,
		"incidents": incs, "updated_at_ms": p.UpdatedMS, "retention_days": service.StatusRetentionDays}
}

func (s *panel) statusPublicRoutes(v1 fiber.Router) {
	v1.Get("/status", publicLimit("status", 60), s.statusPublic)
}

func (s *panel) statusPublic(c fiber.Ctx) error {
	p, err := s.status.Page(c.Context())
	if err != nil {
		if err == domain.ErrNotFound {
			return fiber.NewError(fiber.StatusNotFound, "the status page is not enabled")
		}
		return err
	}
	c.Set(fiber.HeaderCacheControl, "public, max-age=30")
	return c.JSON(statusPageJSON(p, nil))
}

func (s *panel) statusManageRoutes(authed fiber.Router) {
	m := s.requirePerm(domain.PermStatusManage)
	authed.Get("/status/manage", m, s.statusManage)
	authed.Get("/status/manage/candidates", m, s.statusCandidates)
	authed.Put("/status/manage/config", m, s.statusPutConfig)
	authed.Post("/status/manage/components", m, s.statusCreateComponent)
	authed.Patch("/status/manage/components/:cid", m, s.statusUpdateComponent)
	authed.Delete("/status/manage/components/:cid", m, s.statusDeleteComponent)
	authed.Post("/status/manage/incidents", m, s.statusCreateIncident)
	authed.Patch("/status/manage/incidents/:iid", m, s.statusUpdateIncident)
	authed.Post("/status/manage/incidents/:iid/updates", m, s.statusPostUpdate)
	authed.Delete("/status/manage/incidents/:iid", m, s.statusDeleteIncident)
}

func (s *panel) statusManage(c fiber.Ctx) error {
	v, err := s.status.Manage(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := statusPageJSON(v.Page, v.Sources)
	out["enabled"] = v.Page.Config.Enabled
	return c.JSON(out)
}

func (s *panel) statusCandidates(c fiber.Ctx) error {
	cs, err := s.status.Candidates(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	type dto struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]dto, len(cs))
	for i, x := range cs {
		out[i] = dto{x.Kind, x.ID, x.Name}
	}
	return c.JSON(fiber.Map{"candidates": out})
}

func (s *panel) statusPutConfig(c fiber.Ctx) error {
	var in struct {
		Enabled bool   `json:"enabled"`
		Title   string `json:"title"`
		Intro   string `json:"intro"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	cfg, err := s.status.SetConfig(c.Context(), currentUser(c), service.StatusConfig{Enabled: in.Enabled, Title: in.Title, Intro: in.Intro})
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, "enabled="+strconv.FormatBool(cfg.Enabled))
	return c.JSON(fiber.Map{"enabled": cfg.Enabled, "title": cfg.Title, "intro": cfg.Intro})
}

type statusComponentIn struct {
	Kind        string  `json:"kind"`
	RefID       string  `json:"ref_id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
}

func (in statusComponentIn) input() service.StatusComponentInput {
	return service.StatusComponentInput{Kind: in.Kind, RefID: in.RefID, Name: in.Name, Description: in.Description, Position: in.Position}
}

func (s *panel) statusCreateComponent(c fiber.Ctx) error {
	var in statusComponentIn
	if err := decode(c, &in); err != nil {
		return err
	}
	x, err := s.status.SaveComponent(c.Context(), currentUser(c), "", in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Name+" ("+x.Kind+")")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": x.ID, "name": x.Name})
}

func (s *panel) statusUpdateComponent(c fiber.Ctx) error {
	var in statusComponentIn
	if err := decode(c, &in); err != nil {
		return err
	}
	x, err := s.status.SaveComponent(c.Context(), currentUser(c), strings.Clone(c.Params("cid")), in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Name+" ("+x.Kind+")")
	return c.JSON(fiber.Map{"id": x.ID, "name": x.Name})
}

func (s *panel) statusDeleteComponent(c fiber.Ctx) error {
	x, err := s.status.DeleteComponent(c.Context(), currentUser(c), strings.Clone(c.Params("cid")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Name+" ("+x.Kind+")")
	return c.SendStatus(fiber.StatusNoContent)
}

type statusIncidentIn struct {
	Kind         string    `json:"kind"`
	Title        *string   `json:"title"`
	Impact       *string   `json:"impact"`
	Status       *string   `json:"status"`
	ComponentIDs *[]string `json:"component_ids"`
	StartsAtMS   *int64    `json:"starts_at_ms"`
	EndsAtMS     *int64    `json:"ends_at_ms"`
	ClearWindow  bool      `json:"clear_window"`
	Message      string    `json:"message"`
}

func (in statusIncidentIn) input() service.IncidentInput {
	return service.IncidentInput{Kind: in.Kind, Title: in.Title, Impact: in.Impact, Status: in.Status, ComponentIDs: in.ComponentIDs,
		StartsAtMS: in.StartsAtMS, EndsAtMS: in.EndsAtMS, ClearWindow: in.ClearWindow, Message: in.Message}
}

func (s *panel) incidentReply(c fiber.Ctx, i domain.StatusIncident, status int) error {
	c.Locals(keyAuditTarget, i.Title+" ("+i.Kind+", "+i.Status+")")
	return c.Status(status).JSON(toStatusIncident(i, map[string]string{}, 0))
}

func (s *panel) statusCreateIncident(c fiber.Ctx) error {
	var in statusIncidentIn
	if err := decode(c, &in); err != nil {
		return err
	}
	i, err := s.status.SaveIncident(c.Context(), currentUser(c), "", in.input())
	if err != nil {
		return err
	}
	return s.incidentReply(c, i, fiber.StatusCreated)
}

func (s *panel) statusUpdateIncident(c fiber.Ctx) error {
	var in statusIncidentIn
	if err := decode(c, &in); err != nil {
		return err
	}
	i, err := s.status.SaveIncident(c.Context(), currentUser(c), strings.Clone(c.Params("iid")), in.input())
	if err != nil {
		return err
	}
	return s.incidentReply(c, i, fiber.StatusOK)
}

func (s *panel) statusPostUpdate(c fiber.Ctx) error {
	var in struct {
		Status  *string `json:"status"`
		Message string  `json:"message"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Message) == "" {
		return domain.Invalid("the update needs a message")
	}
	i, err := s.status.SaveIncident(c.Context(), currentUser(c), strings.Clone(c.Params("iid")), service.IncidentInput{Status: in.Status, Message: in.Message})
	if err != nil {
		return err
	}
	return s.incidentReply(c, i, fiber.StatusCreated)
}

func (s *panel) statusDeleteIncident(c fiber.Ctx) error {
	i, err := s.status.DeleteIncident(c.Context(), currentUser(c), strings.Clone(c.Params("iid")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, i.Title+" ("+i.Kind+")")
	return c.SendStatus(fiber.StatusNoContent)
}
