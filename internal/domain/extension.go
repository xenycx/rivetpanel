package domain

// Extension states.
const (
	ExtEnabled  = "enabled"
	ExtDisabled = "disabled"
	ExtFailed   = "failed"
)

// Extension sources.
const (
	ExtSourceBuiltin = "builtin"
	ExtSourceUpload  = "upload"
	ExtSourceDev     = "dev"
)

// Extension is an installed panel extension.
type Extension struct {
	ID            string
	Source        string
	ActiveVersion string
	State         string
	Granted       []string // capabilities an administrator approved
	LastError     string
	FailureCount  int
	InstalledBy   string
	CreatedAtMS   int64
	UpdatedAtMS   int64
}

// ExtensionVersion is one stored bundle.
type ExtensionVersion struct {
	ExtensionID  string
	Version      string
	Source       string
	Bundle       []byte // omitted by list queries
	SHA256       string
	ManifestJSON string
	KeyID        string
	UploadedBy   string
	CreatedAtMS  int64
}

// ExtensionPublisher is a trusted signing key.
type ExtensionPublisher struct {
	KeyID       string
	Name        string
	PublicKey   string // "ed25519:<base64>"
	CreatedBy   string
	CreatedAtMS int64
	RevokedAtMS *int64
}

// ExtensionSetting is one stored setting; secrets are sealed.
type ExtensionSetting struct {
	Key          string
	ValueJSON    string
	SecretCipher []byte
	SecretNonce  []byte
	SecretKeyID  string
}

// ExtensionJob is scheduled-job bookkeeping.
type ExtensionJob struct {
	ExtensionID string
	Job         string
	NextRunMS   int64
	LastRunMS   int64
	LastStatus  string
	LastError   string
}
