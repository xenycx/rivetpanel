package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// NewToken returns a random opaque session token (sent to the client) and its
// SHA-256 hash (the only form persisted).
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken returns the SHA-256 of a token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CSRFToken derives the per-session CSRF token from the session token. The
// session cookie is HttpOnly, so scripts on other origins cannot compute it.
func CSRFToken(sessionToken string) string {
	h := sha256.Sum256([]byte("rivetpanel-csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// CSRFValid compares a presented token against the session's in constant time.
func CSRFValid(sessionToken, presented string) bool {
	return subtle.ConstantTimeCompare([]byte(CSRFToken(sessionToken)), []byte(presented)) == 1
}
