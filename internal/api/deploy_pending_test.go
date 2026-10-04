package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRemoteWebhookWhileNodeOffline: a push received while the server's node
// is offline is not recorded as a failed deployment; it waits, a newer push
// replaces it, and once the node reconnects the branch head is deployed
// exactly once. A push whose repository link changed meanwhile is dropped.
func TestRemoteWebhookWhileNodeOffline(t *testing.T) {
	r, node := newRemoteDeployRig(t)
	c := r.c
	if _, err := r.e.db.Exec(`UPDATE users SET role = 'admin'`); err != nil {
		t.Fatal(err)
	}
	var b botDTO
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "pending", "runtime": "nodejs",
		"node_id": remoteDeployNode, "github": map[string]any{"full_name": "o/r", "branch": "main", "auto_deploy": true}}), &b)
	base := "/api/v1/bots/" + b.ID
	r.waitDeploy(t, base, strings.Repeat("a", 40))
	_, cfg := r.gh.onlyHook()
	anon := &client{e: r.e}
	n := 0
	push := func(sha string) {
		t.Helper()
		n++
		body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": sha, "repository": map[string]any{"full_name": "o/r"}})
		resp, out := anon.req("POST", "/api/v1/webhooks/github", nil, func(req *http.Request) {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-GitHub-Event", "push")
			req.Header.Set("X-GitHub-Delivery", "pending-"+sha[:4]+string(rune('a'+n)))
			req.Header.Set("X-Hub-Signature-256", sign(cfg["secret"], body))
		})
		if resp.StatusCode != 202 {
			t.Fatalf("push: %d %s", resp.StatusCode, out)
		}
	}
	ops := func() int {
		var l struct{ Operations []opDTO }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/operations", nil), &l)
		return len(l.Operations)
	}
	pendingAt := func() float64 {
		var g struct{ Repo map[string]any }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/github", nil), &g)
		v, _ := g.Repo["pending_push_at_ms"].(float64)
		return v
	}
	waitPending := func() {
		t.Helper()
		for i := 0; i < 300 && pendingAt() == 0; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if pendingAt() == 0 {
			t.Fatal("push was not remembered")
		}
	}
	opsBefore := ops()

	node.mu.Lock()
	node.online = false
	begun := node.begun
	node.mu.Unlock()
	r.setHead(strings.Repeat("b", 40), map[string]string{"index.js": "v2"})
	push(strings.Repeat("b", 40))
	waitPending()
	r.setHead(strings.Repeat("c", 40), map[string]string{"index.js": "v3"})
	push(strings.Repeat("c", 40))
	time.Sleep(100 * time.Millisecond)
	r.svc.Wait()
	if got := ops(); got != opsBefore {
		t.Fatalf("offline pushes recorded %d operations", got-opsBefore)
	}
	var lastErr *string
	r.e.db.QueryRow(`SELECT last_error FROM github_repos WHERE bot_id = ?`, b.ID).Scan(&lastErr)
	if lastErr != nil {
		t.Fatalf("offline push recorded an error: %s", *lastErr)
	}
	// Still offline: nothing runs.
	if q := r.svc.RunPendingPushes(t.Context(), remoteDeployNode); q != 0 {
		t.Fatalf("ran %d pending pushes while offline", q)
	}

	node.mu.Lock()
	node.online = true
	node.mu.Unlock()
	if q := r.svc.RunPendingPushes(t.Context(), remoteDeployNode); q != 1 {
		t.Fatalf("ran %d pending pushes after reconnect", q)
	}
	r.waitDeploy(t, base, strings.Repeat("c", 40))
	if got := node.read(t, b.ID, "index.js"); got != "v3" {
		t.Fatalf("index.js = %q", got)
	}
	node.mu.Lock()
	ran := node.begun - begun
	node.mu.Unlock()
	if ran != 1 || ops() != opsBefore+1 || pendingAt() != 0 {
		t.Fatalf("pending pushes ran %d deployments, %d operations, pending %v", ran, ops()-opsBefore, pendingAt())
	}
	if q := r.svc.RunPendingPushes(t.Context(), remoteDeployNode); q != 0 {
		t.Fatalf("a pending push ran twice (%d)", q)
	}

	// The link changes while a push waits: it is dropped, not deployed.
	node.mu.Lock()
	node.online = false
	node.mu.Unlock()
	r.setHead(strings.Repeat("d", 40), map[string]string{"index.js": "v4"})
	push(strings.Repeat("d", 40))
	waitPending()
	r.e.db.Exec(`UPDATE github_repos SET root_dir = 'bot' WHERE bot_id = ?`, b.ID)
	if pendingAt() != 0 {
		t.Fatal("a push for the old link is still shown as pending")
	}
	node.mu.Lock()
	node.online = true
	node.mu.Unlock()
	if q := r.svc.RunPendingPushes(t.Context(), remoteDeployNode); q != 0 {
		t.Fatalf("a push for a changed link ran (%d)", q)
	}
	if op := r.waitOp(t, base); op.Status != "cancelled" || !strings.Contains(deref(op.Message), "link") {
		t.Fatalf("dropped push: %+v %s", op, deref(op.Message))
	}
	if got := node.read(t, b.ID, "index.js"); got != "v3" {
		t.Fatalf("index.js after a dropped push = %q", got)
	}
}
