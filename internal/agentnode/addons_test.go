package agentnode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/runner"
)

type fakeAddons struct {
	bot, kind string
	lines     int
	output    string
}

func (f *fakeAddons) AddonStates(_ context.Context, botID string) (map[string]runner.AddonStatus, error) {
	f.bot = botID
	return map[string]runner.AddonStatus{"redis": {State: "running", Health: "healthy"}}, nil
}

func (f *fakeAddons) AddonLogs(_ context.Context, botID, kind string, lines int) (string, error) {
	f.bot, f.kind, f.lines = botID, kind, lines
	return f.output, nil
}

func TestWorkspaceRouteRejectsInvalidID(t *testing.T) {
	files, err := filesystem.NewManager(filepath.Join(t.TempDir(), "w"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	app := App(Deps{Files: files, MaxUpload: 1 << 20})
	for _, id := range []string{"not-a-uuid", "..", strings.ToUpper(uuid.NewString())} {
		resp := nodeRequest(t, app, http.MethodPost, "/node/v1/workspaces/"+id, nil, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", id, resp.StatusCode)
		}
	}
	id := uuid.NewString()
	for i := 0; i < 2; i++ { // idempotent
		resp := nodeRequest(t, app, http.MethodPost, "/node/v1/workspaces/"+id, nil, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create %d: status %d", i, resp.StatusCode)
		}
	}
}

func TestAddonRoutes(t *testing.T) {
	files, err := filesystem.NewManager(filepath.Join(t.TempDir(), "w"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	bot := uuid.NewString()

	// Without add-on observation the routes are unavailable.
	bare := App(Deps{Files: files})
	resp := nodeRequest(t, bare, http.MethodGet, "/node/v1/bots/"+bot+"/addons", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no addons: %d", resp.StatusCode)
	}

	fa := &fakeAddons{output: strings.Repeat("x", agentproto.MaxAddonLogBytes+10) + "END"}
	app := App(Deps{Files: files, Addons: fa})
	resp = nodeRequest(t, app, http.MethodGet, "/node/v1/bots/"+bot+"/addons", nil, "")
	var st agentproto.AddonStates
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != 200 || st.Addons["redis"].State != "running" || fa.bot != bot {
		t.Fatalf("states %d %+v", resp.StatusCode, st)
	}

	resp = nodeRequest(t, app, http.MethodGet, "/node/v1/bots/"+bot+"/addons/redis/logs?lines=20", nil, "")
	var logs agentproto.AddonLogs
	json.NewDecoder(resp.Body).Decode(&logs)
	resp.Body.Close()
	if resp.StatusCode != 200 || fa.kind != "redis" || fa.lines != 20 || len(logs.Output) != agentproto.MaxAddonLogBytes || !strings.HasSuffix(logs.Output, "END") {
		t.Fatalf("logs %d kind=%q lines=%d len=%d", resp.StatusCode, fa.kind, fa.lines, len(logs.Output))
	}

	for _, p := range []string{
		"/node/v1/bots/nope/addons",
		"/node/v1/bots/nope/addons/redis/logs",
		"/node/v1/bots/" + bot + "/addons/oracle/logs",
		"/node/v1/bots/" + bot + "/addons/redis/logs?lines=0",
		"/node/v1/bots/" + bot + "/addons/redis/logs?lines=501",
		"/node/v1/bots/" + bot + "/addons/redis/logs?lines=x",
	} {
		resp := nodeRequest(t, app, http.MethodGet, p, nil, "")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, resp.StatusCode)
		}
	}
}
