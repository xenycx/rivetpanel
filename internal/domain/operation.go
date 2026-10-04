package domain

// Operation kinds.
const (
	OpBuild    = "build"
	OpDeploy   = "deploy"
	OpBackup   = "backup"
	OpRestore  = "restore"
	OpRollback = "rollback"
	OpPublish  = "publish" // push the workspace to GitHub
)

// Operation statuses. Queued and running are active; the rest are final.
const (
	OpQueued      = "queued"
	OpRunning     = "running"
	OpSucceeded   = "succeeded"
	OpFailed      = "failed"
	OpCancelled   = "cancelled"
	OpInterrupted = "interrupted" // the panel stopped while it was active
)

// Operation is one durable unit of long-running work on a bot. Message and
// ResultCode are safe to show to anyone who may view the bot.
type Operation struct {
	ID          string
	BotID       string
	BotName     string // filled by listing queries
	BotKind     string // bot | game, filled by listing queries
	Kind        string
	Trigger     string
	ActorID     *string
	ActorEmail  *string // filled by listing queries
	Status      string
	Stage       string
	SourceRef   *string
	SourceLabel *string
	Generation  *int64
	ResultCode  *string
	Message     *string
	DetailJSON  *string
	LogBytes    int64
	CreatedAtMS int64
	StartedAtMS *int64
	FinishedAt  *int64
}

// Active reports whether the operation has not finished.
func (o Operation) Active() bool { return o.Status == OpQueued || o.Status == OpRunning }
