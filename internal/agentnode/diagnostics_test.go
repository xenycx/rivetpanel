package agentnode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

type fakeDiag struct {
	bot, runtime string
	argv         []string
	res          domain.DiagnosticResult
	err          error
	block        chan struct{}
	started      chan struct{}
}

func (f *fakeDiag) RunDiagnostic(_ context.Context, botID, runtimeID string, argv []string) (domain.DiagnosticResult, error) {
	f.bot, f.runtime, f.argv = botID, runtimeID, argv
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.res, f.err
}

func TestDiagnosticsRoute(t *testing.T) {
	files, err := filesystem.NewManager(filepath.Join(t.TempDir(), "w"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	bot := uuid.NewString()
	if err := files.Create(bot); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(agentproto.DiagnosticRequest{Runtime: "python", Argv: []string{"python", "--version"}})
	path := "/node/v1/bots/" + bot + "/diagnostics"

	// Without a diagnostic runner: 503.
	resp := nodeRequest(t, App(Deps{Files: files}), http.MethodPost, path, body, "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no runner: %d", resp.StatusCode)
	}

	f := &fakeDiag{res: domain.DiagnosticResult{ExitCode: 0, Output: "Python 3.13", Duration: 1500 * time.Millisecond}}
	app := App(Deps{Files: files, Diagnostics: f})
	for name, tc := range map[string]struct {
		path string
		body []byte
		want int
	}{
		"invalid id":    {"/node/v1/bots/not-a-uuid/diagnostics", body, 400},
		"bad json":      {path, []byte("{"), 400},
		"empty argv":    {path, []byte(`{"runtime":"python","argv":[]}`), 400},
		"no runtime":    {path, []byte(`{"argv":["python"]}`), 400},
		"too many args": {path, mustJSON(agentproto.DiagnosticRequest{Runtime: "python", Argv: make([]string, 21)}), 400},
		"no workspace":  {"/node/v1/bots/" + uuid.NewString() + "/diagnostics", body, 404},
	} {
		resp := nodeRequest(t, app, http.MethodPost, tc.path, tc.body, "application/json")
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", name, resp.StatusCode, tc.want)
		}
	}
	if f.bot != "" {
		t.Fatal("an invalid request reached the diagnostic runner")
	}

	resp = nodeRequest(t, app, http.MethodPost, path, body, "application/json")
	var out agentproto.DiagnosticResult
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || out.Output != "Python 3.13" || out.DurationMS != 1500 || out.Error != "" || f.bot != bot || f.runtime != "python" {
		t.Fatalf("run: %d %+v %+v", resp.StatusCode, out, f)
	}

	// A command the node's catalog refuses is a 400 with its message.
	f.err, f.res = domain.Invalid("that command is not allowed"), domain.DiagnosticResult{}
	resp = nodeRequest(t, app, http.MethodPost, path, body, "application/json")
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("refused command: %d", resp.StatusCode)
	}
	// A run that failed after starting keeps its output.
	f.err, f.res = errors.New("context deadline exceeded"), domain.DiagnosticResult{ExitCode: -1, Output: "partial", Duration: time.Second}
	resp = nodeRequest(t, app, http.MethodPost, path, body, "application/json")
	out = agentproto.DiagnosticResult{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || out.Output != "partial" || out.Error == "" {
		t.Fatalf("timed out run: %d %+v", resp.StatusCode, out)
	}

	// At most maxConcurrentDiagnostics run at once.
	blocked := &fakeDiag{block: make(chan struct{}), started: make(chan struct{}, maxConcurrentDiagnostics)}
	app = App(Deps{Files: files, Diagnostics: blocked})
	done := make(chan int, maxConcurrentDiagnostics)
	for i := 0; i < maxConcurrentDiagnostics; i++ {
		go func() {
			r := nodeRequest(t, app, http.MethodPost, path, body, "application/json")
			r.Body.Close()
			done <- r.StatusCode
		}()
		<-blocked.started
	}
	resp = nodeRequest(t, app, http.MethodPost, path, body, "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", resp.StatusCode)
	}
	close(blocked.block)
	for i := 0; i < maxConcurrentDiagnostics; i++ {
		if c := <-done; c != 200 {
			t.Fatalf("blocked run: %d", c)
		}
	}
}

func TestTelemetryRouteReportsWorkspaceDisk(t *testing.T) {
	files, err := filesystem.NewManager(filepath.Join(t.TempDir(), "w"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	resp := nodeRequest(t, App(Deps{Files: files}), http.MethodGet, "/node/v1/telemetry", nil, "")
	var out agentproto.Telemetry
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	total, _, _ := files.Statfs()
	if resp.StatusCode != 200 || out.DiskTotalBytes != total || out.DiskFreeBytes == 0 || out.SampledAtMS == 0 {
		t.Fatalf("%d %+v", resp.StatusCode, out)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
