package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
)

// Mail volume guards. They protect the Mailgun account and recipients from a
// loop or abuse; they are not a delivery guarantee.
const (
	mailPerRecipientHour = 20
	mailPerHour          = 300
	mailMaxInFlight      = 2
	mailSendTimeout      = 20 * time.Second
	mailTrackedMax       = 512
)

// MailService sends the panel's transactional email. It keeps no queue and no
// long-lived goroutine: a message sent with Queue lives in a goroutine only
// for the duration of one HTTPS request, and at most mailMaxInFlight run at
// once. Messages that cannot start right away are dropped and logged, never
// buffered, so memory stays flat when Mailgun is slow or down.
type MailService struct {
	Settings *SettingsService
	// Recipients lists accounts for announcements; nil disables them.
	Recipients RecipientStore
	Client     *mail.Client
	Log        *slog.Logger
	Now        func() time.Time

	mu            sync.Mutex
	inflight      int
	broadcasting  bool
	lastBroadcast time.Time
	global        []int64            // send times within the last hour
	perTo         map[string][]int64 // per recipient
}

func (m *MailService) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *MailService) client() *mail.Client {
	if m.Client != nil {
		return m.Client
	}
	return &mail.Client{}
}

// Config returns the effective Mailgun configuration.
func (m *MailService) Config(ctx context.Context) (mail.Config, error) {
	if m == nil || m.Settings == nil {
		return mail.Config{}, nil
	}
	return m.Settings.MailConfig(ctx)
}

// Enabled reports whether email can be sent.
func (m *MailService) Enabled(ctx context.Context) bool {
	if m == nil {
		return false
	}
	cfg, err := m.Config(ctx)
	return err == nil && cfg.Configured()
}

func prune(ts []int64, cutoff int64) []int64 {
	i := 0
	for i < len(ts) && ts[i] <= cutoff {
		i++
	}
	return ts[i:]
}

// allow records a send and reports whether the volume guards permit it.
func (m *MailService) allow(to string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UnixMilli()
	cutoff := now - time.Hour.Milliseconds()
	m.global = prune(m.global, cutoff)
	if len(m.global) >= mailPerHour {
		return false
	}
	if m.perTo == nil {
		m.perTo = map[string][]int64{}
	}
	if len(m.perTo) > mailTrackedMax {
		for k, v := range m.perTo {
			if v = prune(v, cutoff); len(v) == 0 {
				delete(m.perTo, k)
			} else {
				m.perTo[k] = v
			}
		}
		if len(m.perTo) > mailTrackedMax { // still full of live entries: start over
			m.perTo = map[string][]int64{}
		}
	}
	to = strings.ToLower(to)
	ts := prune(m.perTo[to], cutoff)
	if len(ts) >= mailPerRecipientHour {
		m.perTo[to] = ts
		return false
	}
	m.perTo[to] = append(ts, now)
	m.global = append(m.global, now)
	return true
}

// Send delivers one message now and returns Mailgun's id. The error text is
// safe to show an administrator.
func (m *MailService) Send(ctx context.Context, to string, b mail.Body, tag string) (string, error) {
	cfg, err := m.Config(ctx)
	if err != nil {
		return "", err
	}
	if !cfg.Configured() {
		return "", domain.Invalid("email is not set up: add the Mailgun key, domain and sender in Panel settings")
	}
	if !m.allow(to) {
		return "", domain.Invalid("too many emails were sent recently; try again later")
	}
	return m.client().Send(ctx, cfg, mail.Message{To: to, Subject: b.Subject, Text: b.Text, HTML: b.HTML, Tag: tag})
}

// Queue sends a message in the background and reports whether it was
// started. It never blocks the caller; failures are logged without the
// recipient's address or the key.
func (m *MailService) Queue(to string, b mail.Body, tag string) bool {
	if m == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
	if !m.Enabled(ctx) {
		cancel()
		return false
	}
	m.mu.Lock()
	if m.inflight >= mailMaxInFlight {
		m.mu.Unlock()
		cancel()
		m.warn("email dropped: too many sends in progress", tag, nil)
		return false
	}
	m.inflight++
	m.mu.Unlock()
	go func() {
		defer cancel()
		defer func() { m.mu.Lock(); m.inflight--; m.mu.Unlock() }()
		if _, err := m.Send(ctx, to, b, tag); err != nil {
			m.warn("email not sent", tag, err)
		}
	}()
	return true
}

