package service

import (
	"context"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// NodePlaceable reports whether new apps may be placed on a node, for an
// invitation that pins the invitee's personal workspace to it.
func (s *BotService) NodePlaceable(ctx context.Context, nodeID string) error {
	return s.placeable(ctx, nodeID)
}

// WorkspaceAdmin checks that a team workspace exists and that the actor
// administers it: its admin or owner, a built-in administrator, or
// workspaces.manage. Personal workspaces take no members.
func (s *BotService) WorkspaceAdmin(ctx context.Context, actor domain.User, workspaceID string) error {
	if _, err := s.requireWorkspaceRole(ctx, actor, workspaceID, domain.WorkspaceAdmin); err != nil {
		return err
	}
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if w.Personal {
		return domain.Invalid("invitees can only join team workspaces")
	}
	return nil
}
