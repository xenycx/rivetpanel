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

// APIClientStore is the persistence API clients need.
type APIClientStore interface {
	InsertAPIClient(ctx context.Context, c domain.APIClient) error
	ListAPIClients(ctx context.Context, userID string) ([]domain.APIClient, error)
	GetAPIClient(ctx context.Context, id string) (domain.APIClient, error)
	APIClientByHash(ctx context.Context, hash []byte, nowMS int64) (domain.APIClient, domain.User, error)
	TouchAPIClient(ctx context.Context, id string, nowMS int64) error
	DeleteAPIClient(ctx context.Context, userID, id string) error
	GetUserByID(ctx context.Context, id string) (domain.User, error)
}

const (
	// APIClientPrefix starts every API client token.
	APIClientPrefix     = "rvc_"
	maxAPIClients       = 20
	maxAPIClientRes     = 50
	maxAPIClientLife    = 366 * 24 * time.Hour
	apiClientTouchGap   = time.Minute
	apiClientTokenBytes = 32
)

// APIClientService issues, lists, revokes and authenticates API clients.
type APIClientService struct {
	Store APIClientStore
	Bots  *BotService
	Now   func() time.Time

	mu      sync.Mutex
	touched map[string]time.Time
}

func (s *APIClientService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// APIClientInput creates a client.
type APIClientInput struct {
	Name         string
	Permissions  []string
	BotIDs       []string // nil: not limited to bots
	WorkspaceIDs []string // nil: not limited to workspaces
	LifeDays     int      // 0: never expires
}

// Create issues a client for actor, which must be a browser session (an API
// client cannot mint further clients). The permissions must be ones the actor
// holds right now (the same no-escalation rule as granting roles), and every
// listed bot and workspace must be reachable by the actor. The plaintext
// token is returned once.
func (s *APIClientService) Create(ctx context.Context, actor domain.User, in APIClientInput) (string, domain.APIClient, error) {
	if actor.Client != nil {
		return "", domain.APIClient{}, domain.ErrForbidden
	}
	if !actor.Can(domain.PermAPIKeys) {
		return "", domain.APIClient{}, domain.Denied(actor, domain.PermAPIKeys)
	}
	name, err := validateName(in.Name)
	if err != nil {
		return "", domain.APIClient{}, err
	}
	var perms []string
	for _, p := range in.Permissions {
		if !domain.ValidPermission(p) {
			return "", domain.APIClient{}, domain.Invalid("unknown permission " + strings.ToValidUTF8(p, "?"))
		}
		if !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	if len(perms) == 0 {
		return "", domain.APIClient{}, domain.Invalid("choose at least one permission")
	}
	for _, p := range perms {
		if !actor.Can(p) {
			return "", domain.APIClient{}, domain.Invalid("you cannot give a client " + p + ": you do not hold it yourself")
		}
	}
	slices.SortFunc(perms, func(a, b string) int { return catalogIndex(a) - catalogIndex(b) })
	if in.LifeDays < 0 || time.Duration(in.LifeDays)*24*time.Hour > maxAPIClientLife {
		return "", domain.APIClient{}, domain.Invalid("clients expire after 1 to 366 days, or never")
	}
	var bots, wss []string
	if in.BotIDs != nil {
		bots = uniq(in.BotIDs)
		for _, id := range bots {
			if _, err := s.Bots.Authorize(ctx, actor, id, permAny); err != nil {
				return "", domain.APIClient{}, domain.Invalid("one of the chosen bots is not available to you")
			}
		}
	}
	if in.WorkspaceIDs != nil {
		wss = uniq(in.WorkspaceIDs)
		for _, id := range wss {
			if _, err := s.Bots.GetWorkspace(ctx, actor, id); err != nil {
				return "", domain.APIClient{}, domain.Invalid("one of the chosen workspaces is not available to you")
			}
		}
	}
	if (bots != nil || wss != nil) && len(bots)+len(wss) == 0 {
		return "", domain.APIClient{}, domain.Invalid("choose at least one bot or workspace, or do not limit the client")
	}
	if len(bots)+len(wss) > maxAPIClientRes {
		return "", domain.APIClient{}, domain.Invalid("a client can be limited to at most 50 bots and workspaces")
	}
	existing, err := s.Store.ListAPIClients(ctx, actor.ID)
	if err != nil {
		return "", domain.APIClient{}, err
	}
	if len(existing) >= maxAPIClients {
		return "", domain.APIClient{}, domain.Invalid("you have 20 API clients; revoke one first")
	}
	raw := make([]byte, apiClientTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", domain.APIClient{}, err
	}
	plain := APIClientPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	c := domain.APIClient{ID: uuid.NewString(), UserID: actor.ID, Name: name, Prefix: plain[:len(APIClientPrefix)+4],
		TokenHash: auth.HashToken(plain), Permissions: perms, BotIDs: bots, WorkspaceIDs: wss, CreatedAtMS: now.UnixMilli(),
		OwnerEmail: actor.Email}
	if in.LifeDays > 0 {
		exp := now.Add(time.Duration(in.LifeDays) * 24 * time.Hour).UnixMilli()
		c.ExpiresAtMS = &exp
	}
	if err := s.Store.InsertAPIClient(ctx, c); err != nil {
		return "", domain.APIClient{}, err
	}
	return plain, c, nil
}

func catalogIndex(p string) int {
	for i, x := range domain.Permissions {
		if x.Name == p {
			return i
		}
	}
	return len(domain.Permissions)
}

// List returns the actor's own clients (never the secret).
func (s *APIClientService) List(ctx context.Context, actor domain.User) ([]domain.APIClient, error) {
	return s.Store.ListAPIClients(ctx, actor.ID)
}

// Revoke deletes one of the actor's own clients immediately.
func (s *APIClientService) Revoke(ctx context.Context, actor domain.User, id string) (domain.APIClient, error) {
	c, err := s.Store.GetAPIClient(ctx, id)
	if err != nil || c.UserID != actor.ID {
		return domain.APIClient{}, domain.ErrNotFound
	}
	return c, s.Store.DeleteAPIClient(ctx, actor.ID, id)
}

// ListAll returns every account's clients to account viewers.
func (s *APIClientService) ListAll(ctx context.Context, actor domain.User) ([]domain.APIClient, error) {
	if !actor.Can(domain.PermUsersView) {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListAPIClients(ctx, "")
}

// AdminRevoke lets an account manager revoke another account's client. The
// same rule as other account changes applies: a delegated manager cannot act
// on an account it does not outrank (see manageable).
func (s *APIClientService) AdminRevoke(ctx context.Context, actor domain.User, id string) (domain.APIClient, error) {
	if !actor.Can(domain.PermUsersManage) {
		return domain.APIClient{}, domain.Denied(actor, domain.PermUsersManage)
	}
	c, err := s.Store.GetAPIClient(ctx, id)
	if err != nil {
		return domain.APIClient{}, err
	}
	if c.UserID != actor.ID {
		owner, err := s.Store.GetUserByID(ctx, c.UserID)
		if err != nil {
			return domain.APIClient{}, err
		}
		if err := manageable(actor, owner); err != nil {
			return domain.APIClient{}, err
		}
	}
	return c, s.Store.DeleteAPIClient(ctx, "", id)
}

// Authenticate resolves a bearer token to the creator's account limited to
// the client. Revocation, expiry, a disabled creator and role changes take
// effect on the next request (nothing is cached).
func (s *APIClientService) Authenticate(ctx context.Context, bearer string) (domain.User, error) {
	if !strings.HasPrefix(bearer, APIClientPrefix) || len(bearer) > 128 {
		return domain.User{}, domain.ErrUnauthorized
	}
	now := s.now()
	c, u, err := s.Store.APIClientByHash(ctx, auth.HashToken(bearer), now.UnixMilli())
	if err != nil {
		return domain.User{}, domain.ErrUnauthorized
	}
	s.mu.Lock()
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	due := now.Sub(s.touched[c.ID]) > apiClientTouchGap
	if due {
		if len(s.touched) > 10000 {
			clear(s.touched)
		}
		s.touched[c.ID] = now
	}
	s.mu.Unlock()
	if due {
		_ = s.Store.TouchAPIClient(ctx, c.ID, now.UnixMilli())
	}
	u.Client = &domain.ClientScope{ID: c.ID, Name: c.Name, Permissions: c.Permissions, BotIDs: c.BotIDs, WorkspaceIDs: c.WorkspaceIDs}
	return u, nil
}
