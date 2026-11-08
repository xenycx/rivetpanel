package domain

// Sub-user permission bits (bot_subusers.permissions).
const (
	PermViewConsole = 1 << iota
	PermPower
	PermEditFiles
	PermManageEnv
	PermFullAdmin

	PermAll = PermViewConsole | PermPower | PermEditFiles | PermManageEnv | PermFullAdmin

	// PermConfigure is a bot's startup configuration (runtime, start command,
	// entrypoint, build command, auto-restart). Shares and invitations may
	// grant it on its own; full access implies it, and MaskBotPerms keeps it
	// for a role that trims full access but holds bots.configure. It is not
	// part of PermAll, so roles that predate it keep their full access.
	PermConfigure = PermFullAdmin << 1

	// PermGrantable is every bit a share or invitation may store.
	PermGrantable = PermAll | PermConfigure
)

// Has reports whether the mask grants perm; full admin grants everything.
func HasPerm(mask, perm int) bool {
	return mask&PermFullAdmin != 0 || mask&perm == perm
}

const (
	ProviderGitHub  = "github"
	ProviderDiscord = "discord"
)

// ErrLastAuthMethod rejects unlinking the only remaining way to sign in.
var ErrLastAuthMethod = Invalid("cannot disconnect the only remaining sign-in method")

// OAuthAccount links a panel user to an external identity. Token* hold a
// sealed provider access token (nil when none is stored).
type OAuthAccount struct {
	Provider       string
	ProviderUserID string
	UserID         string
	Username       string
	Email          *string
	AvatarURL      *string
	Scopes         string
	TokenCipher    []byte
	TokenNonce     []byte
	TokenKeyID     *string
	NotifyEnabled  bool
	WebhookCipher  []byte // sealed Discord webhook URL, nil when not granted
	WebhookNonce   []byte
	WebhookKeyID   *string
	CreatedAtMS    int64
	UpdatedAtMS    int64
}

// APIKey authenticates non-browser clients (SFTP). Only the hash is stored.
type APIKey struct {
	ID           string
	UserID       string
	Name         string
	Prefix       string
	TokenHash    []byte
	Scope        string
	CreatedAtMS  int64
	LastUsedAtMS *int64
	ExpiresAtMS  *int64
}

type SubUser struct {
	BotID       string
	UserID      string
	Email       string
	Permissions int
	InvitedBy   *string
	CreatedAtMS int64
	UpdatedAtMS int64
}

type GitHubRepo struct {
	BotID          string
	TokenUserID    string
	FullName       string
	Branch         string
	RootDir        string
	Private        bool
	AutoDeploy     bool
	SecretCipher   []byte
	SecretNonce    []byte
	SecretKeyID    string
	LastSHA        *string
	LastDeployedMS *int64
	LastError      *string
	HookID         *int64
	CreatedAtMS    int64
	UpdatedAtMS    int64
}

type Backup struct {
	ID          string
	BotID       string
	Kind        string // manual | auto | pre_restore
	Status      string // creating | ready | failed
	FileName    string
	SizeBytes   int64
	SHA256Hex   *string
	IncludesEnv bool
	Error       *string
	CreatedBy   *string
	CreatedAtMS int64

	Label        *string
	VerifiedAtMS *int64  // last successful or failed verification
	VerifyError  *string // nil when the last verification passed
	Consistent   bool    // written while the bot was stopped
}

type BotTelemetryLog struct {
	ID           int64
	BotID        string
	RecordedAtMS int64
	Kind         string // stat | command | event
	Name         string
	Value        *float64
	PayloadJSON  *string
}

type BotPort struct {
	BotID         string
	ContainerPort int
	HostPort      int
	Protocol      string
	HostIP        string
	CreatedAtMS   int64
}

// ResourceSample is one container resource reading (Docker stats).
type ResourceSample struct {
	CPUCores      float64 `json:"cpu_cores"` // CPUs in use (1.0 = one full core)
	MemUsedBytes  int64   `json:"mem_used_bytes"`
	MemLimitBytes int64   `json:"mem_limit_bytes"`
	PIDs          int64   `json:"pids"`
	NetRxBytes    int64   `json:"net_rx_bytes"`
	NetTxBytes    int64   `json:"net_tx_bytes"`
}
