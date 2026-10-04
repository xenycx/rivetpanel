package domain

// Ticket statuses.
const (
	TicketOpen     = "open"     // waiting for staff
	TicketPending  = "pending"  // waiting for the requester
	TicketResolved = "resolved" // staff consider it solved; a reply reopens it
	TicketClosed   = "closed"   // finished; no more replies from the requester
)

// Ticket categories and priorities.
var (
	TicketCategories = []string{"general", "technical", "billing", "account", "abuse"}
	TicketPriorities = []string{"low", "normal", "high", "urgent"}
	TicketStatuses   = []string{TicketOpen, TicketPending, TicketResolved, TicketClosed}
)

// Ticket is a support conversation opened by an account.
type Ticket struct {
	ID            string
	Number        int64
	UserID        string
	UserEmail     string // filled by queries
	Subject       string
	Category      string
	Priority      string
	Status        string
	BotID         *string
	BotName       string
	AssigneeID    *string
	AssigneeEmail string // filled by queries
	CreatedAtMS   int64
	UpdatedAtMS   int64
	ClosedAtMS    *int64
	Messages      int // listing only: messages visible to the reader
}

// TicketMessage is a reply, an internal staff note or a recorded event.
type TicketMessage struct {
	ID          string
	TicketID    string
	AuthorID    *string
	AuthorLabel string
	Kind        string // message | event
	Staff       bool
	Internal    bool
	Body        string
	CreatedAtMS int64
}

// TicketChange is an update applied together with new thread rows.
type TicketChange struct {
	Status     *string
	Priority   *string
	AssigneeID **string // set to change; *AssigneeID nil = unassign
}

// TicketFilter narrows a ticket listing.
type TicketFilter struct {
	UserID     string // "" = every account
	Status     string // "" = any; "active" = open or pending
	AssigneeID string // "" = any
	Limit      int
}
