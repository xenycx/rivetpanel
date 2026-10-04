package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// MaxOwnedWorkspaces bounds the team workspaces one account may own.
const MaxOwnedWorkspaces = 20

// WorkspaceDetail is a workspace with its members, as seen by one caller.
type WorkspaceDetail struct {
	domain.WorkspaceSummary
	Members []domain.WorkspaceMember
}

// workspaceRole returns the actor's effective role. Panel administrators act
// as owners of every workspace; non-members get ErrNotFound so workspace ids
// cannot be probed.
func (s *BotService) workspaceRole(ctx context.Context, actor domain.User, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", domain.ErrNotFound
	}
	if _, err := s.Store.GetWorkspace(ctx, id); err != nil {
		return "", err
	}
	if !actor.Client.AllowsWorkspace(id) {
		return "", &domain.ScopeError{What: "workspace"}
	}
	if actor.IsAdmin() {
		return domain.WorkspaceOwner, nil
	}
	role, err := s.Store.WorkspaceRole(ctx, id, actor.ID)
	if err != nil {
		return "", err
	}
	// A delegated workspaces.view permission reads any workspace as a viewer.
	if role == "" && actor.Can(domain.PermWorkspacesView) {
		role = domain.WorkspaceViewer
	}
	if role == "" {
		return "", domain.ErrNotFound
	}
	return role, nil
}

// requireWorkspaceRole is workspaceRole plus a minimum role (ErrForbidden below it).
func (s *BotService) requireWorkspaceRole(ctx context.Context, actor domain.User, id, min string) (string, error) {
	role, err := s.workspaceRole(ctx, actor, id)
	if err != nil {
		return "", err
	}
	if domain.WorkspaceRoleRank(role) < domain.WorkspaceRoleRank(min) {
		return "", domain.ErrForbidden
	}
	return role, nil
}

// creatableWorkspace resolves the workspace a new bot or site goes into: the
// actor's personal workspace by default, otherwise one where they are at
// least a developer.
func (s *BotService) creatableWorkspace(ctx context.Context, actor domain.User, id string) (string, error) {
	if id == "" {
		w, err := s.Store.PersonalWorkspace(ctx, actor.ID)
		if err != nil {
			return "", err
		}
		if !actor.Client.AllowsWorkspace(w.ID) {
			return "", &domain.ScopeError{What: "workspace"}
		}
		return w.ID, nil
	}
	if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceDeveloper); err != nil {
		var se *domain.ScopeError
		if errors.Is(err, domain.ErrForbidden) && !errors.As(err, &se) {
			return "", domain.Invalid("viewers cannot create anything in that workspace")
		}
		return "", err
	}
	return id, nil
}

// ListWorkspaces returns the workspaces the actor is a member of.
func (s *BotService) ListWorkspaces(ctx context.Context, actor domain.User) ([]domain.WorkspaceSummary, error) {
	all, err := s.Store.ListWorkspacesForUser(ctx, actor.ID)
	if err != nil || !actor.Client.Scoped() {
		return all, err
	}
	out := all[:0]
	for _, w := range all {
		if actor.Client.AllowsWorkspace(w.ID) {
			out = append(out, w)
		}
	}
	return out, nil
}

// ListAllWorkspaces returns every workspace (administrators only).
func (s *BotService) ListAllWorkspaces(ctx context.Context, actor domain.User) ([]domain.WorkspaceSummary, error) {
	if !actor.Can(domain.PermWorkspacesView) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListAllWorkspaces(ctx, actor.ID)
}

// GetWorkspace returns a workspace and its members to any member.
func (s *BotService) GetWorkspace(ctx context.Context, actor domain.User, id string) (WorkspaceDetail, error) {
	role, err := s.workspaceRole(ctx, actor, id)
	if err != nil {
		return WorkspaceDetail{}, err
	}
	sum, err := s.Store.WorkspaceSummaryFor(ctx, id, actor.ID)
	if err != nil {
		return WorkspaceDetail{}, err
	}
	if sum.Role == "" {
		sum.Role = role // administrators who are not members
	}
	members, err := s.Store.ListWorkspaceMembers(ctx, id)
	if err != nil {
		return WorkspaceDetail{}, err
	}
	return WorkspaceDetail{WorkspaceSummary: sum, Members: members}, nil
}

// CreateWorkspace creates a team workspace owned by the actor.
func (s *BotService) CreateWorkspace(ctx context.Context, actor domain.User, name string) (domain.Workspace, error) {
	name, err := validateName(name)
	if err != nil {
		return domain.Workspace{}, err
	}
	if !actor.IsAdmin() {
		n, err := s.Store.CountOwnedWorkspaces(ctx, actor.ID)
		if err != nil {
			return domain.Workspace{}, err
		}
		if n >= MaxOwnedWorkspaces {
			return domain.Workspace{}, domain.Invalid("you can own at most 20 team workspaces; delete one first")
		}
	}
	now := s.now()
	w := domain.Workspace{ID: uuid.NewString(), Name: name, OwnerID: actor.ID, CreatedAtMS: now, UpdatedAtMS: now}
	return w, s.Store.CreateWorkspace(ctx, w)
}

