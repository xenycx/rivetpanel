package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// HealthStore is the persistence application health and alert rules need.
type HealthStore interface {
	RecordHeartbeat(ctx context.Context, botID string, nowMS int64, ready *bool) error
	GetHealth(ctx context.Context, botID string) (domain.BotHealth, error)
	SetStaleAlerted(ctx context.Context, botID string, on bool) error
	GetAlertPrefs(ctx context.Context, botID string) (domain.AlertPrefs, error)
	SetAlertPrefs(ctx context.Context, botID string, p domain.AlertPrefs, nowMS int64) error
	HeartbeatWatches(ctx context.Context) ([]domain.HeartbeatWatch, error)
	GetHealthProbe(ctx context.Context, botID string) (domain.HealthProbe, error)
	ListHealthProbes(ctx context.Context) ([]domain.HealthProbe, error)
	SetHealthProbe(ctx context.Context, p domain.HealthProbe) error
	DeleteHealthProbe(ctx context.Context, botID string) error
	RecordHealthProbe(ctx context.Context, p domain.HealthProbe) (bool, error)
	RestartIfRunning(ctx context.Context, id string, nowMS int64) (domain.Bot, bool, error)
}

const (
	heartbeatWriteGap = 15 * time.Second
	healthTick        = time.Minute
	probeTick         = 5 * time.Second
)

// HealthService tracks whether bots are responsive by their own account (SDK
// pushes), separately from Docker's process state, and evaluates the
// heartbeat alert rule. Missing SDK data means "unknown", never "failed".
type HealthService struct {
	Store  HealthStore
	Bots   *BotService
	Alerts *AlertService // nil: rules are evaluated but nothing is sent
	Log    *slog.Logger
	Now    func() time.Time

	mu      sync.Mutex
	written map[string]time.Time
	evalMu  sync.Mutex
}

func (h *HealthService) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Heartbeat records an SDK push. Writes are throttled per bot; a change in
// readiness is written at once.
func (h *HealthService) Heartbeat(ctx context.Context, botID string, ready *bool) {
	now := h.now()
	h.mu.Lock()
	if h.written == nil {
		h.written = map[string]time.Time{}
	}
	last, seen := h.written[botID]
	due := !seen || now.Sub(last) >= heartbeatWriteGap || ready != nil
	if due {
		if len(h.written) > 10000 {
			clear(h.written)
		}
		h.written[botID] = now
	}
	h.mu.Unlock()
	if due {
		if err := h.Store.RecordHeartbeat(ctx, botID, now.UnixMilli(), ready); err != nil && h.Log != nil {
			h.Log.Warn("record heartbeat", "bot", botID, "err", err)
		}
	}
}

// HealthView is a bot's application health for the interface.
type HealthView struct {
	State        string // unknown | ok | stale | not_ready
	LastSeenAtMS *int64
	Ready        *bool
	Prefs        domain.AlertPrefs
	Webhook      bool // the owner has Discord notifications set up
}

// Get returns a bot's health and alert preferences (console permission).
func (h *HealthService) Get(ctx context.Context, actor domain.User, botID string) (HealthView, error) {
	b, err := h.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole)
	if err != nil {
		return HealthView{}, err
	}
	prefs, err := h.Store.GetAlertPrefs(ctx, botID)
	if err != nil {
		return HealthView{}, err
	}
	v := HealthView{State: "unknown", Prefs: prefs}
	if h.Alerts != nil {
		_, v.Webhook = h.Alerts.webhookFor(ctx, b.OwnerID)
	}
	hl, err := h.Store.GetHealth(ctx, botID)
	if errors.Is(err, domain.ErrNotFound) {
		return v, nil
	}
	if err != nil {
		return HealthView{}, err
	}
	v.LastSeenAtMS, v.Ready = &hl.LastSeenAtMS, hl.Ready
	after := time.Duration(max(prefs.HeartbeatAfter, 120)) * time.Second
	switch {
	case h.stale(b, hl, after):
		v.State = "stale"
	case hl.Ready != nil && !*hl.Ready:
		v.State = "not_ready"
	default:
		v.State = "ok"
	}
	return v, nil
}

// stale reports whether a running bot has not pushed for `after`, measured
// from its last start so the previous run's pushes do not count against it.
func (h *HealthService) stale(b domain.Bot, hl domain.BotHealth, after time.Duration) bool {
	if b.DesiredState != domain.DesiredRunning || b.ObservedState != "running" {
		return false
	}
	ref := hl.LastSeenAtMS
	if b.LastStartedAtMS != nil && *b.LastStartedAtMS > ref {
		ref = *b.LastStartedAtMS
	}
	return h.now().Sub(time.UnixMilli(ref)) > after
}

