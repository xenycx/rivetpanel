package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// AlertService posts deployment and crash notifications to the Discord webhook
// a user granted when connecting Discord ("webhook.incoming") and, when Mailgun
// is configured, emails the bot's owner (unless they switched alert emails off
// on their profile). Sending is best effort: failures are logged and never
// affect the operation that triggered them.
type AlertService struct {
	Store OAuthStore
	Keys  *secrets.Keyring
	Bots  interface {
		GetBot(ctx context.Context, id string) (domain.Bot, error)
	}
	HTTP *http.Client
	Log  *slog.Logger
	Now  func() time.Time
	// Prefs returns a bot's notification preferences; nil sends everything.
	Prefs func(ctx context.Context, botID string) (domain.AlertPrefs, error)
	// Mail and Users enable the email channel; both nil leaves Discord only.
	Mail  *MailService
	Users interface {
		GetUserByID(ctx context.Context, id string) (domain.User, error)
		UserEmailAlerts(ctx context.Context, userID string) (bool, error)
	}
	// Notices, when set, keeps an in-panel notification and sends the email
	// according to the account's per-category preferences (replacing the
	// direct email above for Send/Notify).
	Notices *NotificationService

	mu   sync.Mutex
	last map[string]time.Time // per-bot crash alert throttle
}

const alertCrashInterval = 10 * time.Minute

func (a *AlertService) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// validWebhook accepts only Discord's own webhook endpoints so a stored URL can
// never be used to make the panel call an arbitrary host.
func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	return strings.HasPrefix(u.Path, "/api/webhooks/")
}

func (a *AlertService) webhookFor(ctx context.Context, userID string) (string, bool) {
	accts, err := a.Store.ListOAuthAccounts(ctx, userID)
	if err != nil {
		return "", false
	}
	for _, ac := range accts {
		if ac.Provider != domain.ProviderDiscord || !ac.NotifyEnabled || ac.WebhookCipher == nil || ac.WebhookKeyID == nil {
			continue
		}
		pt, err := a.Keys.Open(ownerNS(userID), secrets.OAuthWebhookName(domain.ProviderDiscord),
			secrets.Sealed{Ciphertext: ac.WebhookCipher, Nonce: ac.WebhookNonce, KeyID: *ac.WebhookKeyID})
		if err != nil || !validWebhook(string(pt)) {
			return "", false
		}
		return string(pt), true
	}
	return "", false
}

// Wants reports whether a bot's owner wants alerts of a kind
// (crash, deploy, backup).
func (a *AlertService) Wants(ctx context.Context, botID, kind string) bool {
	if a == nil {
		return false
	}
	if a.Prefs == nil {
		return true
	}
	p, err := a.Prefs(ctx, botID)
	if err != nil {
		return true
	}
	switch kind {
	case "crash":
		return p.Crash
	case "deploy":
		return p.Deploy
	case "backup":
		return p.Backup
	}
	return true
}

// Send is Notify for the bot alerts category without a link.
func (a *AlertService) Send(ctx context.Context, userID, title, message string) {
	a.Notify(ctx, userID, domain.NotifyBotAlerts, title, message, "")
}

// Notify posts a message to the user's webhook and records/emails it for
// whichever channels they have (the in-panel inbox and email follow the
// account's preferences for the category). The email is sent in the
// background.
func (a *AlertService) Notify(ctx context.Context, userID, category, title, message, link string) {
	if a == nil {
		return
	}
	if _, err := a.discord(ctx, userID, title, message); err != nil && a.Log != nil {
		a.Log.Debug("discord alert not sent", "err", err)
	}
	if a.Notices != nil {
		a.Notices.Notify(ctx, userID, Notice{Category: category, Title: title, Body: message, Link: link})
		return
	}
	a.email(ctx, userID, title, message, false)
}

// BotLink is the interface path of a bot or game server.
func BotLink(b domain.Bot) string {
	if b.IsGame() {
		return "/servers/" + b.ID
	}
	return "/bots/" + b.ID
}

