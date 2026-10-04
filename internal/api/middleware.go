package api

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

const (
	sessionCookie = "rivetpanel_session"
	csrfHeader    = "X-CSRF-Token"
)

func isSafeMethod(m string) bool {
	return m == fiber.MethodGet || m == fiber.MethodHead || m == fiber.MethodOptions
}

// originOK rejects cross-origin state-changing requests when the browser
// supplies an Origin header. Non-browser clients omit it and rely on the CSRF token.
func originOK(c fiber.Ctx) bool {
	o := c.Get(fiber.HeaderOrigin)
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, c.Hostname()+portSuffix(c))
}

func portSuffix(c fiber.Ctx) string {
	h := c.Host()
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		return h[i:]
	}
	return ""
}

func (s *panel) checkOrigin(c fiber.Ctx) error {
	if !isSafeMethod(c.Method()) && !originOK(c) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin request rejected")
	}
	return c.Next()
}

// requireAuth resolves the session cookie and enforces CSRF on unsafe methods.
// An "Authorization: Bearer rvc_..." header authenticates an API client
// instead (no cookie is read, so CSRF does not apply); the request then acts
// as the client's creator limited to the client (see clientGuard).
func (s *panel) requireAuth(c fiber.Ctx) error {
	if s.clients != nil {
		if b, ok := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer "); ok && strings.HasPrefix(strings.TrimSpace(b), service.APIClientPrefix) {
			u, err := s.clients.Authenticate(c.Context(), strings.Clone(strings.TrimSpace(b)))
			if err != nil {
				c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="rivetpanel", error="invalid_token"`)
				return fiber.NewError(fiber.StatusUnauthorized, "the API client token is unknown, expired or revoked")
			}
			c.Locals(keyUser, u)
			if err := s.clientGuard(c, u.Client); err != nil {
				s.auditClientRefusal(c, u)
				return err
			}
			return c.Next()
		}
	}
	token := strings.Clone(c.Cookies(sessionCookie))
	u, err := s.auth.Authenticate(c.Context(), token)
	if err != nil {
		return err
	}
	if !isSafeMethod(c.Method()) && !auth.CSRFValid(token, c.Get(csrfHeader)) {
		return fiber.NewError(fiber.StatusForbidden, "invalid or missing CSRF token")
	}
	c.Locals(keyUser, u)
	c.Locals(keyToken, token)
	return c.Next()
}

func (s *panel) requireAdmin(c fiber.Ctx) error {
	if !currentUser(c).IsAdmin() {
		return domain.ErrForbidden
	}
	return c.Next()
}

// requirePerm refuses (403) an account whose role does not hold permission
// p, after the unverified-email policy (see domain.User.Can). It is the
// server-side enforcement point for role permissions on routes.
func (s *panel) requirePerm(p string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if u := currentUser(c); !u.Can(p) {
			return domain.Denied(u, p)
		}
		return c.Next()
	}
}

// requireAnyPerm passes when the account holds at least one of ps.
func (s *panel) requireAnyPerm(ps ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		u := currentUser(c)
		for _, p := range ps {
			if u.Can(p) {
				return c.Next()
			}
		}
		return domain.Denied(u, ps[0])
	}
}

// clientSessionOnly are routes an API client can never use: managing the
// account's own credentials, sessions, sign-in methods and profile, and
// accepting invitations. A leaked client must not be able to mint longer-lived
// or wider credentials, lock the owner out or change who the account is.
var clientSessionOnly = []string{
	"/auth/logout", "/me/password", "/me/mfa", "/me/tokens", "/me/api-keys", "/me/api-clients",
	"/me/sessions", "/me/connections", "/me/profile", "/me/email", "/me/email-alerts", "/me/email-news",
	"/me/passkeys", "/me/identities", "/invites/accept", "/admin/api-clients",
	// The notification inbox carries ticket replies and alerts about every
	// resource of the account; preferences could silence them.
	"/notifications", "/me/notification-prefs",
}

// clientScopedAllowed are the top-level API areas a client limited to some
// bots or workspaces may reach; everything else (administration, sites,
// account-wide activity, the AI assistant) is outside its scope.
var clientScopedAllowed = map[string]bool{
	"bots": true, "workspaces": true, "games": true, "runtimes": true, "templates": true,
	"modules": true, "addons": true, "blueprints": true, "sdk": true,
}

// clientGuard is the middleware-level enforcement of an API client's scope
// (BotService re-checks the bot and workspace scope on every path to a bot).
// Permissions are enforced by requirePerm and the services, because
// domain.User.Can intersects the client's list with the creator's role.
func (s *panel) clientGuard(c fiber.Ctx, cl *domain.ClientScope) error {
	p := strings.TrimPrefix(c.Path(), "/api/v1")
	for _, pre := range clientSessionOnly {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return fiber.NewError(fiber.StatusForbidden, "API clients cannot use this; sign in to the panel")
		}
	}
	if !cl.Scoped() {
		return nil
	}
	seg := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/auth/me", p == "/me/capacity", p == "/schedules/preview":
		return nil
	case !clientScopedAllowed[seg[0]]:
		return &domain.ScopeError{What: "part of the API"}
	case seg[0] == "bots" && len(seg) > 1 && seg[1] != "batch":
		if b, err := s.bots.Store.GetBot(c.Context(), seg[1]); err == nil && !cl.AllowsBot(b.ID, b.WorkspaceID) {
			return &domain.ScopeError{What: "bot"}
		}
	case seg[0] == "workspaces" && len(seg) == 1 && c.Method() == fiber.MethodPost:
		return &domain.ScopeError{What: "action (creating workspaces)"}
	case seg[0] == "workspaces" && len(seg) > 1 && !cl.AllowsWorkspace(seg[1]):
		return &domain.ScopeError{What: "workspace"}
	}
	return nil
}

// auditClientRefusal records a changing request an API client was refused
// before it reached a route (outside its scope or a session-only route).
func (s *panel) auditClientRefusal(c fiber.Ctx, u domain.User) {
	if s.audit == nil || isSafeMethod(c.Method()) {
		return
	}
	ip := c.IP()
	target := c.Method() + " " + strings.TrimPrefix(c.Path(), "/api/v1")
	if len(target) > 200 {
		target = target[:197] + "..."
	}
	label := u.Email + " via API client " + u.Client.Name
	s.audit.Record(c.Context(), domain.AuditEvent{Action: "account.api_client_refused", Outcome: "denied",
		ActorID: &u.ID, ActorLabel: &label, SubjectUserID: &u.ID, Target: &target, IP: &ip})
}
