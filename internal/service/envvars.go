package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/config"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// PanelEnvStore is the persistence the environment editor needs.
type PanelEnvStore interface {
	EnvOverrides(ctx context.Context) (map[string]domain.Setting, error)
	PutEnvOverrides(ctx context.Context, set []domain.Setting, remove []string, by string, nowMS int64) error
}

// PanelEnvService lets an administrator change the panel's RIVET_* variables from
// the browser. The values are stored in the database and layered over the
// process environment the next time the panel starts; the environment file
// itself is never written (the systemd unit mounts it read-only, and a
// container's env file lives outside the container).
//
// Precedence at start: panel override > process environment > built-in default.
// An override may be empty, which switches off something the environment turns
// on. Variables read before the database opens, and the ones Panel settings
// owns, are never overridden.
type PanelEnvService struct {
	Store PanelEnvStore
	Keys  *secrets.Keyring
	Base  config.Lookup // the process environment
	Log   *slog.Logger
	Now   func() time.Time

	// CanRestart is true when a supervisor (systemd, a Docker restart policy)
	// will start the panel again after it exits. Restart does the exiting.
	CanRestart bool
	Restart    func()

	mu       sync.Mutex
	running  map[string]string // effective value of every variable when the panel started
	bootErr  string            // why stored overrides were ignored at start, if they were
	started  time.Time
	unreadOK []string // overrides that could not be decrypted at start
}

func (s *PanelEnvService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Overrides returns the stored overrides that apply: known, editable variables
// with readable values. Names whose sealed value cannot be decrypted with the
// loaded keys are returned separately so the page can say so.
func (s *PanelEnvService) Overrides(ctx context.Context) (applied map[string]string, unreadable []string, err error) {
	stored, err := s.Store.EnvOverrides(ctx)
	if err != nil {
		return nil, nil, err
	}
	applied = map[string]string{}
	for name, st := range stored {
		spec, ok := config.Spec(name)
		if !ok || !spec.Editable() {
			continue // a row left by another build, or edited by hand
		}
		if st.Cipher != nil {
			pt, err := s.Keys.Open("env", name, secrets.Sealed{Ciphertext: st.Cipher, Nonce: st.Nonce, KeyID: st.KeyID})
			if err != nil {
				unreadable = append(unreadable, name)
				continue
			}
			applied[name] = string(pt)
			continue
		}
		applied[name] = st.Value
	}
	sort.Strings(unreadable)
	return applied, unreadable, nil
}

// Started records the effective configuration the panel is now running with.
// Pass the overrides that were applied (nil when they were rejected) and the
// reason they were rejected.
func (s *PanelEnvService) Started(applied map[string]string, rejected string, unreadable []string) {
	look := config.Overlay(s.Base, applied)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = map[string]string{}
	for _, v := range config.Vars {
		val, _ := look(v.Name)
		s.running[v.Name] = val
	}
	s.bootErr, s.unreadOK, s.started = rejected, unreadable, s.now()
}

// PanelEnvVar is one variable as the page shows it. Secret values are never
// returned: Set says whether one is in effect.
type PanelEnvVar struct {
	config.VarSpec
	Editable bool   `json:"editable"`
	Secret   bool   `json:"secret"`
	Source   string `json:"source"`    // default, environment or panel
	Value    string `json:"value"`     // effective value ("" for secrets)
	Set      bool   `json:"set"`       // an effective value exists
	EnvValue string `json:"env_value"` // what the process environment says ("" for secrets)
	EnvSet   bool   `json:"env_set"`   // the process environment sets it
	Override bool   `json:"override"`  // the panel stores an override
	Pending  bool   `json:"pending"`   // differs from what is running now
	Running  string `json:"running"`   // value in use now ("" for secrets)
}

// PanelEnvView is the whole page.
type PanelEnvView struct {
	Vars       []PanelEnvVar `json:"vars"`
	Groups     []string      `json:"groups"`
	Pending    int           `json:"pending"`
	CanRestart bool          `json:"can_restart"`
	// Rejected is why stored overrides were ignored at the last start, so a
	// bad combination never stops the panel from booting.
	Rejected   string   `json:"rejected,omitempty"`
	Unreadable []string `json:"unreadable,omitempty"`
	StartedAt  int64    `json:"started_at_ms"`
}

// View builds the page for an administrator.
func (s *PanelEnvService) View(ctx context.Context, actor domain.User) (PanelEnvView, error) {
	if !actor.IsAdmin() {
		return PanelEnvView{}, domain.ErrForbidden
	}
	stored, err := s.Store.EnvOverrides(ctx)
	if err != nil {
		return PanelEnvView{}, err
	}
	applied, unreadable, err := s.Overrides(ctx)
	if err != nil {
		return PanelEnvView{}, err
	}
	look := config.Overlay(s.Base, applied)
	s.mu.Lock()
	running, rejected, started := s.running, s.bootErr, s.started
	s.mu.Unlock()

	out := PanelEnvView{Groups: config.Groups, CanRestart: s.CanRestart && s.Restart != nil, Rejected: rejected, Unreadable: unreadable}
	if !started.IsZero() {
		out.StartedAt = started.UnixMilli()
	}
	for _, spec := range config.Vars {
		eff, _ := look(spec.Name)
		envV, envOK := s.Base(spec.Name)
		envSet := envOK && strings.TrimSpace(envV) != ""
		_, hasRow := stored[spec.Name]
		over := hasRow && spec.Editable()
		v := PanelEnvVar{VarSpec: spec, Editable: spec.Editable(), Secret: spec.Secret(), Override: over, EnvSet: envSet, Set: eff != ""}
		switch {
		case over:
			v.Source = "panel"
		case envSet:
			v.Source = "environment"
		default:
			v.Source = "default"
		}
		if spec.Managed != "" {
			// Another page owns these; the environment still wins there, so say so.
			v.Editable, v.Override = false, false
			if envSet {
				v.Source = "environment"
			} else {
				v.Source = "default"
			}
		}
		if !spec.Secret() {
			v.Value, v.EnvValue, v.Running = eff, strings.TrimSpace(envV), running[spec.Name]
		}
		if running != nil && spec.Editable() && eff != running[spec.Name] {
			v.Pending = true
			out.Pending++
		}
		out.Vars = append(out.Vars, v)
	}
	return out, nil
}

// PanelEnvChange is one save: values to override and overrides to drop.
type PanelEnvChange struct {
	Set   map[string]string
	Unset []string
}

// Update stores the change after checking every value and the configuration
// the panel would start with. Nothing is stored when anything is refused.
func (s *PanelEnvService) Update(ctx context.Context, actor domain.User, ch PanelEnvChange) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if len(ch.Set)+len(ch.Unset) == 0 {
		return domain.Invalid("nothing to change")
	}
	check := func(name string) (config.VarSpec, error) {
		spec, ok := config.Spec(name)
		switch {
		case !ok:
			return spec, domain.Invalid(name + " is not a RivetPanel setting")
		case spec.Managed != "":
			return spec, domain.Invalid(name + " is changed under Panel settings")
		case spec.Boot:
			return spec, domain.Invalid(name + " is read before the database opens or controls what the panel may do to the host; change it in the environment file")
		}
		return spec, nil
	}
	current, _, err := s.Overrides(ctx)
	if err != nil {
		return err
	}
	merged := make(map[string]string, len(current)+len(ch.Set))
	for k, v := range current {
		merged[k] = v
	}
	var set []domain.Setting
	for name, raw := range ch.Set {
		spec, err := check(name)
		if err != nil {
			return err
		}
		val := strings.TrimSpace(raw)
		if err := config.CheckValue(spec, val); err != nil {
			return domain.Invalid(err.Error())
		}
		if spec.Secret() && strings.ContainsAny(val, " \t") {
			return domain.Invalid(name + " must not contain spaces")
		}
		merged[name] = val
		row := domain.Setting{Key: name, Value: val}
		if spec.Secret() && val != "" {
			sl, err := s.Keys.Seal("env", name, []byte(val))
			if err != nil {
				return err
			}
			row = domain.Setting{Key: name, Cipher: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID}
		}
		set = append(set, row)
	}
	var remove []string
	for _, name := range ch.Unset {
		if _, err := check(name); err != nil {
			return err
		}
		if _, dup := ch.Set[name]; dup {
			return domain.Invalid(name + " is both set and reset")
		}
		delete(merged, name)
		remove = append(remove, name)
	}
	sort.Slice(set, func(i, j int) bool { return set[i].Key < set[j].Key })
	// The configuration the panel would start with must be valid as a whole.
	if _, err := config.LoadLookup(config.Overlay(s.Base, merged)); err != nil {
		return domain.Invalid("these values do not work together, nothing was saved: " + oneLine(err))
	}
	return s.Store.PutEnvOverrides(ctx, set, remove, actor.ID, s.now().UnixMilli())
}

