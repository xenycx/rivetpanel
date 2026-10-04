package api

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// resetAvailable is public: the sign-in page uses it to decide whether to
// offer "Forgot password?".
func (s *panel) resetAvailable(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"available": s.resets.Available(c.Context())})
}

// requestPasswordReset always answers the same way, so it cannot be used to
// learn which addresses have accounts.
func (s *panel) requestPasswordReset(c fiber.Ctx) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.resets.Request(c.Context(), in.Email); err != nil {
		s.log.Warn("password reset request failed", "err", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) confirmPasswordReset(c fiber.Ctx) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u, err := s.resets.Confirm(c.Context(), in.Token, in.Password)
	if err != nil {
		return err
	}
	if s.audit != nil {
		ip, target := c.IP(), "reset link"
		s.audit.Record(c.Context(), domain.AuditEvent{Action: "account.password_reset", Target: &target, IP: &ip,
			ActorID: &u.ID, ActorLabel: &u.Email, SubjectUserID: &u.ID, Outcome: "ok"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// testMail checks the Mailgun key and domain and sends one message.
func (s *panel) testMail(c fiber.Ctx) error {
	var in struct {
		To string `json:"to"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.To == "" {
		in.To = currentUser(c).Email
	}
	res, err := s.mail.Test(c.Context(), currentUser(c), in.To)
	if err != nil {
		return err
	}
	return c.JSON(res)
}

func (s *panel) putEmailAlerts(c fiber.Ctx) error {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return domain.Invalid("enabled is required")
	}
	if err := s.mailPrefs.SetUserEmailAlerts(c.Context(), currentUser(c).ID, *in.Enabled); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"email_alerts": *in.Enabled})
}

// notifySecurity emails the account about a security change, if email is on.
func (s *panel) notifySecurity(u domain.User, what string) {
	s.mail.Notify(u.Email, what)
}

func (s *panel) putEmailNews(c fiber.Ctx) error {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return domain.Invalid("enabled is required")
	}
	if err := s.mailPrefs.SetUserEmailNews(c.Context(), currentUser(c).ID, *in.Enabled); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"email_news": *in.Enabled})
}

// mailAudience says how many accounts each announcement audience reaches.
func (s *panel) mailAudience(c fiber.Ctx) error {
	counts, err := s.mail.Audience(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"counts": counts})
}

// sendAnnouncement emails administrator-written HTML to an audience, or only to
// the sender when test is set. The body is sanitized again inside the service.
func (s *panel) sendAnnouncement(c fiber.Ctx) error {
	var in struct {
		Subject  string `json:"subject"`
		HTML     string `json:"html"`
		Audience string `json:"audience"`
		Kind     string `json:"kind"`
		Test     bool   `json:"test"`
		// Email (default true) sends it by email; InPanel also posts it to
		// the audience's notification inbox (works without email set up).
		Email   *bool `json:"email"`
		InPanel bool  `json:"in_panel"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	email := in.Email == nil || *in.Email
	if in.Test {
		res, err := s.mail.Broadcast(c.Context(), u, in.Subject, in.HTML, in.Audience, in.Kind, true)
		if err != nil {
			return err
		}
		return c.JSON(res)
	}
	if !email && (!in.InPanel || s.notifications == nil) {
		return domain.Invalid("choose email, the notification inbox or both")
	}
	var out struct {
		service.BroadcastResult
		InPanel int `json:"in_panel"`
	}
	if email {
		res, err := s.mail.Broadcast(c.Context(), u, in.Subject, in.HTML, in.Audience, in.Kind, false)
		if err != nil {
			return err
		}
		out.BroadcastResult = res
	}
	if in.InPanel && s.notifications != nil {
		n, err := s.notifications.Announce(c.Context(), u, in.Subject, in.HTML, in.Audience)
		if err != nil {
			return err
		}
		out.InPanel = n
	}
	return c.JSON(out)
}

// announcementTarget records the subject, whether it was a test, and the
// audience, never the message body.
func announcementTarget(c fiber.Ctx) string {
	var in struct {
		Subject  string `json:"subject"`
		Audience string `json:"audience"`
		Test     bool   `json:"test"`
	}
	if json.Unmarshal(c.Body(), &in) != nil {
		return ""
	}
	t := in.Subject
	if len(t) > 80 {
		t = t[:80]
	}
	if in.Test {
		return "test: " + t
	}
	return in.Audience + ": " + t
}
