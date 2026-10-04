package domain

// Notification is one in-panel notification for one account. Title and Body
// are plain text; Link is a same-origin path ("" = none).
type Notification struct {
	ID          string
	UserID      string
	Category    string
	Title       string
	Body        string
	Link        string
	CreatedAtMS int64
	ReadAtMS    *int64
}

// Notification categories. Each has an in-panel and (where Email is set) an
// email channel the account can switch per category.
const (
	NotifyBotAlerts     = "bot_alerts"
	NotifyDeploys       = "deploys"
	NotifyBackups       = "backups"
	NotifyNodes         = "nodes"
	NotifyAccess        = "access"
	NotifyAnnouncements = "announcements"
	NotifyTickets       = "tickets"
)

// NotificationCategory describes a category for the preferences page.
type NotificationCategory struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Email reports whether the category has an email channel the account
	// controls here (announcement email follows the notice/news rules).
	Email bool `json:"email"`
	// Alert marks categories whose email also needs the profile's
	// "Alert emails" switch (the existing master switch for bot alerts).
	Alert bool `json:"alert"`
	// Permission, when set, is needed to receive the category at all.
	Permission string `json:"permission,omitempty"`
}

// NotificationCategories is the catalog, in display order.
var NotificationCategories = []NotificationCategory{
	{NotifyBotAlerts, "Bot and server alerts", "A bot or server crashed, stopped reporting or recovered.", true, true, ""},
	{NotifyDeploys, "Deployments", "GitHub deployments that finished or failed.", true, true, ""},
	{NotifyBackups, "Backups", "Backups that failed.", true, true, ""},
	{NotifyNodes, "Nodes", "A node's agent went offline or came back (accounts that manage nodes).", true, true, PermNodesManage},
	{NotifyAccess, "Access and invitations", "Someone shared a bot with you, added you to a workspace, transferred a bot to you or accepted your invitation.", true, false, ""},
	{NotifyAnnouncements, "Announcements", "Announcements from the panel's administrators.", false, false, ""},
	{NotifyTickets, "Support tickets", "Replies and status changes on support tickets.", true, false, ""},
}

// NotificationCategoryByName finds a category.
func NotificationCategoryByName(name string) (NotificationCategory, bool) {
	for _, c := range NotificationCategories {
		if c.Name == name {
			return c, true
		}
	}
	return NotificationCategory{}, false
}

// NotificationPref is an account's choice for one category.
type NotificationPref struct {
	Category string
	InPanel  bool
	Email    bool
}
