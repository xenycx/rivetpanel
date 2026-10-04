package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// OIDCStore is the persistence OpenID Connect sign-in needs.
type OIDCStore interface {
	ListOIDCProviders(ctx context.Context) ([]domain.OIDCProvider, error)
	GetOIDCProvider(ctx context.Context, id string) (domain.OIDCProvider, error)
	GetOIDCProviderBySlug(ctx context.Context, slug string) (domain.OIDCProvider, error)
	InsertOIDCProvider(ctx context.Context, p domain.OIDCProvider) error
	UpdateOIDCProvider(ctx context.Context, p domain.OIDCProvider) error
	DeleteOIDCProvider(ctx context.Context, id string) error
	GetOIDCIdentity(ctx context.Context, providerID, subject string) (domain.OIDCIdentity, error)
	ListOIDCIdentities(ctx context.Context, userID string) ([]domain.OIDCIdentity, error)
	UpsertOIDCIdentity(ctx context.Context, i domain.OIDCIdentity) error
	DeleteOIDCIdentity(ctx context.Context, userID, providerID string) error
	CreateUserWithOIDC(ctx context.Context, u domain.User, i domain.OIDCIdentity) error
	GetUserByID(ctx context.Context, id string) (domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
	GetRole(ctx context.Context, id string) (domain.Role, error)
	ListOAuthAccounts(ctx context.Context, userID string) ([]domain.OAuthAccount, error)
	CountPasskeys(ctx context.Context, userID string) (int, error)
}

// OIDC failure codes, in addition to the OAuth ones (shown by the UI).
const (
	OIDCTokenInvalid = "token_invalid" // signature, issuer, audience, expiry or nonce
	OIDCLinkRequired = "link_required" // an account with that address exists: sign in, then link
	OIDCEmailMissing = "email_missing"
)

const (
	oidcSecretName     = "client_secret"
	oidcDiscoveryTTL   = time.Hour
	oidcStatePrefix    = "oidc:"
	maxOIDCScopes      = 20
	oidcDefaultScopes  = "openid email profile"
	oidcMaxDisplayName = 64
)

var oidcSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// OIDCService manages OpenID Connect providers and signs accounts in with
// them (authorization code flow with PKCE, state and nonce; the ID token's
// signature, issuer, audience and expiry are verified by go-oidc).
type OIDCService struct {
	Store     OIDCStore
	Auth      *AuthService
	Keys      *secrets.Keyring
	PublicURL func() string // the panel's origin ("" until it is set)
	States    *oauth.StateStore
	HTTP      *http.Client // discovery, keys and token requests; default 10 s timeout
	Log       *slog.Logger
	Now       func() time.Time

	mu    sync.Mutex
	cache map[string]cachedOIDC // by issuer
}

type cachedOIDC struct {
	p  *oidc.Provider
	at time.Time
}

func (s *OIDCService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *OIDCService) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (s *OIDCService) publicURL() string {
	if s == nil || s.PublicURL == nil {
		return ""
	}
	return strings.TrimRight(s.PublicURL(), "/")
}

// RedirectURI is the callback URL to register with the provider.
func (s *OIDCService) RedirectURI(slug string) string {
	return s.publicURL() + "/api/v1/auth/oidc/" + slug + "/callback"
}

// discover returns the provider's discovery document and key set, cached
// for an hour. The context given to go-oidc outlives the request (the key
// set refreshes with it), so it is not the request context.
func (s *OIDCService) discover(ctx context.Context, issuer string, fresh bool) (*oidc.Provider, error) {
	s.mu.Lock()
	if c, ok := s.cache[issuer]; ok && !fresh && s.now().Sub(c.at) < oidcDiscoveryTTL {
		s.mu.Unlock()
		return c.p, nil
	}
	s.mu.Unlock()
	base := oidc.ClientContext(context.Background(), s.httpClient())
	type res struct {
		p   *oidc.Provider
		err error
	}
	ch := make(chan res, 1)
	go func() {
		p, err := oidc.NewProvider(base, issuer)
		ch <- res{p, err}
	}()
	var r res
	select {
	case r = <-ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	s.mu.Lock()
	if s.cache == nil || len(s.cache) > 100 {
		s.cache = map[string]cachedOIDC{}
	}
	s.cache[issuer] = cachedOIDC{r.p, s.now()}
	s.mu.Unlock()
	return r.p, nil
}

// ---- administration ----

// OIDCProviderInput creates or changes a provider. ClientSecret nil keeps
// the stored secret; "" removes it (a public client using PKCE only).
type OIDCProviderInput struct {
	Slug, Name, Issuer, ClientID string
	ClientSecret                 *string
	Scopes                       string
	Enabled, AllowSignup         bool
	LinkByEmail                  bool
	DefaultRoleID                string
}

func validIssuer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || len(raw) > 512 {
		return "", domain.Invalid("the issuer must be a URL such as https://login.example.com/realms/main")
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if u.Scheme != "http" || !(host == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", domain.Invalid("the issuer must use https (plain http only for a provider on this machine)")
		}
	}
	return raw, nil
}

func normScopes(raw string) (string, error) {
	scopes := strings.Fields(raw)
	if len(scopes) == 0 {
		scopes = strings.Fields(oidcDefaultScopes)
	}
	var out []string
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		out = append(out, oidc.ScopeOpenID)
	}
	for _, sc := range scopes {
		for _, r := range sc {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return "", domain.Invalid("scopes are space-separated words")
			}
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	if len(out) > maxOIDCScopes {
		return "", domain.Invalid("at most 20 scopes")
	}
	return strings.Join(out, " "), nil
}

// checkDefaultRole refuses the administrator role and, for delegated
// settings managers, a role holding permissions they lack: otherwise signing
// up through the provider would hand out more than the manager holds.
func (s *OIDCService) checkDefaultRole(ctx context.Context, actor domain.User, roleID string, signup bool) error {
	switch roleID {
	case domain.RoleAdmin:
		return domain.Invalid("new accounts cannot be made administrators")
	case "", domain.RoleUser:
		if signup {
			return grantable(actor, domain.DefaultUserPermissions())
		}
		return nil
	}
	r, err := s.Store.GetRole(ctx, roleID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && r.System) {
		return domain.Invalid("that role does not exist")
	}
	if err != nil {
		return err
	}
	return grantable(actor, r.Permissions)
}

func (s *OIDCService) build(ctx context.Context, actor domain.User, cur domain.OIDCProvider, in OIDCProviderInput) (domain.OIDCProvider, error) {
	p := cur
	p.Slug = strings.TrimSpace(in.Slug)
	if !oidcSlugRe.MatchString(p.Slug) {
		return p, domain.Invalid("the URL name is 1 to 32 lowercase letters, digits and dashes")
	}
	name, err := validateName(in.Name)
	if err != nil {
		return p, err
	}
	p.Name = name
	if p.Issuer, err = validIssuer(in.Issuer); err != nil {
		return p, err
	}
	p.ClientID = strings.TrimSpace(in.ClientID)
	if p.ClientID == "" || len(p.ClientID) > 256 {
		return p, domain.Invalid("enter the client ID the provider gave you")
	}
	if p.Scopes, err = normScopes(in.Scopes); err != nil {
		return p, err
	}
	p.Enabled, p.AllowSignup, p.LinkByEmail = in.Enabled, in.AllowSignup, in.LinkByEmail
	if in.DefaultRoleID == domain.RoleUser {
		in.DefaultRoleID = ""
	}
	if err := s.checkDefaultRole(ctx, actor, in.DefaultRoleID, p.AllowSignup); err != nil {
		return p, err
	}
	p.DefaultRoleID = in.DefaultRoleID
	if in.ClientSecret != nil {
		sec := strings.TrimSpace(*in.ClientSecret)
		if len(sec) > 1024 {
			return p, domain.Invalid("the client secret is too long")
		}
		if sec == "" {
			p.SecretCipher, p.SecretNonce, p.SecretKeyID = nil, nil, nil
		} else {
			sl, err := s.Keys.Seal(oidcStatePrefix+p.ID, oidcSecretName, []byte(sec))
			if err != nil {
				return p, err
			}
			p.SecretCipher, p.SecretNonce, p.SecretKeyID = sl.Ciphertext, sl.Nonce, &sl.KeyID
		}
	}
	if p.Enabled {
		if _, err := s.discover(ctx, p.Issuer, true); err != nil {
			s.warn("discovery failed", p.Slug, err)
			return p, domain.Invalid("could not read the provider's OpenID configuration at " + p.Issuer + "/.well-known/openid-configuration; check the issuer URL")
		}
	}
	return p, nil
}

// ListProviders returns every provider to settings managers.
func (s *OIDCService) ListProviders(ctx context.Context, actor domain.User) ([]domain.OIDCProvider, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return nil, domain.Denied(actor, domain.PermSettingsManage)
	}
	return s.Store.ListOIDCProviders(ctx)
}

