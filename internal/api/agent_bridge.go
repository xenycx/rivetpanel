package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// agentTokenPrefix marks a bearer token minted by AgentBridge. The token
// lives in memory for the length of one in-process request; it is never
// written anywhere or sent over a network.
const agentTokenPrefix = "rva_"

// AgentBridge lets the AI assistant call the panel's own HTTP API as the
// person it works for. Every call runs the real route: the same
// authentication result, role permissions, per-resource checks, validation
// and audit as a click in the browser. It only skips the session cookie and
// CSRF token, because the request never leaves the process.
type AgentBridge struct {
	mu     sync.RWMutex
	app    *fiber.App
	tokens sync.Map // token -> domain.User
}

// NewAgentBridge returns a bridge; api.New attaches it to the app.
func NewAgentBridge() *AgentBridge { return &AgentBridge{} }

func (b *AgentBridge) attach(app *fiber.App) {
	b.mu.Lock()
	b.app = app
	b.mu.Unlock()
}

// user resolves an in-process agent token.
func (b *AgentBridge) user(token string) (domain.User, bool) {
	if b == nil || !strings.HasPrefix(token, agentTokenPrefix) {
		return domain.User{}, false
	}
	v, ok := b.tokens.Load(token)
	if !ok {
		return domain.User{}, false
	}
	return v.(domain.User), true
}

// agentDenied are areas the assistant never reaches, whatever the person
// could do themselves: signing in and out, credentials, sessions, sign-in
// methods, account deletion, and the assistant's own approvals (it must not
// approve its own actions or start runs from inside a run).
var agentDenied = []string{
	"/auth", "/me/password", "/me/mfa", "/me/tokens", "/me/api-keys", "/me/api-clients",
	"/me/sessions", "/me/connections", "/me/email", "/me/passkeys", "/me/identities", "/me/delete",
	"/me/account", "/admin/api-clients", "/ai", "/invites/accept", "/setup",
}

// agentStreaming are routes that hold the connection open (WebSockets,
// event streams) and would never answer an in-process request.
var agentStreaming = []string{"/stream", "/terminal", "/console", "/events", "/ws", "/live"}

// AgentPathAllowed reports whether the assistant may call path (with or
// without the /api/v1 prefix) and why not.
func AgentPathAllowed(path string) (string, error) {
	p, _, _ := strings.Cut(path, "?")
	p = "/" + strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(p), "/api/v1"), "/")
	if strings.Contains(p, "..") || strings.ContainsAny(p, " \t\r\n\\") {
		return "", domain.Invalid("the path is not valid")
	}
	for _, d := range agentDenied {
		if p == d || strings.HasPrefix(p, d+"/") {
			return "", domain.Invalid("the assistant cannot use " + d + ": credentials, sessions and the assistant's own approvals stay with the person")
		}
	}
	for _, s := range agentStreaming {
		if strings.HasSuffix(p, s) || strings.Contains(p, s+"/") {
			return "", domain.Invalid("streaming routes (consoles, terminals, live events) cannot be called; use the logs tools instead")
		}
	}
	return p, nil
}

// Do runs one API request as actor and returns the status and body.
// pathAndQuery is relative to /api/v1 (a leading /api/v1 is accepted).
func (b *AgentBridge) Do(ctx context.Context, actor domain.User, method, pathAndQuery string, body []byte) (int, []byte, error) {
	b.mu.RLock()
	app := b.app
	b.mu.RUnlock()
	if app == nil {
		return 0, nil, errors.New("the panel API is not available to the assistant")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case fiber.MethodGet, fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
	default:
		return 0, nil, domain.Invalid("method must be GET, POST, PUT, PATCH or DELETE")
	}
	p, err := AgentPathAllowed(pathAndQuery)
	if err != nil {
		return 0, nil, err
	}
	if _, q, ok := strings.Cut(pathAndQuery, "?"); ok {
		p += "?" + q
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, nil, err
	}
	token := agentTokenPrefix + hex.EncodeToString(raw[:])
	b.tokens.Store(token, actor)
	defer b.tokens.Delete(token)

	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequestWithContext(ctx, method, "/api/v1"+p, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "RivetPanel assistant")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	timeout := 2 * time.Minute
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = left
		}
	}
	if timeout <= 0 {
		return 0, nil, context.DeadlineExceeded
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: timeout, FailOnTimeout: true})
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, out, nil
}

// Routes lists the API routes the assistant may call, as "METHOD /path".
func (b *AgentBridge) Routes() []string {
	b.mu.RLock()
	app := b.app
	b.mu.RUnlock()
	if app == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range app.GetRoutes(true) {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		p := strings.TrimPrefix(r.Path, "/api/v1")
		if strings.HasPrefix(p, "/automation") || strings.HasPrefix(p, "/webhooks") {
			continue
		}
		if _, err := AgentPathAllowed(p); err != nil {
			continue
		}
		k := r.Method + " " + p
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i][strings.IndexByte(out[i], ' ')+1:], out[j][strings.IndexByte(out[j], ' ')+1:]
		if pi != pj {
			return pi < pj
		}
		return out[i] < out[j]
	})
	return out
}
