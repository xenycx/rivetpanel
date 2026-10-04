package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/internal/addons"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/service"
)

// localAddons is this panel's own runner; a remote bot must never reach it.
type localAddons struct {
	mu    sync.Mutex
	calls int
}

func (l *localAddons) AddonStates(context.Context, string) (map[string]service.AddonState, error) {
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return map[string]service.AddonState{"postgres": {State: "exited"}}, nil
}

func (l *localAddons) AddonLogs(context.Context, string, string, int) (string, error) {
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return "panel-local add-on output", nil
}

// fakeNodeAddons stands in for noderoute.Router's add-on calls (the real wire
// is covered by internal/noderoute's hub+agent tests).
type fakeNodeAddons struct {
	mu      sync.Mutex
	online  bool
	calls   []string
	lastMax int
}

func (f *fakeNodeAddons) AddonStates(_ context.Context, nodeID, botID string) (map[string]service.AddonState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "states "+nodeID+" "+botID)
	if !f.online {
		return nil, domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return map[string]service.AddonState{"postgres": {State: "running", Health: "healthy"}}, nil
}

func (f *fakeNodeAddons) AddonLogs(_ context.Context, nodeID, botID, kind string, lines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "logs "+nodeID+" "+botID+" "+kind)
	f.lastMax = lines
	if !f.online {
		return "", domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return "node add-on output", nil
}

func (f *fakeNodeAddons) RemoveAddonData(_ context.Context, nodeID, botID, kind string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "data "+nodeID+" "+botID+" "+kind)
	if !f.online {
		return domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	return nil
}

func TestRemoteAddonLogsAndStates(t *testing.T) {
	e := newEnv(t)
	e.bots.Coord = &service.Coordinator{}
	e.bots.AddonData = addons.DataRoot{Dir: filepath.Join(t.TempDir(), "addons")}
	local := &localAddons{}
	e.bots.AddonRuntime = local
	c := e.user("a@x.io", "user")
	other := e.user("b@x.io", "user")
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "db", "runtime": "go",
		"addons": []map[string]any{{"kind": "postgres"}}}), &b)
	base := "/api/v1/bots/" + b.ID

	placeOnRemoteNode(t, e, b.ID, newFakeNodeFiles(t))

	// Without remote add-on access: refused, and never this panel's runner.
	msg := string(c.mustStatus(400, "GET", base+"/addons/postgres/logs", nil))
	if !strings.Contains(msg, "remote nodes") {
		t.Fatalf("unconfigured: %s", msg)
	}

	node := &fakeNodeAddons{online: true}
	e.bots.RemoteAddons = node
	var logs struct{ Output string }
	json.Unmarshal(c.mustStatus(200, "GET", base+"/addons/postgres/logs?lines=9999", nil), &logs)
	if logs.Output != "node add-on output" || node.lastMax != 500 {
		t.Fatalf("logs %q, lines %d", logs.Output, node.lastMax)
	}
	var list struct{ Addons []service.AddonView }
	json.Unmarshal(c.mustStatus(200, "GET", base+"/addons", nil), &list)
	if len(list.Addons) != 1 || list.Addons[0].Status.State != "running" || list.Addons[0].Status.Health != "healthy" {
		t.Fatalf("states %+v", list.Addons)
	}

	// Kinds the bot does not have, and other accounts, never reach the node.
	before := len(node.calls)
	c.mustStatus(404, "GET", base+"/addons/redis/logs", nil)
	other.mustStatus(404, "GET", base+"/addons/postgres/logs", nil)
	if len(node.calls) != before {
		t.Fatalf("unexpected node calls: %v", node.calls[before:])
	}

	// An offline node is a clean error; the listing still answers.
	node.online = false
	msg = string(c.mustStatus(400, "GET", base+"/addons/postgres/logs", nil))
	if !strings.Contains(msg, "offline") {
		t.Fatalf("offline: %s", msg)
	}
	json.Unmarshal(c.mustStatus(200, "GET", base+"/addons", nil), &list)
	if len(list.Addons) != 1 || list.Addons[0].Status.State != "" {
		t.Fatalf("offline states %+v", list.Addons)
	}

	for _, call := range node.calls {
		if !strings.Contains(call, remoteDeployNode+" "+b.ID) {
			t.Fatalf("call for the wrong node or bot: %q", call)
		}
	}
	if local.calls != 0 {
		t.Fatalf("the panel's own runner was asked %d times about a remote bot", local.calls)
	}
}