// CreateProvider adds a provider (discovery is checked when it is enabled).
func (s *OIDCService) CreateProvider(ctx context.Context, actor domain.User, in OIDCProviderInput) (domain.OIDCProvider, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return domain.OIDCProvider{}, domain.Denied(actor, domain.PermSettingsManage)
	}
	now := s.now().UnixMilli()
	p, err := s.build(ctx, actor, domain.OIDCProvider{ID: uuid.NewString(), CreatedAtMS: now}, in)
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	p.UpdatedAtMS = now
	if err := s.Store.InsertOIDCProvider(ctx, p); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.OIDCProvider{}, domain.Invalid("another provider uses that URL name")
		}
		return domain.OIDCProvider{}, err
	}
	return p, nil
}

// UpdateProvider changes a provider.
func (s *OIDCService) UpdateProvider(ctx context.Context, actor domain.User, id string, in OIDCProviderInput) (domain.OIDCProvider, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return domain.OIDCProvider{}, domain.Denied(actor, domain.PermSettingsManage)
	}
	cur, err := s.Store.GetOIDCProvider(ctx, id)
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	// A delegated manager cannot edit a provider that already hands out
	// more than it holds.
	if err := s.checkDefaultRole(ctx, actor, cur.DefaultRoleID, cur.AllowSignup); err != nil {
		return domain.OIDCProvider{}, domain.ErrForbidden
	}
	p, err := s.build(ctx, actor, cur, in)
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	p.UpdatedAtMS = s.now().UnixMilli()
	if err := s.Store.UpdateOIDCProvider(ctx, p); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.OIDCProvider{}, domain.Invalid("another provider uses that URL name")
		}
		return domain.OIDCProvider{}, err
	}
	return p, nil
}

