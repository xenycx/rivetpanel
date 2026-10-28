package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// placementNodeDTO is what any member may learn about the node their new
// apps go to: enough to show it and size an app, no telemetry or host names.
type placementNodeDTO struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Location  string        `json:"location"`
	Transport string        `json:"transport"`
	Online    bool          `json:"online"`
	Limits    nodeLimitsDTO `json:"limits"`
}

// locationName resolves a location's display name ("" when unknown).
func (s *panel) locationName(c fiber.Ctx, id string) string {
	if s.nodes == nil || id == "" {
		return ""
	}
	locs, _, err := s.nodes.ListLocations(c.Context())
	if err != nil {
		return ""
	}
	for _, l := range locs {
		if l.ID == id {
			return l.Name
		}
	}
	return ""
}

func (s *panel) placementNode(c fiber.Ctx, n domain.Node) placementNodeDTO {
	l := s.bots.LimitsFor(c.Context(), n.ID)
	return placementNodeDTO{ID: n.ID, Name: n.Name, Location: s.locationName(c, n.LocationID), Transport: n.Transport,
		Online: s.bots.NodeOnline(n.ID), Limits: nodeLimitsDTO{MaxMemoryBytes: l.MaxMemoryBytes, MaxNanoCPUs: l.MaxNanoCPUs}}
}

// placement tells the caller where new apps in a workspace will be created
// (?workspace=; their personal workspace by default) and whether they may
// choose another node.
func (s *panel) placement(c fiber.Ctx) error {
	u := currentUser(c)
	p, err := s.bots.ResolvePlacement(c.Context(), u, strings.Clone(c.Query("workspace")))
	if err != nil {
		return err
	}
	n, err := s.bots.Store.GetNode(c.Context(), p.NodeID)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"node": s.placementNode(c, n), "reason": p.Reason, "can_choose": u.Can(domain.PermNodesManage)})
}

// placementAllows reports whether nodeID is the node the caller's new apps
// in ?workspace= go to, so forms can read that node's limits without
// nodes.manage.
func (s *panel) placementAllows(c fiber.Ctx, nodeID string) bool {
	p, err := s.bots.ResolvePlacement(c.Context(), currentUser(c), strings.Clone(c.Query("workspace")))
	return err == nil && p.NodeID == nodeID
}

type nodeChoice struct {
	NodeID string `json:"node_id"`
}

// putDefaultNode sets the panel's default node for new apps ("" clears it).
func (s *panel) putDefaultNode(c fiber.Ctx) error {
	var in nodeChoice
	if err := decode(c, &in); err != nil {
		return err
	}
	in.NodeID = strings.TrimSpace(in.NodeID)
	if err := s.bots.SetDefaultNode(c.Context(), currentUser(c), in.NodeID); err != nil {
		return err
	}
	return c.JSON(nodeChoice{NodeID: in.NodeID})
}

// adminPutWorkspaceNode pins a workspace's new apps to a node ("" clears).
func (s *panel) adminPutWorkspaceNode(c fiber.Ctx) error {
	var in nodeChoice
	if err := decode(c, &in); err != nil {
		return err
	}
	in.NodeID = strings.TrimSpace(in.NodeID)
	if err := s.bots.SetWorkspaceNode(c.Context(), currentUser(c), strings.Clone(c.Params("wid")), in.NodeID); err != nil {
		return err
	}
	return c.JSON(nodeChoice{NodeID: in.NodeID})
}

// defaultNodeID is the panel's current default node for GET /nodes; the
// local host when the setting is unset or stale.
func (s *panel) defaultNodeID(c fiber.Ctx) string {
	id, err := s.bots.DefaultNode(c.Context())
	if err != nil {
		return s.bots.LocalNode
	}
	return id
}
