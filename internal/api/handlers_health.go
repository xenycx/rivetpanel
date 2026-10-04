package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type alertPrefsDTO struct {
	Crash          bool `json:"crash"`
	Deploy         bool `json:"deploy"`
	Backup         bool `json:"backup"`
	Recovery       bool `json:"recovery"`
	HeartbeatAfter int  `json:"heartbeat_after_s"`
}

func toPrefs(p domain.AlertPrefs) alertPrefsDTO {
	return alertPrefsDTO{p.Crash, p.Deploy, p.Backup, p.Recovery, p.HeartbeatAfter}
}

func (s *panel) getHealth(c fiber.Ctx) error {
	v, err := s.health.Get(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"state": v.State, "last_seen_at_ms": v.LastSeenAtMS, "ready": v.Ready,
		"alerts": toPrefs(v.Prefs), "webhook": v.Webhook})
}

func (s *panel) putAlerts(c fiber.Ctx) error {
	var in alertPrefsDTO
	if err := decode(c, &in); err != nil {
		return err
	}
	p, err := s.health.SetPrefs(c.Context(), currentUser(c), strings.Clone(c.Params("id")),
		domain.AlertPrefs{Crash: in.Crash, Deploy: in.Deploy, Backup: in.Backup, Recovery: in.Recovery, HeartbeatAfter: in.HeartbeatAfter})
	if err != nil {
		return err
	}
	return c.JSON(toPrefs(p))
}

func (s *panel) testAlert(c fiber.Ctx) error {
	if err := s.health.Test(c.Context(), currentUser(c), strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type healthProbeDTO struct {
	Kind                 string  `json:"kind"`
	HostPort             int     `json:"host_port"`
	Path                 string  `json:"path"`
	IntervalSeconds      int     `json:"interval_s"`
	TimeoutMS            int     `json:"timeout_ms"`
	FailureThreshold     int     `json:"failure_threshold"`
	SuccessThreshold     int     `json:"success_threshold"`
	StartupGraceSeconds  int     `json:"startup_grace_s"`
	RestartUnhealthy     bool    `json:"restart_unhealthy"`
	Status               string  `json:"status"`
	ConsecutiveFailures  int     `json:"consecutive_failures"`
	ConsecutiveSuccesses int     `json:"consecutive_successes"`
	LastCheckedAtMS      *int64  `json:"last_checked_at_ms"`
	LastError            *string `json:"last_error"`
}

func toHealthProbe(p domain.HealthProbe) healthProbeDTO {
	return healthProbeDTO{p.Kind, p.HostPort, p.Path, p.IntervalSeconds, p.TimeoutMS, p.FailureThreshold, p.SuccessThreshold, p.StartupGraceSeconds, p.RestartUnhealthy, p.Status, p.ConsecutiveFailures, p.ConsecutiveSuccesses, p.LastCheckedAtMS, p.LastError}
}

func (s *panel) getHealthProbe(c fiber.Ctx) error {
	p, err := s.health.GetProbe(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(toHealthProbe(p))
}

func (s *panel) putHealthProbe(c fiber.Ctx) error {
	var in healthProbeDTO
	if err := decode(c, &in); err != nil {
		return err
	}
	p, err := s.health.SetProbe(c.Context(), currentUser(c), strings.Clone(c.Params("id")), domain.HealthProbe{Kind: in.Kind, HostPort: in.HostPort, Path: in.Path, IntervalSeconds: in.IntervalSeconds, TimeoutMS: in.TimeoutMS, FailureThreshold: in.FailureThreshold, SuccessThreshold: in.SuccessThreshold, StartupGraceSeconds: in.StartupGraceSeconds, RestartUnhealthy: in.RestartUnhealthy})
	if err != nil {
		return err
	}
	return c.JSON(toHealthProbe(p))
}

func (s *panel) capacity(c fiber.Ctx) error {
	u := currentUser(c)
	cp, err := s.bots.Capacity(c.Context(), u)
	if err != nil {
		return err
	}
	out := fiber.Map{"bots": cp.Bots, "max_bots": cp.MaxBots, "memory_bytes": cp.Memory, "max_memory_bytes": cp.MaxMemory,
		"exempt": cp.Exempt, "build_memory_bytes": s.buildMemory}
	if u.IsAdmin() {
		out["node"] = fiber.Map{"running": cp.NodeRunning, "reserved_bytes": cp.NodeReserved, "budget_bytes": cp.NodeBudget}
	}
	return c.JSON(out)
}
