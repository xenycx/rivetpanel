package domain

// WorkspaceQuota overrides the panel-wide default limits for one workspace.
// A nil field falls back to the panel default; 0 allows none. MaxApps counts
// every app kind together; MaxBots, MaxGames and MaxWebApps count one kind.
// NanoCPUs is the CPU assigned to all apps together (1e9 = one core).
type WorkspaceQuota struct {
	WorkspaceID                                      string
	MaxApps, MaxBots, MaxGames, MaxWebApps, MaxSites *int64
	MemoryBytes, NanoCPUs                            *int64
	UpdatedBy                                        *string
	CreatedAtMS, UpdatedAtMS                         int64
}

// Empty reports whether the quota sets no limit (every field falls back).
func (q WorkspaceQuota) Empty() bool {
	return q.MaxApps == nil && q.MaxBots == nil && q.MaxGames == nil && q.MaxWebApps == nil &&
		q.MaxSites == nil && q.MemoryBytes == nil && q.NanoCPUs == nil
}

// WorkspaceUsage is what a workspace holds against its limits: apps not
// deleted (running or stopped) by kind, their memory limits with add-ons,
// their CPU limits, and sites.
type WorkspaceUsage struct {
	Apps, Bots, Games, Web, Sites int64
	Memory, CPU                   int64
}
