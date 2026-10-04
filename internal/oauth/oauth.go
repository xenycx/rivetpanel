// Package oauth implements the GitHub and Discord authorization-code flows
// with PKCE. It is deliberately small (net/http only) to keep the footprint low.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Identity is the verified profile of an external account.
type Identity struct {
	ID        string
	Username  string
	Email     string // only ever a provider-verified address
	AvatarURL string
}

// Token is the outcome of a code exchange.
type Token struct {
	AccessToken string
	Scope       string
	WebhookURL  string // Discord webhook.incoming grant, else empty
}

// AuthParams describes one authorization request.
type AuthParams struct {
	RedirectURI string
	State       string
	Challenge   string // PKCE S256 challenge
	Notify      bool   // also request the Discord webhook grant
	RepoAccess  bool   // GitHub: also request the repo scope (private repositories, webhooks)
}

// Provider is one external identity provider.
type Provider interface {
	Name() string
	AuthURL(p AuthParams) string
	Exchange(ctx context.Context, redirectURI, code, verifier string) (Token, error)
	Identity(ctx context.Context, t Token) (Identity, error)
}

const maxBody = 1 << 20

var defaultHTTP = &http.Client{Timeout: 10 * time.Second}

// RandString returns n random bytes, base64url encoded (state, nonce).
func RandString(n int) string { return randString(n) }

// NewPKCE returns a verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	verifier = randString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS entropy source failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func doJSON(hc *http.Client, req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: status %d", req.Method, req.URL.Host, res.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func postForm(ctx context.Context, hc *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doJSON(hc, req, out)
}

func getJSON(ctx context.Context, hc *http.Client, endpoint, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	return doJSON(hc, req, out)
}

// ---- GitHub ----

// GitHub implements Provider for github.com OAuth Apps.
type GitHub struct {
	ClientID, Secret string
	// Overridable for tests and GitHub Enterprise Server.
	AuthBase, TokenURL, APIBase string
	HTTP                        *http.Client
}

func (g *GitHub) Name() string { return "github" }
func (g *GitHub) hc() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return defaultHTTP
}
func def(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func githubScope(repo bool) string {
	if repo {
		return "read:user user:email repo"
	}
	return "read:user user:email"
}

func (g *GitHub) AuthURL(p AuthParams) string {
	q := url.Values{
		"client_id": {g.ClientID}, "redirect_uri": {p.RedirectURI}, "state": {p.State},
		"scope": {githubScope(p.RepoAccess)}, "code_challenge": {p.Challenge}, "code_challenge_method": {"S256"},
		"allow_signup": {"true"},
	}
	return def(g.AuthBase, "https://github.com/login/oauth/authorize") + "?" + q.Encode()
}

func (g *GitHub) Exchange(ctx context.Context, redirectURI, code, verifier string) (Token, error) {
	var r struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	err := postForm(ctx, g.hc(), def(g.TokenURL, "https://github.com/login/oauth/access_token"), url.Values{
		"client_id": {g.ClientID}, "client_secret": {g.Secret}, "code": {code},
		"redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}, &r)
	if err != nil {
		return Token{}, err
	}
	// GitHub reports failures with HTTP 200 and an "error" field.
	if r.Error != "" || r.AccessToken == "" {
		return Token{}, fmt.Errorf("github token exchange rejected: %s", def(r.Error, "no access token"))
	}
	return Token{AccessToken: r.AccessToken, Scope: strings.ReplaceAll(r.Scope, ",", " ")}, nil
}

func (g *GitHub) Identity(ctx context.Context, t Token) (Identity, error) {
	api := def(g.APIBase, "https://api.github.com")
	var u struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getJSON(ctx, g.hc(), api+"/user", t.AccessToken, &u); err != nil {
		return Identity{}, err
	}
	if u.ID == 0 || u.Login == "" {
		return Identity{}, errors.New("github profile is missing id or login")
	}
	id := Identity{ID: fmt.Sprint(u.ID), Username: u.Login, AvatarURL: u.AvatarURL}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	// A missing email is not fatal: sign-in by an existing link still works.
	if err := getJSON(ctx, g.hc(), api+"/user/emails", t.AccessToken, &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				id.Email = e.Email
			}
		}
	}
	return id, nil
}

// ---- Discord ----

// Discord implements Provider for Discord OAuth2.
type Discord struct {
	ClientID, Secret            string
	AuthBase, TokenURL, APIBase string
	HTTP                        *http.Client
}

func (d *Discord) Name() string { return "discord" }
func (d *Discord) hc() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return defaultHTTP
}

