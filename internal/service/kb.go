package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// Knowledgebase limits.
const (
	kbTitleMax    = 200
	kbSummaryMax  = 500
	kbBodyMax     = 100000
	kbCatNameMax  = 100
	kbCatDescMax  = 500
	kbSlugMax     = 80
	kbSearchTerms = 8
	kbExcerpt     = 220
	// SetKBPublic is the panel setting that opens public articles to
	// anonymous visitors ("1" = on).
	SetKBPublic = "kb_public"
)

// KBStore persists the knowledgebase.
type KBStore interface {
	ListKBCategories(ctx context.Context) ([]domain.KBCategory, error)
	GetKBCategory(ctx context.Context, id string) (domain.KBCategory, error)
	PutKBCategory(ctx context.Context, c domain.KBCategory) error
	DeleteKBCategory(ctx context.Context, id string) error
	ListKBArticles(ctx context.Context, f domain.KBFilter) ([]domain.KBArticle, error)
	GetKBArticle(ctx context.Context, id string) (domain.KBArticle, error)
	GetKBArticleBySlug(ctx context.Context, slug string) (domain.KBArticle, error)
	PutKBArticle(ctx context.Context, a domain.KBArticle) error
	DeleteKBArticle(ctx context.Context, id string) error
	Settings(ctx context.Context) (map[string]domain.Setting, error)
	PutSettings(ctx context.Context, set []domain.Setting, nowMS int64) error
}

// KBService is the help center. Who reads what (enforced here, by the panel
// application):
//
//   - Drafts: knowledgebase managers (kb.manage) only.
//   - Published "staff" articles: support staff (tickets.view_all,
//     tickets.manage) and knowledgebase managers.
//   - Published "users" articles: every signed-in account.
//   - Published "public" articles: every signed-in account, and anonymous
//     visitors only while the public help center setting is on.
//
// A reader who may not see an article gets not found (its existence is not
// revealed). Anonymous requests while the public help center is off get
// ErrUnauthorized, so the interface can ask the visitor to sign in.
type KBService struct {
	Store KBStore
	Now   func() time.Time
}

