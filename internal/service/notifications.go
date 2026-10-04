package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
)

// Notification limits. Every account keeps at most notificationKeep rows
// (older ones are dropped when a new one arrives) and rows older than
// NotificationRetention are pruned periodically.
const (
	notificationKeep      = 200
	NotificationRetention = 90 * 24 * time.Hour
	notificationPageMax   = 100
	notificationFanoutMax = 1000 // accounts reached by one permission-wide notice
	notificationTitleMax  = 200
	notificationBodyMax   = 2000
)

// NotificationStore persists notifications and preferences.
type NotificationStore interface {
	InsertNotifications(ctx context.Context, ns []domain.Notification, keep int) error
	ListNotifications(ctx context.Context, userID string, beforeMS int64, unreadOnly bool, limit int) ([]domain.Notification, error)
	CountUnreadNotifications(ctx context.Context, userID string) (int, error)
	MarkNotificationsRead(ctx context.Context, userID string, ids []string, nowMS int64) (int, error)
	DeleteNotification(ctx context.Context, userID, id string) error
	DeleteReadNotifications(ctx context.Context, userID string) (int, error)
	PruneNotifications(ctx context.Context, cutoffMS int64) (int, error)
	NotificationPrefs(ctx context.Context, userID string) (map[string]domain.NotificationPref, error)
	SetNotificationPrefs(ctx context.Context, userID string, ps []domain.NotificationPref) error
	GetUserByID(ctx context.Context, id string) (domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	UserEmailAlerts(ctx context.Context, userID string) (bool, error)
}

// Notice is what a producer hands the notification service.
type Notice struct {
	Category string
	Title    string
	Body     string
	Link     string // same-origin path ("/bots/…"), or ""
}

// NotificationService keeps each account's in-panel notification inbox and
// sends the matching email when the account wants it. Producers (alerts,
// deployments, backups, nodes, sharing, announcements, tickets) call Notify;
// it never fails the operation that triggered it.
type NotificationService struct {
	Store    NotificationStore
	Mail     *MailService     // nil: in-panel only
	Settings *SettingsService // panel address for links in emails; nil = no link
	Log      *slog.Logger
	Now      func() time.Time
}

func (n *NotificationService) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *NotificationService) warn(msg string, err error) {
	if n.Log != nil && err != nil {
		n.Log.Warn(msg, "err", err)
	}
}

func cleanNotice(x Notice) Notice {
	x.Title = clip(strings.TrimSpace(x.Title), notificationTitleMax)
	x.Body = clip(strings.TrimSpace(x.Body), notificationBodyMax)
	if x.Title == "" {
		x.Title = "Notification"
	}
	// Only same-origin paths: never a scheme, host or protocol-relative URL.
	if !strings.HasPrefix(x.Link, "/") || strings.HasPrefix(x.Link, "//") || strings.ContainsAny(x.Link, "\\\r\n") || len(x.Link) > 300 {
		x.Link = ""
	}
	return x
}

// Notify delivers a notice to one account.
func (n *NotificationService) Notify(ctx context.Context, userID string, x Notice) {
	if n == nil || userID == "" {
		return
	}
	u, err := n.Store.GetUserByID(ctx, userID)
	if err != nil {
		return
	}
	n.deliver(ctx, []domain.User{u}, x)
}

// NotifyMany delivers a notice to several accounts (duplicates and except
// are skipped).
func (n *NotificationService) NotifyMany(ctx context.Context, userIDs []string, except string, x Notice) {
	if n == nil {
		return
	}
	seen := map[string]bool{except: true}
	var us []domain.User
	for _, id := range userIDs {
		if seen[id] || id == "" {
			continue
		}
		seen[id] = true
		if u, err := n.Store.GetUserByID(ctx, id); err == nil {
			us = append(us, u)
		}
	}
	n.deliver(ctx, us, x)
}

