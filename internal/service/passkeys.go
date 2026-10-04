package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// PasskeyStore is the persistence passkeys need.
type PasskeyStore interface {
	InsertPasskey(ctx context.Context, p domain.Passkey) error
	ListPasskeys(ctx context.Context, userID string) ([]domain.Passkey, error)
	GetPasskeyByCredentialID(ctx context.Context, credID []byte) (domain.Passkey, error)
	UpdatePasskeyUse(ctx context.Context, id string, credential []byte, nowMS int64) error
	RenamePasskey(ctx context.Context, userID, id, name string) error
	DeletePasskey(ctx context.Context, userID, id string) error
	GetUserByID(ctx context.Context, id string) (domain.User, error)
	ListOAuthAccounts(ctx context.Context, userID string) ([]domain.OAuthAccount, error)
	ListOIDCIdentities(ctx context.Context, userID string) ([]domain.OIDCIdentity, error)
}

const (
	maxPasskeys        = 20
	passkeyCeremonyTTL = 5 * time.Minute
	maxCeremonies      = 2000
)

// PasskeyService registers passkeys and signs in with them. The relying party
// is the panel's address (RP ID = its host name), so passkeys are offered
// only once the panel address is set.
type PasskeyService struct {
	Store     PasskeyStore
	Auth      *AuthService
	MFA       *MFAService
	PublicURL func() string
	Now       func() time.Time

	mu         sync.Mutex
	ceremonies map[string]ceremony // by SHA-256 of the ticket
}

type ceremony struct {
	kind    string // register | login | mfa
	userID  string // register: the account; mfa: the account of the MFA ticket
	session webauthn.SessionData
	expires time.Time
}

func (s *PasskeyService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// relyingParty builds the WebAuthn configuration from the panel address.
func (s *PasskeyService) relyingParty() (*webauthn.WebAuthn, error) {
	if s == nil || s.PublicURL == nil {
		return nil, domain.Invalid("passkeys are not available on this panel")
	}
	u, err := url.Parse(strings.TrimRight(s.PublicURL(), "/"))
	if err != nil || u.Hostname() == "" {
		return nil, domain.Invalid("passkeys need the panel address; an administrator sets it under Panel settings")
	}
	if net.ParseIP(u.Hostname()) != nil {
		// Browsers only accept a host name as the relying party.
		return nil, domain.Invalid("passkeys need the panel address to be a host name, not an IP address")
	}
	return webauthn.New(&webauthn.Config{RPID: u.Hostname(), RPDisplayName: "RivetPanel", RPOrigins: []string{u.Scheme + "://" + u.Host}})
}

// Available reports whether passkeys can be used (the panel address is a
// host name a browser accepts as a relying party).
func (s *PasskeyService) Available() bool {
	_, err := s.relyingParty()
	return err == nil
}

// passkeyUser adapts an account to the library.
type passkeyUser struct {
	u     domain.User
	creds []webauthn.Credential
}

func (p passkeyUser) WebAuthnID() []byte {
	id, err := uuid.Parse(p.u.ID)
	if err != nil {
		return []byte(p.u.ID)
	}
	return id[:]
}
func (p passkeyUser) WebAuthnName() string        { return p.u.Email }
func (p passkeyUser) WebAuthnDisplayName() string { return p.u.Email }
func (p passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	return p.creds
}

func (s *PasskeyService) loadUser(ctx context.Context, userID string) (passkeyUser, []domain.Passkey, error) {
	u, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return passkeyUser{}, nil, err
	}
	keys, err := s.Store.ListPasskeys(ctx, userID)
	if err != nil {
		return passkeyUser{}, nil, err
	}
	pu := passkeyUser{u: u}
	for _, k := range keys {
		var c webauthn.Credential
		if json.Unmarshal(k.Credential, &c) == nil {
			pu.creds = append(pu.creds, c)
		}
	}
	return pu, keys, nil
}