// SetPrefs saves a bot's alert preferences (full control of the bot).
func (h *HealthService) SetPrefs(ctx context.Context, actor domain.User, botID string, p domain.AlertPrefs) (domain.AlertPrefs, error) {
	if _, err := h.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin); err != nil {
		return domain.AlertPrefs{}, err
	}
	if p.HeartbeatAfter != 0 && (p.HeartbeatAfter < 60 || p.HeartbeatAfter > 86400) {
		return domain.AlertPrefs{}, domain.Invalid("the heartbeat alert waits between 1 minute and 24 hours")
	}
	if err := h.Store.SetAlertPrefs(ctx, botID, p, h.now().UnixMilli()); err != nil {
		return domain.AlertPrefs{}, err
	}
	if p.HeartbeatAfter == 0 {
		_ = h.Store.SetStaleAlerted(ctx, botID, false)
	}
	return p, nil
}

// Test sends a test message to the bot owner's Discord webhook.
func (h *HealthService) Test(ctx context.Context, actor domain.User, botID string) error {
	b, err := h.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return err
	}
	if h.Alerts == nil {
		return domain.Invalid("notifications need Discord sign-in to be configured on this panel")
	}
	if _, ok := h.Alerts.webhookFor(ctx, b.OwnerID); !ok {
		return domain.Invalid("the bot's owner has not turned on Discord notifications (Settings → Connected accounts)")
	}
	return h.Alerts.SendErr(ctx, b.OwnerID, "🔔 Test notification for "+b.Name, "Alerts for this bot will arrive here.")
}

// GetProbe returns the configured active check, or a disabled default.
func (h *HealthService) GetProbe(ctx context.Context, actor domain.User, botID string) (domain.HealthProbe, error) {
	if _, err := h.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole); err != nil {
		return domain.HealthProbe{}, err
	}
	p, err := h.Store.GetHealthProbe(ctx, botID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.HealthProbe{BotID: botID, Status: "disabled", Path: "/", IntervalSeconds: 15, TimeoutMS: 2000, FailureThreshold: 3, SuccessThreshold: 1, StartupGraceSeconds: 30, RestartUnhealthy: true}, nil
	}
	return p, err
}

// SetProbe validates a loopback probe against the bot's published TCP ports.
// Empty kind disables probing.
func (h *HealthService) SetProbe(ctx context.Context, actor domain.User, botID string, p domain.HealthProbe) (domain.HealthProbe, error) {
	b, err := h.Bots.Authorize(ctx, actor, botID, domain.PermFullAdmin)
	if err != nil {
		return domain.HealthProbe{}, err
	}
	if p.Kind == "" {
		return domain.HealthProbe{BotID: botID, Status: "disabled"}, h.Store.DeleteHealthProbe(ctx, botID)
	}
	if p.Kind != "tcp" && p.Kind != "http" {
		return domain.HealthProbe{}, domain.Invalid("probe kind must be tcp or http")
	}
	portOK := false
	for _, port := range b.Ports {
		if port.HostPort == p.HostPort && port.Protocol == "tcp" {
			portOK = true
			break
		}
	}
	if !portOK {
		return domain.HealthProbe{}, domain.Invalid("probe port must be one of the bot's published TCP host ports")
	}
	if p.Path == "" {
		p.Path = "/"
	}
	if len(p.Path) > 256 || !strings.HasPrefix(p.Path, "/") {
		return domain.HealthProbe{}, domain.Invalid("HTTP probe path must start with / and be at most 256 characters")
	}
	if u, err := url.ParseRequestURI(p.Path); err != nil || u.IsAbs() || u.Host != "" {
		return domain.HealthProbe{}, domain.Invalid("HTTP probe path must be a relative path such as /healthz")
	}
	if p.IntervalSeconds < 5 || p.IntervalSeconds > 300 {
		return domain.HealthProbe{}, domain.Invalid("probe interval must be between 5 and 300 seconds")
	}
	if p.TimeoutMS < 250 || p.TimeoutMS > 10000 || p.TimeoutMS >= p.IntervalSeconds*1000 {
		return domain.HealthProbe{}, domain.Invalid("probe timeout must be 250-10000 ms and shorter than the interval")
	}
	if p.FailureThreshold < 1 || p.FailureThreshold > 10 || p.SuccessThreshold < 1 || p.SuccessThreshold > 10 {
		return domain.HealthProbe{}, domain.Invalid("probe success and failure thresholds must be between 1 and 10")
	}
	if p.StartupGraceSeconds < 0 || p.StartupGraceSeconds > 600 {
		return domain.HealthProbe{}, domain.Invalid("probe startup grace must be between 0 and 600 seconds")
	}
	p.BotID, p.Status, p.UpdatedAtMS = botID, "unknown", h.now().UnixMilli()
	if p.Kind == "tcp" {
		p.Path = "/"
	}
	if err := h.Store.SetHealthProbe(ctx, p); err != nil {
		return domain.HealthProbe{}, err
	}
	return p, nil
}

// Run evaluates the heartbeat rule every minute until ctx ends.
func (h *HealthService) Run(ctx context.Context) {
	t := time.NewTicker(healthTick)
	probes := time.NewTicker(probeTick)
	defer t.Stop()
	defer probes.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Evaluate(ctx)
		case <-probes.C:
			h.EvaluateProbes(ctx)
		}
	}
}