func (s *KBService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// PublicEnabled reports whether anonymous visitors may read public articles.
func (s *KBService) PublicEnabled(ctx context.Context) bool {
	set, err := s.Store.Settings(ctx)
	return err == nil && set[SetKBPublic].Value == "1"
}

// SetPublic turns the public help center on or off.
func (s *KBService) SetPublic(ctx context.Context, actor domain.User, on bool) error {
	if !actor.Can(domain.PermKBManage) {
		return domain.Denied(actor, domain.PermKBManage)
	}
	v := ""
	if on {
		v = "1"
	}
	return s.Store.PutSettings(ctx, []domain.Setting{{Key: SetKBPublic, Value: v}}, s.now().UnixMilli())
}

func kbStaffReader(u domain.User) bool {
	return u.Can(domain.PermKBManage) || u.Can(domain.PermTicketsViewAll) || u.Can(domain.PermTicketsManage)
}

// readFilter is what viewer may read among published articles; nil viewer
// is an anonymous visitor.
func (s *KBService) readFilter(ctx context.Context, viewer *domain.User) (domain.KBFilter, error) {
	f := domain.KBFilter{Statuses: []string{domain.KBPublished}}
	switch {
	case viewer == nil:
		if !s.PublicEnabled(ctx) {
			return f, domain.ErrUnauthorized
		}
		f.Visibilities = []string{domain.KBPublic}
	case kbStaffReader(*viewer):
		f.Visibilities = []string{domain.KBPublic, domain.KBUsers, domain.KBStaff}
	default:
		f.Visibilities = []string{domain.KBPublic, domain.KBUsers}
	}
	return f, nil
}

// KBIndex is the help center front page as one reader sees it.
type KBIndex struct {
	Categories []domain.KBCategory
	Articles   []domain.KBArticle // without bodies
	Public     bool               // the public help center is on
	Manage     bool               // the reader holds kb.manage
}

// Index lists the categories and the articles viewer may read. Categories
// without readable articles are left out for readers (not for managers).
func (s *KBService) Index(ctx context.Context, viewer *domain.User) (KBIndex, error) {
	f, err := s.readFilter(ctx, viewer)
	if err != nil {
		return KBIndex{}, err
	}
	arts, err := s.Store.ListKBArticles(ctx, f)
	if err != nil {
		return KBIndex{}, err
	}
	cats, err := s.Store.ListKBCategories(ctx)
	if err != nil {
		return KBIndex{}, err
	}
	out := KBIndex{Articles: arts, Public: s.PublicEnabled(ctx), Manage: viewer != nil && viewer.Can(domain.PermKBManage)}
	used := map[string]bool{}
	for _, a := range arts {
		if a.CategoryID != nil {
			used[*a.CategoryID] = true
		}
	}
	for _, c := range cats {
		if used[c.ID] || out.Manage {
			out.Categories = append(out.Categories, c)
		}
	}
	return out, nil
}

// Article returns one article by slug if viewer may read it. Managers also
// open drafts (to preview them).
func (s *KBService) Article(ctx context.Context, viewer *domain.User, slug string) (domain.KBArticle, error) {
	f, err := s.readFilter(ctx, viewer)
	if err != nil {
		return domain.KBArticle{}, err
	}
	if !validKBSlug(slug) {
		return domain.KBArticle{}, domain.ErrNotFound
	}
	a, err := s.Store.GetKBArticleBySlug(ctx, slug)
	if err != nil {
		return a, err
	}
	if viewer != nil && viewer.Can(domain.PermKBManage) {
		return a, nil
	}
	if !slices.Contains(f.Statuses, a.Status) || !slices.Contains(f.Visibilities, a.Visibility) {
		return domain.KBArticle{}, domain.ErrNotFound
	}
	return a, nil
}

// KBHit is a search result.
type KBHit struct {
	Article domain.KBArticle // without body
	Excerpt string
	Score   int
}

var kbStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "not": true, "can": true, "cannot": true, "how": true, "what": true,
	"why": true, "does": true, "doesn": true, "this": true, "that": true, "from": true, "into": true, "when": true, "will": true,
	"are": true, "was": true, "have": true, "has": true, "you": true, "your": true, "but": true, "any": true, "all": true,
	"help": true, "please": true, "issue": true, "problem": true,
}

// kbTerms splits a query into at most kbSearchTerms lowercase words.
func kbTerms(q string, min int, stop bool) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-'
	}) {
		w = strings.Trim(w, ".-_")
		if utf8.RuneCountInString(w) < min || utf8.RuneCountInString(w) > 40 || (stop && kbStopwords[w]) || slices.Contains(out, w) {
			continue
		}
		out = append(out, w)
		if len(out) == kbSearchTerms {
			break
		}
	}
	return out
}

