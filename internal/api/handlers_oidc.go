package api

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// OpenID Connect sign-in. The browser flow mirrors GitHub/Discord sign-in
// (state cookie, server-side pending state, redirect to the UI with an error
// code); the provider configuration lives in the database.

func (s *panel) oidcRoutes(v1 fiber.Router, limit fiber.Handler) {
	v1.Get("/auth/oidc/:slug/login", limit, s.oidcLogin)
	v1.Get("/auth/oidc/:slug/callback", limit, s.oidcCallback)
}

func (s *panel) oidcAuthedRoutes(authed fiber.Router) {
	authed.Get("/me/identities", s.listIdentities)
	authed.Post("/me/identities/:slug/start", s.startIdentityLink)
	authed.Delete("/me/identities/:pid", s.unlinkIdentity)
	authed.Get("/admin/oidc-providers", s.requirePerm(domain.PermSettingsManage), s.listOIDCProviders)
	authed.Post("/admin/oidc-providers", s.requirePerm(domain.PermSettingsManage), s.createOIDCProvider)
	authed.Patch("/admin/oidc-providers/:id", s.requirePerm(domain.PermSettingsManage), s.patchOIDCProvider)
	authed.Delete("/admin/oidc-providers/:id", s.requirePerm(domain.PermSettingsManage), s.deleteOIDCProvider)
}

func (s *panel) oidcLogin(c fiber.Ctx) error {
	redirect, binder, err := s.oidc.Begin(c.Context(), strings.Clone(c.Params("slug")), "")
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) {
			return s.oauthDone(c, "/login", oe.Code)
		}
		return err
	}
	s.setOAuthCookie(c, binder)
	return c.Redirect().Status(fiber.StatusFound).To(redirect)
}

func (s *panel) oidcCallback(c fiber.Ctx) error {
	slug := strings.Clone(c.Params("slug"))
	binder := string([]byte(c.Cookies(oauthCookie)))
	state, code, denied := string([]byte(c.Query("state"))), string([]byte(c.Query("code"))), c.Query("error") != ""
	s.setOAuthCookie(c, "")
	if denied {
		return s.oauthDone(c, "/login", "denied")
	}
	res, err := s.oidc.Complete(c.Context(), slug, code, state, binder, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return s.oauthDone(c, "/login", "state_invalid")
		}
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			s.log.Error("oidc callback failed", "provider", slug, "err", err)
			oe = &service.OAuthError{Code: "server_error"}
		}
		dest := "/login"
		if oe.Link {
			dest = "/settings/connected-accounts"
		}
		return s.oauthDone(c, dest, oe.Code)
	}
	if res.Linked {
		s.recordIdentity(c, res.User, "account.identity_link", slug)
		return c.Redirect().Status(fiber.StatusFound).To("/settings/connected-accounts?linked=" + url.QueryEscape(slug))
	}
	if res.Method == "email_link" || res.Method == "signup" {
		s.recordIdentity(c, res.User, "account.identity_"+res.Method, slug)
	}
	// Two-step accounts still need their second factor.
	if pending, err := s.beginMFA(c, res.Session.User, "oidc:"+slug); err != nil || pending {
		_ = s.auth.Logout(c.Context(), res.Session.Token)
		if err != nil {
			return s.oauthDone(c, "/login", "server_error")
		}
		return c.Redirect().Status(fiber.StatusFound).To("/login?step=mfa")
	}
	s.setSessionCookie(c, res.Session.Token, time.UnixMilli(res.Session.ExpiresAtMS))
	s.recordSignIn(c, res.Session.User, "", nil, "oidc:"+slug)
	return c.Redirect().Status(fiber.StatusFound).To("/login?complete=1")
}

// recordIdentity audits a link or an account created on first sign-in.
func (s *panel) recordIdentity(c fiber.Ctx, u domain.User, action, slug string) {
	if s.audit == nil {
		return
	}
	ip := c.IP()
	s.audit.Record(c.Context(), domain.AuditEvent{Action: action, Outcome: "ok", ActorID: &u.ID, ActorLabel: &u.Email,
		SubjectUserID: &u.ID, Target: &slug, IP: &ip})
}