func (d *Discord) AuthURL(p AuthParams) string {
	scope := "identify email"
	if p.Notify {
		scope += " webhook.incoming"
	}
	q := url.Values{
		"client_id": {d.ClientID}, "redirect_uri": {p.RedirectURI}, "state": {p.State}, "response_type": {"code"},
		"scope": {scope}, "code_challenge": {p.Challenge}, "code_challenge_method": {"S256"}, "prompt": {"consent"},
	}
	return def(d.AuthBase, "https://discord.com/oauth2/authorize") + "?" + q.Encode()
}

func (d *Discord) Exchange(ctx context.Context, redirectURI, code, verifier string) (Token, error) {
	var r struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Webhook     *struct {
			URL string `json:"url"`
		} `json:"webhook"`
	}
	err := postForm(ctx, d.hc(), def(d.TokenURL, "https://discord.com/api/oauth2/token"), url.Values{
		"client_id": {d.ClientID}, "client_secret": {d.Secret}, "grant_type": {"authorization_code"},
		"code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}, &r)
	if err != nil {
		return Token{}, err
	}
	if r.AccessToken == "" {
		return Token{}, errors.New("discord token exchange returned no access token")
	}
	t := Token{AccessToken: r.AccessToken, Scope: r.Scope}
	if r.Webhook != nil && strings.HasPrefix(r.Webhook.URL, "https://") {
		t.WebhookURL = r.Webhook.URL
	}
	return t, nil
}

func (d *Discord) Identity(ctx context.Context, t Token) (Identity, error) {
	var u struct {
		ID         string  `json:"id"`
		Username   string  `json:"username"`
		GlobalName *string `json:"global_name"`
		Avatar     *string `json:"avatar"`
		Email      *string `json:"email"`
		Verified   bool    `json:"verified"`
	}
	if err := getJSON(ctx, d.hc(), def(d.APIBase, "https://discord.com/api")+"/users/@me", t.AccessToken, &u); err != nil {
		return Identity{}, err
	}
	if u.ID == "" || u.Username == "" {
		return Identity{}, errors.New("discord profile is missing id or username")
	}
	id := Identity{ID: u.ID, Username: u.Username}
	if u.GlobalName != nil && *u.GlobalName != "" {
		id.Username = *u.GlobalName
	}
	if u.Avatar != nil && *u.Avatar != "" {
		id.AvatarURL = "https://cdn.discordapp.com/avatars/" + u.ID + "/" + *u.Avatar + ".png"
	}
	if u.Email != nil && u.Verified {
		id.Email = *u.Email
	}
	return id, nil
}

// ---- pending authorization state ----

// Pending is one in-flight authorization, kept server-side so nothing
// security-relevant round-trips through the browser except two random values.
type Pending struct {
	Provider   string
	Verifier   string
	UserID     string // non-empty: linking to this signed-in user
	Notify     bool
	RepoAccess bool
	Nonce      string // OpenID Connect: must come back in the ID token
	Binder     string // must match the browser's state cookie
	Expires    time.Time
}

// StateStore holds pending authorizations in memory. It is bounded; if it is
// full the oldest entries are dropped, which only aborts abandoned flows.
type StateStore struct {
	mu  sync.Mutex
	m   map[string]Pending
	max int
	ttl time.Duration
	now func() time.Time
}

func NewStateStore() *StateStore {
	return &StateStore{m: map[string]Pending{}, max: 1000, ttl: 10 * time.Minute, now: time.Now}
}

// Put registers a flow and returns the state parameter and the cookie binder.
func (s *StateStore) Put(p Pending) (state, binder string) {
	state, binder = randString(24), randString(24)
	p.Binder, p.Expires = binder, s.now().Add(s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.m {
		if now.After(v.Expires) {
			delete(s.m, k)
		}
	}
	for len(s.m) >= s.max { // still full: evict the soonest to expire
		var oldest string
		var at time.Time
		for k, v := range s.m {
			if oldest == "" || v.Expires.Before(at) {
				oldest, at = k, v.Expires
			}
		}
		delete(s.m, oldest)
	}
	s.m[state] = p
	return state, binder
}

// Take consumes a flow (single use). It fails if the state is unknown, expired,
// for another provider, or the browser's binder does not match.
func (s *StateStore) Take(state, binder, provider string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[state]
	if !ok {
		return Pending{}, false
	}
	if s.now().After(p.Expires) {
		delete(s.m, state)
		return Pending{}, false
	}
	// A mismatch must not consume the flow, or anyone who saw the state in a
	// URL could cancel the victim's sign-in.
	if p.Provider != provider || !constEq(p.Binder, binder) {
		return Pending{}, false
	}
	delete(s.m, state)
	return p, true
}

func constEq(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
