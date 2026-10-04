package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type roleDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	System      bool     `json:"system"`
	Users       int      `json:"users"`
	CreatedAtMS int64    `json:"created_at_ms"`
	UpdatedAtMS int64    `json:"updated_at_ms"`
}

func toRole(r domain.Role) roleDTO {
	p := r.Permissions
	if p == nil {
		p = []string{}
	}
	return roleDTO{r.ID, r.Name, r.Description, p, r.System, r.Users, r.CreatedAtMS, r.UpdatedAtMS}
}

func (s *panel) roleRoutes(authed fiber.Router) {
	authed.Get("/admin/permissions", s.requireAnyPerm(domain.PermUsersView, domain.PermUsersManage, domain.PermRolesManage, domain.PermSettingsManage), s.listPermissions)
	authed.Get("/admin/roles", s.requireAnyPerm(domain.PermUsersView, domain.PermUsersManage, domain.PermRolesManage), s.listRoles)
	authed.Post("/admin/roles", s.requirePerm(domain.PermRolesManage), s.createRole)
	authed.Patch("/admin/roles/:rid", s.requirePerm(domain.PermRolesManage), s.patchRole)
	authed.Delete("/admin/roles/:rid", s.requirePerm(domain.PermRolesManage), s.deleteRole)
}

func (s *panel) listPermissions(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"permissions": domain.Permissions})
}

func (s *panel) listRoles(c fiber.Ctx) error {
	rs, err := s.auth.ListRoles(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]roleDTO, len(rs))
	for i, r := range rs {
		out[i] = toRole(r)
	}
	return c.JSON(fiber.Map{"roles": out})
}

type roleBody struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (b roleBody) input() service.RoleInput {
	return service.RoleInput{Name: b.Name, Description: b.Description, Permissions: b.Permissions}
}

func (s *panel) createRole(c fiber.Ctx) error {
	var in roleBody
	if err := decode(c, &in); err != nil {
		return err
	}
	r, err := s.auth.CreateRole(c.Context(), currentUser(c), in.input())
	if err != nil {
		return err
	}
	s.auditRole(c, "admin.role_create", r)
	return c.Status(fiber.StatusCreated).JSON(toRole(r))
}

func (s *panel) patchRole(c fiber.Ctx) error {
	var in roleBody
	if err := decode(c, &in); err != nil {
		return err
	}
	r, err := s.auth.UpdateRole(c.Context(), currentUser(c), strings.Clone(c.Params("rid")), in.input())
	if err != nil {
		return err
	}
	s.auditRole(c, "admin.role_update", r)
	return c.JSON(toRole(r))
}

func (s *panel) deleteRole(c fiber.Ctx) error {
	r, err := s.auth.DeleteRole(c.Context(), currentUser(c), strings.Clone(c.Params("rid")))
	if err != nil {
		return err
	}
	s.auditRole(c, "admin.role_delete", r)
	return c.SendStatus(fiber.StatusNoContent)
}

// auditRole names the role and its permission list (names only) as the
// target of the request's activity record.
func (s *panel) auditRole(c fiber.Ctx, action string, r domain.Role) {
	target := r.Name
	if action != "admin.role_delete" {
		target += ": " + strings.Join(r.Permissions, ", ")
		if len(target) > 500 {
			target = target[:497] + "..."
		}
	}
	c.Locals(keyAuditTarget, target)
}

// auditUser records an account-level administration change about subject.
func (s *panel) auditUser(c fiber.Ctx, action, subjectID, target string) {
	if s.audit == nil {
		return
	}
	u := currentUser(c)
	ip := c.IP()
	s.audit.Record(c.Context(), domain.AuditEvent{Action: action, ActorID: &u.ID, ActorLabel: &u.Email, SubjectUserID: &subjectID, Target: &target, Outcome: "ok", IP: &ip})
}
