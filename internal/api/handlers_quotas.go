package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// quotaDTO is a workspace's own limits: null fields use the panel default.
type quotaDTO struct {
	MaxApps     *int64 `json:"max_apps"`
	MaxBots     *int64 `json:"max_bots"`
	MaxGames    *int64 `json:"max_games"`
	MaxWebApps  *int64 `json:"max_web_apps"`
	MaxSites    *int64 `json:"max_sites"`
	MemoryBytes *int64 `json:"memory_bytes"`
	NanoCPUs    *int64 `json:"nano_cpus"`
}

func toQuota(q domain.WorkspaceQuota) quotaDTO {
	return quotaDTO{q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes, q.NanoCPUs}
}

func toUsage(u domain.WorkspaceUsage) fiber.Map {
	return fiber.Map{"apps": u.Apps, "bots": u.Bots, "games": u.Games, "web_apps": u.Web, "sites": u.Sites,
		"memory_bytes": u.Memory, "nano_cpus": u.CPU}
}

// unlimited reports a limit as null when it is unlimited (< 0).
func unlimited(v int64) any {
	if v < 0 {
		return nil
	}
	return v
}

// capacityLimits reports the limits that apply to a workspace; null is
// unlimited.
func capacityLimits(l service.WorkspaceLimits) fiber.Map {
	return fiber.Map{"apps": unlimited(l.Apps.Max), "bots": unlimited(l.Bots.Max), "games": unlimited(l.Games.Max),
		"web_apps": unlimited(l.Web.Max), "sites": unlimited(l.Sites.Max), "memory_bytes": unlimited(l.Memory.Max),
		"nano_cpus": unlimited(l.CPU.Max)}
}

func defaultLimits(d service.LimitDefaults) fiber.Map {
	return fiber.Map{"max_apps": unlimited(d.Apps), "max_bots": unlimited(d.Bots), "max_games": unlimited(d.Games),
		"max_web_apps": unlimited(d.Web), "max_sites": unlimited(d.Sites), "memory_bytes": unlimited(d.Memory),
		"nano_cpus": unlimited(d.CPU), "team_workspaces": d.TeamWorkspaces}
}

// defaultSources says where each default allowance value comes from, keyed
// like defaultLimits.
func defaultSources(d service.LimitDefaults) fiber.Map {
	out := fiber.Map{}
	for k, v := range d.Source {
		out[strings.TrimPrefix(k, "limit_")] = v
	}
	return out
}

// limitSources says where each value of a workspace's allowance comes from:
// "workspace" (its own allowance), else the default's source ("panel",
// "env" or "builtin").
func limitSources(q domain.WorkspaceQuota, d service.LimitDefaults) fiber.Map {
	out := fiber.Map{}
	for _, f := range []struct {
		key, setting string
		own          *int64
	}{{"apps", service.SetLimitApps, q.MaxApps}, {"bots", service.SetLimitBots, q.MaxBots}, {"games", service.SetLimitGames, q.MaxGames},
		{"web_apps", service.SetLimitWeb, q.MaxWebApps}, {"sites", service.SetLimitSites, q.MaxSites},
		{"memory_bytes", service.SetLimitMemory, q.MemoryBytes}, {"nano_cpus", service.SetLimitCPU, q.NanoCPUs}} {
		if f.own != nil {
			out[f.key] = "workspace"
		} else {
			out[f.key] = d.Source[f.setting]
		}
	}
	return out
}

// quotaView is the read view of a workspace's allowance. Exemption belongs
// to the acting account, not the workspace (see the capacity handler), so a
// read view reports none.
func quotaView(v service.QuotaView) fiber.Map {
	out := fiber.Map{"workspace": fiber.Map{"id": v.Workspace.ID, "name": v.Workspace.Name, "personal": v.Workspace.Personal},
		"quota": toQuota(v.Quota), "usage": toUsage(v.Usage), "limits": capacityLimits(v.Limits),
		"sources": limitSources(v.Quota, v.Defaults), "defaults": defaultLimits(v.Defaults), "default_sources": defaultSources(v.Defaults)}
	if v.Quota.UpdatedAtMS != 0 {
		out["updated_at_ms"] = v.Quota.UpdatedAtMS
	}
	return out
}

func (s *panel) adminGetWorkspaceQuota(c fiber.Ctx) error {
	v, err := s.bots.WorkspaceQuota(c.Context(), currentUser(c), strings.Clone(c.Params("wid")))
	if err != nil {
		return err
	}
	return c.JSON(quotaView(v))
}

func (s *panel) adminPutWorkspaceQuota(c fiber.Ctx) error {
	var in quotaDTO
	if err := decode(c, &in); err != nil {
		return err
	}
	id, actor := strings.Clone(c.Params("wid")), currentUser(c)
	q, err := s.bots.SetWorkspaceQuota(c.Context(), actor, id, service.QuotaInput{MaxApps: in.MaxApps, MaxBots: in.MaxBots,
		MaxGames: in.MaxGames, MaxWebApps: in.MaxWebApps, MaxSites: in.MaxSites, MemoryBytes: in.MemoryBytes, NanoCPUs: in.NanoCPUs})
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, id+" "+service.QuotaSummary(q))
	v, err := s.bots.WorkspaceQuota(c.Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(quotaView(v))
}

func (s *panel) adminDeleteWorkspaceQuota(c fiber.Ctx) error {
	id := strings.Clone(c.Params("wid"))
	if err := s.bots.ClearWorkspaceQuota(c.Context(), currentUser(c), id); err != nil {
		return err
	}
	c.Locals(keyAuditTarget, id+" panel defaults")
	return c.SendStatus(fiber.StatusNoContent)
}

// workspaceLimits is a workspace's limits and usage for its members.
func (s *panel) workspaceLimits(c fiber.Ctx) error {
	v, err := s.bots.WorkspaceLimitsFor(c.Context(), currentUser(c), strings.Clone(c.Params("wid")))
	if err != nil {
		return err
	}
	out := quotaView(v)
	delete(out, "defaults")
	delete(out, "default_sources")
	return c.JSON(out)
}

// limitsDTO replaces the default limits; null removes a panel setting (the
// environment, else no limit, applies again).
type limitsDTO struct {
	quotaDTO
	TeamWorkspaces *int64 `json:"team_workspaces"`
}

func (s *panel) getLimitDefaults(c fiber.Ctx) error {
	d, err := s.bots.LimitDefaults(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"limits": defaultLimits(d), "source": defaultSources(d), "builtin_team_workspaces": service.MaxOwnedWorkspaces})
}

func (s *panel) putLimitDefaults(c fiber.Ctx) error {
	var in limitsDTO
	if err := decode(c, &in); err != nil {
		return err
	}
	err := s.bots.SetLimitDefaults(c.Context(), currentUser(c), service.LimitsInput{MaxApps: in.MaxApps, MaxBots: in.MaxBots,
		MaxGames: in.MaxGames, MaxWebApps: in.MaxWebApps, MaxSites: in.MaxSites, MemoryBytes: in.MemoryBytes,
		NanoCPUs: in.NanoCPUs, TeamWorkspaces: in.TeamWorkspaces})
	if err != nil {
		return err
	}
	return s.getLimitDefaults(c)
}