// RenameWorkspace needs the admin role.
func (s *BotService) RenameWorkspace(ctx context.Context, actor domain.User, id, name string) error {
	if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceAdmin); err != nil {
		return err
	}
	name, err := validateName(name)
	if err != nil {
		return err
	}
	return s.Store.RenameWorkspace(ctx, id, name, s.now())
}

// DeleteWorkspace removes an empty team workspace (owner or administrator).
func (s *BotService) DeleteWorkspace(ctx context.Context, actor domain.User, id string) error {
	if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceOwner); err != nil {
		return err
	}
	return s.Store.DeleteWorkspace(ctx, id)
}

// AddWorkspaceMember adds a registered account (or changes an existing
// member's role). Workspace admins may manage every member except the owner.
func (s *BotService) AddWorkspaceMember(ctx context.Context, actor domain.User, id, email, role string) (domain.WorkspaceMember, error) {
	if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceAdmin); err != nil {
		return domain.WorkspaceMember{}, err
	}
	if !domain.ValidWorkspaceRole(role) {
		return domain.WorkspaceMember{}, domain.Invalid("role must be admin, developer or viewer")
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	target, err := s.Store.GetUserByEmail(ctx, norm)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && target.Disabled) {
		return domain.WorkspaceMember{}, domain.Invalid("no active registered user has that email")
	}
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	m, err := s.setMember(ctx, actor, id, target, role)
	if err == nil {
		if w, err := s.Store.GetWorkspace(ctx, id); err == nil {
			s.noticeAccess(ctx, actor, target.ID, actorName(actor)+" added you to the workspace "+w.Name, "Your role: "+role+".", "/settings/workspaces/"+id)
		}
	}
	return m, err
}

// SetWorkspaceMemberRole changes a member's role.
func (s *BotService) SetWorkspaceMemberRole(ctx context.Context, actor domain.User, id, userID, role string) (domain.WorkspaceMember, error) {
	if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceAdmin); err != nil {
		return domain.WorkspaceMember{}, err
	}
	if !domain.ValidWorkspaceRole(role) {
		return domain.WorkspaceMember{}, domain.Invalid("role must be admin, developer or viewer")
	}
	cur, err := s.Store.WorkspaceRole(ctx, id, userID)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	if cur == "" {
		return domain.WorkspaceMember{}, domain.ErrNotFound
	}
	target, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	return s.setMember(ctx, actor, id, target, role)
}

func (s *BotService) setMember(ctx context.Context, actor domain.User, id string, target domain.User, role string) (domain.WorkspaceMember, error) {
	cur, err := s.Store.WorkspaceRole(ctx, id, target.ID)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	if cur == domain.WorkspaceOwner {
		return domain.WorkspaceMember{}, domain.Invalid("the owner's role cannot be changed")
	}
	now := s.now()
	m := domain.WorkspaceMember{WorkspaceID: id, UserID: target.ID, Email: target.Email, DisplayName: target.DisplayName,
		Role: role, AddedBy: &actor.ID, CreatedAtMS: now, UpdatedAtMS: now}
	return m, s.Store.SetWorkspaceMember(ctx, m)
}

// RemoveWorkspaceMember removes a member; anyone but the owner may leave.
func (s *BotService) RemoveWorkspaceMember(ctx context.Context, actor domain.User, id, userID string) error {
	if actor.ID == userID {
		role, err := s.workspaceRole(ctx, actor, id)
		if err != nil {
			return err
		}
		if role == domain.WorkspaceOwner && !actor.IsAdmin() {
			return domain.Invalid("the owner cannot leave; delete the workspace instead")
		}
	} else if _, err := s.requireWorkspaceRole(ctx, actor, id, domain.WorkspaceAdmin); err != nil {
		return err
	}
	cur, err := s.Store.WorkspaceRole(ctx, id, userID)
	if err != nil {
		return err
	}
	if cur == domain.WorkspaceOwner {
		return domain.Invalid("the owner cannot be removed")
	}
	return s.Store.RemoveWorkspaceMember(ctx, id, userID)
}

// MoveBot puts a bot into another workspace. The actor must control the bot
// as its owner (or a workspace admin of its current workspace) and be at
// least a developer in the target.
func (s *BotService) MoveBot(ctx context.Context, actor domain.User, botID, workspaceID string) (domain.Bot, error) {
	b, err := s.load(ctx, actor, botID, false)
	if err != nil {
		return domain.Bot{}, err
	}
	if b.WorkspaceID == workspaceID {
		return b, nil
	}
	if _, err := s.creatableWorkspace(ctx, actor, workspaceID); err != nil {
		return domain.Bot{}, err
	}
	if err := s.Store.SetBotWorkspace(ctx, botID, workspaceID, s.now()); err != nil {
		return domain.Bot{}, err
	}
	b.WorkspaceID = workspaceID
	return b, nil
}

// WorkspaceBots lists a workspace's bots for any member (administrators: any).
func (s *BotService) WorkspaceBots(ctx context.Context, actor domain.User, id string) ([]domain.Bot, error) {
	if _, err := s.workspaceRole(ctx, actor, id); err != nil {
		return nil, err
	}
	return s.Store.ListWorkspaceBots(ctx, id)
}
