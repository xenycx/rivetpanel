// Package main is the Discord ChatOps extension: it answers Discord's signed
// interaction requests (the panel checks the signature before this code
// runs) and acts for the panel account linked to the Discord user.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

// Discord interaction and response types.
const (
	tPing         = 1
	tCommand      = 2
	tComponent    = 3
	tAutocomplete = 4

	rPong         = 1
	rMessage      = 4
	rUpdate       = 7
	rAutocomplete = 8

	ephemeral = 64
)

type option struct {
	Name    string          `json:"name"`
	Value   json.RawMessage `json:"value"`
	Focused bool            `json:"focused"`
}

type interaction struct {
	ID     string `json:"id"`
	Type   int    `json:"type"`
	Member *struct {
		User discordUser `json:"user"`
	} `json:"member"`
	User *discordUser `json:"user"`
	Data struct {
		Name     string   `json:"name"`
		Options  []option `json:"options"`
		CustomID string   `json:"custom_id"`
	} `json:"data"`
}

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func (i *interaction) userID() string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func (i *interaction) opt(name string) (string, bool) {
	for _, o := range i.Data.Options {
		if o.Name == name {
			var s string
			if json.Unmarshal(o.Value, &s) == nil {
				return s, o.Focused
			}
			var n json.Number
			if json.Unmarshal(o.Value, &n) == nil {
				return n.String(), o.Focused
			}
		}
	}
	return "", false
}

