package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

const (
	apiKeyPrefix    = "bpk_"
	maxAPIKeys      = 20
	maxAPIKeyLife   = 3650 * 24 * time.Hour
	apiKeyScopeSFTP = "sftp"
)

// CreateAPIKey issues a key for SFTP. The plaintext is returned once; only its
// SHA-256 is stored, so it cannot be shown again.
func (s *AuthService) CreateAPIKey(ctx context.Context, userID, name string, ttl time.Duration) (plaintext string, k domain.APIKey, err error) {
	if name, err = validateName(name); err != nil {
		return "", k, err
	}
	if ttl < 0 || ttl > maxAPIKeyLife {
		return "", k, domain.Invalid("expiry is out of range")
	}
	existing, err := s.Store.ListAPIKeys(ctx, userID)
	if err != nil {
		return "", k, err
	}
	if len(existing) >= maxAPIKeys {
		return "", k, domain.Invalid("too many API keys; delete one first")
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", k, err
	}
	plaintext = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	k = domain.APIKey{ID: uuid.NewString(), UserID: userID, Name: name, Prefix: plaintext[:len(apiKeyPrefix)+4],
		TokenHash: auth.HashToken(plaintext), Scope: apiKeyScopeSFTP, CreatedAtMS: now.UnixMilli()}
	if ttl > 0 {
		e := now.Add(ttl).UnixMilli()
		k.ExpiresAtMS = &e
	}
	return plaintext, k, s.Store.InsertAPIKey(ctx, k)
}

func (s *AuthService) ListAPIKeys(ctx context.Context, userID string) ([]domain.APIKey, error) {
	return s.Store.ListAPIKeys(ctx, userID)
}

func (s *AuthService) DeleteAPIKey(ctx context.Context, userID, id string) error {
	return s.Store.DeleteAPIKey(ctx, userID, id)
}

// AuthenticateSFTP checks the credentials an SFTP client presents: the panel
// email plus either the account password or an API key. The key must belong to
// the named account.
func (s *AuthService) AuthenticateSFTP(ctx context.Context, email, secret string) (domain.User, error) {
	if !strings.HasPrefix(secret, apiKeyPrefix) {
		u, err := s.checkPassword(ctx, email, secret)
		if err != nil {
			return u, err
		}
		// Two-step accounts would otherwise bypass the second step: they use
		// separately revocable SFTP keys instead of the account password.
		if on, err := s.Store.MFAEnabled(ctx, u.ID); err != nil || on {
			return domain.User{}, domain.ErrUnauthorized
		}
		return u, nil
	}
	norm, err := NormalizeEmail(email)
	if err != nil || len(secret) > 128 {
		return domain.User{}, domain.ErrUnauthorized
	}
	u, err := s.Store.GetUserByAPIKey(ctx, auth.HashToken(secret), apiKeyScopeSFTP, s.now().UnixMilli())
	if err != nil || u.Email != norm {
		return domain.User{}, domain.ErrUnauthorized
	}
	return u, nil
}

// SFTPCheck re-validates an SFTP login: the account still exists and is
// enabled, and the credential it used is still valid (the key was not deleted
// or expired; the password was not changed). It returns the current user.
type SFTPCheck = func(ctx context.Context) (domain.User, error)

// LoginSFTP authenticates like AuthenticateSFTP and also returns a check that
// the SFTP server runs periodically. The check keeps only a hash of the key or
// the account's password hash, never the secret itself, and never re-runs
// Argon2.
func (s *AuthService) LoginSFTP(ctx context.Context, email, secret string) (domain.User, SFTPCheck, error) {
	u, err := s.AuthenticateSFTP(ctx, email, secret)
	if err != nil {
		return domain.User{}, nil, err
	}
	var keyHash []byte
	if strings.HasPrefix(secret, apiKeyPrefix) {
		keyHash = auth.HashToken(secret)
	}
	pwHash, userID := u.PasswordHash, u.ID
	check := func(ctx context.Context) (domain.User, error) {
		cur, err := s.Store.GetUserByID(ctx, userID)
		if err != nil || cur.Disabled {
			return domain.User{}, domain.ErrUnauthorized
		}
		if keyHash != nil {
			ok, err := s.Store.APIKeyValid(ctx, keyHash, userID, apiKeyScopeSFTP, s.now().UnixMilli())
			if err != nil || !ok {
				return domain.User{}, domain.ErrUnauthorized
			}
		} else if cur.PasswordHash == "" || cur.PasswordHash != pwHash {
			return domain.User{}, domain.ErrUnauthorized
		} else if on, err := s.Store.MFAEnabled(ctx, userID); err != nil || on {
			return domain.User{}, domain.ErrUnauthorized // two-step turned on after this login
		}
		return cur, nil
	}
	return u, check, nil
}
