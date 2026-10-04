package domain

import "slices"

// APIClient is a scoped application credential for the HTTP API ("rvc_"
// bearer token). It carries a subset of its creator's permissions, optionally
// limited to some bots and workspaces. Only the SHA-256 of the token is
// stored. Every request re-checks the creator's current permissions, so a
// client never holds more than its creator does at that moment.
type APIClient struct {
	ID           string
	UserID       string
	Name         string
	Prefix       string
	TokenHash    []byte
	Permissions  []string
	BotIDs       []string // nil: not limited to bots
	WorkspaceIDs []string // nil: not limited to workspaces
	CreatedAtMS  int64
	LastUsedAtMS *int64
	ExpiresAtMS  *int64 // nil: never expires

	OwnerEmail string // administration listing only
}

// Scoped reports whether the client is limited to listed resources.
func (c APIClient) Scoped() bool { return c.BotIDs != nil || c.WorkspaceIDs != nil }

// ClientScope is what an authenticated API client limits a request to. It is
// attached to the request's User (User.Client); nil for browser sessions.
type ClientScope struct {
	ID           string
	Name         string
	Permissions  []string
	BotIDs       []string
	WorkspaceIDs []string
}

// Scoped reports whether the client is limited to listed bots or workspaces.
func (c *ClientScope) Scoped() bool {
	return c != nil && (c.BotIDs != nil || c.WorkspaceIDs != nil)
}

// AllowsBot reports whether a bot (in workspace wsID) is inside the scope.
func (c *ClientScope) AllowsBot(botID, wsID string) bool {
	if !c.Scoped() {
		return true
	}
	return slices.Contains(c.BotIDs, botID) || (wsID != "" && slices.Contains(c.WorkspaceIDs, wsID))
}

// AllowsWorkspace reports whether a workspace is inside the scope. A client
// limited to bots only reaches no workspace as a whole.
func (c *ClientScope) AllowsWorkspace(wsID string) bool {
	if !c.Scoped() {
		return true
	}
	return slices.Contains(c.WorkspaceIDs, wsID)
}

// ScopeError is a 403 for a resource outside an API client's scope.
type ScopeError struct{ What string }

func (e *ScopeError) Error() string {
	return "this API client is not allowed to use this " + e.What
}

func (e *ScopeError) Is(target error) bool { return target == ErrForbidden }
