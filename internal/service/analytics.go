package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// AnalyticsStore is the persistence surface for bot-pushed telemetry.
type AnalyticsStore interface {
	GetBotIDByTelemetryKey(ctx context.Context, hash []byte) (string, error)
	InsertBotTelemetry(ctx context.Context, rows []domain.BotTelemetryLog) error
	ListBotTelemetry(ctx context.Context, botID, kind string, sinceMS int64, limit int) ([]domain.BotTelemetryLog, error)
	AggregateBotCommands(ctx context.Context, botID string, sinceMS int64, limit int) ([]sqlite.CommandUsage, error)
	ListBotEvents(ctx context.Context, botID string, limit int) ([]domain.BotTelemetryLog, error)
	HasTelemetryKey(ctx context.Context, botID string) (bool, error)
	SetBotTelemetryKey(ctx context.Context, botID string, hash []byte, nowMS int64) error
	PruneBotTelemetry(ctx context.Context, beforeMS int64, batch int) (int64, error)
	BotsOverTelemetryCap(ctx context.Context, keep int) ([]string, error)
	TrimBotTelemetry(ctx context.Context, botID string, keep, batch int) (int64, error)
	UpsertBotWidgets(ctx context.Context, widgets []domain.BotWidget) error
	ListBotWidgets(ctx context.Context, botID string) ([]domain.BotWidget, error)
	DeleteBotWidgets(ctx context.Context, botID string, keys []string) error
	SetBotDiscordIdentity(ctx context.Context, botID, userID, username, avatarURL string, nowMS int64) error
}

// Limits on what a bot may push.
const (
	MaxTelemetryBody    = 64 << 10
	maxStatsPerPush     = 16
	maxCommandsPerPush  = 20
	maxEventsPerPush    = 10
	maxWidgetsPerPush   = 48
	maxEventData        = 1024
	statMinInterval     = 10 * time.Second // per (bot, stat): faster samples are dropped
	MaxTelemetryRowsBot = 20000
	telemetryKeyPrefix  = "bpt_"
	maxSeriesRows       = 6000
)

var telemetryName = regexp.MustCompile(`^[A-Za-z0-9_.:\- ]{1,64}$`)

// TelemetryPayload is what a bot pushes.
type TelemetryPayload struct {
	// Ready is the bot's own view of its Discord connection (optional).
	Ready    *bool `json:"ready"`
	Identity *struct {
		ID        string `json:"id"`
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url"`
	} `json:"identity"`
	Stats    map[string]float64 `json:"stats"`
	Commands []struct {
		Name  string `json:"name"`
		Count *int   `json:"count"`
	} `json:"commands"`
	Events []struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	} `json:"events"`
	Widgets []struct {
		Key        string          `json:"key"`
		Kind       string          `json:"kind"`
		Title      string          `json:"title"`
		Group      string          `json:"group"`
		Span       int             `json:"span"`
		MinHeight  int             `json:"min_height"`
		Position   int             `json:"position"`
		TTLSeconds int             `json:"ttl_seconds"`
		Data       json.RawMessage `json:"data"`
	} `json:"widgets"`
	Unpublish []string `json:"unpublish"`
}

// Analytics ingests and reports bot-pushed metrics.
type Analytics struct {
	Store AnalyticsStore
	Now   func() time.Time
	// Heartbeat, when set, is told about every accepted push (application
	// health is "the bot is still reporting").
	Heartbeat func(ctx context.Context, botID string, ready *bool)

	mu       sync.Mutex
	lastStat map[string]time.Time
	rate     map[string]*window
}

type window struct {
	n     int
	start time.Time
}

// MaxPushesPerMinute bounds pushes per bot (HTTP requests and WebSocket frames).
const MaxPushesPerMinute = 60

// Allow reports whether the bot may push now (fixed one-minute window).
func (a *Analytics) Allow(botID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rate == nil {
		a.rate = map[string]*window{}
	}
	now := a.now()
	w := a.rate[botID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &window{start: now}
		a.rate[botID] = w
	}
	w.n++
	return w.n <= MaxPushesPerMinute
}

func (a *Analytics) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Authenticate resolves a bearer telemetry key to a bot.
func (a *Analytics) Authenticate(ctx context.Context, key string) (string, error) {
	if !strings.HasPrefix(key, telemetryKeyPrefix) || len(key) > 128 {
		return "", domain.ErrUnauthorized
	}
	id, err := a.Store.GetBotIDByTelemetryKey(ctx, auth.HashToken(key))
	if errors.Is(err, domain.ErrNotFound) {
		return "", domain.ErrUnauthorized
	}
	return id, err
}

