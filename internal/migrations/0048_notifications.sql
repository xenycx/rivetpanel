-- In-panel notifications and per-account notification preferences. Purely
-- additive.
--
-- notifications: one row per recipient. Rows are plain text (title, body)
-- plus an optional same-origin path (link, always starting with "/"); the
-- interface never renders them as HTML. The application keeps at most a
-- bounded number per account and prunes old rows (see NotificationService).
CREATE TABLE notifications (
    id             TEXT PRIMARY KEY NOT NULL,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category       TEXT NOT NULL CHECK (length(category) BETWEEN 1 AND 32),
    title          TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    body           TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
    link           TEXT NOT NULL DEFAULT '' CHECK (link = '' OR (substr(link, 1, 1) = '/' AND length(link) <= 300)),
    created_at_ms  INTEGER NOT NULL,
    read_at_ms     INTEGER
) STRICT;
CREATE INDEX notifications_user_idx ON notifications(user_id, created_at_ms DESC);
CREATE INDEX notifications_unread_idx ON notifications(user_id) WHERE read_at_ms IS NULL;
CREATE INDEX notifications_created_idx ON notifications(created_at_ms);

-- notification_prefs: per account and category, whether the panel keeps an
-- in-panel notification and whether it sends an email. A missing row means
-- the category's default (both on).
CREATE TABLE notification_prefs (
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category  TEXT NOT NULL CHECK (length(category) BETWEEN 1 AND 32),
    in_panel  INTEGER NOT NULL CHECK (in_panel IN (0, 1)),
    email     INTEGER NOT NULL CHECK (email IN (0, 1)),
    PRIMARY KEY (user_id, category)
) STRICT;
