//go:build !wasip1

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/extensions/sdk"
)

type fakeHost struct {
	kv       map[string][]byte
	me       map[string]any // reply to users/@me
	app      map[string]any
	identity []map[string]string
	notified []map[string]string
	discordN int
}

func (f *fakeHost) host(name string, req json.RawMessage) (any, *sdk.HostError) {
	var m map[string]any
	_ = json.Unmarshal(req, &m)
	switch name {
	case "kv.get":
		v, ok := f.kv[m["scope"].(string)+"/"+m["key"].(string)]
		return map[string]any{"found": ok, "value": v}, nil
	case "kv.set":
		var r struct {
			Scope, Key string
			Value      []byte
		}
		_ = json.Unmarshal(req, &r)
		f.kv[r.Scope+"/"+r.Key] = r.Value
		return nil, nil
	case "bot.discord_api":
		f.discordN++
		if m["path"] == "users/@me" {
			return f.me, nil
		}
		return f.app, nil
	case "bot.identity":
		var r map[string]string
		_ = json.Unmarshal(req, &r)
		f.identity = append(f.identity, r)
		return nil, nil
	case "notify":
		var r map[string]string
		_ = json.Unmarshal(req, &r)
		f.notified = append(f.notified, r)
		return nil, nil
	case "bots.list":
		return []map[string]string{{"id": "b1"}}, nil
	}
	return nil, &sdk.HostError{Code: "denied", Message: name}
}

func setup(t *testing.T) *fakeHost {
	f := &fakeHost{kv: map[string][]byte{}}
	sdk.SetHost(f.host)
	sdk.Handle(handle)
	t.Cleanup(func() { sdk.SetHost(nil) })
	return f
}

func TestCardShowsProfileAndCaches(t *testing.T) {
	f := setup(t)
	f.me = map[string]any{"status": 200, "token": "present", "token_var": "DISCORD_TOKEN", "token_fp": "fp1",
		"body": map[string]any{"id": "123456789012345678", "username": "Rivet", "avatar": "abc"}}
	f.app = map[string]any{"status": 200, "body": map[string]any{"id": "123456789012345678", "name": "Rivet App", "bot_public": true, "approximate_guild_count": 42}}
	resp := sdk.Invoke(&sdk.Request{Kind: "route", Route: "card", BotID: "b1"})
	if resp.Status != 200 || !strings.Contains(string(resp.Body), `"value":42`) || !strings.Contains(string(resp.Body), "client_id=123456789012345678") {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if len(f.identity) != 1 || f.identity[0]["avatar_url"] != "https://cdn.discordapp.com/avatars/123456789012345678/abc.png?size=128" {
		t.Fatalf("%v", f.identity)
	}
	calls := f.discordN
	sdk.Invoke(&sdk.Request{Kind: "route", Route: "card", BotID: "b1"})
	if f.discordN != calls {
		t.Fatal("a fresh snapshot was not reused")
	}
}

func TestRejectedTokenWarnsOncePerToken(t *testing.T) {
	f := setup(t)
	f.me = map[string]any{"status": 401, "token": "present", "token_var": "DISCORD_TOKEN", "token_fp": "fp1"}
	for i := 0; i < 3; i++ {
		sdk.Invoke(&sdk.Request{Kind: "job", Job: "refresh"})
	}
	if len(f.notified) != 1 {
		t.Fatalf("notified %d times", len(f.notified))
	}
	f.me["token_fp"] = "fp2" // a new token that is also rejected
	sdk.Invoke(&sdk.Request{Kind: "job", Job: "refresh"})
	if len(f.notified) != 2 {
		t.Fatalf("notified %d times", len(f.notified))
	}
	resp := sdk.Invoke(&sdk.Request{Kind: "route", Route: "card", BotID: "b1"})
	if !strings.Contains(string(resp.Body), "Token rejected") {
		t.Fatalf("%s", resp.Body)
	}
}

func TestMissingTokenAndStaleRefresh(t *testing.T) {
	f := setup(t)
	f.me = map[string]any{"status": 0, "token": "missing"}
	resp := sdk.Invoke(&sdk.Request{Kind: "route", Route: "card", BotID: "b1"})
	if !strings.Contains(string(resp.Body), "No token found") {
		t.Fatalf("%s", resp.Body)
	}
	calls := f.discordN
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return time.Now().Add(time.Hour) }
	sdk.Invoke(&sdk.Request{Kind: "route", Route: "card", BotID: "b1"})
	if f.discordN == calls {
		t.Fatal("a stale snapshot was not refreshed")
	}
}
