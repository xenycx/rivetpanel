package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// quotaDTO is an account's quota: null fields use the panel default.
type quotaDTO struct {
	MaxApps     *int64 `json:"max_apps"`
	MaxBots     *int64 `json:"max_bots"`
	MaxGames    *int64 `json:"max_games"`
	MaxWebApps  *int64 `json:"max_web_apps"`
	MaxSites    *int64 `json:"max_sites"`
	MemoryBytes *int64 `json:"memory_bytes"`
}

func toQuota(q domain.UserQuota) quotaDTO {
	return quotaDTO{q.MaxApps, q.MaxBots, q.MaxGames, q.MaxWebApps, q.MaxSites, q.MemoryBytes}
}

func toUsage(u domain.OwnerUsage) fiber.Map {
	return fiber.Map{"apps": u.Apps, "bots": u.Bots, "games": u.Games, "web_apps": u.Web, "sites": u.Sites, "memory_bytes": u.Memory}
}

func (s *panel) siteDefault() int {
	if s.sites == nil {
		return 0
	}
	return s.sites.MaxPerUser
}

func (s *panel) quotaView(v service.QuotaView) fiber.Map {
	out := fiber.Map{"quota": toQuota(v.Quota), "usage": toUsage(v.Usage), "exempt": v.Admin,
		"defaults": fiber.Map{"max_apps": v.Defaults.MaxApps, "max_sites": v.Defaults.MaxSites, "memory_bytes": v.Defaults.MemoryBytes}}
	if v.Quota.UpdatedAtMS != 0 {
		out["updated_at_ms"] = v.Quota.UpdatedAtMS
	}
	return out
}

func (s *panel) adminGetUserQuota(c fiber.Ctx) error {
	v, err := s.bots.UserQuota(c.Context(), currentUser(c), strings.Clone(c.Params("id")), s.siteDefault())
	if err != nil {
		return err
	}
	return c.JSON(s.quotaView(v))
}

func (s *panel) adminPutUserQuota(c fiber.Ctx) error {
	var in quotaDTO
	if err := decode(c, &in); err != nil {
		return err
	}
	id, actor := strings.Clone(c.Params("id")), currentUser(c)
	q, err := s.bots.SetUserQuota(c.Context(), actor, id, service.QuotaInput{MaxApps: in.MaxApps, MaxBots: in.MaxBots,
		MaxGames: in.MaxGames, MaxWebApps: in.MaxWebApps, MaxSites: in.MaxSites, MemoryBytes: in.MemoryBytes})
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, service.QuotaSummary(q))
	v, err := s.bots.UserQuota(c.Context(), actor, id, s.siteDefault())
	if err != nil {
		return err
	}
	return c.JSON(s.quotaView(v))
}

func (s *panel) adminDeleteUserQuota(c fiber.Ctx) error {
	if err := s.bots.ClearUserQuota(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	c.Locals(keyAuditTarget, "panel defaults")
	return c.SendStatus(fiber.StatusNoContent)
}

// capacityLimits reports the limits that apply to the caller; null is
// unlimited.
func capacityLimits(l service.AccountLimits) fiber.Map {
	n := func(v int64) any {
		if v < 0 {
			return nil
		}
		return v
	}
	return fiber.Map{"apps": n(l.Apps.Max), "bots": n(l.Bots.Max), "games": n(l.Games.Max), "web_apps": n(l.Web.Max),
		"sites": n(l.Sites.Max), "memory_bytes": n(l.Memory.Max)}
}
