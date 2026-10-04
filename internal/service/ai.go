package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	operator "github.com/xenycx/rivetpanel/internal/ai"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

const (
	AIApproval  = "approval"
	AIAuto      = "auto"
	maxAIFile   = 1 << 20
	maxAIChange = 8 << 20
)

type AIStore interface {
	ListAIProviders(context.Context) ([]domain.AIProviderProfile, error)
	GetAIProvider(context.Context, string) (domain.AIProviderProfile, error)
	PutAIProvider(context.Context, domain.AIProviderProfile) error
	DeleteAIProvider(context.Context, string) error
	CreateAIConversation(context.Context, domain.AIConversation) error
	GetAIConversation(context.Context, string) (domain.AIConversation, error)
	ListAIConversations(context.Context, string, *string, *string, bool) ([]domain.AIConversation, error)
	ListUserAIConversations(context.Context, string, int) ([]domain.AIConversation, error)
	UpdateAIConversation(context.Context, domain.AIConversation) error
	DeleteAIConversation(context.Context, string) error
	InsertAIMessage(context.Context, domain.AIMessage) error
	ListAIMessages(context.Context, string, int) ([]domain.AIMessage, error)
	InsertAIRun(context.Context, domain.AIRun) error
	GetAIRun(context.Context, string) (domain.AIRun, error)
	ListAIRuns(context.Context, string, int) ([]domain.AIRun, error)
	UpdateAIRun(context.Context, domain.AIRun) error
	InterruptAIRuns(context.Context, int64) (int64, error)
	CloseAIToolCalls(context.Context, string, int64) error
	PruneAIConversations(context.Context, int64, int) (int64, error)
	InsertAIToolCall(context.Context, domain.AIToolCall) error
	GetAIToolCall(context.Context, string) (domain.AIToolCall, error)
	ListAIToolCalls(context.Context, string) ([]domain.AIToolCall, error)
	UpdateAIToolCall(context.Context, domain.AIToolCall) error
	CountActiveAIRuns(context.Context, string, string, string) (int, int, int, error)
	InsertAIChangeSet(context.Context, domain.AIChangeSet) error
	GetAIChangeSet(context.Context, string) (domain.AIChangeSet, error)
	ListAIChangeSets(context.Context, string) ([]domain.AIChangeSet, error)
	UpdateAIChangeSet(context.Context, domain.AIChangeSet) error
	Settings(context.Context) (map[string]domain.Setting, error)
	PutSettings(context.Context, []domain.Setting, int64) error
	GetUserByID(context.Context, string) (domain.User, error)
}

type DiagnosticRunner interface {
	RunDiagnostic(context.Context, string, string, []string) (domain.DiagnosticResult, error)
}

// diagnosticAvailable refuses a diagnostic that cannot run where b's files
// are: no local runner, or a remote node that is offline or not reachable for
// diagnostics on this panel.
func (s *AIService) diagnosticAvailable(b domain.Bot) error {
	if !s.Bots.remote(b.NodeID) {
		if s.Diagnostic == nil {
			return domain.Invalid("diagnostic runner is unavailable")
		}
		return nil
	}
	if s.RemoteDiagnostic == nil {
		return domain.Invalid("isolated diagnostics are not available for servers on remote nodes on this panel")
	}
	_, _, err := s.Bots.RemoteFiles(b, "Isolated diagnostics")
	return err
}

// NodeDiagnostics runs an isolated diagnostic on a server's remote node
// (noderoute.Router): the node validates the command against its runtime
// catalog and runs the same sandboxed container on its own Docker, using a
// copy of the workspace it holds.
type NodeDiagnostics interface {
	RunDiagnostic(ctx context.Context, nodeID, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error)
}

// LogTail reads the last lines of a bot container's output without following
// it. The Docker adapter implements it.
type LogTail interface {
	Tail(ctx context.Context, containerID string, n int) (string, error)
}

type AIRunLimits struct {
	Rounds           int   `json:"rounds"`
	Diagnostics      int   `json:"diagnostics"`
	ApplyAttempts    int   `json:"apply_attempts"`
	LifecycleActions int   `json:"lifecycle_actions"`
	WallMinutes      int   `json:"wall_minutes"`
	ChangedFiles     int   `json:"changed_files"`
	ChangedBytes     int64 `json:"changed_bytes"`
	RetainedOutput   int64 `json:"retained_output"`
}

func DefaultAIRunLimits() AIRunLimits { return AIRunLimits{12, 5, 3, 2, 20, 32, 8 << 20, 2 << 20} }

// aiBudget counts what one run has consumed against its limits. Every mode
// uses it; approvals never raise a limit. A run executes one tool at a time,
// so it needs no lock.
type aiBudget struct {
	lim                             AIRunLimits
	diagnostics, applies, lifecycle int
	files                           map[string]bool
	bytes, output                   int64
}

func newAIBudget(lim AIRunLimits) *aiBudget { return &aiBudget{lim: lim, files: map[string]bool{}} }

func limitReached(what string, n any) error {
	return domain.Invalid(fmt.Sprintf("run limit reached: at most %v %s per run; summarize the findings and stop or ask the user to start a new run", n, what))
}
func (b *aiBudget) diagnostic() error {
	if b.diagnostics >= b.lim.Diagnostics {
		return limitReached("diagnostic jobs", b.lim.Diagnostics)
	}
	return nil
}
func (b *aiBudget) lifecycleAction() error {
	if b.lifecycle >= b.lim.LifecycleActions {
		return limitReached("lifecycle actions", b.lim.LifecycleActions)
	}
	return nil
}

// change checks a proposed file change before it is drafted.
func (b *aiBudget) change(key string, size int64) error {
	if b.applies >= b.lim.ApplyAttempts {
		return limitReached("apply attempts", b.lim.ApplyAttempts)
	}
	if !b.files[key] && len(b.files) >= b.lim.ChangedFiles {
		return limitReached("changed files", b.lim.ChangedFiles)
	}
	if b.bytes+size > b.lim.ChangedBytes {
		return limitReached("changed bytes", b.lim.ChangedBytes)
	}
	return nil
}

// retain clips a tool result to the run's remaining retained-output budget.
func (b *aiBudget) retain(out string) string {
	left := b.lim.RetainedOutput - b.output
	if left < 0 {
		left = 0
	}
	if int64(len(out)) > left {
		out = clipService(out, int(left)) + "\n[output truncated: the run's retained-output limit is reached; further tool calls will fail]"
	}
	b.output += int64(len(out))
	return out
}

type AIEvent struct {
	Sequence int64  `json:"sequence"`
	Type     string `json:"type"`
	AtMS     int64  `json:"at_ms"`
	Data     any    `json:"data,omitempty"`
}
type aiStream struct {
	events []AIEvent
	next   int64
	subs   map[chan AIEvent]struct{}
}
type pendingApproval struct {
	runID string
	ch    chan bool
}

type AIService struct {
	Store      AIStore
	Keys       *secrets.Keyring
	Bots       *BotService
	Sites      *SiteService
	Files      *filesystem.Manager
	Ops        *Operations
	Audit      *Audit
	Provider   *operator.Client
	Research   *operator.Research
	Diagnostic DiagnosticRunner
	// RemoteDiagnostic runs diagnostics for servers on remote nodes; nil
	// refuses them.
	RemoteDiagnostic NodeDiagnostics
	Logs             LogTail // nil: the read_logs tool reports that logs are unavailable
	Log              *slog.Logger
	Now              func() time.Time
	// Limits overrides DefaultAIRunLimits when Rounds is set.
	Limits AIRunLimits
	// StreamGrace is how long a finished run's event stream stays available
	// for late subscribers before it is dropped (default 5 minutes).
	StreamGrace time.Duration
	// ToolTimeout bounds one read-only tool call (file reads and listings on
	// a remote node, logs, web research); default 90 seconds. Tools that
	// wait for a person's approval are bounded by the run's wall time.
	ToolTimeout time.Duration

	mu      sync.Mutex
	streams map[string]*aiStream
	cancels map[string]context.CancelFunc
	pending map[string]pendingApproval
}

func (s *AIService) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}
func (s *AIService) init() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = map[string]*aiStream{}
	}
	if s.cancels == nil {
		s.cancels = map[string]context.CancelFunc{}
	}
	if s.pending == nil {
		s.pending = map[string]pendingApproval{}
	}
	if s.Provider == nil {
		s.Provider = &operator.Client{}
	}
	if s.Research == nil {
		s.Research = &operator.Research{}
	}
}

func (s *AIService) Start(ctx context.Context) {
	s.init()
	if n, e := s.Store.InterruptAIRuns(ctx, s.now()); e == nil && n > 0 && s.Log != nil {
		s.Log.Warn("AI runs interrupted after panel restart", "count", n)
	}
	go s.prune(ctx)
}
func (s *AIService) prune(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		for {
			n, e := s.Store.PruneAIConversations(ctx, s.now()-(90*24*time.Hour).Milliseconds(), 200)
			if e != nil || n < 200 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *AIService) limits() AIRunLimits {
	if s.Limits.Rounds > 0 {
		return s.Limits
	}
	return DefaultAIRunLimits()
}

// openStream registers a run's event stream. Only runs started by this
// process have one; events for anything else are dropped.
func (s *AIService) openStream(runID string) {
	s.init()
	s.mu.Lock()
	if s.streams[runID] == nil {
		s.streams[runID] = &aiStream{subs: map[chan AIEvent]struct{}{}}
	}
	s.mu.Unlock()
}

// retireStream drops a finished run's stream after the grace period and
// closes its subscribers, so finished runs do not accumulate in memory.
func (s *AIService) retireStream(runID string) {
	grace := s.StreamGrace
	if grace <= 0 {
		grace = 5 * time.Minute
	}
	time.AfterFunc(grace, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		st := s.streams[runID]
		if st == nil {
			return
		}
		for ch := range st.subs {
			delete(st.subs, ch)
			close(ch)
		}
		delete(s.streams, runID)
	})
}

