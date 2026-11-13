package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// The account switcher keeps the other accounts signed in on this browser
// as their session tokens in one HttpOnly, SameSite=Strict cookie (never
// readable by scripts). The active account is the ordinary session cookie;
// switching swaps a stashed token with it. Every stashed token is checked
// against the database on use, so a revoked, expired or disabled session
// silently drops out.
const (
	accountsCookie = "rivetpanel_accounts"
	// maxStashed bounds the accounts kept beside the active one.
	maxStashed = 8
)

func (s *panel) stashedTokens(c fiber.Ctx) []string {
	raw := c.Cookies(accountsCookie)
	if raw == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(raw, ".") {
		if t != "" && len(t) <= 128 && len(out) < maxStashed {
			out = append(out, strings.Clone(t))
		}
	}
	return out
}

func (s *panel) setStash(c fiber.Ctx, tokens []string) {
	exp := time.Now().Add(s.auth.TTL)
	if len(tokens) == 0 {
		exp = time.Unix(0, 0)
	}
	c.Cookie(&fiber.Cookie{
		Name: accountsCookie, Value: strings.Join(tokens, "."), Path: "/", Expires: exp,
		HTTPOnly: true, Secure: s.secureCookies, SameSite: fiber.CookieSameSiteStrictMode,
	})
}

type stashed struct {
	token string
	user  domain.User
	sess  domain.Session
}

// resolveStash returns the stashed accounts that are still signed in, one per
// account and never the active account (skipID), and rewrites the cookie
// when it dropped any. Duplicates and impersonation sessions found there are
// signed out.
func (s *panel) resolveStash(c fiber.Ctx, skipID string) []stashed {
	toks := s.stashedTokens(c)
	var out []stashed
	seen := map[string]bool{skipID: true}
	for _, t := range toks {
		u, sess, err := s.auth.AuthenticateSession(c.Context(), t)
		if err != nil {
			continue
		}
		if seen[u.ID] || u.Impersonator != nil {
			_ = s.auth.Logout(c.Context(), t)
			continue
		}
		seen[u.ID] = true
		out = append(out, stashed{t, u, sess})
	}
	if len(out) != len(toks) {
		s.writeStash(c, out)
	}
	return out
}

func (s *panel) writeStash(c fiber.Ctx, xs []stashed) {
	toks := make([]string, len(xs))
	for i, x := range xs {
		toks[i] = x.token
	}
	s.setStash(c, toks)
}

// pushStash puts token first in the stash. Over the cap, the oldest stashed
// accounts are signed out.
func (s *panel) pushStash(c fiber.Ctx, token string, rest []stashed) {
	toks := []string{token}
	for _, x := range rest {
		if x.token == token {
			continue
		}
		if len(toks) == maxStashed {
			_ = s.auth.Logout(c.Context(), x.token)
			continue
		}
		toks = append(toks, x.token)
	}
	s.setStash(c, toks)
}

type accountDTO struct {
	SessionID string  `json:"session_id"`
	User      userDTO `json:"user"`
}

func impersonatorInfo(u domain.User) fiber.Map {
	if u.Impersonator == nil {
		return nil
	}
	return fiber.Map{"id": u.Impersonator.ID, "email": u.Impersonator.Email}
}

// listAccounts answers the account switcher: the active account (if any)
// and the other accounts signed in on this browser. It needs no session, so
// the sign-in page can offer the accounts that are still signed in.
func (s *panel) listAccounts(c fiber.Ctx) error {
	cur, _, err := s.auth.AuthenticateSession(c.Context(), strings.Clone(c.Cookies(sessionCookie)))
	out := fiber.Map{"current": nil, "impersonator": nil}
	if err == nil {
		out["current"], out["impersonator"] = toUser(cur), impersonatorInfo(cur)
	}
	list := []accountDTO{}
	for _, x := range s.resolveStash(c, cur.ID) {
		list = append(list, accountDTO{x.sess.ID, toUser(x.user)})
	}
	out["accounts"] = list
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(out)
}

// activate makes token the active session and answers like a sign-in.
func (s *panel) activate(c fiber.Ctx, token string, u domain.User, expMS int64) error {
	s.setSessionCookie(c, token, time.UnixMilli(expMS))
	return c.JSON(fiber.Map{"user": toUser(u), "csrf_token": auth.CSRFToken(token), "impersonator": impersonatorInfo(u)})
}

// switchAccount makes a stashed account the active one. The account that was
// active is stashed in its place; an impersonation that was active ends.
func (s *panel) switchAccount(c fiber.Ctx) error {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	curTok := strings.Clone(c.Cookies(sessionCookie))
	cur, _, curErr := s.auth.AuthenticateSession(c.Context(), curTok)
	list := s.resolveStash(c, cur.ID)
	idx := -1
	for i, x := range list {
		if x.sess.ID == in.SessionID {
			idx = i
		}
	}
	if idx < 0 {
		return domain.Invalid("that account is no longer signed in on this browser; sign in to it again")
	}
	next := list[idx]
	rest := append(append([]stashed{}, list[:idx]...), list[idx+1:]...)
	switch {
	case curErr != nil:
		s.writeStash(c, rest)
	case cur.Impersonator != nil:
		_ = s.auth.Logout(c.Context(), curTok)
		s.auditImpersonation(c, "admin.impersonate_stop", cur)
		s.writeStash(c, rest)
	default:
		s.pushStash(c, curTok, rest)
	}
	return s.activate(c, next.token, next.user, next.sess.ExpiresAtMS)
}

