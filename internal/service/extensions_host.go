package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// invocation is the authority one extension call runs with.
//
//   - user set: a route called by a signed-in person; bot capabilities act as
//     that person, through the same checks as the API.
//   - system: a job, an event or a public route; it sees only bots the
//     extension was switched on for, and can act for a person only through
//     identity.discord with that person's recorded consent.
type invocation struct {
	svc    *ExtensionService
	ext    *loadedExt
	user   *domain.User
	botID  string // the bot of a bot route
	system bool
	// handles are people resolved by identity.discord in this invocation.
	handles map[string]actingUser
}

type actingUser struct {
	user   domain.User
	scopes []string
}

type hostError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func reply(v any) []byte {
	b, err := json.Marshal(map[string]any{"ok": v})
	if err != nil {
		return replyErr("failed", "could not encode the reply")
	}
	return b
}

func replyErr(code, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"error": hostError{code, msg}})
	return b
}

func errReply(err error) []byte {
	var ve *domain.ValidationError
	var pe *domain.PermissionError
	switch {
	case errors.As(err, &ve):
		return replyErr("invalid", ve.Msg)
	case errors.As(err, &pe), errors.Is(err, domain.ErrForbidden):
		return replyErr("denied", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return replyErr("not_found", "not found")
	case errors.Is(err, domain.ErrQuota):
		return replyErr("limit", "the extension's storage is full")
	case errors.Is(err, domain.ErrNotStopped):
		return replyErr("invalid", err.Error())
	}
	var be *domain.BusyError
	if errors.As(err, &be) {
		return replyErr("unavailable", be.Error())
	}
	return replyErr("failed", clipService(err.Error(), 300))
}

// call is the HostFunc handed to the runtime.
func (inv *invocation) call(ctx context.Context, name string, req []byte) []byte {
	if name == "log" {
		var r struct{ Message string }
		_ = json.Unmarshal(req, &r)
		inv.svc.log().Info("extension log", "extension", inv.ext.man.ID, "message", clipService(r.Message, 500))
		return reply(nil)
	}
	capName := capabilityOf(name)
	if capName == "" {
		return replyErr("invalid", "unknown host call "+name)
	}
	if !slices.Contains(inv.ext.ext.Granted, capName) {
		return replyErr("denied", capName+" was not granted to this extension")
	}
	switch name {
	case "kv.get", "kv.set", "kv.delete":
		return inv.kv(ctx, name, req)
	case "settings.get":
		return inv.settings(ctx)
	case "http.fetch":
		return inv.fetch(ctx, req)
	case "bots.list", "bots.get":
		return inv.bots(ctx, name, req)
	case "bot.power":
		return inv.power(ctx, req)
	case "bot.logs":
		return inv.logs(ctx, req)
	case "bot.deploy":
		return inv.deploy(ctx, req)
	case "notify":
		return inv.notify(ctx, req)
	case "bot.discord_api":
		return inv.discordAPI(ctx, req)
	case "bot.identity":
		return inv.identity(ctx, req)
	case "identity.discord":
		return inv.discordUser(ctx, req)
	case "addons.list", "addons.query", "addons.confirm":
		return inv.addons(ctx, name, req)
	}
	return replyErr("invalid", "unknown host call "+name)
}

// capabilityOf maps a host call to the capability it needs.
func capabilityOf(call string) string {
	switch call {
	case "kv.get", "kv.set", "kv.delete":
		return bundle.CapKV
	case "settings.get":
		return bundle.CapSettings
	case "http.fetch":
		return bundle.CapHTTP
	case "bots.list", "bots.get":
		return bundle.CapBotsRead
	case "bot.power":
		return bundle.CapBotPower
	case "bot.logs":
		return bundle.CapBotLogs
	case "bot.deploy":
		return bundle.CapBotDeploy
	case "notify":
		return bundle.CapNotify
	case "bot.discord_api":
		return bundle.CapDiscordAPI
	case "bot.identity":
		return bundle.CapBotIdentity
	case "identity.discord":
		return bundle.CapDiscordUser
	case "addons.list", "addons.query", "addons.confirm":
		return bundle.CapAddonsQuery
	}
	return ""
}

var kvKeyRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,200}$`)

func (inv *invocation) kv(ctx context.Context, name string, raw []byte) []byte {
	var r struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
		Value []byte `json:"value"`
		TTLMS int64  `json:"ttl_ms"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || !kvKeyRe.MatchString(r.Key) || (r.Scope != "" && !kvKeyRe.MatchString(r.Scope)) {
		return replyErr("invalid", "kv keys and scopes are 1–200 characters of letters, digits and . _ : / -")
	}
	st, id, now := inv.svc.Store, inv.ext.man.ID, time.Now().UnixMilli()
	switch name {
	case "kv.get":
		v, err := st.GetExtensionKV(ctx, id, r.Scope, r.Key, now)
		if errors.Is(err, domain.ErrNotFound) {
			return reply(map[string]any{"found": false})
		}
		if err != nil {
			return errReply(err)
		}
		return reply(map[string]any{"found": true, "value": v})
	case "kv.set":
		if len(r.Value) > extKVValueMax {
			return replyErr("limit", "values are at most 64 KiB")
		}
		var exp int64
		if r.TTLMS > 0 {
			exp = now + r.TTLMS
		}
		return errOrOK(st.PutExtensionKV(ctx, id, r.Scope, r.Key, r.Value, exp, now, extKVQuota))
	default:
		return errOrOK(st.DeleteExtensionKV(ctx, id, r.Scope, r.Key))
	}
}