func (s *AIService) emit(runID, typ string, data any) {
	s.init()
	s.mu.Lock()
	st := s.streams[runID]
	if st == nil {
		s.mu.Unlock()
		return
	}
	st.next++
	ev := AIEvent{st.next, typ, s.now(), data}
	st.events = append(st.events, ev)
	if len(st.events) > 2000 {
		st.events = append([]AIEvent(nil), st.events[len(st.events)-2000:]...)
	}
	for ch := range st.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	s.mu.Unlock()
}
func (s *AIService) Subscribe(runID string, after int64) ([]AIEvent, <-chan AIEvent, func()) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.streams[runID]
	if st == nil {
		// Finished long ago or started before a restart: nothing more will
		// arrive, so the subscriber gets a closed channel.
		ch := make(chan AIEvent)
		close(ch)
		return nil, ch, func() {}
	}
	var old []AIEvent
	for _, e := range st.events {
		if e.Sequence > after {
			old = append(old, e)
		}
	}
	ch := make(chan AIEvent, 64)
	st.subs[ch] = struct{}{}
	cancel := func() {
		s.mu.Lock()
		if _, ok := st.subs[ch]; ok {
			delete(st.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return old, ch, cancel
}

type AIProviderInput struct {
	Name              string  `json:"name"`
	Enabled           bool    `json:"enabled"`
	Default           bool    `json:"default"`
	BaseURL           string  `json:"base_url"`
	ChatPath          string  `json:"chat_path"`
	ModelsPath        string  `json:"models_path"`
	DefaultModel      string  `json:"default_model"`
	ContextSize       *int64  `json:"context_size"`
	MaxOutputTokens   int64   `json:"max_output_tokens"`
	Temperature       float64 `json:"temperature"`
	TimeoutMS         int64   `json:"timeout_ms"`
	InputPriceMicros  *int64  `json:"input_price_micros"`
	OutputPriceMicros *int64  `json:"output_price_micros"`
	Key               *string `json:"key"`
}
type AIProviderView struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	BaseURL           string  `json:"base_url"`
	ChatPath          string  `json:"chat_path"`
	ModelsPath        string  `json:"models_path"`
	DefaultModel      string  `json:"default_model"`
	Enabled           bool    `json:"enabled"`
	Default           bool    `json:"default"`
	KeySet            bool    `json:"key_set"`
	ContextSize       *int64  `json:"context_size"`
	MaxOutputTokens   int64   `json:"max_output_tokens"`
	TimeoutMS         int64   `json:"timeout_ms"`
	Temperature       float64 `json:"temperature"`
	InputPriceMicros  *int64  `json:"input_price_micros"`
	OutputPriceMicros *int64  `json:"output_price_micros"`
	CreatedAtMS       int64   `json:"created_at_ms"`
	UpdatedAtMS       int64   `json:"updated_at_ms"`
}

func providerView(p domain.AIProviderProfile) AIProviderView {
	return AIProviderView{p.ID, p.Name, p.BaseURL, p.ChatPath, p.ModelsPath, p.DefaultModel, p.Enabled, p.Default, len(p.KeyCipher) > 0, p.ContextSize, p.MaxOutputTokens, p.TimeoutMS, p.Temperature, p.InputPriceMicros, p.OutputPriceMicros, p.CreatedAtMS, p.UpdatedAtMS}
}
func (s *AIService) Providers(ctx context.Context, actor domain.User) ([]AIProviderView, error) {
	if !actor.Can(domain.PermAIManage) {
		return nil, domain.ErrForbidden
	}
	ps, e := s.Store.ListAIProviders(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]AIProviderView, len(ps))
	for i, p := range ps {
		out[i] = providerView(p)
	}
	return out, nil
}

// AvailableProviders is the non-secret selector surface for authenticated AI
// users. Provider origins and administrative tuning remain admin-only.
func (s *AIService) AvailableProviders(ctx context.Context) ([]map[string]any, error) {
	ps, e := s.Store.ListAIProviders(ctx)
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, p := range ps {
		if p.Enabled {
			out = append(out, map[string]any{"id": p.ID, "name": p.Name, "default_model": p.DefaultModel, "default": p.Default, "pricing_configured": p.InputPriceMicros != nil && p.OutputPriceMicros != nil})
		}
	}
	return out, nil
}

// ValidateAIProvider checks a provider profile without saving it (the setup
// wizard validates before it creates the administrator).
func ValidateAIProvider(in AIProviderInput) error {
	if e := validateProvider(in); e != nil {
		return e
	}
	if in.Key == nil || strings.TrimSpace(*in.Key) == "" {
		return domain.Invalid("API key cannot be empty")
	}
	return nil
}

func validateProvider(in AIProviderInput) error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 80 {
		return domain.Invalid("provider name is required")
	}
	if _, e := operatorURL(in.BaseURL); e != nil {
		return domain.Invalid("base URL must be HTTP(S)")
	}
	if !strings.HasPrefix(in.ChatPath, "/") || !strings.HasPrefix(in.ModelsPath, "/") {
		return domain.Invalid("provider paths must start with /")
	}
	if strings.TrimSpace(in.DefaultModel) == "" || len(in.DefaultModel) > 160 {
		return domain.Invalid("default model is required")
	}
	if len(in.ChatPath) > 200 || len(in.ModelsPath) > 200 {
		return domain.Invalid("provider paths are too long")
	}
	if in.ContextSize != nil && (*in.ContextSize < 1024 || *in.ContextSize > 2_000_000) {
		return domain.Invalid("context size must be between 1,024 and 2,000,000 tokens")
	}
	if in.MaxOutputTokens != 0 && (in.MaxOutputTokens < 64 || in.MaxOutputTokens > 131072) {
		return domain.Invalid("max output tokens must be between 64 and 131,072")
	}
	if in.Temperature < 0 || in.Temperature > 2 {
		return domain.Invalid("temperature must be between 0 and 2")
	}
	if in.TimeoutMS != 0 && (in.TimeoutMS < 5000 || in.TimeoutMS > 600000) {
		return domain.Invalid("timeout must be between 5 and 600 seconds")
	}
	if in.InputPriceMicros != nil && *in.InputPriceMicros < 0 || in.OutputPriceMicros != nil && *in.OutputPriceMicros < 0 {
		return domain.Invalid("prices cannot be negative")
	}
	return nil
}
func operatorURL(raw string) (string, error) { u, e := urlParse(raw); return u, e }

// urlParse is deliberately narrow: provider origins are administrator-trusted
// but still may not carry credentials.
func urlParse(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "@") || !(strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")) {
		return "", errors.New("invalid URL")
	}
	return strings.TrimRight(raw, "/"), nil
}

func (s *AIService) PutProvider(ctx context.Context, actor domain.User, id string, in AIProviderInput) (AIProviderView, error) {
	if !actor.Can(domain.PermAIManage) {
		return AIProviderView{}, domain.ErrForbidden
	}
	if err := validateProvider(in); err != nil {
		return AIProviderView{}, err
	}
	now := s.now()
	if id == "" {
		id = uuid.NewString()
	}
	p := domain.AIProviderProfile{ID: id, Name: strings.TrimSpace(in.Name), Enabled: in.Enabled, Default: in.Default, BaseURL: strings.TrimRight(in.BaseURL, "/"), ChatPath: in.ChatPath, ModelsPath: in.ModelsPath, DefaultModel: strings.TrimSpace(in.DefaultModel), ContextSize: in.ContextSize, MaxOutputTokens: in.MaxOutputTokens, Temperature: in.Temperature, TimeoutMS: in.TimeoutMS, InputPriceMicros: in.InputPriceMicros, OutputPriceMicros: in.OutputPriceMicros, CreatedAtMS: now, UpdatedAtMS: now}
	if p.MaxOutputTokens == 0 {
		p.MaxOutputTokens = 4096
	}
	if p.TimeoutMS == 0 {
		p.TimeoutMS = 120000
	}
	if in.Key != nil {
		key := strings.TrimSpace(*in.Key)
		if key == "" {
			return AIProviderView{}, domain.Invalid("API key cannot be empty")
		}
		sealed, e := s.Keys.Seal(secrets.AIProviderNS(id), secrets.AIProviderKeyName, []byte(key))
		if e != nil {
			return AIProviderView{}, e
		}
		p.KeyCipher, p.KeyNonce, p.KeyID = sealed.Ciphertext, sealed.Nonce, &sealed.KeyID
	}
	if old, e := s.Store.GetAIProvider(ctx, id); e == nil {
		p.CreatedAtMS = old.CreatedAtMS
		if in.Key == nil {
			p.KeyCipher, p.KeyNonce, p.KeyID = old.KeyCipher, old.KeyNonce, old.KeyID
		}
	}
	if e := s.Store.PutAIProvider(ctx, p); e != nil {
		return AIProviderView{}, e
	}
	return providerView(p), nil
}
func (s *AIService) DeleteProvider(ctx context.Context, actor domain.User, id string) error {
	if !actor.Can(domain.PermAIManage) {
		return domain.ErrForbidden
	}
	return s.Store.DeleteAIProvider(ctx, id)
}
func (s *AIService) providerConfig(ctx context.Context, id string) (domain.AIProviderProfile, operator.Config, error) {
	p, e := s.Store.GetAIProvider(ctx, id)
	if e != nil {
		return p, operator.Config{}, e
	}
	if !p.Enabled {
		return p, operator.Config{}, domain.Invalid("this AI provider is disabled")
	}
	if p.KeyID == nil {
		return p, operator.Config{}, domain.Invalid("this AI provider has no API key")
	}
	pt, e := s.Keys.Open(secrets.AIProviderNS(p.ID), secrets.AIProviderKeyName, secrets.Sealed{Ciphertext: p.KeyCipher, Nonce: p.KeyNonce, KeyID: *p.KeyID})
	if e != nil {
		return p, operator.Config{}, e
	}
	key := string(pt)
	clear(pt)
	return p, operator.Config{BaseURL: p.BaseURL, ChatPath: p.ChatPath, ModelsPath: p.ModelsPath, Key: key, Model: p.DefaultModel, Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, MaxTokens: p.MaxOutputTokens, Temperature: p.Temperature}, nil
}
func (s *AIService) ProviderModels(ctx context.Context, actor domain.User, id string) ([]string, error) {
	if !actor.Can(domain.PermAIManage) {
		return nil, domain.ErrForbidden
	}
	_, cfg, e := s.providerConfig(ctx, id)
	if e != nil {
		return nil, e
	}
	defer clear([]byte(cfg.Key))
	return s.Provider.Models(ctx, cfg)
}
func (s *AIService) TestProvider(ctx context.Context, actor domain.User, id string) (map[string]bool, error) {
	if !actor.Can(domain.PermAIManage) {
		return nil, domain.ErrForbidden
	}
	_, cfg, e := s.providerConfig(ctx, id)
	if e != nil {
		return nil, e
	}
	return s.Provider.Test(ctx, cfg)
}