// HoldersOf lists enabled accounts whose role holds perm (built-in
// administrators always do), at most notificationFanoutMax.
func (n *NotificationService) HoldersOf(ctx context.Context, perm string) []domain.User {
	if n == nil {
		return nil
	}
	all, err := n.Store.ListUsers(ctx)
	if err != nil {
		n.warn("list notification recipients", err)
		return nil
	}
	var out []domain.User
	for _, u := range all {
		if !u.Disabled && u.Can(perm) {
			out = append(out, u)
			if len(out) >= notificationFanoutMax {
				break
			}
		}
	}
	return out
}

// NotifyHolders delivers a notice to every account holding perm.
func (n *NotificationService) NotifyHolders(ctx context.Context, perm string, x Notice) {
	if n == nil {
		return
	}
	n.deliver(ctx, n.HoldersOf(ctx, perm), x)
}

// deliver applies each recipient's preferences: an in-panel row, an email,
// both or neither. Disabled accounts and accounts lacking the category's
// permission receive nothing.
func (n *NotificationService) deliver(ctx context.Context, us []domain.User, x Notice) {
	cat, ok := domain.NotificationCategoryByName(x.Category)
	if !ok {
		n.warn("unknown notification category", domain.Invalid(x.Category))
		return
	}
	x = cleanNotice(x)
	now := n.now().UnixMilli()
	mailOn := n.Mail != nil && cat.Email && n.Mail.Enabled(ctx)
	var rows []domain.Notification
	for _, u := range us {
		if u.Disabled || (cat.Permission != "" && !u.Can(cat.Permission)) {
			continue
		}
		p := n.prefFor(ctx, u.ID, cat.Name)
		if p.InPanel {
			rows = append(rows, domain.Notification{ID: uuid.NewString(), UserID: u.ID, Category: cat.Name,
				Title: x.Title, Body: x.Body, Link: x.Link, CreatedAtMS: now})
		}
		if mailOn && p.Email {
			n.email(ctx, u, cat, x)
		}
	}
	if err := n.Store.InsertNotifications(ctx, rows, notificationKeep); err != nil {
		n.warn("store notifications", err)
	}
}

// email sends one notification email. Addresses that are not verified
// never receive them (the address may not belong to the account holder);
// alert categories also need the profile's "Alert emails" switch.
func (n *NotificationService) email(ctx context.Context, u domain.User, cat domain.NotificationCategory, x Notice) {
	if !u.EmailVerified || u.Email == "" {
		return
	}
	if cat.Alert {
		on, err := n.Store.UserEmailAlerts(ctx, u.ID)
		if err != nil || !on {
			return
		}
	}
	link := ""
	if x.Link != "" && n.Settings != nil {
		if e, err := n.Settings.Effective(ctx); err == nil && e.PublicURL != "" {
			link = strings.TrimRight(e.PublicURL, "/") + x.Link
		}
	}
	body := mail.Notification(x.Title, clip(x.Body, 1500), link)
	if cat.Alert {
		body = mail.Alert(x.Title, clip(x.Body, 1500))
	}
	n.Mail.Queue(u.Email, body, "notification-"+cat.Name)
}

func (n *NotificationService) prefFor(ctx context.Context, userID, category string) domain.NotificationPref {
	ps, err := n.Store.NotificationPrefs(ctx, userID)
	if p, ok := ps[category]; err == nil && ok {
		return p
	}
	return domain.NotificationPref{Category: category, InPanel: true, Email: true}
}

// ---- the account's own inbox ----

// inboxActor refuses API clients: notifications can carry ticket replies and
// alerts about any of the account's resources, so they stay with browser
// sessions (the routes are also session-only).
func inboxActor(actor domain.User) error {
	if actor.ID == "" || actor.Client != nil {
		return domain.ErrForbidden
	}
	return nil
}

