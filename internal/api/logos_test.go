package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func b64(b []byte) map[string]string {
	return map[string]string{"image": base64.StdEncoding.EncodeToString(b)}
}

// fakeBotToken has the shape of a Discord bot token. It is assembled at run
// time so the source holds nothing that secret scanners mistake for a token.
func fakeBotToken(id, secret string) string {
	return strings.Join([]string{id, "Gabcde", strings.Repeat(secret, 30)}, ".")
}

func TestBotLogoUploadAndDiscordFetch(t *testing.T) {
	e := newEnv(t)
	good, bad := fakeBotToken("MTAwMDAwMDAwMDAwMDAwMDAwMA", "a"), fakeBotToken("MTAwMDAwMDAwMDAwMDAwMDAwMQ", "z")
	var auth string
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/users/@me" || auth != "Bot "+good {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Write([]byte(`{"id":"1000000000000000000","username":"Red","avatar":"0123456789abcdef0123456789abcdef"}`))
	}))
	t.Cleanup(discord.Close)
	e.bots.DiscordAPI = discord.URL
	c := e.user("a@x.io", "user")
	other := e.user("b@x.io", "user")
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "python",
		"env": map[string]string{"RED_TOKEN": good}}), &b)
	base := "/api/v1/bots/" + b.ID
	if b.LogoURL != "" || b.CustomLogo {
		t.Fatalf("%+v", b)
	}
	c.mustStatus(404, "GET", base+"/logo", nil)

	// The token is found under Red's variable name and only sent to Discord.
	json.Unmarshal(c.mustStatus(200, "POST", base+"/logo/discord", nil), &b)
	if b.LogoURL != "https://cdn.discordapp.com/avatars/1000000000000000000/0123456789abcdef0123456789abcdef.png?size=256" || b.DiscordUsername != "Red" {
		t.Fatalf("%+v", b)
	}

	// Validation: format, size and encoding.
	var g bytes.Buffer
	gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 32, 32), []color.Color{color.Black}), nil)
	c.mustStatus(400, "PUT", base+"/logo", b64(g.Bytes()))
	c.mustStatus(400, "PUT", base+"/logo", b64(pngOf(t, 8, 8)))
	c.mustStatus(400, "PUT", base+"/logo", b64(pngOf(t, 600, 600)))
	c.mustStatus(400, "PUT", base+"/logo", map[string]string{"image": "%%%"})
	c.mustStatus(400, "PUT", base+"/logo", b64([]byte("<svg onload=alert(1)>")))
	other.mustStatus(404, "PUT", base+"/logo", b64(pngOf(t, 64, 64)))

	// A custom logo wins over the Discord avatar; removing it falls back.
	logo := pngOf(t, 128, 128)
	json.Unmarshal(c.mustStatus(200, "PUT", base+"/logo", b64(logo)), &b)
	if !b.CustomLogo || !strings.HasPrefix(b.LogoURL, base+"/logo?v=") {
		t.Fatalf("%+v", b)
	}
	resp, body := c.do("GET", base+"/logo", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, logo) ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	other.mustStatus(404, "GET", base+"/logo", nil)
	json.Unmarshal(c.mustStatus(200, "DELETE", base+"/logo", nil), &b)
	if b.CustomLogo || !strings.HasPrefix(b.LogoURL, "https://cdn.discordapp.com/") {
		t.Fatalf("%+v", b)
	}

	// A wrong token is reported without echoing it.
	c.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"RED_TOKEN": bad}})
	_, body = c.do("POST", base+"/logo/discord", nil)
	if !strings.Contains(string(body), "rejected the token in RED_TOKEN") || strings.Contains(string(body), "zzzz") {
		t.Fatalf("%s", body)
	}
}

