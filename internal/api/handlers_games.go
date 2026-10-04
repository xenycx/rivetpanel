package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/modrinth"
	"github.com/xenycx/rivetpanel/internal/service"
)

type blueprintDTO struct {
	ID              string         `json:"id"`
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	Category        string         `json:"category"`
	Description     string         `json:"description"`
	Source          string         `json:"source"`
	CurrentRevision int64          `json:"current_revision"`
	Enabled         bool           `json:"enabled"`
	Servers         int            `json:"servers"`
	Spec            blueprint.Spec `json:"spec"`
}

func toBlueprint(v service.BlueprintView) blueprintDTO {
	return blueprintDTO{ID: v.ID, Slug: v.Slug, Name: v.Name, Category: v.Category, Description: v.Description, Source: v.Source,
		CurrentRevision: v.CurrentRevision, Enabled: v.Enabled, Servers: v.Servers, Spec: publicSpec(v.Spec)}
}

// publicSpec omits the install script from views (administrators export the
// full YAML instead); everything else describes what the server does.
func publicSpec(s blueprint.Spec) blueprint.Spec {
	if s.Install.Script != "" {
		s.Install.Script = "(script)"
	}
	if s.Variables == nil {
		s.Variables = []blueprint.Variable{}
	}
	if s.Agreements == nil {
		s.Agreements = []blueprint.Agreement{}
	}
	return s
}

func (s *panel) listBlueprints(c fiber.Ctx) error {
	list, err := s.games.ListBlueprints(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]blueprintDTO, 0, len(list))
	for _, v := range list {
		out = append(out, toBlueprint(v))
	}
	return c.JSON(fiber.Map{"blueprints": out})
}

func (s *panel) getBlueprint(c fiber.Ctx) error {
	v, err := s.games.GetBlueprint(c.Context(), currentUser(c), strings.Clone(c.Params("bp")))
	if err != nil {
		return err
	}
	return c.JSON(toBlueprint(v))
}

func (s *panel) blueprintVersions(c fiber.Ctx) error {
	list, err := s.games.Versions(c.Context(), currentUser(c), strings.Clone(c.Params("bp")), strings.Clone(c.Params("env")))
	if err != nil {
		return err
	}
	if list == nil {
		list = []blueprint.Version{}
	}
	return c.JSON(fiber.Map{"versions": list})
}

