package api

import (
	"encoding/base64"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// logoIn carries a base64 PNG or JPEG (resized by the browser).
type logoIn struct {
	Image string `json:"image"`
}

func (in logoIn) bytes() ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.Image))
	if err != nil || len(b) == 0 {
		return nil, domain.Invalid("the logo must be a base64-encoded PNG or JPEG image")
	}
	return b, nil
}

// sendImage serves stored image bytes. SVG favicons are served inert (no
// scripts, sandboxed) since an address opened directly renders as a document.
func sendImage(c fiber.Ctx, data []byte, contentType string) error {
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderCacheControl, "private, max-age=3600")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	return c.Send(data)
}

func (s *panel) getBotLogo(c fiber.Ctx) error {
	l, err := s.bots.Logo(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return sendImage(c, l.Data, l.ContentType)
}

func (s *panel) putBotLogo(c fiber.Ctx) error {
	var in logoIn
	if err := decode(c, &in); err != nil {
		return err
	}
	data, err := in.bytes()
	if err != nil {
		return err
	}
	id := strings.Clone(c.Params("id"))
	if err := s.bots.SetLogo(c.Context(), currentUser(c), id, data); err != nil {
		return err
	}
	return s.sendBot(c, id)
}

func (s *panel) deleteBotLogo(c fiber.Ctx) error {
	id := strings.Clone(c.Params("id"))
	if err := s.bots.SetLogo(c.Context(), currentUser(c), id, nil); err != nil {
		return err
	}
	return s.sendBot(c, id)
}

// discordBotLogo fetches the bot user's avatar from Discord with the bot's token.
func (s *panel) discordBotLogo(c fiber.Ctx) error {
	b, err := s.bots.FetchDiscordIdentity(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) sendBot(c fiber.Ctx, id string) error {
	b, err := s.bots.Get(c.Context(), currentUser(c), id)
	if err != nil {
		return err
	}
	return c.JSON(s.viewBot(c, b))
}

func (s *panel) getSiteIcon(c fiber.Ctx) error {
	icon, err := s.sites.Icon(c.Context(), currentUser(c), strings.Clone(c.Params("sid")))
	if err != nil {
		return err
	}
	if icon.BotAvatar != "" {
		return c.Redirect().Status(fiber.StatusFound).To(icon.BotAvatar)
	}
	return sendImage(c, icon.Data, icon.ContentType)
}

func (s *panel) putSiteLogo(c fiber.Ctx) error {
	var in logoIn
	if err := decode(c, &in); err != nil {
		return err
	}
	data, err := in.bytes()
	if err != nil {
		return err
	}
	return s.siteLogo(c, data)
}

func (s *panel) deleteSiteLogo(c fiber.Ctx) error { return s.siteLogo(c, nil) }

func (s *panel) siteLogo(c fiber.Ctx, data []byte) error {
	id := strings.Clone(c.Params("sid"))
	if err := s.sites.SetLogo(c.Context(), currentUser(c), id, data); err != nil {
		return err
	}
	d, err := s.sites.Get(c.Context(), currentUser(c), id)
	if err != nil {
		return err
	}
	return c.JSON(s.toSite(d.Site))
}
