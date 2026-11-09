package service

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
	extrt "github.com/xenycx/rivetpanel/internal/extensions/runtime"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// ExtensionStore is the persistence ExtensionService needs.
type ExtensionStore interface {
	ListExtensions(ctx context.Context) ([]domain.Extension, error)
	GetExtension(ctx context.Context, id string) (domain.Extension, error)
	PutExtension(ctx context.Context, e domain.Extension) error
	SetExtensionState(ctx context.Context, id, state, lastError string, failures int, now int64) error
	DeleteExtension(ctx context.Context, id string) error
	PutExtensionVersion(ctx context.Context, v domain.ExtensionVersion, protect string, keep int) error
	ListExtensionVersions(ctx context.Context, id string) ([]domain.ExtensionVersion, error)
	ListPendingExtensionIDs(ctx context.Context) ([]string, error)
	GetExtensionVersion(ctx context.Context, id, version string) (domain.ExtensionVersion, error)
	DeleteExtensionVersion(ctx context.Context, id, version string) error
	ListExtensionPublishers(ctx context.Context) ([]domain.ExtensionPublisher, error)
	InsertExtensionPublisher(ctx context.Context, p domain.ExtensionPublisher) error
	RevokeExtensionPublisher(ctx context.Context, keyID string, now int64) error
	ListExtensionSettings(ctx context.Context, id string) ([]domain.ExtensionSetting, error)
	PutExtensionSetting(ctx context.Context, id string, s domain.ExtensionSetting, now int64) error
	DeleteExtensionSetting(ctx context.Context, id, key string) error
	GetExtensionKV(ctx context.Context, id, scope, key string, now int64) ([]byte, error)
	PutExtensionKV(ctx context.Context, id, scope, key string, value []byte, expiresAt, now, quota int64) error
	DeleteExtensionKV(ctx context.Context, id, scope, key string) error
	ExtensionBotEnabled(ctx context.Context, id, botID string) (bool, error)
	SetExtensionBotEnabled(ctx context.Context, id, botID, by string, on bool, now int64) error
	ListExtensionBots(ctx context.Context, id string) ([]string, error)
	GetExtensionConsent(ctx context.Context, id, userID string) ([]string, error)
	SetExtensionConsent(ctx context.Context, id, userID string, scopes []string, now int64) error
	ListExtensionJobs(ctx context.Context, id string) ([]domain.ExtensionJob, error)
	PutExtensionJob(ctx context.Context, j domain.ExtensionJob) error
	DeleteExtensionJobsExcept(ctx context.Context, id string, keep []string) error

	GetUserByID(ctx context.Context, id string) (domain.User, error)
	GetOAuthAccount(ctx context.Context, provider, providerUserID string) (domain.OAuthAccount, error)
	GetBot(ctx context.Context, id string) (domain.Bot, error)
	SetBotDiscordIdentity(ctx context.Context, botID, userID, username, avatarURL string, atMS int64) error
}

// Extension limits.
const (
	extKeepVersions = 3
	extKVQuota      = 8 << 20
	extKVValueMax   = 64 << 10
	extMaxFailures  = 5
)

// AddonQuerier runs a query against a bot's database add-on, or a SQLite
// file in its files (kind "sqlite"; see addonquery.go).
type AddonQuerier interface {
	QueryAddon(ctx context.Context, b domain.Bot, kind string, q AddonQuery) (AddonQueryResult, error)
	SQLiteFiles(ctx context.Context, b domain.Bot) ([]filesystem.SQLiteFile, error)
}

// ExtensionService installs, configures and runs panel extensions.
type ExtensionService struct {
	Store        ExtensionStore
	Keys         *secrets.Keyring
	Bots         *BotService
	Deploy       *DeployService
	Notices      *NotificationService
	Audit        *Audit
	Logs         LogTail
	Addons       AddonQuerier
	Bus          *events.Bus
	Builtins     map[string][]byte // built-in bundles (file name -> .rpx), trusted because they ship in the binary
	PanelVersion string
	// AllowUnsigned accepts unsigned bundles (development mode only).
	AllowUnsigned bool
	// SafeMode loads no extension code at all (recovery).
	SafeMode bool
	Log      *slog.Logger
	// HTTP is the client for extension HTTPS requests; nil builds one that
	// refuses private and local addresses.
	HTTP *http.Client
	// DiscordAPI overrides https://discord.com/api/v10 (tests).
	DiscordAPI string

	mu     sync.Mutex
	loaded map[string]*loadedExt
	// confirmations are one-time write approvals (see addons.query).
	confirms map[string]pendingConfirm
	// seen holds signatures of verified public requests until they could no
	// longer be accepted, so a captured request cannot be replayed.
	seen map[string]time.Time
	rate map[string]*rateWindow
}

type loadedExt struct {
	ext      domain.Extension
	man      bundle.Manifest
	files    map[string][]byte
	mod      *extrt.Module
	compiled sync.Once
	err      error
	failures int
	lastUsed time.Time
}

// extIdleUnload is how long compiled extension code stays in memory unused.
// Extensions with public routes (Discord interactions must answer within
// three seconds) stay loaded once used.
const extIdleUnload = 10 * time.Minute

// ExtensionCapabilityView is one requested capability on the review screen.
type ExtensionCapabilityView struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Area     string   `json:"area"`
	Guard    string   `json:"guard"`
	Reason   string   `json:"reason"`
	Optional bool     `json:"optional"`
	Hosts    []string `json:"hosts,omitempty"`
	Granted  bool     `json:"granted"`
}

// ExtensionSettingView is a setting with its current (non-secret) value.
type ExtensionSettingView struct {
	bundle.Setting
	Value any  `json:"value,omitempty"`
	Set   bool `json:"set"`
}

// ExtensionVersionView describes a stored version.
type ExtensionVersionView struct {
	Version     string `json:"version"`
	Source      string `json:"source"`
	KeyID       string `json:"key_id,omitempty"`
	Publisher   string `json:"publisher_key_name,omitempty"`
	SHA256      string `json:"sha256"`
	CreatedAtMS int64  `json:"created_at_ms"`
	Active      bool   `json:"active"`
	Compatible  bool   `json:"compatible"`
	// NewCapabilities are requested by this version but not granted now.
	NewCapabilities []string `json:"new_capabilities,omitempty"`
}

// ExtensionView is an extension as administrators see it.
type ExtensionView struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description"`
	Publisher     string                    `json:"publisher"`
	Homepage      string                    `json:"homepage,omitempty"`
	Version       string                    `json:"version"`
	Source        string                    `json:"source"`
	Installed     bool                      `json:"installed"`
	State         string                    `json:"state"` // enabled | disabled | failed | available
	LastError     string                    `json:"last_error,omitempty"`
	Capabilities  []ExtensionCapabilityView `json:"capabilities"`
	Settings      []ExtensionSettingView    `json:"settings"`
	Versions      []ExtensionVersionView    `json:"versions"`
	Contributes   bundle.Contributes        `json:"contributes"`
	Routes        []bundle.Route            `json:"routes"`
	Jobs          []domain.ExtensionJob     `json:"-"`
	JobViews      []map[string]any          `json:"jobs"`
	Events        []string                  `json:"events"`
	Bots          int                       `json:"bots"`
	MemoryMB      int                       `json:"memory_mb"`
	TimeoutMS     int                       `json:"timeout_ms"`
	UpdateVersion string                    `json:"update_version,omitempty"` // a newer stored version
}

func (s *ExtensionService) now() int64 { return time.Now().UnixMilli() }