const (
	setAISearchEnabled    = "ai_search_enabled"
	setAIFetchEnabled     = "ai_fetch_enabled"
	setAISearchURL        = "ai_search_url"
	setAISearchKeys       = "ai_search_keys"
	setAISearchResults    = "ai_search_results"
	setAISearchLanguage   = "ai_search_language"
	setAISearchCategories = "ai_search_categories"
	setAISearchTime       = "ai_search_time_range"
	setAISearchSafe       = "ai_search_safe"
)

type AISearchInput struct {
	SearchEnabled bool      `json:"search_enabled"`
	FetchEnabled  bool      `json:"fetch_enabled"`
	BaseURL       string    `json:"base_url"`
	Keys          *[]string `json:"keys"`
	Results       int       `json:"results"`
	Language      string    `json:"language"`
	Categories    string    `json:"categories"`
	TimeRange     string    `json:"time_range"`
	SafeSearch    int       `json:"safe_search"`
}
type AISearchView struct {
	SearchEnabled bool   `json:"search_enabled"`
	FetchEnabled  bool   `json:"fetch_enabled"`
	BaseURL       string `json:"base_url"`
	KeyCount      int    `json:"key_count"`
	KeySet        bool   `json:"key_set"`
	Results       int    `json:"results"`
	Language      string `json:"language"`
	Categories    string `json:"categories"`
	TimeRange     string `json:"time_range"`
	SafeSearch    int    `json:"safe_search"`
}

func intValue(s string, d int) int {
	var n int
	if _, e := fmt.Sscan(s, &n); e != nil {
		return d
	}
	return n
}
func (s *AIService) searchConfig(ctx context.Context) (AISearchView, operator.SearchConfig, error) {
	all, e := s.Store.Settings(ctx)
	if e != nil {
		return AISearchView{}, operator.SearchConfig{}, e
	}
	v := AISearchView{BaseURL: "https://risa.xenyc.ge", Results: 5, Language: "auto", SafeSearch: 1}
	v.SearchEnabled = all[setAISearchEnabled].Value == "1"
	v.FetchEnabled = all[setAIFetchEnabled].Value == "1"
	if x := all[setAISearchURL].Value; x != "" {
		v.BaseURL = x
	}
	v.Results = intValue(all[setAISearchResults].Value, 5)
	if x := all[setAISearchLanguage].Value; x != "" {
		v.Language = x
	}
	v.Categories = all[setAISearchCategories].Value
	v.TimeRange = all[setAISearchTime].Value
	v.SafeSearch = intValue(all[setAISearchSafe].Value, 1)
	var keys []string
	if x := all[setAISearchKeys]; len(x.Cipher) > 0 {
		pt, e := s.Keys.Open("settings", setAISearchKeys, secrets.Sealed{Ciphertext: x.Cipher, Nonce: x.Nonce, KeyID: x.KeyID})
		if e != nil {
			return v, operator.SearchConfig{}, e
		}
		_ = json.Unmarshal(pt, &keys)
		clear(pt)
	}
	v.KeyCount = len(keys)
	v.KeySet = len(keys) > 0
	return v, operator.SearchConfig{BaseURL: v.BaseURL, Keys: keys, Results: v.Results, Language: v.Language, Categories: v.Categories, TimeRange: v.TimeRange, SafeSearch: v.SafeSearch}, nil
}
func (s *AIService) SearchSettings(ctx context.Context, actor domain.User) (AISearchView, error) {
	if !actor.Can(domain.PermAIManage) {
		return AISearchView{}, domain.ErrForbidden
	}
	v, _, e := s.searchConfig(ctx)
	return v, e
}
func (s *AIService) PutSearchSettings(ctx context.Context, actor domain.User, in AISearchInput) (AISearchView, error) {
	if !actor.Can(domain.PermAIManage) {
		return AISearchView{}, domain.ErrForbidden
	}
	if _, e := urlParse(in.BaseURL); e != nil {
		return AISearchView{}, domain.Invalid("search URL must be HTTP(S)")
	}
	if in.Results < 1 || in.Results > 10 || in.SafeSearch < 0 || in.SafeSearch > 2 {
		return AISearchView{}, domain.Invalid("search bounds are invalid")
	}
	b := func(v bool) string {
		if v {
			return "1"
		}
		return ""
	}
	set := []domain.Setting{{Key: setAISearchEnabled, Value: b(in.SearchEnabled)}, {Key: setAIFetchEnabled, Value: b(in.FetchEnabled)}, {Key: setAISearchURL, Value: strings.TrimRight(in.BaseURL, "/")}, {Key: setAISearchResults, Value: fmt.Sprint(in.Results)}, {Key: setAISearchLanguage, Value: in.Language}, {Key: setAISearchCategories, Value: in.Categories}, {Key: setAISearchTime, Value: in.TimeRange}, {Key: setAISearchSafe, Value: fmt.Sprint(in.SafeSearch)}}
	if in.Keys != nil {
		var keys []string
		for _, k := range *in.Keys {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 10 {
			return AISearchView{}, domain.Invalid("at most 10 search keys")
		}
		if len(keys) == 0 {
			set = append(set, domain.Setting{Key: setAISearchKeys})
		} else {
			raw, _ := json.Marshal(keys)
			sl, e := s.Keys.Seal("settings", setAISearchKeys, raw)
			clear(raw)
			if e != nil {
				return AISearchView{}, e
			}
			set = append(set, domain.Setting{Key: setAISearchKeys, Cipher: sl.Ciphertext, Nonce: sl.Nonce, KeyID: sl.KeyID})
		}
	}
	if e := s.Store.PutSettings(ctx, set, s.now()); e != nil {
		return AISearchView{}, e
	}
	return s.SearchSettings(ctx, actor)
}
func (s *AIService) TestSearch(ctx context.Context, actor domain.User) (operator.SearchResponse, error) {
	if !actor.Can(domain.PermAIManage) {
		return operator.SearchResponse{}, domain.ErrForbidden
	}
	v, c, e := s.searchConfig(ctx)
	if e != nil {
		return operator.SearchResponse{}, e
	}
	if !v.SearchEnabled {
		return operator.SearchResponse{}, domain.Invalid("web search is disabled")
	}
	return s.Research.Search(ctx, c, "RivetPanel hosting")
}

// effective is the conversation as one run sees it: the run's own target
// replaces the conversation's, because one chat follows the person across the
// panel and each message names what it is about.
func effective(c domain.AIConversation, r domain.AIRun) domain.AIConversation {
	c.BotID, c.SiteID = r.BotID, r.SiteID
	return c
}

func (s *AIService) authorizeTarget(ctx context.Context, actor domain.User, c domain.AIConversation, mutate bool) error {
	if c.BotID == nil && c.SiteID == nil {
		// A chat with no target is private to its creator, administrators included.
		if c.CreatorID != actor.ID {
			return domain.ErrNotFound
		}
		return nil
	}
	if c.CreatorID != actor.ID && !actor.IsAdmin() {
		return domain.ErrNotFound
	}
	if c.BotID != nil {
		perm := 0
		if mutate {
			perm = domain.PermEditFiles
		}
		_, e := s.Bots.Authorize(ctx, actor, *c.BotID, perm)
		return e
	}
	min := domain.WorkspaceViewer
	if mutate {
		min = domain.WorkspaceDeveloper
	}
	_, _, e := s.Sites.Authorize(ctx, actor, *c.SiteID, min)
	return e
}
func (s *AIService) CreateConversation(ctx context.Context, actor domain.User, botID, siteID *string, title string) (domain.AIConversation, error) {
	if botID != nil && siteID != nil {
		return domain.AIConversation{}, domain.Invalid("choose at most one bot or site")
	}
	v := domain.AIConversation{ID: uuid.NewString(), CreatorID: actor.ID, BotID: botID, SiteID: siteID, Title: strings.TrimSpace(title), CreatedAtMS: s.now(), UpdatedAtMS: s.now()}
	if v.Title == "" {
		v.Title = defaultChatTitle
	}
	if e := s.authorizeTarget(ctx, actor, v, false); e != nil {
		return v, e
	}
	ps, e := s.Store.ListAIProviders(ctx)
	if e != nil {
		return v, e
	}
	for _, p := range ps {
		if p.Enabled && (p.Default || v.ProviderID == nil) {
			x, m := p.ID, p.DefaultModel
			v.ProviderID = &x
			v.Model = &m
			if p.Default {
				break
			}
		}
	}
	if e = s.Store.CreateAIConversation(ctx, v); e != nil {
		return v, e
	}
	return v, nil
}
func (s *AIService) ListConversations(ctx context.Context, actor domain.User, botID, siteID *string) ([]domain.AIConversation, error) {
	tmp := domain.AIConversation{CreatorID: actor.ID, BotID: botID, SiteID: siteID}
	if e := s.authorizeTarget(ctx, actor, tmp, false); e != nil {
		return nil, e
	}
	return s.Store.ListAIConversations(ctx, actor.ID, botID, siteID, actor.IsAdmin())
}

// MyConversations lists the caller's own chats across the whole panel.
func (s *AIService) MyConversations(ctx context.Context, actor domain.User) ([]domain.AIConversation, error) {
	return s.Store.ListUserAIConversations(ctx, actor.ID, 50)
}
func (s *AIService) Conversation(ctx context.Context, actor domain.User, id string) (domain.AIConversation, []domain.AIMessage, error) {
	c, e := s.Store.GetAIConversation(ctx, id)
	if e != nil {
		return c, nil, e
	}
	if e = s.authorizeTarget(ctx, actor, c, false); e != nil {
		return c, nil, e
	}
	m, e := s.Store.ListAIMessages(ctx, id, 500)
	return c, m, e
}
func (s *AIService) UpdateConversation(ctx context.Context, actor domain.User, id string, title, providerID, model *string) (domain.AIConversation, error) {
	c, e := s.Store.GetAIConversation(ctx, id)
	if e != nil {
		return c, e
	}
	if c.CreatorID != actor.ID && !actor.IsAdmin() {
		return c, domain.ErrNotFound
	}
	if e = s.authorizeTarget(ctx, actor, c, false); e != nil {
		return c, e
	}
	if title != nil {
		c.Title = strings.TrimSpace(*title)
		if c.Title == "" || len(c.Title) > 120 {
			return c, domain.Invalid("title is invalid")
		}
	}
	if providerID != nil {
		p, e := s.Store.GetAIProvider(ctx, *providerID)
		if e != nil {
			return c, e
		}
		c.ProviderID = providerID
		if model == nil {
			x := p.DefaultModel
			c.Model = &x
		}
	}
	if model != nil {
		if strings.TrimSpace(*model) == "" || len(*model) > 160 {
			return c, domain.Invalid("model is invalid")
		}
		c.Model = model
	}
	c.UpdatedAtMS = s.now()
	e = s.Store.UpdateAIConversation(ctx, c)
	return c, e
}
func (s *AIService) DeleteConversation(ctx context.Context, actor domain.User, id string) error {
	c, e := s.Store.GetAIConversation(ctx, id)
	if e != nil {
		return e
	}
	if c.CreatorID != actor.ID && !actor.IsAdmin() {
		return domain.ErrNotFound
	}
	return s.Store.DeleteAIConversation(ctx, id)
}

func (s *AIService) StartMessage(ctx context.Context, actor domain.User, conversationID, content, mode string, view *AIContext) (domain.AIRun, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 100000 {
		return domain.AIRun{}, domain.Invalid("message is empty or too large")
	}
	if mode == "" {
		mode = AIApproval
	}
	if mode != AIApproval && mode != AIAuto {
		return domain.AIRun{}, domain.Invalid("unknown AI mode")
	}
	c, e := s.Store.GetAIConversation(ctx, conversationID)
	if e != nil {
		return domain.AIRun{}, e
	}
	if e = s.authorizeTarget(ctx, actor, c, false); e != nil {
		return domain.AIRun{}, e
	}
	if c.ProviderID == nil || c.Model == nil {
		return domain.AIRun{}, domain.Invalid("select an enabled AI provider and model")
	}
	p, e := s.Store.GetAIProvider(ctx, *c.ProviderID)
	if e != nil || !p.Enabled {
		return domain.AIRun{}, domain.Invalid("select an enabled AI provider")
	}
	// What the person is looking at decides which bot or site this run works
	// on; a conversation created from a bot's own endpoints keeps its target
	// when the message carries none.
	resolved, botID, siteID, e := s.resolveContext(ctx, actor, view)
	if e != nil {
		return domain.AIRun{}, e
	}
	if botID == nil && siteID == nil {
		botID, siteID = c.BotID, c.SiteID
	}
	if mode == AIAuto && botID == nil && siteID == nil {
		return domain.AIRun{}, domain.Invalid("Auto repair works on one bot or site: open it first, or use Approval mode")
	}
	kind, targetID := "", ""
	switch {
	case botID != nil:
		kind, targetID = "bot", *botID
	case siteID != nil:
		kind, targetID = "site", *siteID
	}
	u, g, t, e := s.Store.CountActiveAIRuns(ctx, actor.ID, kind, targetID)
	if e != nil {
		return domain.AIRun{}, e
	}
	if u >= 2 || g >= 4 || t >= 1 {
		return domain.AIRun{}, fmt.Errorf("%w: AI run limit reached", domain.ErrConflict)
	}
	now := s.now()
	cj, _ := json.Marshal(resolved)
	m := domain.AIMessage{ID: uuid.NewString(), ConversationID: c.ID, Role: "user", Content: content, CitationsJSON: "[]", ContextJSON: string(cj), CreatedAtMS: now}
	if e = s.Store.InsertAIMessage(ctx, m); e != nil {
		return domain.AIRun{}, e
	}
	lim := s.limits()
	lj, _ := json.Marshal(lim)
	run := domain.AIRun{ID: uuid.NewString(), ConversationID: c.ID, UserID: actor.ID, ProviderID: c.ProviderID, Model: *c.Model, Mode: mode, Status: "queued", LimitsJSON: string(lj), PlanJSON: "[]", BotID: botID, SiteID: siteID, CreatedAtMS: now}
	if e = s.Store.InsertAIRun(ctx, run); e != nil {
		return run, e
	}
	c.UpdatedAtMS = now
	if c.Title == defaultChatTitle {
		c.Title = titleFrom(content)
	}
	_ = s.Store.UpdateAIConversation(ctx, c)
	s.openStream(run.ID)
	base := context.WithoutCancel(ctx)
	rctx, cancel := context.WithTimeout(base, time.Duration(lim.WallMinutes)*time.Minute)
	s.init()
	s.mu.Lock()
	s.cancels[run.ID] = cancel
	s.mu.Unlock()
	// The goroutine owns its own copy; the caller gets the queued snapshot.
	live := run
	go func() {
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				if s.Log != nil {
					s.Log.Error("AI run panicked", "run", live.ID, "panic", fmt.Sprint(p))
				}
				s.finishRun(&live, "failed", "internal", "The assistant stopped because of an internal error.")
			}
		}()
		if mode == AIAuto {
			if !s.awaitAutoEnvelope(rctx, &live, lim) {
				return
			}
		}
		s.run(rctx, actor, c, &live, lim)
	}()
	return run, nil
}

