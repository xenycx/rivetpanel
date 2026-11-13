package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// personCols selects how an account appears on a ticket from the users row
// aliased p (all columns tolerate a missing account).
func personCols(p string) string {
	return `COALESCE(` + p + `.id, ''), COALESCE(` + p + `.email, ''), COALESCE(` + p + `.display_name, ''),
	COALESCE(` + p + `.avatar_jpeg IS NOT NULL AND length(` + p + `.avatar_jpeg) > 0, 0), COALESCE(` + p + `.updated_at_ms, 0), COALESCE(` + p + `.logo_variant, 0)`
}

func personDst(p *domain.Person) []any {
	return []any{&p.ID, &p.Email, &p.DisplayName, &p.HasAvatar, &p.AvatarAtMS, &p.LogoVariant}
}

var ticketCols = `t.id, t.number, t.user_id, t.subject, t.category, t.priority, t.status,
	t.bot_id, t.bot_name, t.assignee_id, t.created_at_ms, t.updated_at_ms, t.closed_at_ms, ` + personCols("u") + `, ` + personCols("a")

const ticketFrom = ` FROM support_tickets t LEFT JOIN users u ON u.id = t.user_id LEFT JOIN users a ON a.id = t.assignee_id`

func scanTicket(row interface{ Scan(...any) error }, extra ...any) (domain.Ticket, error) {
	var t domain.Ticket
	dst := []any{&t.ID, &t.Number, &t.UserID, &t.Subject, &t.Category, &t.Priority, &t.Status,
		&t.BotID, &t.BotName, &t.AssigneeID, &t.CreatedAtMS, &t.UpdatedAtMS, &t.ClosedAtMS}
	dst = append(dst, personDst(&t.Requester)...)
	dst = append(dst, personDst(&t.Assignee)...)
	err := row.Scan(append(dst, extra...)...)
	t.UserEmail, t.AssigneeEmail = t.Requester.Email, t.Assignee.Email
	return t, mapErr(err)
}

