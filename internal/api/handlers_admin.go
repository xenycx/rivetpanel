package api

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"
)

// getDiagnostics runs the health report (administrators only). With
// ?download=1 it is served as a JSON file to attach to a support request; the
// report contains only an allowlist of facts, never secrets or logs.
func (s *panel) getDiagnostics(c fiber.Ctx) error {
	r := s.diagnostics(c.Context())
	c.Set(fiber.HeaderCacheControl, "no-store")
	if c.Query("download") == "1" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		c.Set(fiber.HeaderContentType, "application/json")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="rivetpanel-diagnostics-`+time.Now().UTC().Format("20060102-150405")+`.json"`)
		return c.Send(b)
	}
	return c.JSON(r)
}
