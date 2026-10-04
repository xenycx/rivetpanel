package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

const MaxAvatarBytes = 64 << 10
const accountInvitePrefix = "bpu_"

// Session is a freshly issued login session.
type Session struct {
	Token       string
	CSRF        string
	ExpiresAtMS int64
	User        domain.User
}

// AuthService handles accounts and sessions.
type AuthService struct {
	Store  Store
	Hasher *auth.Hasher
	TTL    time.Duration
	Now    func() time.Time

	dummyOnce sync.Once
	dummyHash string
}

func (s *AuthService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// dummy returns a valid hash used to equalize timing for unknown accounts.
func (s *AuthService) dummy(ctx context.Context) string {
	s.dummyOnce.Do(func() { s.dummyHash, _ = s.Hasher.Hash(context.Background(), "rivetpanel-dummy-password") })
	return s.dummyHash
}

// CreateUser validates input and creates an account.
func (s *AuthService) CreateUser(ctx context.Context, email, password, role string) (domain.User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	if role != domain.RoleAdmin && role != domain.RoleUser {
		return domain.User{}, domain.Invalid("role must be admin or user")
	}
	if err := validatePassword(password); err != nil {
		return domain.User{}, err
	}
	hash, err := s.Hasher.Hash(ctx, password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now().UnixMilli()
	u := domain.User{ID: uuid.NewString(), Email: email, PasswordHash: hash, Role: role, CreatedAtMS: now, UpdatedAtMS: now}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.User{}, domain.Invalid("email is already registered")
		}
		return domain.User{}, err
	}
	return u, nil
}

// UpdateProfile changes the public display name and, optionally, a compact
// avatar. The browser crops/compresses it; the server still verifies the JPEG
// dimensions so hostile clients cannot smuggle an oversized image.
func (s *AuthService) UpdateProfile(ctx context.Context, actor domain.User, name string, avatar []byte, replaceAvatar bool) (domain.User, error) {
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		return domain.User{}, domain.Invalid("display name must be at most 64 characters")
	}
	if replaceAvatar && len(avatar) > 0 {
		if len(avatar) > MaxAvatarBytes {
			return domain.User{}, domain.Invalid("profile picture must be at most 64 KiB")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(avatar))
		if err != nil || format != "jpeg" || cfg.Width != 128 || cfg.Height != 128 {
			return domain.User{}, domain.Invalid("profile picture must be a 128 by 128 JPEG")
		}
	}
	if err := s.Store.UpdateProfile(ctx, actor.ID, name, avatar, replaceAvatar, s.now().UnixMilli()); err != nil {
		return domain.User{}, err
	}
	return s.Store.GetUserByID(ctx, actor.ID)
}

func accountInviteHash(token string) ([]byte, error) {
	if !strings.HasPrefix(token, accountInvitePrefix) || len(token) > 80 {
		return nil, domain.Invalid("invitation link is not valid")
	}
	return auth.HashToken(token), nil
}

func (s *AuthService) CreateAccountInvite(ctx context.Context, actor domain.User, email, role string, days int) (domain.AccountInvite, string, error) {
	if !actor.Can(domain.PermUsersManage) || (role == domain.RoleAdmin && !actor.IsAdmin()) {
		return domain.AccountInvite{}, "", domain.ErrForbidden
	}
	if email != "" {
		var err error
		email, err = NormalizeEmail(email)
		if err != nil {
			return domain.AccountInvite{}, "", err
		}
	}
	if role != domain.RoleAdmin && role != domain.RoleUser {
		return domain.AccountInvite{}, "", domain.Invalid("role must be admin or user")
	}
	// The built-in user role grants every resource permission; a delegated
	// account manager may only hand it out when it holds them all (as for
	// AssignRole).
	if err := grantable(actor, domain.DefaultUserPermissions()); role == domain.RoleUser && err != nil {
		return domain.AccountInvite{}, "", err
	}
	if days < 1 || days > 14 {
		return domain.AccountInvite{}, "", domain.Invalid("invitation expiry must be between 1 and 14 days")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return domain.AccountInvite{}, "", err
	}
	token := accountInvitePrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	v := domain.AccountInvite{ID: uuid.NewString(), Email: email, Role: role, CreatedBy: actor.ID, CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(time.Duration(days) * 24 * time.Hour).UnixMilli()}
	if err := s.Store.InsertAccountInvite(ctx, v, auth.HashToken(token)); err != nil {
		return domain.AccountInvite{}, "", err
	}
	return v, token, nil
}

func (s *AuthService) ListAccountInvites(ctx context.Context, actor domain.User) ([]domain.AccountInvite, error) {
	if !actor.Can(domain.PermUsersManage) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListAccountInvites(ctx, s.now().UnixMilli())
}

