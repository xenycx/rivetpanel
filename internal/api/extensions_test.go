package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/extensions/bundle"
	"github.com/xenycx/rivetpanel/internal/secrets"
	"github.com/xenycx/rivetpanel/internal/service"
)

var (
	insightsOnce sync.Once
	insightsWasm []byte
	insightsErr  error
)

// insightsFiles builds the Discord insights extension for wasip1 (once).
func insightsFiles(t *testing.T) map[string][]byte {
	t.Helper()
	insightsOnce.Do(func() {
		out := filepath.Join(os.TempDir(), "rivet-insights-test.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", out, "./extensions/discord-insights")
		cmd.Dir = filepath.Join("..", "..")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			insightsErr = errors.New(string(b))
			return
		}
		insightsWasm, insightsErr = os.ReadFile(out)
	})
	if insightsErr != nil {
		t.Skip("cannot build the extension: ", insightsErr)
	}
	dir := filepath.Join("..", "..", "extensions", "discord-insights")
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	icon, _ := os.ReadFile(filepath.Join(dir, "icon.svg"))
	return map[string][]byte{bundle.ManifestFile: manifest, bundle.WasmFile: insightsWasm, bundle.IconFile: icon}
}

func TestExtensionsModuleOff(t *testing.T) {
	e := newEnv(t)
	admin := e.user("root@example.com", domain.RoleAdmin)
	admin.mustStatus(404, "GET", "/api/v1/admin/extensions", nil)
	var me struct {
		Features map[string]bool `json:"features"`
	}
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	if me.Features["extensions"] {
		t.Fatal("extensions advertised while the module is off")
	}
}

