package domain

import "strings"

const (
	DesiredStopped = "stopped"
	DesiredRunning = "running"
	DesiredDeleted = "deleted"
)

// Kinds of hosted resources.
const (
	KindBot  = "bot"  // an application from a language runtime (Discord bots and similar)
	KindGame = "game" // a game server defined by a blueprint revision
)

// Install states of a game server.
const (
	InstallNone       = "none" // not a game server
	InstallPending    = "pending"
	InstallInstalling = "installing"
	InstallInstalled  = "installed"
	InstallFailed     = "failed"
)

// Bot is a hosted resource (a Discord bot or similar application, or a game
// server) and its desired/observed lifecycle state.
type Bot struct {
	ID          string
	OwnerID     string
	WorkspaceID string
	NodeID      string
	Name        string
	// Kind is KindBot or KindGame ("" reads as KindBot).
	Kind string
	// Game servers: the pinned blueprint revision, the chosen image label
	// ("" = automatic) and installation state.
	BlueprintID        string
	BlueprintRevision  int64
	ImageChoice        string
	InstallState       string
	InstalledVersion   string       // version the last installation resolved
	Allocations        []Allocation // game servers; loaded by GetBot only
	Runtime            string
	ImageRef           string
	Argv               []string
	MemoryBytes        int64
	NanoCPUs           int64
	PidsLimit          int64
	DesiredState       string
	ObservedState      string
	Generation         int64
	ObservedGeneration int64
	ContainerID        *string
	LastExitCode       *int64
	LastError          *string
	ObservedAtMS       *int64
	CreatedAtMS        int64
	UpdatedAtMS        int64
	DiscordUserID      string
	DiscordUsername    string
	DiscordAvatarURL   string

	// Startup. When Entrypoint is set it is the container entrypoint (its first
	// element must be a permitted command) and Argv are its arguments; otherwise
	// Argv itself is the exec-form entrypoint.
	Entrypoint []string
	SourceType string // manual | template | github ("" = manual)
	TemplateID *string

	// Network. Zero values keep the historical behaviour (outbound allowed, no
	// published ports). BandwidthKbps is recorded intent: Docker has no native
	// bandwidth cap, so it is not enforced without host traffic shaping.
	NetworkDisabled bool
	BandwidthKbps   *int64
	Ports           []BotPort // loaded by GetBot only

	// BuildCommand, when set, is a shell script that replaces the runtime's
	// default build step. It runs in the builder container (no bot secrets).
	BuildCommand string
	// Addons are companion services on the bot's private network (loaded by
	// GetBot only).
	Addons []BotAddon

	AutoBackupOff bool // scheduled backups disabled for this bot

	// LogoUpdatedMS is when a custom logo was set (0 = none).
	LogoUpdatedMS int64

	// Restart policy. "" behaves as RestartOnFailure with runner defaults.
	RestartPolicy           string
	RestartMaxAttempts      int64 // consecutive crashes before giving up; 0 = unlimited
	RestartBackoffInitialMS int64
	RestartBackoffMaxMS     int64

	// Lifecycle detail written by the runner. RestartCount is the consecutive
	// crash count of the current generation; NextRetryAtMS is set while waiting
	// to retry; StateReason is one of the Reason* codes or nil.
	RestartCount  int64
	NextRetryAtMS *int64
	StateReason   *string
	// LastStartedAtMS is when the bot last became running (nil = never).
	LastStartedAtMS *int64
}

// IsGame reports whether the resource is a game server.
func (b Bot) IsGame() bool { return b.Kind == KindGame }

// PrimaryAllocation returns the game server's primary allocation.
func (b Bot) PrimaryAllocation() (Allocation, bool) {
	for _, a := range b.Allocations {
		if a.Primary {
			return a, true
		}
	}
	return Allocation{}, false
}

// Allocation is an IP:port reservation on a node.
type Allocation struct {
	ID          string
	NodeID      string
	IP          string
	Port        int
	Alias       string
	Notes       string
	BotID       *string
	Primary     bool
	CreatedAtMS int64
}

// Blueprint is a game-server definition; its revisions are immutable.
type Blueprint struct {
	ID              string
	Slug            string
	Name            string
	Category        string
	Description     string
	Source          string // builtin | custom
	CurrentRevision int64
	Enabled         bool
	CreatedAtMS     int64
	UpdatedAtMS     int64
	Servers         int // number of servers using it (list views)
}

// BlueprintRevision is one immutable blueprint version.
type BlueprintRevision struct {
	BlueprintID string
	Revision    int64
	SpecYAML    string
	SHA256      string
	CreatedAtMS int64
}

// Machine-readable explanations for an observed state (bots.state_reason).
const (
	ReasonCleanExit      = "clean_exit"      // exited with code 0; never restarted
	ReasonExitedNoRetry  = "exited"          // crashed under the "never" restart policy
	ReasonGaveUp         = "gave_up"         // crashed more often than the restart budget allows
	ReasonCrashBackoff   = "crash_backoff"   // crashed; waiting before the next restart
	ReasonSetupFailed    = "setup_failed"    // image/build/create/start failed; retrying
	ReasonBuildFailed    = "build_failed"    // the build step failed; retrying
	ReasonRuntimeMissing = "runtime_missing" // the runtime was removed from the catalog
	ReasonCleanupFailed  = "cleanup_failed"  // deletion could not finish; retrying
	ReasonKilled         = "killed"          // stopped with SIGKILL by a user
)

const (
	RestartNever     = "never"
	RestartOnFailure = "on_failure"
)

// Logo is a custom bot or site logo (a small PNG or JPEG).
type Logo struct {
	Data        []byte
	ContentType string
	UpdatedAtMS int64
}

// BotAddon is a companion service (database, cache) that runs next to a bot.
// Kind is also its host name on the bot's private network.
type BotAddon struct {
	BotID       string
	Kind        string
	MemoryBytes int64
	CreatedAtMS int64
	UpdatedAtMS int64
}

// AddonPasswordVar is the system-managed variable holding an add-on's
// generated password (sealed like any other bot variable).
func AddonPasswordVar(kind string) string {
	return "RIVET_ADDON_" + strings.ToUpper(kind) + "_PASSWORD"
}

// EnvVar is an encrypted environment variable row.
type EnvVar struct {
	BotID       string
	Name        string
	Ciphertext  []byte
	Nonce       []byte
	KeyID       string
	CreatedAtMS int64
	UpdatedAtMS int64
}
