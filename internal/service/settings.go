package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/mail"
	"github.com/xenycx/rivetpanel/internal/oauth"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// SettingsStore is the persistence the settings need.
type SettingsStore interface {
	Settings(ctx context.Context) (map[string]domain.Setting, error)
	PutSettings(ctx context.Context, set []domain.Setting, nowMS int64) error
	UserCount(ctx context.Context) (int, error)
}

// Setting keys. Secret ones are sealed under namespace "settings".
const (
	SetPublicURL     = "public_url"
	SetGitHubID      = "github_client_id"
	SetGitHubSecret  = "github_client_secret"
	SetDiscordID     = "discord_client_id"
	SetDiscordSecret = "discord_client_secret"
	SetAllowSignup   = "oauth_allow_signup"
	SetMailKey       = "mailgun_api_key"
	SetMailDomain    = "mailgun_domain"
	SetMailRegion    = "mailgun_region"
	SetMailFrom      = "mail_from"
)

var secretSettings = map[string]bool{SetGitHubSecret: true, SetDiscordSecret: true, SetMailKey: true}

// EnvSettings are values fixed by the environment file; they win over stored
// settings and are shown read-only.
type EnvSettings struct {
	PublicURL                   string
	GitHubID, GitHubSecret      string
	DiscordID, DiscordSecret    string
	AllowSignup, AllowSignupSet bool
	Production                  bool
	// Mailgun (see internal/mail); empty means "decided in the panel".
	MailKey, MailDomain, MailRegion, MailFrom string
}

// SettingsService holds panel settings chosen in the setup wizard or on the
// administration page and applies them without a restart.
type SettingsService struct {
	Store SettingsStore
	Keys  *secrets.Keyring
	Env   EnvSettings
	OAuth *OAuthService
	Auth  *AuthService
	Log   *slog.Logger
	Now   func() time.Time

	mu        sync.Mutex // guards setupCode
	setupCode string
	setupMu   sync.Mutex // one setup completion at a time
}