func errOrOK(err error) []byte {
	if err != nil {
		return errReply(err)
	}
	return reply(nil)
}

func (inv *invocation) settings(ctx context.Context) []byte {
	ss, err := inv.svc.Store.ListExtensionSettings(ctx, inv.ext.man.ID)
	if err != nil {
		return errReply(err)
	}
	out := map[string]any{}
	for _, def := range inv.ext.man.Settings {
		if def.Type == "secret" {
			continue // secrets are never readable; see http.fetch auth_setting
		}
		if def.Default != nil {
			out[def.Key] = def.Default
		}
	}
	for _, st := range ss {
		def, ok := inv.ext.man.Setting(st.Key)
		if !ok {
			continue
		}
		if def.Type == "secret" {
			out[st.Key+"_set"] = true
			continue
		}
		var v any
		if json.Unmarshal([]byte(st.ValueJSON), &v) == nil {
			out[st.Key] = v
		}
	}
	return reply(out)
}

// fetch performs an HTTPS request to a host the manifest lists.
func (inv *invocation) fetch(ctx context.Context, raw []byte) []byte {
	var r struct {
		Method      string            `json:"method"`
		URL         string            `json:"url"`
		Headers     map[string]string `json:"headers"`
		Body        []byte            `json:"body"`
		AuthSetting string            `json:"auth_setting"` // a secret setting sent as the Authorization header
		AuthPrefix  string            `json:"auth_prefix"`  // e.g. "Bot " or "Bearer "
		Form        map[string]string `json:"form"`         // application/x-www-form-urlencoded body
		BasicUser   string            `json:"basic_user_setting"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return replyErr("invalid", "malformed request")
	}
	c, _ := inv.ext.man.Cap(bundle.CapHTTP)
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !slices.Contains(c.Hosts, strings.ToLower(u.Hostname())) {
		return replyErr("denied", "only https:// URLs on the hosts this extension declared are allowed")
	}
	if !inv.svc.allow("http:"+inv.ext.man.ID, 120) {
		return replyErr("limit", "at most 120 requests per minute")
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return replyErr("invalid", "unsupported method")
	}
	body := r.Body
	ct := ""
	if r.Form != nil {
		form := url.Values{}
		for k, v := range r.Form {
			form.Set(k, v)
		}
		body, ct = []byte(form.Encode()), "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return replyErr("invalid", "bad request")
	}
	for k, v := range r.Headers {
		switch strings.ToLower(k) {
		case "host", "cookie", "proxy-authorization", "connection", "content-length", "transfer-encoding":
			continue
		}
		req.Header.Set(k, v)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("User-Agent", "RivetPanel-Extension ("+inv.ext.man.ID+")")
	for _, ref := range []struct {
		key, prefix string
		basic       bool
	}{{r.AuthSetting, r.AuthPrefix, false}, {r.BasicUser, "", true}} {
		if ref.key == "" {
			continue
		}
		def, ok := inv.ext.man.Setting(ref.key)
		if !ok || def.Type != "secret" || !slices.Contains(def.InjectHosts, strings.ToLower(u.Hostname())) {
			return replyErr("denied", "setting "+ref.key+" may not be sent to "+u.Hostname())
		}
		secret, err := inv.svc.settingText(ctx, inv.ext.man.ID, ref.key)
		if err != nil || secret == "" {
			return replyErr("invalid", "setting "+ref.key+" is not configured")
		}
		if ref.basic {
			// The basic-auth user is the extension's application id setting.
			// If a manifest declares it secret, it is held to the same host
			// list as any other secret.
			app := ""
			if d, ok := inv.ext.man.Setting("application_id"); ok {
				if d.Type == "secret" && !slices.Contains(d.InjectHosts, strings.ToLower(u.Hostname())) {
					return replyErr("denied", "setting application_id may not be sent to "+u.Hostname())
				}
				app, _ = inv.svc.settingText(ctx, inv.ext.man.ID, "application_id")
			}
			req.SetBasicAuth(app, secret)
		} else {
			req.Header.Set("Authorization", ref.prefix+secret)
		}
	}
	res, err := inv.svc.client(c.Hosts).Do(req)
	if err != nil {
		return replyErr("unavailable", "request failed: "+clipService(err.Error(), 200))
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return replyErr("unavailable", "reading the response failed")
	}
	if len(data) > 4<<20 {
		return replyErr("limit", "responses are limited to 4 MiB")
	}
	headers := map[string]string{}
	for _, h := range []string{"Content-Type", "Retry-After", "X-RateLimit-Remaining", "X-RateLimit-Reset-After", "Location"} {
		if v := res.Header.Get(h); v != "" {
			headers[strings.ToLower(h)] = v
		}
	}
	return reply(map[string]any{"status": res.StatusCode, "headers": headers, "body": data})
}

// client refuses private and local addresses at connect time and re-checks
// every redirect against the allowlist.
func (s *ExtensionService) client(hosts []string) *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !ai.PublicIP(net.ParseIP(host)) {
			return errors.New("private, local and metadata addresses are blocked")
		}
		return nil
	}}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4,
			IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 15 * time.Second, Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// A redirect is held to the same rules as the first request:
			// https, no credentials in the address, port 443, a listed host.
			if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Port() != "" && req.URL.Port() != "443") ||
				!slices.Contains(hosts, strings.ToLower(req.URL.Hostname())) {
				return http.ErrUseLastResponse
			}
			req.Header.Del("Authorization")
			return nil
		},
	}
}

// actor returns who a bot capability acts as: the route's user, or a
// Discord-mapped person (with consent for scope) named by "as".
func (inv *invocation) actor(ctx context.Context, as, scope string) (domain.User, error) {
	if as != "" {
		a, ok := inv.handles[as]
		if !ok {
			return domain.User{}, &domain.PermissionError{Perm: "an identity resolved in this request"}
		}
		if !slices.Contains(a.scopes, scope) {
			return domain.User{}, &domain.PermissionError{Perm: "consent to " + scope}
		}
		// Re-read the account: its role or state may have changed.
		u, err := inv.svc.Store.GetUserByID(ctx, a.user.ID)
		if err != nil || u.Disabled {
			return domain.User{}, &domain.PermissionError{Perm: "an active account"}
		}
		return u, nil
	}
	if inv.user == nil {
		return domain.User{}, &domain.PermissionError{Perm: "a signed-in person (this runs without one)"}
	}
	return *inv.user, nil
}

// visibleBot returns a bot the invocation may read: through the user's
// access, or (system) because the extension was switched on for it.
func (inv *invocation) visibleBot(ctx context.Context, botID, as string) (domain.Bot, error) {
	if as != "" || inv.user != nil {
		u, err := inv.actor(ctx, as, "status")
		if err != nil {
			return domain.Bot{}, err
		}
		return inv.svc.Bots.Get(ctx, u, botID)
	}
	if on, err := inv.svc.Store.ExtensionBotEnabled(ctx, inv.ext.man.ID, botID); err != nil || !on {
		return domain.Bot{}, domain.ErrNotFound
	}
	b, err := inv.svc.Store.GetBot(ctx, botID)
	if err != nil || b.DesiredState == domain.DesiredDeleted {
		return domain.Bot{}, domain.ErrNotFound
	}
	return b, nil
}

func extBotView(b domain.Bot) map[string]any {
	v := map[string]any{"id": b.ID, "name": b.Name, "kind": b.Kind, "desired_state": b.DesiredState, "observed_state": b.ObservedState,
		"runtime": b.Runtime, "node_id": b.NodeID, "discord_user_id": b.DiscordUserID, "discord_username": b.DiscordUsername, "discord_avatar_url": b.DiscordAvatarURL}
	if b.LastError != nil {
		v["last_error"] = clipService(*b.LastError, 300)
	}
	if b.ObservedAtMS != nil {
		v["observed_at_ms"] = *b.ObservedAtMS
	}
	return v
}

func (inv *invocation) bots(ctx context.Context, name string, raw []byte) []byte {
	var r struct {
		BotID string `json:"bot_id"`
		As    string `json:"as"`
	}
	_ = json.Unmarshal(raw, &r)
	if name == "bots.get" {
		b, err := inv.visibleBot(ctx, r.BotID, r.As)
		if err != nil {
			return errReply(err)
		}
		return reply(extBotView(b))
	}
	var list []domain.Bot
	if r.As != "" || inv.user != nil {
		u, err := inv.actor(ctx, r.As, "status")
		if err != nil {
			return errReply(err)
		}
		if list, err = inv.svc.Bots.List(ctx, u); err != nil {
			return errReply(err)
		}
	} else {
		ids, err := inv.svc.Store.ListExtensionBots(ctx, inv.ext.man.ID)
		if err != nil {
			return errReply(err)
		}
		for _, id := range ids {
			if b, err := inv.svc.Store.GetBot(ctx, id); err == nil {
				list = append(list, b)
			}
		}
	}
	out := make([]map[string]any, 0, len(list))
	for _, b := range list {
		if len(out) == 200 {
			break
		}
		out = append(out, extBotView(b))
	}
	return reply(out)
}

func (inv *invocation) power(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID  string `json:"bot_id"`
		Action string `json:"action"`
		As     string `json:"as"`
	}
	_ = json.Unmarshal(raw, &r)
	u, err := inv.actor(ctx, r.As, "power")
	if err != nil {
		return errReply(err)
	}
	if !u.Can(domain.PermBotsPower) {
		return errReply(&domain.PermissionError{Perm: domain.PermBotsPower})
	}
	var b domain.Bot
	bots := inv.svc.Bots
	switch r.Action {
	case "start":
		b, err = bots.Start(ctx, u, r.BotID)
	case "stop":
		b, err = bots.Stop(ctx, u, r.BotID)
	case "restart":
		b, err = bots.Restart(ctx, u, r.BotID)
	default:
		return replyErr("invalid", "action must be start, stop or restart")
	}
	inv.svc.recordVia(ctx, u, "bot."+r.Action, r.BotID, inv.ext.man.Name, err)
	if err != nil {
		return errReply(err)
	}
	return reply(extBotView(b))
}

func (inv *invocation) logs(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID string `json:"bot_id"`
		Lines int    `json:"lines"`
		As    string `json:"as"`
	}
	_ = json.Unmarshal(raw, &r)
	u, err := inv.actor(ctx, r.As, "logs")
	if err != nil {
		return errReply(err)
	}
	if !u.Can(domain.PermBotsConsole) {
		return errReply(&domain.PermissionError{Perm: domain.PermBotsConsole})
	}
	b, err := inv.svc.Bots.Authorize(ctx, u, r.BotID, domain.PermViewConsole)
	if err != nil {
		return errReply(err)
	}
	if inv.svc.Logs == nil || b.ContainerID == nil {
		return reply(map[string]any{"text": "", "available": false})
	}
	if r.Lines <= 0 || r.Lines > 200 {
		r.Lines = 50
	}
	text, err := inv.svc.Logs.Tail(ctx, *b.ContainerID, r.Lines)
	if err != nil {
		return replyErr("unavailable", "the bot's output could not be read")
	}
	return reply(map[string]any{"text": clipTail(text, 16000), "available": true})
}

func (inv *invocation) deploy(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID string `json:"bot_id"`
		As    string `json:"as"`
	}
	_ = json.Unmarshal(raw, &r)
	u, err := inv.actor(ctx, r.As, "deploy")
	if err != nil {
		return errReply(err)
	}
	if !u.Can(domain.PermBotsDeploy) {
		return errReply(&domain.PermissionError{Perm: domain.PermBotsDeploy})
	}
	if inv.svc.Deploy == nil {
		return replyErr("unavailable", "deployments are not available on this panel")
	}
	err = inv.svc.Deploy.DeployAs(ctx, u, r.BotID, "", "extension")
	inv.svc.recordVia(ctx, u, "deploy.start", r.BotID, inv.ext.man.Name, err)
	if err != nil {
		return errReply(err)
	}
	return reply(map[string]any{"started": true})
}

func (inv *invocation) notify(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID string `json:"bot_id"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Link  string `json:"link"`
	}
	_ = json.Unmarshal(raw, &r)
	if strings.TrimSpace(r.Title) == "" || len(r.Title) > 120 || len(r.Body) > 1000 {
		return replyErr("invalid", "a title (at most 120 characters) and a body (at most 1000) are required")
	}
	if r.Link != "" && (!strings.HasPrefix(r.Link, "/") || strings.HasPrefix(r.Link, "//") || len(r.Link) > 200) {
		return replyErr("invalid", "links must be panel paths such as /bots/…")
	}
	if !inv.svc.allow("notify:"+inv.ext.man.ID, 30) {
		return replyErr("limit", "at most 30 notifications per minute")
	}
	if inv.svc.Notices == nil {
		return reply(nil)
	}
	n := Notice{Category: domain.NotifyExtensions, Title: r.Title, Body: inv.ext.man.Name + ": " + r.Body, Link: r.Link}
	switch {
	case r.BotID != "":
		b, err := inv.visibleBot(ctx, r.BotID, "")
		if err != nil {
			return errReply(err)
		}
		inv.svc.Notices.Notify(ctx, b.OwnerID, n)
	case inv.user != nil:
		inv.svc.Notices.Notify(ctx, inv.user.ID, n)
	default:
		return replyErr("invalid", "a system notification must name a bot")
	}
	return reply(nil)
}

