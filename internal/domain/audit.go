package domain

// AuditEvent is one recorded change or security-relevant action.
type AuditEvent struct {
	ID            int64
	AtMS          int64
	ActorID       *string
	ActorLabel    *string
	BotID         *string
	BotName       *string
	SiteID        *string
	SiteName      *string
	SubjectUserID *string // the account an account-level event concerns
	Action        string  // e.g. bot.start, env.set, files.write
	Target        *string // a name or path, never a value
	Outcome       string  // ok | denied | failed
	IP            *string
	// Source is who or what performed it: person (default), schedule,
	// system or ai.
	Source string
}

// Audit sources.
const (
	SourcePerson   = "person"
	SourceSchedule = "schedule"
	SourceSystem   = "system"
	SourceAI       = "ai"
)
