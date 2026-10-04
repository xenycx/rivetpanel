package api

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/service"
)

// getEnvironment lists every RIVET_* variable with where its value comes
// from. Secret values are never returned.
func (s *panel) getEnvironment(c fiber.Ctx) error {
	v, err := s.env.View(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(v)
}

type environmentBody struct {
	Set   map[string]string `json:"set"`
	Unset []string          `json:"unset"`
}

// putEnvironment stores overrides; they apply at the next start.
func (s *panel) putEnvironment(c fiber.Ctx) error {
	var in environmentBody
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.env.Update(c.Context(), currentUser(c), service.PanelEnvChange{Set: in.Set, Unset: in.Unset}); err != nil {
		return err
	}
	return s.getEnvironment(c)
}

// restartPanel exits the process so its supervisor starts it with the stored
// overrides. Bot containers keep running.
func (s *panel) restartPanel(c fiber.Ctx) error {
	if err := s.env.RestartPanel(currentUser(c)); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"restarting": true})
}

// environmentNames lists the variable NAMES of a PUT body for the activity
// record (never the values).
func environmentNames(c fiber.Ctx) string {
	var in environmentBody
	if json.Unmarshal(c.Body(), &in) != nil {
		return ""
	}
	var names []string
	for n := range in.Set {
		names = append(names, n)
	}
	names = append(names, in.Unset...)
	sort.Strings(names)
	s := strings.Join(names, ", ")
	if len(s) > 300 {
		s = s[:297] + "..."
	}
	return s
}
