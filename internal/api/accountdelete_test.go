package api

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
)

// Deleting an account removes it and everything it owns; other accounts are
// untouched, and the audit log keeps the event.
func TestDeleteAccount(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	ctx := t.Context()
	admin := e.user("root@x.io", domain.RoleAdmin)
	gone := e.user("gone@x.io", domain.RoleUser)
	other := e.user("other@x.io", domain.RoleUser)
	id := e.userID("gone@x.io")

	var wsr struct{ Workspace struct{ ID string } }
	var b1, b2, ob struct {
		ID string `json:"id"`
	}
	json.Unmarshal(gone.mustStatus(201, "POST", "/api/v1/workspaces", map[string]any{"name": "Team"}), &wsr)
	ws := wsr.Workspace
	gone.mustStatus(200, "PUT", "/api/v1/workspaces/"+ws.ID+"/members", map[string]any{"email": "other@x.io", "role": "viewer"})
	json.Unmarshal(gone.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "one", "runtime": "nodejs"}), &b1)
	json.Unmarshal(gone.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "two", "runtime": "nodejs", "workspace_id": ws.ID}), &b2)
	gone.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Docs"})
	json.Unmarshal(other.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "theirs", "runtime": "nodejs"}), &ob)
	// A repository link on another account's bot made with this account's token.
	if _, err := e.db.ExecContext(ctx, `INSERT INTO github_repos (bot_id,token_user_id,full_name,branch,webhook_secret_ciphertext,webhook_secret_nonce,webhook_secret_key_id,created_at_ms,updated_at_ms)
		VALUES (?,?,'me/repo','main',zeroblob(16),zeroblob(12),'k',1,1)`, ob.ID, id); err != nil {
		t.Fatal(err)
	}

	gone.mustStatus(403, "DELETE", "/api/v1/users/"+e.userID("other@x.io"), nil)
	admin.mustStatus(400, "DELETE", "/api/v1/users/"+e.userID("root@x.io"), nil)
	admin.mustStatus(204, "DELETE", "/api/v1/users/"+id, nil)
	admin.mustStatus(404, "DELETE", "/api/v1/users/"+id, nil)

	if _, err := e.db.GetUserByID(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("account still there: %v", err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM bots WHERE owner_id = '` + id + `'`,
		`SELECT count(*) FROM sites WHERE owner_id = '` + id + `'`,
		`SELECT count(*) FROM workspaces WHERE owner_id = '` + id + `'`,
		`SELECT count(*) FROM workspace_members WHERE workspace_id = '` + ws.ID + `'`,
		`SELECT count(*) FROM github_repos WHERE token_user_id = '` + id + `'`,
		`SELECT count(*) FROM sessions WHERE user_id = '` + id + `'`,
	} {
		var n int
		if err := e.db.QueryRowContext(ctx, q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s = %d %v", q, n, err)
		}
	}
	if resp, _ := gone.do("GET", "/api/v1/auth/me", nil); resp.StatusCode != 401 {
		t.Fatalf("deleted account still signed in: %d", resp.StatusCode)
	}
	other.mustStatus(200, "GET", "/api/v1/bots/"+ob.ID, nil)
	var target string
	e.db.QueryRowContext(ctx, `SELECT target FROM audit_events WHERE action = 'admin.user_delete' AND outcome = 'ok'`).Scan(&target)
	if target != "gone@x.io" {
		t.Fatalf("audit target %q", target)
	}
	// The address is free again.
	e.user("gone@x.io", domain.RoleUser)
}

// An account whose team workspace holds someone else's bot is refused before
// anything changes; administrators must be demoted first.
func TestDeleteAccountRefusals(t *testing.T) {
	r := newQuotaRig(t)
	e := r.e
	ctx := t.Context()
	admin := e.user("root@x.io", domain.RoleAdmin)
	owner := e.user("owner2@x.io", domain.RoleUser)
	dev := e.user("dev@x.io", domain.RoleUser)
	e.user("admin2@x.io", domain.RoleAdmin)
	id := e.userID("owner2@x.io")

	var wsr struct{ Workspace struct{ ID string } }
	var b struct {
		ID string `json:"id"`
	}
	json.Unmarshal(owner.mustStatus(201, "POST", "/api/v1/workspaces", map[string]any{"name": "Shared"}), &wsr)
	ws := wsr.Workspace
	owner.mustStatus(200, "PUT", "/api/v1/workspaces/"+ws.ID+"/members", map[string]any{"email": "dev@x.io", "role": "developer"})
	owner.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "mine", "runtime": "nodejs"})
	json.Unmarshal(dev.mustStatus(201, "POST", "/api/v1/bots", map[string]any{"name": "devs", "runtime": "nodejs", "workspace_id": ws.ID}), &b)

	expect400(t, admin, "DELETE", "/api/v1/users/"+id, nil, `workspace \"Shared\"`)
	u, err := e.db.GetUserByID(ctx, id)
	if err != nil || u.Disabled {
		t.Fatalf("refused deletion changed the account: %+v %v", u.Disabled, err)
	}
	if n, _, _ := e.db.OwnerUsage(ctx, id); n != 1 {
		t.Fatalf("refused deletion removed bots: %d", n)
	}
	expect400(t, admin, "DELETE", "/api/v1/users/"+e.userID("admin2@x.io"), nil, "change this account's role")

	// Once the other account's bot leaves, the deletion goes through.
	dev.mustStatus(204, "DELETE", "/api/v1/bots/"+b.ID, nil)
	admin.mustStatus(204, "DELETE", "/api/v1/users/"+id, nil)
}
