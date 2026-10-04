package mail

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveMailgun talks to the real Mailgun API. It is skipped unless
// RIVET_TEST_MAILGUN_KEY and RIVET_TEST_MAILGUN_DOMAIN are set, and it
// always uses test mode, so nothing is delivered.
//
//	RIVET_TEST_MAILGUN_KEY=... RIVET_TEST_MAILGUN_DOMAIN=mg.example.com \
//	RIVET_TEST_MAILGUN_REGION=eu go test ./internal/mail -run Live -v
func TestLiveMailgun(t *testing.T) {
	cfg := Config{APIKey: os.Getenv("RIVET_TEST_MAILGUN_KEY"), Domain: os.Getenv("RIVET_TEST_MAILGUN_DOMAIN"),
		Region: os.Getenv("RIVET_TEST_MAILGUN_REGION")}
	if cfg.APIKey == "" || cfg.Domain == "" {
		t.Skip("set RIVET_TEST_MAILGUN_KEY and RIVET_TEST_MAILGUN_DOMAIN to run")
	}
	cfg.From = "RivetPanel <rivetpanel@" + cfg.Domain + ">"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &Client{}
	info, err := c.Check(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("domain %s state=%s type=%s dns_verified=%v", info.Name, info.State, info.Type, info.Verified)
	b := Test()
	id, err := c.Send(ctx, cfg, Message{To: "test@example.com", Subject: b.Subject, Text: b.Text, HTML: b.HTML, Tag: "test", TestMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("accepted in test mode, id %s", id)
	cfg.APIKey = "key-wrong"
	if _, err := c.Check(ctx, cfg); err == nil {
		t.Fatal("a wrong key was accepted")
	} else {
		t.Logf("wrong key is explained as: %v", err)
	}
}
