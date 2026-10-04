package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// MFAStore is the persistence two-step sign-in needs.
type MFAStore interface {
	GetMFA(ctx context.Context, userID string) (domain.MFA, error)
	MFAEnabled(ctx context.Context, userID string) (bool, error)
	StartMFA(ctx context.Context, m domain.MFA) error
	UseTOTPStep(ctx context.Context, userID string, step int64) (bool, error)
	EnableMFA(ctx context.Context, userID string, codeHashes [][]byte, nowMS int64) error
	ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes [][]byte) error
	UseRecoveryCode(ctx context.Context, userID string, hash []byte, nowMS int64) (bool, error)
	DeleteMFA(ctx context.Context, userID string) error
}

const (
	mfaIssuer         = "RivetPanel"
	mfaTicketTTL      = 5 * time.Minute
	mfaTicketAttempts = 5
	mfaMaxTickets     = 1000
	recoveryCodeCount = 10
)

// ErrMFARequired means the password (or provider) was right and a second
// step is needed; the caller holds a ticket for it.
var ErrMFARequired = errors.New("two-step verification required")

// MFAService implements optional TOTP two-step sign-in with one-use recovery
// codes. The policy is the same after a password or a provider sign-in; SFTP
// accepts only API keys for enrolled accounts, never the account password.
type MFAService struct {
	Store MFAStore
	Keys  *secrets.Keyring
	Auth  *AuthService
	Now   func() time.Time

	mu      sync.Mutex
	tickets map[string]*mfaTicket
}

type mfaTicket struct {
	userID   string
	device   string
	method   string
	expires  time.Time
	attempts int
}

func (m *MFAService) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Required reports whether the user must pass the second step.
func (m *MFAService) Required(ctx context.Context, userID string) (bool, error) {
	if m == nil {
		return false, nil
	}
	return m.Store.MFAEnabled(ctx, userID)
}

// Challenge holds a successful first step for a few minutes and returns the
// opaque ticket that the second step must present.
func (m *MFAService) Challenge(u domain.User, device, method string) (string, error) {
	tok, _, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tickets == nil {
		m.tickets = map[string]*mfaTicket{}
	}
	for k, t := range m.tickets {
		if now.After(t.expires) {
			delete(m.tickets, k)
		}
	}
	if len(m.tickets) >= mfaMaxTickets {
		return "", domain.Invalid("too many sign-ins in progress; try again in a few minutes")
	}
	m.tickets[string(auth.HashToken(tok))] = &mfaTicket{userID: u.ID, device: device, method: method, expires: now.Add(mfaTicketTTL)}
	return tok, nil
}

// Complete checks the second step and issues the session. A wrong code keeps
// the ticket until its attempts run out; a right one consumes it.
func (m *MFAService) Complete(ctx context.Context, ticket, code string) (Session, string, error) {
	key := string(auth.HashToken(ticket))
	m.mu.Lock()
	t := m.tickets[key]
	if t == nil || m.now().After(t.expires) || t.attempts >= mfaTicketAttempts {
		delete(m.tickets, key)
		m.mu.Unlock()
		return Session{}, "", domain.Invalid("this sign-in expired; start again")
	}
	t.attempts++
	uid, device, method := t.userID, t.device, t.method
	m.mu.Unlock()

	u, err := m.Auth.Store.GetUserByID(ctx, uid)
	if err != nil || u.Disabled {
		return Session{}, method, domain.ErrUnauthorized
	}
	if err := m.verify(ctx, uid, code); err != nil {
		return Session{User: u}, method, err
	}
	m.mu.Lock()
	delete(m.tickets, key)
	m.mu.Unlock()
	sess, err := m.Auth.IssueSessionFor(ctx, u, device)
	return sess, method, err
}

