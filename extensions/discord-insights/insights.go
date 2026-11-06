// Package main is the Discord bot insights extension. Its logic is plain Go
// (testable on the host with the SDK's fake host); main_wasip1.go only
// registers the handler.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

// Snapshot is what the extension remembers about one bot.
type Snapshot struct {
	FetchedAtMS int64  `json:"fetched_at_ms"`
	State       string `json:"state"` // ok | invalid | missing | error | limited
	Message     string `json:"message,omitempty"`
	TokenVar    string `json:"token_var,omitempty"`
	TokenFP     string `json:"token_fp,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	AppID       string `json:"app_id,omitempty"`
	AppName     string `json:"app_name,omitempty"`
	Guilds      int    `json:"guilds"`
	Installs    int    `json:"installs"`
	Public      bool   `json:"public"`
}

type discordReply struct {
	Status     int             `json:"status"`
	Body       json.RawMessage `json:"body"`
	Token      string          `json:"token"`
	TokenVar   string          `json:"token_var"`
	TokenFP    string          `json:"token_fp"`
	RetryAfter string          `json:"retry_after"`
}

const staleAfter = 30 * time.Minute

var now = func() time.Time { return time.Now() }

func handle(r *sdk.Request) *sdk.Response {
	switch {
	case r.Kind == "route" && r.Route == "card":
		snap, ok := load(r.BotID)
		if !ok || now().Sub(time.UnixMilli(snap.FetchedAtMS)) > staleAfter {
			snap = refresh(r.BotID)
		}
		return sdk.JSON(200, card(snap))
	case r.Kind == "route" && r.Route == "refresh":
		return sdk.JSON(200, card(refresh(r.BotID)))
	case r.Kind == "job":
		var bots []sdk.Bot
		if err := sdk.Call("bots.list", map[string]string{}, &bots); err != nil {
			return sdk.Error(500, err.Error())
		}
		failed := 0
		for _, b := range bots {
			if s := refresh(b.ID); s.State == "error" {
				failed++
			}
		}
		return sdk.JSON(200, map[string]int{"bots": len(bots), "errors": failed})
	case r.Kind == "uninstall":
		return nil
	}
	return sdk.Error(404, "unknown request")
}

func key(botID string) string { return "bot:" + botID }

func load(botID string) (Snapshot, bool) {
	raw, ok, err := sdk.KVGet(key(botID), "snapshot")
	if err != nil || !ok {
		return Snapshot{}, false
	}
	var s Snapshot
	return s, json.Unmarshal(raw, &s) == nil
}

func save(botID string, s Snapshot) {
	b, _ := json.Marshal(s)
	_ = sdk.KVSet(key(botID), "snapshot", b, 0)
}

func discord(botID, path string) (discordReply, error) {
	var r discordReply
	err := sdk.Call("bot.discord_api", map[string]string{"bot_id": botID, "path": path}, &r)
	return r, err
}

// refresh asks Discord about the bot and records the result.
func refresh(botID string) Snapshot {
	prev, _ := load(botID)
	s := Snapshot{FetchedAtMS: now().UnixMilli()}
	me, err := discord(botID, "users/@me")
	switch {
	case err != nil:
		s.State, s.Message = "error", err.Error()
	case me.Token == "missing":
		s.State, s.Message = "missing", "No Discord bot token was found in this bot's variables (for example DISCORD_TOKEN)."
	case me.Status == 401:
		s.State, s.Message = "invalid", "Discord rejected the token in "+me.TokenVar+". It was probably reset (Discord resets tokens it finds published); generate a new one in the Developer Portal."
	case me.Status == 429:
		s = prev
		s.State, s.Message = "limited", "Discord asked to slow down; showing the last result."
		if s.FetchedAtMS == 0 {
			s.FetchedAtMS = now().UnixMilli()
		}
	case me.Status != 200:
		s.State, s.Message = "error", fmt.Sprintf("Discord answered with status %d.", me.Status)
	default:
		var u struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Avatar   string `json:"avatar"`
		}
		_ = json.Unmarshal(me.Body, &u)
		s.State, s.UserID, s.Username, s.Avatar = "ok", u.ID, u.Username, u.Avatar
		if app, err := discord(botID, "applications/@me"); err == nil && app.Status == 200 {
			var a struct {
				ID                      string `json:"id"`
				Name                    string `json:"name"`
				BotPublic               bool   `json:"bot_public"`
				ApproximateGuildCount   int    `json:"approximate_guild_count"`
				ApproximateUserInstalls int    `json:"approximate_user_install_count"`
			}
			_ = json.Unmarshal(app.Body, &a)
			s.AppID, s.AppName, s.Public, s.Guilds, s.Installs = a.ID, a.Name, a.BotPublic, a.ApproximateGuildCount, a.ApproximateUserInstalls
		}
		_ = sdk.Call("bot.identity", map[string]string{"bot_id": botID, "discord_user_id": u.ID, "username": u.Username, "avatar_url": avatarURL(u.ID, u.Avatar)}, nil)
	}
	s.TokenVar, s.TokenFP = me.TokenVar, me.TokenFP
	if s.State == "invalid" {
		notifyOnce(botID, s)
	}
	save(botID, s)
	return s
}

// notifyOnce warns the owner once per token: a new token that fails again
// is worth a new warning, the same failing token every half hour is not.
func notifyOnce(botID string, s Snapshot) {
	last, ok, _ := sdk.KVGet(key(botID), "warned_fp")
	if ok && string(last) == s.TokenFP {
		return
	}
	if sdk.Notify(botID, "Discord rejected a bot token", s.Message, "/bots/"+botID) == nil {
		_ = sdk.KVSet(key(botID), "warned_fp", []byte(s.TokenFP), 0)
	}
}

func avatarURL(id, hash string) string {
	if id == "" || hash == "" {
		return ""
	}
	return "https://cdn.discordapp.com/avatars/" + url.PathEscape(id) + "/" + url.PathEscape(hash) + ".png?size=128"
}

// Widget is one entry in the docs/widgets.md format.
type Widget struct {
	Key   string         `json:"key"`
	Kind  string         `json:"kind"`
	Title string         `json:"title"`
	Data  map[string]any `json:"data"`
}

func card(s Snapshot) map[string]any {
	tone, text := "neutral", "Not checked yet"
	switch s.State {
	case "ok":
		tone, text = "good", "Token accepted"
	case "invalid":
		tone, text = "bad", "Token rejected"
	case "missing":
		tone, text = "warn", "No token found"
	case "limited":
		tone, text = "warn", "Rate limited"
	case "error":
		tone, text = "warn", "Discord unreachable"
	}
	ws := []Widget{{Key: "token", Kind: "status", Title: "Token", Data: map[string]any{"state": tone, "text": text}}}
	if s.Message != "" {
		ws = append(ws, Widget{Key: "message", Kind: "text", Title: "Details", Data: map[string]any{"text": s.Message}})
	}
	if s.UserID != "" {
		items := []map[string]string{{"key": "Bot", "value": s.Username}, {"key": "User ID", "value": s.UserID}}
		if s.AppName != "" {
			items = append(items, map[string]string{"key": "Application", "value": s.AppName})
		}
		vis := "Private (only the owner can add it)"
		if s.Public {
			vis = "Public"
		}
		if s.AppID != "" {
			items = append(items, map[string]string{"key": "Invite", "value": vis})
		}
		ws = append(ws, Widget{Key: "profile", Kind: "kv", Title: "Profile", Data: map[string]any{"items": items}})
		if s.AppID != "" {
			ws = append(ws, Widget{Key: "guilds", Kind: "metric", Title: "Servers", Data: map[string]any{"value": s.Guilds, "unit": "servers",
				"detail": "approximate, as reported by Discord"}})
			if s.Installs > 0 {
				ws = append(ws, Widget{Key: "installs", Kind: "metric", Title: "User installs", Data: map[string]any{"value": s.Installs, "unit": "users"}})
			}
			ws = append(ws, Widget{Key: "invite", Kind: "link", Title: "Invite", Data: map[string]any{"label": "Invite link",
				"url": "https://discord.com/oauth2/authorize?client_id=" + url.QueryEscape(s.AppID) + "&scope=bot+applications.commands&permissions=0"}})
		}
		if a := avatarURL(s.UserID, s.Avatar); a != "" {
			ws = append([]Widget{{Key: "avatar", Kind: "image", Title: s.Username, Data: map[string]any{"url": a, "alt": s.Username + " avatar"}}}, ws...)
		}
	}
	return map[string]any{"widgets": ws, "fetched_at_ms": s.FetchedAtMS, "state": s.State}
}