func TestSiteIconOrder(t *testing.T) {
	r := newSiteRig(t)
	c := r.owner
	var site struct {
		ID         string
		IconURL    string `json:"icon_url"`
		CustomLogo bool   `json:"custom_logo"`
	}
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}), &site)
	icon := "/api/v1/sites/" + site.ID + "/icon"
	c.mustStatus(404, "GET", icon, nil)

	// The favicon declared by index.html is served from the active release.
	fav := pngOf(t, 32, 32)
	up := zipOf(t, map[string]string{"index.html": `<html><head><link rel="icon" type="image/png" href="/img/fav.png?v=2"></head></html>`,
		"img/fav.png": string(fav), "favicon.ico": "ico"})
	if resp, body := c.raw("POST", "/api/v1/sites/"+site.ID+"/upload", up, "application/zip"); resp.StatusCode != 201 {
		t.Fatalf("upload %d %s", resp.StatusCode, body)
	}
	resp, body := c.do("GET", icon, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, fav) {
		t.Fatalf("favicon %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// A custom logo overrides it.
	logo := pngOf(t, 64, 64)
	json.Unmarshal(c.mustStatus(200, "PUT", "/api/v1/sites/"+site.ID+"/logo", b64(logo)), &site)
	if !site.CustomLogo {
		t.Fatalf("%+v", site)
	}
	if _, body := c.do("GET", icon, nil); !bytes.Equal(body, logo) {
		t.Fatal("custom logo not served")
	}
	c.mustStatus(200, "DELETE", "/api/v1/sites/"+site.ID+"/logo", nil)
	if _, body := c.do("GET", icon, nil); !bytes.Equal(body, fav) {
		t.Fatal("favicon not restored")
	}
	stranger := r.e.user("s@x.io", "user")
	stranger.mustStatus(404, "GET", icon, nil)
}

type wsLogoView struct {
	Workspace struct {
		ID          string
		LogoURL     string `json:"logo_url"`
		LogoVariant int64  `json:"logo_variant"`
	}
	Members []struct {
		UserID      string `json:"user_id"`
		LogoVariant int64  `json:"logo_variant"`
	}
}

func TestWorkspaceLogo(t *testing.T) {
	e := auditEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	dev := e.user("dev@x.io", domain.RoleUser)
	stranger := e.user("str@x.io", domain.RoleUser)
	admin := e.user("root@x.io", domain.RoleAdmin)

	var v wsLogoView
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/workspaces", map[string]string{"name": "Team"}), &v)
	id := v.Workspace.ID
	base := "/api/v1/workspaces/" + id
	if v.Workspace.LogoURL != "" || v.Workspace.LogoVariant != 0 {
		t.Fatalf("new workspace: %+v", v.Workspace)
	}
	owner.mustStatus(200, "PUT", base+"/members", map[string]string{"email": "dev@x.io", "role": "developer"})
	owner.mustStatus(404, "GET", base+"/logo", nil)

	// Validation is shared with bot and site logos.
	owner.mustStatus(400, "PUT", base+"/logo", b64(pngOf(t, 8, 8)))
	owner.mustStatus(400, "PUT", base+"/logo", b64([]byte("<svg onload=alert(1)>")))

	// Who may rename the workspace may set its logo; members only see it.
	logo := pngOf(t, 64, 64)
	dev.mustStatus(403, "PUT", base+"/logo", b64(logo))
	stranger.mustStatus(404, "PUT", base+"/logo", b64(logo))
	json.Unmarshal(owner.mustStatus(200, "PUT", base+"/logo", b64(logo)), &v)
	if !strings.HasPrefix(v.Workspace.LogoURL, base+"/logo?v=") {
		t.Fatalf("logo_url = %q", v.Workspace.LogoURL)
	}
	resp, body := dev.do("GET", base+"/logo", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, logo) ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	stranger.mustStatus(404, "GET", base+"/logo", nil)

	// Both listings carry it.
	var list struct {
		Workspaces []struct {
			ID      string
			LogoURL string `json:"logo_url"`
		}
	}
	for _, c := range []struct {
		cl   *client
		path string
	}{{dev, "/api/v1/workspaces"}, {admin, "/api/v1/admin/workspaces"}} {
		json.Unmarshal(c.cl.mustStatus(200, "GET", c.path, nil), &list)
		found := false
		for _, w := range list.Workspaces {
			if w.ID == id {
				found = w.LogoURL == v.Workspace.LogoURL
			}
		}
		if !found {
			t.Fatalf("%s: %+v", c.path, list.Workspaces)
		}
	}

	// Panel administrators change any workspace's logo.
	json.Unmarshal(admin.mustStatus(200, "DELETE", base+"/logo", nil), &v)
	if v.Workspace.LogoURL != "" {
		t.Fatalf("after remove: %q", v.Workspace.LogoURL)
	}
	owner.mustStatus(404, "GET", base+"/logo", nil)

	// A new generated picture: same permission, the choice is remembered.
	var sh struct {
		LogoVariant int64 `json:"logo_variant"`
	}
	dev.mustStatus(403, "POST", base+"/logo/shuffle", nil)
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/logo/shuffle", nil), &sh)
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/logo/shuffle", nil), &sh)
	json.Unmarshal(dev.mustStatus(200, "GET", base, nil), &v)
	if sh.LogoVariant != 2 || v.Workspace.LogoVariant != 2 {
		t.Fatalf("variant = %d / %d", sh.LogoVariant, v.Workspace.LogoVariant)
	}

	var actions []string
	rows, err := e.db.Query(`SELECT action || ':' || outcome FROM audit_events WHERE action LIKE 'workspace.logo%' AND outcome = 'ok' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	if got := strings.Join(actions, " "); got != "workspace.logo_set:ok workspace.logo_remove:ok workspace.logo_shuffle:ok workspace.logo_shuffle:ok" {
		t.Fatalf("audit: %s", got)
	}
}

func TestLogoShuffle(t *testing.T) {
	r := newSiteRig(t)
	e, owner := r.e, r.owner
	other := e.user("other@x.io", domain.RoleUser)
	admin := e.user("root@x.io", domain.RoleAdmin)
	var sh struct {
		LogoVariant int64 `json:"logo_variant"`
	}

	// Bots: whoever may change the logo.
	var b botDTO
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "x", "runtime": "python"}), &b)
	if b.LogoVariant != 0 {
		t.Fatalf("new bot variant %d", b.LogoVariant)
	}
	other.mustStatus(404, "POST", "/api/v1/bots/"+b.ID+"/logo/shuffle", nil)
	json.Unmarshal(owner.mustStatus(200, "POST", "/api/v1/bots/"+b.ID+"/logo/shuffle", nil), &sh)
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/bots/"+b.ID, nil), &b)
	if sh.LogoVariant != 1 || b.LogoVariant != 1 {
		t.Fatalf("bot variant %d / %d", sh.LogoVariant, b.LogoVariant)
	}

	// Sites.
	var site struct {
		ID          string
		LogoVariant int64 `json:"logo_variant"`
	}
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"}), &site)
	other.mustStatus(404, "POST", "/api/v1/sites/"+site.ID+"/logo/shuffle", nil)
	owner.mustStatus(200, "POST", "/api/v1/sites/"+site.ID+"/logo/shuffle", nil)
	json.Unmarshal(owner.mustStatus(200, "POST", "/api/v1/sites/"+site.ID+"/logo/shuffle", nil), &sh)
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/sites/"+site.ID, nil), &struct{ Site any }{&site})
	if sh.LogoVariant != 2 || site.LogoVariant != 2 {
		t.Fatalf("site variant %d / %d", sh.LogoVariant, site.LogoVariant)
	}

	// Accounts change their own; users.manage changes anyone's.
	var me struct {
		User struct {
			ID          string
			LogoVariant int64 `json:"logo_variant"`
		}
	}
	json.Unmarshal(owner.mustStatus(200, "POST", "/api/v1/me/avatar/shuffle", nil), &sh)
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/auth/me", nil), &me)
	if sh.LogoVariant != 1 || me.User.LogoVariant != 1 {
		t.Fatalf("own variant %d / %d", sh.LogoVariant, me.User.LogoVariant)
	}
	other.mustStatus(403, "POST", "/api/v1/users/"+me.User.ID+"/avatar/shuffle", nil)
	json.Unmarshal(admin.mustStatus(200, "POST", "/api/v1/users/"+me.User.ID+"/avatar/shuffle", nil), &sh)
	admin.mustStatus(404, "POST", "/api/v1/users/nobody/avatar/shuffle", nil)
	if sh.LogoVariant != 2 {
		t.Fatalf("admin shuffle %d", sh.LogoVariant)
	}
	// Workspace member lists carry each member's choice.
	var ws struct {
		Workspaces []struct{ ID string }
	}
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/workspaces", nil), &ws)
	var v wsLogoView
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/workspaces/"+ws.Workspaces[0].ID, nil), &v)
	if len(v.Members) != 1 || v.Members[0].LogoVariant != 2 {
		t.Fatalf("members %+v", v.Members)
	}
}