// Reset drops every override (the command-line recovery path uses the store
// directly; this is the same operation for the page).
func (s *PanelEnvService) Reset(ctx context.Context, actor domain.User) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	stored, err := s.Store.EnvOverrides(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(stored))
	for n := range stored {
		names = append(names, n)
	}
	if len(names) == 0 {
		return nil
	}
	return s.Store.PutEnvOverrides(ctx, nil, names, actor.ID, s.now().UnixMilli())
}

// RestartPanel asks the process to exit so its supervisor starts it again with
// the stored overrides. Bot containers are not touched.
func (s *PanelEnvService) RestartPanel(actor domain.User) error {
	if !actor.IsAdmin() {
		return domain.ErrForbidden
	}
	if !s.CanRestart || s.Restart == nil {
		return domain.Invalid("this panel does not run under systemd or a container restart policy, so it cannot restart itself; stop it and start it again")
	}
	if s.Log != nil {
		s.Log.Warn("panel restart requested from the administration page", "by", actor.Email)
	}
	// Answer the request before the listener closes.
	go func() { time.Sleep(500 * time.Millisecond); s.Restart() }()
	return nil
}

func oneLine(err error) string {
	var parts []string
	var join interface{ Unwrap() []error }
	if errors.As(err, &join) {
		for _, e := range join.Unwrap() {
			parts = append(parts, e.Error())
		}
	} else {
		parts = []string{err.Error()}
	}
	return strings.Join(parts, "; ")
}