type identityDTO struct {
	ProviderID    string `json:"provider_id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	LastLoginAtMS *int64 `json:"last_login_at_ms"`
	CanUnlink     bool   `json:"can_unlink"`
}

func (s *panel) listIdentities(c fiber.Ctx) error {
	if s.oidc == nil {
		return c.JSON(fiber.Map{"identities": []identityDTO{}, "available": []fiber.Map{}})
	}
	ids, avail, err := s.oidc.Identities(c.Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	out := make([]identityDTO, len(ids))
	for i, x := range ids {
		out[i] = identityDTO{x.ProviderID, x.ProviderSlug, x.ProviderName, x.Email, x.EmailVerified, x.CreatedAtMS, x.LastLoginAtMS, x.CanUnlink}
	}
	av := make([]fiber.Map, len(avail))
	for i, p := range avail {
		av[i] = fiber.Map{"slug": p.Slug, "name": p.Name}
	}
	return c.JSON(fiber.Map{"identities": out, "available": av})
}

// startIdentityLink begins linking a provider to the signed-in account (POST
// + CSRF so a third-party page cannot start it).
func (s *panel) startIdentityLink(c fiber.Ctx) error {
	if s.oidc == nil {
		return fiber.ErrNotFound
	}
	redirect, binder, err := s.oidc.Begin(c.Context(), strings.Clone(c.Params("slug")), currentUser(c).ID)
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) {
			return fiber.NewError(fiber.StatusBadGateway, "the provider's OpenID configuration could not be read")
		}
		return err
	}
	s.setOAuthCookie(c, binder)
	return c.JSON(fiber.Map{"url": redirect})
}

func (s *panel) unlinkIdentity(c fiber.Ctx) error {
	if s.oidc == nil {
		return fiber.ErrNotFound
	}
	id, err := s.oidc.Unlink(c.Context(), currentUser(c).ID, strings.Clone(c.Params("pid")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, id.ProviderSlug)
	return c.SendStatus(fiber.StatusNoContent)
}

type oidcProviderDTO struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Issuer        string `json:"issuer"`
	ClientID      string `json:"client_id"`
	SecretSet     bool   `json:"secret_set"`
	Scopes        string `json:"scopes"`
	Enabled       bool   `json:"enabled"`
	AllowSignup   bool   `json:"allow_signup"`
	LinkByEmail   bool   `json:"link_by_email"`
	DefaultRoleID string `json:"default_role_id"`
	RedirectURI   string `json:"redirect_uri"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	UpdatedAtMS   int64  `json:"updated_at_ms"`
}

func (s *panel) toOIDCProvider(p domain.OIDCProvider) oidcProviderDTO {
	return oidcProviderDTO{p.ID, p.Slug, p.Name, p.Issuer, p.ClientID, p.HasSecret(), p.Scopes, p.Enabled, p.AllowSignup, p.LinkByEmail,
		p.DefaultRoleID, s.oidc.RedirectURI(p.Slug), p.CreatedAtMS, p.UpdatedAtMS}
}

func (s *panel) listOIDCProviders(c fiber.Ctx) error {
	ps, err := s.oidc.ListProviders(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]oidcProviderDTO, len(ps))
	for i, p := range ps {
		out[i] = s.toOIDCProvider(p)
	}
	return c.JSON(fiber.Map{"providers": out, "public_url": s.oidc.RedirectURI("x") != "/api/v1/auth/oidc/x/callback",
		"redirect_base": strings.TrimSuffix(s.oidc.RedirectURI("SLUG"), "SLUG/callback")})
}

type oidcProviderBody struct {
	Slug          string  `json:"slug"`
	Name          string  `json:"name"`
	Issuer        string  `json:"issuer"`
	ClientID      string  `json:"client_id"`
	ClientSecret  *string `json:"client_secret"`
	Scopes        string  `json:"scopes"`
	Enabled       bool    `json:"enabled"`
	AllowSignup   bool    `json:"allow_signup"`
	LinkByEmail   bool    `json:"link_by_email"`
	DefaultRoleID string  `json:"default_role_id"`
}

func (b oidcProviderBody) input() service.OIDCProviderInput {
	return service.OIDCProviderInput{Slug: b.Slug, Name: b.Name, Issuer: b.Issuer, ClientID: b.ClientID, ClientSecret: b.ClientSecret,
		Scopes: b.Scopes, Enabled: b.Enabled, AllowSignup: b.AllowSignup, LinkByEmail: b.LinkByEmail, DefaultRoleID: b.DefaultRoleID}
}

func oidcAuditTarget(p domain.OIDCProvider) string {
	t := p.Name + " (" + p.Slug + ", " + p.Issuer + ")"
	if p.AllowSignup {
		role := p.DefaultRoleID
		if role == "" {
			role = "user"
		}
		t += ", sign-up as " + role
	}
	if len(t) > 400 {
		t = t[:397] + "..."
	}
	return t
}

func (s *panel) createOIDCProvider(c fiber.Ctx) error {
	var in oidcProviderBody
	if err := decode(c, &in); err != nil {
		return err
	}
	p, err := s.oidc.CreateProvider(c.Context(), currentUser(c), in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, oidcAuditTarget(p))
	return c.Status(fiber.StatusCreated).JSON(s.toOIDCProvider(p))
}

func (s *panel) patchOIDCProvider(c fiber.Ctx) error {
	var in oidcProviderBody
	if err := decode(c, &in); err != nil {
		return err
	}
	p, err := s.oidc.UpdateProvider(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, oidcAuditTarget(p))
	return c.JSON(s.toOIDCProvider(p))
}

func (s *panel) deleteOIDCProvider(c fiber.Ctx) error {
	p, err := s.oidc.DeleteProvider(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, oidcAuditTarget(p))
	return c.SendStatus(fiber.StatusNoContent)
}