// attachLabels fills LabelIDs for the given tickets.
func (db *DB) attachLabels(ctx context.Context, ts []domain.Ticket) error {
	if len(ts) == 0 {
		return nil
	}
	at := map[string]int{}
	args := make([]any, len(ts))
	for i, t := range ts {
		at[t.ID] = i
		args[i] = t.ID
		ts[i].LabelIDs = []string{}
	}
	rows, err := db.QueryContext(ctx, `SELECT tl.ticket_id, tl.label_id FROM support_ticket_labels tl JOIN support_labels l ON l.id = tl.label_id
		WHERE tl.ticket_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ts)), ",")+`) ORDER BY l.name`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, lid string
		if err := rows.Scan(&tid, &lid); err != nil {
			return err
		}
		ts[at[tid]].LabelIDs = append(ts[at[tid]].LabelIDs, lid)
	}
	return rows.Err()
}

// CreateTicket stores a ticket and its first message, numbering it inside
// the transaction.
func (db *DB) CreateTicket(ctx context.Context, t domain.Ticket, first domain.TicketMessage) (domain.Ticket, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM support_tickets`).Scan(&t.Number); err != nil {
		return t, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO support_tickets (id, number, user_id, subject, category, priority, status,
		bot_id, bot_name, assignee_id, created_at_ms, updated_at_ms) VALUES (?,?,?,?,?,?,?,?,?,NULL,?,?)`,
		t.ID, t.Number, t.UserID, t.Subject, t.Category, t.Priority, t.Status, t.BotID, t.BotName, t.CreatedAtMS, t.UpdatedAtMS); err != nil {
		return t, mapErr(err)
	}
	if err := insertTicketMessage(ctx, tx, first); err != nil {
		return t, err
	}
	return t, tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func insertTicketMessage(ctx context.Context, tx execer, m domain.TicketMessage) error {
	mentions := m.Mentions
	if mentions == nil {
		mentions = []string{}
	}
	mj, _ := json.Marshal(mentions)
	_, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_messages (id, ticket_id, author_id, author_label, kind, staff, internal, body, created_at_ms, mentions_json, auto)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.TicketID, m.AuthorID, m.AuthorLabel, m.Kind, boolInt(m.Staff), boolInt(m.Internal), m.Body, m.CreatedAtMS, string(mj), boolInt(m.Auto))
	if err != nil {
		return mapErr(err)
	}
	// Bind the author's pending uploads sent with the message.
	for _, a := range m.Attachments {
		if m.AuthorID == nil {
			return domain.Invalid("attachments need an author")
		}
		res, err := tx.ExecContext(ctx, `UPDATE support_ticket_attachments SET ticket_id = ?, message_id = ?
			WHERE id = ? AND uploader_id = ? AND message_id IS NULL`, m.TicketID, m.ID, a.ID, *m.AuthorID)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return domain.Invalid("an attachment is no longer available; attach the file again")
		}
	}
	return nil
}

// GetTicket returns one ticket.
func (db *DB) GetTicket(ctx context.Context, id string) (domain.Ticket, error) {
	t, err := scanTicket(db.QueryRowContext(ctx, `SELECT `+ticketCols+ticketFrom+` WHERE t.id = ?`, id))
	if err != nil {
		return t, err
	}
	one := []domain.Ticket{t}
	err = db.attachLabels(ctx, one)
	return one[0], err
}

// GetTicketByNumber returns the ticket with this number.
func (db *DB) GetTicketByNumber(ctx context.Context, n int64) (domain.Ticket, error) {
	var id string
	if err := db.QueryRowContext(ctx, `SELECT id FROM support_tickets WHERE number = ?`, n).Scan(&id); err != nil {
		return domain.Ticket{}, mapErr(err)
	}
	return db.GetTicket(ctx, id)
}

// ListTickets returns tickets matching f, most recently updated first, with
// the number of messages a reader sees (internal notes counted only when
// withInternal).
func (db *DB) ListTickets(ctx context.Context, f domain.TicketFilter, withInternal bool) ([]domain.Ticket, error) {
	q := `SELECT ` + ticketCols + `, (SELECT count(*) FROM support_ticket_messages m WHERE m.ticket_id = t.id AND m.kind = 'message' AND m.auto = 0`
	if !withInternal {
		q += ` AND m.internal = 0`
	}
	q += `)` + ticketFrom + ` WHERE 1 = 1`
	var args []any
	if f.UserID != "" {
		q += ` AND t.user_id = ?`
		args = append(args, f.UserID)
	}
	switch f.Status {
	case "":
	case "active":
		q += ` AND t.status IN ('open', 'pending')`
	default:
		q += ` AND t.status = ?`
		args = append(args, f.Status)
	}
	if f.AssigneeID != "" {
		q += ` AND t.assignee_id = ?`
		args = append(args, f.AssigneeID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY t.updated_at_ms DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Ticket{}
	for rows.Next() {
		var n int
		t, err := scanTicket(rows, &n)
		if err != nil {
			return nil, err
		}
		t.Messages = n
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, db.attachLabels(ctx, out)
}

// CountActiveTickets counts an account's open and pending tickets.
func (db *DB) CountActiveTickets(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM support_tickets WHERE user_id = ? AND status IN ('open', 'pending')`, userID).Scan(&n)
	return n, err
}

var messageCols = `m.id, m.ticket_id, m.author_id, m.author_label, m.kind, m.staff, m.internal, m.body, m.created_at_ms,
	m.edited_at_ms, m.deleted_at_ms, COALESCE(NULLIF(d.display_name, ''), d.email, ''), m.mentions_json, m.auto, ` + personCols("p")

const messageFrom = ` FROM support_ticket_messages m LEFT JOIN users p ON p.id = m.author_id LEFT JOIN users d ON d.id = m.deleted_by`