func (s *SettingsService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Effective is the merged configuration.
type Effective struct {
	PublicURL                string
	GitHubID, GitHubSecret   string
	DiscordID, DiscordSecret string
	AllowSignup              bool
	MailKey, MailDomain      string
	MailRegion, MailFrom     string
	Locked                   map[string]bool // set by the environment
}

// Effective merges stored settings under the environment.
func (s *SettingsService) Effective(ctx context.Context) (Effective, error) {
	stored, err := s.Store.Settings(ctx)
	if err != nil {
		return Effective{}, err
	}
	val := func(k string) string {
		st, ok := stored[k]
		if !ok {
			return ""
		}
		if secretSettings[k] && st.Cipher != nil {
			pt, err := s.Keys.Open("settings", k, secrets.Sealed{Ciphertext: st.Cipher, Nonce: st.Nonce, KeyID: st.KeyID})
			if err != nil {
				if s.Log != nil {
					s.Log.Warn("a stored setting cannot be decrypted with the current keys", "setting", k)
				}
				return ""
			}
			return string(pt)
		}
		return st.Value
	}
	e := Effective{Locked: map[string]bool{}}
	pick := func(dst *string, envV, key string) {
		if envV != "" {
			*dst, e.Locked[key] = envV, true
		} else {
			*dst = val(key)
		}
	}
	pick(&e.PublicURL, s.Env.PublicURL, SetPublicURL)
	pick(&e.GitHubID, s.Env.GitHubID, SetGitHubID)
	pick(&e.GitHubSecret, s.Env.GitHubSecret, SetGitHubSecret)
	pick(&e.DiscordID, s.Env.DiscordID, SetDiscordID)
	pick(&e.DiscordSecret, s.Env.DiscordSecret, SetDiscordSecret)
	pick(&e.MailKey, s.Env.MailKey, SetMailKey)
	pick(&e.MailDomain, s.Env.MailDomain, SetMailDomain)
	pick(&e.MailRegion, s.Env.MailRegion, SetMailRegion)
	pick(&e.MailFrom, s.Env.MailFrom, SetMailFrom)
	if s.Env.AllowSignupSet {
		e.AllowSignup, e.Locked[SetAllowSignup] = s.Env.AllowSignup, true
	} else {
		e.AllowSignup = val(SetAllowSignup) == "1"
	}
	return e, nil
}

// Apply loads the effective settings into the running services.
func (s *SettingsService) Apply(ctx context.Context) error {
	e, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	providers := map[string]oauth.Provider{}
	if e.PublicURL != "" && e.GitHubID != "" && e.GitHubSecret != "" {
		providers["github"] = &oauth.GitHub{ClientID: e.GitHubID, Secret: e.GitHubSecret}
	}
	if e.PublicURL != "" && e.DiscordID != "" && e.DiscordSecret != "" {
		providers["discord"] = &oauth.Discord{ClientID: e.DiscordID, Secret: e.DiscordSecret}
	}
	if s.OAuth != nil {
		s.OAuth.Reconfigure(providers, e.PublicURL, e.AllowSignup)
	}
	if s.Log != nil {
		for name := range providers {
			s.Log.Info("oauth provider enabled", "provider", name, "redirect_uri", e.PublicURL+"/api/v1/auth/"+name+"/callback")
		}
	}
	return nil
}

// SettingsInput changes settings; nil fields are left as they are. An empty
// secret keeps the stored one unless ClearX is set.
type SettingsInput struct {
	PublicURL                *string
	GitHubID, GitHubSecret   *string
	DiscordID, DiscordSecret *string
	AllowSignup              *bool
	MailKey, MailDomain      *string
	MailRegion, MailFrom     *string
	// UnverifiedRestrict replaces the permissions withheld from accounts
	// whose email address is not verified (empty = policy off).
	UnverifiedRestrict *[]string
}

// MailConfig is the effective Mailgun configuration (settings under the
// environment). It reads the settings table on each call and keeps nothing.
func (s *SettingsService) MailConfig(ctx context.Context) (mail.Config, error) {
	e, err := s.Effective(ctx)
	if err != nil {
		return mail.Config{}, err
	}
	return mail.Config{APIKey: e.MailKey, Domain: e.MailDomain, Region: e.MailRegion, From: e.MailFrom}, nil
}

// ValidatePublicURL accepts a bare origin: https, or http for loopback and
// development.
func ValidatePublicURL(raw string, production bool) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", domain.Invalid("enter the address as a bare origin, like https://panel.example.com")
	}
	host := u.Hostname()
	loop := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loop = true
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (loop || !production):
	default:
		return "", domain.Invalid("the panel address must use https (http only for localhost)")
	}
	return u.Scheme + "://" + u.Host, nil
}

