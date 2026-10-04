package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func telemetryEnv(t *testing.T) (*env, *service.Analytics) {
	e := newEnv(t)
	an := &service.Analytics{Store: e.db}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Analytics: an,
		PublicURL: "https://panel.example.com", SecureCookies: true, Files: nil})
	return e, an
}

func (c *client) bearer(method, path, key string, body string) (*http.Response, []byte) {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.e.app.Test(r)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestBotTelemetryEndToEnd(t *testing.T) {
	e, _ := telemetryEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	viewer := e.user("view@x.io", domain.RoleUser)
	id := owner.createBot("b")
	bot := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", bot+"/users", map[string]any{"email": "view@x.io", "permissions": domain.PermViewConsole})

	// Key management needs full admin; ingest is impossible before a key exists.
	viewer.mustStatus(403, "POST", bot+"/telemetry-key", nil)
	anon := &client{e: e}
	if resp, _ := anon.bearer("POST", "/api/v1/bot-telemetry", "bpt_nope", `{}`); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
	var k struct{ Key, URL string }
	json.Unmarshal(owner.mustStatus(200, "POST", bot+"/telemetry-key", nil), &k)
	if !strings.HasPrefix(k.Key, "bpt_") || k.URL != "https://panel.example.com" {
		t.Fatalf("%+v", k)
	}
	// The key is exposed to the bot as a (sealed) environment variable.
	if !strings.Contains(string(owner.mustStatus(200, "GET", bot+"/env", nil)), "RIVET_TELEMETRY_KEY") {
		t.Fatal("env var not set")
	}
	var stored string
	e.db.QueryRow(`SELECT hex(telemetry_key_hash) FROM bots WHERE id = ?`, id).Scan(&stored)
	if stored == "" || strings.Contains(stored, k.Key) {
		t.Fatal("only the hash may be stored")
	}

	push := `{"ready":true,"identity":{"id":"123456789012345678","username":"Example Bot","avatar_url":"https://cdn.discordapp.com/avatars/123456789012345678/abc.png"},"stats":{"guilds":12,"members":3400,"ping":42.5},"commands":[{"name":"ban"},{"name":"ban","count":2},{"name":"help"}],"events":[{"name":"guild_join","data":{"id":"1"}}],"widgets":[{"key":"gateway","kind":"status","title":"Gateway","position":1,"data":{"state":"good","text":"Connected"}}]}`
	resp, body := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, push)
	if resp.StatusCode != 202 || !strings.Contains(string(body), `"stored":8`) {
		t.Fatalf("push: %d %s", resp.StatusCode, body)
	}
	if got := string(owner.mustStatus(200, "GET", bot, nil)); !strings.Contains(got, `"discord_avatar_url":"https://cdn.discordapp.com/avatars/123456789012345678/abc.png"`) {
		t.Fatalf("Discord identity was not attached to bot: %s", got)
	}
	// Repeat stat samples inside the minimum interval are dropped, commands are not.
	_, body = anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, push)
	if !strings.Contains(string(body), `"stored":5`) {
		t.Fatalf("throttle: %s", body)
	}

	var rep struct {
		KeySet bool `json:"key_set"`
		Stats  []struct {
			Name   string
			Latest float64
		}
		Commands []struct {
			Name  string
			Count int64
		}
		Events  []struct{ Name string }
		Widgets []struct{ Key, Kind, Title string }
	}
	json.Unmarshal(viewer.mustStatus(200, "GET", bot+"/analytics?window=1h", nil), &rep)
	if !rep.KeySet || len(rep.Stats) != 3 || rep.Stats[0].Name != "guilds" || rep.Stats[0].Latest != 12 {
		t.Fatalf("stats: %+v", rep)
	}
	if len(rep.Commands) != 2 || rep.Commands[0].Name != "ban" || rep.Commands[0].Count != 6 || len(rep.Events) != 2 {
		t.Fatalf("commands/events: %+v", rep)
	}
	if len(rep.Widgets) != 1 || rep.Widgets[0].Key != "gateway" || rep.Widgets[0].Kind != "status" {
		t.Fatalf("widgets: %+v", rep.Widgets)
	}
	// Widgets have a complete lifecycle: the bot can unpublish, and a full bot
	// administrator can remove a stuck widget without deleting the bot.
	if resp, body := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{"unpublish":["gateway"]}`); resp.StatusCode != 202 || !strings.Contains(string(body), `"stored":1`) {
		t.Fatalf("unpublish: %d %s", resp.StatusCode, body)
	}
	json.Unmarshal(viewer.mustStatus(200, "GET", bot+"/analytics?window=1h", nil), &rep)
	if len(rep.Widgets) != 0 {
		t.Fatalf("widget was not unpublished: %+v", rep.Widgets)
	}
	anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{"widgets":[{"key":"gateway","kind":"status","title":"Gateway","group":"Ops","span":2,"ttl_seconds":120,"data":{"state":"good","text":"Connected"}}]}`)
	owner.mustStatus(204, "DELETE", bot+"/widgets/gateway", nil)
	json.Unmarshal(viewer.mustStatus(200, "GET", bot+"/analytics?window=1h", nil), &rep)
	if len(rep.Widgets) != 0 {
		t.Fatalf("widget was not deleted: %+v", rep.Widgets)
	}
	viewer.mustStatus(400, "GET", bot+"/analytics?window=1y", nil)
	e.user("stranger@x.io", domain.RoleUser).mustStatus(404, "GET", bot+"/analytics", nil)

	// Validation: bad names, NaN-ish values, oversize events, unknown fields.
	for name, bad := range map[string]string{
		"bad stat name":   `{"stats":{"a/b":1}}`,
		"huge value":      `{"stats":{"x":1e30}}`,
		"bad command":     `{"commands":[{"name":""}]}`,
		"zero count":      `{"commands":[{"name":"x","count":0}]}`,
		"big event":       `{"events":[{"name":"e","data":"` + strings.Repeat("a", 2000) + `"}]}`,
		"unknown field":   `{"nope":1}`,
		"not json":        `hello`,
		"too many stats":  `{"stats":{"a":1,"b":1,"c":1,"d":1,"e":1,"f":1,"g":1,"h":1,"i":1,"j":1,"k":1,"l":1,"m":1,"n":1,"o":1,"p":1,"q":1}}`,
		"bad widget":      `{"widgets":[{"key":"x","kind":"html","title":"Unsafe","data":{"html":"<script>"}}]}`,
		"bad widget data": `{"widgets":[{"key":"x","kind":"metric","title":"Metric","data":{"value":"not a number"}}]}`,
	} {
		if resp, _ := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, bad); resp.StatusCode != 400 {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}
	if resp, _ := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{"events":[{"name":"e","data":"`+strings.Repeat("a", 70000)+`"}]}`); resp.StatusCode != 413 {
		t.Errorf("oversize body: %d", resp.StatusCode)
	}

	// Revocation and rotation invalidate the old key immediately.
	owner.mustStatus(204, "DELETE", bot+"/telemetry-key", nil)
	if resp, _ := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{}`); resp.StatusCode != 401 {
		t.Fatalf("revoked key accepted: %d", resp.StatusCode)
	}
}

