-- Support collaboration: edited and removed messages, reactions, mentions,
-- labels, saved replies and watchers. Purely additive; existing rows keep
-- their meaning (no edit, not removed, no mentions).
--
-- support_ticket_messages: edited_at_ms marks a message changed by its
-- author (earlier bodies in support_message_revisions). A removed message
-- keeps its row so the thread stays in order, but its body is replaced with
-- '[removed]' and its revisions are deleted: people remove pasted secrets.
-- mentions_json lists the staff account ids an internal note mentions.
ALTER TABLE support_ticket_messages ADD COLUMN edited_at_ms INTEGER;
ALTER TABLE support_ticket_messages ADD COLUMN deleted_at_ms INTEGER;
ALTER TABLE support_ticket_messages ADD COLUMN deleted_by TEXT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE support_ticket_messages ADD COLUMN mentions_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(mentions_json) AND json_type(mentions_json) = 'array' AND length(mentions_json) <= 4096);

CREATE TABLE support_message_revisions (
    id             TEXT PRIMARY KEY NOT NULL,
    message_id     TEXT NOT NULL REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    body           TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    editor_id      TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms  INTEGER NOT NULL
) STRICT;
CREATE INDEX support_message_revisions_msg_idx ON support_message_revisions(message_id, created_at_ms);

-- Reactions: a small fixed set of acknowledgements, one of each kind per
-- account and message.
CREATE TABLE support_message_reactions (
    message_id     TEXT NOT NULL REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('ack', 'thanks', 'seen', 'solved')),
    created_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (message_id, user_id, kind)
) STRICT;

-- Labels: staff-defined, never shown to requesters. color names a palette
-- token the interface tints the label with.
CREATE TABLE support_labels (
    id             TEXT PRIMARY KEY NOT NULL,
    name           TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK (length(trim(name)) BETWEEN 1 AND 32),
    color          TEXT NOT NULL CHECK (color IN ('action', 'data', 'run', 'warn', 'fail', 'violet', 'cyan', 'slate')),
    created_at_ms  INTEGER NOT NULL
) STRICT;
CREATE TABLE support_ticket_labels (
    ticket_id      TEXT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    label_id       TEXT NOT NULL REFERENCES support_labels(id) ON DELETE CASCADE,
    PRIMARY KEY (ticket_id, label_id)
) STRICT;
CREATE INDEX support_ticket_labels_label_idx ON support_ticket_labels(label_id);

-- Saved replies: answers the support team reuses.
CREATE TABLE support_saved_replies (
    id             TEXT PRIMARY KEY NOT NULL,
    title          TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 80),
    body           TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
    created_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at_ms  INTEGER NOT NULL
) STRICT;

-- Watchers: staff following a ticket without being assigned to it.
CREATE TABLE support_ticket_watchers (
    ticket_id      TEXT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (ticket_id, user_id)
) STRICT;
CREATE INDEX support_ticket_watchers_user_idx ON support_ticket_watchers(user_id);
