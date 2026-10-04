package domain

// OIDCProvider is an administrator-configured OpenID Connect sign-in
// provider. The client secret is sealed (Secret*); nil for a public client.
type OIDCProvider struct {
	ID            string
	Slug          string // in the sign-in and callback URLs
	Name          string // shown on the sign-in button
	Issuer        string
	ClientID      string
	SecretCipher  []byte
	SecretNonce   []byte
	SecretKeyID   *string
	Scopes        string // space separated, always including openid
	Enabled       bool
	AllowSignup   bool   // create an account on first sign-in
	LinkByEmail   bool   // link to an existing account whose address the provider verified
	DefaultRoleID string // custom role for new accounts; "" = the built-in User role
	CreatedAtMS   int64
	UpdatedAtMS   int64
}

// HasSecret reports whether a client secret is stored.
func (p OIDCProvider) HasSecret() bool { return p.SecretKeyID != nil }

// OIDCIdentity links an issuer subject to an account.
type OIDCIdentity struct {
	ProviderID    string
	Subject       string
	UserID        string
	Email         string
	EmailVerified bool
	CreatedAtMS   int64
	LastLoginAtMS *int64

	ProviderSlug, ProviderName string // listing only
}