func TestExtensionLifecycle(t *testing.T) {
	files := insightsFiles(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signed, err := bundle.Pack(files, priv)
	if err != nil {
		t.Fatal(err)
	}
	unsigned, _ := bundle.Pack(files, nil)

	// A fake Discord API.
	var discordCalls int
	var mu sync.Mutex
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		discordCalls++
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bot "+testToken {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/users/@me":
			w.Write([]byte(`{"id":"123456789012345678","username":"Rivet","avatar":"abc"}`))
		case "/applications/@me":
			w.Write([]byte(`{"id":"123456789012345678","name":"Rivet App","bot_public":true,"approximate_guild_count":42}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer discord.Close()

	var ext *service.ExtensionService
	e := newEnvWith(t, func(d *Deps, keys *secrets.Keyring) {
		ext = &service.ExtensionService{Store: d.DB.(service.ExtensionStore), Keys: keys, Bots: d.Bots, PanelVersion: "0.9.1", DiscordAPI: discord.URL, HTTP: discord.Client()}
		d.Extensions = ext
	})
	admin := e.user("root@example.com", domain.RoleAdmin)
	u := e.user("owner@example.com", domain.RoleUser)
	other := e.user("other@example.com", domain.RoleUser)
	const id = "dev.rivetpanel.discord-insights"

	// Only administrators manage extensions.
	u.mustStatus(403, "GET", "/api/v1/admin/extensions", nil)

	// Signed by an unknown key, or unsigned in production: refused.
	if resp, b := admin.raw("POST", "/api/v1/admin/extensions/upload", signed, "application/octet-stream"); resp.StatusCode != 400 || !strings.Contains(string(b), "unknown publisher key") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if resp, b := admin.raw("POST", "/api/v1/admin/extensions/upload", unsigned, "application/octet-stream"); resp.StatusCode != 400 || !strings.Contains(string(b), "not signed") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	admin.mustStatus(201, "POST", "/api/v1/admin/extension-publishers", map[string]string{"name": "Test", "public_key": bundle.FormatPublicKey(pub)})
	if resp, b := admin.raw("POST", "/api/v1/admin/extensions/upload", signed, "application/octet-stream"); resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	// A tampered bundle is refused even though its key is trusted.
	b, _ := bundle.Read(signed)
	b.Files[bundle.IconFile] = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><!--x--></svg>`)
	sig := b.Sig
	tampered, _ := bundle.Pack(b.Files, nil)
	tb, _ := bundle.Read(tampered)
	tb.Sig = sig
	if _, err := tb.Verify(func(string) (ed25519.PublicKey, bool) { return pub, true }); !errors.Is(err, bundle.ErrBadSignature) {
		t.Fatalf("tamper: %v", err)
	}

	// Nothing runs before activation; activation needs every required capability.
	admin.mustStatus(400, "POST", "/api/v1/admin/extensions/"+id+"/activate", map[string]any{"version": "1.0.0", "grants": []string{"kv"}})
	all := []string{"bot.discord_api", "bot.identity", "kv", "notify", "bots.read"}
	admin.mustStatus(200, "POST", "/api/v1/admin/extensions/"+id+"/activate", map[string]any{"version": "1.0.0", "grants": all})

	var contrib struct {
		Extensions []service.ExtensionContribution `json:"extensions"`
	}
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/extensions/contributions", nil), &contrib)
	if len(contrib.Extensions) != 1 || len(contrib.Extensions[0].BotCards) != 1 || contrib.Extensions[0].BotEnable == "" {
		t.Fatalf("%+v", contrib)
	}

	bot := u.createBot("insights")
	u.mustStatus(200, "PUT", "/api/v1/bots/"+bot+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": testToken}})
	card := "/api/v1/bots/" + bot + "/ext/" + id + "/card"

	// Before the bot's manager switches it on, the extension cannot use the token.
	body := u.mustStatus(200, "GET", card, nil)
	if !strings.Contains(string(body), "Discord unreachable") || discordCalls != 0 {
		t.Fatalf("%s (calls %d)", body, discordCalls)
	}
	other.mustStatus(404, "PUT", "/api/v1/bots/"+bot+"/extensions/"+id, map[string]bool{"enabled": true})
	other.mustStatus(404, "GET", card, nil)
	u.mustStatus(204, "PUT", "/api/v1/bots/"+bot+"/extensions/"+id, map[string]bool{"enabled": true})
	body = u.mustStatus(200, "POST", "/api/v1/bots/"+bot+"/ext/"+id+"/refresh", nil)
	if !strings.Contains(string(body), `"value":42`) || !strings.Contains(string(body), "Token accepted") || strings.Contains(string(body), testToken) {
		t.Fatalf("%s", body)
	}
	got, _ := e.db.GetBot(context.Background(), bot)
	if got.DiscordUsername != "Rivet" || got.DiscordUserID != "123456789012345678" {
		t.Fatalf("identity not updated: %+v", got.DiscordUsername)
	}

	// The custom routes stay inside the declared set.
	u.mustStatus(404, "GET", "/api/v1/bots/"+bot+"/ext/"+id+"/nope", nil)
	u.mustStatus(404, "DELETE", card, nil)

	// Settings that do not exist are refused; consent needs a consent model.
	admin.mustStatus(400, "PUT", "/api/v1/admin/extensions/"+id+"/settings", map[string]any{"values": map[string]any{"x": 1}})
	u.mustStatus(400, "PUT", "/api/v1/extensions/"+id+"/consent", map[string]any{"scopes": []string{"power"}})

	// Disabling hides it; revoking its publisher key fails it.
	admin.mustStatus(200, "POST", "/api/v1/admin/extensions/"+id+"/disable", nil)
	json.Unmarshal(u.mustStatus(200, "GET", "/api/v1/extensions/contributions", nil), &contrib)
	if len(contrib.Extensions) != 0 {
		t.Fatal("disabled extension still contributes")
	}
	u.mustStatus(404, "GET", card, nil)
	admin.mustStatus(200, "POST", "/api/v1/admin/extensions/"+id+"/enable", nil)
	admin.mustStatus(204, "DELETE", "/api/v1/admin/extension-publishers/"+bundle.KeyID(pub), nil)
	var view service.ExtensionView
	json.Unmarshal(admin.mustStatus(200, "GET", "/api/v1/admin/extensions/"+id, nil), &view)
	if view.State != domain.ExtFailed {
		t.Fatalf("state after revocation = %s", view.State)
	}
	admin.mustStatus(400, "POST", "/api/v1/admin/extensions/"+id+"/enable", nil)

	// Uninstall removes every trace.
	u.mustStatus(403, "DELETE", "/api/v1/admin/extensions/"+id, nil)
	admin.mustStatus(204, "DELETE", "/api/v1/admin/extensions/"+id, nil)
	for _, table := range []string{"extensions", "extension_versions", "extension_settings", "extension_kv", "extension_bot_enablements", "extension_user_consents", "extension_jobs"} {
		var n int
		e.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
}

// testToken is shaped like a Discord bot token (and only that); it is joined
// at run time so secret scanners do not mistake it for a real one.
var testToken = strings.Join([]string{"MTIzNDU2Nzg5MDEyMzQ1Njc4", "GabcDE", "abcdefghijklmnopqrstuvwxyz0123456789AB"}, ".")
