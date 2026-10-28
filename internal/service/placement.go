package service

import (
	"context"
	"fmt"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// SetDefaultNode is the panel setting naming the node new apps are created
// on when their workspace pins none ("" or absent: the local host).
const SetDefaultNode = "placement_default_node"

// Placement reasons, from the most to the least specific choice.
const (
	PlacementRequested = "requested" // an account that manages nodes chose it
	PlacementWorkspace = "workspace" // the workspace is pinned to it
	PlacementPanel     = "panel"     // the panel's default node
	PlacementLocal     = "local"     // the panel's own host
)

// Placement is the node a workspace's new apps are created on, and why.
type Placement struct {
	NodeID string
	Reason string
}

// NodeOnline reports whether new apps can reach a node right now: the local
// host always, a remote node when its agent is connected. Without remote
// file routing the answer is unknown and the node is not held back here.
func (s *BotService) NodeOnline(id string) bool {
	if !s.remote(id) || s.NodeFiles == nil {
		return true
	}
	return s.NodeFiles.Online(id)
}

// placeable returns a node that may receive new apps: it exists, is enabled,
// is not draining, and (for a remote node) remote routing is on. Being
// online is checked separately, where a message can name the node.
func (s *BotService) placeable(ctx context.Context, nodeID string) (domain.Node, error) {
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil || !node.Enabled {
		return domain.Node{}, domain.Invalid("node is unavailable")
	}
	if node.Draining {
		return domain.Node{}, domain.Invalid("that node is draining and accepts no new apps")
	}
	if node.Transport != "local" && !s.remote(nodeID) {
		return domain.Node{}, domain.Invalid("node is unavailable")
	}
	return node, nil
}

// defaultNode returns the panel's default node and whether it is an explicit
// (still valid) setting rather than the local fallback.
func (s *BotService) defaultNode(ctx context.Context) (string, bool, error) {
	stored, err := s.Store.Settings(ctx)
	if err != nil {
		return "", false, err
	}
	if st, ok := stored[SetDefaultNode]; ok && st.Value != "" {
		if _, err := s.placeable(ctx, st.Value); err == nil {
			return st.Value, true, nil
		}
	}
	return s.LocalNode, false, nil
}

// DefaultNode is the node new apps go to when their workspace pins none: the
// stored setting while that node exists, is enabled and is not draining,
// else the local host.
func (s *BotService) DefaultNode(ctx context.Context) (string, error) {
	id, _, err := s.defaultNode(ctx)
	return id, err
}

// SetDefaultNode stores the panel's default node ("" clears it). The node
// must accept new apps; it need not be online right now.
func (s *BotService) SetDefaultNode(ctx context.Context, actor domain.User, nodeID string) error {
	if err := requirePerm(actor, domain.PermNodesManage); err != nil {
		return err
	}
	if nodeID != "" {
		if _, err := s.placeable(ctx, nodeID); err != nil {
			return err
		}
	}
	return s.Store.PutSettings(ctx, []domain.Setting{{Key: SetDefaultNode, Value: nodeID}}, s.now())
}

// SetWorkspaceNode pins a workspace's new apps to a node ("" clears the
// pin). Existing apps stay where they are.
func (s *BotService) SetWorkspaceNode(ctx context.Context, actor domain.User, workspaceID, nodeID string) error {
	if err := requirePerm(actor, domain.PermNodesManage); err != nil {
		return err
	}
	if _, err := s.Store.GetWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	if nodeID != "" {
		if _, err := s.placeable(ctx, nodeID); err != nil {
			return err
		}
	}
	return s.Store.SetWorkspaceNode(ctx, workspaceID, nodeID, s.now())
}

// resolvePlacement is where a workspace's new apps go when nobody chooses:
// its pin while that node still accepts apps, else the panel default, else
// the local host.
func (s *BotService) resolvePlacement(ctx context.Context, workspaceID string) (Placement, error) {
	w, err := s.Store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return Placement{}, err
	}
	if w.NodeID != "" {
		if _, err := s.placeable(ctx, w.NodeID); err == nil {
			return Placement{NodeID: w.NodeID, Reason: PlacementWorkspace}, nil
		}
	}
	id, explicit, err := s.defaultNode(ctx)
	if err != nil {
		return Placement{}, err
	}
	if explicit {
		return Placement{NodeID: id, Reason: PlacementPanel}, nil
	}
	return Placement{NodeID: s.LocalNode, Reason: PlacementLocal}, nil
}

// ResolvePlacement tells a member where new apps in a workspace will be
// created ("" = the actor's personal workspace).
func (s *BotService) ResolvePlacement(ctx context.Context, actor domain.User, workspaceID string) (Placement, error) {
	if workspaceID == "" {
		w, err := s.Store.PersonalWorkspace(ctx, actor.ID)
		if err != nil {
			return Placement{}, err
		}
		workspaceID = w.ID
	} else if _, err := s.workspaceRole(ctx, actor, workspaceID); err != nil {
		return Placement{}, err
	}
	return s.resolvePlacement(ctx, workspaceID)
}

// chooseNode resolves the node a new app is created on. An explicit choice
// needs nodes.manage (the local host may always be named); otherwise the
// workspace's placement applies. The node must accept new apps and be
// online, so the app's files and container can be made at once.
func (s *BotService) chooseNode(ctx context.Context, actor domain.User, workspaceID, requested string) (string, error) {
	p := Placement{NodeID: requested, Reason: PlacementRequested}
	if requested == "" {
		var err error
		if p, err = s.resolvePlacement(ctx, workspaceID); err != nil {
			return "", err
		}
	} else if requested != s.LocalNode && !actor.Can(domain.PermNodesManage) {
		return "", domain.Invalid("only accounts that manage nodes can choose the node for a new app; this workspace's apps are placed automatically")
	}
	node, err := s.placeable(ctx, p.NodeID)
	if err != nil {
		return "", err
	}
	if !s.NodeOnline(node.ID) {
		return "", domain.Invalid(fmt.Sprintf("node %q is offline; new apps need its agent connected (choose another node or try again when it reconnects)", node.Name))
	}
	return node.ID, nil
}
