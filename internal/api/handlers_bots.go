package api

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func (s *panel) listRuntimes(c fiber.Ctx) error {
	type rtDTO struct {
		ID          string   `json:"id"`
		DisplayName string   `json:"display_name"`
		ImageRef    string   `json:"image_ref"`
		DefaultArgv []string `json:"default_argv"`
		MemoryBytes int64    `json:"default_memory_bytes"`
		NanoCPUs    int64    `json:"default_nano_cpus"`
		PidsLimit   int64    `json:"default_pids_limit"`
		MinMemory   int64    `json:"min_memory_bytes"`
		BuildMemory int64    `json:"build_memory_bytes"`
		HasBuild    bool     `json:"has_build"`
	}
	var out []rtDTO
	for _, r := range s.catalog.List() {
		d := rtDTO{r.ID, r.DisplayName, r.ImageRef(), r.DefaultArgv, r.Defaults.MemoryBytes,
			r.Defaults.NanoCPUs, r.Defaults.PidsLimit, r.MinMemoryBytes, r.BuildMemoryBytes, len(r.BuildArgv) > 0}
		if d.HasBuild && d.BuildMemory == 0 {
			d.BuildMemory = s.buildMemory
		}
		out = append(out, d)
	}
	l := s.bots.Limits
	if l.PortMin == 0 && l.PortMax == 0 {
		l.PortMin, l.PortMax = 20000, 29999
	}
	// The server-side limits, so forms can show the valid range before submit.
	return c.JSON(fiber.Map{"runtimes": out, "limits": fiber.Map{
		"min_memory_bytes": l.MinMemoryBytes, "max_memory_bytes": l.MaxMemoryBytes,
		"min_nano_cpus": l.MinNanoCPUs, "max_nano_cpus": l.MaxNanoCPUs,
		"port_min": l.PortMin, "port_max": l.PortMax, "port_public_bind": l.PortPublicBind,
	}})
}

type githubIn struct {
	FullName   string `json:"full_name"`
	Branch     string `json:"branch"`
	RootDir    string `json:"root_dir"`
	AutoDeploy bool   `json:"auto_deploy"`
	// StartAfterDeploy (creation only) starts the bot once its first
	// deployment has put the files in place.
	StartAfterDeploy bool `json:"start_after_deploy"`
}

// addonIn accepts {"kind": "postgres"} with an optional memory limit.
type addonIn struct {
	Kind        string `json:"kind"`
	MemoryBytes int64  `json:"memory_bytes"`
}