func (s *panel) createGameServer(c fiber.Ctx) error {
	var in struct {
		Name        string            `json:"name"`
		Blueprint   string            `json:"blueprint"`
		Variables   map[string]string `json:"variables"`
		Image       string            `json:"image"`
		MemoryBytes int64             `json:"memory_bytes"`
		NanoCPUs    int64             `json:"nano_cpus"`
		Agreements  []string          `json:"agreements"`
		WorkspaceID string            `json:"workspace_id"`
		NodeID      string            `json:"node_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.games.CreateServer(c.Context(), currentUser(c), service.GameServerInput{Name: in.Name, Blueprint: in.Blueprint,
		Variables: in.Variables, ImageChoice: in.Image, MemoryBytes: in.MemoryBytes, NanoCPUs: in.NanoCPUs,
		Agreements: in.Agreements, WorkspaceID: in.WorkspaceID, NodeID: in.NodeID})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.viewBot(c, b))
}

type gameVariableDTO struct {
	blueprint.Variable
	Value string `json:"value"`
}

func (s *panel) getGame(c fiber.Ctx) error {
	d, err := s.games.Detail(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	vars := make([]gameVariableDTO, 0, len(d.Variables))
	for _, v := range d.Variables {
		vars = append(vars, gameVariableDTO{Variable: v.Variable, Value: v.Value})
	}
	return c.JSON(fiber.Map{
		"blueprint": fiber.Map{"id": d.Blueprint.ID, "slug": d.Blueprint.Slug, "name": d.Blueprint.Name, "category": d.Blueprint.Category,
			"current_revision": d.Blueprint.CurrentRevision},
		"spec": publicSpec(d.Spec), "variables": vars, "update_available": d.UpdateAvailable,
		"startup": blueprint.StartupCommand(d.Spec.Startup.Command),
	})
}

func (s *panel) putGameVariables(c fiber.Ctx) error {
	var in struct {
		Variables map[string]string `json:"variables"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.games.SetVariables(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Variables)
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) reinstallGame(c fiber.Ctx) error {
	b, err := s.games.Reinstall(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) putGameImage(c fiber.Ctx) error {
	var in struct {
		Image string `json:"image"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.games.SetImage(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Image)
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) upgradeGame(c fiber.Ctx) error {
	b, err := s.games.UpgradeBlueprint(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) queryGame(c fiber.Ctx) error {
	st, err := s.games.Query(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	if st.Sample == nil {
		st.Sample = []string{}
	}
	return c.JSON(st)
}

func allocList(list []domain.Allocation) []allocDTO {
	out := make([]allocDTO, 0, len(list))
	for _, a := range list {
		out = append(out, toAlloc(a))
	}
	return out
}

func (s *panel) addGameAllocation(c fiber.Ctx) error {
	list, err := s.games.AddAllocation(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"allocations": allocList(list)})
}

func (s *panel) removeGameAllocation(c fiber.Ctx) error {
	if err := s.games.RemoveAllocation(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("aid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) primaryGameAllocation(c fiber.Ctx) error {
	if err := s.games.SetPrimaryAllocation(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("aid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) addonSearch(c fiber.Ctx) error {
	offset, _ := strconv.Atoi(c.Query("offset"))
	ac, hits, total, err := s.games.AddonSearch(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Query("q")), offset)
	if err != nil {
		return err
	}
	if hits == nil {
		hits = []modrinth.Hit{}
	}
	return c.JSON(fiber.Map{"source": ac.Source, "game_version": ac.GameVersion, "hits": hits, "total": total})
}

func (s *panel) addonVersions(c fiber.Ctx) error {
	vs, err := s.games.AddonVersions(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("project")))
	if err != nil {
		return err
	}
	if vs == nil {
		vs = []modrinth.Version{}
	}
	return c.JSON(fiber.Map{"versions": vs})
}

func (s *panel) addonInstall(c fiber.Ctx) error {
	var in struct {
		Project string `json:"project"`
		Version string `json:"version"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	r, err := s.games.AddonInstall(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Project, in.Version)
	if err != nil {
		return err
	}
	if r.Required == nil {
		r.Required = []string{}
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"filename": r.Filename, "path": r.Path, "required": r.Required})
}

func (s *panel) sendCommand(c fiber.Ctx) error {
	var in struct {
		Command string `json:"command"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.bots.SendCommand(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Command); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// --- Administration ---

func (s *panel) adminImportBlueprint(c fiber.Ctx) error {
	var in struct {
		YAML string `json:"yaml"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, created, err := s.games.ImportBlueprint(c.Context(), currentUser(c), []byte(in.YAML))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": b.ID, "slug": b.Slug, "revision": b.CurrentRevision, "changed": created})
}

func (s *panel) adminEggPreview(c fiber.Ctx) error {
	// The egg arrives as a JSON string; allow for escaping overhead.
	if len(c.Body()) > 2*blueprint.MaxEggBytes+4096 {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "the egg is larger than 512 KiB")
	}
	var in struct {
		Egg string `json:"egg"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	d, err := s.games.ConvertEgg(c.Context(), currentUser(c), []byte(in.Egg))
	if err != nil {
		return err
	}
	return c.JSON(d)
}

func (s *panel) adminExportBlueprint(c fiber.Ctx) error {
	rev, _ := strconv.ParseInt(c.Query("rev"), 10, 64)
	r, err := s.games.ExportBlueprint(c.Context(), currentUser(c), strings.Clone(c.Params("bp")), rev)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/yaml; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="blueprint-r`+strconv.FormatInt(r.Revision, 10)+`.yaml"`)
	return c.SendString(r.SpecYAML)
}

func (s *panel) adminBlueprintRevisions(c fiber.Ctx) error {
	list, err := s.games.Revisions(c.Context(), currentUser(c), strings.Clone(c.Params("bp")))
	if err != nil {
		return err
	}
	out := make([]fiber.Map, 0, len(list))
	for _, r := range list {
		out = append(out, fiber.Map{"revision": r.Revision, "sha256": r.SHA256, "created_at_ms": r.CreatedAtMS})
	}
	return c.JSON(fiber.Map{"revisions": out})
}

func (s *panel) adminPatchBlueprint(c fiber.Ctx) error {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return domain.Invalid("nothing to change")
	}
	id := strings.Clone(c.Params("bp"))
	if err := s.games.SetBlueprintEnabled(c.Context(), currentUser(c), id, *in.Enabled); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) adminDeleteBlueprint(c fiber.Ctx) error {
	id := strings.Clone(c.Params("bp"))
	if err := s.games.DeleteBlueprint(c.Context(), currentUser(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) adminListAllocations(c fiber.Ctx) error {
	list, err := s.games.ListAllocations(c.Context(), currentUser(c), strings.Clone(c.Query("node")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"allocations": allocList(list)})
}

func (s *panel) adminCreateAllocations(c fiber.Ctx) error {
	var in struct {
		NodeID string `json:"node_id"`
		IP     string `json:"ip"`
		Ports  string `json:"ports"`
		Notes  string `json:"notes"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	list, err := s.games.CreateAllocations(c.Context(), currentUser(c), in.NodeID, in.IP, in.Ports, in.Notes)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"allocations": allocList(list)})
}

func (s *panel) adminDeleteAllocation(c fiber.Ctx) error {
	id := strings.Clone(c.Params("aid"))
	if err := s.games.DeleteAllocation(c.Context(), currentUser(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) adminPatchAllocation(c fiber.Ctx) error {
	var in struct {
		Alias string `json:"alias"`
		Notes string `json:"notes"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.games.SetAllocationAlias(c.Context(), currentUser(c), strings.Clone(c.Params("aid")), in.Alias, in.Notes); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) gameRoutes(authed fiber.Router) {
	if s.games == nil {
		return
	}
	authed.Get("/blueprints", s.listBlueprints)
	authed.Get("/blueprints/:bp", s.getBlueprint)
	authed.Get("/blueprints/:bp/versions/:env", s.blueprintVersions)
	authed.Post("/games", s.requirePerm(domain.PermBotsCreate), s.createGameServer)
	authed.Get("/bots/:id/game", s.getGame)
	authed.Put("/bots/:id/game/variables", s.putGameVariables)
	authed.Post("/bots/:id/game/reinstall", s.reinstallGame)
	authed.Put("/bots/:id/game/image", s.putGameImage)
	authed.Post("/bots/:id/game/upgrade", s.upgradeGame)
	authed.Get("/bots/:id/game/query", s.queryGame)
	authed.Get("/bots/:id/game/addons", s.addonSearch)
	authed.Get("/bots/:id/game/addons/:project/versions", s.addonVersions)
	authed.Post("/bots/:id/game/addons", s.addonInstall)
	authed.Post("/bots/:id/allocations", s.addGameAllocation)
	authed.Delete("/bots/:id/allocations/:aid", s.removeGameAllocation)
	authed.Put("/bots/:id/allocations/:aid/primary", s.primaryGameAllocation)

	authed.Post("/admin/blueprints", s.requirePerm(domain.PermBlueprintsManage), s.adminImportBlueprint)
	authed.Post("/admin/blueprints/egg-preview", s.requirePerm(domain.PermBlueprintsManage), s.adminEggPreview)
	authed.Get("/admin/blueprints/:bp/export", s.requirePerm(domain.PermBlueprintsManage), s.adminExportBlueprint)
	authed.Get("/admin/blueprints/:bp/revisions", s.requirePerm(domain.PermBlueprintsManage), s.adminBlueprintRevisions)
	authed.Patch("/admin/blueprints/:bp", s.requirePerm(domain.PermBlueprintsManage), s.adminPatchBlueprint)
	authed.Delete("/admin/blueprints/:bp", s.requirePerm(domain.PermBlueprintsManage), s.adminDeleteBlueprint)
	authed.Get("/admin/allocations", s.requirePerm(domain.PermAllocations), s.adminListAllocations)
	authed.Post("/admin/allocations", s.requirePerm(domain.PermAllocations), s.adminCreateAllocations)
	authed.Patch("/admin/allocations/:aid", s.requirePerm(domain.PermAllocations), s.adminPatchAllocation)
	authed.Delete("/admin/allocations/:aid", s.requirePerm(domain.PermAllocations), s.adminDeleteAllocation)
}
