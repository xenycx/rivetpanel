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

const oauthCookie = "rivetpanel_oauth"

// The state cookie must survive the cross-site redirect back from the provider,
// so it is SameSite=Lax (Strict would be withheld) and scoped to the auth routes.
func (s *panel) setOAuthCookie(c fiber.Ctx, binder string) {
	exp := time.Now().Add(10 * time.Minute)
	if binder == "" {
		exp = time.Unix(0, 0)
	}
	c.Cookie(&fiber.Cookie{
		Name: oauthCookie, Value: binder, Path: "/api/v1/auth", Expires: exp,
		HTTPOnly: true, Secure: s.secureCookies, SameSite: fiber.CookieSameSiteLaxMode,
	})
}

func (s *panel) oauthEnabled(c fiber.Ctx) (string, error) {
	p := strings.Clone(c.Params("provider")) // request memory is reused; the flow outlives it
	if s.oauth == nil || !s.oauth.Enabled(p) {
		return "", fiber.ErrNotFound
	}
	return p, nil
}

// oauthProviders lists the providers configured on this panel (public).
func (s *panel) oauthProviders(c fiber.Ctx) error {
	out := []fiber.Map{}
	if s.oauth != nil {
		for _, p := range []string{domain.ProviderGitHub, domain.ProviderDiscord} {
			if s.oauth.Enabled(p) {
				out = append(out, fiber.Map{"id": p})
			}
		}
	}
	oidc := []fiber.Map{}
	for _, p := range s.oidc.PublicProviders(c.Context()) {
		oidc = append(oidc, fiber.Map{"slug": p.Slug, "name": p.Name})
	}
	return c.JSON(fiber.Map{"providers": out, "oidc": oidc, "passkeys": s.passkeys != nil && s.passkeys.Available()})
}

// oauthLogin starts a sign-in flow.
func (s *panel) oauthLogin(c fiber.Ctx) error {
	p, err := s.oauthEnabled(c)
	if err != nil {
		return err
	}
	redirect, binder, err := s.oauth.Begin(p, "", false, false)
	if err != nil {
		return err
	}
	s.setOAuthCookie(c, binder)
	return c.Redirect().Status(fiber.StatusFound).To(redirect)
}

// oauthCallback finishes either flow and always answers with a redirect to the UI.
func (s *panel) oauthCallback(c fiber.Ctx) error {
	p, err := s.oauthEnabled(c)
	if err != nil {
		return err
	}
	binder := string([]byte(c.Cookies(oauthCookie))) // copy: request memory is reused
	state, code, denied := string([]byte(c.Query("state"))), string([]byte(c.Query("code"))), c.Query("error") != ""
	s.setOAuthCookie(c, "")

	if denied {
		return s.oauthDone(c, "/login", "denied")
	}
	res, err := s.oauth.Complete(c.Context(), p, code, state, binder, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	if err != nil {
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			// Browser navigation: never show raw JSON for an internal failure.
			s.log.Error("oauth callback failed", "provider", p, "err", err)
			oe = &service.OAuthError{Code: "server_error"}
		}
		dest := "/login"
		if oe.Link {
			dest = "/settings/connected-accounts"
		}
		return s.oauthDone(c, dest, oe.Code)
	}
	if res.Linked {
		return c.Redirect().Status(fiber.StatusFound).To("/settings/connected-accounts?linked=" + url.QueryEscape(p))
	}
	// Two-step accounts: the provider session is discarded unused and the
	// browser finishes on the sign-in page with a code.
	if pending, err := s.beginMFA(c, res.Session.User, p); err != nil || pending {
		_ = s.auth.Logout(c.Context(), res.Session.Token)
		if err != nil {
			return s.oauthDone(c, "/login", "server_error")
		}
		return c.Redirect().Status(fiber.StatusFound).To("/login?step=mfa")
	}
	s.setSessionCookie(c, res.Session.Token, time.UnixMilli(res.Session.ExpiresAtMS))
	s.recordSignIn(c, res.Session.User, "", nil, p)
	// Let the sign-in page consume the browser's remembered deep link. Without
	// one, it sends the user to /dashboard; the public home remains at /.
	return c.Redirect().Status(fiber.StatusFound).To("/login?complete=1")
}

func (s *panel) oauthDone(c fiber.Ctx, dest, code string) error {
	return c.Redirect().Status(fiber.StatusFound).To(dest + "?error=" + url.QueryEscape(code))
}

type connectionDTO struct {
	Provider      string `json:"provider"`
	Configured    bool   `json:"configured"`
	Linked        bool   `json:"linked"`
	Username      string `json:"username"`
	AvatarURL     string `json:"avatar_url"`
	Notifications bool   `json:"notifications"`
	RepoAccess    bool   `json:"repo_access"`
	CanDisconnect bool   `json:"can_disconnect"`
}

func (s *panel) listConnections(c fiber.Ctx) error {
	if s.oauth == nil {
		return c.JSON(fiber.Map{"connections": []connectionDTO{}})
	}
	cs, err := s.oauth.Connections(c.Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	out := make([]connectionDTO, len(cs))
	for i, x := range cs {
		out[i] = connectionDTO{x.Provider, x.Configured, x.Linked, x.Username, x.AvatarURL, x.Notifications, x.RepoAccess, x.CanDisconnect}
	}
	return c.JSON(fiber.Map{"connections": out})
}

// startConnection begins a link flow for the signed-in user and returns the
// provider URL; the browser navigates to it. POST + CSRF keeps a third-party
// page from initiating a link.
func (s *panel) startConnection(c fiber.Ctx) error {
	p, err := s.oauthEnabled(c)
	if err != nil {
		return err
	}
	var in struct {
		Notifications bool `json:"notifications"`
		RepoAccess    bool `json:"repo_access"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	redirect, binder, err := s.oauth.Begin(p, currentUser(c).ID, in.Notifications, in.RepoAccess)
	if err != nil {
		return err
	}
	s.setOAuthCookie(c, binder)
	return c.JSON(fiber.Map{"url": redirect})
}

func (s *panel) deleteConnection(c fiber.Ctx) error {
	if s.oauth == nil {
		return fiber.ErrNotFound
	}
	if err := s.oauth.Disconnect(c.Context(), currentUser(c).ID, strings.Clone(c.Params("provider"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