// autoActions are the live actions one Auto approval covers. Secure
// environment input always waits for the user; GitHub writes, kill, startup
// changes, deployments and site publication are not operator tools.
var autoActions = []string{"run_diagnostic", "propose_file_change", "restart_bot"}

func (s *AIService) awaitAutoEnvelope(ctx context.Context, run *domain.AIRun, lim AIRunLimits) bool {
	// The envelope names its target: it covers this bot or site and no other.
	target := s.targetName(ctx, domain.AIConversation{BotID: run.BotID, SiteID: run.SiteID})
	args, _ := json.Marshal(map[string]any{"actions": autoActions, "limits": lim, "target": target, "target_kind": targetKind(*run)})
	call := domain.AIToolCall{ID: uuid.NewString(), RunID: run.ID, CallIndex: -1, Name: "auto_repair_envelope", ArgumentsJSON: string(args), ApprovalState: "pending", Status: "proposed", CreatedAtMS: s.now()}
	wait := s.expectDecision(call.ID, run.ID)
	_ = s.Store.InsertAIToolCall(ctx, call)
	run.Status = "waiting_approval"
	_ = s.Store.UpdateAIRun(ctx, *run)
	s.emit(run.ID, "approval_required", map[string]any{"tool_call": toolCallView(call), "title": "Approve bounded Auto repair", "detail": map[string]any{"actions": autoActions, "limits": lim, "target": target, "target_kind": targetKind(*run)}})
	ok := wait(ctx)
	call.FinishedAtMS = ptrNow(s.now())
	if !ok {
		call.ApprovalState, call.Status = "rejected", "cancelled"
		_ = s.Store.UpdateAIToolCall(context.WithoutCancel(ctx), call)
		s.finishRun(run, "cancelled", "rejected", "Auto repair was not approved.")
		return false
	}
	call.ApprovalState, call.Status = "approved", "completed"
	_ = s.Store.UpdateAIToolCall(ctx, call)
	now := s.now()
	run.AutoApprovedAtMS = &now
	run.Status = "queued"
	_ = s.Store.UpdateAIRun(ctx, *run)
	return true
}

// expectDecision registers a waiter before the pending state is persisted,
// so a decision that arrives immediately is never lost.
func (s *AIService) expectDecision(callID, runID string) func(context.Context) bool {
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.pending[callID] = pendingApproval{runID, ch}
	s.mu.Unlock()
	return func(ctx context.Context) bool {
		defer func() { s.mu.Lock(); delete(s.pending, callID); s.mu.Unlock() }()
		select {
		case v := <-ch:
			return v
		case <-ctx.Done():
			return false
		}
	}
}

func (s *AIService) Decision(ctx context.Context, actor domain.User, callID string, approve bool) error {
	call, e := s.Store.GetAIToolCall(ctx, callID)
	if e != nil {
		return e
	}
	run, e := s.Store.GetAIRun(ctx, call.RunID)
	if e != nil {
		return e
	}
	c, e := s.Store.GetAIConversation(ctx, run.ConversationID)
	if e != nil {
		return e
	}
	if run.UserID != actor.ID && !actor.IsAdmin() {
		return domain.ErrNotFound
	}
	if e = s.authorizeTarget(ctx, actor, effective(c, run), true); e != nil {
		return e
	}
	if call.ApprovalState != "pending" {
		return fmt.Errorf("%w: this decision was already made", domain.ErrConflict)
	}
	s.mu.Lock()
	p, ok := s.pending[callID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: this run is no longer waiting for a decision", domain.ErrConflict)
	}
	if approve {
		call.ApprovalState = "approved"
	} else {
		call.ApprovalState = "rejected"
		call.Status = "cancelled"
	}
	_ = s.Store.UpdateAIToolCall(ctx, call)
	select {
	case p.ch <- approve:
	default:
	}
	s.emit(run.ID, "status", map[string]any{"approval": call.ApprovalState, "tool_call_id": callID})
	return nil
}

func (s *AIService) Cancel(ctx context.Context, actor domain.User, runID string) error {
	r, e := s.Store.GetAIRun(ctx, runID)
	if e != nil {
		return e
	}
	c, e := s.Store.GetAIConversation(ctx, r.ConversationID)
	if e != nil {
		return e
	}
	if r.UserID != actor.ID && !actor.IsAdmin() {
		return domain.ErrNotFound
	}
	if e = s.authorizeTarget(ctx, actor, effective(c, r), false); e != nil {
		return e
	}
	s.mu.Lock()
	cancel := s.cancels[runID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled" || r.Status == "interrupted" {
		return nil
	}
	r.Status = "cancelled"
	now := s.now()
	r.FinishedAtMS = &now
	_ = s.Store.UpdateAIRun(ctx, r)
	_ = s.Store.CloseAIToolCalls(ctx, r.ID, now)
	s.emit(runID, "cancelled", map[string]any{"status": "cancelled"})
	s.retireStream(runID)
	return nil
}
func (s *AIService) Run(ctx context.Context, actor domain.User, id string) (domain.AIRun, error) {
	r, e := s.Store.GetAIRun(ctx, id)
	if e != nil {
		return r, e
	}
	c, e := s.Store.GetAIConversation(ctx, r.ConversationID)
	if e != nil {
		return r, e
	}
	if r.UserID != actor.ID && !actor.IsAdmin() {
		return r, domain.ErrNotFound
	}
	e = s.authorizeTarget(ctx, actor, effective(c, r), false)
	return r, e
}

