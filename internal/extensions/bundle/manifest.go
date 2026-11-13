// Package bundle reads, validates, packs and signs RivetPanel extension
// bundles (.rpx): a zip holding manifest.json, plugin.wasm, an optional icon,
// optional single-file UI pages and a detached Ed25519 SIGNATURE.
package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// ManifestVersion is the only manifest format this panel reads.
const ManifestVersion = 1

// ABI is the host/guest calling convention version (see internal/extensions/runtime).
const ABI = 1

// Capability names. A manifest may only request these; the host checks every
// call against what an administrator granted.
const (
	CapKV          = "kv"               // extension-private key/value storage
	CapSettings    = "settings"         // read the extension's non-secret settings
	CapHTTP        = "http.fetch"       // HTTPS to the hosts listed on the capability
	CapBotsRead    = "bots.read"        // list and read bots the acting user (or an enabled bot) can see
	CapBotPower    = "bot.power"        // start, stop, restart as the acting user
	CapBotLogs     = "bot.logs"         // recent log lines as the acting user
	CapBotDeploy   = "bot.deploy"       // redeploy as the acting user
	CapNotify      = "notify"           // in-panel notifications
	CapDiscordAPI  = "bot.discord_api"  // read-only Discord REST with the bot's own token (never exposed)
	CapBotIdentity = "bot.identity"     // update a bot's Discord identity (name, avatar)
	CapDiscordUser = "identity.discord" // map a Discord user to a consenting panel user
	CapAddonsQuery = "addons.query"     // query a bot's database add-on as the acting user
)

// CapabilityInfo describes a capability for the review screen: what it
// lets an extension do (Label), which part of the panel it touches (Area:
// "own", "bots", "people" or "internet") and the safeguard the panel itself
// enforces on every use (Guard). Capabilities are not ranked by danger: each
// is shown with the limit that applies to it, so a reviewer decides from
// facts, not from a warning colour.
type CapabilityInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Area  string `json:"area"`
	Guard string `json:"guard"`
}

// Capabilities is the plain-language catalog shown before consent. Every
// Guard sentence must describe a check the host performs (extensions_host.go).
var Capabilities = []CapabilityInfo{
	{CapKV, "Store its own data in the panel", "own",
		"Kept apart from other extensions, at most 8 MiB, and deleted when it is uninstalled."},
	{CapSettings, "Read its own settings", "own",
		"Secret settings are never readable: the panel attaches them to requests itself."},
	{CapNotify, "Send in-panel notifications", "people",
		"Only to a bot's owner or the person using it, at most 30 a minute."},
	{CapHTTP, "Connect to the listed internet hosts", "internet",
		"HTTPS to exactly these hosts; redirects are re-checked and private or local addresses are refused."},
	{CapBotsRead, "See bots", "bots",
		"Never more than the person using it can see; on its own, only bots it was turned on for."},
	{CapBotLogs, "Read recent bot output", "bots",
		"At most 200 lines, through the same access checks as the panel."},
	{CapDiscordAPI, "Read a bot's Discord profile", "bots",
		"Read-only Discord endpoints. The panel adds the bot's token; the extension never sees it."},
	{CapBotIdentity, "Update a bot's Discord name and avatar in the panel", "bots",
		"Only the name and picture the panel shows for that bot, and only for bots it was turned on for."},
	{CapBotPower, "Start, stop and restart bots", "bots",
		"With the permissions of the person using it, never more."},
	{CapBotDeploy, "Redeploy bots", "bots",
		"With the permissions of the person using it, never more."},
	{CapDiscordUser, "Act for people who linked Discord", "people",
		"Only people who allowed it under Settings, with the scopes they chose; it never adds permissions."},
	{CapAddonsQuery, "Query bot databases", "bots",
		"Read-only sessions; a change runs only after the person confirms it in a panel dialog."},
}

func knownCapability(n string) bool {
	for _, c := range Capabilities {
		if c.Name == n {
			return true
		}
	}
	return false
}

