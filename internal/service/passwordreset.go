package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
)

const (
	resetPrefix   = "bpr_"
	resetTTL      = 60 * time.Minute
	resetCooldown = 60 * time.Second
)

// ResetStore is the persistence password resets need.
type ResetStore interface {
	CreatePasswordReset(ctx context.Context, id, userID string, hash []byte, nowMS, expiresMS, cooldownMS int64) error
	ConsumePasswordReset(ctx context.Context, hash []byte, nowMS int64) (string, error)
}

// PasswordResetService lets people who forgot their password get a one-use
// link by email. It answers every request the same way, whether or not the
// address belongs to an account, so it cannot be used to find accounts.
type PasswordResetService struct {
	Auth     *AuthService
	Store    ResetStore
	Mail     *MailService
	Settings *SettingsService
	Log      *slog.Logger
	Now      func() time.Time
}

func (p *PasswordResetService) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *PasswordResetService) publicURL(ctx context.Context) string {
	if p.Settings == nil {
		return ""
	}
	e, err := p.Settings.Effective(ctx)
	if err != nil {
		return ""
	}
	return e.PublicURL
}

// Available reports whether resets can work: the panel can send email and
// knows its own address (the link points at it).
func (p *PasswordResetService) Available(ctx context.Context) bool {
	return p != nil && p.Mail.Enabled(ctx) && p.publicURL(ctx) != ""
}

// Request emails a reset link when the address belongs to an active account.
// It returns nothing about whether it did.
func (p *PasswordResetService) Request(ctx context.Context, email string) error {
	if !p.Available(ctx) {
		return nil
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return nil
	}
	u, err := p.Auth.Store.GetUserByEmail(ctx, norm)
	if err != nil || u.Disabled {
		return nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := resetPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := p.now()
	err = p.Store.CreatePasswordReset(ctx, uuid.NewString(), u.ID, auth.HashToken(token), now.UnixMilli(), now.Add(resetTTL).UnixMilli(), resetCooldown.Milliseconds())
	if errors.Is(err, domain.ErrConflict) {
		return nil // asked a moment ago; the earlier email is still valid
	}
	if err != nil {
		return err
	}
	// The token travels in the URL fragment so it is never sent to the server
	// by the browser, nor logged by a reverse proxy.
	p.Mail.Queue(u.Email, mail.PasswordReset(p.publicURL(ctx)+"/reset#"+token, int(resetTTL.Minutes())), "password-reset")
	return nil
}

var errBadReset = domain.Invalid("this reset link is not valid or has expired; ask for a new one")

// Confirm sets a new password with a reset link and signs the account out
// everywhere. A weak password is refused before the link is used up.
func (p *PasswordResetService) Confirm(ctx context.Context, token, next string) (domain.User, error) {
	if !strings.HasPrefix(token, resetPrefix) || len(token) > 80 {
		return domain.User{}, errBadReset
	}
	if err := validatePassword(next); err != nil {
		return domain.User{}, err
	}
	hash, err := p.Auth.Hasher.Hash(ctx, next)
	if err != nil {
		return domain.User{}, err
	}
	userID, err := p.Store.ConsumePasswordReset(ctx, auth.HashToken(token), p.now().UnixMilli())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, errBadReset
	}
	if err != nil {
		return domain.User{}, err
	}
	u, err := p.Auth.Store.GetUserByID(ctx, userID)
	if err != nil || u.Disabled {
		return domain.User{}, errBadReset
	}
	if err := p.Auth.Store.SetPassword(ctx, u.ID, hash, nil, p.now().UnixMilli()); err != nil {
		return domain.User{}, err
	}
	// The link reached the account's address, which proves it is theirs.
	if !u.EmailVerified {
		if err := p.Auth.Store.SetEmailVerified(ctx, u.ID, true, p.now().UnixMilli()); err != nil {
			return domain.User{}, err
		}
		u.EmailVerified = true
	}
	p.Mail.Notify(u.Email, "The password for this account was changed using a reset link, and every session was signed out.")
	return u, nil
}