func (s *AuthService) PreviewAccountInvite(ctx context.Context, token string) (domain.AccountInvite, error) {
	h, err := accountInviteHash(token)
	if err != nil {
		return domain.AccountInvite{}, err
	}
	return s.Store.AccountInviteByHash(ctx, h, s.now().UnixMilli())
}

func (s *AuthService) RegisterWithInvite(ctx context.Context, token, email, password string) (domain.User, error) {
	h, err := accountInviteHash(token)
	if err != nil {
		return domain.User{}, err
	}
	v, err := s.Store.AccountInviteByHash(ctx, h, s.now().UnixMilli())
	if err != nil {
		return domain.User{}, err
	}
	email, err = NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	if v.Email != "" && !strings.EqualFold(v.Email, email) {
		return domain.User{}, domain.Invalid("this invitation was sent to a different email address")
	}
	if err := validatePassword(password); err != nil {
		return domain.User{}, err
	}
	hash, err := s.Hasher.Hash(ctx, password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now().UnixMilli()
	u := domain.User{ID: uuid.NewString(), Email: email, PasswordHash: hash, Role: v.Role, CreatedAtMS: now, UpdatedAtMS: now}
	if err := s.Store.UseAccountInvite(ctx, h, u, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.User{}, domain.Invalid("email is already registered")
		}
		return domain.User{}, err
	}
	return u, nil
}

func (s *AuthService) DeleteAccountInvite(ctx context.Context, actor domain.User, id string) error {
	if !actor.Can(domain.PermUsersManage) {
		return domain.ErrForbidden
	}
	return s.Store.DeleteAccountInvite(ctx, id)
}

// checkPassword verifies an email/password pair. All failures, including
// unknown, passwordless and disabled accounts, return ErrUnauthorized.
func (s *AuthService) checkPassword(ctx context.Context, email, password string) (domain.User, error) {
	if len(password) > maxPasswordLen {
		return domain.User{}, domain.ErrUnauthorized
	}
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, domain.ErrUnauthorized
	}
	u, err := s.Store.GetUserByEmail(ctx, norm)
	hash := u.PasswordHash
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, err
		}
		hash = s.dummy(ctx)
	}
	ok, verr := s.Hasher.Verify(ctx, password, hash)
	if verr != nil && ctx.Err() != nil {
		return domain.User{}, ctx.Err()
	}
	if err != nil || verr != nil || !ok || u.Disabled {
		return domain.User{}, domain.ErrUnauthorized
	}
	return u, nil
}

// CheckPassword verifies an email/password pair without issuing a session
// (the first step of a two-step sign-in).
func (s *AuthService) CheckPassword(ctx context.Context, email, password string) (domain.User, error) {
	return s.checkPassword(ctx, email, password)
}

// Login verifies credentials and issues a session.
func (s *AuthService) Login(ctx context.Context, email, password, device string) (Session, error) {
	u, err := s.checkPassword(ctx, email, password)
	if err != nil {
		return Session{}, err
	}
	return s.IssueSessionFor(ctx, u, device)
}

// IssueSession creates a session for an already-authenticated user (password
// or OAuth). The caller must have checked that the account is not disabled.
func (s *AuthService) IssueSession(ctx context.Context, u domain.User) (Session, error) {
	return s.IssueSessionFor(ctx, u, "")
}

// IssueSessionFor is IssueSession with a coarse device label for the
// sessions list.
func (s *AuthService) IssueSessionFor(ctx context.Context, u domain.User, device string) (Session, error) {
	token, th, err := auth.NewToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	exp := now.Add(s.TTL).UnixMilli()
	if len(device) > 64 {
		device = device[:64]
	}
	if err := s.Store.CreateSession(ctx, th, domain.Session{ID: uuid.NewString(), UserID: u.ID, CreatedAtMS: now.UnixMilli(),
		ExpiresAtMS: exp, AuthAtMS: now.UnixMilli(), Device: device}); err != nil {
		return Session{}, err
	}
	return Session{Token: token, CSRF: auth.CSRFToken(token), ExpiresAtMS: exp, User: u}, nil
}

// Authenticate resolves a session token to an active user.
func (s *AuthService) Authenticate(ctx context.Context, token string) (domain.User, error) {
	u, _, err := s.AuthenticateSession(ctx, token)
	return u, err
}

// touchEvery throttles last-seen writes to one per session per interval.
const touchEvery = 5 * time.Minute

// AuthenticateSession resolves a token to its user and session.
func (s *AuthService) AuthenticateSession(ctx context.Context, token string) (domain.User, domain.Session, error) {
	if token == "" || len(token) > 128 {
		return domain.User{}, domain.Session{}, domain.ErrUnauthorized
	}
	th := auth.HashToken(token)
	now := s.now().UnixMilli()
	u, sess, err := s.Store.GetSession(ctx, th, now)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, domain.Session{}, domain.ErrUnauthorized
	}
	if err == nil && now-sess.LastSeenAtMS > touchEvery.Milliseconds() {
		_ = s.Store.TouchSession(ctx, th, now)
	}
	return u, sess, err
}