// identity is the panel account a Discord user maps to.
type identity struct {
	Linked    bool     `json:"linked"`
	Consented bool     `json:"consented"`
	As        string   `json:"as"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
}

func (id identity) allows(scope string) bool {
	for _, s := range id.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

var now = time.Now

func handle(r *sdk.Request) *sdk.Response {
	switch {
	case r.Kind == "route" && r.Route == "interactions":
		var in interaction
		if err := json.Unmarshal(r.Body, &in); err != nil {
			return sdk.Error(400, "malformed interaction")
		}
		return sdk.JSON(200, interact(&in))
	case r.Kind == "route" && r.Route == "register":
		return register()
	case r.Kind == "route" && r.Route == "setup":
		return setup()
	case r.Kind == "uninstall":
		return nil
	}
	return sdk.Error(404, "unknown request")
}

func msg(text string) map[string]any {
	return map[string]any{"type": rMessage, "data": map[string]any{"content": clip(text, 1900), "flags": ephemeral, "allowed_mentions": map[string]any{"parse": []string{}}}}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func interact(in *interaction) map[string]any {
	switch in.Type {
	case tPing:
		return map[string]any{"type": rPong}
	case tAutocomplete:
		return autocomplete(in)
	case tCommand, tComponent:
	default:
		return msg("This kind of interaction is not supported.")
	}
	// Discord may deliver an interaction twice; act once.
	if _, seen, _ := sdk.KVGet("seen", in.ID); seen && in.ID != "" {
		return msg("This request was already handled.")
	}
	_ = sdk.KVSet("seen", in.ID, []byte{1}, 15*time.Minute)

	var id identity
	if err := sdk.Call("identity.discord", map[string]string{"discord_user_id": in.userID()}, &id); err != nil {
		return msg("The panel could not check who you are: " + err.Error())
	}
	switch {
	case !id.Linked:
		return msg("Your Discord account is not linked to a panel account. Sign in to the panel, link Discord under **Settings → Connected accounts**, then allow ChatOps under **Settings → Extensions**.")
	case !id.Consented:
		return msg("Allow Discord ChatOps to act for you under **Settings → Extensions** in the panel first.")
	}
	if in.Type == tComponent {
		return button(in, id)
	}
	switch in.Data.Name {
	case "status":
		return status(in, id)
	case "logs":
		return logs(in, id)
	case "start", "stop", "restart", "deploy":
		return ask(in, id, in.Data.Name)
	}
	return msg("Unknown command.")
}

type bot struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DesiredState  string `json:"desired_state"`
	ObservedState string `json:"observed_state"`
	LastError     string `json:"last_error"`
}

func listBots(as string) ([]bot, error) {
	var bots []bot
	err := sdk.Call("bots.list", map[string]string{"as": as}, &bots)
	sort.Slice(bots, func(i, j int) bool { return strings.ToLower(bots[i].Name) < strings.ToLower(bots[j].Name) })
	return bots, err
}

// findBot resolves an option value: a bot id (from autocomplete) or a name.
func findBot(as, value string) (bot, string) {
	if strings.TrimSpace(value) == "" {
		return bot{}, "Choose a bot."
	}
	bots, err := listBots(as)
	if err != nil {
		return bot{}, "The panel refused: " + err.Error()
	}
	var byName []bot
	for _, b := range bots {
		if b.ID == value {
			return b, ""
		}
		if strings.EqualFold(b.Name, value) {
			byName = append(byName, b)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], ""
	case 0:
		return bot{}, "No bot you can see is called " + value + "."
	}
	return bot{}, "Several bots are called " + value + "; pick one from the list."
}

func stateIcon(b bot) string {
	switch b.ObservedState {
	case "running":
		return "🟢"
	case "crashed", "failed":
		return "🔴"
	case "starting", "stopping", "building", "deploying":
		return "🟡"
	}
	return "⚪"
}

func status(in *interaction, id identity) map[string]any {
	if !id.allows("status") {
		return msg("You did not allow ChatOps to see your bots.")
	}
	if v, _ := in.opt("bot"); v != "" {
		b, problem := findBot(id.As, v)
		if problem != "" {
			return msg(problem)
		}
		text := fmt.Sprintf("%s **%s** is %s (wanted: %s)", stateIcon(b), b.Name, b.ObservedState, b.DesiredState)
		if b.LastError != "" {
			text += "\nLast error: " + b.LastError
		}
		return msg(text)
	}
	bots, err := listBots(id.As)
	if err != nil {
		return msg("The panel refused: " + err.Error())
	}
	if len(bots) == 0 {
		return msg("You have no bots.")
	}
	var sb strings.Builder
	for i, b := range bots {
		if i == 25 {
			fmt.Fprintf(&sb, "…and %d more", len(bots)-25)
			break
		}
		fmt.Fprintf(&sb, "%s **%s** · %s\n", stateIcon(b), b.Name, b.ObservedState)
	}
	return msg(sb.String())
}

func logs(in *interaction, id identity) map[string]any {
	if !id.allows("logs") {
		return msg("You did not allow ChatOps to read output.")
	}
	v, _ := in.opt("bot")
	b, problem := findBot(id.As, v)
	if problem != "" {
		return msg(problem)
	}
	lines := 30
	if n, _ := in.opt("lines"); n != "" {
		fmt.Sscan(n, &lines)
	}
	var out struct {
		Text      string `json:"text"`
		Available bool   `json:"available"`
	}
	if err := sdk.Call("bot.logs", map[string]any{"bot_id": b.ID, "lines": lines, "as": id.As}, &out); err != nil {
		return msg("The panel refused: " + err.Error())
	}
	if !out.Available || strings.TrimSpace(out.Text) == "" {
		return msg("**" + b.Name + "** has no console output right now.")
	}
	text := strings.ReplaceAll(out.Text, "```", "'''")
	if len(text) > 1800 {
		text = "…" + text[len(text)-1800:]
	}
	return msg("**" + b.Name + "**, last lines:\n```\n" + text + "\n```")
}

var scopeFor = map[string]string{"start": "power", "stop": "power", "restart": "power", "deploy": "deploy"}

type pending struct {
	User   string `json:"user"`
	Action string `json:"action"`
	BotID  string `json:"bot_id"`
	Name   string `json:"name"`
}

// ask replies with confirmation buttons; nothing changes until one is pressed.
func ask(in *interaction, id identity, action string) map[string]any {
	if !id.allows(scopeFor[action]) {
		return msg("You did not allow ChatOps to " + action + " bots.")
	}
	v, _ := in.opt("bot")
	b, problem := findBot(id.As, v)
	if problem != "" {
		return msg(problem)
	}
	var n [12]byte
	_, _ = rand.Read(n[:])
	nonce := hex.EncodeToString(n[:])
	p, _ := json.Marshal(pending{User: in.userID(), Action: action, BotID: b.ID, Name: b.Name})
	if err := sdk.KVSet("confirm", nonce, p, time.Minute); err != nil {
		return msg("Could not prepare the confirmation: " + err.Error())
	}
	style := 1 // primary
	if action == "stop" {
		style = 4 // danger
	}
	return map[string]any{"type": rMessage, "data": map[string]any{
		"content": fmt.Sprintf("%s **%s**? This acts as %s in the panel. The buttons expire in a minute.", strings.ToUpper(action[:1])+action[1:], b.Name, id.Name),
		"flags":   ephemeral, "allowed_mentions": map[string]any{"parse": []string{}},
		"components": []any{map[string]any{"type": 1, "components": []any{
			map[string]any{"type": 2, "style": style, "label": strings.ToUpper(action[:1]) + action[1:], "custom_id": "ok:" + nonce},
			map[string]any{"type": 2, "style": 2, "label": "Cancel", "custom_id": "no:" + nonce},
		}}},
	}}
}

func update(text string) map[string]any {
	return map[string]any{"type": rUpdate, "data": map[string]any{"content": clip(text, 1900), "components": []any{}, "allowed_mentions": map[string]any{"parse": []string{}}}}
}

// button handles a confirmation press: same Discord user, within a minute,
// once.
func button(in *interaction, id identity) map[string]any {
	verb, nonce, ok := strings.Cut(in.Data.CustomID, ":")
	if !ok || len(nonce) != 24 {
		return update("Unknown button.")
	}
	raw, found, err := sdk.KVGet("confirm", nonce)
	if err != nil || !found {
		return update("This confirmation expired. Run the command again.")
	}
	var p pending
	if json.Unmarshal(raw, &p) != nil || p.User != in.userID() {
		return update("Only the person who ran the command can confirm it.")
	}
	_ = sdk.KVDelete("confirm", nonce)
	if verb != "ok" {
		return update("Cancelled.")
	}
	if !id.allows(scopeFor[p.Action]) {
		return update("You no longer allow ChatOps to " + p.Action + " bots.")
	}
	if p.Action == "deploy" {
		if err := sdk.Call("bot.deploy", map[string]string{"bot_id": p.BotID, "as": id.As}, nil); err != nil {
			return update("Deploy refused: " + err.Error())
		}
		return update("Deployment of **" + p.Name + "** started. Follow it in the panel.")
	}
	var b bot
	if err := sdk.Call("bot.power", map[string]string{"bot_id": p.BotID, "action": p.Action, "as": id.As}, &b); err != nil {
		return update(strings.ToUpper(p.Action[:1]) + p.Action[1:] + " refused: " + err.Error())
	}
	return update(fmt.Sprintf("Done: **%s** %s (now %s).", p.Name, map[string]string{"start": "is starting", "stop": "is stopping", "restart": "is restarting"}[p.Action], b.ObservedState))
}

func autocomplete(in *interaction) map[string]any {
	empty := map[string]any{"type": rAutocomplete, "data": map[string]any{"choices": []any{}}}
	var id identity
	if err := sdk.Call("identity.discord", map[string]string{"discord_user_id": in.userID()}, &id); err != nil || !id.Consented || !id.allows("status") {
		return empty
	}
	typed, _ := in.opt("bot")
	bots, err := listBots(id.As)
	if err != nil {
		return empty
	}
	choices := []any{}
	for _, b := range bots {
		if typed != "" && !strings.Contains(strings.ToLower(b.Name), strings.ToLower(typed)) {
			continue
		}
		choices = append(choices, map[string]string{"name": clip(b.Name+" · "+b.ObservedState, 100), "value": b.ID})
		if len(choices) == 25 {
			break
		}
	}
	return map[string]any{"type": rAutocomplete, "data": map[string]any{"choices": choices}}
}

// commands is what /register sends to Discord.
func commands() []any {
	botOpt := func(required bool) map[string]any {
		return map[string]any{"type": 3, "name": "bot", "description": "The bot", "required": required, "autocomplete": true}
	}
	return []any{
		map[string]any{"name": "status", "description": "Show your bots, or one bot", "options": []any{botOpt(false)}},
		map[string]any{"name": "logs", "description": "Show a bot's recent console output", "options": []any{botOpt(true),
			map[string]any{"type": 4, "name": "lines", "description": "How many lines (up to 200)", "min_value": 5, "max_value": 200}}},
		map[string]any{"name": "start", "description": "Start a bot", "options": []any{botOpt(true)}},
		map[string]any{"name": "stop", "description": "Stop a bot", "options": []any{botOpt(true)}},
		map[string]any{"name": "restart", "description": "Restart a bot", "options": []any{botOpt(true)}},
		map[string]any{"name": "deploy", "description": "Redeploy a bot from its linked repository", "options": []any{botOpt(true)}},
	}
}

type fetchReply struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// register publishes the slash commands with a client-credentials token.
func register() *sdk.Response {
	set, err := sdk.Settings()
	if err != nil {
		return sdk.Error(500, err.Error())
	}
	app, _ := set["application_id"].(string)
	if app == "" || set["client_secret_set"] != true {
		return sdk.Error(400, "Set the application ID and client secret first.")
	}
	var tok fetchReply
	if err := sdk.Call("http.fetch", map[string]any{"method": "POST", "url": "https://discord.com/api/v10/oauth2/token",
		"form": map[string]string{"grant_type": "client_credentials", "scope": "applications.commands.update"}, "basic_user_setting": "client_secret"}, &tok); err != nil {
		return sdk.Error(502, err.Error())
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if tok.Status != 200 || json.Unmarshal(decodeBody(tok.Body), &token) != nil || token.AccessToken == "" {
		return sdk.Error(400, fmt.Sprintf("Discord refused the application ID or client secret (status %d).", tok.Status))
	}
	url := "https://discord.com/api/v10/applications/" + app + "/commands"
	if g, _ := set["guild_id"].(string); g != "" {
		url = "https://discord.com/api/v10/applications/" + app + "/guilds/" + g + "/commands"
	}
	body, _ := json.Marshal(commands())
	var put fetchReply
	if err := sdk.Call("http.fetch", map[string]any{"method": "PUT", "url": url, "body": body,
		"headers": map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token.AccessToken}}, &put); err != nil {
		return sdk.Error(502, err.Error())
	}
	if put.Status != 200 {
		return sdk.Error(400, fmt.Sprintf("Discord refused the commands (status %d): %s", put.Status, clip(string(decodeBody(put.Body)), 300)))
	}
	_ = sdk.KVSet("", "registered_at", []byte(now().UTC().Format(time.RFC3339)), 0)
	return sdk.JSON(200, map[string]any{"registered": len(commands()), "scope": map[bool]string{true: "server", false: "global"}[strings.Contains(url, "/guilds/")]})
}

// decodeBody turns the host's base64 []byte JSON back into bytes.
func decodeBody(raw json.RawMessage) []byte {
	var b []byte
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return raw
}

func setup() *sdk.Response {
	set, _ := sdk.Settings()
	at, _, _ := sdk.KVGet("", "registered_at")
	return sdk.JSON(200, map[string]any{"configured": set["application_id"] != nil && set["public_key"] != nil, "secret_set": set["client_secret_set"] == true,
		"registered_at": string(at), "commands": len(commands())})
}
