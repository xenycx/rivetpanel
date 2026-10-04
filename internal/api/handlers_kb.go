package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// Knowledgebase (help center). Reading works with or without a session:
// service.KBService decides what each reader sees. Management needs
// kb.manage.

type kbCategoryDTO struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Position    int    `json:"position"`
}

func toKBCategory(c domain.KBCategory) kbCategoryDTO {
	return kbCategoryDTO{c.ID, c.Slug, c.Name, c.Description, c.Position}
}

type kbArticleDTO struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	Summary       string  `json:"summary"`
	Body          string  `json:"body,omitempty"`
	CategoryID    *string `json:"category_id"`
	Status        string  `json:"status"`
	Visibility    string  `json:"visibility"`
	Position      int     `json:"position"`
	UpdatedAtMS   int64   `json:"updated_at_ms"`
	PublishedAtMS *int64  `json:"published_at_ms"`
	// Management view only.
	UpdatedBy   string `json:"updated_by,omitempty"`
	CreatedAtMS int64  `json:"created_at_ms,omitempty"`
}

func toKBArticle(a domain.KBArticle, manage bool) kbArticleDTO {
	d := kbArticleDTO{ID: a.ID, Slug: a.Slug, Title: a.Title, Summary: a.Summary, Body: a.Body, CategoryID: a.CategoryID,
		Status: a.Status, Visibility: a.Visibility, Position: a.Position, UpdatedAtMS: a.UpdatedAtMS, PublishedAtMS: a.PublishedAtMS}
	if manage {
		d.UpdatedBy, d.CreatedAtMS = a.UpdatedBy, a.CreatedAtMS
	}
	return d
}

// viewer resolves an optional reader for routes that also serve anonymous
// visitors: an API client bearer token (subject to clientGuard), a valid
// session cookie, or nobody (nil). An expired or unknown session reads as
// anonymous. No CSRF check: these routes only read.
func (s *panel) viewer(c fiber.Ctx) (*domain.User, error) {
	if s.clients != nil {
		if b, ok := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer "); ok && strings.HasPrefix(strings.TrimSpace(b), service.APIClientPrefix) {
			u, err := s.clients.Authenticate(c.Context(), strings.Clone(strings.TrimSpace(b)))
			if err != nil {
				return nil, fiber.NewError(fiber.StatusUnauthorized, "the API client token is unknown, expired or revoked")
			}
			if err := s.clientGuard(c, u.Client); err != nil {
				return nil, err
			}
			return &u, nil
		}
	}
	token := strings.Clone(c.Cookies(sessionCookie))
	if token == "" {
		return nil, nil
	}
	u, err := s.auth.Authenticate(c.Context(), token)
	if err != nil {
		return nil, nil
	}
	return &u, nil
}

// publicLimit rate-limits routes anonymous visitors can reach, per client IP.
func publicLimit(name string, max int) fiber.Handler {
	return limiter.New(limiter.Config{
		Max: max, Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string { return name + ":" + c.IP() },
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many requests; try again in a minute")
		},
	})
}

func (s *panel) kbPublicRoutes(v1 fiber.Router) {
	lim := publicLimit("kb", 120)
	v1.Get("/kb", lim, s.kbIndex)
	v1.Get("/kb/search", lim, s.kbSearch)
	v1.Get("/kb/articles/:slug", lim, s.kbArticle)
}

func (s *panel) kbManageRoutes(authed fiber.Router) {
	m := s.requirePerm(domain.PermKBManage)
	authed.Get("/kb/manage", m, s.kbManageIndex)
	authed.Get("/kb/manage/articles/:aid", m, s.kbManageArticle)
	authed.Post("/kb/manage/articles", m, s.kbCreateArticle)
	authed.Patch("/kb/manage/articles/:aid", m, s.kbUpdateArticle)
	authed.Delete("/kb/manage/articles/:aid", m, s.kbDeleteArticle)
	authed.Post("/kb/manage/categories", m, s.kbCreateCategory)
	authed.Patch("/kb/manage/categories/:cid", m, s.kbUpdateCategory)
	authed.Delete("/kb/manage/categories/:cid", m, s.kbDeleteCategory)
	authed.Put("/kb/manage/settings", m, s.kbPutSettings)
}

func kbIndexJSON(c fiber.Ctx, ix service.KBIndex, manage bool) error {
	cats := make([]kbCategoryDTO, len(ix.Categories))
	for i, x := range ix.Categories {
		cats[i] = toKBCategory(x)
	}
	arts := make([]kbArticleDTO, len(ix.Articles))
	for i, a := range ix.Articles {
		arts[i] = toKBArticle(a, manage)
	}
	return c.JSON(fiber.Map{"categories": cats, "articles": arts, "public": ix.Public, "manage": ix.Manage})
}

func (s *panel) kbIndex(c fiber.Ctx) error {
	v, err := s.viewer(c)
	if err != nil {
		return err
	}
	ix, err := s.kb.Index(c.Context(), v)
	if err != nil {
		return err
	}
	return kbIndexJSON(c, ix, false)
}