func (m *MailService) warn(msg, tag string, err error) {
	if m.Log == nil {
		return
	}
	if err != nil {
		m.Log.Warn(msg, "kind", tag, "err", err.Error())
		return
	}
	m.Log.Warn(msg, "kind", tag)
}

// Notify tells a user about a change to their account's security. Security
// notices are not switchable: a person must be able to learn that their
// password or two-step sign-in changed.
func (m *MailService) Notify(email, what string) {
	m.Queue(email, mail.SecurityNotice(what), "security")
}

// TestResult is what an administrator sees after sending a test email.
type TestResult struct {
	MessageID string `json:"message_id"`
	Domain    string `json:"domain"`
	State     string `json:"state"`
	Sandbox   bool   `json:"sandbox"`
	Verified  bool   `json:"dns_verified"`
}

// Test checks the Mailgun key and domain and sends one message to the
// administrator's chosen address.
func (m *MailService) Test(ctx context.Context, actor domain.User, to string) (TestResult, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return TestResult{}, domain.ErrForbidden
	}
	addr, err := mail.ValidateAddress(to)
	if err != nil {
		return TestResult{}, domain.Invalid(err.Error())
	}
	cfg, err := m.Config(ctx)
	if err != nil {
		return TestResult{}, err
	}
	if !cfg.Configured() {
		return TestResult{}, domain.Invalid("fill in the Mailgun key, domain and sender, save, then send the test")
	}
	info, err := m.client().Check(ctx, cfg)
	if err != nil {
		return TestResult{}, domain.Invalid(err.Error())
	}
	id, err := m.Send(ctx, addr, mail.Test(), "test")
	if err != nil {
		return TestResult{}, domain.Invalid(err.Error())
	}
	return TestResult{MessageID: id, Domain: info.Name, State: info.State, Sandbox: info.Type == "sandbox", Verified: info.Verified}, nil
}

// ---- announcements ----

// Announcement audiences and kinds.
const (
	AudienceAll    = "all"    // every enabled account
	AudienceAdmins = "admins" // enabled administrators only
	KindNotice     = "notice" // policy and service notices: sent to the whole audience
	KindNews       = "news"   // optional: skips people who turned news emails off
)

// maxBroadcastRecipients and broadcastGap bound one announcement and how often
// they can go out.
const (
	maxBroadcastRecipients = 1000
	broadcastGap           = time.Minute
)

// RecipientStore lists the accounts an announcement can go to.
type RecipientStore interface {
	ListMailRecipients(ctx context.Context) ([]domain.MailRecipient, error)
}

// BroadcastResult is the outcome of sending an announcement.
type BroadcastResult struct {
	Recipients int    `json:"recipients"`
	Sent       int    `json:"sent"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"` // opted out of news
	Error      string `json:"error,omitempty"`
}

func pick(rs []domain.MailRecipient, audience, kind string) (to []string, skipped int) {
	for _, r := range rs {
		if audience == AudienceAdmins && !r.Admin {
			continue
		}
		if kind == KindNews && !r.News {
			skipped++
			continue
		}
		to = append(to, r.Email)
	}
	return to, skipped
}