func (s *AIService) run(ctx context.Context, actor domain.User, conv domain.AIConversation, run *domain.AIRun, lim AIRunLimits) {
	now := s.now()
	run.StartedAtMS = &now
	run.Status = "running"
	_ = s.Store.UpdateAIRun(ctx, *run)
	s.emit(run.ID, "start", map[string]any{"mode": run.Mode, "limits": lim, "target_kind": targetKind(*run), "target_id": targetID(*run)})
	p, cfg, e := s.providerConfig(ctx, *run.ProviderID)
	if e != nil {
		code, msg := operator.FriendlyError(e)
		s.finishRun(run, "failed", code, msg)
		return
	}
	cfg.Model = run.Model
	messages, e := s.Store.ListAIMessages(ctx, conv.ID, 100)
	if e != nil {
		s.finishRun(run, "failed", "storage", "Could not load the retained conversation.")
		return
	}
	// The run's target is rederived after every tool call, because the model
	// may focus a bot or site when the person was not on one.
	eff := effective(conv, *run)
	var view AIContext
	chat := []operator.Message{{Role: "system"}}
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			content := m.Content
			if m.Role == "user" {
				if mc := parseContext(m.ContextJSON); mc.describe() != "" {
					view = mc
					content = "[Viewing: " + mc.describe() + "]\n" + content
				}
			}
			chat = append(chat, operator.Message{Role: m.Role, Content: content})
		}
	}
	chat[0].Content = s.systemPromptFor(ctx, *run, view, s.targetName(ctx, eff))
	repeat := map[string]int{}
	budget := newAIBudget(lim)
	mutated := false
	callIndex := int64(0)
	for round := 0; round < lim.Rounds; round++ {
		if e = s.reauthorize(ctx, actor, eff, false); e != nil {
			s.finishRun(run, "cancelled", "access_revoked", "Access changed while the run was active.")
			return
		}
		s.emit(run.ID, "status", map[string]any{"label": "Consulting " + p.Name, "round": round + 1})
		var resp operator.Response
		for attempt := 0; attempt < 3; attempt++ {
			resp, e = s.Provider.Complete(ctx, cfg, chat, aiTools(s.diagnosticPolicy(ctx, eff.BotID)), func(d string) { s.emit(run.ID, "delta", map[string]string{"text": d}) })
			if e == nil {
				break
			}
			var pe *operator.ProviderError
			if mutated || !errors.As(e, &pe) || !pe.Temporary() || attempt == 2 {
				break
			}
			delay := pe.RetryAfter
			if delay <= 0 {
				delay = time.Duration(attempt+1) * time.Second
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				e = ctx.Err()
				break
			}
		}
		if e != nil {
			code, msg := operator.FriendlyError(e)
			s.finishRun(run, map[bool]string{true: "cancelled", false: "failed"}[errors.Is(e, context.Canceled)], code, msg)
			return
		}
		run.InputTokens += resp.Usage.PromptTokens
		run.OutputTokens += resp.Usage.CompletionTokens
		_ = s.Store.UpdateAIRun(ctx, *run)
		s.emit(run.ID, "usage", map[string]any{"input_tokens": run.InputTokens, "output_tokens": run.OutputTokens, "estimated_cost_micros": estimatedCost(p, run.InputTokens, run.OutputTokens)})
		if len(resp.Calls) == 0 {
			content := strings.TrimSpace(resp.Content)
			if content == "" {
				content = "The provider ended without an answer."
			}
			m := domain.AIMessage{ID: uuid.NewString(), ConversationID: conv.ID, Role: "assistant", Content: content, CitationsJSON: "[]", CreatedAtMS: s.now()}
			_ = s.Store.InsertAIMessage(ctx, m)
			s.finishRun(run, "completed", "", "")
			return
		}
		chat = append(chat, operator.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.Calls})
		for _, tc := range resp.Calls {
			key := tc.Function.Name + "\x00" + tc.Function.Arguments
			repeat[key]++
			if repeat[key] == 3 {
				s.emit(run.ID, "status", map[string]string{"warning": "The provider repeated an identical tool call three times."})
			}
			if repeat[key] > 3 {
				s.finishRun(run, "failed", "tool_loop", "The provider kept repeating the same tool call.")
				return
			}
			// Providers reuse short call ids across runs, so rows get their
			// own UUID and the provider id is only echoed in the tool message.
			call := domain.AIToolCall{ID: uuid.NewString(), RunID: run.ID, CallIndex: callIndex, Name: tc.Function.Name, ArgumentsJSON: sanitizeArgs(tc.Function.Arguments), ApprovalState: "not_required", Status: "running", CreatedAtMS: s.now()}
			callIndex++
			if tc.ID != "" && len(tc.ID) <= 200 {
				pid := tc.ID
				call.ProviderCallID = &pid
			}
			if e = s.Store.InsertAIToolCall(ctx, call); e != nil {
				s.finishRun(run, "failed", "storage", "Could not record the tool call.")
				return
			}
			s.emit(run.ID, "tool_proposed", toolCallView(call))
			var out string
			var didMutate bool
			if budget.output >= lim.RetainedOutput {
				e = limitReached("bytes of retained tool output", lim.RetainedOutput)
			} else {
				out, didMutate, e = s.executeToolBounded(ctx, actor, eff, run, &call, budget)
				// focus_target may have moved the run onto a bot or site.
				if eff.BotID != run.BotID || eff.SiteID != run.SiteID {
					eff = effective(conv, *run)
					chat[0].Content = s.systemPromptFor(ctx, *run, view, s.targetName(ctx, eff))
				}
			}
			mutated = mutated || didMutate
			finished := s.now()
			call.FinishedAtMS = &finished
			if e != nil {
				call.Status = "failed"
				msg := safeToolError(e)
				call.ErrorMessage = &msg
				out = "Tool failed: " + msg
			} else {
				call.Status = "completed"
			}
			if e != nil {
				// Errors are short and must reach the model intact.
				budget.output += int64(len(out))
				call.Output = out
			} else {
				call.Output = budget.retain(clipService(out, 1<<20))
			}
			_ = s.Store.UpdateAIToolCall(context.WithoutCancel(ctx), call)
			s.emit(run.ID, "tool_output", map[string]any{"id": call.ID, "name": call.Name, "output": call.Output, "status": call.Status, "duration_ms": call.DurationMS, "exit_code": call.ExitCode})
			chat = append(chat, operator.Message{Role: "tool", ToolCallID: tc.ID, Content: clipService(call.Output, 24000)})
		}
	}
	s.finishRun(run, "failed", "round_limit", "The run reached its tool/provider round limit.")
}

func ptrNow(v int64) *int64 { return &v }

func targetKind(r domain.AIRun) string {
	switch {
	case r.BotID != nil:
		return "bot"
	case r.SiteID != nil:
		return "site"
	}
	return ""
}
func targetID(r domain.AIRun) string {
	switch {
	case r.BotID != nil:
		return *r.BotID
	case r.SiteID != nil:
		return *r.SiteID
	}
	return ""
}

// targetName is the focused bot's or site's display name, empty with no target.
func (s *AIService) targetName(ctx context.Context, c domain.AIConversation) string {
	if c.BotID != nil && s.Bots != nil {
		if b, e := s.Bots.Store.GetBot(ctx, *c.BotID); e == nil {
			return b.Name
		}
	}
	if c.SiteID != nil && s.Sites != nil {
		if st, e := s.Sites.Store.GetSite(ctx, *c.SiteID); e == nil {
			return st.Name
		}
	}
	return ""
}

func estimatedCost(p domain.AIProviderProfile, in, out int64) *int64 {
	if p.InputPriceMicros == nil || p.OutputPriceMicros == nil {
		return nil
	}
	v := in**p.InputPriceMicros/1_000_000 + out**p.OutputPriceMicros/1_000_000
	return &v
}
func sanitizeArgs(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return `{}`
	}
	b, _ := json.Marshal(v)
	if len(b) > 65536 {
		return `{}`
	}
	return string(b)
}
func safeToolError(e error) string {
	switch {
	case errors.Is(e, domain.ErrForbidden):
		return "Permission is required for this action."
	case errors.Is(e, domain.ErrNotFound):
		return "The target or resource is no longer available."
	case errors.Is(e, filesystem.ErrPatchConflict):
		return "A person or deployment changed these files after the AI snapshot; nothing was overwritten."
	default:
		return clipService(e.Error(), 800)
	}
}
func clipService(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n] + "…"
}
func (s *AIService) finishRun(r *domain.AIRun, status, code, msg string) {
	now := s.now()
	r.Status = status
	r.FinishedAtMS = &now
	if code != "" {
		r.ErrorCode = &code
	}
	if msg != "" {
		r.ErrorMessage = &msg
	}
	_ = s.Store.UpdateAIRun(context.Background(), *r)
	// A run that ends mid-call (failure, cancellation, limit) must not leave
	// a tool call shown as running forever.
	_ = s.Store.CloseAIToolCalls(context.Background(), r.ID, now)
	s.emit(r.ID, map[string]string{"completed": "done", "cancelled": "cancelled", "failed": "error"}[status], map[string]any{"status": status, "error_code": code, "message": msg})
	s.mu.Lock()
	delete(s.cancels, r.ID)
	s.mu.Unlock()
	s.retireStream(r.ID)
}
func (s *AIService) reauthorize(ctx context.Context, actor domain.User, c domain.AIConversation, mutate bool) error {
	fresh, e := s.Store.GetUserByID(ctx, actor.ID)
	if e != nil || fresh.Disabled {
		return domain.ErrForbidden
	}
	return s.authorizeTarget(ctx, fresh, c, mutate)
}

func (s *AIService) targetWorkspace(ctx context.Context, actor domain.User, c domain.AIConversation, scope string, mutate bool) (aiFiles, string, string, error) {
	if c.BotID != nil && scope != "linked_site" {
		perm := 0
		if mutate {
			perm = domain.PermEditFiles
		}
		b, e := s.Bots.Authorize(ctx, actor, *c.BotID, perm)
		if e != nil {
			return nil, "", "", e
		}
		if mutate {
			// A deployment or restore is replacing the files right now.
			if e := s.Bots.FilesBlocked(b.ID); e != nil {
				return nil, "", "", e
			}
		}
		// A bot on a remote node is reached through its agent only; the
		// panel's own disk is never read or written for it.
		nf, remote, e := s.Bots.RemoteFiles(b, "The AI assistant's file tools")
		if e != nil {
			return nil, "", "", e
		}
		if remote {
			return newRemoteAIFiles(ctx, nf, b.NodeID, b.ID), "bot", b.ID, nil
		}
		w, e := s.Files.Open(b.ID)
		if e != nil {
			return nil, "", "", e
		}
		return localAIFiles{w}, "bot", b.ID, nil
	}
	siteID := ""
	if c.SiteID != nil {
		siteID = *c.SiteID
	} else {
		d, e := s.Sites.GetForBot(ctx, actor, *c.BotID)
		if e != nil {
			return nil, "", "", e
		}
		siteID = d.Site.ID
	}
	min := domain.WorkspaceViewer
	if mutate {
		min = domain.WorkspaceDeveloper
	}
	if _, _, e := s.Sites.Authorize(ctx, actor, siteID, min); e != nil {
		return nil, "", "", e
	}
	w, e := s.Sites.Draft(ctx, actor, siteID)
	if e != nil {
		return nil, "", "", e
	}
	return localAIFiles{w}, "site", siteID, nil
}

