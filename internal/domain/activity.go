package domain

// ActivityItem is one entry of the unified activity timeline: either a
// recorded change (an AuditEvent) or a unit of long-running work (an
// Operation). Key orders items that share a timestamp and is the cursor.
type ActivityItem struct {
	Key      string // "a:<zero-padded audit id>" or "o:<operation id>"
	AtMS     int64
	Type     string // change | work
	Source   string // person | schedule | push | system | ai
	Category string // see ActivityCategories
	Outcome  string // ok | failed | denied | running | cancelled

	Actor *Person // who did it, or on whose behalf the panel did it

	ActorLabel  *string // recorded label (survives a deleted account)
	BotID       *string
	BotName     *string
	BotKind     string // bot | game | web, when known
	BotLogoURL  string // custom logo or Discord avatar, when known
	BotVariant  int64
	SiteID      *string
	SiteName    *string
	SiteVariant int64

	Action string  // change: the audit action; work: "op.<kind>"
	Target *string // change: a name or path, never a value

	Op *Operation // work only
}

// ActivityCategories are the groups the timeline can be filtered and
// reported by, in display order.
var ActivityCategories = []string{"power", "deploys", "backups", "files", "variables", "settings", "access", "account", "admin", "ai", "extensions", "other"}

// ActivitySources are who or what performed an item.
var ActivitySources = []string{"person", "schedule", "push", "system", "ai"}

// ActivityOutcomes are the normalized results of an item.
var ActivityOutcomes = []string{"ok", "failed", "denied", "running", "cancelled"}

// ActivityStats summarizes a window of the timeline.
type ActivityStats struct {
	FromMS, ToMS, BucketMS int64
	Buckets                []ActivityBucket
	Total                  int64
	BySource               map[string]int64
	ByOutcome              map[string]int64
	ByCategory             map[string]int64
	People                 []ActivityPersonCount
	Apps                   []ActivityAppCount
	// Week holds counts by local weekday (0 = Monday) and hour.
	Week [7][24]int64
}

// ActivityBucket counts one slice of the window.
type ActivityBucket struct {
	StartMS   int64
	People    int64
	Automatic int64
	Failed    int64
}

// ActivityPersonCount is how much one account did in the window.
type ActivityPersonCount struct {
	Person Person
	Label  string
	Count  int64
	Failed int64
}

// ActivityAppCount is how much happened on one app or site in the window.
type ActivityAppCount struct {
	ID      string
	Name    string
	Kind    string // bot | game | web | site
	LogoURL string
	Variant int64
	Count   int64
	Failed  int64
}
