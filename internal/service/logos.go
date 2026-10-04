package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // logo formats
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// MaxLogoBytes bounds a custom logo (the browser resizes it first).
const MaxLogoBytes = 256 << 10

// validateLogo accepts a PNG or JPEG between 16 and 512 pixels a side.
func validateLogo(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxLogoBytes {
		return "", domain.Invalid(fmt.Sprintf("the logo must be an image of at most %d KiB", MaxLogoBytes>>10))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", domain.Invalid("the logo must be a PNG or JPEG image")
	}
	if cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 512 || cfg.Height > 512 {
		return "", domain.Invalid("the logo must be between 16 and 512 pixels wide and high")
	}
	return "image/" + format, nil
}

// SetLogo stores a bot's custom logo; nil data removes it.
func (s *BotService) SetLogo(ctx context.Context, actor domain.User, botID string, data []byte) error {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false)
	if err != nil {
		return err
	}
	if data == nil {
		return s.Store.SetBotLogo(ctx, b.ID, nil, s.now())
	}
	ct, err := validateLogo(data)
	if err != nil {
		return err
	}
	return s.Store.SetBotLogo(ctx, b.ID, &domain.Logo{Data: data, ContentType: ct}, s.now())
}

// Logo returns a bot's custom logo (domain.ErrNotFound when none).
func (s *BotService) Logo(ctx context.Context, actor domain.User, botID string) (domain.Logo, error) {
	b, err := s.loadPerm(ctx, actor, botID, permAny, false)
	if err != nil {
		return domain.Logo{}, err
	}
	return s.Store.GetBotLogo(ctx, b.ID)
}

var avatarHashRe = regexp.MustCompile(`^(a_)?[0-9a-f]{32}$`)

var discordTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{18,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{20,}$`)

// tokenVars are checked first, in order; then any variable whose value looks
// like a bot token.
var tokenVars = []string{"DISCORD_TOKEN", "DISCORD_BOT_TOKEN", "BOT_TOKEN", "TOKEN", "RED_TOKEN", "YAGPDB_BOTTOKEN"}

// discordToken finds the bot token among a bot's variables.
func discordToken(env map[string]string) (name, token string) {
	clean := func(v string) string { return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "Bot ")) }
	for _, n := range tokenVars {
		if v := clean(env[n]); discordTokenRe.MatchString(v) {
			return n, v
		}
	}
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if v := clean(env[n]); !strings.HasPrefix(n, "RIVET_") && discordTokenRe.MatchString(v) {
			return n, v
		}
	}
	return "", ""
}

// FetchDiscordIdentity asks Discord for the bot user's name and avatar with
// the token stored in the bot's own variables, and records them like the
// telemetry SDK does. The token never leaves the panel except to Discord.
func (s *BotService) FetchDiscordIdentity(ctx context.Context, actor domain.User, botID string) (domain.Bot, error) {
	b, err := s.loadPerm(ctx, actor, botID, domain.PermFullAdmin, false)
	if err != nil {
		return domain.Bot{}, err
	}
	env, err := s.DecryptEnv(ctx, b.ID)
	if err != nil {
		return domain.Bot{}, err
	}
	name, tok := discordToken(env)
	if tok == "" {
		return domain.Bot{}, domain.Invalid("no Discord bot token was found in this bot's variables (for example DISCORD_TOKEN)")
	}
	api := s.DiscordAPI
	if api == "" {
		api = "https://discord.com/api/v10"
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, http.MethodGet, strings.TrimRight(api, "/")+"/users/@me", nil)
	req.Header.Set("Authorization", "Bot "+tok)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/xenycx/rivetpanel, 1)")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return domain.Bot{}, domain.Invalid("Discord could not be reached; try again later")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return domain.Bot{}, domain.Invalid("Discord rejected the token in " + name)
	case res.StatusCode != http.StatusOK:
		return domain.Bot{}, domain.Invalid(fmt.Sprintf("Discord answered with status %d", res.StatusCode))
	}
	var u struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Avatar   string `json:"avatar"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&u); err != nil || !discordID.MatchString(u.ID) {
		return domain.Bot{}, domain.Invalid("Discord returned an unexpected answer")
	}
	avatar := "https://cdn.discordapp.com/avatars/" + u.ID + "/" + u.Avatar + ".png?size=256"
	if !avatarHashRe.MatchString(u.Avatar) {
		n, _ := strconv.ParseUint(u.ID, 10, 64)
		avatar = fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", (n>>22)%6)
	}
	if !validDiscordIdentity(u.ID, u.Username, avatar) {
		return domain.Bot{}, domain.Invalid("Discord returned an unexpected answer")
	}
	if err := s.Store.SetBotDiscordIdentity(ctx, b.ID, u.ID, strings.TrimSpace(u.Username), avatar, s.now()); err != nil {
		return domain.Bot{}, err
	}
	return s.Store.GetBot(ctx, b.ID)
}