func (s *panel) createBot(c fiber.Ctx) error {
	var in struct {
		Name        string   `json:"name"`
		Runtime     string   `json:"runtime"`
		Argv        []string `json:"argv"`
		MemoryBytes int64    `json:"memory_bytes"`
		NanoCPUs    int64    `json:"nano_cpus"`
		PidsLimit   int64    `json:"pids_limit"`
		TemplateID  *string  `json:"template_id"`
		WorkspaceID string   `json:"workspace_id"`
		// NodeID places the bot on a node (administrators; "" = this panel).
		NodeID string            `json:"node_id"`
		GitHub *githubIn         `json:"github"`
		Env    map[string]string `json:"env"`
		// BuildCommand replaces the runtime's build step; Addons attaches
		// databases (kinds from GET /addons).
		BuildCommand string    `json:"build_command"`
		Addons       []addonIn `json:"addons"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	ci := service.CreateBotInput{
		Name: in.Name, Runtime: in.Runtime, Argv: in.Argv, MemoryBytes: in.MemoryBytes,
		NanoCPUs: in.NanoCPUs, PidsLimit: in.PidsLimit, TemplateID: in.TemplateID, Env: in.Env, WorkspaceID: in.WorkspaceID,
		BuildCommand: in.BuildCommand, NodeID: in.NodeID,
	}
	for _, a := range in.Addons {
		ci.Addons = append(ci.Addons, service.AddonInput{Kind: a.Kind, MemoryBytes: a.MemoryBytes})
	}
	if in.GitHub != nil {
		if in.TemplateID != nil {
			return domain.Invalid("choose either a template or a GitHub repository")
		}
		if s.deploy == nil {
			return fiber.ErrNotFound
		}
		b, _, err := s.deploy.CreateFromGitHub(c.Context(), currentUser(c), ci,
			service.ConfigureInput{FullName: in.GitHub.FullName, Branch: in.GitHub.Branch, RootDir: in.GitHub.RootDir, AutoDeploy: in.GitHub.AutoDeploy},
			in.GitHub.StartAfterDeploy)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(s.viewBot(c, b))
	}
	b, err := s.bots.Create(c.Context(), currentUser(c), ci)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.viewBot(c, b))
}

// viewBot renders a bot with the caller's permissions.
func (s *panel) viewBot(c fiber.Ctx, b domain.Bot) botDTO {
	d := toBot(b)
	u := currentUser(c)
	d.Permissions = s.bots.Permissions(c.Context(), u, b)
	// Shared means access through a per-bot grant; teammates see workspace bots
	// as their own workspace's, not as shared.
	d.Shared = b.OwnerID != u.ID && !u.IsAdmin()
	if d.Shared {
		if role, _ := s.bots.Store.BotWorkspaceRole(c.Context(), b.ID, u.ID); role != "" {
			d.Shared = false
		}
	}
	d.Phase = phaseOf(b, s.runnerErr(c.Context()), s.bots.Notifier != nil)
	return d
}

// runnerErr reports whether the local runner can currently act (nil when it
// can, or when no readiness probe is wired).
func (s *panel) runnerErr(ctx context.Context) error {
	if s.runnerReady == nil {
		return nil
	}
	return s.runnerReady(ctx)
}

// listBots returns the caller's bots. Optional filters for scripts and large
// fleets: q (name contains), runtime, tag, and limit/offset paging; the
// response always carries the unpaged total.
func (s *panel) listBots(c fiber.Ctx) error {
	bs, err := s.bots.List(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	tags, favs, err := s.bots.TagsAndFavorites(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	q, rt, tag := strings.ToLower(c.Query("q")), c.Query("runtime"), c.Query("tag")
	out := make([]botDTO, 0, len(bs))
	for _, b := range bs {
		if (q != "" && !strings.Contains(strings.ToLower(b.Name), q)) || (rt != "" && b.Runtime != rt) || (tag != "" && !slices.Contains(tags[b.ID], tag)) {
			continue
		}
		d := s.viewBot(c, b)
		d.Tags, d.Favorite = tags[b.ID], favs[b.ID]
		if d.Tags == nil {
			d.Tags = []string{}
		}
		out = append(out, d)
	}
	total := len(out)
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return domain.Invalid("offset must be a non-negative number")
		}
		out = out[min(n, len(out)):]
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return domain.Invalid("limit must be between 1 and 1000")
		}
		out = out[:min(n, len(out))]
	}
	return c.JSON(fiber.Map{"bots": out, "total": total})
}

func (s *panel) getBot(c fiber.Ctx) error {
	b, err := s.bots.Get(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	d := s.viewBot(c, b)
	if tags, favs, err := s.bots.TagsAndFavorites(c.Context(), currentUser(c)); err == nil {
		d.Tags, d.Favorite = tags[b.ID], favs[b.ID]
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	return c.JSON(d)
}

func (s *panel) setTags(c fiber.Ctx) error {
	var in struct {
		Tags []string `json:"tags"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	tags, err := s.bots.SetTags(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Tags)
	if err != nil {
		return err
	}
	if tags == nil {
		tags = []string{}
	}
	return c.JSON(fiber.Map{"tags": tags})
}

func (s *panel) setFavorite(c fiber.Ctx) error {
	var in struct {
		Favorite bool `json:"favorite"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.bots.SetFavorite(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Favorite); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// batchBots applies one lifecycle action to several bots and reports each
// outcome; bots the caller may not control fail individually.
func (s *panel) batchBots(c fiber.Ctx) error {
	var in struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	ids := make([]string, len(in.IDs))
	for i, id := range in.IDs {
		ids[i] = strings.Clone(id)
	}
	res, err := s.bots.Batch(c.Context(), currentUser(c), strings.Clone(in.Action), ids)
	if err != nil {
		return err
	}
	type resDTO struct {
		BotID   string `json:"bot_id"`
		Name    string `json:"name"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	out := make([]resDTO, len(res))
	for i, r := range res {
		out[i] = resDTO{r.BotID, r.Name, r.OK, r.Message}
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"results": out})
}

func (s *panel) patchBot(c fiber.Ctx) error {
	var in struct {
		Name        *string   `json:"name"`
		Argv        *[]string `json:"argv"`
		MemoryBytes *int64    `json:"memory_bytes"`
		NanoCPUs    *int64    `json:"nano_cpus"`
		PidsLimit   *int64    `json:"pids_limit"`

		Runtime        *string   `json:"runtime"`
		Entrypoint     *[]string `json:"entrypoint"`
		NetworkEnabled *bool     `json:"network_enabled"`
		BandwidthKbps  *int64    `json:"bandwidth_kbps"`
		AutoBackup     *bool     `json:"auto_backup"`

		RestartPolicy           *string `json:"restart_policy"`
		RestartMaxAttempts      *int64  `json:"restart_max_attempts"`
		RestartBackoffInitialMS *int64  `json:"restart_backoff_initial_ms"`
		RestartBackoffMaxMS     *int64  `json:"restart_backoff_max_ms"`

		BuildCommand *string `json:"build_command"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.bots.Update(c.Context(), currentUser(c), strings.Clone(c.Params("id")), service.UpdateBotInput{
		Name: in.Name, Argv: in.Argv, MemoryBytes: in.MemoryBytes, NanoCPUs: in.NanoCPUs, PidsLimit: in.PidsLimit,
		Runtime: in.Runtime, Entrypoint: in.Entrypoint, NetworkEnabled: in.NetworkEnabled, BandwidthKbps: in.BandwidthKbps, AutoBackup: in.AutoBackup,
		RestartPolicy: in.RestartPolicy, RestartMaxAttempts: in.RestartMaxAttempts,
		RestartBackoffInitialMS: in.RestartBackoffInitialMS, RestartBackoffMaxMS: in.RestartBackoffMaxMS,
		BuildCommand: in.BuildCommand,
	})
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) deleteBot(c fiber.Ctx) error {
	if err := s.bots.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// maskedValue is returned in place of every secret value; it deliberately does
// not reflect the length of the real value.
const maskedValue = "********"

func (s *panel) listEnv(c fiber.Ctx) error {
	vs, err := s.bots.ListEnv(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	type envDTO struct {
		Name        string `json:"name"`
		Value       string `json:"value"`
		UpdatedAtMS int64  `json:"updated_at_ms"`
	}
	out := make([]envDTO, len(vs))
	for i, v := range vs {
		out[i] = envDTO{v.Name, maskedValue, v.UpdatedAtMS}
	}
	return c.JSON(fiber.Map{"vars": out})
}

func (s *panel) setEnv(c fiber.Ctx) error {
	var in struct {
		Vars map[string]string `json:"vars"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.bots.SetEnv(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Vars); err != nil {
		return err
	}
	return s.listEnv(c)
}

func (s *panel) deleteEnv(c fiber.Ctx) error {
	if err := s.bots.DeleteEnv(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("name"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) lifecycle(op func(*service.BotService, fiber.Ctx, string) (domain.Bot, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		b, err := op(s.bots, c, strings.Clone(c.Params("id")))
		if err != nil {
			return err
		}
		// 202: intent is persisted; the runner converges asynchronously.
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"bot_id": b.ID, "generation": b.Generation,
			"desired_state": b.DesiredState, "observed_state": b.ObservedState,
		})
	}
}

func (s *panel) setPorts(c fiber.Ctx) error {
	var in struct {
		Ports []portDTO `json:"ports"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	ports := make([]service.PortInput, len(in.Ports))
	for i, p := range in.Ports {
		ports[i] = service.PortInput{ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol, HostIP: p.HostIP}
	}
	b, err := s.bots.SetPorts(c.Context(), currentUser(c), strings.Clone(c.Params("id")), ports)
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

// revealEnv returns one secret's value on explicit request. POST + CSRF, never cached.
func (s *panel) revealEnv(c fiber.Ctx) error {
	v, err := s.bots.RevealEnv(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("name")))
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"value": v})
}
