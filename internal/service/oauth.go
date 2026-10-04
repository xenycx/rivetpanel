package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// OAuthStore is the persistence surface OAuthService needs.
type OAuthStore interface {
	GetUserByID(ctx context.Context, id string) (domain.User, error)
	GetOAuthAccount(ctx context.Context, provider, providerUserID string) (domain.OAuthAccount, error)
	ListOAuthAccounts(ctx context.Context, userID string) ([]domain.OAuthAccount, error)
	UpsertOAuthAccount(ctx context.Context, a domain.OAuthAccount) error
	UnlinkOAuthAccount(ctx context.Context, userID, provider string) error
	CreateUserWithOAuth(ctx context.Context, u domain.User, a domain.OAuthAccount) error
	SetOAuthWebhook(ctx context.Context, userID, provider string, cipher, nonce []byte, keyID *string, nowMS int64) error
}

// OAuth failure codes are stable strings shown to the user by the UI.
const (
	OAuthStateInvalid    = "state_invalid"
	OAuthExchangeFailed  = "exchange_failed"
	OAuthSignupDisabled  = "signup_disabled"
	OAuthEmailInUse      = "email_in_use"
	OAuthEmailUnverified = "email_unverified"
	OAuthAlreadyLinked   = "already_linked"
	OAuthAccountDisabled = "account_disabled"
)

// OAuthError is a browser-facing failure: the callback redirects with Code.
type OAuthError struct {
	Code string
	Link bool // the failed flow was linking an account (so return to settings)
}

func (e *OAuthError) Error() string { return "oauth: " + e.Code }

// OAuthService links external identities and signs users in with them.
type OAuthService struct {
	Store       OAuthStore
	Auth        *AuthService
	Keys        *secrets.Keyring
	Providers   map[string]oauth.Provider
	PublicURL   string // origin without trailing slash
	AllowSignup bool
	States      *oauth.StateStore
	Log         *slog.Logger
	Now         func() time.Time

	// mu guards Providers, PublicURL and AllowSignup, which the settings page
	// can change while the panel runs (Reconfigure).
	mu sync.RWMutex
}

// Reconfigure replaces the providers and options at runtime. Flows already
// started keep their state; a flow for a removed provider fails at callback.
func (s *OAuthService) Reconfigure(providers map[string]oauth.Provider, publicURL string, allowSignup bool) {
	s.mu.Lock()
	s.Providers, s.PublicURL, s.AllowSignup = providers, publicURL, allowSignup
	s.mu.Unlock()
}

func (s *OAuthService) provider(name string) (oauth.Provider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.Providers[name]
	return p, ok
}

// CurrentPublicURL is the panel's externally reachable origin ("" if unset).
func (s *OAuthService) CurrentPublicURL() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PublicURL
}

// AnyEnabled reports whether at least one provider is configured.
func (s *OAuthService) AnyEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Providers) > 0
}

