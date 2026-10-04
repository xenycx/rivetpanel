package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func kbEnv(t *testing.T) *env {
	e := newEnv(t)
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: &service.Audit{Store: e.db, Bots: e.bots}, SecureCookies: true,
		Clients: &service.APIClientService{Store: e.db, Bots: e.bots},
		KB:      &service.KBService{Store: e.db},
		Status:  &service.StatusService{Store: e.db, Bots: e.bots}})
	return e
}

func kbArticle(t *testing.T, c *client, body map[string]any) kbArticleDTO {
	t.Helper()
	var a kbArticleDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/kb/manage/articles", body), &a)
	if a.ID == "" || a.Slug == "" {
		t.Fatalf("article: %+v", a)
	}
	return a
}

type kbIndexOut struct {
	Categories []kbCategoryDTO
	Articles   []kbArticleDTO
	Public     bool
	Manage     bool
}

func slugs(as []kbArticleDTO) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Slug)
	}
	return strings.Join(out, ",")
}

// Drafts, staff-only and signed-in-only articles are hidden from readers
// who may not see them; anonymous visitors read public articles only while
// the public help center is on; management needs kb.manage.
func TestKnowledgebaseVisibility(t *testing.T) {
	e := kbEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	alice := e.user("alice@x.io", domain.RoleUser)
	staff := e.user("staff@x.io", domain.RoleUser)
	writer := e.user("writer@x.io", domain.RoleUser)
	anon := &client{e: e}
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("staff@x.io"), map[string]any{"role": admin.createRole("Support", domain.PermTicketsManage, domain.PermTicketsCreate)})
	admin.mustStatus(204, "PATCH", "/api/v1/users/"+e.userID("writer@x.io"), map[string]any{"role": admin.createRole("Writer", domain.PermKBManage)})

	var cat kbCategoryDTO
	json.Unmarshal(admin.mustStatus(201, "POST", "/api/v1/kb/manage/categories", map[string]any{"name": "Getting started"}), &cat)
	if cat.Slug != "getting-started" {
		t.Fatalf("category: %+v", cat)
	}
	pub := kbArticle(t, admin, map[string]any{"title": "Restarting a crashed bot", "body": "Open the bot and press **Restart**.",
		"status": "published", "visibility": "public", "category_id": cat.ID})
	users := kbArticle(t, admin, map[string]any{"title": "Members guide", "body": "zebracorn members only", "status": "published", "visibility": "users"})
	staffOnly := kbArticle(t, admin, map[string]any{"title": "Escalation runbook", "body": "zebracorn staff runbook", "status": "published", "visibility": "staff"})
	draft := kbArticle(t, writer, map[string]any{"title": "Upcoming feature", "body": "zebracorn draft", "visibility": "public"})
	if pub.Slug != "restarting-a-crashed-bot" || draft.Status != "draft" {
		t.Fatalf("slugs/status: %+v %+v", pub, draft)
	}
	admin.mustStatus(400, "POST", "/api/v1/kb/manage/articles", map[string]any{"title": "Dup", "slug": pub.Slug, "body": "x"})

	// Management needs kb.manage.
	alice.mustStatus(403, "GET", "/api/v1/kb/manage", nil)
	alice.mustStatus(403, "POST", "/api/v1/kb/manage/articles", map[string]any{"title": "x", "body": "y"})
	staff.mustStatus(403, "PATCH", "/api/v1/kb/manage/articles/"+pub.ID, map[string]any{"title": "x"})
	alice.mustStatus(403, "PUT", "/api/v1/kb/manage/settings", map[string]any{"public": true})
	anon.mustStatus(401, "GET", "/api/v1/kb/manage", nil)

	read := func(c *client) kbIndexOut {
		var out kbIndexOut
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/kb", nil), &out)
		return out
	}
	if got := slugs(read(alice).Articles); got != "members-guide,restarting-a-crashed-bot" {
		t.Fatalf("alice sees %s", got)
	}
	if got := slugs(read(staff).Articles); got != "escalation-runbook,members-guide,restarting-a-crashed-bot" {
		t.Fatalf("staff sees %s", got)
	}
	if ix := read(writer); !ix.Manage || strings.Contains(slugs(ix.Articles), "upcoming") {
		t.Fatalf("manager index (published only): %+v", ix)
	}
	alice.mustStatus(404, "GET", "/api/v1/kb/articles/"+staffOnly.Slug, nil)
	alice.mustStatus(404, "GET", "/api/v1/kb/articles/"+draft.Slug, nil)
	staff.mustStatus(404, "GET", "/api/v1/kb/articles/"+draft.Slug, nil)
	staff.mustStatus(200, "GET", "/api/v1/kb/articles/"+staffOnly.Slug, nil)
	writer.mustStatus(200, "GET", "/api/v1/kb/articles/"+draft.Slug, nil) // preview
	var art struct {
		Article  kbArticleDTO
		Category *kbCategoryDTO
	}
	raw := alice.mustStatus(200, "GET", "/api/v1/kb/articles/"+pub.Slug, nil)
	json.Unmarshal(raw, &art)
	if art.Article.Body == "" || art.Category == nil || art.Category.ID != cat.ID || strings.Contains(string(raw), "admin@x.io") {
		t.Fatalf("article view: %s", raw)
	}

	// Search respects visibility.
	search := func(c *client, q string) string {
		var out struct{ Results []kbArticleDTO }
		json.Unmarshal(c.mustStatus(200, "GET", "/api/v1/kb/search?q="+q, nil), &out)
		return slugs(out.Results)
	}
	if got := search(alice, "zebracorn"); got != "members-guide" {
		t.Fatalf("alice search: %s", got)
	}
	if got := search(staff, "zebracorn"); got != "members-guide,escalation-runbook" && got != "escalation-runbook,members-guide" {
		t.Fatalf("staff search: %s", got)
	}

	// Anonymous: refused until the public help center is on, then public
	// articles only.
	anon.mustStatus(401, "GET", "/api/v1/kb", nil)
	anon.mustStatus(401, "GET", "/api/v1/kb/articles/"+pub.Slug, nil)
	anon.mustStatus(401, "GET", "/api/v1/kb/search?q=bot", nil)
	writer.mustStatus(200, "PUT", "/api/v1/kb/manage/settings", map[string]any{"public": true})
	ix := read(anon)
	if slugs(ix.Articles) != pub.Slug || !ix.Public || ix.Manage || len(ix.Categories) != 1 {
		t.Fatalf("anonymous index: %+v", ix)
	}
	anon.mustStatus(200, "GET", "/api/v1/kb/articles/"+pub.Slug, nil)
	for _, a := range []kbArticleDTO{users, staffOnly, draft} {
		anon.mustStatus(404, "GET", "/api/v1/kb/articles/"+a.Slug, nil)
	}
	if got := search(anon, "zebracorn"); got != "" {
		t.Fatalf("anonymous search: %s", got)
	}
	// A broken session reads as anonymous, not as an error.
	stale := &client{e: e, cookie: "not-a-session"}
	stale.mustStatus(200, "GET", "/api/v1/kb", nil)

	// Publishing the draft makes it visible; unpublishing hides it again.
	writer.mustStatus(200, "PATCH", "/api/v1/kb/manage/articles/"+draft.ID, map[string]any{"status": "published"})
	anon.mustStatus(200, "GET", "/api/v1/kb/articles/"+draft.Slug, nil)
	writer.mustStatus(200, "PATCH", "/api/v1/kb/manage/articles/"+draft.ID, map[string]any{"status": "draft"})
	anon.mustStatus(404, "GET", "/api/v1/kb/articles/"+draft.Slug, nil)
	writer.mustStatus(200, "PUT", "/api/v1/kb/manage/settings", map[string]any{"public": false})
	anon.mustStatus(401, "GET", "/api/v1/kb", nil)

	// Related articles for a new ticket ignore common words.
	var sug struct{ Results []kbArticleDTO }
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/kb/search?suggest=1&limit=5&q=Why+does+my+bot+keep+crashing", nil), &sug)
	if slugs(sug.Results) != pub.Slug {
		t.Fatalf("suggestions: %+v", sug.Results)
	}

	// Deleting a category keeps its articles (uncategorized).
	admin.mustStatus(204, "DELETE", "/api/v1/kb/manage/categories/"+cat.ID, nil)
	json.Unmarshal(alice.mustStatus(200, "GET", "/api/v1/kb/articles/"+pub.Slug, nil), &art)
	if art.Article.CategoryID != nil {
		t.Fatalf("category kept: %+v", art.Article)
	}
	writer.mustStatus(204, "DELETE", "/api/v1/kb/manage/articles/"+draft.ID, nil)
	writer.mustStatus(404, "DELETE", "/api/v1/kb/manage/articles/"+draft.ID, nil)

	var n int
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'kb.article_create' AND outcome = 'ok'`).Scan(&n)
	if n != 4 {
		t.Fatalf("kb.article_create audited %d times", n)
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'kb.article_create' AND outcome = 'denied'`).Scan(&n)
	if n != 1 {
		t.Fatalf("denied create audited %d times", n)
	}
	e.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'kb.settings' AND outcome = 'ok'`).Scan(&n)
	if n != 2 {
		t.Fatalf("kb.settings audited %d times", n)
	}
}

// Unsafe link targets cannot be saved; raw HTML is kept as text and served
// only inside JSON (the interface renders it as text, never as markup).
func TestKnowledgebaseMarkdownSafety(t *testing.T) {
	e := kbEnv(t)
	admin := e.user("admin@x.io", domain.RoleAdmin)
	for _, bad := range []string{
		"[click](javascript:alert(1))",
		"[click]( JavaScript:alert(1))",
		"[click](<javascript:alert(1)>)",
		"[x](data:text/html;base64,PHNjcmlwdD4=)",
		"[x](vbscript:msgbox)",
		"[x](//evil.example/steal)",
		"[x](/\\evil.example)",
		"[x](https://ok.example/\"onmouseover=alert(1))",
		"nul\x00byte",
		"bell\x07",
	} {
		resp, body := admin.do("POST", "/api/v1/kb/manage/articles", map[string]any{"title": "XSS", "body": bad})
		if resp.StatusCode != 400 {
			t.Fatalf("%q accepted: %d %s", bad, resp.StatusCode, body)
		}
	}
	// Allowed: http(s) links, same-site paths, and anything inside code.
	ok := "See [docs](https://example.com/a?b=c), [tickets](/support) and `[x](javascript:void)`.\n\n```\n[y](javascript:alert(1))\n<script>alert(1)</script>\n```\n\n<script>alert('x')</script><img src=x onerror=alert(1)>"
	a := kbArticle(t, admin, map[string]any{"title": "Links <b>bold</b>", "body": ok, "status": "published", "visibility": "users"})
	resp, raw := admin.do("GET", "/api/v1/kb/articles/"+a.Slug, nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	if strings.Contains(string(raw), "<script>") || !strings.Contains(string(raw), "\\"+"u003cscript"+"\\"+"u003e") {
		t.Fatalf("raw HTML must only appear JSON-escaped: %s", raw)
	}
	var art struct{ Article kbArticleDTO }
	json.Unmarshal(raw, &art)
	if art.Article.Body != ok || art.Article.Title != "Links <b>bold</b>" {
		t.Fatalf("body/title changed: %+v", art.Article)
	}
	if err := service.CheckKBMarkdown("[a](HTTPS://EXAMPLE.COM)"); err != nil {
		t.Fatal(err)
	}
}