// DeleteProvider removes a provider and unlinks every account from it.
// Accounts whose only sign-in method it was keep existing; an account
// manager can reset their password.
func (s *OIDCService) DeleteProvider(ctx context.Context, actor domain.User, id string) (domain.OIDCProvider, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return domain.OIDCProvider{}, domain.Denied(actor, domain.PermSettingsManage)
	}
	cur, err := s.Store.GetOIDCProvider(ctx, id)
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	return cur, s.Store.DeleteOIDCProvider(ctx, id)
}

// PublicProvider is a sign-in button.
type PublicProvider struct{ Slug, Name string }

// PublicProviders lists the enabled providers (none until the panel address
// is known, because the callback URL depends on it).
func (s *OIDCService) PublicProviders(ctx context.Context) []PublicProvider {
	if s == nil || s.publicURL() == "" {
		return nil
	}
	ps, err := s.Store.ListOIDCProviders(ctx)
	if err != nil {
		return nil
	}
	var out []PublicProvider
	for _, p := range ps {
		if p.Enabled {
			out = append(out, PublicProvider{p.Slug, p.Name})
		}
	}
	return out
}

// ---- sign-in ----

func (s *OIDCService) enabledProvider(ctx context.Context, slug string) (domain.OIDCProvider, error) {
	if s == nil || s.publicURL() == "" || !oidcSlugRe.MatchString(slug) {
		return domain.OIDCProvider{}, domain.ErrNotFound
	}
	p, err := s.Store.GetOIDCProviderBySlug(ctx, slug)
	if err != nil || !p.Enabled {
		return domain.OIDCProvider{}, domain.ErrNotFound
	}
	return p, nil
}

