package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

func auditEnv(t *testing.T) *env {
	e := newEnv(t)
	a := &service.Audit{Store: e.db, Bots: e.bots}
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Catalog: e.bots.Catalog,
		Nodes: e.db, Audit: a, SecureCookies: true})
	return e
}

type auditList struct {
	Events []auditDTO `json:"events"`
}

func TestActivityRecordsChangesWithoutValues(t *testing.T) {
	e := auditEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	other := e.user("other@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": "super-secret-value"}})
	owner.mustStatus(200, "POST", base+"/env/DISCORD_TOKEN/reveal", nil)
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "other@x.io", "permissions": domain.PermViewConsole})
	other.mustStatus(403, "POST", base+"/env/DISCORD_TOKEN/reveal", nil) // a refused reveal is recorded
	owner.mustStatus(202, "POST", base+"/start", nil)

	body := owner.mustStatus(200, "GET", base+"/changes", nil)
	if strings.Contains(string(body), "super-secret-value") {
		t.Fatal("a secret value reached the activity record")
	}
	var l auditList
	json.Unmarshal(body, &l)
	var actions []string
	for _, ev := range l.Events {
		actions = append(actions, ev.Action+":"+ev.Outcome+":"+deref(ev.Target))
	}
	got := strings.Join(actions, " ")
	for _, want := range []string{"bot.start:ok:", "env.reveal:denied:DISCORD_TOKEN", "access.grant:ok:other@x.io", "env.reveal:ok:DISCORD_TOKEN", "env.set:ok:DISCORD_TOKEN", "bot.create:ok:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	// A shared user sees the bot's history; an unrelated user does not.
	other.mustStatus(200, "GET", base+"/changes", nil)
	stranger := e.user("x@x.io", domain.RoleUser)
	stranger.mustStatus(404, "GET", base+"/changes", nil)
	json.Unmarshal(stranger.mustStatus(200, "GET", "/api/v1/activity/changes", nil), &l)
	for _, ev := range l.Events {
		if ev.BotID != nil && *ev.BotID == id {
			t.Fatal("an unrelated user saw another user's bot history")
		}
	}
	// Sign-ins (including failed attempts) show up in the account's own activity.
	(&client{e: e}).req("POST", "/api/v1/auth/login", map[string]string{"email": "x@x.io", "password": "wrong-password-here"})
	json.Unmarshal(stranger.mustStatus(200, "GET", "/api/v1/activity/changes", nil), &l)
	signIns := 0
	for _, ev := range l.Events {
		if ev.Action == "account.sign_in" {
			signIns++
		}
	}
	if signIns < 2 {
		t.Fatalf("sign-ins recorded = %d, want the success and the failure", signIns)
	}
}
