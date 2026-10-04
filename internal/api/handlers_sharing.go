package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

type subUserDTO struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	Permissions int    `json:"permissions"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

func (s *panel) listSubUsers(c fiber.Ctx) error {
	us, err := s.bots.ListSubUsers(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	out := make([]subUserDTO, len(us))
	for i, u := range us {
		out[i] = subUserDTO{u.UserID, u.Email, u.Permissions, u.CreatedAtMS}
	}
	return c.JSON(fiber.Map{"users": out})
}

func (s *panel) shareBot(c fiber.Ctx) error {
	var in struct {
		Email       string `json:"email"`
		Permissions int    `json:"permissions"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u, err := s.bots.ShareBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Email, in.Permissions)
	if err != nil {
		return err
	}
	return c.JSON(subUserDTO{u.UserID, u.Email, u.Permissions, u.CreatedAtMS})
}

func (s *panel) unshareBot(c fiber.Ctx) error {
	if err := s.bots.UnshareBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("uid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) transferBot(c fiber.Ctx) error {
	var in struct {
		Email      string `json:"email"`
		KeepAccess bool   `json:"keep_access"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	removed, err := s.bots.TransferBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Email, in.KeepAccess)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"repository_unlinked": removed})
}