// parkAccount keeps the active account signed in on this browser and clears
// the active session, so the sign-in page can add another account (with any
// sign-in method, provider redirects included).
func (s *panel) parkAccount(c fiber.Ctx) error {
	u := currentUser(c)
	if u.Impersonator != nil {
		return domain.Invalid("return to your own account before adding another")
	}
	list := s.resolveStash(c, u.ID)
	if len(list) >= maxStashed {
		return domain.Invalid("up to 9 accounts stay signed in on one browser; sign out of one first")
	}
	s.pushStash(c, currentToken(c), list)
	s.setSessionCookie(c, "", time.Unix(0, 0))
	return c.SendStatus(fiber.StatusNoContent)
}

// forgetAccount signs out one of the stashed accounts.
func (s *panel) forgetAccount(c fiber.Ctx) error {
	sid := strings.Clone(c.Params("sid"))
	cur, _, _ := s.auth.AuthenticateSession(c.Context(), strings.Clone(c.Cookies(sessionCookie)))
	list := s.resolveStash(c, cur.ID)
	for i, x := range list {
		if x.sess.ID == sid {
			_ = s.auth.Logout(c.Context(), x.token)
			s.writeStash(c, append(list[:i:i], list[i+1:]...))
			return c.SendStatus(fiber.StatusNoContent)
		}
	}
	return domain.ErrNotFound
}

// impersonate signs the administrator in as another account in one step.
// The administrator's own session stays signed in on this browser and comes
// back when the impersonation ends.
func (s *panel) impersonate(c fiber.Ctx) error {
	actor := currentUser(c)
	tok := currentToken(c)
	_, sess, err := s.auth.AuthenticateSession(c.Context(), tok)
	if err != nil {
		return err
	}
	ns, err := s.auth.Impersonate(c.Context(), actor, sess, strings.Clone(c.Params("id")), deviceLabel(c.Get(fiber.HeaderUserAgent)))
	if err != nil {
		return err
	}
	s.pushStash(c, tok, s.resolveStash(c, actor.ID))
	s.auditImpersonation(c, "admin.impersonate_start", ns.User)
	return s.activate(c, ns.Token, ns.User, ns.ExpiresAtMS)
}

// stopImpersonation ends the active impersonation and returns to the
// administrator's own account when it is still signed in on this browser.
func (s *panel) stopImpersonation(c fiber.Ctx) error {
	u := currentUser(c)
	if u.Impersonator == nil {
		return domain.Invalid("you are not signed in as another account")
	}
	return s.endImpersonation(c, u)
}

func (s *panel) endImpersonation(c fiber.Ctx, u domain.User) error {
	if err := s.auth.Logout(c.Context(), currentToken(c)); err != nil {
		return err
	}
	s.auditImpersonation(c, "admin.impersonate_stop", u)
	list := s.resolveStash(c, u.ID)
	for i, x := range list {
		if x.user.ID == u.Impersonator.ID {
			s.writeStash(c, append(list[:i:i], list[i+1:]...))
			return s.activate(c, x.token, x.user, x.sess.ExpiresAtMS)
		}
	}
	s.setSessionCookie(c, "", time.Unix(0, 0))
	return c.JSON(fiber.Map{"user": nil})
}

// auditImpersonation records the start or end of an impersonation in the
// administrator's and the account's activity.
func (s *panel) auditImpersonation(c fiber.Ctx, action string, target domain.User) {
	if s.audit == nil || target.Impersonator == nil {
		return
	}
	ip := c.IP()
	adminID, adminEmail := target.Impersonator.ID, target.Impersonator.Email
	s.audit.Record(c.Context(), domain.AuditEvent{Action: action, Outcome: "ok", ActorID: &adminID, ActorLabel: &adminEmail,
		SubjectUserID: &target.ID, Target: strPtr(target.Email), IP: &ip})
}

// impersonationBlocked are changes an administrator acting as an account
// cannot make: the account's credentials, sign-in methods, sessions,
// identity, email preferences and invitations stay with its owner.
var impersonationBlocked = []string{
	"/me/password", "/me/mfa", "/me/tokens", "/me/api-keys", "/me/api-clients", "/me/sessions",
	"/me/connections", "/me/email", "/me/email-alerts", "/me/email-news", "/me/notification-prefs",
	"/me/passkeys", "/me/identities", "/me/profile", "/me/avatar", "/invites/accept",
	"/auth/accounts", "/registration",
}

func impersonationGuard(c fiber.Ctx, u domain.User) error {
	if u.Impersonator == nil || isSafeMethod(c.Method()) {
		return nil
	}
	p := strings.TrimPrefix(c.Path(), "/api/v1")
	for _, pre := range impersonationBlocked {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return fiber.NewError(fiber.StatusForbidden, "not while signed in as another account: its sign-in, security and profile settings stay with its owner")
		}
	}
	return nil
}
