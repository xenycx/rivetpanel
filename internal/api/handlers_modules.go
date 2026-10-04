package api

import "github.com/gofiber/fiber/v3"

func (s *panel) listModules(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"modules": s.modules.List()})
}