func (s *PasskeyService) put(c ceremony) (string, error) {
	tok, _, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	c.expires = now.Add(passkeyCeremonyTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ceremonies == nil {
		s.ceremonies = map[string]ceremony{}
	}
	for k, v := range s.ceremonies {
		if now.After(v.expires) {
			delete(s.ceremonies, k)
		}
	}
	if len(s.ceremonies) >= maxCeremonies {
		return "", domain.Invalid("too many passkey requests in progress; try again in a few minutes")
	}
	s.ceremonies[string(auth.HashToken(tok))] = c
	return tok, nil
}

// take consumes a ceremony (single use).
func (s *PasskeyService) take(ticket, kind string) (ceremony, bool) {
	key := string(auth.HashToken(ticket))
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[key]
	delete(s.ceremonies, key)
	if !ok || c.kind != kind || s.now().After(c.expires) {
		return ceremony{}, false
	}
	return c, true
}

// ---- registration and management ----

// BeginRegistration starts adding a passkey to the actor's account; the
// caller has already checked the person is present (password or a recent
// sign-in). It returns the browser's creation options and a ticket.
func (s *PasskeyService) BeginRegistration(ctx context.Context, actor domain.User) (*protocol.CredentialCreation, string, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, "", err
	}
	pu, keys, err := s.loadUser(ctx, actor.ID)
	if err != nil {
		return nil, "", err
	}
	if len(keys) >= maxPasskeys {
		return nil, "", domain.Invalid("you have 20 passkeys; remove one first")
	}
	excl := make([]protocol.CredentialDescriptor, len(pu.creds))
	for i := range pu.creds {
		excl[i] = pu.creds[i].Descriptor()
	}
	opts, sess, err := rp.BeginRegistration(pu,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(excl))
	if err != nil {
		return nil, "", err
	}
	tok, err := s.put(ceremony{kind: "register", userID: actor.ID, session: *sess})
	return opts, tok, err
}

// FinishRegistration verifies the authenticator's response and stores the
// passkey under name.
func (s *PasskeyService) FinishRegistration(ctx context.Context, actor domain.User, ticket, name string, response []byte) (domain.Passkey, error) {
	name, err := validateName(name)
	if err != nil {
		return domain.Passkey{}, err
	}
	c, ok := s.take(ticket, "register")
	if !ok || c.userID != actor.ID {
		return domain.Passkey{}, domain.Invalid("this passkey request expired; start again")
	}
	rp, err := s.relyingParty()
	if err != nil {
		return domain.Passkey{}, err
	}
	pu, _, err := s.loadUser(ctx, actor.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return domain.Passkey{}, domain.Invalid("the browser's passkey response could not be read")
	}
	cred, err := rp.CreateCredential(pu, c.session, parsed)
	if err != nil {
		return domain.Passkey{}, domain.Invalid("the passkey could not be verified")
	}
	js, err := json.Marshal(cred)
	if err != nil {
		return domain.Passkey{}, err
	}
	p := domain.Passkey{ID: uuid.NewString(), UserID: actor.ID, CredentialID: cred.ID, Name: name, Credential: js, CreatedAtMS: s.now().UnixMilli()}
	if err := s.Store.InsertPasskey(ctx, p); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.Passkey{}, domain.Invalid("that passkey is already registered")
		}
		return domain.Passkey{}, err
	}
	return p, nil
}

// List returns the actor's passkeys.
func (s *PasskeyService) List(ctx context.Context, actor domain.User) ([]domain.Passkey, error) {
	return s.Store.ListPasskeys(ctx, actor.ID)
}

// Rename renames one of the actor's passkeys.
func (s *PasskeyService) Rename(ctx context.Context, actor domain.User, id, name string) error {
	name, err := validateName(name)
	if err != nil {
		return err
	}
	return s.Store.RenamePasskey(ctx, actor.ID, id, name)
}

