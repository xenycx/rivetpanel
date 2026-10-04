package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/service"
)

func enrollAgent(enrollment *service.AgentEnrollmentService) fiber.Handler {
	return func(c fiber.Ctx) error {
		var in struct {
			Token string `json:"token"`
			CSR   string `json:"csr"`
		}
		if err := decode(c, &in); err != nil {
			return err
		}
		cert, err := enrollment.Enroll(c.Context(), in.Token, in.CSR)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"node_id":       cert.NodeID,
			"certificate":   cert.Certificate,
			"ca":            cert.CA,
			"serial":        cert.Serial,
			"expires_at_ms": cert.ExpiresAtMS,
			"agent_address": enrollment.AgentAddress,
		})
	}
}
