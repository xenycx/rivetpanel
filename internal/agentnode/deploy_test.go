package agentnode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

func repoTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "o-r-abc/", Typeflag: tar.TypeDir, Mode: 0o755})
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: "o-r-abc/" + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestRemoteDeployStagesAppliesAndCommits(t *testing.T) {
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	botID := uuid.NewString()
	if err := files.Create(botID); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, files, botID, "index.js", "old")
	app := App(Deps{Files: files, MaxUpload: 8 << 20})
	deployPath := "/node/v1/bots/" + botID + "/deploy?" + url.Values{
		"max_entries": {"100"}, "max_bytes": {"8388608"}, "max_file": {"4194304"}, "root_dir": {"bot"},
	}.Encode()
	txPath := func(tx string) string { return "/node/v1/bots/" + botID + "/transactions/" + tx }

	// Validation failures are reported as the archive's fault and change nothing.
	resp := nodeRequest(t, app, http.MethodPost, deployPath, repoTarball(t, map[string]string{"index.js": "x"}), "application/gzip")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("root directory")) {
		t.Fatalf("missing root directory = %d %s", resp.StatusCode, body)
	}

	archive := repoTarball(t, map[string]string{"bot/index.js": "new", "README.md": "outside the root"})
	begin := func() string {
		t.Helper()
		resp := nodeRequest(t, app, http.MethodPost, deployPath, archive, "application/gzip")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("begin deploy = %d %s", resp.StatusCode, b)
		}
		var out agentproto.TransactionStarted
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Transaction == "" {
			t.Fatalf("transaction = %+v %v", out, err)
		}
		return out.Transaction
	}
	complete := func(tx string, commit bool, want int) {
		t.Helper()
		in, _ := json.Marshal(agentproto.TransactionComplete{Commit: commit})
		resp := nodeRequest(t, app, http.MethodPost, txPath(tx), in, "application/json")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("complete(%v) = %d, want %d", commit, resp.StatusCode, want)
		}
	}

	// Staged: nothing changed, a second deployment is refused, and the panel
	// can abandon it.
	tx := begin()
	if got := readWorkspaceFile(t, files, botID, "index.js"); got != "old" {
		t.Fatalf("staging changed the workspace: %q", got)
	}
	busy := nodeRequest(t, app, http.MethodPost, deployPath, archive, "application/gzip")
	busy.Body.Close()
	if busy.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent deploy = %d", busy.StatusCode)
	}
	complete(tx, true, http.StatusConflict) // must be applied first
	complete(tx, false, http.StatusNoContent)
	if got := readWorkspaceFile(t, files, botID, "index.js"); got != "old" {
		t.Fatalf("abandoned deployment changed files: %q", got)
	}

	// Applied, then rolled back after a failed database update.
	tx = begin()
	resp = nodeRequest(t, app, http.MethodPost, txPath(tx)+"/apply", nil, "")
	var applied agentproto.DeployApplied
	json.NewDecoder(resp.Body).Decode(&applied)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || applied.Files != 1 {
		t.Fatalf("apply = %d %+v", resp.StatusCode, applied)
	}
	if got := readWorkspaceFile(t, files, botID, "index.js"); got != "new" {
		t.Fatalf("applied file = %q", got)
	}
	complete(tx, false, http.StatusNoContent)
	if got := readWorkspaceFile(t, files, botID, "index.js"); got != "old" {
		t.Fatalf("rolled back file = %q", got)
	}

	// Applied and committed.
	tx = begin()
	resp = nodeRequest(t, app, http.MethodPost, txPath(tx)+"/apply", nil, "")
	resp.Body.Close()
	complete(tx, true, http.StatusNoContent)
	complete(tx, true, http.StatusNoContent) // a retried confirmation
	if got := readWorkspaceFile(t, files, botID, "index.js"); got != "new" {
		t.Fatalf("committed file = %q", got)
	}
	resp = nodeRequest(t, app, http.MethodPost, txPath("unknown")+"/apply", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown transaction = %d", resp.StatusCode)
	}
}