var discordAPIPaths = map[string]bool{"users/@me": true, "applications/@me": true, "oauth2/applications/@me": true, "users/@me/guilds": true}

// discordAPI calls Discord's read-only REST API with a bot's own token. The
// token never reaches the extension; it gets a keyed fingerprint so it can
// tell when the token changed.
func (inv *invocation) discordAPI(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID string `json:"bot_id"`
		Path  string `json:"path"`
	}
	_ = json.Unmarshal(raw, &r)
	path, query, _ := strings.Cut(r.Path, "?")
	if !discordAPIPaths[path] || (query != "" && path != "users/@me/guilds") || len(query) > 100 {
		return replyErr("denied", "only "+strings.Join(boolKeys(discordAPIPaths), ", ")+" may be read")
	}
	if on, err := inv.svc.Store.ExtensionBotEnabled(ctx, inv.ext.man.ID, r.BotID); err != nil || !on {
		return replyErr("denied", "this extension is not switched on for the bot")
	}
	if inv.user != nil {
		if _, err := inv.svc.Bots.Get(ctx, *inv.user, r.BotID); err != nil {
			return errReply(err)
		}
	}
	env, err := inv.svc.Bots.DecryptEnv(ctx, r.BotID)
	if err != nil {
		return errReply(err)
	}
	varName, tok := discordToken(env)
	if tok == "" {
		return reply(map[string]any{"status": 0, "token": "missing"})
	}
	if !inv.svc.allow("discord:"+r.BotID, 30) {
		return replyErr("limit", "at most 30 Discord requests per bot per minute")
	}
	base := inv.svc.DiscordAPI
	if base == "" {
		base = "https://discord.com/api/v10"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+r.Path, nil)
	req.Header.Set("Authorization", "Bot "+tok)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/xenycx/rivetpanel, 1)")
	client := inv.svc.HTTP
	if client == nil {
		client = inv.svc.client([]string{"discord.com"})
	}
	res, err := client.Do(req)
	if err != nil {
		return replyErr("unavailable", "Discord could not be reached")
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	mac := hmac.New(sha256.New, []byte("rivetpanel-ext-token-fp:"+inv.ext.man.ID))
	mac.Write([]byte(tok))
	return reply(map[string]any{"status": res.StatusCode, "body": json.RawMessage(validJSON(data)), "token": "present", "token_var": varName,
		"token_fp": hex.EncodeToString(mac.Sum(nil))[:16], "retry_after": res.Header.Get("Retry-After")})
}

