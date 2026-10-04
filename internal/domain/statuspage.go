package domain

// Status page component states, least to most severe (maintenance is shown
// separately and never counts as downtime).
const (
	StateOperational = "operational"
	StateUnknown     = "unknown"
	StateDegraded    = "degraded"
	StatePartial     = "partial_outage"
	StateMajor       = "major_outage"
	StateMaintenance = "maintenance"
)

// StateRank orders states by severity (maintenance ranks with unknown).
func StateRank(s string) int {
	switch s {
	case StateOperational:
		return 0
	case StateDegraded:
		return 2
	case StatePartial:
		return 3
	case StateMajor:
		return 4
	}
	return 1
}

// Status page component sources.
const (
	ComponentPanel = "panel" // the panel itself
	ComponentNode  = "node"  // a node (the local one or a rivet-agent)
	ComponentBot   = "bot"   // a bot or game server
)

// StatusComponent is one row of the public status page. RefID points at the
// node or bot it follows ("" for the panel) and is never published.
type StatusComponent struct {
	ID          string
	Kind        string
	RefID       string
	Name        string // the public, administrator-chosen name
	Description string
	Position    int
	CreatedAtMS int64
	UpdatedAtMS int64
}

// Incident kinds, impacts and statuses.
const (
	IncidentKind    = "incident"
	MaintenanceKind = "maintenance"

	IncidentResolved     = "resolved"
	MaintenanceScheduled = "scheduled"
	MaintenanceActive    = "in_progress"
	MaintenanceCompleted = "completed"
)

var (
	IncidentImpacts     = []string{"none", "minor", "major", "critical"}
	IncidentStatuses    = []string{"investigating", "identified", "monitoring", IncidentResolved}
	MaintenanceStatuses = []string{MaintenanceScheduled, MaintenanceActive, MaintenanceCompleted}
	IncidentKinds       = []string{IncidentKind, MaintenanceKind}
)

// StatusIncident is a manually reported incident or scheduled maintenance.
type StatusIncident struct {
	ID           string
	Kind         string
	Title        string
	Impact       string
	Status       string
	ComponentIDs []string
	StartsAtMS   *int64 // maintenance window
	EndsAtMS     *int64
	CreatedBy    *string
	CreatedAtMS  int64
	UpdatedAtMS  int64
	ResolvedAtMS *int64                 // set when resolved/completed
	Updates      []StatusIncidentUpdate // newest first
}

// Closed reports whether the incident is resolved or the maintenance done.
func (i StatusIncident) Closed() bool {
	return i.Status == IncidentResolved || i.Status == MaintenanceCompleted
}

// StatusIncidentUpdate is one entry of an incident's timeline.
type StatusIncidentUpdate struct {
	ID          string
	IncidentID  string
	Status      string
	Body        string
	AuthorID    *string
	CreatedAtMS int64
}

// StatusDay counts one component's samples on one UTC day (days since the
// Unix epoch).
type StatusDay struct {
	ComponentID string
	Day         int64
	Operational int
	Degraded    int
	Outage      int
	Maintenance int
}
