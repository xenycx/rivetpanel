package domain

// Schedule is a recurring action on a bot.
type Schedule struct {
	ID          string
	BotID       string
	OwnerID     string
	OwnerEmail  string // filled by listing queries
	Action      string // backup | start | stop | restart | deploy | chain
	Spec        string // five-field cron
	Timezone    string
	Enabled     bool
	NextRunMS   *int64
	LastRunMS   *int64
	LastStatus  *string
	LastMessage *string
	CreatedAtMS int64
	UpdatedAtMS int64
	// Tasks run in order when Action is "chain".
	Tasks []ScheduleTask
}

// ScheduleTask is one step of a task-chain schedule.
type ScheduleTask struct {
	Action            string // command | start | stop | restart | kill | backup
	Payload           string // the console command for "command"
	DelaySeconds      int    // wait before this task
	ContinueOnFailure bool
}

// ScheduleChain marks a task-chain schedule.
const ScheduleChain = "chain"