var oauthIDRe = func(s string) bool {
	if len(s) > 200 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Update validates and stores settings, then applies them (administrators).
func (s *SettingsService) Update(ctx context.Context, actor domain.User, in SettingsInput) error {
	if !actor.Can(domain.PermSettingsManage) {
		return domain.ErrForbidden
	}
	return s.update(ctx, in)
}

func (s *SettingsService) update(ctx context.Context, in SettingsInput) error {
	var set []domain.Setting
	plain := func(key string, v *string, check func(string) (string, error)) error {
		if v == nil {
			return nil
		}
		val, err := check(*v)
		if err != nil {
			return err
		}
		set = append(set, domain.Setting{Key: key, Value: val})
		return nil
	}
	id := func(label string) func(string) (string, error) {
		return func(v string) (string, error) {
			v = strings.TrimSpace(v)
			if !oauthIDRe(v) {
				return "", domain.Invalid("the " + label + " client ID looks wrong; copy it again from the developer portal")
			}
			return v, nil
		}
	}
	if err := plain(SetPublicURL, in.PublicURL, func(v string) (string, error) { return ValidatePublicURL(v, s.Env.Production) }); err != nil {
		return err
	}
	if err := plain(SetGitHubID, in.GitHubID, id("GitHub")); err != nil {
		return err
	}
	if err := plain(SetDiscordID, in.DiscordID, id("Discord")); err != nil {
		return err
	}
	for key, v := range map[string]*string{SetGitHubSecret: in.GitHubSecret, SetDiscordSecret: in.DiscordSecret} {
		if v == nil {
			continue
		}
		val := strings.TrimSpace(*v)
		if len(val) > 400 || strings.ContainsAny(val, " \n\r\t") {
			return domain.Invalid("that client secret looks wrong; copy it again from the developer portal")
		}
		if val == "" {
			set = append(set, domain.Setting{Key: key}) // remove
			continue
		}
		sl, err := s.Keys.Seal("settings", key, []byte(val))
		if err != nil {
			return err
		}
		set = append(set, domain.Setting{Key: key, Cipher: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID})
	}
	asInvalid := func(check func(string) (string, error)) func(string) (string, error) {
		return func(v string) (string, error) {
			out, err := check(v)
			if err != nil {
				return "", domain.Invalid(err.Error())
			}
			return out, nil
		}
	}
	if err := plain(SetMailDomain, in.MailDomain, asInvalid(mail.ValidateDomain)); err != nil {
		return err
	}
	if err := plain(SetMailFrom, in.MailFrom, asInvalid(mail.ValidateFrom)); err != nil {
		return err
	}
	if err := plain(SetMailRegion, in.MailRegion, func(v string) (string, error) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && v != mail.RegionUS && v != mail.RegionEU {
			return "", domain.Invalid("the Mailgun region must be us or eu")
		}
		return v, nil
	}); err != nil {
		return err
	}
	if in.MailKey != nil {
		val := strings.TrimSpace(*in.MailKey)
		if len(val) > 200 || strings.ContainsAny(val, " \n\r\t") {
			return domain.Invalid("that Mailgun API key looks wrong; copy it again from the Mailgun dashboard")
		}
		if val == "" {
			set = append(set, domain.Setting{Key: SetMailKey}) // remove
		} else {
			sl, err := s.Keys.Seal("settings", SetMailKey, []byte(val))
			if err != nil {
				return err
			}
			set = append(set, domain.Setting{Key: SetMailKey, Cipher: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID})
		}
	}
	if in.UnverifiedRestrict != nil {
		var keep []string
		for _, p := range *in.UnverifiedRestrict {
			if !domain.ValidPermission(p) {
				return domain.Invalid("unknown permission " + strings.ToValidUTF8(p, "?"))
			}
			if !slices.Contains(keep, p) {
				keep = append(keep, p)
			}
		}
		set = append(set, domain.Setting{Key: sqlite.UnverifiedPolicyKey, Value: strings.Join(keep, ",")})
	}
	if in.AllowSignup != nil {
		v := ""
		if *in.AllowSignup {
			v = "1"
		}
		set = append(set, domain.Setting{Key: SetAllowSignup, Value: v})
	}
	if len(set) > 0 {
		if err := s.Store.PutSettings(ctx, set, s.now().UnixMilli()); err != nil {
			return err
		}
	}
	e, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	if (e.GitHubID != "" || e.GitHubSecret != "" || e.DiscordID != "" || e.DiscordSecret != "") && e.PublicURL == "" {
		return domain.Invalid("set the panel address first: sign-in providers send people back to it")
	}
	return s.Apply(ctx)
}

// SettingsView is what the settings page shows. Secrets are never returned.
type SettingsView struct {
	PublicURL                      string
	GitHubID, DiscordID            string
	GitHubSecretSet, DiscordSecSet bool
	AllowSignup                    bool
	Locked                         map[string]bool
	GitHubEnabled, DiscordEnabled  bool
	MailKeySet, MailEnabled        bool
	MailDomain, MailRegion         string
	MailFrom                       string
	UnverifiedRestrict             []string
}