// verify accepts a current TOTP code (once) or an unused recovery code.
func (m *MFAService) verify(ctx context.Context, userID, code string) error {
	en, err := m.Store.GetMFA(ctx, userID)
	if err != nil || en.EnabledAtMS == nil {
		return domain.ErrUnauthorized
	}
	if len(code) > 32 {
		return domain.Invalid("that code is not right")
	}
	secret, err := m.Keys.Open(secrets.MFANS(userID), "totp", secretsSealed(en))
	if err != nil {
		return err
	}
	if step, ok := auth.VerifyTOTP(secret, code, m.now(), en.LastStep); ok {
		if used, err := m.Store.UseTOTPStep(ctx, userID, step); err != nil || !used {
			return domain.Invalid("that code was already used; wait for the next one")
		}
		return nil
	}
	if rc := auth.NormalizeRecoveryCode(code); len(rc) == 12 {
		h := sha256.Sum256([]byte(rc))
		if ok, err := m.Store.UseRecoveryCode(ctx, userID, h[:], m.now().UnixMilli()); err == nil && ok {
			return nil
		}
	}
	return domain.Invalid("that code is not right")
}

func secretsSealed(en domain.MFA) secrets.Sealed {
	return secrets.Sealed{Ciphertext: en.Cipher, Nonce: en.Nonce, KeyID: en.KeyID}
}

// MFAStatus describes a user's enrollment for the settings page.
type MFAStatus struct {
	Enabled      bool
	EnabledAtMS  *int64
	RecoveryLeft int
}

// Status returns the actor's enrollment state.
func (m *MFAService) Status(ctx context.Context, actor domain.User) (MFAStatus, error) {
	en, err := m.Store.GetMFA(ctx, actor.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return MFAStatus{}, nil
	}
	if err != nil {
		return MFAStatus{}, err
	}
	return MFAStatus{Enabled: en.EnabledAtMS != nil, EnabledAtMS: en.EnabledAtMS, RecoveryLeft: en.RecoveryLeft}, nil
}

// MFASetup is a new, not yet confirmed enrollment.
type MFASetup struct {
	Key string // base32, for manual entry
	URI string // otpauth://
}

// Setup starts an enrollment. It needs recent proof of the account: the
// current password, or (for provider-only accounts) a sign-in within
// RecentAuthWindow.
func (m *MFAService) Setup(ctx context.Context, actor domain.User, currentToken, password string) (MFASetup, error) {
	if err := m.recentAuth(ctx, actor, currentToken, password); err != nil {
		return MFASetup{}, err
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		return MFASetup{}, err
	}
	sl, err := m.Keys.Seal(secrets.MFANS(actor.ID), "totp", secret)
	if err != nil {
		return MFASetup{}, err
	}
	err = m.Store.StartMFA(ctx, domain.MFA{UserID: actor.ID, Cipher: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID, CreatedAtMS: m.now().UnixMilli()})
	if errors.Is(err, domain.ErrConflict) {
		return MFASetup{}, domain.Invalid("two-step sign-in is already on; turn it off first to use a new authenticator")
	}
	if err != nil {
		return MFASetup{}, err
	}
	return MFASetup{Key: auth.TOTPKey(secret), URI: auth.TOTPURI(secret, mfaIssuer, actor.Email)}, nil
}

func (m *MFAService) recentAuth(ctx context.Context, actor domain.User, currentToken, password string) error {
	u, sess, err := m.Auth.AuthenticateSession(ctx, currentToken)
	if err != nil || u.ID != actor.ID {
		return domain.ErrUnauthorized
	}
	if u.PasswordHash != "" {
		ok, verr := m.Auth.Hasher.Verify(ctx, password, u.PasswordHash)
		if verr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if verr != nil || !ok {
			return domain.Invalid("the current password is not correct")
		}
		return nil
	}
	if m.now().UnixMilli()-sess.AuthAtMS > RecentAuthWindow.Milliseconds() {
		return domain.Invalid("sign out and sign in again with your provider, then set this up within 10 minutes")
	}
	return nil
}