func (s *OAuthService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Enabled reports whether a provider is configured.
func (s *OAuthService) Enabled(provider string) bool {
	if s == nil {
		return false
	}
	_, ok := s.provider(provider)
	return ok
}

// RedirectURI is the callback URL registered with the provider.
func (s *OAuthService) RedirectURI(provider string) string {
	return s.CurrentPublicURL() + "/api/v1/auth/" + provider + "/callback"
}

// Begin starts an authorization. userID is non-empty when linking to a
// signed-in account. The caller sets binder as the browser's state cookie.
func (s *OAuthService) Begin(provider, userID string, notify, repoAccess bool) (redirect, binder string, err error) {
	p, ok := s.provider(provider)
	if !ok {
		return "", "", domain.ErrNotFound
	}
	verifier, challenge := oauth.NewPKCE()
	state, binder := s.States.Put(oauth.Pending{Provider: provider, Verifier: verifier, UserID: userID, Notify: notify && provider == domain.ProviderDiscord, RepoAccess: repoAccess && provider == domain.ProviderGitHub})
	return p.AuthURL(oauth.AuthParams{RedirectURI: s.RedirectURI(provider), State: state, Challenge: challenge, Notify: notify, RepoAccess: repoAccess}), binder, nil
}

// Result is a completed callback.
type Result struct {
	Linked  bool     // true: an identity was linked to the signed-in user
	Session *Session // set when a login session was issued
}

// Complete finishes the callback. A link flow is bound to the user who started
// it (an authenticated, CSRF-checked request) and to the browser by the binder
// cookie, so the callback does not need the session cookie, which SameSite=Strict
// withholds on the cross-site redirect back from the provider.
func (s *OAuthService) Complete(ctx context.Context, provider, code, state, binder, device string) (Result, error) {
	p, ok := s.provider(provider)
	if !ok {
		return Result{}, domain.ErrNotFound
	}
	pend, ok := s.States.Take(state, binder, provider)
	if !ok || code == "" {
		return Result{}, &OAuthError{Code: OAuthStateInvalid}
	}
	linking := pend.UserID != ""
	tok, err := p.Exchange(ctx, s.RedirectURI(provider), code, pend.Verifier)
	if err != nil {
		s.warn("token exchange failed", provider, err)
		return Result{}, &OAuthError{Code: OAuthExchangeFailed, Link: linking}
	}
	ident, err := p.Identity(ctx, tok)
	if err != nil {
		s.warn("profile fetch failed", provider, err)
		return Result{}, &OAuthError{Code: OAuthExchangeFailed, Link: linking}
	}

	existing, err := s.Store.GetOAuthAccount(ctx, provider, ident.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Result{}, err
	}
	found := err == nil

	var userID string
	switch {
	case linking:
		if found && existing.UserID != pend.UserID {
			return Result{}, &OAuthError{Code: OAuthAlreadyLinked, Link: linking}
		}
		userID = pend.UserID
	case found:
		userID = existing.UserID
	}

	now := s.now().UnixMilli()
	acct := domain.OAuthAccount{
		Provider: provider, ProviderUserID: ident.ID, UserID: userID, Username: ident.Username,
		Scopes: tok.Scope, NotifyEnabled: true, CreatedAtMS: now, UpdatedAtMS: now,
	}
	if ident.Email != "" {
		acct.Email = &ident.Email
	}
	if ident.AvatarURL != "" {
		acct.AvatarURL = &ident.AvatarURL
	}
	var user domain.User
	if userID == "" {
		user, err = s.signup(ctx, acct, ident, tok)
		if err != nil {
			return Result{}, err
		}
	} else {
		user, err = s.Store.GetUserByID(ctx, userID)
		if err != nil {
			return Result{}, err
		}
		if user.Disabled {
			return Result{}, &OAuthError{Code: OAuthAccountDisabled, Link: linking}
		}
		if err := s.sealToken(&acct, tok); err != nil {
			return Result{}, err
		}
		if err := s.Store.UpsertOAuthAccount(ctx, acct); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				return Result{}, &OAuthError{Code: OAuthAlreadyLinked, Link: linking}
			}
			return Result{}, err
		}
	}
	if tok.WebhookURL != "" {
		if err := s.storeWebhook(ctx, user.ID, provider, tok.WebhookURL); err != nil {
			s.warn("webhook store failed", provider, err)
		}
	}
	if linking {
		return Result{Linked: true}, nil
	}
	sess, err := s.Auth.IssueSessionFor(ctx, user, device)
	if err != nil {
		return Result{}, err
	}
	return Result{Session: &sess}, nil
}

// signup creates a passwordless account for an unknown identity. It never
// attaches to an existing account by email: a provider address is a claim, and
// merging on it would let anyone who controls that address take the account over.
func (s *OAuthService) signup(ctx context.Context, acct domain.OAuthAccount, ident oauth.Identity, tok oauth.Token) (domain.User, error) {
	s.mu.RLock()
	allow := s.AllowSignup
	s.mu.RUnlock()
	if !allow {
		return domain.User{}, &OAuthError{Code: OAuthSignupDisabled}
	}
	if ident.Email == "" {
		return domain.User{}, &OAuthError{Code: OAuthEmailUnverified}
	}
	email, err := NormalizeEmail(ident.Email)
	if err != nil {
		return domain.User{}, &OAuthError{Code: OAuthEmailUnverified}
	}
	now := s.now().UnixMilli()
	// The provider only ever hands over an address it has verified (see
	// oauth.Identity), so the account starts verified.
	u := domain.User{ID: uuid.NewString(), Email: email, Role: domain.RoleUser, CreatedAtMS: now, UpdatedAtMS: now, EmailVerified: true}
	acct.UserID = u.ID
	if err := s.sealToken(&acct, tok); err != nil {
		return domain.User{}, err
	}
	if err := s.Store.CreateUserWithOAuth(ctx, u, acct); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.User{}, &OAuthError{Code: OAuthEmailInUse}
		}
		return domain.User{}, err
	}
	return u, nil
}

func tokenName(provider string) string { return provider + ":token" }
func ownerNS(userID string) string     { return "oauth:" + userID }

