package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// API clients: bearer credentials for the whole session API, limited to a
// subset of the creator's permissions and optionally to some bots and
// workspaces. Managed with a browser session only (see clientSessionOnly).

type apiClientDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	Permissions  []string `json:"permissions"`
	BotIDs       []string `json:"bot_ids"`
	WorkspaceIDs []string `json:"workspace_ids"`
	CreatedAtMS  int64    `json:"created_at_ms"`
	LastUsedAtMS *int64   `json:"last_used_at_ms"`
	ExpiresAtMS  *int64   `json:"expires_at_ms"`
	OwnerID      string   `json:"owner_id"`
	OwnerEmail   string   `json:"owner_email"`
}

func toAPIClient(c domain.APIClient) apiClientDTO {
	return apiClientDTO{c.ID, c.Name, c.Prefix, nonNil(c.Permissions), c.BotIDs, c.WorkspaceIDs, c.CreatedAtMS, c.LastUsedAtMS, c.ExpiresAtMS, c.UserID, c.OwnerEmail}
}

func (s *panel) apiClientRoutes(authed fiber.Router) {
	authed.Get("/me/api-clients", s.listAPIClients)
	authed.Post("/me/api-clients", s.requirePerm(domain.PermAPIKeys), s.createAPIClient)
	authed.Delete("/me/api-clients/:id", s.revokeAPIClient)
	authed.Get("/admin/api-clients", s.requirePerm(domain.PermUsersView), s.adminListAPIClients)
	authed.Delete("/admin/api-clients/:id", s.requirePerm(domain.PermUsersManage), s.adminRevokeAPIClient)
}

// clientLimit caps requests per API client (browser sessions are not
// affected).
func clientLimit() fiber.Handler {
	return limiter.New(limiter.Config{
		Max: 600, Expiration: time.Minute,
		Next:         func(c fiber.Ctx) bool { return currentUser(c).Client == nil },
		KeyGenerator: func(c fiber.Ctx) string { return "client:" + currentUser(c).Client.ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "rate limit: 600 requests per minute per API client")
		},
	})
}

func (s *panel) listAPIClients(c fiber.Ctx) error {
	u := currentUser(c)
	cs, err := s.clients.List(c.Context(), u)
	if err != nil {
		return err
	}
	out := make([]apiClientDTO, len(cs))
	for i, x := range cs {
		out[i] = toAPIClient(x)
	}
	// The permissions this account could give a new client right now.
	grantable := []domain.PermissionInfo{}
	for _, p := range domain.Permissions {
		if u.Can(p.Name) {
			grantable = append(grantable, p)
		}
	}
	return c.JSON(fiber.Map{"clients": out, "grantable": grantable})
}

func (s *panel) createAPIClient(c fiber.Ctx) error {
	var in struct {
		Name         string   `json:"name"`
		Permissions  []string `json:"permissions"`
		BotIDs       []string `json:"bot_ids"`
		WorkspaceIDs []string `json:"workspace_ids"`
		LifeDays     int      `json:"expires_in_days"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	plain, cl, err := s.clients.Create(c.Context(), currentUser(c), service.APIClientInput{Name: in.Name, Permissions: in.Permissions,
		BotIDs: in.BotIDs, WorkspaceIDs: in.WorkspaceIDs, LifeDays: in.LifeDays})
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, clientAuditTarget(cl))
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"token": plain, "info": toAPIClient(cl)})
}

func (s *panel) revokeAPIClient(c fiber.Ctx) error {
	cl, err := s.clients.Revoke(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, cl.Name)
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) adminListAPIClients(c fiber.Ctx) error {
	cs, err := s.clients.ListAll(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]apiClientDTO, len(cs))
	for i, x := range cs {
		out[i] = toAPIClient(x)
	}
	return c.JSON(fiber.Map{"clients": out})
}

func (s *panel) adminRevokeAPIClient(c fiber.Ctx) error {
	cl, err := s.clients.AdminRevoke(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, cl.Name+" ("+cl.OwnerEmail+")")
	return c.SendStatus(fiber.StatusNoContent)
}

// clientAuditTarget names a client and what it carries (never the token).
func clientAuditTarget(cl domain.APIClient) string {
	t := cl.Name + ": " + strings.Join(cl.Permissions, ", ")
	if cl.Scoped() {
		t += " (limited to " + strconv.Itoa(len(cl.BotIDs)) + " bots, " + strconv.Itoa(len(cl.WorkspaceIDs)) + " workspaces)"
	}
	if len(t) > 400 {
		t = t[:397] + "..."
	}
	return t
}
