package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// TicketKey is a ticket's human reference: "RT-" and its number padded to
// six digits (RT-000001 … RT-999999; later numbers simply grow wider).
func TicketKey(n int64) string { return fmt.Sprintf("RT-%06d", n) }

// ParseTicketKey reads "RT-000042" (any case, any padding) as 42.
func ParseTicketKey(s string) (int64, bool) {
	if len(s) < 4 || !strings.EqualFold(s[:3], "RT-") {
		return 0, false
	}
	n, err := strconv.ParseInt(s[3:], 10, 64)
	if err != nil || n <= 0 || len(s) > 24 {
		return 0, false
	}
	return n, true
}

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
	// People as the queries fill them (display name, avatar, picture variant).
	Requester   Person
	Assignee    Person
	LabelIDs    []string // filled by queries
	CreatedAtMS int64
	UpdatedAtMS int64
	ClosedAtMS  *int64
	Messages    int // listing only: messages visible to the reader
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
	// Filled by queries.
	Author      Person // zero when the author account is gone
	EditedAtMS  *int64
	DeletedAtMS *int64
	DeletedBy   string // label of the account that removed it
	Mentions    []string
	Reactions   []TicketReaction
}

// Person is how an account appears on a ticket: id, name and picture.
type Person struct {
	ID          string
	Email       string
	DisplayName string
	HasAvatar   bool
	AvatarAtMS  int64 // cache key of an uploaded avatar
	LogoVariant int64
}

// Reaction kinds.
var TicketReactionKinds = []string{"ack", "thanks", "seen", "solved"}

// TicketReaction is one account's reaction to a message.
type TicketReaction struct {
	Kind   string
	UserID string
	Staff  bool // the reacting account answered the ticket as staff (not the requester)
	Label  string
}

// TicketRevision is an earlier body of an edited message.
type TicketRevision struct {
	ID          string
	MessageID   string
	Body        string
	EditorLabel string
	CreatedAtMS int64
}

// Label colors (palette tokens).
var TicketLabelColors = []string{"action", "data", "run", "warn", "fail", "violet", "cyan", "slate"}

// TicketLabel is a staff-only tag on tickets.
type TicketLabel struct {
	ID          string
	Name        string
	Color       string
	CreatedAtMS int64
	Tickets     int // listing only
}

// SavedReply is an answer the support team reuses.
type SavedReply struct {
	ID          string
	Title       string
	Body        string
	CreatedBy   string
	UpdatedAtMS int64
}

// TicketChange is an update applied together with new thread rows.
type TicketChange struct {
	Status     *string
	Priority   *string
	AssigneeID **string // set to change; *AssigneeID nil = unassign
	Subject    *string
	Category   *string
	BotID      **string // set to change; *BotID nil = unlink
	BotName    *string
}

// TicketFilter narrows a ticket listing.
type TicketFilter struct {
	UserID     string // "" = every account
	Status     string // "" = any; "active" = open or pending
	AssigneeID string // "" = any
	Limit      int
}