// List returns the actor's notifications, newest first, and the unread count.
func (n *NotificationService) List(ctx context.Context, actor domain.User, beforeMS int64, unreadOnly bool, limit int) ([]domain.Notification, int, error) {
	if err := inboxActor(actor); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > notificationPageMax {
		limit = 30
	}
	ns, err := n.Store.ListNotifications(ctx, actor.ID, beforeMS, unreadOnly, limit)
	if err != nil {
		return nil, 0, err
	}
	unread, err := n.Store.CountUnreadNotifications(ctx, actor.ID)
	return ns, unread, err
}

// Unread counts the actor's unread notifications.
func (n *NotificationService) Unread(ctx context.Context, actor domain.User) (int, error) {
	if err := inboxActor(actor); err != nil {
		return 0, err
	}
	return n.Store.CountUnreadNotifications(ctx, actor.ID)
}

// MarkRead marks some (ids) or all of the actor's notifications read.
func (n *NotificationService) MarkRead(ctx context.Context, actor domain.User, ids []string, all bool) (int, error) {
	if err := inboxActor(actor); err != nil {
		return 0, err
	}
	if all {
		ids = nil
	} else if len(ids) == 0 || len(ids) > notificationPageMax {
		return 0, domain.Invalid("give 1 to 100 notification ids, or all")
	}
	return n.Store.MarkNotificationsRead(ctx, actor.ID, ids, n.now().UnixMilli())
}

// Delete removes one of the actor's notifications (another account's
// notification answers not found).
func (n *NotificationService) Delete(ctx context.Context, actor domain.User, id string) error {
	if err := inboxActor(actor); err != nil {
		return err
	}
	return n.Store.DeleteNotification(ctx, actor.ID, id)
}

// ClearRead removes the actor's read notifications.
func (n *NotificationService) ClearRead(ctx context.Context, actor domain.User) (int, error) {
	if err := inboxActor(actor); err != nil {
		return 0, err
	}
	return n.Store.DeleteReadNotifications(ctx, actor.ID)
}

// PrefView is one category with the account's choice.
type PrefView struct {
	domain.NotificationCategory
	InPanel bool `json:"in_panel"`
	EmailOn bool `json:"email_on"`
}