// Manifest is manifest.json. Unknown fields are rejected so a newer format
// is never half-understood.
type Manifest struct {
	ManifestVersion int          `json:"manifest_version"`
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Version         string       `json:"version"`
	Publisher       string       `json:"publisher"`
	Description     string       `json:"description"`
	Homepage        string       `json:"homepage,omitempty"`
	Compat          Compat       `json:"compat"`
	Runtime         Runtime      `json:"runtime"`
	DataVersion     int          `json:"data_version,omitempty"`
	Capabilities    []Capability `json:"capabilities"`
	Settings        []Setting    `json:"settings,omitempty"`
	Routes          []Route      `json:"routes,omitempty"`
	Events          []string     `json:"events,omitempty"`
	Jobs            []Job        `json:"jobs,omitempty"`
	Contributes     Contributes  `json:"contributes"`
	UI              []UIPage     `json:"ui,omitempty"`
}

type Compat struct {
	Panel string `json:"panel"` // e.g. ">=0.9.0 <1.0.0"; "" accepts every version
	ABI   int    `json:"abi"`
}

type Runtime struct {
	MemoryMB  int `json:"memory_mb"`
	TimeoutMS int `json:"timeout_ms"`
}

type Capability struct {
	Name     string   `json:"name"`
	Reason   string   `json:"reason"`
	Optional bool     `json:"optional,omitempty"`
	Hosts    []string `json:"hosts,omitempty"` // http.fetch only
}