func validJSON(b []byte) []byte {
	if json.Valid(b) {
		return b
	}
	q, _ := json.Marshal(string(b))
	return q
}

func boolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (inv *invocation) identity(ctx context.Context, raw []byte) []byte {
	var r struct {
		BotID     string `json:"bot_id"`
		UserID    string `json:"discord_user_id"`
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url"`
	}
	_ = json.Unmarshal(raw, &r)
	if on, err := inv.svc.Store.ExtensionBotEnabled(ctx, inv.ext.man.ID, r.BotID); err != nil || !on {
		return replyErr("denied", "this extension is not switched on for the bot")
	}
	// On a person's behalf, only for a bot that person can see.
	if inv.user != nil {
		if _, err := inv.svc.Bots.Get(ctx, *inv.user, r.BotID); err != nil {
			return errReply(err)
		}
	}
	if !discordID.MatchString(r.UserID) || r.Username == "" || len(r.Username) > 64 {
		return replyErr("invalid", "a Discord user id and name are required")
	}
	if r.AvatarURL != "" && !strings.HasPrefix(r.AvatarURL, "https://cdn.discordapp.com/") {
		return replyErr("invalid", "avatars must be on cdn.discordapp.com")
	}
	return errOrOK(inv.svc.Store.SetBotDiscordIdentity(ctx, r.BotID, r.UserID, r.Username, r.AvatarURL, time.Now().UnixMilli()))
}

