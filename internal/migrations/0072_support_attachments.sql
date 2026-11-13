-- Support attachments and automatic replies. Purely additive.
--
-- support_ticket_attachments: a file sent with a ticket message (screenshots,
-- logs). An upload starts pending (ticket_id and message_id NULL, visible to
-- its uploader only) and is bound to the message it is sent with; pending
-- uploads older than a day are pruned. The bytes live in the row (at most
-- 10 MiB each, enforced by the application and this check). Removing a
-- message deletes its attachments; deleting a ticket deletes them all.
CREATE TABLE support_ticket_attachments (
    id             TEXT PRIMARY KEY NOT NULL,
    uploader_id    TEXT REFERENCES users(id) ON DELETE SET NULL,
    ticket_id      TEXT REFERENCES support_tickets(id) ON DELETE CASCADE,
    message_id     TEXT REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    content_type   TEXT NOT NULL CHECK (length(content_type) BETWEEN 1 AND 100),
    size           INTEGER NOT NULL CHECK (size BETWEEN 1 AND 10485760),
    data           BLOB NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    CHECK ((ticket_id IS NULL) = (message_id IS NULL))
) STRICT;
CREATE INDEX support_ticket_attachments_msg_idx ON support_ticket_attachments(ticket_id, message_id);
CREATE INDEX support_ticket_attachments_pending_idx ON support_ticket_attachments(uploader_id, created_at_ms) WHERE message_id IS NULL;

-- auto = 1 marks a message the panel sent on the support team's behalf (the
-- configurable automatic reply to a new ticket). It is a staff message with
-- no author account.
ALTER TABLE support_ticket_messages ADD COLUMN auto INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1));