func (s *OIDCService) oauthConfig(ctx context.Context, p domain.OIDCProvider) (*oauth2.Config, *oidc.Provider, error) {
	op, err := s.discover(ctx, p.Issuer, false)
	if err != nil {
		return nil, nil, err
	}
	cfg := &oauth2.Config{ClientID: p.ClientID, Endpoint: op.Endpoint(), RedirectURL: s.RedirectURI(p.Slug), Scopes: strings.Fields(p.Scopes)}
	if p.HasSecret() {
		sec, err := s.Keys.Open(oidcStatePrefix+p.ID, oidcSecretName, secrets.Sealed{Ciphertext: p.SecretCipher, Nonce: p.SecretNonce, KeyID: *p.SecretKeyID})
		if err != nil {
			return nil, nil, err
		}
		cfg.ClientSecret = string(sec)
	}
	return cfg, op, nil
}

// Begin starts a sign-in (userID "") or a link to a signed-in account. The
// caller sets binder as the browser's state cookie.
func (s *OIDCService) Begin(ctx context.Context, slug, userID string) (redirect, binder string, err error) {
	p, err := s.enabledProvider(ctx, slug)
	if err != nil {
		return "", "", err
	}
	cfg, _, err := s.oauthConfig(ctx, p)
	if err != nil {
		s.warn("discovery failed", slug, err)
		return "", "", &OAuthError{Code: OAuthExchangeFailed, Link: userID != ""}
	}
	verifier, nonce := oauth2.GenerateVerifier(), oauth.RandString(24)
	state, binder := s.States.Put(oauth.Pending{Provider: oidcStatePrefix + p.ID, Verifier: verifier, UserID: userID, Nonce: nonce})
	return cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), binder, nil
}

// OIDCResult is a completed callback.
type OIDCResult struct {
	Linked  bool
	Session *Session
	User    domain.User
	Method  string // how the account was resolved: identity | email_link | signup
}

