-- Support tickets. Purely additive.
--
-- support_tickets: one conversation opened by an account (user_id). number is
-- a panel-wide, human-friendly ticket number. bot_id optionally links a bot
-- or server the requester could access when opening it (bot_name keeps the
-- name for staff after the bot is deleted). assignee_id is a staff account.
CREATE TABLE support_tickets (
    id                   TEXT PRIMARY KEY NOT NULL,
    number               INTEGER NOT NULL UNIQUE,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject              TEXT NOT NULL CHECK (length(trim(subject)) BETWEEN 1 AND 150),
    category             TEXT NOT NULL CHECK (category IN ('general', 'technical', 'billing', 'account', 'abuse')),
    priority             TEXT NOT NULL CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    status               TEXT NOT NULL CHECK (status IN ('open', 'pending', 'resolved', 'closed')),
    bot_id               TEXT REFERENCES bots(id) ON DELETE SET NULL,
    bot_name             TEXT NOT NULL DEFAULT '' CHECK (length(bot_name) <= 200),
    assignee_id          TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at_ms        INTEGER NOT NULL,
    updated_at_ms        INTEGER NOT NULL,
    closed_at_ms         INTEGER
) STRICT;
CREATE INDEX support_tickets_user_idx ON support_tickets(user_id, updated_at_ms DESC);
CREATE INDEX support_tickets_status_idx ON support_tickets(status, updated_at_ms DESC);
CREATE INDEX support_tickets_assignee_idx ON support_tickets(assignee_id);

-- support_ticket_messages: replies, internal staff notes and recorded events
-- (status changes, assignment). internal = 1 rows are never shown to the
-- requester; only staff can write them.
CREATE TABLE support_ticket_messages (
    id             TEXT PRIMARY KEY NOT NULL,
    ticket_id      TEXT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_id      TEXT REFERENCES users(id) ON DELETE SET NULL,
    author_label   TEXT NOT NULL CHECK (length(author_label) <= 320),
    kind           TEXT NOT NULL CHECK (kind IN ('message', 'event')),
    staff          INTEGER NOT NULL CHECK (staff IN (0, 1)),
    internal       INTEGER NOT NULL CHECK (internal IN (0, 1) AND (internal = 0 OR staff = 1)),
    body           TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    created_at_ms  INTEGER NOT NULL
) STRICT;
CREATE INDEX support_ticket_messages_ticket_idx ON support_ticket_messages(ticket_id, created_at_ms);
