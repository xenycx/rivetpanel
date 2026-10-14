package domain

// AlertPrefs are a bot's notification preferences.
type AlertPrefs struct {
	Crash          bool
	Deploy         bool
	Backup         bool
	Recovery       bool
	HeartbeatAfter int // seconds without a push before alerting; 0 = off
}

// DefaultAlertPrefs apply to bots without saved preferences.
var DefaultAlertPrefs = AlertPrefs{Crash: true, Deploy: true, Backup: true, Recovery: true}

// BotHealth is what the bot itself last reported through the SDK.
type BotHealth struct {
	BotID        string
	LastSeenAtMS int64
	Ready        *bool
	StaleAlerted bool
}

// HeartbeatWatch is a bot with a heartbeat alert rule and its last report
// (nil when it never pushed).
type HeartbeatWatch struct {
	Bot    Bot
	Prefs  AlertPrefs
	Health *BotHealth
}

// HealthProbe is a loopback TCP/HTTP check against one of a bot's published
// TCP ports. Runtime state lives with the configuration so it survives restarts.
type HealthProbe struct {
	BotID                string
	Kind                 string // tcp | http; empty means disabled
	HostPort             int
	Path                 string
	IntervalSeconds      int
	TimeoutMS            int
	FailureThreshold     int
	SuccessThreshold     int
	StartupGraceSeconds  int
	RestartUnhealthy     bool
	Status               string // unknown | starting | healthy | unhealthy
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastCheckedAtMS      *int64
	LastError            *string
	UpdatedAtMS          int64
	// Target is ProbeHost (HostPort is a published port on the node) or
	// ProbeApp (HostPort is a site app's port, dialled on its container).
	Target string
}

// Health probe targets.
const (
	ProbeHost = "host"
	ProbeApp  = "app"
)
