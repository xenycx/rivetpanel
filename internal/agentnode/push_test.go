package agentnode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// TestPushSetSelectsWithSharedRules covers the protocol 4 push selection:
// the node applies .gitignore, built-in and secret-file exclusions, enforces
// the requested limits, refuses limits above the shared defaults, and the
// selected files are readable through the existing files/content route.
func TestPushSetSelectsWithSharedRules(t *testing.T) {
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	botID := uuid.NewString()
	if err := files.Create(botID); err != nil {
		t.Fatal(err)
	}
	dir, _ := files.Path(botID)
	for p, content := range map[string]string{"index.js": "console.log(1)", ".env": "TOKEN=secret", ".env.example": "TOKEN=",
		"node_modules/x/index.js": "dep", ".gitignore": "data/\n*.log\n!keep.log\n", "data/db.json": "{}", "src/util.js": "u",
		"debug.log": "noise", "keep.log": "kept"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	app := App(Deps{Files: files, MaxUpload: 8 << 20})
	def := filesystem.DefaultPushLimits
	pushSet := func(req agentproto.PushSetRequest, id string) (*http.Response, agentproto.PushSet) {
		in, _ := json.Marshal(req)
		resp := nodeRequest(t, app, http.MethodPost, "/node/v1/bots/"+id+"/push-set", in, "application/json")
		defer resp.Body.Close()
		var out agentproto.PushSet
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		} else {
			b, _ := io.ReadAll(resp.Body)
			out.Skipped = append(out.Skipped, filesystem.PushSkip{Reason: string(b)})
		}
		return resp, out
	}

	resp, set := pushSet(agentproto.PushSetRequest{MaxFiles: def.MaxFiles, MaxBytes: def.MaxBytes, MaxFile: def.MaxFile}, botID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push-set = %d", resp.StatusCode)
	}
	var paths []string
	for _, f := range set.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	if got, want := strings.Join(paths, ","), ".env.example,.gitignore,index.js,keep.log,src/util.js"; got != want {
		t.Fatalf("selected = %s, want %s", got, want)
	}
	if set.Ignored == 0 {
		t.Fatal("ignored count not reported")
	}

	// Selected files are read through the existing file route.
	q := url.Values{"path": {"src/util.js"}, "download": {"1"}}
	r := nodeRequest(t, app, http.MethodGet, "/node/v1/bots/"+botID+"/files/content?"+q.Encode(), nil, "")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || string(body) != "u" {
		t.Fatalf("read = %d %q", r.StatusCode, body)
	}

	// Limits are enforced on the node with a message for the user.
	resp, set = pushSet(agentproto.PushSetRequest{MaxFiles: 2, MaxBytes: def.MaxBytes, MaxFile: def.MaxFile}, botID)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(set.Skipped[0].Reason, "more than 2 files") {
		t.Fatalf("file limit = %d %v", resp.StatusCode, set.Skipped)
	}
	resp, set = pushSet(agentproto.PushSetRequest{MaxFiles: def.MaxFiles, MaxBytes: def.MaxBytes, MaxFile: 4}, botID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("per-file limit = %d", resp.StatusCode)
	}
	for _, f := range set.Files {
		if f.Size > 4 {
			t.Fatalf("file over the per-file limit selected: %+v", f)
		}
	}
	if len(set.Skipped) == 0 {
		t.Fatal("oversized files not reported as skipped")
	}

	// A panel cannot ask for more than local publishing allows, and unknown
	// servers are not found.
	if resp, _ := pushSet(agentproto.PushSetRequest{MaxFiles: def.MaxFiles + 1, MaxBytes: def.MaxBytes, MaxFile: def.MaxFile}, botID); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("over-default limits = %d", resp.StatusCode)
	}
	if resp, _ := pushSet(agentproto.PushSetRequest{MaxFiles: 10, MaxBytes: 10, MaxFile: 10}, uuid.NewString()); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown bot = %d", resp.StatusCode)
	}
}