// Audience counts who an announcement would reach.
func (m *MailService) Audience(ctx context.Context, actor domain.User) (map[string]int, error) {
	if !actor.Can(domain.PermMailAnnounce) {
		return nil, domain.ErrForbidden
	}
	if m.Recipients == nil {
		return map[string]int{}, nil
	}
	rs, err := m.Recipients.ListMailRecipients(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, aud := range []string{AudienceAll, AudienceAdmins} {
		for _, kind := range []string{KindNotice, KindNews} {
			to, _ := pick(rs, aud, kind)
			out[aud+"_"+kind] = len(to)
		}
	}
	return out, nil
}

// Broadcast sends an administrator-written announcement. With test set it
// goes only to the sender, so they can see how it arrives. It sends in batches
// (each person sees only their own address), waits for Mailgun's answers, and
// holds no state afterwards: there is no queue and no goroutine. Only one
// announcement can run at a time and they are spaced a minute apart (a test
// is exempt) so a double click cannot send twice.
func (m *MailService) Broadcast(ctx context.Context, actor domain.User, subject, htmlBody, audience, kind string, test bool) (BroadcastResult, error) {
	if !actor.Can(domain.PermMailAnnounce) {
		return BroadcastResult{}, domain.ErrForbidden
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len([]rune(subject)) > 150 {
		return BroadcastResult{}, domain.Invalid("the subject must be 1 to 150 characters")
	}
	if strings.TrimSpace(mail.PlainText(htmlBody)) == "" {
		return BroadcastResult{}, domain.Invalid("the message is empty")
	}
	if len(htmlBody) > mail.MaxAnnouncementHTML {
		return BroadcastResult{}, domain.Invalid("the message is too large; keep it under 80 KB (host large images elsewhere)")
	}
	if audience != AudienceAll && audience != AudienceAdmins {
		return BroadcastResult{}, domain.Invalid("choose who should receive it")
	}
	if kind != KindNotice && kind != KindNews {
		return BroadcastResult{}, domain.Invalid("choose a notice or news")
	}
	cfg, err := m.Config(ctx)
	if err != nil {
		return BroadcastResult{}, err
	}
	if !cfg.Configured() {
		return BroadcastResult{}, domain.Invalid("email is not set up: add the Mailgun key, domain and sender in Panel settings")
	}
	footer := "You receive this because you have an account on this RivetPanel panel."
	if kind == KindNews {
		footer += " You can turn news emails off in Settings → Profile."
	}
	body := mail.Announcement(subject, htmlBody, footer)

	if test {
		if !m.allow(actor.Email) {
			return BroadcastResult{}, domain.Invalid("too many emails were sent recently; try again later")
		}
		subj := "[Test] " + subject
		if _, err := m.client().Send(ctx, cfg, mail.Message{To: actor.Email, Subject: subj, Text: body.Text, HTML: body.HTML, Tag: "announcement-test"}); err != nil {
			return BroadcastResult{}, domain.Invalid(err.Error())
		}
		return BroadcastResult{Recipients: 1, Sent: 1}, nil
	}

	if m.Recipients == nil {
		return BroadcastResult{}, domain.Invalid("announcements are not available")
	}
	rs, err := m.Recipients.ListMailRecipients(ctx)
	if err != nil {
		return BroadcastResult{}, err
	}
	to, skipped := pick(rs, audience, kind)
	if len(to) == 0 {
		return BroadcastResult{Skipped: skipped}, domain.Invalid("nobody is in that audience")
	}
	if len(to) > maxBroadcastRecipients {
		return BroadcastResult{}, domain.Invalid("too many recipients for one announcement")
	}
	m.mu.Lock()
	if m.broadcasting || m.now().Sub(m.lastBroadcast) < broadcastGap {
		m.mu.Unlock()
		return BroadcastResult{}, domain.Invalid("an announcement was sent a moment ago; wait a minute before sending another")
	}
	m.broadcasting = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.broadcasting = false; m.lastBroadcast = m.now(); m.mu.Unlock() }()

	res := BroadcastResult{Recipients: len(to), Skipped: skipped}
	for i := 0; i < len(to); i += mail.MaxBatch {
		end := min(i+mail.MaxBatch, len(to))
		if _, err := m.client().SendBatch(ctx, cfg, to[i:end], mail.Message{Subject: subject, Text: body.Text, HTML: body.HTML, Tag: "announcement"}); err != nil {
			res.Failed += end - i
			res.Error = err.Error()
			m.warn("announcement batch not sent", "announcement", err)
			continue
		}
		res.Sent += end - i
	}
	return res, nil
}