// DeleteWidgets explicitly unpublishes widgets for an authenticated dashboard user.
func (a *Analytics) DeleteWidgets(ctx context.Context, botID string, keys []string) error {
	if len(keys) == 0 || len(keys) > maxWidgetsPerPush {
		return domain.Invalid("provide between 1 and 48 widget keys")
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if !telemetryName.MatchString(key) || seen[key] {
			return domain.Invalid("widget keys must be unique valid widget identifiers")
		}
		seen[key] = true
	}
	return a.Store.DeleteBotWidgets(ctx, botID, keys)
}

// Ingest validates and stores a push. Stat samples that arrive faster than
// statMinInterval per name are dropped (still acknowledged) to bound storage.
func (a *Analytics) Ingest(ctx context.Context, botID string, p TelemetryPayload) (stored int, err error) {
	if len(p.Stats) > maxStatsPerPush {
		return 0, domain.Invalid("stats may contain at most 16 entries per push")
	}
	if len(p.Commands) > maxCommandsPerPush {
		return 0, domain.Invalid("commands may contain at most 20 entries per push")
	}
	if len(p.Events) > maxEventsPerPush {
		return 0, domain.Invalid("events may contain at most 10 entries per push")
	}
	if len(p.Widgets) > maxWidgetsPerPush {
		return 0, domain.Invalid("widgets may contain at most 48 entries per push")
	}
	if len(p.Unpublish) > maxWidgetsPerPush {
		return 0, domain.Invalid("unpublish may contain at most 48 widget keys per push")
	}
	now := a.now()
	ms := now.UnixMilli()
	var rows []domain.BotTelemetryLog

	names := make([]string, 0, len(p.Stats))
	for n := range p.Stats {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := p.Stats[n]
		if !telemetryName.MatchString(n) || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e15 {
			return 0, domain.Invalid("invalid stat name or value")
		}
		if !a.stat(botID, n, now) {
			continue
		}
		v2 := v
		rows = append(rows, domain.BotTelemetryLog{BotID: botID, RecordedAtMS: ms, Kind: "stat", Name: n, Value: &v2})
	}
	for _, c := range p.Commands {
		if !telemetryName.MatchString(c.Name) {
			return 0, domain.Invalid("invalid command name")
		}
		n := 1
		if c.Count != nil {
			n = *c.Count
		}
		if n < 1 || n > 1_000_000 {
			return 0, domain.Invalid("command count must be between 1 and 1000000")
		}
		v := float64(n)
		rows = append(rows, domain.BotTelemetryLog{BotID: botID, RecordedAtMS: ms, Kind: "command", Name: c.Name, Value: &v})
	}
	for _, e := range p.Events {
		if !telemetryName.MatchString(e.Name) {
			return 0, domain.Invalid("invalid event name")
		}
		r := domain.BotTelemetryLog{BotID: botID, RecordedAtMS: ms, Kind: "event", Name: e.Name}
		if len(e.Data) > 0 && !bytes.Equal(bytes.TrimSpace(e.Data), []byte("null")) {
			if len(e.Data) > maxEventData || !json.Valid(e.Data) {
				return 0, domain.Invalid("event data must be valid JSON of at most 1024 bytes")
			}
			s := string(e.Data)
			r.PayloadJSON = &s
		}
		rows = append(rows, r)
	}
	widgets := make([]domain.BotWidget, 0, len(p.Widgets))
	seenWidgets := map[string]bool{}
	for _, w := range p.Widgets {
		if !telemetryName.MatchString(w.Key) {
			return 0, domain.Invalid("widget key must be 1-64 letters, numbers, spaces, dots, colons, underscores or hyphens")
		}
		if seenWidgets[w.Key] {
			return 0, domain.Invalid("widget key appears more than once: " + w.Key)
		}
		title := strings.TrimSpace(w.Title)
		if len(title) < 1 || len(title) > 80 {
			return 0, domain.Invalid("widget " + w.Key + " title must be 1-80 characters")
		}
		if w.Position < 0 || w.Position > 1000 {
			return 0, domain.Invalid("widget " + w.Key + " position must be between 0 and 1000")
		}
		seenWidgets[w.Key] = true
		switch w.Kind {
		case "metric", "status", "progress", "text", "chart", "table", "link", "line", "area", "donut", "gauge", "heatmap", "sparkline", "kv", "markdown", "image", "log", "code":
		default:
			return 0, domain.Invalid("widget " + w.Key + " has unknown kind " + w.Kind)
		}
		if len(w.Data) == 0 || len(w.Data) > 4096 || !json.Valid(w.Data) || bytes.Equal(bytes.TrimSpace(w.Data), []byte("null")) {
			return 0, domain.Invalid("widget " + w.Key + " data must be valid non-null JSON of at most 4096 bytes")
		}
		if err := validateWidgetData(w.Key, w.Kind, w.Data); err != nil {
			return 0, err
		}
		group := strings.TrimSpace(w.Group)
		if group == "" {
			group = "Overview"
		}
		if len(group) > 48 {
			return 0, domain.Invalid("widget " + w.Key + " group must be at most 48 characters")
		}
		span := w.Span
		if span == 0 {
			span = 1
		}
		if span < 1 || span > 3 {
			return 0, domain.Invalid("widget " + w.Key + " span must be 1, 2 or 3")
		}
		if w.MinHeight < 0 || w.MinHeight > 800 {
			return 0, domain.Invalid("widget " + w.Key + " min_height must be between 0 and 800")
		}
		var expires *int64
		if w.TTLSeconds != 0 {
			if w.TTLSeconds < 30 || w.TTLSeconds > 604800 {
				return 0, domain.Invalid("widget " + w.Key + " ttl_seconds must be between 30 and 604800")
			}
			x := ms + int64(w.TTLSeconds)*1000
			expires = &x
		}
		widgets = append(widgets, domain.BotWidget{BotID: botID, Key: w.Key, Kind: w.Kind, Title: title, Group: group, Span: span, MinHeight: w.MinHeight, Position: w.Position, PayloadJSON: string(w.Data), UpdatedAtMS: ms, ExpiresAtMS: expires})
	}
	seenDelete := map[string]bool{}
	for _, key := range p.Unpublish {
		if !telemetryName.MatchString(key) {
			return 0, domain.Invalid("unpublish contains an invalid widget key")
		}
		if seenWidgets[key] {
			return 0, domain.Invalid("widget " + key + " cannot be published and unpublished in the same push")
		}
		seenDelete[key] = true
	}
	if p.Identity != nil {
		if p.Ready == nil || !*p.Ready {
			return 0, domain.Invalid("identity is accepted only when ready is true")
		}
		if !validDiscordIdentity(p.Identity.ID, p.Identity.Username, p.Identity.AvatarURL) {
			return 0, domain.Invalid("identity must contain a Discord user id, username, and Discord CDN avatar URL")
		}
		if err := a.Store.SetBotDiscordIdentity(ctx, botID, p.Identity.ID, strings.TrimSpace(p.Identity.Username), p.Identity.AvatarURL, ms); err != nil {
			return 0, err
		}
	}
	// A valid push, even an empty one, is a heartbeat.
	if a.Heartbeat != nil {
		a.Heartbeat(ctx, botID, p.Ready)
	}
	if len(rows) == 0 {
		if len(widgets) > 0 || len(seenDelete) > 0 {
			if err := a.Store.UpsertBotWidgets(ctx, widgets); err != nil {
				return 0, err
			}
			keys := make([]string, 0, len(seenDelete))
			for key := range seenDelete {
				keys = append(keys, key)
			}
			if err := a.Store.DeleteBotWidgets(ctx, botID, keys); err != nil {
				return 0, err
			}
			return len(widgets) + len(keys), nil
		}
		return 0, nil
	}
	if err := a.Store.InsertBotTelemetry(ctx, rows); err != nil {
		return 0, err
	}
	if len(widgets) > 0 {
		if err := a.Store.UpsertBotWidgets(ctx, widgets); err != nil {
			return 0, err
		}
	}
	keys := make([]string, 0, len(seenDelete))
	for key := range seenDelete {
		keys = append(keys, key)
	}
	if err := a.Store.DeleteBotWidgets(ctx, botID, keys); err != nil {
		return 0, err
	}
	return len(rows) + len(widgets) + len(keys), nil
}