func (s *AIService) redactor(ctx context.Context, c domain.AIConversation) *operator.Redactor {
	if c.BotID == nil {
		return operator.NewRedactor(nil)
	}
	env, e := s.Bots.DecryptEnv(ctx, *c.BotID)
	if e != nil {
		return operator.NewRedactor(nil)
	}
	vals := make([]string, 0, len(env))
	for k, v := range env {
		vals = append(vals, v)
		clear([]byte(v))
		delete(env, k)
	}
	r := operator.NewRedactor(vals)
	for i := range vals {
		vals[i] = ""
	}
	return r
}

// readOnlyAITools only read; each call runs under ToolTimeout so a slow or
// unresponsive node, log source or web page cannot hang the run.
var readOnlyAITools = map[string]bool{"list_targets": true, "target_status": true, "read_logs": true, "build_output": true, "list_files": true, "read_file": true, "search_files": true, "environment_names": true, "web_search": true, "web_fetch": true}

// errAIToolTimeout is reported to the model (and the person) when a
// read-only tool does not finish in time.
var errAIToolTimeout = errors.New("the tool did not finish in time and was stopped (the server's node may be offline or slow); try again or use another tool")

func (s *AIService) toolTimeout() time.Duration {
	if s.ToolTimeout > 0 {
		return s.ToolTimeout
	}
	return 90 * time.Second
}

// executeToolBounded runs a read-only tool with a deadline and returns when
// the deadline passes even if the tool ignores its context. The tool works
// on copies of the run and call rows (read-only tools do not change them).
func (s *AIService) executeToolBounded(ctx context.Context, actor domain.User, c domain.AIConversation, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget) (string, bool, error) {
	if !readOnlyAITools[call.Name] {
		return s.executeTool(ctx, actor, c, run, call, budget)
	}
	tctx, cancel := context.WithTimeout(ctx, s.toolTimeout())
	defer cancel()
	type result struct {
		out string
		mut bool
		err error
	}
	ch := make(chan result, 1)
	runCopy, callCopy := *run, *call
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- result{err: errors.New("the tool stopped because of an internal error")}
			}
		}()
		o, m, e := s.executeTool(tctx, actor, c, &runCopy, &callCopy, newAIBudget(budget.lim))
		ch <- result{o, m, e}
	}()
	select {
	case r := <-ch:
		if r.err != nil && ctx.Err() == nil && errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return "", false, errAIToolTimeout
		}
		return r.out, r.mut, r.err
	case <-tctx.Done():
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		s.emit(run.ID, "status", map[string]any{"warning": "A tool call timed out.", "tool_call_id": call.ID})
		return "", false, errAIToolTimeout
	}
}

func (s *AIService) executeTool(ctx context.Context, actor domain.User, c domain.AIConversation, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget) (string, bool, error) {
	var a map[string]json.RawMessage
	if e := json.Unmarshal([]byte(call.ArgumentsJSON), &a); e != nil {
		return "", false, domain.Invalid("tool arguments are invalid")
	}
	str := func(k string) string { var v string; _ = json.Unmarshal(a[k], &v); return v }
	red := s.redactor(ctx, c)
	if c.BotID == nil && c.SiteID == nil && !targetless[call.Name] {
		return "", false, domain.Invalid("no bot or site is in focus: call list_targets and focus_target, or ask the person which one they mean")
	}
	switch call.Name {
	case "list_targets":
		out, e := s.toolListTargets(ctx, actor)
		return out, false, e
	case "focus_target":
		out, e := s.toolFocus(ctx, actor, c, run, str("kind"), str("id"))
		return out, false, e
	case "read_logs":
		var n int
		_ = json.Unmarshal(a["lines"], &n)
		out, e := s.toolReadLogs(ctx, actor, c, n)
		return red.Text(out), false, e
	case "build_output":
		out, e := s.toolBuildOutput(ctx, actor, c, str("operation_id"))
		return red.Text(out), false, e
	case "target_status":
		if e := s.reauthorize(ctx, actor, c, false); e != nil {
			return "", false, e
		}
		if c.BotID != nil {
			b, e := s.Bots.Authorize(ctx, actor, *c.BotID, 0)
			if e != nil {
				return "", false, e
			}
			raw, _ := json.Marshal(map[string]any{"kind": "bot", "name": b.Name, "runtime": b.Runtime, "desired_state": b.DesiredState, "observed_state": b.ObservedState, "generation": b.Generation, "observed_generation": b.ObservedGeneration, "last_exit_code": b.LastExitCode, "last_error": b.LastError, "startup_argv": b.Argv, "network_enabled": !b.NetworkDisabled})
			return red.Text(string(raw)), false, nil
		}
		d, e := s.Sites.Get(ctx, actor, *c.SiteID)
		if e != nil {
			return "", false, e
		}
		raw, _ := json.Marshal(map[string]any{"kind": "site", "name": d.Site.Name, "mode": d.Site.Mode, "disabled": d.Site.Disabled, "current_release": d.Site.CurrentRelease, "latest_job": d.Job})
		return string(raw), false, nil
	case "list_files":
		w, _, _, e := s.targetWorkspace(ctx, actor, c, "target", false)
		if e != nil {
			return "", false, e
		}
		defer w.Close()
		p := str("path")
		if p == "" {
			p = "."
		}
		if operator.ProtectedPath(p) {
			return "", false, domain.ErrForbidden
		}
		es, e := w.List(p)
		if e != nil {
			return "", false, e
		}
		if len(es) > 500 {
			es = es[:500]
		}
		raw, _ := json.Marshal(es)
		return string(raw), false, nil
	case "read_file":
		p := str("path")
		if operator.ProtectedPath(p) {
			return "", false, domain.ErrForbidden
		}
		w, _, _, e := s.targetWorkspace(ctx, actor, c, "target", false)
		if e != nil {
			return "", false, e
		}
		defer w.Close()
		b, e := w.Read(p, maxAIFile)
		if e != nil {
			return "", false, e
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return "", false, domain.Invalid("binary files cannot enter AI context")
		}
		return red.Text(clipService(string(b), 24000)), false, nil
	case "search_files":
		q := str("query")
		if q == "" || len(q) > 300 {
			return "", false, domain.Invalid("search query is invalid")
		}
		w, _, _, e := s.targetWorkspace(ctx, actor, c, "target", false)
		if e != nil {
			return "", false, e
		}
		defer w.Close()
		matches, e := searchWorkspace(w, q)
		if e != nil {
			return "", false, e
		}
		return red.Text(strings.Join(matches, "\n")), false, nil
	case "environment_names":
		if c.BotID == nil {
			return "No environment belongs to a standalone site.", false, nil
		}
		vars, e := s.Bots.ListEnv(ctx, actor, *c.BotID)
		if e != nil {
			return "", false, e
		}
		names := make([]string, len(vars))
		for i, v := range vars {
			names[i] = v.Name
		}
		sort.Strings(names)
		raw, _ := json.Marshal(names)
		return string(raw), false, nil
	case "request_environment_values":
		if c.BotID == nil {
			return "", false, domain.Invalid("standalone sites do not have environment values")
		}
		if _, e := s.Bots.Authorize(ctx, actor, *c.BotID, domain.PermManageEnv); e != nil {
			return "", false, e
		}
		var names []string
		if json.Unmarshal(a["names"], &names) != nil || len(names) == 0 {
			return "", false, domain.Invalid("environment names are invalid")
		}
		call.ApprovalState, call.Status = "pending", "proposed"
		wait := s.expectDecision(call.ID, run.ID)
		_ = s.Store.UpdateAIToolCall(ctx, *call)
		run.Status = "waiting_approval"
		_ = s.Store.UpdateAIRun(ctx, *run)
		s.emit(run.ID, "approval_required", map[string]any{"tool_call": toolCallView(*call), "title": "Secure environment input", "names": names, "secure_input": true})
		ok := wait(ctx)
		call.ApprovalState = map[bool]string{true: "approved", false: "rejected"}[ok]
		run.Status = "running"
		_ = s.Store.UpdateAIRun(ctx, *run)
		if !ok {
			return "Secure input was cancelled.", false, nil
		}
		return "The requested environment variable names were configured through the secure form. Their values were not shown to the model.", true, nil
	case "web_search":
		v, cfg, e := s.searchConfig(ctx)
		if e != nil {
			return "", false, e
		}
		if !v.SearchEnabled {
			return "", false, domain.Invalid("web search is disabled by an administrator")
		}
		res, e := s.Research.Search(ctx, cfg, str("query"))
		if e != nil {
			return "", false, e
		}
		raw, _ := json.Marshal(res)
		s.emit(run.ID, "citation", res.Results)
		return red.Text(string(raw)), false, nil
	case "web_fetch":
		v, _, e := s.searchConfig(ctx)
		if e != nil {
			return "", false, e
		}
		if !v.FetchEnabled {
			return "", false, domain.Invalid("page fetching is disabled by an administrator")
		}
		title, text, e := s.Research.Fetch(ctx, str("url"))
		if e != nil {
			return "", false, e
		}
		s.emit(run.ID, "citation", map[string]any{"title": title, "url": str("url"), "retrieved_at_ms": s.now()})
		return red.Text(title + "\n\n" + text), false, nil
	case "propose_file_change":
		return s.fileChange(ctx, actor, c, run, call, budget, str("scope"), str("path"), str("content"), str("summary"), a["delete"])
	case "run_diagnostic":
		if c.BotID == nil {
			return "", false, domain.Invalid("standalone sites do not have a runtime diagnostic image")
		}
		var argv []string
		if json.Unmarshal(a["argv"], &argv) != nil || len(argv) == 0 {
			return "", false, domain.Invalid("argv is invalid")
		}
		label := red.Text(clipService(strings.Join(argv, " "), 200))
		db, e := s.Bots.Authorize(ctx, actor, *c.BotID, domain.PermEditFiles)
		if e != nil {
			s.audit(ctx, actor, run, "bot", *c.BotID, "ai.diagnostic", label, "denied")
			return "", false, e
		}
		// A remote server's diagnostic runs on its node (its files are
		// there); the panel's own disk and Docker are never used for it.
		if e := s.diagnosticAvailable(db); e != nil {
			return "", false, e
		}
		if e := budget.diagnostic(); e != nil {
			return "", false, e
		}
		if run.Mode == AIApproval && !s.awaitMutation(ctx, run, call, "Run isolated diagnostic", argv) {
			return "Rejected by user.", false, nil
		}
		// Access may have changed while the approval card was open.
		b, e := s.Bots.Authorize(ctx, actor, *c.BotID, domain.PermEditFiles)
		if e != nil {
			s.audit(ctx, actor, run, "bot", *c.BotID, "ai.diagnostic", label, "denied")
			return "", false, e
		}
		if e := s.diagnosticAvailable(b); e != nil {
			return "", false, e // moved to another node, or its node went offline
		}
		budget.diagnostics++
		start := time.Now()
		var res domain.DiagnosticResult
		if s.Bots.remote(b.NodeID) {
			res, e = s.RemoteDiagnostic.RunDiagnostic(ctx, b.NodeID, *c.BotID, b.Runtime, argv)
		} else {
			res, e = s.Diagnostic.RunDiagnostic(ctx, *c.BotID, b.Runtime, argv)
		}
		dur := time.Since(start)
		ms := dur.Milliseconds()
		call.DurationMS = &ms
		call.ExitCode = &res.ExitCode
		s.audit(ctx, actor, run, "bot", *c.BotID, "ai.diagnostic", label, outcomeOf(e))
		return red.Text(clipService(res.Output, 1<<20)), false, e
	case "restart_bot":
		if c.BotID == nil {
			return "", false, domain.Invalid("a site cannot be restarted")
		}
		if _, e := s.Bots.Authorize(ctx, actor, *c.BotID, domain.PermPower); e != nil {
			s.audit(ctx, actor, run, "bot", *c.BotID, "ai.restart", "", "denied")
			return "", false, e
		}
		if e := budget.lifecycleAction(); e != nil {
			return "", false, e
		}
		if run.Mode == AIApproval && !s.awaitMutation(ctx, run, call, "Restart bot", nil) {
			return "Rejected by user.", false, nil
		}
		budget.lifecycle++
		b, e := s.Bots.Restart(ctx, actor, *c.BotID)
		s.audit(ctx, actor, run, "bot", *c.BotID, "ai.restart", "", outcomeOf(e))
		if e != nil {
			return "", false, e
		}
		return fmt.Sprintf("Restart requested. generation=%d desired_state=%s", b.Generation, b.DesiredState), true, nil
	default:
		return "", false, domain.Invalid("unknown tool")
	}
}

