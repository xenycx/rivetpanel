package api

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// The second-step ticket rides in its own cookie, scoped to the auth routes.
// Lax, like the OAuth binder: a provider sign-in sets it on the cross-site
// callback redirect.
const mfaCookie = "rivetpanel_mfa"

func (s *panel) setMFACookie(c fiber.Ctx, ticket string) {
	exp := time.Now().Add(5 * time.Minute)
	if ticket == "" {
		exp = time.Unix(0, 0)
	}
	c.Cookie(&fiber.Cookie{
		Name: mfaCookie, Value: ticket, Path: "/api/v1/auth", Expires: exp,
		HTTPOnly: true, Secure: s.secureCookies, SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// beginMFA replaces a first-step success with a second-step ticket when the
// account has two-step sign-in. It returns false when no second step is due.
func (s *panel) beginMFA(c fiber.Ctx, u domain.User, method string) (bool, error) {
	if s.mfa == nil {
		return false, nil
	}
	on, err := s.mfa.Required(c.Context(), u.ID)
	if err != nil || !on {
		return false, err
	}
	ticket, err := s.mfa.Challenge(u, deviceLabel(c.Get(fiber.HeaderUserAgent)), method)
	if err != nil {
		return false, err
	}
	s.setMFACookie(c, ticket)
	return true, nil
}

// completeMFA is POST /auth/mfa {code}: the second step of a sign-in.
func (s *panel) completeMFA(c fiber.Ctx) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	ticket := strings.Clone(c.Cookies(mfaCookie))
	if ticket == "" {
		return domain.Invalid("this sign-in expired; start again")
	}
	sess, method, err := s.mfa.Complete(c.Context(), ticket, in.Code)
	if err != nil {
		if sess.User.ID != "" {
			s.recordMFAFailure(c, sess.User, method)
		}
		return err
	}
	s.setMFACookie(c, "")
	s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
	s.recordSignIn(c, sess.User, "", nil, method+"+totp")
	return c.JSON(fiber.Map{"user": toUser(sess.User), "csrf_token": sess.CSRF})
}

func (s *panel) recordMFAFailure(c fiber.Ctx, u domain.User, method string) {
	if s.audit == nil {
		return
	}
	ip, target := c.IP(), method+"+totp"
	s.audit.Record(c.Context(), domain.AuditEvent{Action: "account.sign_in", Target: &target, IP: &ip, Outcome: "denied",
		SubjectUserID: &u.ID, ActorLabel: &u.Email})
}

func (s *panel) mfaStatus(c fiber.Ctx) error {
	st, err := s.mfa.Status(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"enabled": st.Enabled, "enabled_at_ms": st.EnabledAtMS, "recovery_left": st.RecoveryLeft})
}

func (s *panel) mfaSetup(c fiber.Ctx) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	setup, err := s.mfa.Setup(c.Context(), currentUser(c), currentToken(c), in.Password)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"key": setup.Key, "uri": setup.URI})
}

func (s *panel) mfaEnable(c fiber.Ctx) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	codes, err := s.mfa.Enable(c.Context(), currentUser(c), in.Code)
	if err != nil {
		return err
	}
	s.notifySecurity(currentUser(c), "Two-step sign-in was turned on for this account.")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"recovery_codes": codes})
}

func (s *panel) mfaDisable(c fiber.Ctx) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.mfa.Disable(c.Context(), currentUser(c), in.Code); err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return domain.Invalid("two-step sign-in is not on")
		}
		return err
	}
	s.notifySecurity(currentUser(c), "Two-step sign-in was turned off for this account.")
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) mfaRecoveryCodes(c fiber.Ctx) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	codes, err := s.mfa.RegenerateCodes(c.Context(), currentUser(c), in.Code)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"recovery_codes": codes})
}
