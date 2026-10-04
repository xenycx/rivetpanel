package domain

// Passkey is a registered WebAuthn credential. Credential is the library's
// JSON record (public key and counters only; no secret).
type Passkey struct {
	ID           string
	UserID       string
	CredentialID []byte
	Name         string
	Credential   []byte
	CreatedAtMS  int64
	LastUsedAtMS *int64
}