// Prefs returns the categories the actor can receive with its choices.
func (n *NotificationService) Prefs(ctx context.Context, actor domain.User) ([]PrefView, error) {
	if err := inboxActor(actor); err != nil {
		return nil, err
	}
	ps, err := n.Store.NotificationPrefs(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	out := []PrefView{}
	for _, c := range domain.NotificationCategories {
		if c.Permission != "" && !actor.Can(c.Permission) {
			continue
		}
		v := PrefView{NotificationCategory: c, InPanel: true, EmailOn: true}
		if p, ok := ps[c.Name]; ok {
			v.InPanel, v.EmailOn = p.InPanel, p.Email
		}
		if !c.Email {
			v.EmailOn = false
		}
		out = append(out, v)
	}
	return out, nil
}

// SetPrefs stores the actor's choices for the given categories.
func (n *NotificationService) SetPrefs(ctx context.Context, actor domain.User, in []domain.NotificationPref) error {
	if err := inboxActor(actor); err != nil {
		return err
	}
	if len(in) == 0 || len(in) > len(domain.NotificationCategories) {
		return domain.Invalid("give the categories to change")
	}
	seen := map[string]bool{}
	for i, p := range in {
		c, ok := domain.NotificationCategoryByName(p.Category)
		if !ok || seen[p.Category] {
			return domain.Invalid("unknown or repeated notification category")
		}
		seen[p.Category] = true
		if !c.Email {
			in[i].Email = false
		}
	}
	return n.Store.SetNotificationPrefs(ctx, actor.ID, in)
}

// Prune deletes notifications older than the retention period.
func (n *NotificationService) Prune(ctx context.Context) (int, error) {
	return n.Store.PruneNotifications(ctx, n.now().Add(-NotificationRetention).UnixMilli())
}

// Announce posts an administrator's announcement to the in-panel inbox of
// the audience (every enabled account, or administrators only). The HTML is
// reduced to plain text; nothing is rendered as HTML in the panel. It
// returns how many accounts it was offered to (their preferences decide).
func (n *NotificationService) Announce(ctx context.Context, actor domain.User, subject, htmlBody, audience string) (int, error) {
	if !actor.Can(domain.PermMailAnnounce) {
		return 0, domain.ErrForbidden
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len([]rune(subject)) > 150 {
		return 0, domain.Invalid("the subject must be 1 to 150 characters")
	}
	if len(htmlBody) > mail.MaxAnnouncementHTML {
		return 0, domain.Invalid("the message is too large; keep it under 80 KB (host large images elsewhere)")
	}
	plain := strings.TrimSpace(mail.PlainText(htmlBody))
	if plain == "" {
		return 0, domain.Invalid("the message is empty")
	}
	if audience != AudienceAll && audience != AudienceAdmins {
		return 0, domain.Invalid("choose who should receive it")
	}
	all, err := n.Store.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	var us []domain.User
	for _, u := range all {
		if u.Disabled || (audience == AudienceAdmins && u.Role != domain.RoleAdmin) {
			continue
		}
		us = append(us, u)
	}
	n.deliver(ctx, us, Notice{Category: domain.NotifyAnnouncements, Title: subject, Body: plain})
	return len(us), nil
}

// NodeNotices tells accounts that manage nodes when a node's agent has been
// offline for longer than Grace (a quick reconnect sends nothing) and when it
// comes back after such a notice.
type NodeNotices struct {
	Notices *NotificationService
	Name    func(ctx context.Context, nodeID string) string
	Online  func(nodeID string) bool
	Grace   time.Duration

	mu      sync.Mutex
	offline map[string]bool        // nodes an offline notice went out for
	timers  map[string]*time.Timer // pending offline checks
}

// Disconnected schedules the offline check. It returns at once.
func (w *NodeNotices) Disconnected(ctx context.Context, nodeID string) {
	if w == nil || w.Notices == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timers == nil {
		w.timers, w.offline = map[string]*time.Timer{}, map[string]bool{}
	}
	if t := w.timers[nodeID]; t != nil {
		t.Stop()
	}
	w.timers[nodeID] = time.AfterFunc(w.Grace, func() {
		w.mu.Lock()
		delete(w.timers, nodeID)
		if ctx.Err() != nil || w.offline[nodeID] || (w.Online != nil && w.Online(nodeID)) {
			w.mu.Unlock()
			return
		}
		w.offline[nodeID] = true
		w.mu.Unlock()
		name := w.name(ctx, nodeID)
		w.Notices.NotifyHolders(ctx, domain.PermNodesManage, Notice{Category: domain.NotifyNodes,
			Title: "Node " + name + " is offline", Body: fmt.Sprintf("Its agent has not been connected for %s. Servers on it cannot be controlled until it reconnects.", w.Grace),
			Link: "/admin/nodes"})
	})
}

// Connected cancels a pending check and, after an offline notice, says the
// node is back.
func (w *NodeNotices) Connected(ctx context.Context, nodeID string) {
	if w == nil || w.Notices == nil {
		return
	}
	w.mu.Lock()
	if t := w.timers[nodeID]; t != nil {
		t.Stop()
		delete(w.timers, nodeID)
	}
	was := w.offline[nodeID]
	delete(w.offline, nodeID)
	w.mu.Unlock()
	if was {
		w.Notices.NotifyHolders(ctx, domain.PermNodesManage, Notice{Category: domain.NotifyNodes,
			Title: "Node " + w.name(ctx, nodeID) + " is back online", Link: "/admin/nodes"})
	}
}

func (w *NodeNotices) name(ctx context.Context, nodeID string) string {
	if w.Name != nil {
		if n := w.Name(ctx, nodeID); n != "" {
			return n
		}
	}
	return nodeID
}
