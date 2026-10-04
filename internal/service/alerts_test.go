package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/events"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

func TestValidWebhookOnlyDiscord(t *testing.T) {
	for u, want := range map[string]bool{
		"https://discord.com/api/webhooks/1/abc":              true,
		"https://discordapp.com/api/webhooks/1/abc":           true,
		"https://canary.discord.com/api/webhooks/1/abc":       true,
		"http://discord.com/api/webhooks/1/abc":               false,
		"https://evil.example/api/webhooks/1/abc":             false,
		"https://discord.com.evil.example/api/webhooks/1/abc": false,
		"https://discord.com@evil.example/api/webhooks/1/abc": false,
		"https://discord.com:8443/api/webhooks/1/abc":         false,
		"https://discord.com/other":                           false,
		"https://127.0.0.1/api/webhooks/1":                    false,
		"":                                                    false,
	} {
		if validWebhook(u) != want {
			t.Errorf("validWebhook(%q) = %v, want %v", u, !want, want)
		}
	}
}

type alertStore struct {
	accts []domain.OAuthAccount
	bots  map[string]domain.Bot
}

func (a *alertStore) ListOAuthAccounts(ctx context.Context, uid string) ([]domain.OAuthAccount, error) {
	return a.accts, nil
}
func (a *alertStore) GetBot(ctx context.Context, id string) (domain.Bot, error) {
	if b, ok := a.bots[id]; ok {
		return b, nil
	}
	return domain.Bot{}, domain.ErrNotFound
}

// Unused parts of OAuthStore.
func (a *alertStore) GetUserByID(context.Context, string) (domain.User, error) {
	return domain.User{}, nil
}
func (a *alertStore) GetOAuthAccount(context.Context, string, string) (domain.OAuthAccount, error) {
	return domain.OAuthAccount{}, domain.ErrNotFound
}
func (a *alertStore) UpsertOAuthAccount(context.Context, domain.OAuthAccount) error { return nil }
func (a *alertStore) UnlinkOAuthAccount(context.Context, string, string) error      { return nil }
func (a *alertStore) CreateUserWithOAuth(context.Context, domain.User, domain.OAuthAccount) error {
	return nil
}
func (a *alertStore) SetOAuthWebhook(context.Context, string, string, []byte, []byte, *string, int64) error {
	return nil
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAlertsSendAndThrottle(t *testing.T) {
	kr, _ := secrets.NewKeyring("k1", map[string][]byte{"k1": make([]byte, 32)})
	seal := func(url string) domain.OAuthAccount {
		sl, _ := kr.Seal(secrets.OAuthNS("u1"), secrets.OAuthWebhookName("discord"), []byte(url))
		return domain.OAuthAccount{Provider: "discord", UserID: "u1", NotifyEnabled: true, WebhookCipher: sl.Ciphertext, WebhookNonce: sl.Nonce, WebhookKeyID: &sl.KeyID}
	}
	var mu sync.Mutex
	var posts []map[string]any
	var hosts []string
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		mu.Lock()
		posts, hosts = append(posts, m), append(hosts, r.URL.Host)
		mu.Unlock()
		return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	st := &alertStore{accts: []domain.OAuthAccount{seal("https://discord.com/api/webhooks/1/abc")},
		bots: map[string]domain.Bot{"b1": {ID: "b1", OwnerID: "u1", Name: "My Bot"},
			"g1": {ID: "g1", OwnerID: "u1", Name: "My Server", Kind: domain.KindGame}}}
	var clock atomic.Int64
	clock.Store(1000)
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(posts) }
	a := &AlertService{Store: st, Keys: kr, Bots: st, HTTP: client, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Unix(clock.Load(), 0) }}

	a.Send(context.Background(), "u1", "T", strings.Repeat("x", 5000)+" @everyone")
	if count() != 1 || hosts[0] != "discord.com" {
		t.Fatalf("posts: %v %v", posts, hosts)
	}
	body, _ := json.Marshal(posts[0])
	if !strings.Contains(string(body), `"parse":[]`) || strings.Count(string(body), "x") > 1600 {
		t.Fatalf("mentions must be disabled and the text clipped: %d bytes", len(body))
	}

	// No message without a webhook, with notifications off, or with a hostile stored URL.
	for name, accts := range map[string][]domain.OAuthAccount{
		"none": nil,
		"disabled": {func() domain.OAuthAccount {
			x := seal("https://discord.com/api/webhooks/1/abc")
			x.NotifyEnabled = false
			return x
		}()},
		"hostile":   {seal("https://evil.example/api/webhooks/1/abc")},
		"no secret": {{Provider: "discord", UserID: "u1", NotifyEnabled: true}},
	} {
		st.accts = accts
		before := count()
		a.Send(context.Background(), "u1", "T", "m")
		if count() != before {
			t.Errorf("%s: a message was sent", name)
		}
	}

	// Crash alerts: only for failed bots meant to run, once per interval per bot.
	st.accts = []domain.OAuthAccount{seal("https://discord.com/api/webhooks/1/abc")}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { a.Watch(ctx, bus); close(done) }()
	waitPosts := func(n int) {
		for i := 0; i < 200; i++ {
			if got := count(); got >= n {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("expected %d posts, have %d", n, count())
	}
	time.Sleep(50 * time.Millisecond) // let Watch subscribe
	base := count()
	bus.Publish(events.Status{BotID: "b1", DesiredState: "running", ObservedState: "running"})
	bus.Publish(events.Status{BotID: "b1", DesiredState: "stopped", ObservedState: "failed", LastError: "x"})
	bus.Publish(events.Status{BotID: "b1", DesiredState: "running", ObservedState: "failed", LastError: "exited with code 1; gave up after 5 restarts"})
	waitPosts(base + 1)
	bus.Publish(events.Status{BotID: "b1", DesiredState: "running", ObservedState: "failed", LastError: "again"})
	time.Sleep(100 * time.Millisecond)
	if count() != base+1 {
		t.Fatalf("throttle: %d alerts", count()-base)
	}
	clock.Add(int64(11 * time.Minute / time.Second))
	bus.Publish(events.Status{BotID: "b1", DesiredState: "running", ObservedState: "failed", LastError: "after the interval"})
	waitPosts(base + 2)
	// A taken host port is a configuration problem, not a crash: no alert.
	bus.Publish(events.Status{BotID: "g1", DesiredState: "running", ObservedState: "failed", Reason: domain.ReasonPortConflict,
		LastError: "Port 25565 is already used by container x. Choose another port in Network or stop that container."})
	time.Sleep(100 * time.Millisecond)
	if count() != base+2 {
		t.Fatalf("port conflict alerted: %d", count()-base)
	}
	// Game servers get crash alerts too (they have no SDK heartbeat).
	bus.Publish(events.Status{BotID: "g1", DesiredState: "running", ObservedState: "failed", LastError: "exited with code 1"})
	waitPosts(base + 3)
	mu.Lock()
	b, _ := json.Marshal(posts[base+2])
	mu.Unlock()
	if !strings.Contains(string(b), "My Server") {
		t.Fatalf("game crash alert: %s", b)
	}
	cancel()
	<-done
}