// ---- account security ----

// RecentAuthWindow is how long after signing in a user counts as recently
// authenticated for sensitive changes that have no password to confirm.
const RecentAuthWindow = 10 * time.Minute

// ListSessions returns the actor's active sessions.
func (s *AuthService) ListSessions(ctx context.Context, actor domain.User) ([]domain.Session, error) {
	return s.Store.ListSessions(ctx, actor.ID, s.now().UnixMilli())
}

// RevokeSession signs out one of the actor's sessions.
func (s *AuthService) RevokeSession(ctx context.Context, actor domain.User, id string) error {
	return s.Store.DeleteSessionByID(ctx, actor.ID, id)
}

// RevokeOtherSessions signs out every session of the actor except current.
func (s *AuthService) RevokeOtherSessions(ctx context.Context, actor domain.User, currentToken string) (int64, error) {
	return s.Store.DeleteOtherSessions(ctx, actor.ID, auth.HashToken(currentToken))
}

// ChangePassword sets a new password. Accounts with a password must confirm
// the current one; accounts that only sign in through a provider may add a
// password within RecentAuthWindow of signing in (they just proved control of
// the account). Every other session is signed out in the same transaction.
func (s *AuthService) ChangePassword(ctx context.Context, actor domain.User, currentToken, current, next string) error {
	u, sess, err := s.AuthenticateSession(ctx, currentToken)
	if err != nil || u.ID != actor.ID {
		return domain.ErrUnauthorized
	}
	if u.PasswordHash != "" {
		ok, verr := s.Hasher.Verify(ctx, current, u.PasswordHash)
		if verr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if verr != nil || !ok {
			return domain.Invalid("the current password is not correct")
		}
	} else if s.now().UnixMilli()-sess.AuthAtMS > RecentAuthWindow.Milliseconds() {
		return domain.Invalid("sign out and sign in again with your provider, then add the password within 10 minutes")
	}
	if err := validatePassword(next); err != nil {
		return err
	}
	hash, err := s.Hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	return s.Store.SetPassword(ctx, u.ID, hash, auth.HashToken(currentToken), s.now().UnixMilli())
}

// ResetPassword is the administrator recovery path (CLI): it sets a password
// and signs the account out everywhere.
func (s *AuthService) ResetPassword(ctx context.Context, email, next string) (domain.User, error) {
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	u, err := s.Store.GetUserByEmail(ctx, norm)
	if err != nil {
		return domain.User{}, err
	}
	if err := validatePassword(next); err != nil {
		return domain.User{}, err
	}
	hash, err := s.Hasher.Hash(ctx, next)
	if err != nil {
		return domain.User{}, err
	}
	return u, s.Store.SetPassword(ctx, u.ID, hash, nil, s.now().UnixMilli())
}

// Logout revokes a session.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.Store.DeleteSession(ctx, auth.HashToken(token))
}

func (s *AuthService) ListUsers(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if !actor.Can(domain.PermUsersView) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListUsers(ctx)
}

// SetDisabled enables or disables an account (users.manage); disabling
// revokes its sessions. Nobody can disable themselves; delegated account
// managers cannot act on administrators or accounts holding administration
// permissions they lack (see manageable).
func (s *AuthService) SetDisabled(ctx context.Context, actor domain.User, id string, disabled bool) error {
	if !actor.Can(domain.PermUsersManage) {
		return domain.ErrForbidden
	}
	if disabled && actor.ID == id {
		return domain.Invalid("you cannot disable your own account")
	}
	if !actor.IsAdmin() {
		target, err := s.Store.GetUserByID(ctx, id)
		if err != nil {
			return err
		}
		if err := manageable(actor, target); err != nil {
			return err
		}
	}
	return s.Store.SetUserDisabled(ctx, id, disabled, s.now().UnixMilli())
}

// SetRole assigns the built-in "admin" or "user" role or a custom role id
// (see AssignRole). The store refuses to remove the last active
// administrator.
func (s *AuthService) SetRole(ctx context.Context, actor domain.User, id, role string) error {
	_, err := s.AssignRole(ctx, actor, id, role)
	return err
}

// BotCounts returns owned-bot counts per user (admin only).
func (s *AuthService) BotCounts(ctx context.Context, actor domain.User) (map[string]int, error) {
	if !actor.Can(domain.PermUsersView) {
		return nil, domain.ErrForbidden
	}
	return s.Store.BotCountsByOwner(ctx)
}
