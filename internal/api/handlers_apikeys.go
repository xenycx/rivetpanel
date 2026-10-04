package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type apiKeyDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	CreatedAtMS  int64  `json:"created_at_ms"`
	LastUsedAtMS *int64 `json:"last_used_at_ms"`
	ExpiresAtMS  *int64 `json:"expires_at_ms"`
}

func (s *panel) sftpInfo(c fiber.Ctx) error {
	if s.sftp == nil {
		return c.JSON(fiber.Map{"enabled": false})
	}
	return c.JSON(fiber.Map{"enabled": true, "port": s.sftp.Port, "fingerprint": s.sftp.Fingerprint})
}

func (s *panel) listAPIKeys(c fiber.Ctx) error {
	ks, err := s.auth.ListAPIKeys(c.Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	out := make([]apiKeyDTO, len(ks))
	for i, k := range ks {
		out[i] = apiKeyDTO{k.ID, k.Name, k.Prefix, k.CreatedAtMS, k.LastUsedAtMS, k.ExpiresAtMS}
	}
	return c.JSON(fiber.Map{"keys": out})
}

// createAPIKey returns the plaintext key exactly once.
func (s *panel) createAPIKey(c fiber.Ctx) error {
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	plain, k, err := s.auth.CreateAPIKey(c.Context(), currentUser(c).ID, in.Name, time.Duration(in.ExpiresInDays)*24*time.Hour)
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"key": plain, "info": apiKeyDTO{k.ID, k.Name, k.Prefix, k.CreatedAtMS, k.LastUsedAtMS, k.ExpiresAtMS}})
}

func (s *panel) deleteAPIKey(c fiber.Ctx) error {
	if err := s.auth.DeleteAPIKey(c.Context(), currentUser(c).ID, strings.Clone(c.Params("id"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
