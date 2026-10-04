package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// In-panel notifications: each account reads and changes only its own
// inbox (the store queries are keyed by the signed-in account). Browser
// sessions only (see clientSessionOnly).

type notificationDTO struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Link        string `json:"link"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ReadAtMS    *int64 `json:"read_at_ms"`
}

func (s *panel) notificationRoutes(authed fiber.Router) {
	authed.Get("/notifications", s.listNotifications)
	authed.Get("/notifications/unread", s.unreadNotifications)
	authed.Post("/notifications/read", s.readNotifications)
	authed.Post("/notifications/clear", s.clearNotifications)
	authed.Delete("/notifications/:nid", s.deleteNotification)
	authed.Get("/me/notification-prefs", s.getNotificationPrefs)
	authed.Put("/me/notification-prefs", s.putNotificationPrefs)
}

func (s *panel) listNotifications(c fiber.Ctx) error {
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	ns, unread, err := s.notifications.List(c.Context(), currentUser(c), before, c.Query("unread") == "1", limit)
	if err != nil {
		return err
	}
	out := make([]notificationDTO, len(ns))
	for i, n := range ns {
		out[i] = notificationDTO{n.ID, n.Category, n.Title, n.Body, n.Link, n.CreatedAtMS, n.ReadAtMS}
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"notifications": out, "unread": unread})
}

func (s *panel) unreadNotifications(c fiber.Ctx) error {
	n, err := s.notifications.Unread(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"unread": n})
}

func (s *panel) readNotifications(c fiber.Ctx) error {
	var in struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	n, err := s.notifications.MarkRead(c.Context(), currentUser(c), in.IDs, in.All)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"marked": n})
}

func (s *panel) clearNotifications(c fiber.Ctx) error {
	n, err := s.notifications.ClearRead(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deleted": n})
}

func (s *panel) deleteNotification(c fiber.Ctx) error {
	if err := s.notifications.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("nid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) getNotificationPrefs(c fiber.Ctx) error {
	ps, err := s.notifications.Prefs(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"categories": ps, "email_available": s.mail != nil && s.mail.Enabled(c.Context())})
}

func (s *panel) putNotificationPrefs(c fiber.Ctx) error {
	var in struct {
		Prefs []struct {
			Category string `json:"category"`
			InPanel  bool   `json:"in_panel"`
			Email    bool   `json:"email"`
		} `json:"prefs"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	ps := make([]domain.NotificationPref, len(in.Prefs))
	for i, p := range in.Prefs {
		ps[i] = domain.NotificationPref{Category: p.Category, InPanel: p.InPanel, Email: p.Email}
	}
	if err := s.notifications.SetPrefs(c.Context(), currentUser(c), ps); err != nil {
		return err
	}
	return s.getNotificationPrefs(c)
}