func TestBotTelemetryRateLimitPerBot(t *testing.T) {
	e, an := telemetryEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	var k struct{ Key string }
	json.Unmarshal(owner.mustStatus(200, "POST", "/api/v1/bots/"+id+"/telemetry-key", nil), &k)
	anon := &client{e: e}
	limited := 0
	for i := 0; i < service.MaxPushesPerMinute+5; i++ {
		if resp, _ := anon.bearer("POST", "/api/v1/bot-telemetry", k.Key, `{"commands":[{"name":"x"}]}`); resp.StatusCode == 429 {
			limited++
		}
	}
	if limited != 5 {
		t.Fatalf("limited = %d, want 5", limited)
	}
	_ = an
}

func TestBotTelemetryWebSocket(t *testing.T) {
	e, _ := telemetryEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	var k struct{ Key string }
	json.Unmarshal(owner.mustStatus(200, "POST", "/api/v1/bots/"+id+"/telemetry-key", nil), &k)

	ln := serve(t, e)
	hdr := http.Header{"Authorization": {"Bearer " + k.Key}}
	if _, resp, err := websocket.DefaultDialer.Dial("ws://"+ln+"/api/v1/bot-telemetry/ws", http.Header{"Authorization": {"Bearer bpt_wrong"}}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("bad key must be refused before upgrade: %v", err)
	}
	c, _, err := websocket.DefaultDialer.Dial("ws://"+ln+"/api/v1/bot-telemetry/ws", hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	c.WriteMessage(websocket.TextMessage, []byte(`{"stats":{"ping":9},"commands":[{"name":"ws"}]}`))
	var r map[string]any
	if err := c.ReadJSON(&r); err != nil || r["stored"] != float64(2) {
		t.Fatalf("%v %v", r, err)
	}
	c.WriteMessage(websocket.TextMessage, []byte(`{"stats":{"bad name!":1}}`))
	if err := c.ReadJSON(&r); err != nil || r["error"] == nil {
		t.Fatalf("%v %v", r, err)
	}
	var rep struct{ Stats []struct{ Name string } }
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/bots/"+id+"/analytics", nil), &rep)
	if len(rep.Stats) != 1 || rep.Stats[0].Name != "ping" {
		t.Fatalf("%+v", rep)
	}
}