func (s *ExtensionService) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func requireBuiltinAdmin(actor domain.User) error {
	if !actor.IsAdmin() {
		return &domain.PermissionError{Perm: "built-in administrator"}
	}
	return nil
}

func requireExtManage(actor domain.User) error {
	if actor.IsAdmin() || actor.Can(domain.PermExtensionsManage) {
		return nil
	}
	return &domain.PermissionError{Perm: domain.PermExtensionsManage}
}

// trustedKey resolves a publisher key that is not revoked.
func (s *ExtensionService) trustedKey(ctx context.Context) (func(string) (ed25519.PublicKey, bool), map[string]string, error) {
	pubs, err := s.Store.ListExtensionPublishers(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys := map[string]ed25519.PublicKey{}
	names := map[string]string{}
	for _, p := range pubs {
		names[p.KeyID] = p.Name
		if p.RevokedAtMS != nil {
			continue
		}
		if k, err := bundle.ParsePublicKey(p.PublicKey); err == nil {
			keys[p.KeyID] = k
		}
	}
	return func(id string) (ed25519.PublicKey, bool) { k, ok := keys[id]; return k, ok }, names, nil
}

// SyncBuiltins stores the bundles shipped in the binary as available
// versions, and moves installed built-ins to a newer shipped version when
// it asks for no capability beyond what was granted.
func (s *ExtensionService) SyncBuiltins(ctx context.Context) error {
	names := make([]string, 0, len(s.Builtins))
	for n := range s.Builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := s.Builtins[name]
		b, err := bundle.Read(raw)
		if err != nil {
			s.log().Error("built-in extension bundle is invalid", "file", name, "err", err)
			continue
		}
		m := b.Manifest
		cur, err := s.Store.GetExtensionVersion(ctx, m.ID, m.Version)
		if err == nil && cur.SHA256 == b.SHA256 {
			continue
		}
		mj, _ := json.Marshal(m)
		ext, extErr := s.Store.GetExtension(ctx, m.ID)
		protect := ""
		if extErr == nil {
			protect = ext.ActiveVersion
		}
		if err := s.Store.PutExtensionVersion(ctx, domain.ExtensionVersion{ExtensionID: m.ID, Version: m.Version, Source: domain.ExtSourceBuiltin,
			Bundle: raw, SHA256: b.SHA256, ManifestJSON: string(mj), CreatedAtMS: s.now()}, protect, extKeepVersions); err != nil {
			return err
		}
		if extErr != nil || ext.Source != domain.ExtSourceBuiltin || bundle.CompareVersions(m.Version, ext.ActiveVersion) <= 0 {
			continue
		}
		if extra := missingGrants(m, ext.Granted); len(extra) > 0 {
			s.log().Warn("a newer built-in extension needs new capabilities; an administrator must approve it", "extension", m.ID, "version", m.Version, "capabilities", extra)
			continue
		}
		if !s.compatible(m) {
			continue
		}
		ext.ActiveVersion, ext.UpdatedAtMS = m.Version, s.now()
		if err := s.Store.PutExtension(ctx, ext); err != nil {
			return err
		}
		s.unload(m.ID)
		s.log().Info("built-in extension updated", "extension", m.ID, "version", m.Version)
	}
	return nil
}

func (s *ExtensionService) compatible(m bundle.Manifest) bool {
	r, err := bundle.ParseRange(m.Compat.Panel)
	return err == nil && r.Allows(s.PanelVersion)
}

