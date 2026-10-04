package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Passkeys (WebAuthn): registered under Settings → Security, used to sign in
// without a password, or instead of a code as the second step.

func (s *panel) passkeyPublicRoutes(v1 fiber.Router) {
	lim := limiter.New(limiter.Config{
		Max: 20, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "passkey:" + c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many attempts; try again in a minute")
		},
	})
	v1.Post("/auth/passkey/begin", lim, s.passkeyLoginBegin)
	v1.Post("/auth/passkey/finish", lim, s.passkeyLoginFinish)
	if s.mfa != nil {
		v1.Post("/auth/mfa/passkey/begin", lim, s.passkeyMFABegin)
		v1.Post("/auth/mfa/passkey/finish", lim, s.passkeyMFAFinish)
	}
}

func (s *panel) passkeyRoutes(authed fiber.Router, passwordLimit fiber.Handler) {
	authed.Get("/me/passkeys", s.listPasskeys)
	authed.Post("/me/passkeys/register/begin", passwordLimit, s.passkeyRegisterBegin)
	authed.Post("/me/passkeys/register/finish", passwordLimit, s.passkeyRegisterFinish)
	authed.Patch("/me/passkeys/:id", s.renamePasskey)
	authed.Delete("/me/passkeys/:id", s.deletePasskey)
}

type passkeyDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAtMS  int64  `json:"created_at_ms"`
	LastUsedAtMS *int64 `json:"last_used_at_ms"`
}

func (s *panel) listPasskeys(c fiber.Ctx) error {
	ks, err := s.passkeys.List(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	out := make([]passkeyDTO, len(ks))
	for i, k := range ks {
		out[i] = passkeyDTO{k.ID, k.Name, k.CreatedAtMS, k.LastUsedAtMS}
	}
	return c.JSON(fiber.Map{"passkeys": out, "available": s.passkeys.Available()})
}

// passkeyRegisterBegin needs the current password (or, without one, a recent
// sign-in), like turning on two-step sign-in.
func (s *panel) passkeyRegisterBegin(c fiber.Ctx) error {
	var in struct {
		Password string `json:"password"`
	}
	if len(c.Body()) > 0 {
		if err := decode(c, &in); err != nil {
			return err
		}
	}
	u := currentUser(c)
	if s.mfa != nil {
		if err := s.mfa.RecentAuth(c.Context(), u, currentToken(c), in.Password); err != nil {
			return err
		}
	}
	opts, ticket, err := s.passkeys.BeginRegistration(c.Context(), u)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"ticket": ticket, "options": opts})
}

func (s *panel) passkeyRegisterFinish(c fiber.Ctx) error {
	var in struct {
		Ticket     string          `json:"ticket"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	k, err := s.passkeys.FinishRegistration(c.Context(), currentUser(c), in.Ticket, in.Name, in.Credential)
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, k.Name)
	return c.Status(fiber.StatusCreated).JSON(passkeyDTO{k.ID, k.Name, k.CreatedAtMS, k.LastUsedAtMS})
}

func (s *panel) renamePasskey(c fiber.Ctx) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.passkeys.Rename(c.Context(), currentUser(c), strings.Clone(c.Params("id")), in.Name); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) deletePasskey(c fiber.Ctx) error {
	k, err := s.passkeys.Delete(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, k.Name)
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) passkeyLoginBegin(c fiber.Ctx) error {
	opts, ticket, err := s.passkeys.BeginLogin()
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"ticket": ticket, "options": opts})
}

func (s *panel) passkeyLoginFinish(c fiber.Ctx) error {
	var in struct {
		Ticket     string          `json:"ticket"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u, err := s.passkeys.FinishLogin(c.Context(), in.Ticket, in.Credential)
	if err != nil {
		s.recordPasskeyFailure(c)
		return err
	}
	sess, err := s.auth.IssueSessionFor(c.Context(), u, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	s.recordSignIn(c, sess.User, "", err, "passkey")
	if err != nil {
		return err
	}
	s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
	return c.JSON(fiber.Map{"user": toUser(sess.User), "csrf_token": sess.CSRF})
}

func (s *panel) passkeyMFABegin(c fiber.Ctx) error {
	opts, ticket, err := s.passkeys.BeginSecondFactor(c.Context(), strings.Clone(c.Cookies(mfaCookie)))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"ticket": ticket, "options": opts})
}

func (s *panel) passkeyMFAFinish(c fiber.Ctx) error {
	var in struct {
		Ticket     string          `json:"ticket"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	sess, method, err := s.passkeys.FinishSecondFactor(c.Context(), strings.Clone(c.Cookies(mfaCookie)), in.Ticket, in.Credential)
	if err != nil {
		return err
	}
	s.setMFACookie(c, "")
	s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
	s.recordSignIn(c, sess.User, "", nil, method+"+passkey")
	return c.JSON(fiber.Map{"user": toUser(sess.User), "csrf_token": sess.CSRF})
}

// recordPasskeyFailure notes a refused passkey sign-in (no account is known).
func (s *panel) recordPasskeyFailure(c fiber.Ctx) {
	if s.audit == nil {
		return
	}
	ip, m := c.IP(), "passkey"
	s.audit.Record(c.Context(), domain.AuditEvent{Action: "account.sign_in", Outcome: "denied", Target: &m, IP: &ip})
}
