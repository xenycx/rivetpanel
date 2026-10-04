-- Knowledgebase (help center). Purely additive.
--
-- kb_categories: staff-defined groups shown in position order.
CREATE TABLE kb_categories (
    id             TEXT PRIMARY KEY NOT NULL,
    slug           TEXT NOT NULL UNIQUE CHECK (length(slug) BETWEEN 1 AND 80),
    name           TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    description    TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    position       INTEGER NOT NULL DEFAULT 0,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL
) STRICT;

-- kb_articles: Markdown help articles. body is stored as written and is
-- never served as HTML. status draft rows are visible to knowledgebase
-- managers only; visibility decides which readers see a published row
-- (public: also anonymous visitors when the public help center is enabled,
-- users: signed-in accounts, staff: support staff). Deleting a category
-- leaves its articles uncategorized.
CREATE TABLE kb_articles (
    id                TEXT PRIMARY KEY NOT NULL,
    category_id       TEXT REFERENCES kb_categories(id) ON DELETE SET NULL,
    slug              TEXT NOT NULL UNIQUE CHECK (length(slug) BETWEEN 1 AND 120),
    title             TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    summary           TEXT NOT NULL DEFAULT '' CHECK (length(summary) <= 500),
    body              TEXT NOT NULL CHECK (length(body) <= 100000),
    status            TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    visibility        TEXT NOT NULL CHECK (visibility IN ('public', 'users', 'staff')),
    position          INTEGER NOT NULL DEFAULT 0,
    author_id         TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_by        TEXT NOT NULL DEFAULT '' CHECK (length(updated_by) <= 320),
    created_at_ms     INTEGER NOT NULL,
    updated_at_ms     INTEGER NOT NULL,
    published_at_ms   INTEGER
) STRICT;
CREATE INDEX kb_articles_category_idx ON kb_articles(category_id, position, title);
CREATE INDEX kb_articles_status_idx ON kb_articles(status, visibility);