// discordUser maps a Discord user to a panel account that linked that Discord
// account and consented to this extension. It returns an opaque handle valid
// for the rest of this invocation only.
func (inv *invocation) discordUser(ctx context.Context, raw []byte) []byte {
	var r struct {
		DiscordUserID string `json:"discord_user_id"`
	}
	_ = json.Unmarshal(raw, &r)
	// Only work that arrives without a signed-in person (a verified public
	// request, a job or an event) may act for someone else; a route a person
	// calls always acts as that person.
	if inv.user != nil {
		return replyErr("denied", "a request made by a signed-in person cannot act for someone else")
	}
	if !discordID.MatchString(r.DiscordUserID) {
		return replyErr("invalid", "not a Discord user id")
	}
	acct, err := inv.svc.Store.GetOAuthAccount(ctx, domain.ProviderDiscord, r.DiscordUserID)
	if err != nil {
		return reply(map[string]any{"linked": false})
	}
	u, err := inv.svc.Store.GetUserByID(ctx, acct.UserID)
	if err != nil || u.Disabled {
		return reply(map[string]any{"linked": false})
	}
	scopes, err := inv.svc.Store.GetExtensionConsent(ctx, inv.ext.man.ID, u.ID)
	if err != nil || len(scopes) == 0 {
		return reply(map[string]any{"linked": true, "consented": false})
	}
	var h [12]byte
	_, _ = rand.Read(h[:])
	handle := hex.EncodeToString(h[:])
	if inv.handles == nil {
		inv.handles = map[string]actingUser{}
	}
	inv.handles[handle] = actingUser{user: u, scopes: scopes}
	return reply(map[string]any{"linked": true, "consented": true, "as": handle, "name": userName(u), "scopes": scopes})
}