// Delete removes one of the actor's passkeys unless it is the account's only
// way to sign in.
func (s *PasskeyService) Delete(ctx context.Context, actor domain.User, id string) (domain.Passkey, error) {
	keys, err := s.Store.ListPasskeys(ctx, actor.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	var target *domain.Passkey
	for i := range keys {
		if keys[i].ID == id {
			target = &keys[i]
		}
	}
	if target == nil {
		return domain.Passkey{}, domain.ErrNotFound
	}
	u, err := s.Store.GetUserByID(ctx, actor.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	oauthAccts, err := s.Store.ListOAuthAccounts(ctx, actor.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	ids, err := s.Store.ListOIDCIdentities(ctx, actor.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	others := len(keys) - 1 + len(oauthAccts) + len(ids)
	if u.PasswordHash != "" {
		others++
	}
	if others == 0 {
		return domain.Passkey{}, domain.Invalid("this is your only way to sign in; set a password first")
	}
	return *target, s.Store.DeletePasskey(ctx, actor.ID, id)
}

// ---- sign-in ----

// BeginLogin starts a passwordless sign-in with any passkey of this panel
// (discoverable credentials, user verification required).
func (s *PasskeyService) BeginLogin() (*protocol.CredentialAssertion, string, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, "", err
	}
	opts, sess, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", err
	}
	tok, err := s.put(ceremony{kind: "login", session: *sess})
	return opts, tok, err
}

// FinishLogin verifies the assertion and returns the account it signs in.
// A passkey with user verification is itself two factors (the device and its
// PIN or biometric), so no TOTP step follows.
func (s *PasskeyService) FinishLogin(ctx context.Context, ticket string, response []byte) (domain.User, error) {
	c, ok := s.take(ticket, "login")
	if !ok {
		return domain.User{}, domain.Invalid("this passkey request expired; start again")
	}
	return s.finish(ctx, c, response, "")
}

// BeginSecondFactor starts a passkey as the second step of a sign-in for the
// account holding mfaTicket (TOTP accounts can use a passkey instead of a
// code).
func (s *PasskeyService) BeginSecondFactor(ctx context.Context, mfaTicket string) (*protocol.CredentialAssertion, string, error) {
	userID, ok := s.MFA.TicketUser(mfaTicket)
	if !ok {
		return nil, "", domain.Invalid("this sign-in expired; start again")
	}
	rp, err := s.relyingParty()
	if err != nil {
		return nil, "", err
	}
	pu, _, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if len(pu.creds) == 0 {
		return nil, "", domain.Invalid("this account has no passkey; enter a code instead")
	}
	opts, sess, err := rp.BeginLogin(pu, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return nil, "", err
	}
	tok, err := s.put(ceremony{kind: "mfa", userID: userID, session: *sess})
	return opts, tok, err
}

// FinishSecondFactor verifies the assertion for the MFA ticket's account and
// completes that sign-in.
func (s *PasskeyService) FinishSecondFactor(ctx context.Context, mfaTicket, ticket string, response []byte) (Session, string, error) {
	c, ok := s.take(ticket, "mfa")
	if !ok {
		return Session{}, "", domain.Invalid("this passkey request expired; start again")
	}
	if uid, ok := s.MFA.TicketUser(mfaTicket); !ok || uid != c.userID {
		return Session{}, "", domain.Invalid("this sign-in expired; start again")
	}
	if _, err := s.finish(ctx, c, response, c.userID); err != nil {
		return Session{}, "", err
	}
	return s.MFA.CompleteVerified(ctx, mfaTicket, c.userID)
}

// finish validates an assertion. wantUser "" accepts any account's
// discoverable passkey; otherwise the passkey must belong to wantUser.
func (s *PasskeyService) finish(ctx context.Context, c ceremony, response []byte, wantUser string) (domain.User, error) {
	bad := domain.Invalid("the passkey was not accepted")
	rp, err := s.relyingParty()
	if err != nil {
		return domain.User{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return domain.User{}, bad
	}
	stored, err := s.Store.GetPasskeyByCredentialID(ctx, parsed.RawID)
	if err != nil || (wantUser != "" && stored.UserID != wantUser) {
		return domain.User{}, bad
	}
	pu, _, err := s.loadUser(ctx, stored.UserID)
	if err != nil {
		return domain.User{}, bad
	}
	var cred *webauthn.Credential
	if wantUser == "" {
		// The user handle must name the account the credential belongs to.
		cred, err = rp.ValidateDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
			if string(userHandle) != string(pu.WebAuthnID()) {
				return nil, errors.New("user handle does not match the credential")
			}
			return pu, nil
		}, c.session, parsed)
	} else {
		cred, err = rp.ValidateLogin(pu, c.session, parsed)
	}
	if err != nil || cred.Authenticator.CloneWarning {
		return domain.User{}, bad
	}
	if pu.u.Disabled {
		return domain.User{}, domain.ErrUnauthorized
	}
	if js, err := json.Marshal(cred); err == nil {
		_ = s.Store.UpdatePasskeyUse(ctx, stored.ID, js, s.now().UnixMilli())
	}
	return pu.u, nil
}
