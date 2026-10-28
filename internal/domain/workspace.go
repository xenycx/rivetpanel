package domain

// Workspace roles, from most to least privileged.
const (
	WorkspaceOwner     = "owner"     // everything, including deleting the workspace
	WorkspaceAdmin     = "admin"     // manage members and every bot and site
	WorkspaceDeveloper = "developer" // create and operate bots and sites; no resources, deletion or sharing
	WorkspaceViewer    = "viewer"    // read-only: status and console output
)

// Workspace groups bots and sites. Every account has one Personal workspace.
type Workspace struct {
	ID       string
	Name     string
	OwnerID  string
	Personal bool
	// NodeID pins new apps of the workspace to a node ("" = the panel's
	// default node, then the local host). Existing apps stay where they are.
	NodeID      string
	CreatedAtMS int64
	UpdatedAtMS int64
}

// WorkspaceSummary is a workspace with the caller's role and usage figures.
type WorkspaceSummary struct {
	Workspace
	OwnerEmail   string
	Role         string // the caller's role; "" when the caller is not a member (administrators)
	Members      int
	Bots         int
	RunningBots  int
	MemoryBytes  int64 // assigned to its apps, add-ons included (the figure its limits count)
	Sites        int
	LastActiveMS int64 // newest bot update in the workspace; 0 = none
}

// WorkspaceMember is one account's role in a workspace.
type WorkspaceMember struct {
	WorkspaceID string
	UserID      string
	Email       string
	DisplayName string
	Role        string
	AddedBy     *string
	CreatedAtMS int64
	UpdatedAtMS int64
}

// ValidWorkspaceRole reports whether r is a role that can be assigned to a
// member (the single owner is fixed when the workspace is created).
func ValidWorkspaceRole(r string) bool {
	return r == WorkspaceAdmin || r == WorkspaceDeveloper || r == WorkspaceViewer
}

// WorkspaceRoleRank orders roles; 0 means no membership.
func WorkspaceRoleRank(r string) int {
	switch r {
	case WorkspaceOwner:
		return 4
	case WorkspaceAdmin:
		return 3
	case WorkspaceDeveloper:
		return 2
	case WorkspaceViewer:
		return 1
	}
	return 0
}

// WorkspaceRolePerms is the bot permission mask a workspace role grants on
// every bot in the workspace (owner and admin: everything).
func WorkspaceRolePerms(r string) int {
	switch r {
	case WorkspaceOwner, WorkspaceAdmin:
		return PermAll
	case WorkspaceDeveloper:
		return PermViewConsole | PermPower | PermEditFiles | PermManageEnv
	case WorkspaceViewer:
		return PermViewConsole
	}
	return 0
}
