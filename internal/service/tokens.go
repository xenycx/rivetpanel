package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/auth"
	"github.com/xenycx/rivetpanel/internal/domain"
)

// TokenStore is the persistence automation tokens need.
type TokenStore interface {
	InsertToken(ctx context.Context, t domain.AutomationToken) error
	ListTokens(ctx context.Context, userID string) ([]domain.AutomationToken, error)
	TokenByHash(ctx context.Context, hash []byte, nowMS int64) (domain.AutomationToken, domain.User, error)
	TouchToken(ctx context.Context, id string, nowMS int64) error
	DeleteToken(ctx context.Context, userID, id string) error
}

// Token actions. Each maps to the bot permission it needs; a token never
// grants more than its owner currently holds.
const (
	TokenRead   = "read"   // bot status and operation history
	TokenPower  = "power"  // start, stop, restart
	TokenDeploy = "deploy" // GitHub deployment of the linked branch or a commit
	TokenBackup = "backup" // create backups
)

// TokenActions lists the valid actions in display order.
var TokenActions = []string{TokenRead, TokenPower, TokenDeploy, TokenBackup}

const (
	tokenPrefix    = "bpa_"
	maxTokens      = 20
	maxTokenBots   = 50
	maxTokenLife   = 366 * 24 * time.Hour
	tokenTouchGap  = time.Minute
	idemTTL        = 24 * time.Hour
	idemMaxEntries = 2000
	idemMaxBody    = 8 << 10
)

// TokenService issues and checks scoped automation tokens.
type TokenService struct {
	Store TokenStore
	Bots  *BotService
	Now   func() time.Time

	mu      sync.Mutex
	touched map[string]time.Time
	idem    map[string]*IdemEntry
}

func (s *TokenService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// TokenInput creates a token.
type TokenInput struct {
	Name     string
	Actions  []string
	BotIDs   []string // nil: every bot the owner can access
	LifeDays int
}

// Create issues a token; the plaintext is returned once.
func (s *TokenService) Create(ctx context.Context, actor domain.User, in TokenInput) (string, domain.AutomationToken, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return "", domain.AutomationToken{}, err
	}
	if len(in.Actions) == 0 {
		return "", domain.AutomationToken{}, domain.Invalid("choose at least one action")
	}
	var actions []string
	for _, a := range TokenActions { // canonical order, no duplicates
		if slices.Contains(in.Actions, a) {
			actions = append(actions, a)
		}
	}
	if len(actions) != len(uniq(in.Actions)) {
		return "", domain.AutomationToken{}, domain.Invalid("actions are read, power, deploy and backup")
	}
	life := time.Duration(in.LifeDays) * 24 * time.Hour
	if in.LifeDays < 1 || life > maxTokenLife {
		return "", domain.AutomationToken{}, domain.Invalid("tokens expire after 1 to 366 days")
	}
	var bots []string
	if in.BotIDs != nil {
		bots = uniq(in.BotIDs)
		if len(bots) == 0 || len(bots) > maxTokenBots {
			return "", domain.AutomationToken{}, domain.Invalid("choose 1 to 50 bots, or all of your bots")
		}
		for _, id := range bots {
			if _, err := s.Bots.Authorize(ctx, actor, id, permAny); err != nil {
				return "", domain.AutomationToken{}, domain.Invalid("one of the chosen bots is not available to you")
			}
		}
	}
	existing, err := s.Store.ListTokens(ctx, actor.ID)
	if err != nil {
		return "", domain.AutomationToken{}, err
	}
	if len(existing) >= maxTokens {
		return "", domain.AutomationToken{}, domain.Invalid("you have 20 tokens; revoke one first")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", domain.AutomationToken{}, err
	}
	plain := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	t := domain.AutomationToken{ID: uuid.NewString(), UserID: actor.ID, Name: name, Prefix: plain[:len(tokenPrefix)+4],
		TokenHash: auth.HashToken(plain), Actions: actions, BotIDs: bots, CreatedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(life).UnixMilli()}
	if err := s.Store.InsertToken(ctx, t); err != nil {
		return "", domain.AutomationToken{}, err
	}
	return plain, t, nil
}

func uniq(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// List returns the actor's tokens (never the secret).
func (s *TokenService) List(ctx context.Context, actor domain.User) ([]domain.AutomationToken, error) {
	return s.Store.ListTokens(ctx, actor.ID)
}

// Delete revokes a token immediately.
func (s *TokenService) Delete(ctx context.Context, actor domain.User, id string) error {
	return s.Store.DeleteToken(ctx, actor.ID, id)
}

// Authenticate resolves a bearer token. Revocation, expiry and a disabled
// owner take effect on the next request (nothing is cached).
func (s *TokenService) Authenticate(ctx context.Context, bearer string) (domain.AutomationToken, domain.User, error) {
	if !strings.HasPrefix(bearer, tokenPrefix) || len(bearer) > 128 {
		return domain.AutomationToken{}, domain.User{}, domain.ErrUnauthorized
	}
	now := s.now()
	t, u, err := s.Store.TokenByHash(ctx, auth.HashToken(bearer), now.UnixMilli())
	if err != nil {
		return domain.AutomationToken{}, domain.User{}, domain.ErrUnauthorized
	}
	s.mu.Lock()
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	last := s.touched[t.ID]
	due := now.Sub(last) > tokenTouchGap
	if due {
		if len(s.touched) > 4*maxTokens*50 {
			clear(s.touched)
		}
		s.touched[t.ID] = now
	}
	s.mu.Unlock()
	if due {
		_ = s.Store.TouchToken(ctx, t.ID, now.UnixMilli())
	}
	return t, u, nil
}

// Allows reports whether the token itself permits an action on a bot (the
// owner's permissions are checked separately by the service called).
func Allows(t domain.AutomationToken, action, botID string) bool {
	if !slices.Contains(t.Actions, action) {
		return false
	}
	return t.BotIDs == nil || botID == "" || slices.Contains(t.BotIDs, botID)
}

// IdemEntry is a stored response for an Idempotency-Key.
type IdemEntry struct {
	Done   bool
	Status int
	Body   []byte
	at     time.Time
}

// Idempotent reserves key for a request. It returns the stored entry when the
// key was seen before (Done=false means the first request is still running),
// or nil when the caller should run the request and then call Remember.
func (s *TokenService) Idempotent(key string) *IdemEntry {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idem == nil {
		s.idem = map[string]*IdemEntry{}
	}
	if e, ok := s.idem[key]; ok && now.Sub(e.at) < idemTTL {
		cp := *e
		return &cp
	}
	if len(s.idem) >= idemMaxEntries {
		for k, e := range s.idem {
			if now.Sub(e.at) >= idemTTL || !e.Done {
				continue
			}
			delete(s.idem, k)
			if len(s.idem) < idemMaxEntries*9/10 {
				break
			}
		}
		if len(s.idem) >= idemMaxEntries {
			for k := range s.idem { // everything is fresh: drop arbitrary entries
				delete(s.idem, k)
				if len(s.idem) < idemMaxEntries*9/10 {
					break
				}
			}
		}
	}
	s.idem[key] = &IdemEntry{at: now}
	return nil
}

// Remember stores the response for key; server errors release the key so the
// client can retry.
func (s *TokenService) Remember(key string, status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.idem[key]
	if !ok {
		return
	}
	if status >= 500 || len(body) > idemMaxBody {
		delete(s.idem, key)
		return
	}
	e.Done, e.Status, e.Body = true, status, append([]byte(nil), body...)
}