var discordID = regexp.MustCompile(`^[0-9]{15,24}$`)

func validDiscordIdentity(id, username, avatarURL string) bool {
	if !discordID.MatchString(id) || len(strings.TrimSpace(username)) < 1 || len(username) > 80 || len(avatarURL) > 512 {
		return false
	}
	u, err := url.ParseRequestURI(avatarURL)
	if err != nil || u.Scheme != "https" || (u.Host != "cdn.discordapp.com" && u.Host != "media.discordapp.net") {
		return false
	}
	return strings.HasPrefix(u.Path, "/avatars/"+id+"/") || strings.HasPrefix(u.Path, "/embed/avatars/")
}

func validateWidgetData(key, kind string, raw json.RawMessage) error {
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil || d == nil {
		return domain.Invalid("widget " + key + " data must be a JSON object")
	}
	bad := func(want string) error {
		return domain.Invalid("widget " + key + " (" + kind + ") data must contain " + want)
	}
	num := func(name string) bool { v, ok := d[name].(float64); return ok && !math.IsNaN(v) && !math.IsInf(v, 0) }
	str := func(name string) bool { _, ok := d[name].(string); return ok }
	items := func(name string, limit int) ([]any, bool) { v, ok := d[name].([]any); return v, ok && len(v) <= limit }
	series := func(name string, limit int) bool {
		v, ok := items(name, limit)
		if !ok {
			return false
		}
		for _, x := range v {
			m, ok := x.(map[string]any)
			if !ok {
				return false
			}
			n, ok := m["value"].(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
				return false
			}
			if label, exists := m["label"]; exists {
				if _, ok := label.(string); !ok {
					return false
				}
			}
		}
		return true
	}
	switch kind {
	case "metric":
		if !num("value") {
			return bad("a numeric value")
		}
	case "status":
		state, ok := d["state"].(string)
		if !ok || !str("text") || (state != "good" && state != "warn" && state != "bad" && state != "neutral") {
			return bad("state (good|warn|bad|neutral) and text strings")
		}
	case "progress", "gauge":
		if !num("value") {
			return bad("a numeric value and optional numeric max")
		}
		if _, ok := d["max"]; ok && !num("max") {
			return bad("a numeric value and optional numeric max")
		}
	case "text", "markdown", "log":
		if !str("text") {
			return bad("a text string")
		}
	case "code":
		if !str("code") {
			return bad("a code string")
		}
	case "link", "image":
		rawURL, ok := d["url"].(string)
		u, err := url.ParseRequestURI(rawURL)
		if !ok || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return bad("an absolute http(s) url")
		}
	case "chart", "line", "area", "sparkline":
		if !series("points", 100) {
			return bad("points (up to 100 {label?, value:number} items)")
		}
	case "donut":
		if !series("values", 30) {
			return bad("values (up to 30 {label?, value:number} items)")
		}
	case "heatmap":
		if !series("cells", 120) {
			return bad("cells (up to 120 {label?, value:number} items)")
		}
	case "kv":
		v, ok := items("items", 30)
		if !ok {
			return bad("items (up to 30 {key, value} items)")
		}
		for _, x := range v {
			m, ok := x.(map[string]any)
			if !ok {
				return bad("items (up to 30 {key, value} items)")
			}
			if _, ok = m["key"].(string); !ok {
				return bad("items with string keys")
			}
			switch m["value"].(type) {
			case string, float64, bool:
			default:
				return bad("items with string, number or boolean values")
			}
		}
	case "table":
		cols, ok := items("columns", 12)
		if !ok {
			return bad("columns (up to 12) and rows (up to 50)")
		}
		_ = cols
		tableRows, ok := items("rows", 50)
		if !ok {
			return bad("columns (up to 12) and rows (up to 50)")
		}
		for _, r := range tableRows {
			cells, ok := r.([]any)
			if !ok || len(cells) > 12 {
				return bad("rows containing at most 12 cells")
			}
		}
	}
	return nil
}

