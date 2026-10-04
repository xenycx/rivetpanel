package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// publicVerifyRoutes must be registered before the authenticated group: the
// link works without a session (the token is the proof).
func (s *panel) publicVerifyRoutes(v1 fiber.Router) {
	if s.verify == nil {
		return
	}
	v1.Post("/auth/email/verify", limiter.New(limiter.Config{
		Max: 20, Expiration: 10 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return "verify:" + c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many verification attempts; try again in a few minutes")
		},
	}), s.confirmEmail)
}

func (s *panel) verifyRoutes(authed fiber.Router) {
	if s.verify == nil {
		return
	}
	// Resends are limited per account here and to one a minute in the store.
	sendLimit := limiter.New(limiter.Config{
		Max: 5, Expiration: time.Hour,
		KeyGenerator: func(c fiber.Ctx) string { return "verify-send:" + currentUser(c).ID },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many verification emails were requested; try again later")
		},
	})
	authed.Post("/me/email/verification", sendLimit, s.sendVerification)
	authed.Post("/me/email", sendLimit, s.changeEmail)
}

// emailVerification is the account's verification state for /auth/me.
func (s *panel) emailVerification(c fiber.Ctx, u domain.User) fiber.Map {
	out := fiber.Map{"verified": u.EmailVerified, "withheld": u.Withheld, "available": false, "pending_email": "", "sent_at_ms": 0}
	if out["withheld"] == nil || len(u.Withheld) == 0 {
		out["withheld"] = []string{}
	}
	if s.verify != nil {
		out["available"] = s.verify.Available(c.Context())
		email, at := s.verify.Pending(c.Context(), u)
		out["pending_email"], out["sent_at_ms"] = email, at
	}
	return out
}

func (s *panel) sendVerification(c fiber.Ctx) error {
	if err := s.verify.Send(c.Context(), currentUser(c)); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusAccepted)
}

func (s *panel) changeEmail(c fiber.Ctx) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"current_password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.verify.RequestChange(c.Context(), currentUser(c), currentToken(c), in.Password, in.Email); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusAccepted)
}

func (s *panel) confirmEmail(c fiber.Ctx) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u, old, err := s.verify.Confirm(c.Context(), strings.Clone(in.Token))
	if err != nil {
		return err
	}
	if s.audit != nil {
		ip := c.IP()
		ev := domain.AuditEvent{Action: "account.email_verified", ActorID: &u.ID, ActorLabel: &u.Email, SubjectUserID: &u.ID, Outcome: "ok", IP: &ip}
		if old != "" {
			ev.Action = "account.email_changed"
			t := old + " -> " + u.Email
			ev.Target = &t
		}
		s.audit.Record(c.Context(), ev)
	}
	return c.JSON(fiber.Map{"email": u.Email, "changed": old != ""})
}