// SendErr is Send that reports whether a message reached the user: it
// succeeds when Discord or email accepted it, and waits for the email.
func (a *AlertService) SendErr(ctx context.Context, userID, title, message string) error {
	sent, derr := a.discord(ctx, userID, title, message)
	if sent {
		a.email(ctx, userID, title, message, false)
		return nil
	}
	esent, eerr := a.email(ctx, userID, title, message, true)
	if esent {
		return nil
	}
	if eerr != nil && !errors.Is(eerr, errNoEmail) {
		return eerr
	}
	return derr
}

var errNoEmail = errors.New("no email channel")

// email sends an alert to the user's address when the mail channel is on for
// them. wait=false sends in the background and reports only that it started.
func (a *AlertService) email(ctx context.Context, userID, title, message string, wait bool) (bool, error) {
	if a.Mail == nil || a.Users == nil || !a.Mail.Enabled(ctx) {
		return false, errNoEmail
	}
	on, err := a.Users.UserEmailAlerts(ctx, userID)
	if err != nil || !on {
		return false, errNoEmail
	}
	u, err := a.Users.GetUserByID(ctx, userID)
	if err != nil || u.Disabled {
		return false, errNoEmail
	}
	body := mail.Alert(title, clip(message, 1500))
	if !wait {
		return a.Mail.Queue(u.Email, body, "alert"), nil
	}
	if _, err := a.Mail.Send(ctx, u.Email, body, "alert"); err != nil {
		return false, err
	}
	return true, nil
}

// discord posts to the user's webhook and reports whether Discord accepted it.
func (a *AlertService) discord(ctx context.Context, userID, title, message string) (bool, error) {
	hook, ok := a.webhookFor(ctx, userID)
	if !ok {
		return false, domain.Invalid("no Discord webhook")
	}
	body, _ := json.Marshal(map[string]any{
		"username": "RivetPanel", "allowed_mentions": map[string]any{"parse": []string{}},
		"embeds": []map[string]any{{"title": clip(title, 200), "description": clip(message, 1500)}},
	})
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		a.warn("discord webhook", errors.New("request failed"))
		return false, domain.Invalid("Discord could not be reached")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode/100 != 2 {
		a.warn("discord webhook", fmt.Errorf("status %d", res.StatusCode))
		return false, domain.Invalid(fmt.Sprintf("Discord refused the message (HTTP %d); reconnect Discord notifications", res.StatusCode))
	}
	return true, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (a *AlertService) warn(msg string, err error) {
	if a.Log != nil {
		a.Log.Warn(msg, "err", err) // never logs the webhook URL
	}
}

// Watch alerts the owner when a bot settles in a failed state (crash with no
// restart left, or a failure the restart policy will not retry), at most once
// per bot per interval. It returns when ctx ends.
func (a *AlertService) Watch(ctx context.Context, bus *events.Bus) {
	sub := bus.SubscribeAll()
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-sub.C:
			// A taken host port is a configuration problem shown on the
			// server's page, not a crash: it is not retried or alerted.
			if st.ObservedState != "failed" || st.LastError == "" || st.DesiredState != domain.DesiredRunning || st.Reason == domain.ReasonPortConflict {
				continue
			}
			a.mu.Lock()
			if a.last == nil {
				a.last = map[string]time.Time{}
			}
			if t, ok := a.last[st.BotID]; ok && a.now().Sub(t) < alertCrashInterval {
				a.mu.Unlock()
				continue
			}
			a.last[st.BotID] = a.now()
			a.mu.Unlock()
			bot, err := a.Bots.GetBot(ctx, st.BotID)
			if err != nil || !a.Wants(ctx, st.BotID, "crash") {
				continue
			}
			a.Notify(ctx, bot.OwnerID, domain.NotifyBotAlerts, "⚠️ "+bot.Name+" needs attention", st.LastError, BotLink(bot))
		}
	}
}
