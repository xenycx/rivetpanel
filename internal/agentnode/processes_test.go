package agentnode

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// The process list validates its query, passes the container filter through
// and cuts command lines to the wire limit.
func TestProcessesRoute(t *testing.T) {
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	cid := strings.Repeat("c", 64)
	var gotLimit int
	var gotCID string
	app := App(Deps{Files: files, Processes: func(_ context.Context, limit int, containerID string) (agentproto.Processes, error) {
		gotLimit, gotCID = limit, containerID
		return agentproto.Processes{SampledAtMS: 5, Source: domain.ProcSourceAgent,
			Items: []domain.ProcessEntry{{PID: 1, Comm: "x", Cmd: strings.Repeat("a", 2000), ContainerID: cid}}}, nil
	}})
	resp := nodeRequest(t, app, http.MethodGet, "/node/v1/processes?limit=7&container="+cid, nil, "")
	defer resp.Body.Close()
	var out agentproto.Processes
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if gotLimit != 7 || gotCID != cid || len(out.Items) != 1 || len(out.Items[0].Cmd) != agentproto.MaxProcessCmd {
		t.Fatalf("%d %q %+v", gotLimit, gotCID, out)
	}
	for _, q := range []string{"?limit=0", "?limit=500", "?container=../x"} {
		r := nodeRequest(t, app, http.MethodGet, "/node/v1/processes"+q, nil, "")
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d", q, r.StatusCode)
		}
	}
	none := App(Deps{Files: files})
	r := nodeRequest(t, none, http.MethodGet, "/node/v1/processes", nil, "")
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("without a source: %d", r.StatusCode)
	}
}