// stat reports whether a sample for (bot, name) may be stored now.
func (a *Analytics) stat(botID, name string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastStat == nil {
		a.lastStat = map[string]time.Time{}
	}
	k := botID + "\x00" + name
	if t, ok := a.lastStat[k]; ok && now.Sub(t) < statMinInterval {
		return false
	}
	if len(a.lastStat) > 20000 {
		for k, t := range a.lastStat {
			if now.Sub(t) >= statMinInterval {
				delete(a.lastStat, k)
			}
		}
	}
	a.lastStat[k] = now
	return true
}

// Point is one time-series sample.
type Point struct {
	AtMS  int64   `json:"t"`
	Value float64 `json:"v"`
}

// Series is a named stat with its latest value.
type Series struct {
	Name   string  `json:"name"`
	Latest float64 `json:"latest"`
	Points []Point `json:"points"`
}

// Event is a recent bot event.
type Event struct {
	AtMS int64           `json:"t"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

// CommandOut is command usage over the window.
type CommandOut struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// Report is the dashboard data for one bot.
type Report struct {
	KeySet   bool         `json:"key_set"`
	WindowMS int64        `json:"window_ms"`
	LastAtMS int64        `json:"last_at_ms"`
	Stats    []Series     `json:"stats"`
	Commands []CommandOut `json:"commands"`
	Events   []Event      `json:"events"`
	Widgets  []WidgetOut  `json:"widgets"`
}

type WidgetOut struct {
	Key         string          `json:"key"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Group       string          `json:"group"`
	Span        int             `json:"span"`
	MinHeight   int             `json:"min_height"`
	Position    int             `json:"position"`
	Data        json.RawMessage `json:"data"`
	UpdatedAtMS int64           `json:"updated_at_ms"`
	ExpiresAtMS *int64          `json:"expires_at_ms"`
	Stale       bool            `json:"stale"`
}

