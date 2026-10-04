package api

import (
	"errors"
	"os"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// logArchiveRoutes: the archive job's settings (read with settings.manage or
// system.view, changed with settings.manage), the panel log's days
// (system.view, like the panel log viewer) and a bot's days (the same access
// as its console).
func (s *panel) logArchiveRoutes(authed fiber.Router) {
	if s.logArchive == nil {
		return
	}
	authed.Get("/admin/log-archive", s.requireAnyPerm(domain.PermSettingsManage, domain.PermSystemView), s.getLogArchive)
	authed.Put("/admin/log-archive", s.requirePerm(domain.PermSettingsManage), s.putLogArchive)
	authed.Post("/admin/log-archive/run", s.requirePerm(domain.PermSettingsManage), s.runLogArchive)
	authed.Get("/admin/logs/days", s.requirePerm(domain.PermSystemView), s.panelLogDays)
	authed.Get("/admin/logs/days/:date", s.requirePerm(domain.PermSystemView), s.panelLogDay)
	authed.Get("/bots/:id/logs/days", s.requirePerm(domain.PermBotsConsole), s.botLogDays)
	authed.Get("/bots/:id/logs/days/:date", s.requirePerm(domain.PermBotsConsole), s.botLogDay)
}

func (s *panel) getLogArchive(c fiber.Ctx) error {
	v, err := s.logArchive.View(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) putLogArchive(c fiber.Ctx) error {
	var in service.LogSettingsInput
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.logArchive.Update(c.Context(), currentUser(c), in)
	if err != nil {
		return err
	}
	return c.JSON(v)
}

func (s *panel) runLogArchive(c fiber.Ctx) error {
	r, err := s.logArchive.RunNow(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(r)
}

func (s *panel) panelLogDays(c fiber.Ctx) error {
	days, err := s.logArchive.PanelLogDays()
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"days": days, "current_day": s.logArchive.CurrentDay()})
}

func (s *panel) botLogDays(c fiber.Ctx) error {
	days, err := s.logArchive.BotLogDays(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"days": days, "current_day": s.logArchive.CurrentDay()})
}

// archivedParam: "1" the archive, "0" the live file, absent the archive
// when there is one and the live file otherwise.
func archivedParam(c fiber.Ctx) (archived, auto bool, err error) {
	switch c.Query("archived") {
	case "":
		return true, true, nil
	case "1", "true":
		return true, false, nil
	case "0", "false":
		return false, false, nil
	}
	return false, false, domain.Invalid("archived must be 1 or 0")
}

func (s *panel) panelLogDay(c fiber.Ctx) error {
	day := strings.Clone(c.Params("date"))
	return s.sendLogDay(c, "rivetpanel", day, func(archived bool) (*os.File, int64, error) {
		return s.logArchive.OpenPanelLogDay(day, archived)
	})
}

func (s *panel) botLogDay(c fiber.Ctx) error {
	id, day := strings.Clone(c.Params("id")), strings.Clone(c.Params("date"))
	name := "server-" + id
	if len(id) >= 8 {
		name = "server-" + id[:8]
	}
	return s.sendLogDay(c, name, day, func(archived bool) (*os.File, int64, error) {
		return s.logArchive.OpenBotLogDay(c.Context(), currentUser(c), id, day, archived)
	})
}

func (s *panel) sendLogDay(c fiber.Ctx, name, day string, open func(bool) (*os.File, int64, error)) error {
	archived, auto, err := archivedParam(c)
	if err != nil {
		return err
	}
	f, size, err := open(archived)
	if auto && errors.Is(err, domain.ErrNotFound) {
		archived = false
		f, size, err = open(false)
	}
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	if archived {
		c.Set(fiber.HeaderContentType, "application/gzip")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+`-`+day+`.log.gz"`)
	} else {
		c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+`-`+day+`.log"`)
	}
	return c.SendStream(f, int(size)) // closes f when done
}