func scanMessage(row interface{ Scan(...any) error }) (domain.TicketMessage, error) {
	var m domain.TicketMessage
	var mj string
	dst := append([]any{&m.ID, &m.TicketID, &m.AuthorID, &m.AuthorLabel, &m.Kind, &m.Staff, &m.Internal, &m.Body, &m.CreatedAtMS,
		&m.EditedAtMS, &m.DeletedAtMS, &m.DeletedBy, &mj, &m.Auto}, personDst(&m.Author)...)
	if err := row.Scan(dst...); err != nil {
		return m, mapErr(err)
	}
	_ = json.Unmarshal([]byte(mj), &m.Mentions)
	return m, nil
}

// TicketMessages returns a ticket's messages, oldest first, with their
// reactions; internal notes only when withInternal.
func (db *DB) TicketMessages(ctx context.Context, ticketID string, withInternal bool) ([]domain.TicketMessage, error) {
	q := `SELECT ` + messageCols + messageFrom + ` WHERE m.ticket_id = ?`
	if !withInternal {
		q += ` AND m.internal = 0`
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY m.created_at_ms, m.rowid LIMIT 2000`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TicketMessage{}
	at := map[string]int{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		at[m.ID] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	rr, err := db.QueryContext(ctx, `SELECT r.message_id, r.kind, r.user_id, r.user_id != t.user_id,
		COALESCE(NULLIF(u.display_name, ''), u.email, '')
		FROM support_message_reactions r JOIN support_ticket_messages m ON m.id = r.message_id
		JOIN support_tickets t ON t.id = m.ticket_id LEFT JOIN users u ON u.id = r.user_id
		WHERE m.ticket_id = ? ORDER BY r.created_at_ms`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rr.Close()
	for rr.Next() {
		var mid string
		var r domain.TicketReaction
		if err := rr.Scan(&mid, &r.Kind, &r.UserID, &r.Staff, &r.Label); err != nil {
			return nil, err
		}
		if i, ok := at[mid]; ok {
			out[i].Reactions = append(out[i].Reactions, r)
		}
	}
	if err := rr.Err(); err != nil {
		return nil, err
	}
	rr.Close()
	ar, err := db.QueryContext(ctx, `SELECT id, COALESCE(uploader_id, ''), ticket_id, message_id, name, content_type, size, created_at_ms
		FROM support_ticket_attachments WHERE ticket_id = ? ORDER BY created_at_ms, rowid`, ticketID)
	if err != nil {
		return nil, err
	}
	defer ar.Close()
	for ar.Next() {
		var a domain.TicketAttachment
		if err := ar.Scan(&a.ID, &a.UploaderID, &a.TicketID, &a.MessageID, &a.Name, &a.ContentType, &a.Size, &a.CreatedAtMS); err != nil {
			return nil, err
		}
		if i, ok := at[a.MessageID]; ok {
			out[i].Attachments = append(out[i].Attachments, a)
		}
	}
	return out, ar.Err()
}

// GetTicketMessage returns one thread row (no reactions).
func (db *DB) GetTicketMessage(ctx context.Context, id string) (domain.TicketMessage, error) {
	return scanMessage(db.QueryRowContext(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.id = ?`, id))
}

