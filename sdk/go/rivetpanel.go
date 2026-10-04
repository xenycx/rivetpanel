// Package rivetpanel reports a Discord bot's stats, command counts, events and
// a heartbeat to RivetPanel. It uses only the standard library, so it works
// with DiscordGo, Disgo or any other library: copy this file into your bot as
// rivetpanel/rivetpanel.go.
//
//	panel := rivetpanel.New(func() map[string]float64 {
//		return map[string]float64{"guilds": float64(len(dg.State.Guilds))}
//	}, func() bool { return dg.DataReady })
//	go panel.Run(ctx)            // after the session is open
//	panel.Command("ping")        // in a command handler
//	panel.Event("guild_join", map[string]string{"id": g.ID})
//
// RIVET_URL and RIVET_TELEMETRY_KEY are set for you when you generate a
// key on the bot's Analytics tab. Without them Run returns immediately.
package rivetpanel

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Panel batches telemetry and pushes it every interval. Pushes that fail are
// dropped, never queued: the bot must not grow memory while the panel is away.
type Panel struct {
	url, key string
	interval time.Duration
	stats    func() map[string]float64
	ready    func() bool
	client   *http.Client

	mu       sync.Mutex
	commands map[string]int
	events   []event
	widgets  map[string]Widget
}

// Widget is a safe, declarative dashboard element. Data must be JSON-marshalable.
type Widget struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	Data     any    `json:"data"`
}

type event struct {
	Name string `json:"name"`
	Data any    `json:"data,omitempty"`
}

// New creates a Panel. stats and ready may be nil.
func New(stats func() map[string]float64, ready func() bool) *Panel {
	return &Panel{
		url:      strings.TrimRight(os.Getenv("RIVET_URL"), "/"),
		key:      os.Getenv("RIVET_TELEMETRY_KEY"),
		interval: 30 * time.Second, // the panel keeps one sample per stat per 10 s
		stats:    stats,
		ready:    ready,
		client:   &http.Client{Timeout: 5 * time.Second},
		commands: map[string]int{},
		widgets:  map[string]Widget{},
	}
}

// SetWidget publishes or replaces a dashboard widget.
func (p *Panel) SetWidget(key, kind, title string, data any, position int) {
	p.mu.Lock()
	if len(p.widgets) < 48 || p.widgets[key].Key != "" {
		p.widgets[key] = Widget{key, kind, title, position, data}
	}
	p.mu.Unlock()
}

// Enabled reports whether the panel's URL and key are configured.
func (p *Panel) Enabled() bool { return p.url != "" && p.key != "" }

// Command counts one invocation of a command.
func (p *Panel) Command(name string) {
	p.mu.Lock()
	if len(p.commands) < 100 || p.commands[name] > 0 {
		p.commands[name]++
	}
	p.mu.Unlock()
}

// Event records a notable event; data must marshal to at most 1 KB of JSON.
func (p *Panel) Event(name string, data any) {
	p.mu.Lock()
	if len(p.events) < 10 {
		p.events = append(p.events, event{name, data})
	}
	p.mu.Unlock()
}

// Run pushes until ctx ends.
func (p *Panel) Run(ctx context.Context) {
	if !p.Enabled() {
		return
	}
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		p.Flush(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Flush sends one push now.
func (p *Panel) Flush(ctx context.Context) {
	type cmd struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	p.mu.Lock()
	cmds := make([]cmd, 0, len(p.commands))
	for n, c := range p.commands {
		if len(cmds) == 20 {
			break
		}
		cmds = append(cmds, cmd{n, c})
		delete(p.commands, n)
	}
	evs := p.events
	widgets := make([]Widget, 0, len(p.widgets))
	for _, w := range p.widgets {
		widgets = append(widgets, w)
	}
	p.events = nil
	p.mu.Unlock()

	body := map[string]any{"commands": cmds, "events": evs, "widgets": widgets}
	if p.stats != nil {
		body["stats"] = p.stats()
	}
	if p.ready != nil {
		body["ready"] = p.ready()
	}
	b, err := json.Marshal(body)
	if err != nil {
		log.Printf("[rivetpanel] push failed: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/v1/bot-telemetry", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.key)
	res, err := p.client.Do(req)
	if err != nil {
		log.Printf("[rivetpanel] push failed: %v", err) // never crash the bot
		return
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		log.Printf("[rivetpanel] push failed: HTTP %d", res.StatusCode)
	}
}
