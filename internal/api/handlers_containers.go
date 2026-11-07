package api

import (
	"strings"

	fws "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/apptemplates"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Container apps (a Docker image chosen by its owner) and stacks imported
// from Compose files.
func (s *panel) containerRoutes(authed fiber.Router) {
	if s.containers == nil {
		return
	}
	authed.Get("/containers/policy", s.containerPolicy)
	authed.Post("/containers", s.requirePerm(domain.PermBotsCreate), s.createContainer)
	authed.Put("/bots/:id/container", s.putContainer)
	authed.Post("/bots/:id/container/pull", s.pullContainer)
	authed.Get("/stacks", s.listStacks)
	authed.Post("/stacks/import", s.requirePerm(domain.PermBotsCreate), s.importStack)
	authed.Get("/stacks/:id", s.getStack)
	authed.Patch("/stacks/:id", s.renameStack)
	authed.Post("/stacks/:id/:action", s.stackPower)
	authed.Delete("/stacks/:id", s.deleteStack)
	authed.Get("/app-templates", s.listAppTemplates)
	authed.Post("/app-templates/:id/install", s.requirePerm(domain.PermBotsCreate), s.installAppTemplate)
	if s.containers.TerminalAvailable() {
		authed.Get("/bots/:id/terminal", s.terminalGuard, fws.New(s.terminalWS, fws.Config{
			ReadBufferSize: 4096, WriteBufferSize: 32 << 10, AllowEmptyOrigin: true,
		}))
	}
}

func (s *panel) listAppTemplates(c fiber.Ctx) error {
	sitesOn := s.sites != nil && s.sites.Enabled()
	return c.JSON(fiber.Map{"templates": s.containers.AppTemplates(), "categories": apptemplates.Categories,
		"sites": sitesOn, "public_ports": s.bots.Limits.PortPublicBind})
}

func (s *panel) installAppTemplate(c fiber.Ctx) error {
	var in struct {
		Name        string            `json:"name"`
		WorkspaceID string            `json:"workspace_id"`
		Values      map[string]string `json:"values"`
		Address     string            `json:"address"`
		Slug        string            `json:"slug"`
		DomainID    string            `json:"domain_id"`
		DryRun      bool              `json:"dry_run"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	res, err := s.containers.InstallTemplate(c.Context(), currentUser(c), service.TemplateInstall{
		TemplateID: strings.Clone(c.Params("id")), Name: in.Name, WorkspaceID: in.WorkspaceID, Values: in.Values,
		Address: in.Address, Slug: in.Slug, DomainID: in.DomainID, DryRun: in.DryRun})
	if err != nil {
		return err
	}
	out := fiber.Map{"plan": res.Plan, "url": res.URL}
	if res.Stack != nil {
		res.Plan.Stack = nil
		out["plan"] = res.Plan
		out["stack"] = s.viewStack(c, *res.Stack)
		return c.Status(fiber.StatusCreated).JSON(out)
	}
	return c.JSON(out)
}

func (s *panel) containerPolicy(c fiber.Ctx) error {
	p := s.containers.Policy
	return c.JSON(fiber.Map{"registries": nonNil(p.Registries), "max_image_bytes": p.MaxImageBytes,
		"enabled": p.Enabled(), "public_ports": s.bots.Limits.PortPublicBind})
}

type portIn struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip"`
}

func portInputs(in []portIn) []service.PortInput {
	out := make([]service.PortInput, len(in))
	for i, p := range in {
		out[i] = service.PortInput{ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol, HostIP: p.HostIP}
	}
	return out
}

func (s *panel) createContainer(c fiber.Ctx) error {
	var in struct {
		Name            string            `json:"name"`
		WorkspaceID     string            `json:"workspace_id"`
		Image           string            `json:"image"`
		Command         []string          `json:"command"`
		Entrypoint      []string          `json:"entrypoint"`
		User            string            `json:"user"`
		Writable        []string          `json:"writable"`
		Volumes         []domain.Volume   `json:"volumes"`
		Ports           []portIn          `json:"ports"`
		Env             map[string]string `json:"env"`
		MemoryBytes     int64             `json:"memory_bytes"`
		NanoCPUs        int64             `json:"nano_cpus"`
		NetworkDisabled bool              `json:"network_disabled"`
		Start           bool              `json:"start"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	b, err := s.containers.Create(c.Context(), u, service.ContainerInput{Name: in.Name, WorkspaceID: in.WorkspaceID, Image: in.Image,
		Command: in.Command, Entrypoint: in.Entrypoint, User: in.User, Writable: in.Writable, Volumes: in.Volumes,
		Ports: portInputs(in.Ports), Env: in.Env, MemoryBytes: in.MemoryBytes, NanoCPUs: in.NanoCPUs, NetworkDisabled: in.NetworkDisabled})
	if err != nil {
		return err
	}
	if in.Start {
		if started, err := s.bots.Start(c.Context(), u, b.ID); err == nil {
			b = started
		}
	}
	return c.Status(fiber.StatusCreated).JSON(s.viewBot(c, b))
}

func (s *panel) putContainer(c fiber.Ctx) error {
	var in struct {
		Image    *string          `json:"image"`
		User     *string          `json:"user"`
		Writable *[]string        `json:"writable"`
		Volumes  *[]domain.Volume `json:"volumes"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.containers.Update(c.Context(), currentUser(c), strings.Clone(c.Params("id")),
		service.ContainerUpdate{Image: in.Image, User: in.User, Writable: in.Writable, Volumes: in.Volumes})
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) pullContainer(c fiber.Ctx) error {
	b, err := s.containers.Pull(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

type stackDTO struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	OwnerID     string   `json:"owner_id"`
	Name        string   `json:"name"`
	Compose     string   `json:"compose,omitempty"`
	CreatedAtMS int64    `json:"created_at_ms"`
	UpdatedAtMS int64    `json:"updated_at_ms"`
	Role        string   `json:"role"`
	Services    []botDTO `json:"services"`
	// One-click apps: the template, the address and its site.
	TemplateID string                 `json:"template_id"`
	Template   *service.StackTemplate `json:"template,omitempty"`
	URL        string                 `json:"url,omitempty"`
	SiteID     string                 `json:"site_id,omitempty"`
}

func (s *panel) viewStack(c fiber.Ctx, v service.StackView) stackDTO {
	d := stackDTO{ID: v.ID, WorkspaceID: v.WorkspaceID, OwnerID: v.OwnerID, Name: v.Name, Compose: v.ComposeYAML,
		CreatedAtMS: v.CreatedAtMS, UpdatedAtMS: v.UpdatedAtMS, Role: v.Role, Services: make([]botDTO, len(v.Services)),
		TemplateID: v.TemplateID}
	d.Template, d.URL, d.SiteID = s.containers.StackAddress(c.Context(), v)
	tags, favs, _ := s.bots.TagsAndFavorites(c.Context(), currentUser(c))
	for i, b := range v.Services {
		bd := s.viewBot(c, b)
		bd.Tags, bd.Favorite = tags[b.ID], favs[b.ID]
		if bd.Tags == nil {
			bd.Tags = []string{}
		}
		d.Services[i] = bd
	}
	return d
}

func (s *panel) listStacks(c fiber.Ctx) error {
	vs, err := s.containers.ListStacks(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]stackDTO, len(vs))
	for i, v := range vs {
		out[i] = s.viewStack(c, v)
	}
	return c.JSON(fiber.Map{"stacks": out})
}

func (s *panel) importStack(c fiber.Ctx) error {
	var in struct {
		Name        string `json:"name"`
		WorkspaceID string `json:"workspace_id"`
		Compose     string `json:"compose"`
		Env         string `json:"env"`
		DryRun      bool   `json:"dry_run"`
		Start       bool   `json:"start"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	plan, err := s.containers.ImportCompose(c.Context(), currentUser(c), service.ComposeImport{Name: in.Name, WorkspaceID: in.WorkspaceID,
		Compose: in.Compose, Env: in.Env, DryRun: in.DryRun, Start: in.Start})
	if err != nil {
		return err
	}
	out := fiber.Map{"plan": plan}
	if plan.Stack != nil {
		out["stack"] = s.viewStack(c, *plan.Stack)
		plan.Stack = nil
		out["plan"] = plan
		return c.Status(fiber.StatusCreated).JSON(out)
	}
	return c.JSON(out)
}

func (s *panel) getStack(c fiber.Ctx) error {
	v, err := s.containers.GetStack(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewStack(c, v))
}

func (s *panel) renameStack(c fiber.Ctx) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.containers.RenameStack(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Name)
	if err != nil {
		return err
	}
	return c.JSON(s.viewStack(c, v))
}

func (s *panel) stackPower(c fiber.Ctx) error {
	v, err := s.containers.StackPower(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("action")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewStack(c, v))
}

func (s *panel) deleteStack(c fiber.Ctx) error {
	if err := s.containers.DeleteStack(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
