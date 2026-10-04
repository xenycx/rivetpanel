package sqlite

import (
	"context"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Knowledgebase persistence (migration 0050).

// ListKBCategories returns every category in display order.
func (db *DB) ListKBCategories(ctx context.Context) ([]domain.KBCategory, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, slug, name, description, position, created_at_ms, updated_at_ms
		FROM kb_categories ORDER BY position, lower(name), id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.KBCategory
	for rows.Next() {
		var c domain.KBCategory
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Position, &c.CreatedAtMS, &c.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetKBCategory returns one category.
func (db *DB) GetKBCategory(ctx context.Context, id string) (domain.KBCategory, error) {
	var c domain.KBCategory
	err := db.QueryRowContext(ctx, `SELECT id, slug, name, description, position, created_at_ms, updated_at_ms
		FROM kb_categories WHERE id = ?`, id).Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Position, &c.CreatedAtMS, &c.UpdatedAtMS)
	return c, mapErr(err)
}

// PutKBCategory inserts or updates a category (a taken slug is ErrConflict).
func (db *DB) PutKBCategory(ctx context.Context, c domain.KBCategory) error {
	_, err := db.ExecContext(ctx, `INSERT INTO kb_categories (id, slug, name, description, position, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, name = excluded.name,
		description = excluded.description, position = excluded.position, updated_at_ms = excluded.updated_at_ms`,
		c.ID, c.Slug, c.Name, c.Description, c.Position, c.CreatedAtMS, c.UpdatedAtMS)
	return mapErr(err)
}

// DeleteKBCategory removes a category; its articles become uncategorized.
func (db *DB) DeleteKBCategory(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM kb_categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const kbCols = `id, category_id, slug, title, summary, %BODY%, status, visibility, position, author_id, updated_by,
	created_at_ms, updated_at_ms, published_at_ms`

func kbSelect(withBody bool) string {
	body := `''`
	if withBody {
		body = `body`
	}
	return `SELECT ` + strings.Replace(kbCols, "%BODY%", body, 1) + ` FROM kb_articles`
}

func scanKB(row interface{ Scan(...any) error }) (domain.KBArticle, error) {
	var a domain.KBArticle
	err := row.Scan(&a.ID, &a.CategoryID, &a.Slug, &a.Title, &a.Summary, &a.Body, &a.Status, &a.Visibility, &a.Position,
		&a.AuthorID, &a.UpdatedBy, &a.CreatedAtMS, &a.UpdatedAtMS, &a.PublishedAtMS)
	return a, mapErr(err)
}

// likeEscape escapes LIKE wildcards (ESCAPE '\').
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func inList(col string, vals []string, args *[]any) string {
	if len(vals) == 0 {
		return ""
	}
	q := ` AND ` + col + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",") + `)`
	for _, v := range vals {
		*args = append(*args, v)
	}
	return q
}

// ListKBArticles returns articles matching f in display order.
func (db *DB) ListKBArticles(ctx context.Context, f domain.KBFilter) ([]domain.KBArticle, error) {
	q := kbSelect(f.WithBody) + ` WHERE 1 = 1`
	var args []any
	q += inList("status", f.Statuses, &args)
	q += inList("visibility", f.Visibilities, &args)
	if len(f.Terms) > 0 {
		var ors []string
		for _, t := range f.Terms {
			p := "%" + likeEscape(t) + "%"
			ors = append(ors, `title LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\'`)
			args = append(args, p, p, p)
		}
		q += ` AND (` + strings.Join(ors, " OR ") + `)`
	}
	limit := f.Limit
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	q += ` ORDER BY position, lower(title), id LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.KBArticle
	for rows.Next() {
		a, err := scanKB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetKBArticle returns one article with its body.
func (db *DB) GetKBArticle(ctx context.Context, id string) (domain.KBArticle, error) {
	return scanKB(db.QueryRowContext(ctx, kbSelect(true)+` WHERE id = ?`, id))
}

// GetKBArticleBySlug returns one article with its body.
func (db *DB) GetKBArticleBySlug(ctx context.Context, slug string) (domain.KBArticle, error) {
	return scanKB(db.QueryRowContext(ctx, kbSelect(true)+` WHERE slug = ?`, slug))
}

// PutKBArticle inserts or updates an article (a taken slug is ErrConflict).
func (db *DB) PutKBArticle(ctx context.Context, a domain.KBArticle) error {
	_, err := db.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, title, summary, body, status, visibility, position,
		author_id, updated_by, created_at_ms, updated_at_ms, published_at_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET category_id = excluded.category_id, slug = excluded.slug, title = excluded.title,
		summary = excluded.summary, body = excluded.body, status = excluded.status, visibility = excluded.visibility,
		position = excluded.position, updated_by = excluded.updated_by, updated_at_ms = excluded.updated_at_ms,
		published_at_ms = excluded.published_at_ms`,
		a.ID, a.CategoryID, a.Slug, a.Title, a.Summary, a.Body, a.Status, a.Visibility, a.Position,
		a.AuthorID, a.UpdatedBy, a.CreatedAtMS, a.UpdatedAtMS, a.PublishedAtMS)
	return mapErr(err)
}

// DeleteKBArticle removes an article.
func (db *DB) DeleteKBArticle(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM kb_articles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
