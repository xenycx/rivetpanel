package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

const (
	verifyPrefix   = "bpv_"
	verifyTTL      = 24 * time.Hour
	verifyCooldown = 60 * time.Second
)

// EmailVerificationService confirms that an account's email address (or a
// new address it wants to switch to) belongs to the person: a one-use link
// is emailed; only the SHA-256 of its token is stored.
type EmailVerificationService struct {
	Auth     *AuthService
	Mail     *MailService
	Settings *SettingsService
	Now      func() time.Time
}

func (v *EmailVerificationService) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *EmailVerificationService) publicURL(ctx context.Context) string {
	if v.Settings == nil {
		return ""
	}
	e, err := v.Settings.Effective(ctx)
	if err != nil {
		return ""
	}
	return e.PublicURL
}

// Available reports whether links can be sent: email is configured and the
// panel knows its own address.
func (v *EmailVerificationService) Available(ctx context.Context) bool {
	return v != nil && v.Mail.Enabled(ctx) && v.publicURL(ctx) != ""
}

var errNoVerifyMail = domain.Invalid("this panel cannot send email yet; ask an administrator to set up email or to mark your address as verified")

// errVerifyCooldown is the resend rate limit (one link per account per
// minute; the HTTP route adds a per-account and per-address limit).
var errVerifyCooldown = domain.Invalid("a verification email was sent less than a minute ago; check your inbox or try again shortly")

func (v *EmailVerificationService) issue(ctx context.Context, u domain.User, email string, change bool) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := verifyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := v.now()
	err := v.Auth.Store.CreateEmailVerification(ctx, uuid.NewString(), u.ID, email, auth.HashToken(token),
		now.UnixMilli(), now.Add(verifyTTL).UnixMilli(), verifyCooldown.Milliseconds())
	if errors.Is(err, domain.ErrConflict) {
		return errVerifyCooldown
	}
	if err != nil {
		return err
	}
	// The token travels in the URL fragment, so browsers never send it to the
	// server in a request line and proxies do not log it.
	if !v.Mail.Queue(email, mail.VerifyEmail(v.publicURL(ctx)+"/verify-email#"+token, int(verifyTTL.Hours()), change), "email-verification") {
		return domain.Invalid("the verification email could not be sent right now; try again in a minute")
	}
	return nil
}

// Send emails a verification link for the account's current address.
func (v *EmailVerificationService) Send(ctx context.Context, u domain.User) error {
	if u.EmailVerified {
		return domain.Invalid("your email address is already verified")
	}
	if !v.Available(ctx) {
		return errNoVerifyMail
	}
	return v.issue(ctx, u, u.Email, false)
}

// Pending returns the address and send time of the account's outstanding
// link ("" when there is none).
func (v *EmailVerificationService) Pending(ctx context.Context, u domain.User) (string, int64) {
	email, at, err := v.Auth.Store.PendingEmailVerification(ctx, u.ID, v.now().UnixMilli())
	if err != nil {
		return "", 0
	}
	return email, at
}

// RequestChange starts an email change: the account confirms with its
// password (or, without one, within RecentAuthWindow of signing in) and the
// new address gets a link. The account keeps its current address until the
// link is used; the current address is told about the request.
func (v *EmailVerificationService) RequestChange(ctx context.Context, actor domain.User, sessionToken, password, newEmail string) error {
	norm, err := NormalizeEmail(newEmail)
	if err != nil {
		return err
	}
	u, sess, err := v.Auth.AuthenticateSession(ctx, sessionToken)
	if err != nil || u.ID != actor.ID {
		return domain.ErrUnauthorized
	}
	if strings.EqualFold(norm, u.Email) {
		return domain.Invalid("that is already your email address")
	}
	if u.PasswordHash != "" {
		ok, verr := v.Auth.Hasher.Verify(ctx, password, u.PasswordHash)
		if verr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if verr != nil || !ok {
			return domain.Invalid("the current password is not correct")
		}
	} else if v.now().UnixMilli()-sess.AuthAtMS > RecentAuthWindow.Milliseconds() {
		return domain.Invalid("sign out and sign in again with your provider, then change the address within 10 minutes")
	}
	if !v.Available(ctx) {
		return domain.Invalid("this panel cannot send email yet, so the new address cannot be confirmed; ask an administrator to change it")
	}
	if other, err := v.Auth.Store.GetUserByEmail(ctx, norm); err == nil && other.ID != u.ID {
		return domain.Invalid("another account already uses that email address")
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err := v.issue(ctx, u, norm, true); err != nil {
		return err
	}
	v.Mail.Notify(u.Email, "Someone asked to change this account's email address to "+norm+". The address changes only after the link sent there is used.")
	return nil
}

var errBadVerify = domain.Invalid("this verification link is not valid or has expired; ask for a new one")

// Confirm uses a link. It returns the account and, for an email change, the
// previous address (which is told about the change).
func (v *EmailVerificationService) Confirm(ctx context.Context, token string) (domain.User, string, error) {
	if !strings.HasPrefix(token, verifyPrefix) || len(token) > 80 {
		return domain.User{}, "", errBadVerify
	}
	userID, oldEmail, newEmail, err := v.Auth.Store.ConsumeEmailVerification(ctx, auth.HashToken(token), v.now().UnixMilli())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, "", errBadVerify
	}
	if errors.Is(err, sqlite.ErrEmailTaken) {
		return domain.User{}, "", err
	}
	if err != nil {
		return domain.User{}, "", err
	}
	u, err := v.Auth.Store.GetUserByID(ctx, userID)
	if err != nil {
		return domain.User{}, "", err
	}
	if !strings.EqualFold(oldEmail, newEmail) {
		v.Mail.Notify(oldEmail, "The email address of this account was changed to "+newEmail+". Sign-in now uses the new address.")
		return u, oldEmail, nil
	}
	return u, "", nil
}

// ---- administration ----

// SetEmail changes an account's address as an account manager; the new
// address is unverified. Delegated managers cannot change the address of an
// account they do not outrank (see manageable).
func (s *AuthService) SetEmail(ctx context.Context, actor domain.User, id, email string) (domain.User, error) {
	if !actor.Can(domain.PermUsersManage) {
		return domain.User{}, domain.ErrForbidden
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	target, err := s.Store.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if err := manageable(actor, target); err != nil {
		return domain.User{}, err
	}
	if strings.EqualFold(target.Email, norm) {
		return target, nil
	}
	if err := s.Store.SetUserEmail(ctx, id, norm, s.now().UnixMilli()); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.User{}, domain.Invalid("another account already uses that email address")
		}
		return domain.User{}, err
	}
	return target, nil
}

// SetEmailVerified marks an account's address verified or not (for panels
// without email, or after checking the address another way).
func (s *AuthService) SetEmailVerified(ctx context.Context, actor domain.User, id string, verified bool) error {
	if !actor.Can(domain.PermUsersManage) {
		return domain.ErrForbidden
	}
	if !actor.IsAdmin() {
		target, err := s.Store.GetUserByID(ctx, id)
		if err != nil {
			return err
		}
		if err := manageable(actor, target); err != nil {
			return err
		}
		if actor.ID == id {
			return domain.Invalid("you cannot verify your own address this way")
		}
	}
	return s.Store.SetEmailVerified(ctx, id, verified, s.now().UnixMilli())
}