// SecureInput sends values straight to the environment service. Values never
// enter tool JSON, messages, stream events, or audit targets.
func (s *AIService) SecureInput(ctx context.Context, actor domain.User, callID string, values map[string]string) error {
	call, e := s.Store.GetAIToolCall(ctx, callID)
	if e != nil {
		return e
	}
	if call.Name != "request_environment_values" || call.ApprovalState != "pending" {
		return fmt.Errorf("%w: secure input is not pending", domain.ErrConflict)
	}
	run, e := s.Store.GetAIRun(ctx, call.RunID)
	if e != nil {
		return e
	}
	c, e := s.Store.GetAIConversation(ctx, run.ConversationID)
	if e != nil {
		return e
	}
	if run.UserID != actor.ID && !actor.IsAdmin() {
		return domain.ErrNotFound
	}
	c = effective(c, run)
	if c.BotID == nil {
		return domain.ErrForbidden
	}
	if _, e = s.Bots.Authorize(ctx, actor, *c.BotID, domain.PermManageEnv); e != nil {
		return e
	}
	var args struct {
		Names []string `json:"names"`
	}
	if json.Unmarshal([]byte(call.ArgumentsJSON), &args) != nil {
		return domain.Invalid("secure input request is invalid")
	}
	allowed := map[string]bool{}
	for _, n := range args.Names {
		allowed[n] = true
	}
	if len(values) == 0 {
		return domain.Invalid("enter at least one requested value")
	}
	for n := range values {
		if !allowed[n] {
			return domain.Invalid("a value was supplied for an unrequested name")
		}
	}
	names := strings.Join(sortedKeys(values), ", ")
	if e = s.Bots.SetEnv(ctx, actor, *c.BotID, values); e != nil {
		s.audit(ctx, actor, &run, "bot", *c.BotID, "ai.env_input", names, outcomeOf(e))
		return e
	}
	s.audit(ctx, actor, &run, "bot", *c.BotID, "ai.env_input", names, "ok")
	call.ApprovalState = "approved"
	_ = s.Store.UpdateAIToolCall(ctx, call)
	s.mu.Lock()
	p, ok := s.pending[callID]
	s.mu.Unlock()
	if ok {
		select {
		case p.ch <- true:
		default:
		}
	}
	s.emit(run.ID, "status", map[string]any{"tool_call_id": callID, "secure_names_configured": sortedKeys(values)})
	return nil
}

func sortedKeys(v map[string]string) []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *AIService) awaitMutation(ctx context.Context, run *domain.AIRun, call *domain.AIToolCall, title string, detail any) bool {
	call.ApprovalState = "pending"
	call.Status = "proposed"
	wait := s.expectDecision(call.ID, run.ID)
	_ = s.Store.UpdateAIToolCall(ctx, *call)
	run.Status = "waiting_approval"
	_ = s.Store.UpdateAIRun(ctx, *run)
	s.emit(run.ID, "approval_required", map[string]any{"tool_call": toolCallView(*call), "title": title, "detail": detail})
	ok := wait(ctx)
	// Keep the in-memory row in step with Decision, which already stored it.
	call.ApprovalState = map[bool]string{true: "approved", false: "rejected"}[ok]
	run.Status = "running"
	_ = s.Store.UpdateAIRun(ctx, *run)
	call.Status = "running"
	return ok
}

// maxAISearchReads bounds how many files one search reads (each is a request
// to the node for a remote bot).
const maxAISearchReads = 2000

func searchWorkspace(w aiFiles, q string) ([]string, error) {
	var out []string
	reads := 0
	var walk func(string) error
	walk = func(dir string) error {
		es, e := w.List(dir)
		if e != nil {
			return e
		}
		for _, x := range es {
			p := x.Name
			if dir != "." {
				p = path.Join(dir, x.Name)
			}
			if operator.ProtectedPath(p) || x.Symlink {
				continue
			}
			if x.IsDir {
				if len(out) < 200 {
					if e := walk(p); e != nil {
						return e
					}
				}
				continue
			}
			if x.Size > maxAIFile {
				continue
			}
			if reads >= maxAISearchReads {
				return nil
			}
			reads++
			b, e := w.Read(p, maxAIFile)
			if e != nil || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
				continue
			}
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, q) {
					out = append(out, fmt.Sprintf("%s:%d: %s", p, i+1, clipService(strings.TrimSpace(line), 300)))
					if len(out) >= 200 {
						return nil
					}
				}
			}
		}
		return nil
	}
	return out, walk(".")
}

func gzipText(b []byte) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	var out bytes.Buffer
	z := gzip.NewWriter(&out)
	if _, e := z.Write(b); e != nil {
		return nil, e
	}
	if e := z.Close(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func gunzipText(b []byte) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	z, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	return io.ReadAll(io.LimitReader(z, maxAIFile+1))
}
func simpleDiff(p string, before, after []byte) string {
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", p, p)
	bs, as := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	out.WriteString("@@ full file @@\n")
	for _, l := range bs {
		out.WriteString("-")
		out.WriteString(l)
		out.WriteByte('\n')
	}
	for _, l := range as {
		out.WriteString("+")
		out.WriteString(l)
		out.WriteByte('\n')
	}
	return clipService(out.String(), 2<<20)
}

func (s *AIService) fileChange(ctx context.Context, actor domain.User, c domain.AIConversation, run *domain.AIRun, call *domain.AIToolCall, budget *aiBudget, scope, p, content, summary string, deleteRaw json.RawMessage) (string, bool, error) {
	if operator.ProtectedPath(p) {
		return "", false, domain.ErrForbidden
	}
	if p == "" || len(p) > 500 {
		return "", false, domain.Invalid("path is invalid")
	}
	var del bool
	_ = json.Unmarshal(deleteRaw, &del)
	w, kind, target, e := s.targetWorkspace(ctx, actor, c, scope, true)
	if e != nil {
		return "", false, e
	}
	defer w.Close()
	var before []byte
	rev, e := w.RevisionOrMissing(p)
	if e != nil {
		return "", false, e
	}
	if rev != "" {
		before, e = w.Read(p, maxAIFile)
		if e != nil {
			return "", false, e
		}
		if !utf8.Valid(before) || bytes.IndexByte(before, 0) >= 0 {
			return "", false, domain.Invalid("binary files cannot be changed")
		}
	}
	var after []byte
	if !del {
		after = []byte(content)
		if len(after) > maxAIFile || !utf8.Valid(after) || bytes.IndexByte(after, 0) >= 0 {
			return "", false, domain.Invalid("proposed text file is invalid or too large")
		}
	}
	// The redactor is also applied to the stored diff, which people review;
	// the compressed snapshots keep the exact bytes for Undo.
	red := s.redactor(ctx, c)
	if !del && red.Text(content) != content {
		return "", false, domain.Invalid("proposed content contains a configured secret value")
	}
	size := int64(len(after))
	if del {
		size = int64(len(before))
	}
	fileKey := kind + ":" + target + ":" + path.Clean(p)
	if e := budget.change(fileKey, size); e != nil {
		return "", false, e
	}
	bg, _ := gzipText(before)
	ag, _ := gzipText(after)
	op := "modify"
	var beforeRev *string
	if rev == "" {
		op = "add"
	} else {
		beforeRev = &rev
	}
	if del {
		op = "delete"
	}
	change := domain.AIChangeSet{ID: uuid.NewString(), RunID: run.ID, TargetKind: kind, TargetID: target, Status: "draft", Summary: clipService(summary, 2000), CreatedAtMS: s.now(), Files: []domain.AIChangeFile{{Path: p, Operation: op, BeforeGzip: bg, AfterGzip: ag, BeforeRevision: beforeRev, Mode: 0o640, Diff: red.Text(simpleDiff(p, before, after))}}}
	change.Files[0].ChangeSetID = change.ID
	if e = s.Store.InsertAIChangeSet(ctx, change); e != nil {
		return "", false, e
	}
	s.emit(run.ID, "change_set", publicChange(change))
	if run.Mode == AIApproval && !s.awaitMutation(ctx, run, call, "Apply reviewed file change", publicChange(change)) {
		return "Change was drafted but not applied.", false, nil
	}
	// Access may have changed while the approval card was open.
	if e := s.authorizeChangeTarget(ctx, actor, kind, target); e != nil {
		s.audit(ctx, actor, run, kind, target, "ai.file_apply", p, "denied")
		return "", false, e
	}
	budget.applies++
	pf := filesystem.PatchFile{Path: p, BeforeRevision: rev, After: after, Mode: 0o640}
	if del {
		pf.After = nil
	}
	commit, e := w.ApplyPatch([]filesystem.PatchFile{pf}, maxAIFile, maxAIChange)
	if e != nil {
		change.Status = "conflicted"
		_ = s.Store.UpdateAIChangeSet(context.WithoutCancel(ctx), change)
		s.emit(run.ID, "change_set", publicChange(change))
		s.audit(ctx, actor, run, kind, target, "ai.file_apply", p, "failed")
		return "", false, e
	}
	change.Status = "applied"
	now := s.now()
	change.AppliedAtMS = &now
	if !del {
		r, e := commit.Revision(p)
		if e != nil {
			_ = commit.Rollback()
			return "", false, e
		}
		change.Files[0].AfterRevision = &r
	}
	if e = s.Store.UpdateAIChangeSet(ctx, change); e != nil {
		_ = commit.Rollback()
		return "", false, e
	}
	if e = commit.Finish(); e != nil {
		if errors.Is(e, errPatchRolledBack) {
			// The previous file is back: record that nothing was applied.
			change.Status, change.AppliedAtMS, change.Files[0].AfterRevision = "conflicted", nil, nil
			_ = s.Store.UpdateAIChangeSet(context.WithoutCancel(ctx), change)
			s.emit(run.ID, "change_set", publicChange(change))
		}
		s.audit(ctx, actor, run, kind, target, "ai.file_apply", p, "failed")
		return "", false, e
	}
	budget.files[fileKey] = true
	budget.bytes += size
	s.audit(ctx, actor, run, kind, target, "ai.file_apply", p, "ok")
	s.emit(run.ID, "change_set", publicChange(change))
	return "Applied change set " + change.ID + ". Undo remains available while file revisions still match.", true, nil
}

