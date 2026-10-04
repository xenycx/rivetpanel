package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

type inviteDTO struct {
	ID          string `json:"id"`
	BotID       string `json:"bot_id"`
	BotName     string `json:"bot_name"`
	Permissions int    `json:"permissions"`
	CreatedBy   string `json:"created_by"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
}

func toInvite(v domain.Invite) inviteDTO {
	return inviteDTO{v.ID, v.BotID, v.BotName, v.Permissions, v.CreatorName, v.CreatedAtMS, v.ExpiresAtMS}
}

func (s *panel) listInvites(c fiber.Ctx) error {
	vs, err := s.bots.ListInvites(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	out := make([]inviteDTO, len(vs))
	for i, v := range vs {
		out[i] = toInvite(v)
	}
	return c.JSON(fiber.Map{"invites": out})
}

func (s *panel) createInvite(c fiber.Ctx) error {
	var in struct {
		Permissions int `json:"permissions"`
		Days        int `json:"expires_in_days"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	tok, v, err := s.bots.CreateInvite(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Permissions, in.Days)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"token": tok, "path": "/invite#" + tok, "info": toInvite(v)})
}

func (s *panel) deleteInvite(c fiber.Ctx) error {
	if err := s.bots.RevokeInvite(c.Context(), currentUser(c), strings.Clone(c.Params("id")), strings.Clone(c.Params("iid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// The token travels in POST bodies (the link keeps it in the URL fragment,
// which browsers never send to the server or put in Referer headers).
func (s *panel) previewInvite(c fiber.Ctx) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.bots.PreviewInvite(c.Context(), currentUser(c), in.Token)
	if err != nil {
		return err
	}
	return c.JSON(toInvite(v))
}

func (s *panel) acceptInvite(c fiber.Ctx) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	v, err := s.bots.AcceptInvite(c.Context(), currentUser(c), in.Token)
	if err != nil {
		return err
	}
	return c.JSON(toInvite(v))
}
