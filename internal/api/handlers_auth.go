package api

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func (s *panel) setSessionCookie(c fiber.Ctx, token string, exp time.Time) {
	c.Cookie(&fiber.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp,
		HTTPOnly: true, Secure: s.secureCookies, SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func (s *panel) login(c fiber.Ctx) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	// The password is hashed once; the session is issued only after the second
	// step when the account has two-step sign-in.
	u, err := s.auth.CheckPassword(c.Context(), in.Email, in.Password)
	if err != nil {
		s.recordSignIn(c, u, strings.Clone(in.Email), err, "password")
		return err
	}
	if pending, err := s.beginMFA(c, u, "password"); err != nil {
		return err
	} else if pending {
		return c.JSON(fiber.Map{"mfa_required": true})
	}
	sess, err := s.auth.IssueSessionFor(c.Context(), u, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	s.recordSignIn(c, sess.User, strings.Clone(in.Email), err, "password")
	if err != nil {
		return err
	}
	s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
	return c.JSON(fiber.Map{"user": toUser(sess.User), "csrf_token": sess.CSRF})
}

func (s *panel) logout(c fiber.Ctx) error {
	if err := s.auth.Logout(c.Context(), currentToken(c)); err != nil {
		return err
	}
	s.setSessionCookie(c, "", time.Unix(0, 0))
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) me(c fiber.Ctx) error {
	u := currentUser(c)
	csrf := ""
	if u.Client == nil { // API clients send no cookie and need no CSRF token
		csrf = auth.CSRFToken(currentToken(c))
	}
	return c.JSON(fiber.Map{"user": toUser(u), "csrf_token": csrf, "has_password": u.PasswordHash != "",
		// What this installation offers, so the interface can explain missing
		// features instead of calling routes that do not exist.
		"features": fiber.Map{
			"runner": s.bots.Notifier != nil, "console": s.console != nil, "stats": s.stats != nil, "files": s.files != nil,
			"deploy": s.deploy != nil && s.oauth.Enabled("github"), "backups": s.backups != nil, "analytics": s.analytics != nil, "sftp": s.sftp != nil,
			"operations": s.ops != nil, "oauth": s.oauth.AnyEnabled(), "schedules": s.schedules != nil, "mfa": s.mfa != nil, "automation": s.tokens != nil, "health": s.health != nil,
			"sites": s.sites.Enabled(), "workspaces": true, "ai": s.ai != nil, "games": s.games != nil, "agents": s.enrollment != nil, "mail": s.mail.Enabled(c.Context()),
			// Public repositories deploy without GitHub sign-in; add-ons need the Docker runner.
			"public_repos": s.deploy != nil, "addons": s.bots.AddonData != nil,
			"api_clients": s.clients != nil, "passkeys": s.passkeys != nil, "usage": s.usage != nil,
		}, "email_alerts": s.emailAlerts(c), "email_news": s.emailNews(c),
		// Effective role permissions (after the unverified-email policy); the
		// interface uses them to show what the server will allow.
		"permissions": nonNil(u.EffectivePermissions()), "email_verification": s.emailVerification(c, u),
		"api_client": clientInfo(u)})
}

// clientInfo names the API client a request was authenticated by (nil for a
// browser session).
func clientInfo(u domain.User) fiber.Map {
	if u.Client == nil {
		return nil
	}
	return fiber.Map{"id": u.Client.ID, "name": u.Client.Name, "bot_ids": u.Client.BotIDs, "workspace_ids": u.Client.WorkspaceIDs}
}

func (s *panel) updateProfile(c fiber.Ctx) error {
	var in struct {
		DisplayName string  `json:"display_name"`
		Avatar      *string `json:"avatar_jpeg"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	var avatar []byte
	if in.Avatar != nil && *in.Avatar != "" {
		var err error
		avatar, err = base64.StdEncoding.DecodeString(*in.Avatar)
		if err != nil {
			return domain.Invalid("profile picture is not valid base64")
		}
	}
	u, err := s.auth.UpdateProfile(c.Context(), currentUser(c), in.DisplayName, avatar, in.Avatar != nil)
	if err != nil {
		return err
	}
	return c.JSON(toUser(u))
}

func (s *panel) userAvatar(c fiber.Ctx) error {
	u, err := s.auth.Store.GetUserByID(c.Context(), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	if len(u.AvatarJPEG) == 0 {
		return fiber.ErrNotFound
	}
	c.Set(fiber.HeaderContentType, "image/jpeg")
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.Send(u.AvatarJPEG)
}

// deviceLabel reduces a User-Agent to "Browser on OS" so the sessions page is
// recognizable without storing the raw header.
func deviceLabel(ua string) string {
	l := strings.ToLower(ua)
	browser := "Unknown browser"
	switch {
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "firefox/"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "chromium/"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	case strings.HasPrefix(l, "curl/"):
		browser = "curl"
	}
	os := ""
	switch {
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad"):
		os = "iOS"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "mac os") || strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}

type sessionDTO struct {
	ID           string `json:"id"`
	Device       string `json:"device"`
	CreatedAtMS  int64  `json:"created_at_ms"`
	LastSeenAtMS int64  `json:"last_seen_at_ms"`
	ExpiresAtMS  int64  `json:"expires_at_ms"`
	Current      bool   `json:"current"`
}

func (s *panel) listSessions(c fiber.Ctx) error {
	_, cur, err := s.auth.AuthenticateSession(c.Context(), currentToken(c))
	if err != nil {
		return err
	}
	ss, err := s.auth.ListSessions(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]sessionDTO, len(ss))
	for i, x := range ss {
		out[i] = sessionDTO{x.ID, x.Device, x.CreatedAtMS, x.LastSeenAtMS, x.ExpiresAtMS, x.ID == cur.ID}
	}
	return c.JSON(fiber.Map{"sessions": out})
}

func (s *panel) revokeSession(c fiber.Ctx) error {
	if err := s.auth.RevokeSession(c.Context(), currentUser(c), strings.Clone(c.Params("sid"))); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) revokeOtherSessions(c fiber.Ctx) error {
	n, err := s.auth.RevokeOtherSessions(c.Context(), currentUser(c), currentToken(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"revoked": n})
}

func (s *panel) changePassword(c fiber.Ctx) error {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.auth.ChangePassword(c.Context(), currentUser(c), currentToken(c), in.Current, in.New); err != nil {
		return err
	}
	s.notifySecurity(currentUser(c), "The password for this account was changed, and your other sessions were signed out.")
	return c.SendStatus(fiber.StatusNoContent)
}

// createUser adds an account with the built-in "admin" or "user" role or a
// custom role id. Only administrators create administrators; delegated
// account managers can only give roles whose permissions they hold.
func (s *panel) createUser(c fiber.Ctx) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	actor := currentUser(c)
	if in.Role == "" {
		in.Role = domain.RoleUser
	}
	base := in.Role
	if base != domain.RoleAdmin {
		base = domain.RoleUser
	}
	if base == domain.RoleAdmin && !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if in.Role != base || !actor.IsAdmin() {
		// Check the role (exists, grantable) before any account is created.
		r, err := s.auth.Store.GetRole(c.Context(), in.Role)
		if err != nil {
			return domain.Invalid("that role does not exist")
		}
		for _, p := range r.Permissions {
			if !actor.Can(p) {
				return domain.Invalid("you cannot grant " + p + ": you do not hold it yourself")
			}
		}
	}
	u, err := s.auth.CreateUser(c.Context(), in.Email, in.Password, base)
	if err != nil {
		return err
	}
	if in.Role != base {
		r, err := s.auth.AssignRole(c.Context(), actor, u.ID, in.Role)
		if err != nil {
			return err
		}
		u.RoleID, u.RoleName = r.ID, r.Name
		s.auditUser(c, "admin.user_role", u.ID, r.Name)
	}
	return c.Status(fiber.StatusCreated).JSON(toUser(u))
}

func (s *panel) listUsers(c fiber.Ctx) error {
	us, err := s.auth.ListUsers(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	counts, err := s.auth.BotCounts(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	type adminUserDTO struct {
		userDTO
		Bots        int  `json:"bots"`
		HasPassword bool `json:"has_password"`
	}
	out := make([]adminUserDTO, len(us))
	for i, u := range us {
		out[i] = adminUserDTO{toUser(u), counts[u.ID], u.PasswordHash != ""}
	}
	return c.JSON(fiber.Map{"users": out})
}

// patchUser changes an account's role ("admin", "user" or a custom role
// id), enabled state, email address (which then needs verifying again) or
// verified flag. stop_bots, with disabled=true, also stops every bot the
// account owns (offboarding).
func (s *panel) patchUser(c fiber.Ctx) error {
	var in struct {
		Disabled      *bool   `json:"disabled"`
		Role          *string `json:"role"`
		StopBots      bool    `json:"stop_bots"`
		Email         *string `json:"email"`
		EmailVerified *bool   `json:"email_verified"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Disabled == nil && in.Role == nil && in.Email == nil && in.EmailVerified == nil {
		return domain.Invalid("nothing to change")
	}
	id := strings.Clone(c.Params("id"))
	actor := currentUser(c)
	if in.Role != nil {
		r, err := s.auth.AssignRole(c.Context(), actor, id, strings.Clone(*in.Role))
		if err != nil {
			return err
		}
		s.auditUser(c, "admin.user_role", id, r.Name)
	}
	if in.Email != nil {
		prev, err := s.auth.SetEmail(c.Context(), actor, id, *in.Email)
		if err != nil {
			return err
		}
		if !strings.EqualFold(prev.Email, strings.TrimSpace(*in.Email)) {
			s.auditUser(c, "admin.user_email", id, prev.Email+" -> "+strings.ToLower(strings.TrimSpace(*in.Email)))
			s.mail.Notify(prev.Email, "An administrator changed this account's email address. Sign-in now uses the new address.")
		}
	}
	if in.EmailVerified != nil {
		if err := s.auth.SetEmailVerified(c.Context(), actor, id, *in.EmailVerified); err != nil {
			return err
		}
		what := "unverified"
		if *in.EmailVerified {
			what = "verified"
		}
		s.auditUser(c, "admin.user_email_verified", id, what)
	}
	if in.Disabled != nil {
		if err := s.auth.SetDisabled(c.Context(), actor, id, *in.Disabled); err != nil {
			return err
		}
		if *in.Disabled && in.StopBots {
			if _, err := s.bots.StopOwnedBy(c.Context(), actor, id); err != nil {
				return err
			}
		}
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// recordSignIn keeps successful and failed password/provider sign-ins in the
// account's activity (failed attempts only for existing accounts).
func (s *panel) recordSignIn(c fiber.Ctx, u domain.User, email string, err error, method string) {
	if s.audit == nil {
		return
	}
	ip := c.IP()
	ev := domain.AuditEvent{Action: "account.sign_in", Target: &method, IP: &ip}
	if err != nil {
		if norm, nerr := service.NormalizeEmail(email); nerr == nil {
			if acct, gerr := s.auth.Store.GetUserByEmail(c.Context(), norm); gerr == nil {
				ev.Outcome, ev.SubjectUserID, ev.ActorLabel = "denied", &acct.ID, &acct.Email
				s.audit.Record(c.Context(), ev)
			}
		}
		return
	}
	ev.ActorID, ev.ActorLabel, ev.SubjectUserID = &u.ID, &u.Email, &u.ID
	s.audit.Record(c.Context(), ev)
}

// emailAlerts is whether the signed-in account gets alert emails (true when
// the switch is not available).
func (s *panel) emailAlerts(c fiber.Ctx) bool {
	if s.mailPrefs == nil {
		return true
	}
	on, err := s.mailPrefs.UserEmailAlerts(c.Context(), currentUser(c).ID)
	return err != nil || on
}

// emailNews is whether the signed-in account gets optional news emails.
func (s *panel) emailNews(c fiber.Ctx) bool {
	if s.mailPrefs == nil {
		return true
	}
	on, err := s.mailPrefs.UserEmailNews(c.Context(), currentUser(c).ID)
	return err != nil || on
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