// EvaluateProbes performs due checks with bounded concurrency. Targets are
// always loopback published ports, so users cannot turn probes into SSRF.
func (h *HealthService) EvaluateProbes(ctx context.Context) {
	if !h.evalMu.TryLock() {
		return
	}
	defer h.evalMu.Unlock()
	ps, err := h.Store.ListHealthProbes(ctx)
	if err != nil {
		if h.Log != nil && ctx.Err() == nil {
			h.Log.Warn("health probes", "err", err)
		}
		return
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, p := range ps {
		b, err := h.Bots.Store.GetBot(ctx, p.BotID)
		if err != nil || b.DesiredState != domain.DesiredRunning || b.ObservedState != "running" {
			continue
		}
		now := h.now()
		if p.LastCheckedAtMS != nil && now.Sub(time.UnixMilli(*p.LastCheckedAtMS)) < time.Duration(p.IntervalSeconds)*time.Second {
			continue
		}
		if b.LastStartedAtMS != nil && now.Sub(time.UnixMilli(*b.LastStartedAtMS)) < time.Duration(p.StartupGraceSeconds)*time.Second {
			if p.Status != "starting" {
				x := now.UnixMilli()
				p.Status = "starting"
				p.ConsecutiveFailures = 0
				p.ConsecutiveSuccesses = 0
				p.LastCheckedAtMS = &x
				p.LastError = nil
				_, _ = h.Store.RecordHealthProbe(ctx, p)
			}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p domain.HealthProbe) { defer wg.Done(); defer func() { <-sem }(); h.checkProbe(ctx, p) }(p)
	}
	wg.Wait()
}

func (h *HealthService) checkProbe(parent context.Context, p domain.HealthProbe) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.HostPort))
	var checkErr error
	if p.Kind == "tcp" {
		var c net.Conn
		c, checkErr = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if c != nil {
			_ = c.Close()
		}
	} else {
		transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: time.Duration(p.TimeoutMS) * time.Millisecond}
		client := &http.Client{Transport: transport, Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var req *http.Request
		req, checkErr = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+p.Path, nil)
		if checkErr == nil {
			var res *http.Response
			res, checkErr = client.Do(req)
			if res != nil {
				_ = res.Body.Close()
				if res.StatusCode < 200 || res.StatusCode >= 400 {
					checkErr = fmt.Errorf("HTTP %d", res.StatusCode)
				}
			}
		}
	}
	now := h.now().UnixMilli()
	p.LastCheckedAtMS = &now
	old := p.Status
	if checkErr == nil {
		p.ConsecutiveSuccesses++
		p.ConsecutiveFailures = 0
		p.LastError = nil
		if p.ConsecutiveSuccesses >= p.SuccessThreshold {
			p.Status = "healthy"
		}
	} else {
		p.ConsecutiveFailures++
		p.ConsecutiveSuccesses = 0
		msg := checkErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		p.LastError = &msg
		if p.ConsecutiveFailures >= p.FailureThreshold {
			p.Status = "unhealthy"
		}
	}
	saved, err := h.Store.RecordHealthProbe(parent, p)
	if err != nil || !saved {
		return
	}
	if p.Status == "unhealthy" && old != "unhealthy" && p.RestartUnhealthy && h.Bots.Notifier != nil {
		if _, changed, err := h.Store.RestartIfRunning(parent, p.BotID, now); err == nil && changed {
			h.Bots.Notifier.Notify(p.BotID)
		}
	}
}

// Evaluate sends one alert when a running bot stops pushing for longer than
// its threshold, and one recovery message when pushes resume. A bot that
// never pushed is unknown and never alerts.
func (h *HealthService) Evaluate(ctx context.Context) {
	ws, err := h.Store.HeartbeatWatches(ctx)
	if err != nil {
		if h.Log != nil && ctx.Err() == nil {
			h.Log.Warn("heartbeat rules", "err", err)
		}
		return
	}
	for _, w := range ws {
		if w.Health == nil || w.Bot.DesiredState == domain.DesiredDeleted {
			continue
		}
		after := time.Duration(w.Prefs.HeartbeatAfter) * time.Second
		stale := h.stale(w.Bot, *w.Health, after)
		switch {
		case stale && !w.Health.StaleAlerted:
			_ = h.Store.SetStaleAlerted(ctx, w.Bot.ID, true)
			if h.Alerts != nil {
				h.Alerts.Notify(ctx, w.Bot.OwnerID, domain.NotifyBotAlerts, "💤 "+w.Bot.Name+" stopped reporting",
					fmt.Sprintf("No heartbeat for more than %s while the container is running. The bot may be stuck or disconnected from Discord.", after), BotLink(w.Bot))
			}
		case !stale && w.Health.StaleAlerted:
			_ = h.Store.SetStaleAlerted(ctx, w.Bot.ID, false)
			if h.Alerts != nil && w.Prefs.Recovery && w.Bot.ObservedState == "running" {
				h.Alerts.Notify(ctx, w.Bot.OwnerID, domain.NotifyBotAlerts, "✅ "+w.Bot.Name+" is reporting again", "Heartbeats resumed.", BotLink(w.Bot))
			}
		}
	}
}
