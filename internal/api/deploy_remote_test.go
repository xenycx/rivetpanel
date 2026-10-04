package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/nodetx"
)

// fakeNode stands in for a connected rivet-agent: its workspaces live in a
// separate directory and it runs the same staged/journaled transaction
// registry as the real agent (the HTTP shim is covered in agentnode).
type fakeNode struct {
	mu         sync.Mutex
	files      *filesystem.Manager
	reg        *nodetx.Registry
	online     bool
	afterStage func(botID string) // runs once the archive is staged
	begun      int
}

func (f *fakeNode) Online(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online
}

func nodeErr(err error) error {
	if errors.Is(err, nodetx.ErrNotFound) {
		return errors.Join(err, domain.ErrNotFound)
	}
	return err
}

func (f *fakeNode) BeginDeploy(_ context.Context, _ string, botID string, src io.Reader, rootDir string, lim filesystem.BackupLimits) (string, error) {
	if !f.Online("") {
		return "", domain.Invalid("the server's node is offline; try again when its agent reconnects")
	}
	if _, err := f.files.Path(botID); err != nil {
		if err := f.files.Create(botID); err != nil {
			return "", err
		}
	}
	w, err := f.files.Open(botID)
	if err != nil {
		return "", err
	}
	tx, err := f.reg.BeginDeploy(botID, w, func(gate func() error) (int, *filesystem.Commit, error) {
		return w.DeployTarGzCommit(src, rootDir, lim, gate)
	})
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.begun++
	hook := f.afterStage
	f.mu.Unlock()
	if hook != nil {
		hook(botID)
	}
	return tx, nil
}

func (f *fakeNode) ApplyDeploy(_ context.Context, _ string, botID, tx string) (int, error) {
	n, err := f.reg.Apply(botID, tx)
	return n, nodeErr(err)
}

func (f *fakeNode) CompleteTransaction(_ context.Context, _ string, botID, tx string, commit bool) error {
	return nodeErr(f.reg.Complete(botID, tx, commit))
}

func (f *fakeNode) read(t *testing.T, botID, name string) string {
	t.Helper()
	dir, err := f.files.Path(botID)
	if err != nil {
		return ""
	}
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}

const remoteDeployNode = "22222222-2222-4222-8222-222222222222"

func newRemoteDeployRig(t *testing.T) (*deployRig, *fakeNode) {
	r := newDeployRig(t)
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	now := time.Now().UnixMilli()
	if _, err := r.e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Deploy agent', 'agent', NULL, 1, ?, ?)`, remoteDeployNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	node := &fakeNode{files: files, reg: nodetx.New(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute), online: true}
	r.e.bots.RemoteNode = func(id string) bool { return id == remoteDeployNode }
	r.svc.Remote = node
	return r, node
}

