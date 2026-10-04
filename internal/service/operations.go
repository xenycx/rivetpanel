package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/oplog"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// OpStore is the persistence surface of operations.
type OpStore interface {
	InsertOperation(ctx context.Context, o domain.Operation) ([]string, error)
	SetOperationStage(ctx context.Context, id, stage string, nowMS int64) error
	SetOperationSource(ctx context.Context, id string, ref, label *string) error
	FinishOperation(ctx context.Context, id, status string, code, msg, detail *string, logBytes, nowMS int64) error
	GetOperation(ctx context.Context, id string) (domain.Operation, error)
	ListOperations(ctx context.Context, f sqlite.OperationFilter) ([]domain.Operation, error)
	InterruptActiveOperations(ctx context.Context, nowMS int64) ([]domain.Operation, error)
	OperationIDs(ctx context.Context) (map[string]bool, error)
}

// Operations records long-running work durably and serves it to users who
// may view the bot. Recording is best effort: a failed write is logged and
// never fails the work itself.
type Operations struct {
	Store OpStore
	Bots  *BotService
	Logs  *oplog.Store // nil: output is not retained
	Log   *slog.Logger
	Now   func() time.Time
}

func (o *Operations) now() int64 {
	if o.Now != nil {
		return o.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (o *Operations) warn(msg string, err error) {
	if o.Log != nil && err != nil {
		o.Log.Warn("operations: "+msg, "err", err)
	}
}

// bg detaches recording from request or job cancellation, with a deadline.
func bg(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

// OpStart describes a new operation.
type OpStart struct {
	BotID       string
	Kind        string
	Trigger     string
	ActorID     *string
	Generation  *int64
	SourceRef   *string
	SourceLabel *string
	Queued      bool // waiting for a slot rather than running already
}

// Begin records a new operation and returns its ID ("" when recording failed).
func (o *Operations) Begin(ctx context.Context, in OpStart) string {
	if o == nil {
		return ""
	}
	ctx, cancel := bg(ctx)
	defer cancel()
	now := o.now()
	op := domain.Operation{ID: uuid.NewString(), BotID: in.BotID, Kind: in.Kind, Trigger: in.Trigger, ActorID: in.ActorID,
		Status: domain.OpRunning, Generation: in.Generation, SourceRef: in.SourceRef, SourceLabel: in.SourceLabel, CreatedAtMS: now}
	if in.Queued {
		op.Status = domain.OpQueued
	} else {
		op.StartedAtMS = &now
	}
	pruned, err := o.Store.InsertOperation(ctx, op)
	if err != nil {
		o.warn("record start", err)
		return ""
	}
	if o.Logs != nil {
		for _, id := range pruned {
			o.Logs.Remove(id)
		}
	}
	return op.ID
}

// Stage moves an operation to a named stage (and to running).
func (o *Operations) Stage(ctx context.Context, id, stage string) {
	if o == nil || id == "" {
		return
	}
	ctx, cancel := bg(ctx)
	defer cancel()
	o.warn("record stage", o.Store.SetOperationStage(ctx, id, stage, o.now()))
}

// Source records what the operation works on once known.
func (o *Operations) Source(ctx context.Context, id string, ref, label string) {
	if o == nil || id == "" {
		return
	}
	ctx, cancel := bg(ctx)
	defer cancel()
	var r, l *string
	if ref != "" {
		r = &ref
	}
	if label != "" {
		l = &label
	}
	o.warn("record source", o.Store.SetOperationSource(ctx, id, r, l))
}

// Output returns a writer for the operation's output (nil when not retained).
func (o *Operations) Output(id string) io.WriteCloser {
	if o == nil || id == "" || o.Logs == nil {
		return nil
	}
	w, err := o.Logs.Open(id)
	if err != nil {
		return nil
	}
	return w
}

// Finish records the final outcome. detail must marshal to a small JSON object.
func (o *Operations) Finish(ctx context.Context, id, status, code, msg string, detail any) {
	if o == nil || id == "" {
		return
	}
	ctx, cancel := bg(ctx)
	defer cancel()
	var c, m, d *string
	if code != "" {
		c = &code
	}
	if msg != "" {
		m = &msg
	}
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil && len(b) <= 4096 {
			s := string(b)
			d = &s
		}
	}
	var logBytes int64
	if o.Logs != nil {
		if ch, err := o.Logs.Read(id, 1<<62, 1); err == nil {
			logBytes = ch.Total
		}
	}
	o.warn("record finish", o.Store.FinishOperation(ctx, id, status, c, m, d, logBytes, o.now()))
}

// Recover marks operations left active by a previous process as interrupted
// and removes retained output of operations that no longer exist.
func (o *Operations) Recover(ctx context.Context) []domain.Operation {
	ops, err := o.Store.InterruptActiveOperations(ctx, o.now())
	o.warn("interrupt stale", err)
	if o.Logs != nil {
		if ids, err := o.Store.OperationIDs(ctx); err == nil {
			o.Logs.Sweep(ids)
		}
	}
	return ops
}

// ---- reading (authorized) ----

// ListForBot returns a bot's operations, newest first. Anyone with access to
// the bot may see its operation history (no secrets are recorded in it).
func (o *Operations) ListForBot(ctx context.Context, actor domain.User, botID string, kinds []string, before int64, limit int) ([]domain.Operation, error) {
	if _, err := o.Bots.Authorize(ctx, actor, botID, permAny); err != nil {
		return nil, err
	}
	return o.Store.ListOperations(ctx, sqlite.OperationFilter{BotID: botID, Kinds: kinds, BeforeMS: before, Limit: limit})
}

// Activity returns operations across every bot the actor can see.
func (o *Operations) Activity(ctx context.Context, actor domain.User, active bool, kinds []string, before int64, limit int) ([]domain.Operation, error) {
	f := sqlite.OperationFilter{Active: active, Kinds: kinds, BeforeMS: before, Limit: limit}
	if !actor.IsAdmin() {
		f.UserID = actor.ID
	}
	return o.Store.ListOperations(ctx, f)
}

// ForWorkspace returns recent operations on a workspace's bots to any member
// (administrators: any workspace).
func (o *Operations) ForWorkspace(ctx context.Context, actor domain.User, workspaceID string, limit int) ([]domain.Operation, error) {
	if _, err := o.Bots.workspaceRole(ctx, actor, workspaceID); err != nil {
		return nil, err
	}
	return o.Store.ListOperations(ctx, sqlite.OperationFilter{WorkspaceID: workspaceID, Limit: limit})
}

// ForOwner returns recent operations on the bots an account owns
// (administrators only).
func (o *Operations) ForOwner(ctx context.Context, actor domain.User, ownerID string, limit int) ([]domain.Operation, error) {
	if !actor.Can(domain.PermUsersView) {
		return nil, domain.ErrForbidden
	}
	return o.Store.ListOperations(ctx, sqlite.OperationFilter{OwnerID: ownerID, Limit: limit})
}

// Get returns one operation of a bot.
func (o *Operations) Get(ctx context.Context, actor domain.User, botID, id string) (domain.Operation, error) {
	if _, err := o.Bots.Authorize(ctx, actor, botID, permAny); err != nil {
		return domain.Operation{}, err
	}
	op, err := o.Store.GetOperation(ctx, id)
	if err != nil || op.BotID != botID {
		return domain.Operation{}, domain.ErrNotFound
	}
	return op, nil
}

// ReadOutput returns retained output. Build output can contain anything the
// build printed, so it needs the console permission, like runtime logs.
func (o *Operations) ReadOutput(ctx context.Context, actor domain.User, botID, id string, offset int64, limit int) (oplog.Chunk, error) {
	if _, err := o.Bots.Authorize(ctx, actor, botID, domain.PermViewConsole); err != nil {
		return oplog.Chunk{}, err
	}
	if _, err := o.Get(ctx, actor, botID, id); err != nil {
		return oplog.Chunk{}, err
	}
	if o.Logs == nil {
		return oplog.Chunk{}, domain.ErrNotFound
	}
	c, err := o.Logs.Read(id, offset, limit)
	if err == oplog.ErrUnknown {
		return oplog.Chunk{}, domain.ErrNotFound
	}
	return c, err
}

// ---- runner.BuildRecorder ----

// BuildStarted records a build of the given generation.
func (o *Operations) BuildStarted(ctx context.Context, botID string, generation int64) string {
	g := generation
	return o.Begin(ctx, OpStart{BotID: botID, Kind: domain.OpBuild, Trigger: "start", Generation: &g})
}

func (o *Operations) BuildStage(ctx context.Context, id, stage string) { o.Stage(ctx, id, stage) }

func (o *Operations) BuildOutput(id string) io.WriteCloser { return o.Output(id) }

func (o *Operations) BuildFinished(ctx context.Context, id, status, code, msg string) {
	o.Finish(ctx, id, status, code, msg, nil)
}