// View returns the current settings without secret values (administrators).
func (s *SettingsService) View(ctx context.Context, actor domain.User) (SettingsView, error) {
	if !actor.Can(domain.PermSettingsManage) {
		return SettingsView{}, domain.ErrForbidden
	}
	return s.view(ctx)
}

func (s *SettingsService) view(ctx context.Context) (SettingsView, error) {
	e, err := s.Effective(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	stored, err := s.Store.Settings(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	restrict := sqlite.SplitPermissionList(stored[sqlite.UnverifiedPolicyKey].Value)
	if restrict == nil {
		restrict = []string{}
	}
	return SettingsView{UnverifiedRestrict: restrict, PublicURL: e.PublicURL, GitHubID: e.GitHubID, DiscordID: e.DiscordID, GitHubSecretSet: e.GitHubSecret != "",
		DiscordSecSet: e.DiscordSecret != "", AllowSignup: e.AllowSignup, Locked: e.Locked,
		GitHubEnabled: s.OAuth.Enabled("github"), DiscordEnabled: s.OAuth.Enabled("discord"),
		MailKeySet: e.MailKey != "", MailEnabled: (mail.Config{APIKey: e.MailKey, Domain: e.MailDomain, From: e.MailFrom}).Configured(),
		MailDomain: e.MailDomain, MailRegion: e.MailRegion, MailFrom: e.MailFrom}, nil
}

// ---- first-run setup ----

// SetupNeeded reports whether the installation has no account yet.
func (s *SettingsService) SetupNeeded(ctx context.Context) (bool, error) {
	n, err := s.Store.UserCount(ctx)
	return n == 0, err
}

// SetupCode returns the one-time code that proves the person running the
// setup can read the server's log or files. It is created once per process.
func (s *SettingsService) SetupCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupCode == "" {
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		c := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b) // 16 characters
		s.setupCode = c[:4] + "-" + c[4:8] + "-" + c[8:12] + "-" + c[12:16]
	}
	return s.setupCode
}

func normCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

// CheckSetupCode verifies the code without side effects.
func (s *SettingsService) CheckSetupCode(ctx context.Context, code string) error {
	need, err := s.SetupNeeded(ctx)
	if err != nil {
		return err
	}
	if !need {
		return domain.Invalid("this panel is already set up; sign in instead")
	}
	if subtle.ConstantTimeCompare([]byte(normCode(code)), []byte(normCode(s.SetupCode()))) != 1 {
		return domain.Invalid("that setup code is not right; it is in the panel's log and in the setup-code file next to its database")
	}
	return nil
}

// SetupInput completes the first-run setup.
type SetupInput struct {
	Code     string
	Email    string
	Password string
	Settings SettingsInput
}

// CompleteSetup creates the first administrator, saves the settings and signs
// the administrator in. It works once: while no account exists, and only with
// the setup code.
func (s *SettingsService) CompleteSetup(ctx context.Context, in SetupInput, device string) (Session, error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if err := s.CheckSetupCode(ctx, in.Code); err != nil {
		return Session{}, err
	}
	// Validate the settings before creating anything.
	if in.Settings.PublicURL != nil {
		if _, err := ValidatePublicURL(*in.Settings.PublicURL, s.Env.Production); err != nil {
			return Session{}, err
		}
	}
	u, err := s.Auth.CreateUser(ctx, in.Email, in.Password, domain.RoleAdmin)
	if err != nil {
		return Session{}, err
	}
	if n, err := s.Store.UserCount(ctx); err != nil || n != 1 {
		return Session{}, errors.New("another account was created during setup")
	}
	if err := s.update(ctx, in.Settings); err != nil {
		// The administrator exists; settings can be fixed after signing in.
		if s.Log != nil {
			s.Log.Warn("setup: settings not saved", "err", err)
		}
		sess, serr := s.Auth.IssueSessionFor(ctx, u, device)
		if serr != nil {
			return Session{}, serr
		}
		return sess, err
	}
	return s.Auth.IssueSessionFor(ctx, u, device)
}