// Enable confirms the enrollment with a first code and returns the recovery
// codes (shown once; only their hashes are stored).
func (m *MFAService) Enable(ctx context.Context, actor domain.User, code string) ([]string, error) {
	en, err := m.Store.GetMFA(ctx, actor.ID)
	if err != nil || en.EnabledAtMS != nil {
		return nil, domain.Invalid("start the setup again")
	}
	if m.now().Sub(time.UnixMilli(en.CreatedAtMS)) > 30*time.Minute {
		return nil, domain.Invalid("this setup expired; start again")
	}
	secret, err := m.Keys.Open(secrets.MFANS(actor.ID), "totp", secretsSealed(en))
	if err != nil {
		return nil, err
	}
	step, ok := auth.VerifyTOTP(secret, code, m.now(), en.LastStep)
	if !ok {
		return nil, domain.Invalid("that code is not right; check the time on your phone")
	}
	if used, err := m.Store.UseTOTPStep(ctx, actor.ID, step); err != nil || !used {
		return nil, domain.Invalid("that code was already used; wait for the next one")
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := m.Store.EnableMFA(ctx, actor.ID, hashes, m.now().UnixMilli()); err != nil {
		return nil, err
	}
	return codes, nil
}

func newRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		c, err := auth.NewRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		h := sha256.Sum256([]byte(auth.NormalizeRecoveryCode(c)))
		codes[i], hashes[i] = c, h[:]
	}
	return codes, hashes, nil
}

// Disable turns two-step sign-in off after a valid code (TOTP or recovery).
func (m *MFAService) Disable(ctx context.Context, actor domain.User, code string) error {
	if err := m.verify(ctx, actor.ID, code); err != nil {
		return err
	}
	return m.Store.DeleteMFA(ctx, actor.ID)
}

// RegenerateCodes replaces the recovery codes after a valid code.
func (m *MFAService) RegenerateCodes(ctx context.Context, actor domain.User, code string) ([]string, error) {
	if err := m.verify(ctx, actor.ID, code); err != nil {
		return nil, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	return codes, m.Store.ReplaceRecoveryCodes(ctx, actor.ID, hashes)
}

// Reset is the administrator recovery path (CLI, host access): it removes
// two-step sign-in from an account and signs it out everywhere.
func (m *MFAService) Reset(ctx context.Context, email string) (domain.User, error) {
	norm, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	u, err := m.Auth.Store.GetUserByEmail(ctx, norm)
	if err != nil {
		return domain.User{}, err
	}
	if err := m.Store.DeleteMFA(ctx, u.ID); err != nil {
		return domain.User{}, err
	}
	_, err = m.Auth.Store.DeleteOtherSessions(ctx, u.ID, []byte{}) // keep nothing (NULL would match nothing)
	return u, err
}

// RecentAuth proves the signed-in person is present: the current password,
// or (for accounts without one) a sign-in within RecentAuthWindow.
func (m *MFAService) RecentAuth(ctx context.Context, actor domain.User, currentToken, password string) error {
	return m.recentAuth(ctx, actor, currentToken, password)
}

// TicketUser returns the account a pending second step belongs to, without
// consuming the ticket (a passkey second step needs it to build the request).
func (m *MFAService) TicketUser(ticket string) (string, bool) {
	if m == nil || ticket == "" {
		return "", false
	}
	key := string(auth.HashToken(ticket))
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tickets[key]
	if t == nil || m.now().After(t.expires) || t.attempts >= mfaTicketAttempts {
		return "", false
	}
	return t.userID, true
}

// CompleteVerified finishes a second step that was proven another way (a
// passkey assertion for userID). It consumes the ticket and issues the session.
func (m *MFAService) CompleteVerified(ctx context.Context, ticket, userID string) (Session, string, error) {
	key := string(auth.HashToken(ticket))
	m.mu.Lock()
	t := m.tickets[key]
	if t == nil || m.now().After(t.expires) || t.userID != userID {
		m.mu.Unlock()
		return Session{}, "", domain.Invalid("this sign-in expired; start again")
	}
	delete(m.tickets, key)
	device, method := t.device, t.method
	m.mu.Unlock()
	u, err := m.Auth.Store.GetUserByID(ctx, userID)
	if err != nil || u.Disabled {
		return Session{}, method, domain.ErrUnauthorized
	}
	sess, err := m.Auth.IssueSessionFor(ctx, u, device)
	return sess, method, err
}