// ---- sites ----

// SetLogo stores a site's custom logo; nil data removes it.
func (s *SiteService) SetLogo(ctx context.Context, actor domain.User, id string, data []byte) error {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceDeveloper)
	if err != nil {
		return err
	}
	if data == nil {
		return s.Store.SetSiteLogo(ctx, st.ID, nil, s.now().UnixMilli())
	}
	ct, err := validateLogo(data)
	if err != nil {
		return err
	}
	return s.Store.SetSiteLogo(ctx, st.ID, &domain.Logo{Data: data, ContentType: ct}, s.now().UnixMilli())
}

// SiteIcon is an image to show for a site.
type SiteIcon struct {
	Data        []byte
	ContentType string
	Source      string // custom | favicon | bot
	BotAvatar   string // set instead of Data when the bot's Discord avatar is the icon
}

var iconLinkRe = regexp.MustCompile(`(?is)<link\b[^>]*\brel\s*=\s*["']?(?:shortcut\s+)?(?:icon|apple-touch-icon)["']?[^>]*>`)
var hrefRe = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)

var iconTypes = map[string]string{".ico": "image/x-icon", ".png": "image/png", ".svg": "image/svg+xml", ".jpg": "image/jpeg",
	".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

// Icon returns a site's logo: the custom one, else the favicon of the
// active release (declared in index.html or at a conventional path), else
// the linked bot's logo. domain.ErrNotFound when there is none.
func (s *SiteService) Icon(ctx context.Context, actor domain.User, id string) (SiteIcon, error) {
	st, _, err := s.requireSite(ctx, actor, id, domain.WorkspaceViewer)
	if err != nil {
		return SiteIcon{}, err
	}
	if l, err := s.Store.GetSiteLogo(ctx, st.ID); err == nil {
		return SiteIcon{Data: l.Data, ContentType: l.ContentType, Source: "custom"}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return SiteIcon{}, err
	}
	if st.CurrentRelease != nil {
		if icon, ok := s.favicon(st.ID, *st.CurrentRelease); ok {
			return icon, nil
		}
	}
	if st.BotID != nil && s.Bots != nil {
		if l, err := s.Bots.Store.GetBotLogo(ctx, *st.BotID); err == nil {
			return SiteIcon{Data: l.Data, ContentType: l.ContentType, Source: "bot"}, nil
		}
		if b, err := s.Bots.Store.GetBot(ctx, *st.BotID); err == nil && b.DiscordAvatarURL != "" {
			return SiteIcon{Source: "bot", BotAvatar: b.DiscordAvatarURL}, nil
		}
	}
	return SiteIcon{}, domain.ErrNotFound
}

func (s *SiteService) favicon(siteID, releaseID string) (SiteIcon, bool) {
	root, err := s.ReleaseRoot(siteID, releaseID)
	if err != nil {
		return SiteIcon{}, false
	}
	defer root.Close()
	var cands []string
	if html, err := fs.ReadFile(root.FS(), "index.html"); err == nil {
		if len(html) > 512<<10 {
			html = html[:512<<10]
		}
		for _, tag := range iconLinkRe.FindAll(html, 8) {
			if m := hrefRe.FindSubmatch(tag); m != nil {
				h := string(m[1])
				if strings.Contains(h, "://") || strings.HasPrefix(h, "//") || strings.HasPrefix(h, "data:") {
					continue // only files of the site itself
				}
				if i := strings.IndexAny(h, "?#"); i >= 0 {
					h = h[:i]
				}
				cands = append(cands, strings.TrimPrefix(path.Clean("/"+h), "/"))
			}
		}
	}
	cands = append(cands, "favicon.ico", "favicon.png", "favicon.svg", "apple-touch-icon.png", "logo.png", "icon.png")
	for _, c := range cands {
		ct, ok := iconTypes[strings.ToLower(path.Ext(c))]
		if !ok || c == "" {
			continue
		}
		f, err := root.Open(c)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxLogoBytes+1))
		f.Close()
		if err == nil && len(data) > 0 && len(data) <= MaxLogoBytes {
			return SiteIcon{Data: data, ContentType: ct, Source: "favicon"}, true
		}
	}
	return SiteIcon{}, false
}
