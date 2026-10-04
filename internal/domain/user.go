package domain

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is a panel account. PasswordHash is never serialized to clients.
type User struct {
	ID           string
	Email        string
	DisplayName  string
	AvatarJPEG   []byte
	PasswordHash string
	Role         string
	Disabled     bool
	CreatedAtMS  int64
	UpdatedAtMS  int64

	// RoleID is the custom role ("" = the built-in role named by Role);
	// RoleName and CustomPermissions are read with it.
	RoleID            string
	RoleName          string
	CustomPermissions []string
	// EmailVerified is false until the address is confirmed through a link
	// (accounts created before verification existed count as verified).
	EmailVerified     bool
	EmailVerifiedAtMS *int64
	// Withheld lists permissions the unverified-email policy currently takes
	// away from this account (empty when verified or the policy is off).
	Withheld []string
	// Client is set when the request was authenticated by an API client
	// rather than a browser session: permissions are then limited to the
	// client's list (intersected with the role), and the account never acts
	// as an administrator.
	Client *ClientScope
}

// IsAdmin reports whether the request acts as a built-in administrator. An
// API client never does, even one created by an administrator.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin && u.Client == nil }

// Session is a signed-in browser. The token itself is never stored; ID is a
// separate public handle for listing and revoking.
type Session struct {
	ID           string
	UserID       string
	CreatedAtMS  int64
	ExpiresAtMS  int64
	LastSeenAtMS int64
	AuthAtMS     int64 // when the user last proved their identity in this session
	Device       string
}