// waitNewOp waits for a finished operation newer than prev.
func (r *deployRig) waitNewOp(t *testing.T, base, prev string) opDTO {
	t.Helper()
	for i := 0; i < 300; i++ {
		var l struct{ Operations []opDTO }
		json.Unmarshal(r.c.mustStatus(200, "GET", base+"/operations", nil), &l)
		if len(l.Operations) > 0 && l.Operations[0].ID != prev && l.Operations[0].Status != "running" && l.Operations[0].Status != "queued" {
			return l.Operations[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return opDTO{}
}

func (r *deployRig) setHead(sha string, files map[string]string) {
	r.gh.mu.Lock()
	r.gh.sha, r.gh.files = sha, files
	r.gh.mu.Unlock()
}

func TestRemoteGitHubDeploy(t *testing.T) {
	r, node := newRemoteDeployRig(t)
	c := r.c
	gh := map[string]any{"full_name": "o/r", "branch": "main", "auto_deploy": true}

	// Only administrators choose a node; an offline node is refused before a
	// bot is created.
	c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "r", "runtime": "nodejs", "node_id": remoteDeployNode, "github": gh})
	if _, err := r.e.db.Exec(`UPDATE users SET role = 'admin'`); err != nil {
		t.Fatal(err)
	}
	node.online = false
	if out := string(c.mustStatus(400, "POST", "/api/v1/bots", map[string]any{"name": "r", "runtime": "nodejs", "node_id": remoteDeployNode, "github": gh})); !strings.Contains(out, "offline") {
		t.Fatalf("offline node: %s", out)
	}
	var n int
	r.e.db.QueryRow(`SELECT count(*) FROM bots`).Scan(&n)
	if n != 0 {
		t.Fatalf("refused creations left %d bots", n)
	}

	// Created on the node: the first deployment lands in the node's
	// workspace, never in the panel's.
	node.online = true
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "remote gh", "runtime": "nodejs",
		"node_id": remoteDeployNode, "github": gh}), &b)
	if b.NodeID != remoteDeployNode {
		t.Fatalf("node = %q", b.NodeID)
	}
	base := "/api/v1/bots/" + b.ID
	r.waitDeploy(t, base, strings.Repeat("a", 40))
	if got := node.read(t, b.ID, "index.js"); got != "v1" {
		t.Fatalf("remote index.js = %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.e.dataDir, b.ID, "index.js")); err == nil {
		t.Fatal("a remote deployment was unpacked on the panel host")
	}

	// A signed push deploys the new commit to the node.
	_, cfg := r.gh.onlyHook()
	r.setHead(strings.Repeat("b", 40), map[string]string{"index.js": "v2"})
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": strings.Repeat("b", 40), "repository": map[string]any{"full_name": "o/r"}})
	anon := &client{e: r.e}
	resp, out := anon.req("POST", "/api/v1/webhooks/github", nil, func(req *http.Request) {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", "remote-1")
		req.Header.Set("X-Hub-Signature-256", sign(cfg["secret"], body))
	})
	if resp.StatusCode != 202 || !strings.Contains(string(out), "queued") {
		t.Fatalf("push: %d %s", resp.StatusCode, out)
	}
	r.waitDeploy(t, base, strings.Repeat("b", 40))
	if got := node.read(t, b.ID, "index.js"); got != "v2" {
		t.Fatalf("pushed index.js = %q", got)
	}

	// The database refuses to record the commit: the node's swap is rolled back.
	if _, err := r.e.db.Exec(`CREATE TRIGGER fail_record BEFORE UPDATE ON github_repos WHEN NEW.last_deployed_sha = '` +
		strings.Repeat("c", 40) + `' BEGIN SELECT RAISE(ABORT, 'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	r.setHead(strings.Repeat("c", 40), map[string]string{"index.js": "v3"})
	prev := r.waitOp(t, base).ID
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	if op := r.waitNewOp(t, base, prev); op.Status != "failed" {
		t.Fatalf("deploy with a failed record: %+v", op)
	}
	if got := node.read(t, b.ID, "index.js"); got != "v2" || node.reg.Open() != 0 {
		t.Fatalf("after a failed record: index.js = %q, open transactions = %d", got, node.reg.Open())
	}
	r.e.db.Exec(`DROP TRIGGER fail_record`)

	// The link changes while the node stages the archive: it is abandoned
	// before any file changes.
	node.mu.Lock()
	node.afterStage = func(id string) {
		r.e.db.Exec(`UPDATE github_repos SET root_dir = 'bot' WHERE bot_id = ?`, id)
	}
	node.mu.Unlock()
	prev = r.waitOp(t, base).ID
	c.mustStatus(202, "POST", base+"/github/deploy", nil)
	if op := r.waitNewOp(t, base, prev); op.Status != "cancelled" || !strings.Contains(deref(op.Message), "link changed") {
		t.Fatalf("deploy during a link change: %+v %s", op, deref(op.Message))
	}
	if got := node.read(t, b.ID, "index.js"); got != "v2" || node.reg.Open() != 0 {
		t.Fatalf("after a superseded deploy: index.js = %q, open transactions = %d", got, node.reg.Open())
	}
	node.mu.Lock()
	node.afterStage = nil
	node.mu.Unlock()
	r.e.db.Exec(`UPDATE github_repos SET root_dir = '' WHERE bot_id = ?`, b.ID)

	// Offline: manual deploys are refused and polling waits for the node.
	r.e.db.Exec(`UPDATE github_repos SET hook_id = NULL WHERE bot_id = ?`, b.ID)
	node.mu.Lock()
	node.online = false
	begun := node.begun
	node.mu.Unlock()
	if out := string(c.mustStatus(400, "POST", base+"/github/deploy", nil)); !strings.Contains(out, "offline") {
		t.Fatalf("deploy to an offline node: %s", out)
	}
	if q := r.svc.PollOnce(t.Context()); q != 0 {
		t.Fatalf("polling queued %d deployments for an offline node", q)
	}
	node.mu.Lock()
	node.online = true
	node.mu.Unlock()
	if q := r.svc.PollOnce(t.Context()); q != 1 {
		t.Fatalf("polling after reconnect queued %d", q)
	}
	r.waitDeploy(t, base, strings.Repeat("c", 40))
	if got := node.read(t, b.ID, "index.js"); got != "v3" || node.begun <= begun {
		t.Fatalf("polled deployment: index.js = %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.e.dataDir, b.ID, "index.js")); err == nil {
		t.Fatal("a remote deployment was unpacked on the panel host")
	}
}