func (s *panel) kbArticle(c fiber.Ctx) error {
	v, err := s.viewer(c)
	if err != nil {
		return err
	}
	a, err := s.kb.Article(c.Context(), v, strings.Clone(c.Params("slug")))
	if err != nil {
		return err
	}
	out := fiber.Map{"article": toKBArticle(a, false)}
	if a.CategoryID != nil {
		if ix, err := s.kb.Index(c.Context(), v); err == nil {
			for _, x := range ix.Categories {
				if x.ID == *a.CategoryID {
					out["category"] = toKBCategory(x)
				}
			}
		}
	}
	return c.JSON(out)
}

func (s *panel) kbSearch(c fiber.Ctx) error {
	v, err := s.viewer(c)
	if err != nil {
		return err
	}
	q := c.Query("q")
	if len(q) > 300 {
		q = q[:300]
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	hits, err := s.kb.Search(c.Context(), v, q, limit, c.Query("suggest") == "1")
	if err != nil {
		return err
	}
	type hitDTO struct {
		kbArticleDTO
		Excerpt string `json:"excerpt"`
	}
	out := make([]hitDTO, len(hits))
	for i, h := range hits {
		out[i] = hitDTO{toKBArticle(h.Article, false), h.Excerpt}
	}
	return c.JSON(fiber.Map{"results": out})
}

func (s *panel) kbManageIndex(c fiber.Ctx) error {
	ix, err := s.kb.ManageIndex(c.Context(), currentUser(c))
	if err != nil {
		return err
	}
	return kbIndexJSON(c, ix, true)
}

func (s *panel) kbManageArticle(c fiber.Ctx) error {
	a, err := s.kb.ManageArticle(c.Context(), currentUser(c), strings.Clone(c.Params("aid")))
	if err != nil {
		return err
	}
	return c.JSON(toKBArticle(a, true))
}

type kbArticleIn struct {
	CategoryID *string `json:"category_id"`
	Slug       *string `json:"slug"`
	Title      *string `json:"title"`
	Summary    *string `json:"summary"`
	Body       *string `json:"body"`
	Status     *string `json:"status"`
	Visibility *string `json:"visibility"`
	Position   *int    `json:"position"`
}

func (in kbArticleIn) input() service.KBArticleInput {
	return service.KBArticleInput{CategoryID: in.CategoryID, Slug: in.Slug, Title: in.Title, Summary: in.Summary, Body: in.Body,
		Status: in.Status, Visibility: in.Visibility, Position: in.Position}
}

func kbAuditTarget(a domain.KBArticle) string {
	return a.Slug + " (" + a.Status + ", " + a.Visibility + ")"
}

func (s *panel) kbCreateArticle(c fiber.Ctx) error {
	var in kbArticleIn
	if err := decode(c, &in); err != nil {
		return err
	}
	a, err := s.kb.SaveArticle(c.Context(), currentUser(c), "", in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, kbAuditTarget(a))
	return c.Status(fiber.StatusCreated).JSON(toKBArticle(a, true))
}

func (s *panel) kbUpdateArticle(c fiber.Ctx) error {
	var in kbArticleIn
	if err := decode(c, &in); err != nil {
		return err
	}
	a, err := s.kb.SaveArticle(c.Context(), currentUser(c), strings.Clone(c.Params("aid")), in.input())
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, kbAuditTarget(a))
	return c.JSON(toKBArticle(a, true))
}

func (s *panel) kbDeleteArticle(c fiber.Ctx) error {
	a, err := s.kb.DeleteArticle(c.Context(), currentUser(c), strings.Clone(c.Params("aid")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, a.Slug)
	return c.SendStatus(fiber.StatusNoContent)
}

type kbCategoryIn struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
}

func (s *panel) kbCreateCategory(c fiber.Ctx) error {
	var in kbCategoryIn
	if err := decode(c, &in); err != nil {
		return err
	}
	x, err := s.kb.SaveCategory(c.Context(), currentUser(c), "", service.KBCategoryInput(in))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Slug)
	return c.Status(fiber.StatusCreated).JSON(toKBCategory(x))
}

func (s *panel) kbUpdateCategory(c fiber.Ctx) error {
	var in kbCategoryIn
	if err := decode(c, &in); err != nil {
		return err
	}
	x, err := s.kb.SaveCategory(c.Context(), currentUser(c), strings.Clone(c.Params("cid")), service.KBCategoryInput(in))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Slug)
	return c.JSON(toKBCategory(x))
}

func (s *panel) kbDeleteCategory(c fiber.Ctx) error {
	x, err := s.kb.DeleteCategory(c.Context(), currentUser(c), strings.Clone(c.Params("cid")))
	if err != nil {
		return err
	}
	c.Locals(keyAuditTarget, x.Slug)
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *panel) kbPutSettings(c fiber.Ctx) error {
	var in struct {
		Public bool `json:"public"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if err := s.kb.SetPublic(c.Context(), currentUser(c), in.Public); err != nil {
		return err
	}
	c.Locals(keyAuditTarget, "public help center="+strconv.FormatBool(in.Public))
	return c.JSON(fiber.Map{"public": in.Public})
}