// Report builds the dashboard data for a window ending now.
func (a *Analytics) Report(ctx context.Context, botID string, window time.Duration) (Report, error) {
	r := Report{WindowMS: window.Milliseconds(), Stats: []Series{}, Events: []Event{}}
	var err error
	if r.KeySet, err = a.Store.HasTelemetryKey(ctx, botID); err != nil {
		return r, err
	}
	since := a.now().Add(-window).UnixMilli()
	rows, err := a.Store.ListBotTelemetry(ctx, botID, "stat", since, maxSeriesRows)
	if err != nil {
		return r, err
	}
	idx := map[string]int{}
	for _, row := range rows {
		if row.Value == nil {
			continue
		}
		i, ok := idx[row.Name]
		if !ok {
			i = len(r.Stats)
			idx[row.Name] = i
			r.Stats = append(r.Stats, Series{Name: row.Name})
		}
		r.Stats[i].Points = append(r.Stats[i].Points, Point{row.RecordedAtMS, *row.Value})
		r.Stats[i].Latest = *row.Value
		if row.RecordedAtMS > r.LastAtMS {
			r.LastAtMS = row.RecordedAtMS
		}
	}
	sort.Slice(r.Stats, func(i, j int) bool { return r.Stats[i].Name < r.Stats[j].Name })
	cmds, err := a.Store.AggregateBotCommands(ctx, botID, since, 25)
	if err != nil {
		return r, err
	}
	r.Commands = make([]CommandOut, len(cmds))
	for i, c := range cmds {
		r.Commands[i] = CommandOut{c.Name, c.Count}
	}
	evs, err := a.Store.ListBotEvents(ctx, botID, 50)
	if err != nil {
		return r, err
	}
	for _, e := range evs {
		ev := Event{AtMS: e.RecordedAtMS, Name: e.Name}
		if e.PayloadJSON != nil {
			ev.Data = json.RawMessage(*e.PayloadJSON)
		}
		r.Events = append(r.Events, ev)
		if e.RecordedAtMS > r.LastAtMS {
			r.LastAtMS = e.RecordedAtMS
		}
	}
	ws, err := a.Store.ListBotWidgets(ctx, botID)
	if err != nil {
		return r, err
	}
	r.Widgets = make([]WidgetOut, len(ws))
	for i, w := range ws {
		r.Widgets[i] = WidgetOut{Key: w.Key, Kind: w.Kind, Title: w.Title, Group: w.Group, Span: w.Span, MinHeight: w.MinHeight, Position: w.Position, Data: json.RawMessage(w.PayloadJSON), UpdatedAtMS: w.UpdatedAtMS, ExpiresAtMS: w.ExpiresAtMS, Stale: a.now().UnixMilli()-w.UpdatedAtMS > int64(5*time.Minute/time.Millisecond)}
		if w.UpdatedAtMS > r.LastAtMS {
			r.LastAtMS = w.UpdatedAtMS
		}
	}
	return r, nil
}

// Prune enforces time retention and the per-bot row cap in bounded batches.
func (a *Analytics) Prune(ctx context.Context, retention time.Duration) error {
	before := a.now().Add(-retention).UnixMilli()
	for {
		n, err := a.Store.PruneBotTelemetry(ctx, before, 1000)
		if err != nil {
			return err
		}
		if n < 1000 {
			break
		}
	}
	bots, err := a.Store.BotsOverTelemetryCap(ctx, MaxTelemetryRowsBot)
	if err != nil {
		return err
	}
	for _, id := range bots {
		for {
			n, err := a.Store.TrimBotTelemetry(ctx, id, MaxTelemetryRowsBot, 1000)
			if err != nil {
				return err
			}
			if n < 1000 {
				break
			}
		}
	}
	return nil
}
