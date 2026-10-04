package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/xenycx/rivetpanel/internal/domain"
)

const aiProviderCols = `id,name,enabled,is_default,base_url,chat_path,models_path,default_model,context_size,
	max_output_tokens,temperature,timeout_ms,input_price_micros,output_price_micros,key_cipher,key_nonce,key_id,created_at_ms,updated_at_ms`

func scanAIProvider(row interface{ Scan(...any) error }) (domain.AIProviderProfile, error) {
	var p domain.AIProviderProfile
	var enabled, def int
	err := row.Scan(&p.ID, &p.Name, &enabled, &def, &p.BaseURL, &p.ChatPath, &p.ModelsPath, &p.DefaultModel,
		&p.ContextSize, &p.MaxOutputTokens, &p.Temperature, &p.TimeoutMS, &p.InputPriceMicros, &p.OutputPriceMicros,
		&p.KeyCipher, &p.KeyNonce, &p.KeyID, &p.CreatedAtMS, &p.UpdatedAtMS)
	p.Enabled, p.Default = enabled != 0, def != 0
	return p, mapErr(err)
}

func (db *DB) ListAIProviders(ctx context.Context) ([]domain.AIProviderProfile, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+aiProviderCols+` FROM ai_provider_profiles ORDER BY is_default DESC, lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AIProviderProfile
	for rows.Next() {
		p, err := scanAIProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) GetAIProvider(ctx context.Context, id string) (domain.AIProviderProfile, error) {
	return scanAIProvider(db.QueryRowContext(ctx, `SELECT `+aiProviderCols+` FROM ai_provider_profiles WHERE id=?`, id))
}

func (db *DB) PutAIProvider(ctx context.Context, p domain.AIProviderProfile) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if p.Default {
			if _, err := tx.ExecContext(ctx, `UPDATE ai_provider_profiles SET is_default=0 WHERE id<>?`, p.ID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_provider_profiles (`+aiProviderCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,is_default=excluded.is_default,
			base_url=excluded.base_url,chat_path=excluded.chat_path,models_path=excluded.models_path,default_model=excluded.default_model,
			context_size=excluded.context_size,max_output_tokens=excluded.max_output_tokens,temperature=excluded.temperature,
			timeout_ms=excluded.timeout_ms,input_price_micros=excluded.input_price_micros,output_price_micros=excluded.output_price_micros,
			key_cipher=COALESCE(excluded.key_cipher,ai_provider_profiles.key_cipher),key_nonce=COALESCE(excluded.key_nonce,ai_provider_profiles.key_nonce),
			key_id=COALESCE(excluded.key_id,ai_provider_profiles.key_id),updated_at_ms=excluded.updated_at_ms`,
			p.ID, p.Name, boolInt(p.Enabled), boolInt(p.Default), p.BaseURL, p.ChatPath, p.ModelsPath, p.DefaultModel, p.ContextSize,
			p.MaxOutputTokens, p.Temperature, p.TimeoutMS, p.InputPriceMicros, p.OutputPriceMicros, p.KeyCipher, p.KeyNonce, p.KeyID, p.CreatedAtMS, p.UpdatedAtMS)
		return mapErr(err)
	})
}

func (db *DB) DeleteAIProvider(ctx context.Context, id string) error {
	r, err := db.ExecContext(ctx, `DELETE FROM ai_provider_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanAIConversation(row interface{ Scan(...any) error }) (domain.AIConversation, error) {
	var v domain.AIConversation
	err := row.Scan(&v.ID, &v.CreatorID, &v.BotID, &v.SiteID, &v.Title, &v.ProviderID, &v.Model, &v.CreatedAtMS, &v.UpdatedAtMS)
	return v, mapErr(err)
}

func (db *DB) CreateAIConversation(ctx context.Context, v domain.AIConversation) error {
	_, err := db.ExecContext(ctx, `INSERT INTO ai_conversations(id,creator_id,bot_id,site_id,title,provider_id,model,created_at_ms,updated_at_ms) VALUES(?,?,?,?,?,?,?,?,?)`,
		v.ID, v.CreatorID, v.BotID, v.SiteID, v.Title, v.ProviderID, v.Model, v.CreatedAtMS, v.UpdatedAtMS)
	return mapErr(err)
}

func (db *DB) GetAIConversation(ctx context.Context, id string) (domain.AIConversation, error) {
	return scanAIConversation(db.QueryRowContext(ctx, `SELECT id,creator_id,bot_id,site_id,title,provider_id,model,created_at_ms,updated_at_ms FROM ai_conversations WHERE id=?`, id))
}

func (db *DB) ListAIConversations(ctx context.Context, creatorID string, botID, siteID *string, admin bool) ([]domain.AIConversation, error) {
	q := `SELECT id,creator_id,bot_id,site_id,title,provider_id,model,created_at_ms,updated_at_ms FROM ai_conversations WHERE `
	var args []any
	if botID != nil {
		q += `bot_id=?`
		args = append(args, *botID)
	} else {
		q += `site_id=?`
		args = append(args, *siteID)
	}
	if !admin {
		q += ` AND creator_id=?`
		args = append(args, creatorID)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY updated_at_ms DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AIConversation
	for rows.Next() {
		v, e := scanAIConversation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListUserAIConversations returns one person's conversations across the whole
// panel, newest first. Administrators get no wider view: a chat is private to
// its creator.
func (db *DB) ListUserAIConversations(ctx context.Context, creatorID string, limit int) ([]domain.AIConversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id,creator_id,bot_id,site_id,title,provider_id,model,created_at_ms,updated_at_ms FROM ai_conversations WHERE creator_id=? ORDER BY updated_at_ms DESC LIMIT ?`, creatorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AIConversation
	for rows.Next() {
		v, e := scanAIConversation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (db *DB) UpdateAIConversation(ctx context.Context, v domain.AIConversation) error {
	r, err := db.ExecContext(ctx, `UPDATE ai_conversations SET title=?,provider_id=?,model=?,updated_at_ms=? WHERE id=?`, v.Title, v.ProviderID, v.Model, v.UpdatedAtMS, v.ID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) DeleteAIConversation(ctx context.Context, id string) error {
	r, e := db.ExecContext(ctx, `DELETE FROM ai_conversations WHERE id=?`, id)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func contextOrEmpty(v string) string {
	if v == "" {
		return "{}"
	}
	return v
}

func (db *DB) InsertAIMessage(ctx context.Context, m domain.AIMessage) error {
	_, e := db.ExecContext(ctx, `INSERT INTO ai_messages(id,conversation_id,role,content,citations_json,context_json,created_at_ms) VALUES(?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Role, m.Content, m.CitationsJSON, contextOrEmpty(m.ContextJSON), m.CreatedAtMS)
	return mapErr(e)
}

func (db *DB) ListAIMessages(ctx context.Context, conversationID string, limit int) ([]domain.AIMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, e := db.QueryContext(ctx, `SELECT id,conversation_id,role,content,citations_json,context_json,created_at_ms FROM (SELECT * FROM ai_messages WHERE conversation_id=? ORDER BY created_at_ms DESC LIMIT ?) ORDER BY created_at_ms`, conversationID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.AIMessage
	for rows.Next() {
		var m domain.AIMessage
		if e = rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CitationsJSON, &m.ContextJSON, &m.CreatedAtMS); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (db *DB) InsertAIRun(ctx context.Context, r domain.AIRun) error {
	_, e := db.ExecContext(ctx, `INSERT INTO ai_runs(id,conversation_id,user_id,provider_id,model,mode,status,limits_json,plan_json,auto_approved_at_ms,input_tokens,output_tokens,error_code,error_message,created_at_ms,started_at_ms,finished_at_ms,bot_id,site_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.ConversationID, r.UserID, r.ProviderID, r.Model, r.Mode, r.Status, r.LimitsJSON, r.PlanJSON, r.AutoApprovedAtMS, r.InputTokens, r.OutputTokens, r.ErrorCode, r.ErrorMessage, r.CreatedAtMS, r.StartedAtMS, r.FinishedAtMS, r.BotID, r.SiteID)
	return mapErr(e)
}

func scanAIRun(row interface{ Scan(...any) error }) (domain.AIRun, error) {
	var r domain.AIRun
	e := row.Scan(&r.ID, &r.ConversationID, &r.UserID, &r.ProviderID, &r.Model, &r.Mode, &r.Status, &r.LimitsJSON, &r.PlanJSON, &r.AutoApprovedAtMS, &r.InputTokens, &r.OutputTokens, &r.ErrorCode, &r.ErrorMessage, &r.CreatedAtMS, &r.StartedAtMS, &r.FinishedAtMS, &r.BotID, &r.SiteID)
	return r, mapErr(e)
}

const aiRunCols = `id,conversation_id,user_id,provider_id,model,mode,status,limits_json,plan_json,auto_approved_at_ms,input_tokens,output_tokens,error_code,error_message,created_at_ms,started_at_ms,finished_at_ms,bot_id,site_id`

func (db *DB) GetAIRun(ctx context.Context, id string) (domain.AIRun, error) {
	return scanAIRun(db.QueryRowContext(ctx, `SELECT `+aiRunCols+` FROM ai_runs WHERE id=?`, id))
}

func (db *DB) UpdateAIRun(ctx context.Context, r domain.AIRun) error {
	_, e := db.ExecContext(ctx, `UPDATE ai_runs SET bot_id=?,site_id=?,status=?,plan_json=?,auto_approved_at_ms=?,input_tokens=?,output_tokens=?,error_code=?,error_message=?,started_at_ms=?,finished_at_ms=? WHERE id=?`, r.BotID, r.SiteID, r.Status, r.PlanJSON, r.AutoApprovedAtMS, r.InputTokens, r.OutputTokens, r.ErrorCode, r.ErrorMessage, r.StartedAtMS, r.FinishedAtMS, r.ID)
	return e
}

func (db *DB) InterruptAIRuns(ctx context.Context, nowMS int64) (int64, error) {
	r, e := db.ExecContext(ctx, `UPDATE ai_runs SET status='interrupted',error_code='panel_restart',error_message='The panel restarted during this run. Retry from the retained conversation.',finished_at_ms=? WHERE status IN ('queued','running','waiting_approval')`, nowMS)
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

func (db *DB) PruneAIConversations(ctx context.Context, beforeMS int64, batch int) (int64, error) {
	r, e := db.ExecContext(ctx, `DELETE FROM ai_conversations WHERE id IN (SELECT id FROM ai_conversations WHERE updated_at_ms<? ORDER BY updated_at_ms LIMIT ?)`, beforeMS, batch)
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

func (db *DB) InsertAIToolCall(ctx context.Context, c domain.AIToolCall) error {
	_, e := db.ExecContext(ctx, `INSERT INTO ai_tool_calls(id,run_id,call_index,name,arguments_json,output,approval_state,status,exit_code,duration_ms,error_message,created_at_ms,finished_at_ms,provider_call_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.ID, c.RunID, c.CallIndex, c.Name, c.ArgumentsJSON, c.Output, c.ApprovalState, c.Status, c.ExitCode, c.DurationMS, c.ErrorMessage, c.CreatedAtMS, c.FinishedAtMS, c.ProviderCallID)
	return mapErr(e)
}

const aiToolCallCols = `id,run_id,call_index,name,arguments_json,output,approval_state,status,exit_code,duration_ms,error_message,created_at_ms,finished_at_ms,provider_call_id`

func scanAIToolCall(row interface{ Scan(...any) error }) (domain.AIToolCall, error) {
	var c domain.AIToolCall
	e := row.Scan(&c.ID, &c.RunID, &c.CallIndex, &c.Name, &c.ArgumentsJSON, &c.Output, &c.ApprovalState, &c.Status, &c.ExitCode, &c.DurationMS, &c.ErrorMessage, &c.CreatedAtMS, &c.FinishedAtMS, &c.ProviderCallID)
	return c, mapErr(e)
}

func (db *DB) GetAIToolCall(ctx context.Context, id string) (domain.AIToolCall, error) {
	return scanAIToolCall(db.QueryRowContext(ctx, `SELECT `+aiToolCallCols+` FROM ai_tool_calls WHERE id=?`, id))
}

// ListAIRuns returns a conversation's latest runs, newest first.
func (db *DB) ListAIRuns(ctx context.Context, conversationID string, limit int) ([]domain.AIRun, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, e := db.QueryContext(ctx, `SELECT `+aiRunCols+` FROM ai_runs WHERE conversation_id=? ORDER BY created_at_ms DESC, rowid DESC LIMIT ?`, conversationID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.AIRun
	for rows.Next() {
		r, e := scanAIRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAIToolCalls returns a run's tool calls in the order they were made
// (the Auto envelope, index -1, first).
func (db *DB) ListAIToolCalls(ctx context.Context, runID string) ([]domain.AIToolCall, error) {
	rows, e := db.QueryContext(ctx, `SELECT `+aiToolCallCols+` FROM ai_tool_calls WHERE run_id=? ORDER BY call_index, created_at_ms LIMIT 500`, runID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.AIToolCall
	for rows.Next() {
		c, e := scanAIToolCall(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAIChangeSets returns a run's change sets with their files, oldest first.
func (db *DB) ListAIChangeSets(ctx context.Context, runID string) ([]domain.AIChangeSet, error) {
	rows, e := db.QueryContext(ctx, `SELECT id FROM ai_change_sets WHERE run_id=? ORDER BY created_at_ms, rowid LIMIT 200`, runID)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := make([]domain.AIChangeSet, 0, len(ids))
	for _, id := range ids {
		c, e := db.GetAIChangeSet(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}

func (db *DB) UpdateAIToolCall(ctx context.Context, c domain.AIToolCall) error {
	r, e := db.ExecContext(ctx, `UPDATE ai_tool_calls SET output=?,approval_state=?,status=?,exit_code=?,duration_ms=?,error_message=?,finished_at_ms=? WHERE id=?`, c.Output, c.ApprovalState, c.Status, c.ExitCode, c.DurationMS, c.ErrorMessage, c.FinishedAtMS, c.ID)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) CountActiveAIRuns(ctx context.Context, userID, targetKind, targetID string) (user, global, target int, error error) {
	error = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_runs WHERE user_id=? AND status IN ('queued','running','waiting_approval')`, userID).Scan(&user)
	if error != nil {
		return
	}
	error = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_runs WHERE status IN ('queued','running','waiting_approval')`).Scan(&global)
	if error != nil {
		return
	}
	if targetID == "" {
		return // a run with no target shares no per-target cap
	}
	col := "bot_id"
	if targetKind == "site" {
		col = "site_id"
	}
	error = db.QueryRowContext(ctx, `SELECT count(*) FROM ai_runs WHERE `+col+`=? AND status IN ('queued','running','waiting_approval')`, targetID).Scan(&target)
	return
}

func (db *DB) InsertAIChangeSet(ctx context.Context, c domain.AIChangeSet) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO ai_change_sets(id,run_id,target_kind,target_id,status,summary,created_at_ms,applied_at_ms,reverted_at_ms) VALUES(?,?,?,?,?,?,?,?,?)`, c.ID, c.RunID, c.TargetKind, c.TargetID, c.Status, c.Summary, c.CreatedAtMS, c.AppliedAtMS, c.RevertedAtMS)
		if e != nil {
			return mapErr(e)
		}
		for _, f := range c.Files {
			_, e = tx.ExecContext(ctx, `INSERT INTO ai_change_files(change_set_id,path,operation,before_gzip,after_gzip,before_revision,after_revision,mode,diff) VALUES(?,?,?,?,?,?,?,?,?)`, c.ID, f.Path, f.Operation, f.BeforeGzip, f.AfterGzip, f.BeforeRevision, f.AfterRevision, f.Mode, f.Diff)
			if e != nil {
				return mapErr(e)
			}
		}
		return nil
	})
}

func (db *DB) GetAIChangeSet(ctx context.Context, id string) (domain.AIChangeSet, error) {
	var c domain.AIChangeSet
	e := db.QueryRowContext(ctx, `SELECT id,run_id,target_kind,target_id,status,summary,created_at_ms,applied_at_ms,reverted_at_ms FROM ai_change_sets WHERE id=?`, id).Scan(&c.ID, &c.RunID, &c.TargetKind, &c.TargetID, &c.Status, &c.Summary, &c.CreatedAtMS, &c.AppliedAtMS, &c.RevertedAtMS)
	if e != nil {
		return c, mapErr(e)
	}
	rows, e := db.QueryContext(ctx, `SELECT change_set_id,path,operation,before_gzip,after_gzip,before_revision,after_revision,mode,diff FROM ai_change_files WHERE change_set_id=? ORDER BY path`, id)
	if e != nil {
		return c, e
	}
	defer rows.Close()
	for rows.Next() {
		var f domain.AIChangeFile
		if e = rows.Scan(&f.ChangeSetID, &f.Path, &f.Operation, &f.BeforeGzip, &f.AfterGzip, &f.BeforeRevision, &f.AfterRevision, &f.Mode, &f.Diff); e != nil {
			return c, e
		}
		c.Files = append(c.Files, f)
	}
	return c, rows.Err()
}

func (db *DB) UpdateAIChangeSet(ctx context.Context, c domain.AIChangeSet) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, `UPDATE ai_change_sets SET status=?,applied_at_ms=?,reverted_at_ms=? WHERE id=?`, c.Status, c.AppliedAtMS, c.RevertedAtMS, c.ID)
		if e != nil {
			return e
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		for _, f := range c.Files {
			if _, e = tx.ExecContext(ctx, `UPDATE ai_change_files SET after_revision=? WHERE change_set_id=? AND path=?`, f.AfterRevision, c.ID, f.Path); e != nil {
				return e
			}
		}
		return nil
	})
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