// Setting is one administrator-editable field. Secret values are write-only:
// the extension can never read them, only have the host attach them to an
// HTTPS request to one of InjectHosts.
type Setting struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // string | number | boolean | enum | secret
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`
	Default     any      `json:"default,omitempty"`
	InjectHosts []string `json:"inject_hosts,omitempty"`
}

// Route is an HTTP endpoint the extension serves through the panel.
//
//	auth "user":   /api/v1/ext/{id}{path}, signed-in users holding Perm
//	auth "bot":    /api/v1/bots/{bot}/ext/{id}{path}, users holding BotPerm on that bot
//	auth "public": /api/v1/ext-public/{id}{path}, anyone; Verify is checked first
type Route struct {
	ID       string  `json:"id"`
	Method   string  `json:"method"`
	Path     string  `json:"path"`
	Auth     string  `json:"auth"`
	Perm     string  `json:"perm,omitempty"`
	BotPerm  string  `json:"bot_perm,omitempty"`
	Verify   *Verify `json:"verify,omitempty"`
	MaxBody  int     `json:"max_body,omitempty"`
	Mutating bool    `json:"mutating,omitempty"` // recorded in the audit log
}

// Verify is a request signature the host checks before running any code.
type Verify struct {
	Scheme     string `json:"scheme"`      // discord-ed25519 | hmac-sha256
	KeySetting string `json:"key_setting"` // the setting holding the public key or secret
}

type Job struct {
	ID        string `json:"id"`
	Every     string `json:"every"` // Go duration, at least 5m
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// Contributes declares where the extension appears in the interface.
type Contributes struct {
	Nav         []NavItem    `json:"nav,omitempty"`
	BotTabs     []BotTab     `json:"bot_tabs,omitempty"`
	BotCards    []BotCard    `json:"bot_cards,omitempty"`
	UserConsent *UserConsent `json:"user_consent,omitempty"`
	// BotEnablement asks a bot's manager to switch the extension on per bot
	// (system jobs only ever see enabled bots).
	BotEnablement string `json:"bot_enablement,omitempty"` // the explanation shown on the toggle
}

// PermExtension, as a nav item's or user route's perm, stands for the
// extension's own permission, ext:<id> (domain.ExtensionPermission), which a
// role grants as "Use <extension name>". Built-in administrators always hold
// it.
const PermExtension = "extension"

// NavItem is a link in the panel's navigation to one of the extension's UI
// pages. The page is served only to accounts that may see a nav item opening
// it (or, if a bot tab opens it too, to any signed-in account; see
// ExtensionService.UIPage).
type NavItem struct {
	Label string `json:"label"`
	Page  string `json:"page"`
	Perm  string `json:"perm,omitempty"`
}

type BotTab struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Page    string `json:"page"`
	BotPerm string `json:"bot_perm,omitempty"`
}

// BotCard is a card on the bot overview. Route (a "bot" route) returns
// widget JSON in the docs/guide/05-observe/01-widgets.md format.
type BotCard struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Route string `json:"route"`
}

type UserConsent struct {
	Explanation string         `json:"explanation"`
	Scopes      []ConsentScope `json:"scopes"`
}

type ConsentScope struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type UIPage struct {
	ID   string `json:"id"`
	File string `json:"file"` // ui/<name>.html
}

// Events an extension may subscribe to.
var Events = map[string]string{
	"bot.status": "a bot's observed state changed",
}

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	pathRe    = regexp.MustCompile(`^(/[a-z0-9_-]+)+$|^/$`)
	hostRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	settingRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// ParseManifest decodes and validates manifest.json strictly.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest.json: %w", err)
	}
	if dec.More() {
		return Manifest{}, fmt.Errorf("manifest.json: trailing data")
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func bad(format string, a ...any) error { return fmt.Errorf("manifest: "+format, a...) }

// Validate checks every field. It is also run on manifests read back from the
// database, so a tightened rule is applied to installed extensions too.
func (m *Manifest) Validate() error {
	if m.ManifestVersion != ManifestVersion {
		return bad("manifest_version %d is not supported (want %d)", m.ManifestVersion, ManifestVersion)
	}
	if !domain.ValidExtensionID(m.ID) {
		return bad("id %q must be reverse-DNS style, lower case, at most 64 characters (e.g. dev.example.tool)", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 60 {
		return bad("name is required (at most 60 characters)")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return bad("version: %v", err)
	}
	if strings.TrimSpace(m.Publisher) == "" || len(m.Publisher) > 80 || len(m.Description) > 500 {
		return bad("publisher is required (at most 80 characters); description at most 500")
	}
	if m.Homepage != "" && !strings.HasPrefix(m.Homepage, "https://") {
		return bad("homepage must be an https:// URL")
	}
	if m.Compat.ABI != ABI {
		return bad("compat.abi %d is not supported (this panel speaks %d)", m.Compat.ABI, ABI)
	}
	if _, err := ParseRange(m.Compat.Panel); err != nil {
		return bad("compat.panel: %v", err)
	}
	if m.Runtime.MemoryMB < 1 || m.Runtime.MemoryMB > 128 {
		return bad("runtime.memory_mb must be 1–128")
	}
	if m.Runtime.TimeoutMS < 100 || m.Runtime.TimeoutMS > 30000 {
		return bad("runtime.timeout_ms must be 100–30000")
	}
	if m.DataVersion < 0 {
		return bad("data_version must not be negative")
	}

	caps := map[string]bool{}
	for _, c := range m.Capabilities {
		if !knownCapability(c.Name) {
			return bad("unknown capability %q", c.Name)
		}
		if caps[c.Name] {
			return bad("capability %q listed twice", c.Name)
		}
		caps[c.Name] = true
		if strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 200 {
			return bad("capability %q needs a reason (at most 200 characters)", c.Name)
		}
		if c.Name == CapHTTP {
			if len(c.Hosts) == 0 || len(c.Hosts) > 16 {
				return bad("http.fetch needs 1–16 hosts")
			}
			for _, h := range c.Hosts {
				if err := validHost(h); err != nil {
					return err
				}
			}
		} else if len(c.Hosts) > 0 {
			return bad("only http.fetch takes hosts")
		}
	}

	keys := map[string]Setting{}
	for _, s := range m.Settings {
		if !settingRe.MatchString(s.Key) {
			return bad("setting key %q must be lower_snake_case", s.Key)
		}
		if _, dup := keys[s.Key]; dup {
			return bad("setting %q listed twice", s.Key)
		}
		keys[s.Key] = s
		if strings.TrimSpace(s.Label) == "" || len(s.Label) > 80 || len(s.Description) > 300 {
			return bad("setting %q needs a label (at most 80 characters); description at most 300", s.Key)
		}
		switch s.Type {
		case "string", "number", "boolean":
		case "enum":
			if len(s.Options) == 0 || len(s.Options) > 32 {
				return bad("enum setting %q needs 1–32 options", s.Key)
			}
		case "secret":
			if s.Default != nil {
				return bad("secret setting %q cannot have a default", s.Key)
			}
			for _, h := range s.InjectHosts {
				if err := validHost(h); err != nil {
					return err
				}
			}
		default:
			return bad("setting %q has unknown type %q", s.Key, s.Type)
		}
		if s.Type != "secret" && len(s.InjectHosts) > 0 {
			return bad("only secret settings take inject_hosts")
		}
	}
	if len(m.Settings) > 32 {
		return bad("at most 32 settings")
	}

	routes := map[string]Route{}
	seen := map[string]bool{}
	for _, r := range m.Routes {
		if !slugRe.MatchString(r.ID) {
			return bad("route id %q must be a short lower-case slug", r.ID)
		}
		if _, dup := routes[r.ID]; dup {
			return bad("route %q listed twice", r.ID)
		}
		routes[r.ID] = r
		switch r.Method {
		case "GET", "POST", "PUT", "DELETE":
		default:
			return bad("route %q: method must be GET, POST, PUT or DELETE", r.ID)
		}
		if len(r.Path) > 100 || !pathRe.MatchString(r.Path) {
			return bad("route %q: path %q must be like /name or /name/sub (lower case)", r.ID, r.Path)
		}
		k := r.Auth + " " + r.Method + " " + r.Path
		if seen[k] {
			return bad("route %q duplicates %s", r.ID, k)
		}
		seen[k] = true
		switch r.Auth {
		case "user":
			if r.BotPerm != "" {
				return bad("route %q: bot_perm needs auth \"bot\"", r.ID)
			}
			if err := m.checkPerm(r.Perm); err != nil {
				return bad("route %q: %v", r.ID, err)
			}
		case "bot":
			if r.Perm != "" {
				return bad("route %q: perm needs auth \"user\"", r.ID)
			}
			if _, ok := BotPerms[r.BotPerm]; !ok {
				return bad("route %q: bot_perm must be one of %s", r.ID, strings.Join(botPermNames(), ", "))
			}
		case "public":
			if r.Perm != "" || r.BotPerm != "" {
				return bad("route %q: public routes take no permission", r.ID)
			}
			if r.Verify == nil {
				return bad("route %q: public routes must verify a request signature", r.ID)
			}
			if r.Method != "POST" {
				return bad("route %q: public routes must be POST", r.ID)
			}
		default:
			return bad("route %q: auth must be user, bot or public", r.ID)
		}
		if r.Verify != nil {
			if r.Verify.Scheme != "discord-ed25519" && r.Verify.Scheme != "hmac-sha256" {
				return bad("route %q: verify scheme must be discord-ed25519 or hmac-sha256", r.ID)
			}
			if _, ok := keys[r.Verify.KeySetting]; !ok {
				return bad("route %q: verify key_setting %q is not a setting", r.ID, r.Verify.KeySetting)
			}
		}
		if r.MaxBody < 0 || r.MaxBody > 256<<10 {
			return bad("route %q: max_body must be at most 262144", r.ID)
		}
	}
	if len(m.Routes) > 64 {
		return bad("at most 64 routes")
	}

	for _, e := range m.Events {
		if _, ok := Events[e]; !ok {
			return bad("unknown event %q", e)
		}
	}
	jobs := map[string]bool{}
	for _, j := range m.Jobs {
		if !slugRe.MatchString(j.ID) || jobs[j.ID] {
			return bad("job id %q must be a unique lower-case slug", j.ID)
		}
		jobs[j.ID] = true
		d, err := time.ParseDuration(j.Every)
		if err != nil || d < 5*time.Minute || d > 7*24*time.Hour {
			return bad("job %q: every must be a duration from 5m to 168h", j.ID)
		}
		if j.TimeoutMS < 0 || j.TimeoutMS > 120000 {
			return bad("job %q: timeout_ms must be at most 120000", j.ID)
		}
	}
	if len(m.Jobs) > 8 {
		return bad("at most 8 jobs")
	}

	pages := map[string]bool{}
	for _, p := range m.UI {
		if !slugRe.MatchString(p.ID) || pages[p.ID] {
			return bad("ui page id %q must be a unique lower-case slug", p.ID)
		}
		pages[p.ID] = true
		if !uiFileRe.MatchString(p.File) {
			return bad("ui page %q: file must be ui/<name>.html", p.ID)
		}
	}
	c := m.Contributes
	for _, n := range c.Nav {
		if strings.TrimSpace(n.Label) == "" || len(n.Label) > 30 || !pages[n.Page] {
			return bad("nav item %q needs a label (at most 30 characters) and a declared ui page", n.Label)
		}
		if err := m.checkPerm(n.Perm); err != nil {
			return bad("nav item %q: %v", n.Label, err)
		}
	}
	tabs := map[string]bool{}
	for _, t := range c.BotTabs {
		if !slugRe.MatchString(t.ID) || tabs[t.ID] || strings.TrimSpace(t.Label) == "" || len(t.Label) > 30 || !pages[t.Page] {
			return bad("bot tab %q needs a unique id, a label (at most 30 characters) and a declared ui page", t.ID)
		}
		tabs[t.ID] = true
		if _, ok := BotPerms[t.BotPerm]; t.BotPerm != "" && !ok {
			return bad("bot tab %q: unknown bot_perm %q", t.ID, t.BotPerm)
		}
	}
	cards := map[string]bool{}
	for _, bc := range c.BotCards {
		r, ok := routes[bc.Route]
		if !slugRe.MatchString(bc.ID) || cards[bc.ID] || strings.TrimSpace(bc.Title) == "" || len(bc.Title) > 40 || !ok || r.Auth != "bot" || r.Method != "GET" {
			return bad("bot card %q needs a unique id, a title and a GET bot route", bc.ID)
		}
		cards[bc.ID] = true
	}
	if uc := c.UserConsent; uc != nil {
		if strings.TrimSpace(uc.Explanation) == "" || len(uc.Scopes) == 0 || len(uc.Scopes) > 16 {
			return bad("user_consent needs an explanation and 1–16 scopes")
		}
		ids := map[string]bool{}
		for _, s := range uc.Scopes {
			if !slugRe.MatchString(s.ID) || ids[s.ID] || strings.TrimSpace(s.Label) == "" {
				return bad("consent scope %q needs a unique id and a label", s.ID)
			}
			ids[s.ID] = true
		}
	}
	if caps[CapDiscordUser] && c.UserConsent == nil {
		return bad("identity.discord needs contributes.user_consent")
	}
	if len(c.BotEnablement) > 300 {
		return bad("bot_enablement explanation is at most 300 characters")
	}
	return nil
}

// checkPerm validates a nav item's or user route's perm: empty (any signed-in
// account), "extension" or this extension's own ext:<id>, or a panel
// permission. Another extension's permission is refused, so an extension can
// never piggyback on access granted for a different one.
func (m *Manifest) checkPerm(p string) error {
	if p == "" || p == PermExtension {
		return nil
	}
	if id, ok := domain.ExtensionOfPermission(p); ok {
		if id != m.ID {
			return fmt.Errorf("permission %q belongs to another extension; use %q for this extension's own", p, PermExtension)
		}
		return nil
	}
	if !domain.ValidPermission(p) {
		return fmt.Errorf("unknown permission %q", p)
	}
	return nil
}

// ResolvePerm turns a declared perm into the permission checked at run time:
// "extension" becomes ext:<id>; everything else is returned as is ("" means
// no permission is needed).
func (m *Manifest) ResolvePerm(p string) string {
	if p == PermExtension {
		return domain.ExtensionPermission(m.ID)
	}
	return p
}

var uiFileRe = regexp.MustCompile(`^ui/[a-z0-9][a-z0-9_-]{0,39}\.html$`)

func validHost(h string) error {
	if len(h) > 253 || !hostRe.MatchString(h) {
		return bad("host %q must be a lower-case DNS name (no scheme, port, path or wildcard)", h)
	}
	if net.ParseIP(h) != nil || h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return bad("host %q is not a public DNS name", h)
	}
	return nil
}

// BotPerms are the per-bot checks a bot route or tab may require, mapped to
// the sub-user permission bits BotService.Authorize checks ("view" is any
// access to the bot).
var BotPerms = map[string]int{
	"view":       0,
	"console":    domain.PermViewConsole,
	"power":      domain.PermPower,
	"files":      domain.PermEditFiles,
	"manage_env": domain.PermManageEnv,
}

func botPermNames() []string {
	out := make([]string, 0, len(BotPerms))
	for k := range BotPerms {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Cap returns the requested capability.
func (m *Manifest) Cap(name string) (Capability, bool) {
	for _, c := range m.Capabilities {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

// Route finds a route by id.
func (m *Manifest) Route(id string) (Route, bool) {
	for _, r := range m.Routes {
		if r.ID == id {
			return r, true
		}
	}
	return Route{}, false
}

// Setting finds a setting by key.
func (m *Manifest) Setting(key string) (Setting, bool) {
	for _, s := range m.Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Page finds a UI page by id.
func (m *Manifest) Page(id string) (UIPage, bool) {
	for _, p := range m.UI {
		if p.ID == id {
			return p, true
		}
	}
	return UIPage{}, false
}
