package sqlite

import (
	"context"
	"database/sql"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const ticketCols = `t.id, t.number, t.user_id, COALESCE(u.email, ''), t.subject, t.category, t.priority, t.status,
	t.bot_id, t.bot_name, t.assignee_id, COALESCE(a.email, ''), t.created_at_ms, t.updated_at_ms, t.closed_at_ms`

const ticketFrom = ` FROM support_tickets t LEFT JOIN users u ON u.id = t.user_id LEFT JOIN users a ON a.id = t.assignee_id`

func scanTicket(row interface{ Scan(...any) error }, extra ...any) (domain.Ticket, error) {
	var t domain.Ticket
	dst := []any{&t.ID, &t.Number, &t.UserID, &t.UserEmail, &t.Subject, &t.Category, &t.Priority, &t.Status,
		&t.BotID, &t.BotName, &t.AssigneeID, &t.AssigneeEmail, &t.CreatedAtMS, &t.UpdatedAtMS, &t.ClosedAtMS}
	err := row.Scan(append(dst, extra...)...)
	return t, mapErr(err)
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
	_, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_messages (id, ticket_id, author_id, author_label, kind, staff, internal, body, created_at_ms)
		VALUES (?,?,?,?,?,?,?,?,?)`, m.ID, m.TicketID, m.AuthorID, m.AuthorLabel, m.Kind, boolInt(m.Staff), boolInt(m.Internal), m.Body, m.CreatedAtMS)
	return mapErr(err)
}

// GetTicket returns one ticket.
func (db *DB) GetTicket(ctx context.Context, id string) (domain.Ticket, error) {
	return scanTicket(db.QueryRowContext(ctx, `SELECT `+ticketCols+ticketFrom+` WHERE t.id = ?`, id))
}

// ListTickets returns tickets matching f, most recently updated first, with
// the number of messages a reader sees (internal notes counted only when
// withInternal).
func (db *DB) ListTickets(ctx context.Context, f domain.TicketFilter, withInternal bool) ([]domain.Ticket, error) {
	q := `SELECT ` + ticketCols + `, (SELECT count(*) FROM support_ticket_messages m WHERE m.ticket_id = t.id AND m.kind = 'message'`
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
	return out, rows.Err()
}

// CountActiveTickets counts an account's open and pending tickets.
func (db *DB) CountActiveTickets(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM support_tickets WHERE user_id = ? AND status IN ('open', 'pending')`, userID).Scan(&n)
	return n, err
}

// TicketMessages returns a ticket's messages, oldest first; internal notes
// only when withInternal.
func (db *DB) TicketMessages(ctx context.Context, ticketID string, withInternal bool) ([]domain.TicketMessage, error) {
	q := `SELECT id, ticket_id, author_id, author_label, kind, staff, internal, body, created_at_ms FROM support_ticket_messages WHERE ticket_id = ?`
	if !withInternal {
		q += ` AND internal = 0`
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY created_at_ms, rowid LIMIT 2000`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TicketMessage{}
	for rows.Next() {
		var m domain.TicketMessage
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.AuthorLabel, &m.Kind, &m.Staff, &m.Internal, &m.Body, &m.CreatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, m)
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
