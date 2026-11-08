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

// ---- workspace logos ----

func (s *panel) getWorkspaceLogo(c fiber.Ctx) error {
	l, err := s.bots.WorkspaceLogo(c.Context(), currentUser(c), strings.Clone(c.Params("wid")))
	if err != nil {
		return err
	}
	return sendImage(c, l.Data, l.ContentType)
}

func (s *panel) putWorkspaceLogo(c fiber.Ctx) error {
	var in logoIn
	if err := decode(c, &in); err != nil {
		return err
	}
	data, err := in.bytes()
	if err != nil {
		return err
	}
	return s.workspaceLogo(c, data)
}

func (s *panel) deleteWorkspaceLogo(c fiber.Ctx) error { return s.workspaceLogo(c, nil) }

func (s *panel) workspaceLogo(c fiber.Ctx, data []byte) error {
	if err := s.bots.SetWorkspaceLogo(c.Context(), currentUser(c), strings.Clone(c.Params("wid")), data); err != nil {
		return err
	}
	return s.getWorkspace(c)
}

// ---- generated pictures ----
//
// Each answers {"logo_variant": n}: the browser seeds the generated picture
// with "<id>:<n>" (just the id while n is 0).

func sendVariant(c fiber.Ctx, v int64, err error) error {
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"logo_variant": v})
}

func (s *panel) shuffleBotLogo(c fiber.Ctx) error {
	v, err := s.bots.ShuffleLogo(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	return sendVariant(c, v, err)
}

func (s *panel) shuffleSiteLogo(c fiber.Ctx) error {
	v, err := s.sites.ShuffleLogo(c.Context(), currentUser(c), strings.Clone(c.Params("sid")))
	return sendVariant(c, v, err)
}

func (s *panel) shuffleWorkspaceLogo(c fiber.Ctx) error {
	v, err := s.bots.ShuffleWorkspaceLogo(c.Context(), currentUser(c), strings.Clone(c.Params("wid")))
	return sendVariant(c, v, err)
}

func (s *panel) shuffleMyAvatar(c fiber.Ctx) error {
	u := currentUser(c)
	v, err := s.auth.ShuffleAvatar(c.Context(), u, u.ID)
	return sendVariant(c, v, err)
}

func (s *panel) shuffleUserAvatar(c fiber.Ctx) error {
	v, err := s.auth.ShuffleAvatar(c.Context(), currentUser(c), strings.Clone(c.Params("id")))
	return sendVariant(c, v, err)
}
