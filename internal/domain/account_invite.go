package domain

// AccountInvite lets one person register an account. Role is the built-in
// role the account gets ("admin" or "user"); RoleID names a custom role
// instead (the account keeps Role "user" plus the custom role, like users).
//
// The setup fields are optional and applied when the invitation is used:
// Quota becomes the limits of the invitee's personal workspace, NodeID pins
// that workspace to a node, and WorkspaceID/WorkspaceRole add the invitee to
// a team workspace. A role, node or workspace deleted before the link is
// used is skipped; registration never fails because of them.
type AccountInvite struct {
	ID, Email, Role, CreatedBy string
	CreatedAtMS, ExpiresAtMS   int64

	RoleID        string
	Quota         *WorkspaceQuota
	NodeID        string
	WorkspaceID   string
	WorkspaceRole string

	// Display names resolved when the invitation is read: the creator's
	// address and the names of the role, node and workspace ("" when gone).
	CreatedByEmail string
	RoleName       string
	NodeName       string
	WorkspaceName  string
}