// recordVia audits an action an extension took for someone.
func (s *ExtensionService) recordVia(ctx context.Context, u domain.User, action, botID, extName string, err error) {
	if s.Audit == nil {
		return
	}
	label := userName(u) + " via extension " + extName
	t := "via extension " + extName
	e := domain.AuditEvent{ActorID: &u.ID, ActorLabel: &label, Action: action, BotID: &botID, Target: &t}
	if err != nil {
		e.Outcome = "failed"
		var pe *domain.PermissionError
		if errors.As(err, &pe) {
			e.Outcome = "denied"
		}
	}
	s.Audit.Record(ctx, e)
}

// AddonQuery and AddonQueryResult are the runner's query types.
type (
	AddonQuery       = runner.AddonQuery
	AddonQueryResult = runner.AddonQueryResult
)

type pendingConfirm struct {
	user, bot, kind, hash string
	q                     AddonQuery
	expires               time.Time
}

func queryHash(kind string, q AddonQuery) string {
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(append([]byte(kind+"\x00"), b...))
	return hex.EncodeToString(sum[:])
}

// addons serves addons.list, addons.query and addons.confirm. Every call acts
// as the signed-in person and needs the bot's environment permission, the
// same gate as revealing the add-on's password.
func (inv *invocation) addons(ctx context.Context, name string, raw []byte) []byte {
	var r struct {
		BotID string     `json:"bot_id"`
		Kind  string     `json:"kind"`
		Query AddonQuery `json:"query"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return replyErr("invalid", "malformed request")
	}
	// Only the fields this database kind reads survive, so the statement the
	// confirmation dialog shows is exactly the one that runs.
	r.Query = r.Query.ForKind(r.Kind)
	if inv.user == nil {
		return errReply(&domain.PermissionError{Perm: "a signed-in person"})
	}
	u := *inv.user
	if !u.Can(domain.PermBotsEnv) {
		return errReply(&domain.PermissionError{Perm: domain.PermBotsEnv})
	}
	b, err := inv.svc.Bots.Authorize(ctx, u, r.BotID, domain.PermManageEnv)
	if err != nil {
		return errReply(err)
	}
	if name == "addons.list" {
		views, err := inv.svc.Bots.Addons(ctx, u, r.BotID)
		if err != nil {
			return errReply(err)
		}
		out := []map[string]any{}
		for _, v := range views {
			out = append(out, map[string]any{"kind": v.Kind, "name": v.DisplayName, "status": v.Status})
		}
		return reply(map[string]any{"addons": out, "remote": inv.svc.Bots.remote(b.NodeID)})
	}
	if inv.svc.Addons == nil {
		return replyErr("unavailable", "database queries need the local Docker runner")
	}
	if inv.svc.Bots.remote(b.NodeID) {
		return replyErr("unavailable", "database queries are not available yet for apps on remote nodes")
	}
	if name == "addons.confirm" {
		// Only the panel's own dialog redeems a confirmation (see ConfirmAddonWrite).
		return replyErr("denied", "write confirmations are redeemed by the panel, not by extensions")
	}
	if !inv.svc.allow("addons:"+u.ID, 60) {
		return replyErr("limit", "at most 60 queries per minute")
	}
	if r.Query.Mode == "write" {
		token := make([]byte, 16)
		_, _ = rand.Read(token)
		t := hex.EncodeToString(token)
		inv.svc.mu.Lock()
		if inv.svc.confirms == nil {
			inv.svc.confirms = map[string]pendingConfirm{}
		}
		for k, p := range inv.svc.confirms {
			if time.Now().After(p.expires) {
				delete(inv.svc.confirms, k)
			}
		}
		inv.svc.confirms[t] = pendingConfirm{user: u.ID, bot: b.ID, kind: r.Kind, hash: queryHash(r.Kind, r.Query), q: r.Query, expires: time.Now().Add(2 * time.Minute)}
		inv.svc.mu.Unlock()
		return reply(map[string]any{"confirm": t})
	}
	r.Query.Mode = "read"
	res, err := inv.svc.Addons.QueryAddon(ctx, b, r.Kind, r.Query)
	inv.svc.recordQuery(ctx, u, b.ID, r.Kind, "read", queryHash(r.Kind, r.Query), err)
	if err != nil {
		return errReply(err)
	}
	return reply(res)
}

// ConfirmAddonWrite runs a write an extension prepared, after the person who
// asked for it confirmed it in the panel's own dialog.
func (s *ExtensionService) ConfirmAddonWrite(ctx context.Context, actor domain.User, token string) (AddonQueryResult, error) {
	s.mu.Lock()
	p, ok := s.confirms[token]
	delete(s.confirms, token)
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) || p.user != actor.ID {
		return AddonQueryResult{}, domain.Invalid("this confirmation expired or belongs to someone else; run the query again")
	}
	if !actor.Can(domain.PermBotsEnv) {
		return AddonQueryResult{}, &domain.PermissionError{Perm: domain.PermBotsEnv}
	}
	b, err := s.Bots.Authorize(ctx, actor, p.bot, domain.PermManageEnv)
	if err != nil {
		return AddonQueryResult{}, err
	}
	if s.Addons == nil {
		return AddonQueryResult{}, domain.Invalid("database queries need the local Docker runner")
	}
	if queryHash(p.kind, p.q) != p.hash {
		return AddonQueryResult{}, domain.Invalid("the query changed")
	}
	if len(p.q.ForKind(p.kind).Describe(p.kind)) > 2000 {
		return AddonQueryResult{}, domain.Invalid("this change is too long to show for confirmation")
	}
	res, err := s.Addons.QueryAddon(ctx, b, p.kind, p.q.ForKind(p.kind))
	s.recordQuery(ctx, actor, b.ID, p.kind, "write", p.hash, err)
	return res, err
}

// PendingAddonWrite describes a confirmation for the panel's dialog.
func (s *ExtensionService) PendingAddonWrite(actor domain.User, token string) (map[string]string, error) {
	s.mu.Lock()
	p, ok := s.confirms[token]
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) || p.user != actor.ID {
		return nil, domain.ErrNotFound
	}
	text := p.q.ForKind(p.kind).Describe(p.kind)
	// A statement too long to show in full is not offered for confirmation:
	// a person approves only what they could read.
	if len(text) > 2000 {
		return nil, domain.Invalid("this change is too long to show for confirmation (at most 2000 characters)")
	}
	return map[string]string{"bot_id": p.bot, "kind": p.kind, "query": text}, nil
}

func (s *ExtensionService) recordQuery(ctx context.Context, u domain.User, botID, kind, mode, hash string, err error) {
	if s.Audit == nil {
		return
	}
	label := userName(u)
	t := fmt.Sprintf("%s %s query %s", kind, mode, hash[:12])
	e := domain.AuditEvent{ActorID: &u.ID, ActorLabel: &label, Action: "addon.query", BotID: &botID, Target: &t}
	if err != nil {
		e.Outcome = "failed"
	}
	s.Audit.Record(ctx, e)
}