// Search finds readable articles matching q (any word; title matches rank
// first). suggest drops common words, for "related articles" on a new
// ticket.
func (s *KBService) Search(ctx context.Context, viewer *domain.User, q string, limit int, suggest bool) ([]KBHit, error) {
	f, err := s.readFilter(ctx, viewer)
	if err != nil {
		return nil, err
	}
	min := 2
	if suggest {
		min = 3
	}
	f.Terms = kbTerms(q, min, suggest)
	if len(f.Terms) == 0 {
		return []KBHit{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	f.WithBody, f.Limit = true, 200
	arts, err := s.Store.ListKBArticles(ctx, f)
	if err != nil {
		return nil, err
	}
	hits := make([]KBHit, 0, len(arts))
	for _, a := range arts {
		title, summary, body := strings.ToLower(a.Title), strings.ToLower(a.Summary), strings.ToLower(a.Body)
		score, matched := 0, 0
		for _, t := range f.Terms {
			m := false
			if strings.Contains(title, t) {
				score += 6
				m = true
			}
			if strings.Contains(summary, t) {
				score += 3
				m = true
			}
			if strings.Contains(body, t) {
				score++
				m = true
			}
			if m {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		if matched == len(f.Terms) {
			score += 4
		}
		ex := a.Summary
		if ex == "" {
			ex = kbExcerptOf(a.Body)
		}
		a.Body = ""
		hits = append(hits, KBHit{Article: a, Excerpt: ex, Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

var mdNoise = lazyre.New("(?m)^\\s*(#{1,6}|>|[-*]|\\d+\\.)\\s+|[`*_|]|\\[([^\\]]*)\\]\\([^)]*\\)")

// kbExcerptOf is the start of a Markdown body as plain text.
func kbExcerptOf(body string) string {
	body = stripFences(body)
	t := mdNoise.ReplaceAllStringFunc(body, func(m string) string {
		if strings.HasPrefix(m, "[") {
			return m[1:strings.Index(m, "]")]
		}
		return ""
	})
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) > kbExcerpt {
		r := []rune(t)[:kbExcerpt]
		t = strings.TrimSpace(string(r)) + "…"
	}
	return t
}

var fenceRe = lazyre.New("(?s)```.*?(```|$)")

func stripFences(s string) string { return fenceRe.ReplaceAllString(s, " ") }

// ---- Management (kb.manage) ----

func (s *KBService) manager(actor domain.User) error {
	if !actor.Can(domain.PermKBManage) {
		return domain.Denied(actor, domain.PermKBManage)
	}
	return nil
}

// ManageIndex lists every category and article, drafts included.
func (s *KBService) ManageIndex(ctx context.Context, actor domain.User) (KBIndex, error) {
	if err := s.manager(actor); err != nil {
		return KBIndex{}, err
	}
	arts, err := s.Store.ListKBArticles(ctx, domain.KBFilter{})
	if err != nil {
		return KBIndex{}, err
	}
	cats, err := s.Store.ListKBCategories(ctx)
	if err != nil {
		return KBIndex{}, err
	}
	return KBIndex{Categories: cats, Articles: arts, Public: s.PublicEnabled(ctx), Manage: true}, nil
}

// ManageArticle returns one article (any state) by id.
func (s *KBService) ManageArticle(ctx context.Context, actor domain.User, id string) (domain.KBArticle, error) {
	if err := s.manager(actor); err != nil {
		return domain.KBArticle{}, err
	}
	if !validUUID(id) {
		return domain.KBArticle{}, domain.ErrNotFound
	}
	return s.Store.GetKBArticle(ctx, id)
}

func validUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}

// KBArticleInput creates or changes an article; nil fields keep their value
// on update.
type KBArticleInput struct {
	CategoryID *string // "" = uncategorized
	Slug       *string // "" = derived from the title
	Title      *string
	Summary    *string
	Body       *string
	Status     *string
	Visibility *string
	Position   *int
}

var kbSlugRe = lazyre.New(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validKBSlug(s string) bool { return len(s) <= kbSlugMax && kbSlugRe.MatchString(s) }

// kbSlugFrom derives a slug from a title.
func kbSlugFrom(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= kbSlugMax {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > kbSlugMax {
		out = strings.Trim(out[:kbSlugMax], "-")
	}
	if out == "" {
		out = "article-" + uuid.NewString()[:8]
	}
	return out
}

var (
	mdLinkTarget = lazyre.New(`\]\(\s*<?([^)\s>]*)`)
	mdCodeSpan   = lazyre.New("`[^`\\n]*`")
)

// CheckKBMarkdown is the server-side half of safe rendering. Bodies are
// stored as Markdown text and the interface renders them as text nodes
// (raw HTML is shown literally, never interpreted); this check refuses
// control characters and link targets other than http(s) URLs and
// same-site paths, so a javascript:, data: or protocol-relative link can
// never be saved, whatever renders it later.
func CheckKBMarkdown(body string) error {
	if !utf8.ValidString(body) {
		return domain.Invalid("the article is not valid UTF-8 text")
	}
	for _, r := range body {
		if r == 0 || (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return domain.Invalid("the article contains control characters")
		}
	}
	prose := mdCodeSpan.ReplaceAllString(stripFences(body), "")
	for _, m := range mdLinkTarget.FindAllStringSubmatch(prose, -1) {
		if !safeKBLink(m[1]) {
			return domain.Invalid("link \"" + clip(m[1], 60) + "\" is not allowed: links must start with https://, http:// or / (a page of this panel)")
		}
	}
	return nil
}

func safeKBLink(t string) bool {
	l := strings.ToLower(t)
	switch {
	case strings.HasPrefix(l, "https://"), strings.HasPrefix(l, "http://"):
		return !strings.ContainsAny(t, "\"'<>`\\")
	case strings.HasPrefix(t, "/"):
		return !strings.HasPrefix(t, "//") && !strings.ContainsAny(t, "\\\"'<>`")
	}
	return false
}

func cleanLine(s string, max int, what string, required bool) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if required && s == "" {
		return "", domain.Invalid(what + " is required")
	}
	if utf8.RuneCountInString(s) > max {
		return "", domain.Invalid(what + " is too long")
	}
	return s, nil
}

// SaveArticle creates (id == "") or updates an article.
func (s *KBService) SaveArticle(ctx context.Context, actor domain.User, id string, in KBArticleInput) (domain.KBArticle, error) {
	if err := s.manager(actor); err != nil {
		return domain.KBArticle{}, err
	}
	now := s.now().UnixMilli()
	var a domain.KBArticle
	if id == "" {
		a = domain.KBArticle{ID: uuid.NewString(), Status: domain.KBDraft, Visibility: domain.KBUsers, CreatedAtMS: now, AuthorID: &actor.ID}
		if in.Title == nil || in.Body == nil {
			return a, domain.Invalid("title and body are required")
		}
	} else {
		cur, err := s.ManageArticle(ctx, actor, id)
		if err != nil {
			return cur, err
		}
		a = cur
	}
	var err error
	if in.Title != nil {
		if a.Title, err = cleanLine(*in.Title, kbTitleMax, "title", true); err != nil {
			return a, err
		}
	}
	if in.Summary != nil {
		if a.Summary, err = cleanLine(*in.Summary, kbSummaryMax, "summary", false); err != nil {
			return a, err
		}
	}
	if in.Body != nil {
		b := strings.ReplaceAll(*in.Body, "\r\n", "\n")
		if utf8.RuneCountInString(b) > kbBodyMax {
			return a, domain.Invalid("the article is too long (100,000 characters at most)")
		}
		if strings.TrimSpace(b) == "" {
			return a, domain.Invalid("body is required")
		}
		if err := CheckKBMarkdown(b); err != nil {
			return a, err
		}
		a.Body = b
	}
	if in.Status != nil {
		if !slices.Contains(domain.KBStatuses, *in.Status) {
			return a, domain.Invalid("status must be draft or published")
		}
		a.Status = *in.Status
	}
	if in.Visibility != nil {
		if !slices.Contains(domain.KBVisibilities, *in.Visibility) {
			return a, domain.Invalid("visibility must be public, users or staff")
		}
		a.Visibility = *in.Visibility
	}
	if in.Position != nil {
		if *in.Position < -100000 || *in.Position > 100000 {
			return a, domain.Invalid("position is out of range")
		}
		a.Position = *in.Position
	}
	if in.CategoryID != nil {
		if *in.CategoryID == "" {
			a.CategoryID = nil
		} else {
			if !validUUID(*in.CategoryID) {
				return a, domain.Invalid("unknown category")
			}
			if _, err := s.Store.GetKBCategory(ctx, *in.CategoryID); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return a, domain.Invalid("unknown category")
				}
				return a, err
			}
			c := *in.CategoryID
			a.CategoryID = &c
		}
	}
	if in.Slug != nil {
		sl := strings.TrimSpace(strings.ToLower(*in.Slug))
		if sl == "" {
			sl = kbSlugFrom(a.Title)
		} else if !validKBSlug(sl) {
			return a, domain.Invalid("the address may only use a-z, 0-9 and single dashes (80 characters at most)")
		}
		a.Slug = sl
	} else if a.Slug == "" {
		a.Slug = kbSlugFrom(a.Title)
	}
	if a.Status == domain.KBPublished && a.PublishedAtMS == nil {
		a.PublishedAtMS = &now
	}
	a.UpdatedAtMS, a.UpdatedBy = now, label(actor)
	if err := s.Store.PutKBArticle(ctx, a); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return a, domain.Invalid("another article already uses the address \"" + a.Slug + "\"")
		}
		return a, err
	}
	return a, nil
}

// DeleteArticle removes an article.
func (s *KBService) DeleteArticle(ctx context.Context, actor domain.User, id string) (domain.KBArticle, error) {
	a, err := s.ManageArticle(ctx, actor, id)
	if err != nil {
		return a, err
	}
	return a, s.Store.DeleteKBArticle(ctx, id)
}

// KBCategoryInput creates or changes a category.
type KBCategoryInput struct {
	Name        *string
	Slug        *string
	Description *string
	Position    *int
}

// SaveCategory creates (id == "") or updates a category.
func (s *KBService) SaveCategory(ctx context.Context, actor domain.User, id string, in KBCategoryInput) (domain.KBCategory, error) {
	if err := s.manager(actor); err != nil {
		return domain.KBCategory{}, err
	}
	now := s.now().UnixMilli()
	var c domain.KBCategory
	if id == "" {
		c = domain.KBCategory{ID: uuid.NewString(), CreatedAtMS: now}
		if in.Name == nil {
			return c, domain.Invalid("name is required")
		}
	} else {
		if !validUUID(id) {
			return c, domain.ErrNotFound
		}
		cur, err := s.Store.GetKBCategory(ctx, id)
		if err != nil {
			return cur, err
		}
		c = cur
	}
	var err error
	if in.Name != nil {
		if c.Name, err = cleanLine(*in.Name, kbCatNameMax, "name", true); err != nil {
			return c, err
		}
	}
	if in.Description != nil {
		if c.Description, err = cleanLine(*in.Description, kbCatDescMax, "description", false); err != nil {
			return c, err
		}
	}
	if in.Position != nil {
		if *in.Position < -100000 || *in.Position > 100000 {
			return c, domain.Invalid("position is out of range")
		}
		c.Position = *in.Position
	}
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		sl := strings.TrimSpace(strings.ToLower(*in.Slug))
		if !validKBSlug(sl) {
			return c, domain.Invalid("the address may only use a-z, 0-9 and single dashes (80 characters at most)")
		}
		c.Slug = sl
	} else if c.Slug == "" {
		c.Slug = kbSlugFrom(c.Name)
	}
	c.UpdatedAtMS = now
	if err := s.Store.PutKBCategory(ctx, c); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return c, domain.Invalid("another category already uses the address \"" + c.Slug + "\"")
		}
		return c, err
	}
	return c, nil
}

// DeleteCategory removes a category; its articles become uncategorized.
func (s *KBService) DeleteCategory(ctx context.Context, actor domain.User, id string) (domain.KBCategory, error) {
	if err := s.manager(actor); err != nil {
		return domain.KBCategory{}, err
	}
	if !validUUID(id) {
		return domain.KBCategory{}, domain.ErrNotFound
	}
	c, err := s.Store.GetKBCategory(ctx, id)
	if err != nil {
		return c, err
	}
	return c, s.Store.DeleteKBCategory(ctx, id)
}
