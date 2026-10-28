package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// WorkspaceDeleteSummary counts what an administrator's workspace deletion
// removed, for the audit record and the response.
type WorkspaceDeleteSummary struct {
	Apps, Sites, Members int
}

// DeleteWorkspaceCascade removes a team workspace from Administration with
// everything in it: its sites (and their web apps), its bots and game
// servers (containers, files and add-on data go the way a normal deletion
// takes them), then the workspace row with its members and limits. The
// caller types the workspace's name to confirm. Personal workspaces are
// refused (delete the account instead), as is a workspace with an app on a
// remote node that is offline, before anything changes; a later failure
// leaves the remaining apps in place and the deletion can be repeated.
// sites may be nil when site hosting is off.
func (s *BotService) DeleteWorkspaceCascade(ctx context.Context, actor domain.User, id, confirm string, sites *SiteService) (domain.Workspace, WorkspaceDeleteSummary, error) {
	var sum WorkspaceDeleteSummary
	if !managesWorkspaces(actor) {
		return domain.Workspace{}, sum, domain.Denied(actor, domain.PermWorkspacesManage)
	}
	w, err := s.Store.GetWorkspace(ctx, id)
	if err != nil {
		return domain.Workspace{}, sum, err
	}
	if w.Personal {
		return w, sum, domain.Invalid("a personal workspace cannot be deleted; delete the account instead")
	}
	if confirm != w.Name {
		return w, sum, domain.Invalid("type the workspace's name exactly to confirm")
	}
	bots, siteIDs, members, err := s.Store.WorkspaceHoldings(ctx, id)
	if err != nil {
		return w, sum, err
	}
	sum = WorkspaceDeleteSummary{Apps: len(bots), Sites: len(siteIDs), Members: members}
	// Apps on an offline remote node could not be torn down: refuse before
	// touching anything so the workspace is deleted whole or not at all.
	offline := map[string]int{}
	for _, b := range bots {
		if s.remote(b.NodeID) && !(s.NodeFiles != nil && s.NodeFiles.Online(b.NodeID)) {
			offline[b.NodeID]++
		}
	}
	for nodeID, n := range offline {
		name := nodeID
		if node, err := s.Store.GetNode(ctx, nodeID); err == nil {
			name = node.Name
		}
		return w, sum, domain.Invalid(fmt.Sprintf("%d apps run on node %q, which is offline; wait for it to reconnect (or delete the node) and try again", n, name))
	}
	// The actor was authorized above for the whole workspace, so each site
	// and app is torn down directly (a delegated workspaces.manage account
	// is no member and would not pass the per-app checks). Sites first:
	// deleting a built site also removes its web app unit.
	if sites.Enabled() {
		for _, sid := range siteIDs {
			st, err := sites.Store.GetSite(ctx, sid)
			if err == nil {
				err = sites.deleteSite(ctx, st)
			}
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return w, sum, fmt.Errorf("delete site %s: %w", sid, err)
			}
		}
	}
	for _, b := range bots {
		if err := s.deleteLoaded(ctx, b, true); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return w, sum, fmt.Errorf("delete app %s: %w", b.ID, err)
		}
	}
	return w, sum, s.Store.DeleteWorkspaceCascade(ctx, id)
}