// FirstTicketMessageID returns the id of the message that opened a ticket.
func (db *DB) FirstTicketMessageID(ctx context.Context, ticketID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM support_ticket_messages WHERE ticket_id = ? AND kind = 'message'
		ORDER BY created_at_ms, rowid LIMIT 1`, ticketID).Scan(&id)
	return id, mapErr(err)
}

// EditTicketMessage replaces a message body, keeping the old one as a revision.
func (db *DB) EditTicketMessage(ctx context.Context, msgID, body string, rev domain.TicketRevision, editorID string, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO support_message_revisions (id, message_id, body, editor_id, created_at_ms)
		SELECT ?, id, body, ?, ? FROM support_ticket_messages WHERE id = ? AND deleted_at_ms IS NULL`, rev.ID, editorID, nowMS, msgID); err != nil {
		return mapErr(err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE support_ticket_messages SET body = ?, edited_at_ms = ? WHERE id = ? AND deleted_at_ms IS NULL`, body, nowMS, msgID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

// RemoveTicketMessage blanks a message, deleting its earlier bodies and reactions.
func (db *DB) RemoveTicketMessage(ctx context.Context, msgID, byID string, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE support_ticket_messages SET body = '[removed]', deleted_at_ms = ?, deleted_by = ?, mentions_json = '[]'
		WHERE id = ? AND deleted_at_ms IS NULL`, nowMS, byID, msgID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	for _, q := range []string{`DELETE FROM support_message_revisions WHERE message_id = ?`, `DELETE FROM support_message_reactions WHERE message_id = ?`,
		`DELETE FROM support_ticket_attachments WHERE message_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, msgID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TicketMessageRevisions returns a message's earlier bodies, oldest first.
func (db *DB) TicketMessageRevisions(ctx context.Context, msgID string) ([]domain.TicketRevision, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.id, r.message_id, r.body, COALESCE(NULLIF(u.display_name, ''), u.email, ''), r.created_at_ms
		FROM support_message_revisions r LEFT JOIN users u ON u.id = r.editor_id WHERE r.message_id = ? ORDER BY r.created_at_ms, r.rowid`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TicketRevision{}
	for rows.Next() {
		var r domain.TicketRevision
		if err := rows.Scan(&r.ID, &r.MessageID, &r.Body, &r.EditorLabel, &r.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetTicketReaction adds (on) or removes one account's reaction.
func (db *DB) SetTicketReaction(ctx context.Context, msgID, userID, kind string, on bool, nowMS int64) error {
	var err error
	if on {
		_, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO support_message_reactions (message_id, user_id, kind, created_at_ms) VALUES (?,?,?,?)`, msgID, userID, kind, nowMS)
	} else {
		_, err = db.ExecContext(ctx, `DELETE FROM support_message_reactions WHERE message_id = ? AND user_id = ? AND kind = ?`, msgID, userID, kind)
	}
	return mapErr(err)
}

// ---- Labels ----

// ListTicketLabels returns every label with the number of tickets carrying it.
func (db *DB) ListTicketLabels(ctx context.Context) ([]domain.TicketLabel, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.id, l.name, l.color, l.created_at_ms, (SELECT count(*) FROM support_ticket_labels tl WHERE tl.label_id = l.id)
		FROM support_labels l ORDER BY l.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TicketLabel{}
	for rows.Next() {
		var l domain.TicketLabel
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.CreatedAtMS, &l.Tickets); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateTicketLabel stores a label (ErrConflict for a taken name).
func (db *DB) CreateTicketLabel(ctx context.Context, l domain.TicketLabel) error {
	_, err := db.ExecContext(ctx, `INSERT INTO support_labels (id, name, color, created_at_ms) VALUES (?,?,?,?)`, l.ID, l.Name, l.Color, l.CreatedAtMS)
	return mapErr(err)
}

// UpdateTicketLabel renames or recolors a label.
func (db *DB) UpdateTicketLabel(ctx context.Context, l domain.TicketLabel) error {
	res, err := db.ExecContext(ctx, `UPDATE support_labels SET name = ?, color = ? WHERE id = ?`, l.Name, l.Color, l.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteTicketLabel removes a label from every ticket and deletes it.
func (db *DB) DeleteTicketLabel(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM support_labels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetTicketLabels replaces a ticket's labels; unknown label ids answer ErrNotFound.
func (db *DB) SetTicketLabels(ctx context.Context, ticketID string, labelIDs []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM support_ticket_labels WHERE ticket_id = ?`, ticketID); err != nil {
		return err
	}
	for _, id := range labelIDs {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO support_ticket_labels (ticket_id, label_id) SELECT ?, id FROM support_labels WHERE id = ?`, ticketID, id)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var dup int
			tx.QueryRowContext(ctx, `SELECT count(*) FROM support_ticket_labels WHERE ticket_id = ? AND label_id = ?`, ticketID, id).Scan(&dup)
			if dup == 0 {
				return domain.ErrNotFound
			}
		}
	}
	return tx.Commit()
}

// ---- Saved replies ----

// ListSavedReplies returns the team's saved replies by title.
func (db *DB) ListSavedReplies(ctx context.Context) ([]domain.SavedReply, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, title, body, COALESCE(created_by, ''), updated_at_ms FROM support_saved_replies ORDER BY title COLLATE NOCASE LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SavedReply{}
	for rows.Next() {
		var r domain.SavedReply
		if err := rows.Scan(&r.ID, &r.Title, &r.Body, &r.CreatedBy, &r.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountSavedReplies counts saved replies.
func (db *DB) CountSavedReplies(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM support_saved_replies`).Scan(&n)
	return n, err
}

// PutSavedReply creates or replaces a saved reply.
func (db *DB) PutSavedReply(ctx context.Context, r domain.SavedReply) error {
	_, err := db.ExecContext(ctx, `INSERT INTO support_saved_replies (id, title, body, created_by, updated_at_ms) VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title = excluded.title, body = excluded.body, updated_at_ms = excluded.updated_at_ms`,
		r.ID, r.Title, r.Body, nullStr(r.CreatedBy), r.UpdatedAtMS)
	return mapErr(err)
}

// GetSavedReply returns one saved reply.
func (db *DB) GetSavedReply(ctx context.Context, id string) (domain.SavedReply, error) {
	var r domain.SavedReply
	err := db.QueryRowContext(ctx, `SELECT id, title, body, COALESCE(created_by, ''), updated_at_ms FROM support_saved_replies WHERE id = ?`, id).
		Scan(&r.ID, &r.Title, &r.Body, &r.CreatedBy, &r.UpdatedAtMS)
	return r, mapErr(err)
}

// DeleteSavedReply deletes a saved reply.
func (db *DB) DeleteSavedReply(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM support_saved_replies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---- Watchers ----

// SetTicketWatcher adds (on) or removes a watcher.
func (db *DB) SetTicketWatcher(ctx context.Context, ticketID, userID string, on bool, nowMS int64) error {
	var err error
	if on {
		_, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO support_ticket_watchers (ticket_id, user_id, created_at_ms) VALUES (?,?,?)`, ticketID, userID, nowMS)
	} else {
		_, err = db.ExecContext(ctx, `DELETE FROM support_ticket_watchers WHERE ticket_id = ? AND user_id = ?`, ticketID, userID)
	}
	return mapErr(err)
}

// TicketWatchers returns the accounts watching a ticket.
func (db *DB) TicketWatchers(ctx context.Context, ticketID string) ([]domain.Person, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+personCols("p")+` FROM support_ticket_watchers w JOIN users p ON p.id = w.user_id
		WHERE w.ticket_id = ? ORDER BY w.created_at_ms`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Person{}
	for rows.Next() {
		var p domain.Person
		if err := rows.Scan(personDst(&p)...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// WatchedTicketIDs returns the ids of tickets an account watches.
func (db *DB) WatchedTicketIDs(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT ticket_id FROM support_ticket_watchers WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CountTicketMessages counts every row of a ticket's thread.
func (db *DB) CountTicketMessages(ctx context.Context, ticketID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM support_ticket_messages WHERE ticket_id = ?`, ticketID).Scan(&n)
	return n, err
}

// UpdateTicket applies a change and appends messages in one transaction.
func (db *DB) UpdateTicket(ctx context.Context, id string, ch domain.TicketChange, msgs []domain.TicketMessage, nowMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	set, args := `updated_at_ms = ?`, []any{nowMS}
	if ch.Status != nil {
		set += `, status = ?, closed_at_ms = CASE WHEN ? = 'closed' THEN ? ELSE NULL END`
		args = append(args, *ch.Status, *ch.Status, nowMS)
	}
	if ch.Priority != nil {
		set += `, priority = ?`
		args = append(args, *ch.Priority)
	}
	if ch.AssigneeID != nil {
		set += `, assignee_id = ?`
		args = append(args, *ch.AssigneeID)
	}
	if ch.Subject != nil {
		set += `, subject = ?`
		args = append(args, *ch.Subject)
	}
	if ch.Category != nil {
		set += `, category = ?`
		args = append(args, *ch.Category)
	}
	if ch.BotID != nil {
		name := ""
		if ch.BotName != nil {
			name = *ch.BotName
		}
		set += `, bot_id = ?, bot_name = ?`
		args = append(args, *ch.BotID, name)
	}
	res, err := tx.ExecContext(ctx, `UPDATE support_tickets SET `+set+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	for _, m := range msgs {
		if err := insertTicketMessage(ctx, tx, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- Attachments ----

// PutTicketAttachment stores a pending upload.
func (db *DB) PutTicketAttachment(ctx context.Context, a domain.TicketAttachment) error {
	_, err := db.ExecContext(ctx, `INSERT INTO support_ticket_attachments (id, uploader_id, name, content_type, size, data, created_at_ms)
		VALUES (?,?,?,?,?,?,?)`, a.ID, a.UploaderID, a.Name, a.ContentType, a.Size, a.Data, a.CreatedAtMS)
	return mapErr(err)
}

// CountPendingTicketAttachments counts an account's unsent uploads.
func (db *DB) CountPendingTicketAttachments(ctx context.Context, uploaderID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM support_ticket_attachments WHERE uploader_id = ? AND message_id IS NULL`, uploaderID).Scan(&n)
	return n, err
}

// PrunePendingTicketAttachments deletes unsent uploads created before beforeMS.
func (db *DB) PrunePendingTicketAttachments(ctx context.Context, beforeMS int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM support_ticket_attachments WHERE message_id IS NULL AND created_at_ms < ?`, beforeMS)
	return err
}

// PendingTicketAttachments returns the account's unsent uploads among ids
// (no bytes).
func (db *DB) PendingTicketAttachments(ctx context.Context, uploaderID string, ids []string) ([]domain.TicketAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{uploaderID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, `SELECT id, name, content_type, size, created_at_ms FROM support_ticket_attachments
		WHERE uploader_id = ? AND message_id IS NULL AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TicketAttachment{}
	for rows.Next() {
		a := domain.TicketAttachment{UploaderID: uploaderID}
		if err := rows.Scan(&a.ID, &a.Name, &a.ContentType, &a.Size, &a.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeletePendingTicketAttachment deletes one of the account's unsent uploads.
func (db *DB) DeletePendingTicketAttachment(ctx context.Context, id, uploaderID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM support_ticket_attachments WHERE id = ? AND uploader_id = ? AND message_id IS NULL`, id, uploaderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetTicketAttachment returns a sent attachment of a ticket with its bytes.
func (db *DB) GetTicketAttachment(ctx context.Context, ticketID, id string) (domain.TicketAttachment, error) {
	var a domain.TicketAttachment
	err := db.QueryRowContext(ctx, `SELECT id, COALESCE(uploader_id, ''), ticket_id, message_id, name, content_type, size, data, created_at_ms
		FROM support_ticket_attachments WHERE id = ? AND ticket_id = ?`, id, ticketID).
		Scan(&a.ID, &a.UploaderID, &a.TicketID, &a.MessageID, &a.Name, &a.ContentType, &a.Size, &a.Data, &a.CreatedAtMS)
	return a, mapErr(err)
}
