package api

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

type settingsBody struct {
	PublicURL     *string `json:"public_url"`
	GitHubID      *string `json:"github_client_id"`
	GitHubSecret  *string `json:"github_client_secret"`
	DiscordID     *string `json:"discord_client_id"`
	DiscordSecret *string `json:"discord_client_secret"`
	AllowSignup   *bool   `json:"oauth_allow_signup"`
	MailKey       *string `json:"mailgun_api_key"`
	MailDomain    *string `json:"mailgun_domain"`
	MailRegion    *string `json:"mailgun_region"`
	MailFrom      *string `json:"mail_from"`
	// UnverifiedRestrict lists the permissions withheld from accounts whose
	// email address is not verified; an empty list turns the policy off.
	UnverifiedRestrict *[]string `json:"unverified_restrict"`
}

func (b settingsBody) input() service.SettingsInput {
	return service.SettingsInput{PublicURL: b.PublicURL, GitHubID: b.GitHubID, GitHubSecret: b.GitHubSecret,
		DiscordID: b.DiscordID, DiscordSecret: b.DiscordSecret, AllowSignup: b.AllowSignup,
		MailKey: b.MailKey, MailDomain: b.MailDomain, MailRegion: b.MailRegion, MailFrom: b.MailFrom,
		UnverifiedRestrict: b.UnverifiedRestrict}
}

func settingsJSON(v service.SettingsView) fiber.Map {
	return fiber.Map{"public_url": v.PublicURL, "github_client_id": v.GitHubID, "github_secret_set": v.GitHubSecretSet,
		"discord_client_id": v.DiscordID, "discord_secret_set": v.DiscordSecSet, "oauth_allow_signup": v.AllowSignup,
		"locked": v.Locked, "github_enabled": v.GitHubEnabled, "discord_enabled": v.DiscordEnabled,
		"mailgun_key_set": v.MailKeySet, "mailgun_domain": v.MailDomain, "mailgun_region": v.MailRegion, "mail_from": v.MailFrom, "mail_enabled": v.MailEnabled,
		"unverified_restrict": v.UnverifiedRestrict}
}

// setupStatus is public: the interface uses it to send a fresh installation
// to the setup wizard.
func (s *panel) setupStatus(c fiber.Ctx) error {
	need, err := s.settings.SetupNeeded(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"needed": need, "code_file": s.setupCodeFile})
}

func (s *panel) setupCheck(c fiber.Ctx) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.settings.CheckSetupCode(c.Context(), in.Code); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) setupComplete(c fiber.Ctx) error {
	var in struct {
		Code     string       `json:"code"`
		Email    string       `json:"email"`
		Password string       `json:"password"`
		Settings settingsBody `json:"settings"`
		// Optional first AI operator provider; its key is sealed like any
		// provider key and never returned.
		AIProvider *service.AIProviderInput `json:"ai_provider"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if in.AIProvider != nil {
		if s.ai == nil {
			return domain.Invalid("the AI operator is not available on this panel")
		}
		in.AIProvider.Enabled, in.AIProvider.Default = true, true
		if in.AIProvider.ChatPath == "" {
			in.AIProvider.ChatPath = "/chat/completions"
		}
		if in.AIProvider.ModelsPath == "" {
			in.AIProvider.ModelsPath = "/models"
		}
		if in.AIProvider.Temperature == 0 {
			in.AIProvider.Temperature = 0.2
		}
		if err := service.ValidateAIProvider(*in.AIProvider); err != nil {
			return err
		}
	}
	sess, err := s.settings.CompleteSetup(c.Context(), service.SetupInput{Code: in.Code, Email: in.Email, Password: in.Password,
		Settings: in.Settings.input()}, deviceLabel(c.Get(fiber.HeaderUserAgent)))
	if sess.Token != "" {
		s.setSessionCookie(c, sess.Token, time.UnixMilli(sess.ExpiresAtMS))
		if s.onSetupDone != nil {
			s.onSetupDone()
		}
	}
	if err != nil && sess.Token == "" {
		return err
	}
	out := fiber.Map{"user": toUser(sess.User), "csrf_token": sess.CSRF}
	if in.AIProvider != nil {
		if _, aerr := s.ai.PutProvider(c.Context(), sess.User, "", *in.AIProvider); aerr != nil {
			if err == nil {
				err = fmt.Errorf("AI provider not saved: %w", aerr)
			} else {
				err = fmt.Errorf("%w; AI provider not saved: %v", err, aerr)
			}
		}
	}
	if err != nil {
		out["settings_error"] = err.Error()
	}
	return c.JSON(out)
}

func (s *panel) getSettings(c fiber.Ctx) error {
	v, err := s.settings.View(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(settingsJSON(v))
}

func (s *panel) putSettings(c fiber.Ctx) error {
	var in settingsBody
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.settings.Update(c.Context(), currentUser(c), in.input()); err != nil {
		return err
	}
	return s.getSettings(c)
}
