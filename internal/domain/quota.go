package domain

// UserQuota overrides the panel-wide per-account limits for one account. A
// nil field falls back to the panel default; 0 allows none. MaxApps counts
// every app kind together; MaxBots, MaxGames and MaxWebApps count one kind.
type UserQuota struct {
	UserID                                           string
	MaxApps, MaxBots, MaxGames, MaxWebApps, MaxSites *int64
	MemoryBytes                                      *int64
	UpdatedBy                                        *string
	CreatedAtMS, UpdatedAtMS                         int64
}

// Empty reports whether the quota sets no limit (every field falls back).
func (q UserQuota) Empty() bool {
	return q.MaxApps == nil && q.MaxBots == nil && q.MaxGames == nil && q.MaxWebApps == nil &&
		q.MaxSites == nil && q.MemoryBytes == nil
}

// OwnerUsage is what an account holds against its limits: apps not deleted
// (running or stopped) by kind, their memory limits with add-ons, and sites.
type OwnerUsage struct {
	Apps, Bots, Games, Web, Sites int64
	Memory                        int64
}
