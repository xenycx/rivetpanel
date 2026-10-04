package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type workspaceDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	OwnerID      string `json:"owner_id"`
	OwnerEmail   string `json:"owner_email"`
	Personal     bool   `json:"personal"`
	Role         string `json:"role"` // the caller's role ("" for administrators who are not members)
	Members      int    `json:"members"`
	Bots         int    `json:"bots"`
	RunningBots  int    `json:"running_bots"`
	MemoryBytes  int64  `json:"memory_bytes"`
	Sites        int    `json:"sites"`
	LastActiveMS int64  `json:"last_active_at_ms"`
	CreatedAtMS  int64  `json:"created_at_ms"`
}

func toWorkspace(w domain.WorkspaceSummary) workspaceDTO {
	return workspaceDTO{w.ID, w.Name, w.OwnerID, w.OwnerEmail, w.Personal, w.Role, w.Members, w.Bots, w.RunningBots,
		w.MemoryBytes, w.Sites, w.LastActiveMS, w.CreatedAtMS}
}

type memberDTO struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

func toMember(m domain.WorkspaceMember) memberDTO {
	return memberDTO{m.UserID, m.Email, m.DisplayName, m.Role, m.CreatedAtMS}
}

func toWorkspaceList(ws []domain.WorkspaceSummary) []workspaceDTO {
	out := make([]workspaceDTO, len(ws))
	for i, w := range ws {
		out[i] = toWorkspace(w)
	}
	return out
}

func (s *panel) listWorkspaces(c fiber.Ctx) error {
	ws, err := s.bots.ListWorkspaces(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"workspaces": toWorkspaceList(ws)})
}

func (s *panel) createWorkspace(c fiber.Ctx) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	w, err := s.bots.CreateWorkspace(c.Context(), currentUser(c), in.Name)
	if err != nil {
		return err
	}
	d, err := s.bots.GetWorkspace(c.Context(), currentUser(c), w.ID)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(s.workspaceDetail(d))
}

func (s *panel) workspaceDetail(d service.WorkspaceDetail) fiber.Map {
	members := make([]memberDTO, len(d.Members))
	for i, m := range d.Members {
		members[i] = toMember(m)
	}
	return fiber.Map{"workspace": toWorkspace(d.WorkspaceSummary), "members": members}
}

func (s *panel) getWorkspace(c fiber.Ctx) error {
	d, err := s.bots.GetWorkspace(c.Context(), currentUser(c), strings.Clone(c.Params("wid")))
	if err != nil {
		return err
	}
	return c.JSON(s.workspaceDetail(d))
}

func (s *panel) patchWorkspace(c fiber.Ctx) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	id := strings.Clone(c.Params("wid"))
	if err := s.bots.RenameWorkspace(c.Context(), currentUser(c), id, in.Name); err != nil {
		return err
	}
	return s.getWorkspace(c)
}