type oidcClaims struct {
	Email             string `json:"email"`
	EmailVerified     any    `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

// verified reads email_verified as a boolean (some providers send "true").
func (c oidcClaims) verified() bool {
	switch v := c.EmailVerified.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// Complete finishes the callback: exchanges the code (PKCE), verifies the ID
// token (signature against the issuer's keys, issuer, audience = client ID,
// expiry) and the nonce, then resolves the account.
func (s *OIDCService) Complete(ctx context.Context, slug, code, state, binder, device string) (OIDCResult, error) {
	p, err := s.enabledProvider(ctx, slug)
	if err != nil {
		return OIDCResult{}, err
	}
	pend, ok := s.States.Take(state, binder, oidcStatePrefix+p.ID)
	if !ok || code == "" {
		return OIDCResult{}, &OAuthError{Code: OAuthStateInvalid}
	}
	linking := pend.UserID != ""
	fail := func(c string) error { return &OAuthError{Code: c, Link: linking} }
	cfg, op, err := s.oauthConfig(ctx, p)
	if err != nil {
		s.warn("discovery failed", slug, err)
		return OIDCResult{}, fail(OAuthExchangeFailed)
	}
	hctx := context.WithValue(ctx, oauth2.HTTPClient, s.httpClient())
	tok, err := cfg.Exchange(hctx, code, oauth2.VerifierOption(pend.Verifier))
	if err != nil {
		s.warn("token exchange failed", slug, err)
		return OIDCResult{}, fail(OAuthExchangeFailed)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		s.warn("no id_token in the token response", slug, errors.New("missing id_token"))
		return OIDCResult{}, fail(OIDCTokenInvalid)
	}
	idt, err := op.VerifierContext(oidc.ClientContext(ctx, s.httpClient()), &oidc.Config{ClientID: p.ClientID, Now: s.now}).Verify(ctx, raw)
	if err != nil {
		s.warn("id token rejected", slug, err)
		return OIDCResult{}, fail(OIDCTokenInvalid)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(pend.Nonce)) != 1 {
		s.warn("id token rejected", slug, errors.New("nonce mismatch"))
		return OIDCResult{}, fail(OIDCTokenInvalid)
	}
	var cl oidcClaims
	if err := idt.Claims(&cl); err != nil || idt.Subject == "" || len(idt.Subject) > 255 {
		return OIDCResult{}, fail(OIDCTokenInvalid)
	}
	email := ""
	if e, err := NormalizeEmail(cl.Email); err == nil {
		email = e
	}
	verified := email != "" && cl.verified()
	now := s.now().UnixMilli()
	ident := domain.OIDCIdentity{ProviderID: p.ID, Subject: idt.Subject, Email: email, EmailVerified: verified, CreatedAtMS: now, LastLoginAtMS: &now}

	existing, err := s.Store.GetOIDCIdentity(ctx, p.ID, idt.Subject)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return OIDCResult{}, err
	}
	found := err == nil

	var user domain.User
	method := "identity"
	switch {
	case linking:
		if found && existing.UserID != pend.UserID {
			return OIDCResult{}, fail(OAuthAlreadyLinked)
		}
		ident.UserID = pend.UserID
		ident.LastLoginAtMS = nil
		if err := s.Store.UpsertOIDCIdentity(ctx, ident); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				return OIDCResult{}, fail(OAuthAlreadyLinked)
			}
			return OIDCResult{}, err
		}
		u, err := s.Store.GetUserByID(ctx, pend.UserID)
		if err != nil {
			return OIDCResult{}, err
		}
		return OIDCResult{Linked: true, User: u, Method: "link"}, nil
	case found:
		if user, err = s.Store.GetUserByID(ctx, existing.UserID); err != nil {
			return OIDCResult{}, err
		}
		ident.UserID = user.ID
		if err := s.Store.UpsertOIDCIdentity(ctx, ident); err != nil {
			return OIDCResult{}, err
		}
	default:
		user, method, err = s.firstSignIn(ctx, p, ident, cl)
		if err != nil {
			return OIDCResult{}, err
		}
	}
	if user.Disabled {
		return OIDCResult{}, fail(OAuthAccountDisabled)
	}
	sess, err := s.Auth.IssueSessionFor(ctx, user, device)
	if err != nil {
		return OIDCResult{}, err
	}
	return OIDCResult{Session: &sess, User: sess.User, Method: method}, nil
}

// firstSignIn resolves an identity seen for the first time: link it to the
// account with the same address when the provider asserted the address is
// verified (and the provider allows it, and the account holds no
// administration permission), otherwise create an account when sign-up is
// allowed. An existing account is never taken over on an unverified claim:
// its owner signs in and links the provider from Settings instead.
func (s *OIDCService) firstSignIn(ctx context.Context, p domain.OIDCProvider, ident domain.OIDCIdentity, cl oidcClaims) (domain.User, string, error) {
	if ident.Email != "" {
		u, err := s.Store.GetUserByEmail(ctx, ident.Email)
		switch {
		case err == nil:
			if !ident.EmailVerified || !p.LinkByEmail || u.HasAnyAdminPermission() {
				return domain.User{}, "", &OAuthError{Code: OIDCLinkRequired}
			}
			ident.UserID = u.ID
			if err := s.Store.UpsertOIDCIdentity(ctx, ident); err != nil {
				if errors.Is(err, domain.ErrConflict) {
					return domain.User{}, "", &OAuthError{Code: OIDCLinkRequired}
				}
				return domain.User{}, "", err
			}
			return u, "email_link", nil
		case !errors.Is(err, domain.ErrNotFound):
			return domain.User{}, "", err
		}
	}
	if !p.AllowSignup {
		return domain.User{}, "", &OAuthError{Code: OAuthSignupDisabled}
	}
	if ident.Email == "" {
		return domain.User{}, "", &OAuthError{Code: OIDCEmailMissing}
	}
	now := s.now().UnixMilli()
	name := strings.TrimSpace(cl.Name)
	if name == "" {
		name = strings.TrimSpace(cl.PreferredUsername)
	}
	if n, err := validateName(name); err == nil {
		name = n
	} else {
		name = ""
	}
	// The address counts as verified only when the provider said so.
	u := domain.User{ID: uuid.NewString(), Email: ident.Email, DisplayName: name, Role: domain.RoleUser,
		CreatedAtMS: now, UpdatedAtMS: now, EmailVerified: ident.EmailVerified}
	if p.DefaultRoleID != "" {
		if r, err := s.Store.GetRole(ctx, p.DefaultRoleID); err == nil && !r.System {
			u.RoleID = r.ID
		}
	}
	if err := s.Store.CreateUserWithOIDC(ctx, u, ident); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.User{}, "", &OAuthError{Code: OIDCLinkRequired}
		}
		return domain.User{}, "", err
	}
	created, err := s.Store.GetUserByID(ctx, u.ID)
	return created, "signup", err
}

// ---- linked identities ----

// LinkedIdentity is one row of Settings → Connected accounts.
type LinkedIdentity struct {
	domain.OIDCIdentity
	CanUnlink bool
}

// Identities lists the providers an account can use: linked ones and the
// enabled ones it could link.
func (s *OIDCService) Identities(ctx context.Context, userID string) ([]LinkedIdentity, []PublicProvider, error) {
	ids, err := s.Store.ListOIDCIdentities(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	methods, err := s.signInMethods(ctx, userID, len(ids))
	if err != nil {
		return nil, nil, err
	}
	out := make([]LinkedIdentity, len(ids))
	for i, x := range ids {
		out[i] = LinkedIdentity{x, methods > 1}
	}
	var avail []PublicProvider
	for _, p := range s.PublicProviders(ctx) {
		if !slices.ContainsFunc(ids, func(x domain.OIDCIdentity) bool { return x.ProviderSlug == p.Slug }) {
			avail = append(avail, p)
		}
	}
	return out, avail, nil
}

func (s *OIDCService) signInMethods(ctx context.Context, userID string, identities int) (int, error) {
	u, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	accts, err := s.Store.ListOAuthAccounts(ctx, userID)
	if err != nil {
		return 0, err
	}
	keys, err := s.Store.CountPasskeys(ctx, userID)
	if err != nil {
		return 0, err
	}
	n := identities + len(accts) + keys
	if u.PasswordHash != "" {
		n++
	}
	return n, nil
}

// Unlink removes a linked identity unless it is the account's only way to
// sign in.
func (s *OIDCService) Unlink(ctx context.Context, userID, providerID string) (domain.OIDCIdentity, error) {
	ids, err := s.Store.ListOIDCIdentities(ctx, userID)
	if err != nil {
		return domain.OIDCIdentity{}, err
	}
	i := slices.IndexFunc(ids, func(x domain.OIDCIdentity) bool { return x.ProviderID == providerID })
	if i < 0 {
		return domain.OIDCIdentity{}, domain.ErrNotFound
	}
	methods, err := s.signInMethods(ctx, userID, len(ids))
	if err != nil {
		return domain.OIDCIdentity{}, err
	}
	if methods <= 1 {
		return domain.OIDCIdentity{}, domain.Invalid("this is your only way to sign in; set a password first")
	}
	return ids[i], s.Store.DeleteOIDCIdentity(ctx, userID, providerID)
}

func (s *OIDCService) warn(msg, slug string, err error) {
	if s.Log != nil {
		// Errors from go-oidc and oauth2 name hosts and claims, never secrets.
		s.Log.Warn("oidc: "+msg, "provider", slug, "err", strings.TrimSpace(err.Error()))
	}
}