// sealToken encrypts the GitHub access token; Discord's is not kept.
func (s *OAuthService) sealToken(a *domain.OAuthAccount, tok oauth.Token) error {
	if a.Provider != domain.ProviderGitHub || tok.AccessToken == "" {
		return nil
	}
	sl, err := s.Keys.Seal(ownerNS(a.UserID), tokenName(a.Provider), []byte(tok.AccessToken))
	if err != nil {
		return err
	}
	a.TokenCipher, a.TokenNonce, a.TokenKeyID = sl.Ciphertext, sl.Nonce, &sl.KeyID
	return nil
}

func (s *OAuthService) storeWebhook(ctx context.Context, userID, provider, url string) error {
	sl, err := s.Keys.Seal(ownerNS(userID), secrets.OAuthWebhookName(provider), []byte(url))
	if err != nil {
		return err
	}
	return s.Store.SetOAuthWebhook(ctx, userID, provider, sl.Ciphertext, sl.Nonce, &sl.KeyID, s.now().UnixMilli())
}

// GitHubToken returns the user's decrypted GitHub access token.
func (s *OAuthService) GitHubToken(ctx context.Context, userID string) (string, error) {
	accts, err := s.Store.ListOAuthAccounts(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, a := range accts {
		if a.Provider == domain.ProviderGitHub && a.TokenKeyID != nil {
			pt, err := s.Keys.Open(ownerNS(userID), tokenName(a.Provider),
				secrets.Sealed{Ciphertext: a.TokenCipher, Nonce: a.TokenNonce, KeyID: *a.TokenKeyID})
			return string(pt), err
		}
	}
	return "", domain.ErrNotFound
}

// GitHubPushToken returns the user's GitHub token when it may write to
// repositories (repo scope); otherwise a message explaining what to do.
func (s *OAuthService) GitHubPushToken(ctx context.Context, userID string) (string, error) {
	accts, err := s.Store.ListOAuthAccounts(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, a := range accts {
		if a.Provider == domain.ProviderGitHub && a.TokenKeyID != nil {
			if !hasScope(a.Scopes, "repo") {
				return "", domain.Invalid("grant repository access to your GitHub connection in Settings → Connected accounts first")
			}
			return s.GitHubToken(ctx, userID)
		}
	}
	return "", domain.Invalid("connect your GitHub account in Settings → Connected accounts first")
}

// Connection is one row of the Connected Accounts page.
type Connection struct {
	Provider      string
	Configured    bool
	Linked        bool
	Username      string
	AvatarURL     string
	Notifications bool // Discord webhook granted
	RepoAccess    bool // GitHub token holds the repo scope
	CanDisconnect bool
}

// Connections lists both providers for a user. CanDisconnect is false when the
// link is the user's only remaining sign-in method.
func (s *OAuthService) Connections(ctx context.Context, userID string) ([]Connection, error) {
	u, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	accts, err := s.Store.ListOAuthAccounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	methods := len(accts)
	if u.PasswordHash != "" {
		methods++
	}
	byProv := map[string]domain.OAuthAccount{}
	for _, a := range accts {
		byProv[a.Provider] = a
	}
	var out []Connection
	for _, p := range []string{domain.ProviderDiscord, domain.ProviderGitHub} {
		c := Connection{Provider: p, Configured: s.Enabled(p)}
		if a, ok := byProv[p]; ok {
			c.Linked, c.Username = true, a.Username
			if a.AvatarURL != nil {
				c.AvatarURL = *a.AvatarURL
			}
			c.Notifications = a.WebhookCipher != nil
			c.RepoAccess = a.Provider == domain.ProviderGitHub && hasScope(a.Scopes, "repo")
			c.CanDisconnect = methods > 1
		}
		out = append(out, c)
	}
	return out, nil
}

// Disconnect unlinks a provider, refusing to remove the last sign-in method.
func (s *OAuthService) Disconnect(ctx context.Context, userID, provider string) error {
	if provider != domain.ProviderGitHub && provider != domain.ProviderDiscord {
		return domain.ErrNotFound
	}
	return s.Store.UnlinkOAuthAccount(ctx, userID, provider)
}

func (s *OAuthService) warn(msg, provider string, err error) {
	if s.Log != nil {
		// Provider errors never contain our secrets (only host and status).
		s.Log.Warn("oauth: "+msg, "provider", provider, "err", strings.TrimSpace(err.Error()))
	}
}

func hasScope(scopes, want string) bool {
	for _, s := range strings.FieldsFunc(scopes, func(r rune) bool { return r == ' ' || r == ',' }) {
		if s == want {
			return true
		}
	}
	return false
}