func (s *AIService) authorizeChangeTarget(ctx context.Context, actor domain.User, kind, target string) error {
	fresh, e := s.Store.GetUserByID(ctx, actor.ID)
	if e != nil || fresh.Disabled {
		return domain.ErrForbidden
	}
	if kind == "bot" {
		_, e = s.Bots.Authorize(ctx, fresh, target, domain.PermEditFiles)
		return e
	}
	_, _, e = s.Sites.Authorize(ctx, fresh, target, domain.WorkspaceDeveloper)
	return e
}

func outcomeOf(e error) string {
	switch {
	case e == nil:
		return "ok"
	case errors.Is(e, domain.ErrForbidden), errors.Is(e, domain.ErrNotFound):
		return "denied"
	default:
		return "failed"
	}
}

// audit records a live action the model took for actor, in either mode. The
// target is a path, argv summary or variable names, never content or values.
func (s *AIService) audit(ctx context.Context, actor domain.User, run *domain.AIRun, kind, targetID, action, target, outcome string) {
	if s.Audit == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	label := actor.Email + " via AI operator"
	if run != nil && run.Mode == AIAuto {
		label += " (Auto)"
	}
	ev := domain.AuditEvent{Action: action, Outcome: outcome, ActorID: &actor.ID, ActorLabel: &label}
	if target != "" {
		t := clipService(target, 300)
		ev.Target = &t
	}
	id := targetID
	if kind == "bot" {
		ev.BotID = &id
		if s.Bots != nil {
			if b, e := s.Bots.Store.GetBot(ctx, id); e == nil {
				ev.BotName = &b.Name
			}
		}
	} else {
		ev.SiteID = &id
		if s.Sites != nil {
			if st, e := s.Sites.Store.GetSite(ctx, id); e == nil {
				ev.SiteName = &st.Name
			}
		}
	}
	s.Audit.Record(ctx, ev)
}

func toolCallView(c domain.AIToolCall) map[string]any {
	return map[string]any{"id": c.ID, "run_id": c.RunID, "call_index": c.CallIndex, "name": c.Name, "arguments_json": c.ArgumentsJSON, "output": c.Output, "approval_state": c.ApprovalState, "status": c.Status, "exit_code": c.ExitCode, "duration_ms": c.DurationMS, "error_message": c.ErrorMessage, "created_at_ms": c.CreatedAtMS, "finished_at_ms": c.FinishedAtMS}
}

// AIRunDetail is a run with its tool calls and change sets, for restoring
// the run inspector after a reload.
type AIRunDetail struct {
	Run        domain.AIRun
	ToolCalls  []map[string]any
	ChangeSets []map[string]any
}

// ConversationRuns returns the latest runs of a conversation, newest first.
func (s *AIService) ConversationRuns(ctx context.Context, actor domain.User, conversationID string, limit int) ([]AIRunDetail, error) {
	c, e := s.Store.GetAIConversation(ctx, conversationID)
	if e != nil {
		return nil, e
	}
	if e = s.authorizeTarget(ctx, actor, c, false); e != nil {
		return nil, e
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	runs, e := s.Store.ListAIRuns(ctx, c.ID, limit)
	if e != nil {
		return nil, e
	}
	out := make([]AIRunDetail, 0, len(runs))
	for _, r := range runs {
		// Access to a bot or site can end after a run: hide that run's output.
		if s.authorizeTarget(ctx, actor, effective(c, r), false) != nil {
			continue
		}
		calls, e := s.Store.ListAIToolCalls(ctx, r.ID)
		if e != nil {
			return nil, e
		}
		changes, e := s.Store.ListAIChangeSets(ctx, r.ID)
		if e != nil {
			return nil, e
		}
		d := AIRunDetail{Run: r, ToolCalls: make([]map[string]any, len(calls)), ChangeSets: make([]map[string]any, len(changes))}
		ended := r.Status != "queued" && r.Status != "running" && r.Status != "waiting_approval"
		for i, x := range calls {
			if ended && (x.Status == "running" || x.Status == "proposed") {
				// Stored before open calls were closed with their run.
				x.Status = map[bool]string{true: "cancelled", false: "failed"}[x.Status == "proposed"]
				if x.ApprovalState == "pending" {
					x.ApprovalState = "rejected"
				}
				if x.ErrorMessage == nil {
					m := "The run ended before this call finished."
					x.ErrorMessage = &m
				}
			}
			d.ToolCalls[i] = toolCallView(x)
		}
		for i, x := range changes {
			d.ChangeSets[i] = publicChange(x)
		}
		out = append(out, d)
	}
	return out, nil
}

func publicChange(c domain.AIChangeSet) map[string]any {
	files := make([]map[string]any, 0, len(c.Files))
	for _, f := range c.Files {
		files = append(files, map[string]any{"path": f.Path, "operation": f.Operation, "diff": f.Diff, "before_revision": f.BeforeRevision, "after_revision": f.AfterRevision})
	}
	return map[string]any{"id": c.ID, "run_id": c.RunID, "status": c.Status, "summary": c.Summary, "target_kind": c.TargetKind, "target_id": c.TargetID, "created_at_ms": c.CreatedAtMS, "applied_at_ms": c.AppliedAtMS, "reverted_at_ms": c.RevertedAtMS, "files": files}
}
func (s *AIService) ChangeSet(ctx context.Context, actor domain.User, id string) (domain.AIChangeSet, error) {
	c, e := s.Store.GetAIChangeSet(ctx, id)
	if e != nil {
		return c, e
	}
	r, e := s.Store.GetAIRun(ctx, c.RunID)
	if e != nil {
		return c, e
	}
	conv, e := s.Store.GetAIConversation(ctx, r.ConversationID)
	if e != nil {
		return c, e
	}
	if r.UserID != actor.ID && !actor.IsAdmin() {
		return c, domain.ErrNotFound
	}
	e = s.authorizeTarget(ctx, actor, effective(conv, r), false)
	return c, e
}
func (s *AIService) RevertChangeSet(ctx context.Context, actor domain.User, id string) error {
	c, e := s.ChangeSet(ctx, actor, id)
	if e != nil {
		return e
	}
	if c.Status != "applied" {
		return fmt.Errorf("%w: only an applied change set can be undone", domain.ErrConflict)
	}
	r, _ := s.Store.GetAIRun(ctx, c.RunID)
	conv, _ := s.Store.GetAIConversation(ctx, r.ConversationID)
	conv = effective(conv, r)
	if c.TargetKind == "bot" && conv.BotID == nil {
		// The run's focus was never recorded: the change set names its own target.
		conv.BotID, conv.SiteID = &c.TargetID, nil
	} else if c.TargetKind == "site" && conv.SiteID == nil && conv.BotID == nil {
		conv.SiteID = &c.TargetID
	}
	w, _, _, e := s.targetWorkspace(ctx, actor, conv, map[bool]string{true: "linked_site", false: "target"}[c.TargetKind == "site" && conv.BotID != nil], true)
	if e != nil {
		return e
	}
	defer w.Close()
	var patches []filesystem.PatchFile
	for _, f := range c.Files {
		before, e := gunzipText(f.BeforeGzip)
		if e != nil {
			return e
		}
		expected := ""
		if f.AfterRevision != nil {
			expected = *f.AfterRevision
		}
		pf := filesystem.PatchFile{Path: f.Path, BeforeRevision: expected, After: before, Mode: fs.FileMode(f.Mode)}
		if f.Operation == "add" {
			pf.After = nil
		}
		patches = append(patches, pf)
	}
	commit, e := w.ApplyPatch(patches, maxAIFile, maxAIChange)
	if e != nil {
		return e
	}
	c.Status = "reverted"
	now := s.now()
	c.RevertedAtMS = &now
	if e = s.Store.UpdateAIChangeSet(ctx, c); e != nil {
		_ = commit.Rollback()
		return e
	}
	if e = commit.Finish(); e != nil {
		if errors.Is(e, errPatchRolledBack) {
			// The undo was rolled back on the node: the change is still applied.
			c.Status, c.RevertedAtMS = "applied", nil
			_ = s.Store.UpdateAIChangeSet(context.WithoutCancel(ctx), c)
		}
		return e
	}
	return nil
}
