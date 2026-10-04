package agentnode

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/agentproto"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

func TestRemoteBackupAndTransactionalRestore(t *testing.T) {
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	botID := uuid.NewString()
	if err := files.Create(botID); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, files, botID, "state.txt", "before")
	app := App(Deps{Files: files, MaxUpload: 8 << 20})

	limits := filesystem.BackupLimits{MaxEntries: 100, MaxBytes: 8 << 20, MaxFile: 4 << 20}
	in, _ := json.Marshal(agentproto.BackupRequest{Extra: map[string][]byte{"env.json": []byte(`{"sealed":true}`)},
		MaxEntries: limits.MaxEntries, MaxBytes: limits.MaxBytes, MaxFile: limits.MaxFile})
	resp := nodeRequest(t, app, http.MethodPost, "/node/v1/bots/"+botID+"/backups/archive", in, "application/json")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("archive response = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	archive, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The archive contains both the remote workspace and the sealed metadata
	// supplied by the panel.
	check, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if err := check.Create(botID); err != nil {
		t.Fatal(err)
	}
	cw, err := check.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := cw.RestoreTarGz(bytes.NewReader(archive), limits)
	cw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceFile(t, check, botID, "state.txt"); got != "before" || string(meta["env.json"]) != `{"sealed":true}` {
		t.Fatalf("archive contents = %q, metadata = %q", got, meta["env.json"])
	}

	writeWorkspaceFile(t, check, botID, "state.txt", "after")
	restoreArchive := workspaceArchive(t, check, botID, limits)
	restorePath := "/node/v1/bots/" + botID + "/backups/restore?" + url.Values{
		"max_entries": {"100"}, "max_bytes": {"8388608"}, "max_file": {"4194304"},
	}.Encode()

	// A begun restore swaps the files but retains the previous workspace. A
	// concurrent restore is refused and rollback puts the original file back.
	tx := beginRestore(t, app, restorePath, restoreArchive)
	if got := readWorkspaceFile(t, files, botID, "state.txt"); got != "after" {
		t.Fatalf("restored file = %q", got)
	}
	conflict := nodeRequest(t, app, http.MethodPost, restorePath, restoreArchive, "application/gzip")
	conflict.Body.Close()
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent restore = %d", conflict.StatusCode)
	}
	completeRestore(t, app, botID, tx, false)
	if got := readWorkspaceFile(t, files, botID, "state.txt"); got != "before" {
		t.Fatalf("rolled back file = %q", got)
	}

	// Confirmation makes the replacement permanent. Repeating the same outcome
	// is idempotent so a lost HTTP response can be retried, while asking for the
	// opposite outcome is refused.
	tx = beginRestore(t, app, restorePath, restoreArchive)
	completeRestore(t, app, botID, tx, true)
	if got := readWorkspaceFile(t, files, botID, "state.txt"); got != "after" {
		t.Fatalf("committed file = %q", got)
	}
	replay, _ := json.Marshal(agentproto.RestoreComplete{Commit: true})
	resp = nodeRequest(t, app, http.MethodPost, "/node/v1/bots/"+botID+"/backups/restore/"+tx, replay, "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("replayed confirmation = %d", resp.StatusCode)
	}
	replay, _ = json.Marshal(agentproto.RestoreComplete{Commit: false})
	resp = nodeRequest(t, app, http.MethodPost, "/node/v1/bots/"+botID+"/backups/restore/"+tx, replay, "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting confirmation = %d", resp.StatusCode)
	}
}

func nodeRequest(t *testing.T, app interface {
	Test(*http.Request, ...fiber.TestConfig) (*http.Response, error)
}, method, path string, body []byte, contentType string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func beginRestore(t *testing.T, app interface {
	Test(*http.Request, ...fiber.TestConfig) (*http.Response, error)
}, path string, archive []byte) string {
	t.Helper()
	resp := nodeRequest(t, app, http.MethodPost, path, archive, "application/gzip")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("begin restore = %d: %s", resp.StatusCode, body)
	}
	var out agentproto.RestoreStarted
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Transaction == "" {
		t.Fatalf("restore transaction = %+v, %v", out, err)
	}
	return out.Transaction
}

func completeRestore(t *testing.T, app interface {
	Test(*http.Request, ...fiber.TestConfig) (*http.Response, error)
}, botID, tx string, commit bool) {
	t.Helper()
	in, _ := json.Marshal(agentproto.RestoreComplete{Commit: commit})
	resp := nodeRequest(t, app, http.MethodPost, "/node/v1/bots/"+botID+"/backups/restore/"+tx, in, "application/json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("complete restore = %d: %s", resp.StatusCode, body)
	}
}

func writeWorkspaceFile(t *testing.T, files *filesystem.Manager, botID, name, value string) {
	t.Helper()
	dir, err := files.Path(botID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readWorkspaceFile(t *testing.T, files *filesystem.Manager, botID, name string) string {
	t.Helper()
	dir, err := files.Path(botID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func workspaceArchive(t *testing.T, files *filesystem.Manager, botID string, limits filesystem.BackupLimits) []byte {
	t.Helper()
	w, err := files.Open(botID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var out bytes.Buffer
	if _, _, err := w.WriteTarGz(&out, nil, limits); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
