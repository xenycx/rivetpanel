package service

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/runner"
)

// A route a signed-in person calls acts as that person: it may never
// resolve another panel user through identity.discord.
func TestDiscordIdentityOnlyWithoutAPerson(t *testing.T) {
	inv := &invocation{user: &domain.User{ID: "u1"}}
	out := string(inv.discordUser(context.Background(), []byte(`{"discord_user_id":"123456789012345678"}`)))
	if !strings.Contains(out, "denied") || strings.Contains(out, `"as"`) {
		t.Fatalf("identity.discord answered a person's request: %s", out)
	}
}

// Redirects are held to the first request's rules.
func TestFetchRedirectRules(t *testing.T) {
	c := (&ExtensionService{}).client([]string{"discord.com"})
	for raw, ok := range map[string]bool{
		"https://discord.com/api":         true,
		"https://discord.com:443/api":     true,
		"https://discord.com:8443/api":    false,
		"http://discord.com/api":          false,
		"https://user:pw@discord.com/api": false,
		"https://evil.example/api":        false,
	} {
		u, _ := url.Parse(raw)
		req := &http.Request{URL: u, Header: http.Header{"Authorization": {"Bot x"}}}
		err := c.CheckRedirect(req, []*http.Request{{}})
		if (err == nil) != ok {
			t.Errorf("%s: allowed=%v, want %v", raw, err == nil, ok)
		}
		if err == nil && req.Header.Get("Authorization") != "" {
			t.Errorf("%s: credentials followed the redirect", raw)
		}
	}
}

// What a person confirms is what runs: fields another database kind would
// read are dropped, and the dialog text comes from the same request.
func TestAddonWriteShowsWhatRuns(t *testing.T) {
	s := &ExtensionService{confirms: map[string]pendingConfirm{}}
	q := runner.AddonQuery{Mode: "write", Query: "DROP TABLE users", Args: []string{"SELECT", "1"}, Op: "find"}
	s.confirms["t"] = pendingConfirm{user: "u1", bot: "b1", kind: "postgres", q: q.ForKind("postgres"), expires: time.Now().Add(time.Minute)}
	p, err := s.PendingAddonWrite(domain.User{ID: "u1"}, "t")
	if err != nil || p["query"] != "DROP TABLE users" {
		t.Fatalf("dialog shows %q (%v), runs %q", p["query"], err, q.Query)
	}
	if got := q.ForKind("postgres"); len(got.Args) != 0 || got.Op != "" {
		t.Fatalf("unused fields kept: %+v", got)
	}
	s.confirms["long"] = pendingConfirm{user: "u1", kind: "postgres", q: runner.AddonQuery{Query: strings.Repeat("x", 2001)}, expires: time.Now().Add(time.Minute)}
	if _, err := s.PendingAddonWrite(domain.User{ID: "u1"}, "long"); err == nil {
		t.Fatal("a change too long to show in full was offered for confirmation")
	}
}

// A verified public request is accepted once.
func TestPublicSignatureOnce(t *testing.T) {
	s := &ExtensionService{}
	if err := s.once("ext", "sig", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.once("ext", "sig", time.Minute); err == nil {
		t.Fatal("replay accepted")
	}
	if err := s.once("other", "sig", time.Minute); err != nil {
		t.Fatal("signatures are per extension")
	}
}