func (s *panel) deleteWorkspace(c fiber.Ctx) error {
	if err := s.bots.DeleteWorkspace(c.Context(), currentUser(c), strings.Clone(c.Params("wid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) addWorkspaceMember(c fiber.Ctx) error {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	m, err := s.bots.AddWorkspaceMember(c.Context(), currentUser(c), strings.Clone(c.Params("wid")), in.Email, in.Role)
	if err != nil {
		return err
	}
	return c.JSON(toMember(m))
}

func (s *panel) patchWorkspaceMember(c fiber.Ctx) error {
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	m, err := s.bots.SetWorkspaceMemberRole(c.Context(), currentUser(c), strings.Clone(c.Params("wid")), strings.Clone(c.Params("uid")), in.Role)
	if err != nil {
		return err
	}
	return c.JSON(toMember(m))
}

func (s *panel) removeWorkspaceMember(c fiber.Ctx) error {
	if err := s.bots.RemoveWorkspaceMember(c.Context(), currentUser(c), strings.Clone(c.Params("wid")), strings.Clone(c.Params("uid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) moveBot(c fiber.Ctx) error {
	var in struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	b, err := s.bots.MoveBot(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.WorkspaceID)
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

// adminBotDTO is a bot row in administrator overviews, with its owner.
type adminBotDTO struct {
	botDTO
	OwnerEmail string `json:"owner_email"`
}

// adminBots renders bots with owner emails for administrator views.
func (s *panel) adminBots(c fiber.Ctx, bots []domain.Bot) ([]adminBotDTO, error) {
	emails := map[string]string{}
	out := make([]adminBotDTO, 0, len(bots))
	for _, b := range bots {
		if _, ok := emails[b.OwnerID]; !ok {
			if u, err := s.bots.Store.GetUserByID(c.Context(), b.OwnerID); err == nil {
				emails[b.OwnerID] = u.Email
			}
		}
		out = append(out, adminBotDTO{s.viewBot(c, b), emails[b.OwnerID]})
	}
	return out, nil
}

// recentOps renders operations, tolerating a panel without operation history.
func (s *panel) recentOps(list func() ([]domain.Operation, error)) ([]opDTO, error) {
	out := []opDTO{}
	if s.ops == nil {
		return out, nil
	}
	ops, err := list()
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		out = append(out, toOp(o))
	}
	return out, nil
}

// adminListWorkspaces lists every workspace, or with ?user= the workspaces an
// account belongs to (with that account's role).
func (s *panel) adminListWorkspaces(c fiber.Ctx) error {
	actor := currentUser(c)
	var ws []domain.WorkspaceSummary
	var err error
	if uid := c.Query("user"); uid != "" {
		ws, err = s.bots.Store.ListWorkspacesForUser(c.Context(), strings.Clone(uid))
	} else {
		ws, err = s.bots.ListAllWorkspaces(c.Context(), actor)
	}
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"workspaces": toWorkspaceList(ws)})
}

// adminGetWorkspace is the administrator's view of one workspace: members,
// bots with their owners, hosted sites and recent deployments.
func (s *panel) adminGetWorkspace(c fiber.Ctx) error {
	actor := currentUser(c)
	id := strings.Clone(c.Params("wid"))
	d, err := s.bots.GetWorkspace(c.Context(), actor, id)
	if err != nil {
		return err
	}
	bots, err := s.bots.WorkspaceBots(c.Context(), actor, id)
	if err != nil {
		return err
	}
	botRows, err := s.adminBots(c, bots)
	if err != nil {
		return err
	}
	ops, err := s.recentOps(func() ([]domain.Operation, error) { return s.ops.ForWorkspace(c.Context(), actor, id, 40) })
	if err != nil {
		return err
	}
	out := s.workspaceDetail(d)
	out["bots"], out["operations"] = botRows, ops
	out["sites"] = s.sitesIn(c, id)
	return c.JSON(out)
}

// adminGetUser is the administrator's view of one account: its workspaces,
// the bots it owns and their recent deployments.
func (s *panel) adminGetUser(c fiber.Ctx) error {
	actor := currentUser(c)
	id := strings.Clone(c.Params("id"))
	u, err := s.bots.Store.GetUserByID(c.Context(), id)
	if err != nil {
		return err
	}
	ws, err := s.bots.Store.ListWorkspacesForUser(c.Context(), id)
	if err != nil {
		return err
	}
	owned, err := s.bots.Store.ListBots(c.Context(), id)
	if err != nil {
		return err
	}
	live := owned[:0]
	for _, b := range owned {
		if b.DesiredState != domain.DesiredDeleted {
			live = append(live, b)
		}
	}
	botRows, err := s.adminBots(c, live)
	if err != nil {
		return err
	}
	ops, err := s.recentOps(func() ([]domain.Operation, error) { return s.ops.ForOwner(c.Context(), actor, id, 40) })
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"user": toUser(u), "has_password": u.PasswordHash != "", "workspaces": toWorkspaceList(ws), "bots": botRows, "operations": ops})
}