// missingGrants lists capabilities m requires (non-optional) that granted lacks.
func missingGrants(m bundle.Manifest, granted []string) []string {
	var out []string
	for _, c := range m.Capabilities {
		if !c.Optional && !slices.Contains(granted, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

// Upload validates and stores a bundle as an available version. Nothing
// runs until an administrator activates it.
func (s *ExtensionService) Upload(ctx context.Context, actor domain.User, raw []byte) (ExtensionView, error) {
	if err := requireBuiltinAdmin(actor); err != nil {
		return ExtensionView{}, err
	}
	b, err := bundle.Read(raw)
	if err != nil {
		return ExtensionView{}, domain.Invalid(err.Error())
	}
	source, keyID := domain.ExtSourceUpload, ""
	keyFor, _, err := s.trustedKey(ctx)
	if err != nil {
		return ExtensionView{}, err
	}
	switch id, verr := b.Verify(keyFor); {
	case verr == nil:
		keyID = id
	case errors.Is(verr, bundle.ErrUnsigned) && s.AllowUnsigned:
		source = domain.ExtSourceDev
	case errors.Is(verr, bundle.ErrUnsigned):
		return ExtensionView{}, domain.Invalid("this bundle is not signed; only signed bundles can be installed (unsigned ones load in development mode only)")
	default:
		return ExtensionView{}, domain.Invalid(verr.Error())
	}
	m := b.Manifest
	if v, err := s.Store.GetExtensionVersion(ctx, m.ID, m.Version); err == nil && v.Source == domain.ExtSourceBuiltin {
		return ExtensionView{}, domain.Invalid("version " + m.Version + " of " + m.ID + " ships with the panel and cannot be replaced by an upload")
	}
	protect := ""
	if ext, err := s.Store.GetExtension(ctx, m.ID); err == nil {
		protect = ext.ActiveVersion
		if ext.ActiveVersion == m.Version {
			return ExtensionView{}, domain.Invalid("version " + m.Version + " is active; upload a new version number")
		}
	}
	mj, _ := json.Marshal(m)
	if err := s.Store.PutExtensionVersion(ctx, domain.ExtensionVersion{ExtensionID: m.ID, Version: m.Version, Source: source, Bundle: raw,
		SHA256: b.SHA256, ManifestJSON: string(mj), KeyID: keyID, UploadedBy: actor.ID, CreatedAtMS: s.now()}, protect, extKeepVersions); err != nil {
		return ExtensionView{}, err
	}
	return s.view(ctx, m.ID)
}

// Activate installs a stored version (first install, upgrade or rollback)
// with the capabilities the administrator approved, and enables it.
func (s *ExtensionService) Activate(ctx context.Context, actor domain.User, id, version string, grants []string) (ExtensionView, error) {
	if err := requireBuiltinAdmin(actor); err != nil {
		return ExtensionView{}, err
	}
	v, err := s.Store.GetExtensionVersion(ctx, id, version)
	if err != nil {
		return ExtensionView{}, err
	}
	b, err := s.verifyStored(ctx, v)
	if err != nil {
		return ExtensionView{}, domain.Invalid(err.Error())
	}
	m := b.Manifest
	if !s.compatible(m) {
		return ExtensionView{}, domain.Invalid(fmt.Sprintf("this version needs panel %s; this panel is %s", m.Compat.Panel, s.PanelVersion))
	}
	var granted []string
	for _, g := range grants {
		if _, ok := m.Cap(g); !ok {
			return ExtensionView{}, domain.Invalid("capability " + g + " is not requested by this version")
		}
		if !slices.Contains(granted, g) {
			granted = append(granted, g)
		}
	}
	if missing := missingGrants(m, granted); len(missing) > 0 {
		return ExtensionView{}, domain.Invalid("these capabilities are required: " + strings.Join(missing, ", "))
	}
	sort.Strings(granted)
	now := s.now()
	ext, err := s.Store.GetExtension(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		ext = domain.Extension{ID: id, InstalledBy: actor.ID, CreatedAtMS: now}
	} else if err != nil {
		return ExtensionView{}, err
	}
	ext.Source, ext.ActiveVersion, ext.State, ext.Granted = v.Source, version, domain.ExtEnabled, granted
	ext.LastError, ext.FailureCount, ext.UpdatedAtMS = "", 0, now
	if err := s.Store.PutExtension(ctx, ext); err != nil {
		return ExtensionView{}, err
	}
	jobs := make([]string, 0, len(m.Jobs))
	for _, j := range m.Jobs {
		jobs = append(jobs, j.ID)
	}
	if err := s.Store.DeleteExtensionJobsExcept(ctx, id, jobs); err != nil {
		return ExtensionView{}, err
	}
	s.unload(id)
	s.warmPublic(context.WithoutCancel(ctx))
	return s.view(ctx, id)
}

// verifyStored re-checks a stored bundle before it runs: built-ins by their
// recorded hash, uploads by signature against keys that are still trusted.
func (s *ExtensionService) verifyStored(ctx context.Context, v domain.ExtensionVersion) (*bundle.Bundle, error) {
	b, err := bundle.Read(v.Bundle)
	if err != nil {
		return nil, err
	}
	if b.SHA256 != v.SHA256 || b.Manifest.ID != v.ExtensionID || b.Manifest.Version != v.Version {
		return nil, fmt.Errorf("the stored bundle does not match its record")
	}
	switch v.Source {
	case domain.ExtSourceBuiltin:
		return b, nil
	case domain.ExtSourceDev:
		if !s.AllowUnsigned {
			return nil, fmt.Errorf("unsigned extensions run only in development mode")
		}
		return b, nil
	}
	keyFor, _, err := s.trustedKey(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := b.Verify(keyFor); err != nil {
		return nil, fmt.Errorf("signature check failed: %w (was the publisher key revoked?)", err)
	}
	return b, nil
}

// SetEnabled turns an installed extension on or off. Turning on clears a
// failed state.
func (s *ExtensionService) SetEnabled(ctx context.Context, actor domain.User, id string, on bool) (ExtensionView, error) {
	if err := requireExtManage(actor); err != nil {
		return ExtensionView{}, err
	}
	ext, err := s.Store.GetExtension(ctx, id)
	if err != nil {
		return ExtensionView{}, err
	}
	state := domain.ExtDisabled
	if on {
		v, err := s.Store.GetExtensionVersion(ctx, id, ext.ActiveVersion)
		if err != nil {
			return ExtensionView{}, err
		}
		if _, err := s.verifyStored(ctx, v); err != nil {
			return ExtensionView{}, domain.Invalid(err.Error())
		}
		state = domain.ExtEnabled
	}
	if err := s.Store.SetExtensionState(ctx, id, state, "", 0, s.now()); err != nil {
		return ExtensionView{}, err
	}
	s.unload(id)
	if on {
		s.warmPublic(context.WithoutCancel(ctx))
	}
	return s.view(ctx, id)
}

// Configure changes settings. Secret values are sealed and write-only; a nil
// value clears a setting.
func (s *ExtensionService) Configure(ctx context.Context, actor domain.User, id string, values map[string]any) (ExtensionView, error) {
	if err := requireExtManage(actor); err != nil {
		return ExtensionView{}, err
	}
	l, err := s.manifestFor(ctx, id)
	if err != nil {
		return ExtensionView{}, err
	}
	now := s.now()
	for key, val := range values {
		def, ok := l.Setting(key)
		if !ok {
			return ExtensionView{}, domain.Invalid("unknown setting " + key)
		}
		if val == nil {
			if err := s.Store.DeleteExtensionSetting(ctx, id, key); err != nil {
				return ExtensionView{}, err
			}
			continue
		}
		st := domain.ExtensionSetting{Key: key}
		if def.Type == "secret" {
			text, ok := val.(string)
			if !ok || text == "" || len(text) > 4096 {
				return ExtensionView{}, domain.Invalid(def.Label + " must be text up to 4096 characters")
			}
			sealed, err := s.Keys.Seal("ext:"+id, key, []byte(text))
			if err != nil {
				return ExtensionView{}, err
			}
			st.SecretCipher, st.SecretNonce, st.SecretKeyID = sealed.Ciphertext, sealed.Nonce, sealed.KeyID
		} else {
			if err := checkSettingValue(def, val); err != nil {
				return ExtensionView{}, err
			}
			b, _ := json.Marshal(val)
			st.ValueJSON = string(b)
		}
		if err := s.Store.PutExtensionSetting(ctx, id, st, now); err != nil {
			return ExtensionView{}, err
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return s.view(ctx, id)
}

func checkSettingValue(def bundle.Setting, val any) error {
	switch def.Type {
	case "string":
		if t, ok := val.(string); !ok || len(t) > 2000 {
			return domain.Invalid(def.Label + " must be text up to 2000 characters")
		}
	case "number":
		if _, ok := val.(float64); !ok {
			return domain.Invalid(def.Label + " must be a number")
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return domain.Invalid(def.Label + " must be true or false")
		}
	case "enum":
		if t, ok := val.(string); !ok || !slices.Contains(def.Options, t) {
			return domain.Invalid(def.Label + " must be one of " + strings.Join(def.Options, ", "))
		}
	}
	return nil
}

// Uninstall removes an extension with all its data, after giving it up to
// ten seconds to clean up after itself.
func (s *ExtensionService) Uninstall(ctx context.Context, actor domain.User, id string) error {
	if err := requireBuiltinAdmin(actor); err != nil {
		return err
	}
	ext, err := s.Store.GetExtension(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		// Only uploaded versions, never installed: drop them.
		vs, lerr := s.Store.ListExtensionVersions(ctx, id)
		if lerr != nil || len(vs) == 0 {
			return domain.ErrNotFound
		}
		for _, v := range vs {
			if v.Source == domain.ExtSourceBuiltin {
				return domain.Invalid("built-in extensions cannot be removed from the panel; leave them uninstalled")
			}
		}
		if err := s.Store.DeleteExtension(ctx, id); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if ext.State == domain.ExtEnabled {
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = s.invoke(hctx, id, &invocation{system: true}, map[string]any{"kind": "uninstall"})
		cancel()
	}
	s.unload(id)
	if err := s.Store.DeleteExtension(ctx, id); err != nil {
		return err
	}
	// Built-in bundles stay available for a later reinstall.
	if s.builtinIDs()[id] {
		_ = s.SyncBuiltins(ctx)
	}
	return nil
}

func (s *ExtensionService) builtinIDs() map[string]bool {
	out := map[string]bool{}
	for _, raw := range s.Builtins {
		if b, err := bundle.Read(raw); err == nil {
			out[b.Manifest.ID] = true
		}
	}
	return out
}

// DeleteVersion removes a stored version that is not active.
func (s *ExtensionService) DeleteVersion(ctx context.Context, actor domain.User, id, version string) error {
	if err := requireBuiltinAdmin(actor); err != nil {
		return err
	}
	if ext, err := s.Store.GetExtension(ctx, id); err == nil && ext.ActiveVersion == version {
		return domain.Invalid("the active version cannot be deleted; activate another version or uninstall")
	}
	v, err := s.Store.GetExtensionVersion(ctx, id, version)
	if err != nil {
		return err
	}
	if v.Source == domain.ExtSourceBuiltin {
		return domain.Invalid("versions shipped with the panel cannot be deleted")
	}
	return s.Store.DeleteExtensionVersion(ctx, id, version)
}

// Publishers.

// ExtensionPublisherView is a trusted key.
type ExtensionPublisherView struct {
	KeyID       string `json:"key_id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	CreatedAtMS int64  `json:"created_at_ms"`
	RevokedAtMS *int64 `json:"revoked_at_ms,omitempty"`
}

// Publishers lists trusted keys.
func (s *ExtensionService) Publishers(ctx context.Context, actor domain.User) ([]ExtensionPublisherView, error) {
	if err := requireExtManage(actor); err != nil {
		return nil, err
	}
	ps, err := s.Store.ListExtensionPublishers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExtensionPublisherView, 0, len(ps))
	for _, p := range ps {
		out = append(out, ExtensionPublisherView{p.KeyID, p.Name, p.PublicKey, p.CreatedAtMS, p.RevokedAtMS})
	}
	return out, nil
}

// AddPublisher trusts a signing key.
func (s *ExtensionService) AddPublisher(ctx context.Context, actor domain.User, name, publicKey string) (ExtensionPublisherView, error) {
	if err := requireBuiltinAdmin(actor); err != nil {
		return ExtensionPublisherView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return ExtensionPublisherView{}, domain.Invalid("give the publisher a name (at most 80 characters)")
	}
	pub, err := bundle.ParsePublicKey(publicKey)
	if err != nil {
		return ExtensionPublisherView{}, domain.Invalid(err.Error())
	}
	p := domain.ExtensionPublisher{KeyID: bundle.KeyID(pub), Name: name, PublicKey: bundle.FormatPublicKey(pub), CreatedBy: actor.ID, CreatedAtMS: s.now()}
	if err := s.Store.InsertExtensionPublisher(ctx, p); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return ExtensionPublisherView{}, domain.Invalid("this key is already listed (revoked keys cannot be added again)")
		}
		return ExtensionPublisherView{}, err
	}
	return ExtensionPublisherView{p.KeyID, p.Name, p.PublicKey, p.CreatedAtMS, nil}, nil
}

// RevokePublisher distrusts a key and fails every extension running a
// version it signed.
func (s *ExtensionService) RevokePublisher(ctx context.Context, actor domain.User, keyID string) error {
	if err := requireBuiltinAdmin(actor); err != nil {
		return err
	}
	if err := s.Store.RevokeExtensionPublisher(ctx, keyID, s.now()); err != nil {
		return err
	}
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return err
	}
	for _, e := range exts {
		v, err := s.Store.GetExtensionVersion(ctx, e.ID, e.ActiveVersion)
		if err == nil && v.KeyID == keyID && e.State == domain.ExtEnabled {
			_ = s.Store.SetExtensionState(ctx, e.ID, domain.ExtFailed, "its publisher key was revoked", 0, s.now())
			s.unload(e.ID)
		}
	}
	return nil
}

// Catalog lists installed, uploaded and built-in extensions.
func (s *ExtensionService) Catalog(ctx context.Context, actor domain.User) ([]ExtensionView, error) {
	if err := requireExtManage(actor); err != nil {
		return nil, err
	}
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, e := range exts {
		ids[e.ID] = true
	}
	pending, err := s.Store.ListPendingExtensionIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range pending {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := make([]ExtensionView, 0, len(sorted))
	for _, id := range sorted {
		v, err := s.view(ctx, id)
		if err != nil {
			s.log().Warn("extension could not be described", "extension", id, "err", err)
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// Get describes one extension, as version would run ("" = as installed).
func (s *ExtensionService) Get(ctx context.Context, actor domain.User, id, version string) (ExtensionView, error) {
	if err := requireExtManage(actor); err != nil {
		return ExtensionView{}, err
	}
	return s.viewVersion(ctx, id, version)
}

func (s *ExtensionService) view(ctx context.Context, id string) (ExtensionView, error) {
	return s.viewVersion(ctx, id, "")
}

// viewVersion describes the extension as version would run ("": the active
// version, or the newest when not installed).
func (s *ExtensionService) viewVersion(ctx context.Context, id, version string) (ExtensionView, error) {
	vs, err := s.Store.ListExtensionVersions(ctx, id)
	if err != nil {
		return ExtensionView{}, err
	}
	if len(vs) == 0 {
		return ExtensionView{}, domain.ErrNotFound
	}
	ext, extErr := s.Store.GetExtension(ctx, id)
	installed := extErr == nil
	shown := vs[0] // newest, unless installed
	want := version
	if want == "" && installed {
		want = ext.ActiveVersion
	}
	found := want == ""
	for _, v := range vs {
		if v.Version == want {
			shown, found = v, true
		}
	}
	if !found {
		return ExtensionView{}, domain.ErrNotFound
	}
	var m bundle.Manifest
	if err := json.Unmarshal([]byte(shown.ManifestJSON), &m); err != nil {
		return ExtensionView{}, err
	}
	_, names, err := s.trustedKey(ctx)
	if err != nil {
		return ExtensionView{}, err
	}
	view := ExtensionView{ID: id, Name: m.Name, Description: m.Description, Publisher: m.Publisher, Homepage: m.Homepage,
		Version: m.Version, Source: shown.Source, Installed: installed, State: "available", Contributes: m.Contributes,
		Routes: m.Routes, Events: m.Events, MemoryMB: m.Runtime.MemoryMB, TimeoutMS: m.Runtime.TimeoutMS}
	if view.Routes == nil {
		view.Routes = []bundle.Route{}
	}
	if view.Events == nil {
		view.Events = []string{}
	}
	if installed {
		view.State, view.LastError = ext.State, ext.LastError
	}
	for _, c := range m.Capabilities {
		info := capInfo(c.Name)
		view.Capabilities = append(view.Capabilities, ExtensionCapabilityView{Name: c.Name, Label: info.Label, Area: info.Area, Guard: info.Guard, Reason: c.Reason,
			Optional: c.Optional, Hosts: c.Hosts, Granted: installed && slices.Contains(ext.Granted, c.Name)})
	}
	stored := map[string]domain.ExtensionSetting{}
	if installed {
		ss, err := s.Store.ListExtensionSettings(ctx, id)
		if err != nil {
			return ExtensionView{}, err
		}
		for _, st := range ss {
			stored[st.Key] = st
		}
	}
	view.Settings = []ExtensionSettingView{}
	for _, def := range m.Settings {
		sv := ExtensionSettingView{Setting: def}
		if st, ok := stored[def.Key]; ok {
			sv.Set = true
			if def.Type != "secret" && st.ValueJSON != "" {
				_ = json.Unmarshal([]byte(st.ValueJSON), &sv.Value)
			}
		}
		view.Settings = append(view.Settings, sv)
	}
	for _, v := range vs {
		var vm bundle.Manifest
		_ = json.Unmarshal([]byte(v.ManifestJSON), &vm)
		vv := ExtensionVersionView{Version: v.Version, Source: v.Source, KeyID: v.KeyID, Publisher: names[v.KeyID], SHA256: v.SHA256,
			CreatedAtMS: v.CreatedAtMS, Active: installed && v.Version == ext.ActiveVersion, Compatible: s.compatible(vm)}
		if installed {
			vv.NewCapabilities = missingGrants(vm, ext.Granted)
			if !vv.Active && bundle.CompareVersions(v.Version, ext.ActiveVersion) > 0 && (view.UpdateVersion == "" || bundle.CompareVersions(v.Version, view.UpdateVersion) > 0) {
				view.UpdateVersion = v.Version
			}
		}
		view.Versions = append(view.Versions, vv)
	}
	view.JobViews = []map[string]any{}
	if installed {
		jobs, err := s.Store.ListExtensionJobs(ctx, id)
		if err != nil {
			return ExtensionView{}, err
		}
		for _, j := range jobs {
			view.JobViews = append(view.JobViews, map[string]any{"job": j.Job, "next_run_ms": j.NextRunMS, "last_run_ms": j.LastRunMS, "last_status": j.LastStatus, "last_error": j.LastError})
		}
		bots, err := s.Store.ListExtensionBots(ctx, id)
		if err != nil {
			return ExtensionView{}, err
		}
		view.Bots = len(bots)
	}
	return view, nil
}

func capInfo(name string) bundle.CapabilityInfo {
	for _, c := range bundle.Capabilities {
		if c.Name == name {
			return c
		}
	}
	return bundle.CapabilityInfo{Name: name, Label: name, Area: "own"}
}

// manifestFor returns the active manifest of an installed extension.
func (s *ExtensionService) manifestFor(ctx context.Context, id string) (*bundle.Manifest, error) {
	ext, err := s.Store.GetExtension(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.Store.GetExtensionVersion(ctx, id, ext.ActiveVersion)
	if err != nil {
		return nil, err
	}
	var m bundle.Manifest
	if err := json.Unmarshal([]byte(v.ManifestJSON), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// load returns the running form of an enabled extension, verifying the
// stored bundle and compiling it on first use.
func (s *ExtensionService) load(ctx context.Context, id string) (*loadedExt, error) {
	if s.SafeMode {
		return nil, domain.Invalid("extensions are not loaded: the panel runs in extension safe mode")
	}
	s.mu.Lock()
	if s.loaded == nil {
		s.loaded = map[string]*loadedExt{}
	}
	l := s.loaded[id]
	s.mu.Unlock()
	if l == nil {
		ext, err := s.Store.GetExtension(ctx, id)
		if err != nil {
			return nil, err
		}
		if ext.State != domain.ExtEnabled {
			return nil, domain.ErrNotFound
		}
		v, err := s.Store.GetExtensionVersion(ctx, id, ext.ActiveVersion)
		if err != nil {
			return nil, err
		}
		b, err := s.verifyStored(ctx, v)
		if err != nil {
			_ = s.Store.SetExtensionState(ctx, id, domain.ExtFailed, err.Error(), 0, s.now())
			return nil, domain.Invalid("extension failed its integrity check: " + err.Error())
		}
		l = &loadedExt{ext: ext, man: b.Manifest, files: b.Files}
		s.mu.Lock()
		if cur := s.loaded[id]; cur != nil {
			l = cur
		} else {
			s.loaded[id] = l
		}
		s.mu.Unlock()
	}
	l.compiled.Do(func() {
		m := l.man
		l.mod, l.err = extrt.Compile(context.Background(), m.ID+"@"+m.Version, l.files[bundle.WasmFile], extrt.Limits{
			MemoryMB: m.Runtime.MemoryMB, Timeout: time.Duration(m.Runtime.TimeoutMS) * time.Millisecond,
		}, s.log())
		if l.err != nil {
			_ = s.Store.SetExtensionState(context.Background(), id, domain.ExtFailed, l.err.Error(), 0, s.now())
		}
	})
	if l.err != nil {
		s.unload(id)
		return nil, domain.Invalid("extension could not start: " + l.err.Error())
	}
	return l, nil
}

// warmPublic compiles, in the background, enabled extensions that answer
// requests from outside (Discord gives an interaction three seconds, less
// than a cold compile can take).
func (s *ExtensionService) warmPublic(ctx context.Context) {
	if s.SafeMode {
		return
	}
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return
	}
	for _, e := range exts {
		if e.State != domain.ExtEnabled {
			continue
		}
		m, err := s.manifestFor(ctx, e.ID)
		if err != nil {
			continue
		}
		for _, r := range m.Routes {
			if r.Auth == "public" {
				id := e.ID
				go func() { _, _ = s.load(context.Background(), id) }()
				break
			}
		}
	}
}

// unloadIdle frees the compiled code of extensions nobody used for a while
// and returns the memory to the operating system.
func (s *ExtensionService) unloadIdle() {
	var idle []string
	s.mu.Lock()
	for id, l := range s.loaded {
		if l.mod == nil || time.Since(l.lastUsed) < extIdleUnload {
			continue
		}
		public := false
		for _, r := range l.man.Routes {
			public = public || r.Auth == "public"
		}
		if !public {
			idle = append(idle, id)
		}
	}
	s.mu.Unlock()
	for _, id := range idle {
		s.mu.Lock()
		l := s.loaded[id]
		delete(s.loaded, id)
		s.mu.Unlock()
		if l != nil && l.mod != nil {
			_ = l.mod.Close(context.Background())
		}
	}
	if len(idle) > 0 {
		debug.FreeOSMemory()
		s.log().Debug("unloaded idle extensions", "extensions", idle)
	}
}

// Invocations counts calls into an extension's code since it was loaded
// (tests, diagnostics).
func (s *ExtensionService) Invocations(id string) int64 {
	s.mu.Lock()
	l := s.loaded[id]
	s.mu.Unlock()
	if l == nil || l.mod == nil {
		return 0
	}
	return l.mod.Invocations()
}

func (s *ExtensionService) unload(id string) {
	s.mu.Lock()
	l := s.loaded[id]
	delete(s.loaded, id)
	s.mu.Unlock()
	if l != nil && l.mod != nil {
		go l.mod.Close(context.Background())
	}
}

// invoke runs one request through an enabled extension and tracks crashes:
// after extMaxFailures in a row it is marked failed and its managers told.
func (s *ExtensionService) invoke(ctx context.Context, id string, inv *invocation, req map[string]any) (map[string]json.RawMessage, error) {
	l, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	inv.svc, inv.ext = s, l
	s.mu.Lock()
	l.lastUsed = time.Now()
	s.mu.Unlock()
	if inv.user != nil {
		req["user"] = map[string]any{"id": inv.user.ID, "name": userName(*inv.user), "admin": inv.user.IsAdmin()}
	}
	raw, _ := json.Marshal(req)
	out, err := l.mod.Invoke(ctx, raw, inv.call)
	if err != nil {
		if errors.Is(err, extrt.ErrBusy) {
			return nil, domain.Invalid("the extension is busy; try again in a moment")
		}
		s.fail(id, l, err)
		return nil, domain.Invalid("the extension failed: " + err.Error())
	}
	s.mu.Lock()
	l.failures = 0
	s.mu.Unlock()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(out, &resp); err != nil {
		s.fail(id, l, err)
		return nil, domain.Invalid("the extension returned an unreadable answer")
	}
	return resp, nil
}

func (s *ExtensionService) fail(id string, l *loadedExt, err error) {
	s.mu.Lock()
	l.failures++
	n := l.failures
	s.mu.Unlock()
	s.log().Warn("extension invocation failed", "extension", id, "err", err, "consecutive", n)
	if n < extMaxFailures {
		return
	}
	msg := fmt.Sprintf("stopped after %d failures in a row; last: %v", n, err)
	_ = s.Store.SetExtensionState(context.Background(), id, domain.ExtFailed, msg, n, s.now())
	s.unload(id)
	if s.Notices != nil {
		s.Notices.deliver(context.Background(), s.Notices.HoldersOf(context.Background(), domain.PermExtensionsManage), Notice{
			Category: domain.NotifyExtensions, Title: "Extension " + l.man.Name + " stopped", Body: msg, Link: "/admin/extensions"})
	}
}

func userName(u domain.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Email
}

func (s *ExtensionService) record(ctx context.Context, actor domain.User, action, target string, meta map[string]string) {
	if s.Audit == nil {
		return
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := target
	for _, k := range keys {
		if meta[k] != "" {
			t += " " + k + "=" + meta[k]
		}
	}
	t = clipService(t, 300)
	label := userName(actor)
	e := domain.AuditEvent{ActorID: &actor.ID, ActorLabel: &label, Action: action, Target: &t}
	if strings.HasPrefix(action, "extension.bot_") {
		e.BotID = &target
	}
	s.Audit.Record(ctx, e)
}

// Routes.

// RouteResult is a route's HTTP answer.
type RouteResult struct {
	Status      int
	ContentType string
	Body        []byte
}

// ExtensionRouteRequest carries an incoming HTTP request to an extension route.
type ExtensionRouteRequest struct {
	Auth    string // user | bot | public
	BotID   string
	Method  string
	Path    string
	Query   map[string]string
	Headers map[string]string
	Body    []byte
}

// HandleRoute serves a declared route. Authorization happens here, before
// any extension code runs: the user's role permission for user routes, the
// per-bot permission for bot routes, and the request signature for public ones.
func (s *ExtensionService) HandleRoute(ctx context.Context, actor *domain.User, id string, r ExtensionRouteRequest) (RouteResult, error) {
	m, err := s.manifestFor(ctx, id)
	if err != nil {
		return RouteResult{}, domain.ErrNotFound
	}
	var route *bundle.Route
	for i := range m.Routes {
		rt := m.Routes[i]
		if rt.Auth == r.Auth && rt.Method == r.Method && rt.Path == r.Path {
			route = &rt
		}
	}
	if route == nil {
		return RouteResult{}, domain.ErrNotFound
	}
	max := route.MaxBody
	if max == 0 {
		max = 64 << 10
	}
	if len(r.Body) > max {
		return RouteResult{}, domain.Invalid("request body is too large")
	}
	inv := &invocation{}
	switch r.Auth {
	case "user":
		if actor == nil {
			return RouteResult{}, domain.ErrUnauthorized
		}
		if p := m.ResolvePerm(route.Perm); p != "" && !actor.Can(p) {
			return RouteResult{}, domain.Denied(*actor, p)
		}
		inv.user = actor
	case "bot":
		if actor == nil {
			return RouteResult{}, domain.ErrUnauthorized
		}
		if _, err := s.Bots.loadPerm(ctx, *actor, r.BotID, bundle.BotPerms[route.BotPerm], false); err != nil {
			return RouteResult{}, err
		}
		inv.user, inv.botID = actor, r.BotID
	case "public":
		if err := s.verifyPublic(ctx, id, route, r); err != nil {
			s.log().Info("extension public request refused", "extension", id, "route", route.ID, "err", err)
			return RouteResult{Status: http.StatusUnauthorized, ContentType: "application/json", Body: []byte(`{"error":"invalid request signature"}`)}, nil
		}
		inv.system = true
	}
	// Public endpoints have their own budget, so heavy use of an extension's
	// pages can never crowd out the requests other services send it.
	if !s.allow("route:"+route.Auth+":"+id, 600) {
		return RouteResult{}, domain.Invalid("the extension is receiving too many requests")
	}
	resp, err := s.invoke(ctx, id, inv, map[string]any{"kind": "route", "route": route.ID, "method": r.Method, "path": r.Path,
		"query": r.Query, "headers": r.Headers, "body": r.Body, "bot_id": r.BotID})
	if err != nil {
		return RouteResult{}, err
	}
	if route.Mutating && actor != nil {
		s.record(ctx, *actor, "extension.route", id, map[string]string{"route": route.ID, "bot": r.BotID})
	}
	return routeResult(resp), nil
}

func routeResult(resp map[string]json.RawMessage) RouteResult {
	var status int
	_ = json.Unmarshal(resp["status"], &status)
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	var ct, text string
	_ = json.Unmarshal(resp["content_type"], &ct)
	_ = json.Unmarshal(resp["text"], &text)
	switch {
	case text != "":
		// Only harmless types: an extension never serves HTML into the panel origin.
		if ct != "text/plain" && ct != "text/csv" {
			ct = "text/plain"
		}
		return RouteResult{Status: status, ContentType: ct + "; charset=utf-8", Body: []byte(text)}
	case len(resp["body"]) > 0:
		return RouteResult{Status: status, ContentType: "application/json", Body: resp["body"]}
	}
	return RouteResult{Status: status}
}

var discordTSRe = regexp.MustCompile(`^[0-9]{1,12}$`)

// verifyPublic checks a public route's signature before any code runs.
func (s *ExtensionService) verifyPublic(ctx context.Context, id string, route *bundle.Route, r ExtensionRouteRequest) error {
	if route.Verify == nil {
		return errors.New("route has no verification")
	}
	key, err := s.settingText(ctx, id, route.Verify.KeySetting)
	if err != nil || key == "" {
		return errors.New("the verification key is not configured")
	}
	switch route.Verify.Scheme {
	case "discord-ed25519":
		pub, err := hex.DecodeString(strings.TrimSpace(key))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("the configured public key is not a hex Ed25519 key")
		}
		sig, err := hex.DecodeString(r.Headers["x-signature-ed25519"])
		ts := r.Headers["x-signature-timestamp"]
		if err != nil || len(sig) != ed25519.SignatureSize || !discordTSRe.MatchString(ts) {
			return errors.New("missing signature headers")
		}
		sec, _ := strconv.ParseInt(ts, 10, 64)
		if d := time.Since(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
			return errors.New("stale timestamp")
		}
		if !ed25519.Verify(pub, append([]byte(ts), r.Body...), sig) {
			return errors.New("bad signature")
		}
		// The timestamp bounds acceptance to ten minutes; remember it that long.
		return s.once(id, "ed25519:"+hex.EncodeToString(sig), 10*time.Minute)
	case "hmac-sha256":
		got := strings.TrimPrefix(r.Headers["x-signature"], "sha256=")
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(r.Body)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(got), []byte(want)) {
			return errors.New("bad signature")
		}
		// The signature covers only the body, so an identical delivery is
		// refused for a day; senders put a timestamp or id in the body.
		return s.once(id, "hmac:"+want, 24*time.Hour)
	default:
		return errors.New("unknown scheme")
	}
}

// maxSeen bounds the replay cache; at the per-address rate limit it is
// reached only under a flood, and then the oldest entries go first.
const maxSeen = 100_000

// once accepts a verified signature the first time only, within keep.
func (s *ExtensionService) once(id, sig string, keep time.Duration) error {
	now := time.Now()
	k := id + "\x00" + sig
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	if until, ok := s.seen[k]; ok && now.Before(until) {
		return errors.New("this request was already received")
	}
	if len(s.seen) >= maxSeen {
		var oldK string
		var oldT time.Time
		for sk, t := range s.seen {
			if t.Before(now) {
				delete(s.seen, sk)
			} else if oldK == "" || t.Before(oldT) {
				oldK, oldT = sk, t
			}
		}
		if len(s.seen) >= maxSeen {
			delete(s.seen, oldK)
		}
	}
	s.seen[k] = now.Add(keep)
	return nil
}

// settingText returns a setting's value as text (secrets opened); only the
// host uses it, never an extension.
func (s *ExtensionService) settingText(ctx context.Context, id, key string) (string, error) {
	ss, err := s.Store.ListExtensionSettings(ctx, id)
	if err != nil {
		return "", err
	}
	for _, st := range ss {
		if st.Key != key {
			continue
		}
		if st.SecretCipher != nil {
			pt, err := s.Keys.Open("ext:"+id, key, secrets.Sealed{Ciphertext: st.SecretCipher, Nonce: st.SecretNonce, KeyID: st.SecretKeyID})
			return string(pt), err
		}
		var v any
		if err := json.Unmarshal([]byte(st.ValueJSON), &v); err != nil {
			return "", err
		}
		if t, ok := v.(string); ok {
			return t, nil
		}
		return fmt.Sprint(v), nil
	}
	return "", nil
}

// User-facing contributions.

// ExtensionContribution is where enabled extensions appear for one user.
type ExtensionContribution struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Publisher string              `json:"publisher"`
	Version   string              `json:"version"`
	Source    string              `json:"source"`
	Nav       []bundle.NavItem    `json:"nav"`
	BotTabs   []bundle.BotTab     `json:"bot_tabs"`
	BotCards  []ExtensionCardView `json:"bot_cards"`
	Consent   *bundle.UserConsent `json:"consent,omitempty"`
	Consented []string            `json:"consented"`
	BotEnable string              `json:"bot_enablement,omitempty"`
	Settings  map[string]any      `json:"-"`
}

// ExtensionCardView is a bot overview card with the path of its route.
type ExtensionCardView struct {
	bundle.BotCard
	Path string `json:"path"`
}

// Contributions lists what enabled extensions add to the interface for actor.
func (s *ExtensionService) Contributions(ctx context.Context, actor domain.User) ([]ExtensionContribution, error) {
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return nil, err
	}
	out := []ExtensionContribution{}
	for _, e := range exts {
		if e.State != domain.ExtEnabled || s.SafeMode {
			continue
		}
		m, err := s.manifestFor(ctx, e.ID)
		if err != nil {
			continue
		}
		c := ExtensionContribution{ID: e.ID, Name: m.Name, Publisher: m.Publisher, Version: m.Version, Source: e.Source,
			Nav: []bundle.NavItem{}, BotTabs: m.Contributes.BotTabs, BotCards: []ExtensionCardView{},
			Consent: m.Contributes.UserConsent, Consented: []string{}, BotEnable: m.Contributes.BotEnablement}
		for _, n := range m.Contributes.Nav {
			if mayUse(actor, m, n.Perm) {
				// The resolved name ("ext:<id>" for "extension") is what a role grants.
				n.Perm = m.ResolvePerm(n.Perm)
				c.Nav = append(c.Nav, n)
			}
		}
		if c.BotTabs == nil {
			c.BotTabs = []bundle.BotTab{}
		}
		for _, bc := range m.Contributes.BotCards {
			if r, ok := m.Route(bc.Route); ok {
				c.BotCards = append(c.BotCards, ExtensionCardView{bc, r.Path})
			}
		}
		if c.Consent != nil {
			if sc, err := s.Store.GetExtensionConsent(ctx, e.ID, actor.ID); err == nil {
				c.Consented = sc
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// PermissionCatalog lists the per-extension permissions for the roles
// editor: one "Use <name>" entry per installed extension (enabled, disabled
// or failed), sorted by name, so a role can be prepared before the extension
// is switched on. Only extensions that mark pages or routes with
// "perm": "extension" make the permission matter; it is offered for every
// installed one so the list does not depend on manifest details.
func (s *ExtensionService) PermissionCatalog(ctx context.Context) ([]domain.PermissionInfo, error) {
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.PermissionInfo{}
	for _, e := range exts {
		name := e.ID
		if m, err := s.manifestFor(ctx, e.ID); err == nil && strings.TrimSpace(m.Name) != "" {
			name = m.Name
		}
		out = append(out, domain.PermissionInfo{Name: domain.ExtensionPermission(e.ID), Group: domain.PermGroupExtensions,
			Label: "Use " + name, Description: "See and use the pages of the " + name + " extension."})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
	return out, nil
}

// SetConsent records which scopes the user lets an extension use on their
// behalf (an empty list withdraws consent).
func (s *ExtensionService) SetConsent(ctx context.Context, actor domain.User, id string, scopes []string) error {
	if actor.Client != nil {
		return &domain.PermissionError{Perm: "a signed-in session", Client: true}
	}
	m, err := s.manifestFor(ctx, id)
	if err != nil {
		return err
	}
	if m.Contributes.UserConsent == nil {
		return domain.Invalid("this extension does not act on behalf of users")
	}
	var keep []string
	for _, sc := range scopes {
		ok := false
		for _, def := range m.Contributes.UserConsent.Scopes {
			ok = ok || def.ID == sc
		}
		if !ok {
			return domain.Invalid("unknown scope " + sc)
		}
		if !slices.Contains(keep, sc) {
			keep = append(keep, sc)
		}
	}
	sort.Strings(keep)
	if err := s.Store.SetExtensionConsent(ctx, id, actor.ID, keep, s.now()); err != nil {
		return err
	}
	return nil
}

// BotExtension is an extension's switch on one bot.
type BotExtension struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Explanation string `json:"explanation"`
	Enabled     bool   `json:"enabled"`
}

// BotExtensions lists extensions that can be switched on per bot.
func (s *ExtensionService) BotExtensions(ctx context.Context, actor domain.User, botID string) ([]BotExtension, error) {
	if _, err := s.Bots.Get(ctx, actor, botID); err != nil {
		return nil, err
	}
	cs, err := s.Contributions(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := []BotExtension{}
	for _, c := range cs {
		if c.BotEnable == "" {
			continue
		}
		on, err := s.Store.ExtensionBotEnabled(ctx, c.ID, botID)
		if err != nil {
			return nil, err
		}
		out = append(out, BotExtension{c.ID, c.Name, c.BotEnable, on})
	}
	return out, nil
}

// SetBotEnabled switches an extension on or off for a bot (needs the bot's
// environment permission, since extensions may use the bot's own token).
func (s *ExtensionService) SetBotEnabled(ctx context.Context, actor domain.User, id, botID string, on bool) error {
	if !actor.Can(domain.PermBotsEnv) {
		return &domain.PermissionError{Perm: domain.PermBotsEnv}
	}
	if _, err := s.Bots.Authorize(ctx, actor, botID, domain.PermManageEnv); err != nil {
		return err
	}
	m, err := s.manifestFor(ctx, id)
	if err != nil {
		return err
	}
	if m.Contributes.BotEnablement == "" {
		return domain.Invalid("this extension is not switched on per bot")
	}
	if err := s.Store.SetExtensionBotEnabled(ctx, id, botID, actor.ID, on, s.now()); err != nil {
		return err
	}
	return nil
}

// mayUse reports whether actor holds a nav item's or user route's declared
// perm ("" needs nothing; "extension" is the extension's own ext:<id>).
func mayUse(actor domain.User, m *bundle.Manifest, perm string) bool {
	p := m.ResolvePerm(perm)
	return p == "" || actor.Can(p)
}

// pageDenied applies the nav items' permissions to the page they open. Pages
// carry no permission of their own: a page that one or more nav items open
// is served only to an account that may see at least one of them (the same
// rule Contributions applies to the links). A page a bot tab opens, or one no
// nav item opens, is served to any signed-in account, as bot tabs are
// checked per bot by their routes. The returned error names a permission the
// account lacks, or is nil.
func pageDenied(actor domain.User, m *bundle.Manifest, page string) error {
	for _, t := range m.Contributes.BotTabs {
		if t.Page == page {
			return nil
		}
	}
	var missing string
	for _, n := range m.Contributes.Nav {
		if n.Page != page {
			continue
		}
		if mayUse(actor, m, n.Perm) {
			return nil
		}
		if missing == "" {
			missing = m.ResolvePerm(n.Perm)
		}
	}
	if missing != "" {
		return domain.Denied(actor, missing)
	}
	return nil
}

// UIPage returns a custom page with the hashes of its inline scripts, for
// the sandbox Content-Security-Policy. A page behind a nav item needs that
// nav item's permission (see pageDenied).
func (s *ExtensionService) UIPage(ctx context.Context, actor domain.User, id, page string) ([]byte, []string, error) {
	l, err := s.loadManifestFiles(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	p, ok := l.man.Page(page)
	if !ok {
		return nil, nil, domain.ErrNotFound
	}
	if err := pageDenied(actor, &l.man, page); err != nil {
		return nil, nil, err
	}
	html := l.files[p.File]
	return html, inlineScriptHashes(html), nil
}

// loadManifestFiles reads an enabled extension's files without compiling it.
func (s *ExtensionService) loadManifestFiles(ctx context.Context, id string) (*loadedExt, error) {
	s.mu.Lock()
	l := s.loaded[id]
	s.mu.Unlock()
	if l != nil {
		return l, nil
	}
	ext, err := s.Store.GetExtension(ctx, id)
	if err != nil || ext.State != domain.ExtEnabled || s.SafeMode {
		return nil, domain.ErrNotFound
	}
	v, err := s.Store.GetExtensionVersion(ctx, id, ext.ActiveVersion)
	if err != nil {
		return nil, err
	}
	b, err := s.verifyStored(ctx, v)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	return &loadedExt{ext: ext, man: b.Manifest, files: b.Files}, nil
}

var scriptRe = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)

// inlineScriptHashes returns CSP 'sha256-…' sources for every inline script.
func inlineScriptHashes(html []byte) []string {
	var out []string
	for _, m := range scriptRe.FindAllSubmatch(html, 64) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

// Background work: scheduled jobs and events.

// Run schedules jobs and delivers events until ctx ends.
func (s *ExtensionService) Run(ctx context.Context) {
	if s.SafeMode {
		return
	}
	var sub *events.Sub
	var evc chan events.Status
	if s.Bus != nil {
		sub = s.Bus.SubscribeAll()
		defer sub.Close()
		evc = sub.C
	}
	s.warmPublic(ctx)
	queue := make(chan func(), 64)
	go func() {
		for f := range queue {
			f()
		}
	}()
	defer close(queue)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	s.runDueJobs(ctx, queue)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runDueJobs(ctx, queue)
			s.unloadIdle()
		case st, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			s.deliverStatus(ctx, st, queue)
		}
	}
}

func (s *ExtensionService) runDueJobs(ctx context.Context, queue chan<- func()) {
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return
	}
	jobs, err := s.Store.ListExtensionJobs(ctx, "")
	if err != nil {
		return
	}
	known := map[string]domain.ExtensionJob{}
	for _, j := range jobs {
		known[j.ExtensionID+"/"+j.Job] = j
	}
	now := time.Now()
	for _, e := range exts {
		if e.State != domain.ExtEnabled {
			continue
		}
		m, err := s.manifestFor(ctx, e.ID)
		if err != nil {
			continue
		}
		for _, j := range m.Jobs {
			every, _ := time.ParseDuration(j.Every)
			rec, ok := known[e.ID+"/"+j.ID]
			if !ok {
				// First sight: run soon, spread out so a restart is not a stampede.
				rec = domain.ExtensionJob{ExtensionID: e.ID, Job: j.ID, NextRunMS: now.Add(time.Duration(rand.Int64N(int64(time.Minute)))).UnixMilli()}
				_ = s.Store.PutExtensionJob(ctx, rec)
				continue
			}
			if rec.NextRunMS > now.UnixMilli() {
				continue
			}
			jitter := time.Duration(rand.Int64N(int64(every / 10)))
			rec.NextRunMS = now.Add(every + jitter).UnixMilli()
			rec.LastRunMS, rec.LastStatus, rec.LastError = now.UnixMilli(), "running", ""
			_ = s.Store.PutExtensionJob(ctx, rec)
			id, job, timeout := e.ID, j, j.TimeoutMS
			select {
			case queue <- func() { s.runJob(ctx, id, job.ID, timeout, rec) }:
			default:
				rec.LastStatus, rec.LastError = "skipped", "the job queue is full"
				_ = s.Store.PutExtensionJob(ctx, rec)
			}
		}
	}
}

func (s *ExtensionService) runJob(ctx context.Context, id, job string, timeoutMS int, rec domain.ExtensionJob) {
	if timeoutMS <= 0 {
		timeoutMS = 60000
	}
	jctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	resp, err := s.invoke(jctx, id, &invocation{system: true}, map[string]any{"kind": "job", "job": job})
	rec.LastStatus, rec.LastError = "ok", ""
	if err != nil {
		rec.LastStatus, rec.LastError = "failed", clipService(err.Error(), 300)
	} else if st := routeResult(resp).Status; st >= 400 {
		rec.LastStatus, rec.LastError = "failed", clipService(string(resp["body"]), 300)
	}
	_ = s.Store.PutExtensionJob(context.Background(), rec)
}

// RunJobNow runs a scheduled job immediately (administrators testing a job).
func (s *ExtensionService) RunJobNow(ctx context.Context, actor domain.User, id, job string) error {
	if err := requireExtManage(actor); err != nil {
		return err
	}
	m, err := s.manifestFor(ctx, id)
	if err != nil {
		return err
	}
	for _, j := range m.Jobs {
		if j.ID == job {
			rec := domain.ExtensionJob{ExtensionID: id, Job: job, NextRunMS: time.Now().Add(mustDuration(j.Every)).UnixMilli(), LastRunMS: s.now()}
			s.runJob(ctx, id, job, j.TimeoutMS, rec)
			return nil
		}
	}
	return domain.ErrNotFound
}

func mustDuration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

// deliverStatus hands a bot state change to extensions that subscribed and
// are switched on for that bot. Delivery is at most once; a full queue drops.
func (s *ExtensionService) deliverStatus(ctx context.Context, st events.Status, queue chan<- func()) {
	exts, err := s.Store.ListExtensions(ctx)
	if err != nil {
		return
	}
	for _, e := range exts {
		if e.State != domain.ExtEnabled {
			continue
		}
		m, err := s.manifestFor(ctx, e.ID)
		if err != nil || !slices.Contains(m.Events, "bot.status") {
			continue
		}
		if on, _ := s.Store.ExtensionBotEnabled(ctx, e.ID, st.BotID); !on {
			continue
		}
		id := e.ID
		data, _ := json.Marshal(map[string]any{"bot_id": st.BotID, "desired_state": st.DesiredState, "observed_state": st.ObservedState, "last_error": st.LastError, "reason": st.Reason})
		select {
		case queue <- func() {
			ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			_, _ = s.invoke(ectx, id, &invocation{system: true}, map[string]any{"kind": "event", "event": "bot.status", "bot_id": st.BotID, "data": json.RawMessage(data)})
		}:
		default:
			s.log().Debug("extension event dropped: queue full", "extension", id)
		}
	}
}

// rateWindow is a fixed one-minute counter.
type rateWindow struct {
	start time.Time
	n     int
}

func (s *ExtensionService) allow(key string, perMinute int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rate == nil {
		s.rate = map[string]*rateWindow{}
	}
	w := s.rate[key]
	if w == nil || time.Since(w.start) > time.Minute {
		w = &rateWindow{start: time.Now()}
		s.rate[key] = w
	}
	w.n++
	return w.n <= perMinute
}
