package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/service"
)

var testClock atomic.Int64

func backupEnv(t *testing.T) (*env, *service.BackupService, string) {
	e := newEnv(t)
	fm, err := filesystem.NewManager(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	dir := filepath.Join(t.TempDir(), "backups")
	// Interval and clock are set before Start: the service's goroutine reads them.
	bs := &service.BackupService{Bots: e.bots, Files: fm, Keys: e.bots.Keys, Dir: dir, Keep: 2, Interval: time.Hour,
		Now: func() time.Time { return time.UnixMilli(testClock.Load()) }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	testClock.Store(time.Now().UnixMilli())
	ctx, cancel := context.WithCancel(context.Background())
	if err := bs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); bs.Wait() })
	e.bots.OnDelete = bs.PurgeBot // as wired in main
	e.app = New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Auth: e.auth, Bots: e.bots, Files: fm, Backups: bs, SecureCookies: true})
	return e, bs, dir
}

func waitBackup(t *testing.T, c *client, base, id string) backupDTO {
	t.Helper()
	for i := 0; i < 200; i++ {
		var l struct{ Backups []backupDTO }
		json.Unmarshal(c.mustStatus(200, "GET", base+"/backups", nil), &l)
		for _, b := range l.Backups {
			if b.ID == id && b.Status != "creating" {
				return b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("backup did not finish")
	return backupDTO{}
}

func TestBackupLifecycle(t *testing.T) {
	e, bs, dir := backupEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	editor := e.user("ed@x.io", domain.RoleUser)
	admin := e.user("adm@x.io", domain.RoleUser)
	id := owner.createBot("My Bot")
	base := "/api/v1/bots/" + id
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "ed@x.io", "permissions": domain.PermEditFiles})
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "adm@x.io", "permissions": domain.PermFullAdmin})
	ws := filepath.Join(e.dataDir, id)
	os.WriteFile(filepath.Join(ws, "index.js"), []byte("v1"), 0o644)
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": "tok-v1", "MODE": "prod"}})

	// Create (asynchronous) and wait; the archive checksum and size are recorded.
	var bk backupDTO
	json.Unmarshal(editor.mustStatus(202, "POST", base+"/backups", nil), &bk)
	if bk.Status != "creating" || bk.Kind != "manual" || !bk.IncludesEnv {
		t.Fatalf("%+v", bk)
	}
	bk = waitBackup(t, owner, base, bk.ID)
	if bk.Status != "ready" || bk.SizeBytes == 0 || bk.SHA256 == nil || len(*bk.SHA256) != 64 {
		t.Fatalf("%+v", bk)
	}

	// Download: opaque gzip attachment, sealed (never plaintext) environment.
	resp, body := editor.do("GET", base+"/backups/"+bk.ID+"/download", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/gzip" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="My_Bot-`) || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		found[h.Name] = string(b)
	}
	if found["index.js"] != "v1" || !strings.Contains(found[".rivetpanel/env.json"], "DISCORD_TOKEN") {
		t.Fatalf("archive: %v", found)
	}
	if strings.Contains(found[".rivetpanel/env.json"], "tok-v1") || strings.Contains(string(body), "tok-v1") {
		t.Fatal("plaintext secret in the archive")
	}

	// Permissions: strangers see nothing, sub-users need edit-files, delete/restore need full admin.
	stranger := e.user("st@x.io", domain.RoleUser)
	stranger.mustStatus(404, "GET", base+"/backups", nil)
	viewer := e.user("vw@x.io", domain.RoleUser)
	owner.mustStatus(200, "PUT", base+"/users", map[string]any{"email": "vw@x.io", "permissions": domain.PermViewConsole})
	viewer.mustStatus(403, "GET", base+"/backups", nil)
	editor.mustStatus(403, "POST", base+"/backups/"+bk.ID+"/restore", nil)
	editor.mustStatus(403, "DELETE", base+"/backups/"+bk.ID, nil)

	// Change files and environment, then restore.
	os.WriteFile(filepath.Join(ws, "index.js"), []byte("v2"), 0o644)
	os.WriteFile(filepath.Join(ws, "extra.txt"), []byte("later"), 0o644)
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"DISCORD_TOKEN": "tok-v2", "NEWVAR": "x"}})

	// A running bot cannot be restored.
	e.db.Exec(`UPDATE bots SET observed_state = 'running', desired_state = 'running' WHERE id = ?`, id)
	admin.mustStatus(409, "POST", base+"/backups/"+bk.ID+"/restore", nil)
	e.db.Exec(`UPDATE bots SET observed_state = 'stopped', desired_state = 'stopped' WHERE id = ?`, id)

	before := botGen(t, owner, base)
	admin.mustStatus(200, "POST", base+"/backups/"+bk.ID+"/restore", nil)
	if b, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(b) != "v1" {
		t.Fatalf("index.js = %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "extra.txt")); err == nil {
		t.Fatal("post-backup file survived the restore")
	}
	envs, err := e.bots.DecryptEnv(context.Background(), id)
	if err != nil || envs["DISCORD_TOKEN"] != "tok-v1" || envs["MODE"] != "prod" || envs["NEWVAR"] != "" {
		t.Fatalf("env after restore: %v %v", envs, err)
	}
	if botGen(t, owner, base) <= before {
		t.Fatal("restore must advance the generation")
	}
	// A safety snapshot of the pre-restore state was taken and holds the v2 data.
	var l struct{ Backups []backupDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/backups", nil), &l)
	var pre *backupDTO
	for i := range l.Backups {
		if l.Backups[i].Kind == "pre_restore" {
			pre = &l.Backups[i]
		}
	}
	if pre == nil || pre.Status != "ready" {
		t.Fatalf("no pre_restore backup: %+v", l.Backups)
	}

	// Restore without the environment leaves current variables alone.
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"MODE": "changed"}})
	admin.mustStatus(200, "POST", base+"/backups/"+bk.ID+"/restore", map[string]any{"restore_env": false})
	if envs, _ := e.bots.DecryptEnv(context.Background(), id); envs["MODE"] != "changed" {
		t.Fatalf("restore_env=false touched the environment: %v", envs)
	}

	// Tampering is detected before anything is modified.
	path := filepath.Join(dir, id, bk.ID+".tar.gz")
	os.WriteFile(filepath.Join(ws, "index.js"), []byte("keep-me"), 0o644)
	orig, _ := os.ReadFile(path)
	os.WriteFile(path, append(append([]byte{}, orig...), 0), 0o600)
	admin.mustStatus(400, "POST", base+"/backups/"+bk.ID+"/restore", nil)
	if b, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(b) != "keep-me" {
		t.Fatal("a corrupted backup modified the workspace")
	}
	os.WriteFile(path, orig, 0o600)

	// Delete removes file and row; deleting the bot removes all its backups.
	admin.mustStatus(204, "DELETE", base+"/backups/"+bk.ID, nil)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("file left behind")
	}
	admin.mustStatus(404, "GET", base+"/backups/"+bk.ID+"/download", nil)
	owner.mustStatus(204, "DELETE", base, nil)
	if _, err := os.Stat(filepath.Join(dir, id)); err == nil {
		t.Fatal("bot deletion left its backups on disk")
	}
	_ = bs
}

type remoteArchiveTx struct {
	commit *filesystem.Commit
	w      *filesystem.Workspace
}

type remoteArchives struct {
	files *filesystem.Manager
	mu    sync.Mutex
	tx    map[string]remoteArchiveTx
}

func (r *remoteArchives) Backup(_ context.Context, _, botID string, extra map[string][]byte, limits filesystem.BackupLimits, dst io.Writer) error {
	w, err := r.files.Open(botID)
	if err != nil {
		return err
	}
	defer w.Close()
	_, _, err = w.WriteTarGz(dst, extra, limits)
	return err
}

func (r *remoteArchives) BeginRestore(_ context.Context, _, botID string, src io.Reader, limits filesystem.BackupLimits) (string, error) {
	w, err := r.files.Open(botID)
	if err != nil {
		return "", err
	}
	_, commit, err := w.RestoreTarGzCommit(src, limits)
	if err != nil {
		w.Close()
		return "", err
	}
	id := fmt.Sprintf("tx-%d", time.Now().UnixNano())
	r.mu.Lock()
	r.tx[id] = remoteArchiveTx{commit: commit, w: w}
	r.mu.Unlock()
	return id, nil
}

func (r *remoteArchives) CompleteRestore(_ context.Context, _, _ string, id string, commit bool) error {
	r.mu.Lock()
	tx, ok := r.tx[id]
	delete(r.tx, id)
	r.mu.Unlock()
	if !ok {
		return domain.ErrNotFound
	}
	defer tx.w.Close()
	if commit {
		return tx.commit.Finish()
	}
	return tx.commit.Rollback()
}

func TestRemoteBackupAndRestoreUseAssignedNode(t *testing.T) {
	e, bs, _ := backupEnv(t)
	owner := e.user("remote-backup@x.io", domain.RoleUser)
	id := owner.createBot("Remote backup")
	base := "/api/v1/bots/" + id

	remoteFiles, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remoteFiles.Close() })
	if err := remoteFiles.Create(id); err != nil {
		t.Fatal(err)
	}
	remoteDir, _ := remoteFiles.Path(id)
	if err := os.WriteFile(filepath.Join(remoteDir, "state.txt"), []byte("remote-v1"), 0o640); err != nil {
		t.Fatal(err)
	}
	remoteNode := "11111111-1111-4111-8111-111111111111"
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO nodes (id, location_id, name, transport, endpoint, enabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, 'Backup agent', 'agent', NULL, 1, ?, ?)`, remoteNode, domain.LocalLocationID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE bots SET node_id = ? WHERE id = ?`, remoteNode, id); err != nil {
		t.Fatal(err)
	}
	e.bots.RemoteNode = func(nodeID string) bool { return nodeID == remoteNode }
	bs.Remote = &remoteArchives{files: remoteFiles, tx: map[string]remoteArchiveTx{}}

	var created backupDTO
	json.Unmarshal(owner.mustStatus(http.StatusAccepted, http.MethodPost, base+"/backups", map[string]any{"include_env": false}), &created)
	created = waitBackup(t, owner, base, created.ID)
	if created.Status != "ready" {
		t.Fatalf("remote backup = %+v", created)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "state.txt"), []byte("remote-v2"), 0o640); err != nil {
		t.Fatal(err)
	}
	owner.mustStatus(http.StatusOK, http.MethodPost, base+"/backups/"+created.ID+"/restore", map[string]any{"restore_env": false})
	got, err := os.ReadFile(filepath.Join(remoteDir, "state.txt"))
	if err != nil || string(got) != "remote-v1" {
		t.Fatalf("remote restored file = %q, %v", got, err)
	}
	local, _ := os.ReadFile(filepath.Join(e.dataDir, id, "state.txt"))
	if string(local) == "remote-v1" {
		t.Fatal("remote restore wrote into the panel's local workspace")
	}
}

func botGen(t *testing.T, c *client, base string) int64 {
	var b botDTO
	json.Unmarshal(c.mustStatus(200, "GET", base, nil), &b)
	return b.Generation
}

func TestBackupCapAndScheduling(t *testing.T) {
	e, bs, dir := backupEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id

	// One at a time per bot.
	var first backupDTO
	json.Unmarshal(owner.mustStatus(202, "POST", base+"/backups", nil), &first)
	waitBackup(t, owner, base, first.ID)

	// Scheduled backups honor the per-bot switch, the interval and retention.
	other := owner.createBot("off")
	owner.mustStatus(200, "PATCH", "/api/v1/bots/"+other, map[string]any{"auto_backup": false})
	for i := 0; i < 4; i++ {
		bs.RunScheduledForTest(context.Background())
		testClock.Add(int64(2 * time.Hour / time.Millisecond))
	}
	var l struct{ Backups []backupDTO }
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/backups", nil), &l)
	auto := 0
	for _, b := range l.Backups {
		if b.Kind == "auto" {
			auto++
		}
	}
	if auto != 2 { // Keep = 2 of the 4 taken
		t.Fatalf("auto backups = %d, want 2 (retention)", auto)
	}
	json.Unmarshal(owner.mustStatus(200, "GET", "/api/v1/bots/"+other+"/backups", nil), &l)
	if len(l.Backups) != 0 {
		t.Fatal("bot with auto_backup off was backed up")
	}
	// Orphaned backup directories of deleted bots are swept.
	orphan := filepath.Join(dir, "99999999-0000-4000-8000-000000000000")
	os.MkdirAll(orphan, 0o700)
	bs.SweepOrphansForTest(context.Background())
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("orphan backup directory not swept")
	}

	// The per-bot cap is enforced.
	for i := 0; i < service.MaxBackupsPerBot; i++ {
		e.db.Exec(`INSERT INTO bot_backups (id, bot_id, kind, status, file_name, created_at_ms) VALUES (?,?,?,?,?,?)`,
			"fill-"+string(rune('a'+i%26))+string(rune('a'+i/26)), id, "manual", "ready", "fill"+string(rune('a'+i%26))+string(rune('a'+i/26))+".tar.gz", 1)
	}
	owner.mustStatus(400, "POST", base+"/backups", nil)
}

func TestRestoreWithUndecryptableEnvChangesNothing(t *testing.T) {
	e, _, dir := backupEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id
	ws := filepath.Join(e.dataDir, id)
	os.WriteFile(filepath.Join(ws, "index.js"), []byte("v1"), 0o644)
	owner.mustStatus(200, "PUT", base+"/env", map[string]any{"vars": map[string]string{"TOKEN": "t1"}})
	var bk backupDTO
	json.Unmarshal(owner.mustStatus(202, "POST", base+"/backups", nil), &bk)
	bk = waitBackup(t, owner, base, bk.ID)

	// Simulate a key mix-up: the stored ciphertext no longer opens with this server's key.
	arch := filepath.Join(dir, id, bk.ID+".tar.gz")
	data, _ := os.ReadFile(arch)
	corrupt := rewriteEnvSnapshot(t, data)
	os.WriteFile(arch, corrupt, 0o600)
	sum := sha256.Sum256(corrupt)
	e.db.Exec(`UPDATE bot_backups SET sha256_hex = ? WHERE id = ?`, hex.EncodeToString(sum[:]), bk.ID)

	os.WriteFile(filepath.Join(ws, "index.js"), []byte("KEEP-ME"), 0o644)
	owner.mustStatus(400, "POST", base+"/backups/"+bk.ID+"/restore", nil)
	if b, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(b) != "KEEP-ME" {
		t.Fatalf("a failed environment check still changed the files: %q", b)
	}
	// Files-only restore of the same archive is still possible.
	owner.mustStatus(200, "POST", base+"/backups/"+bk.ID+"/restore", map[string]any{"restore_env": false})
	if b, _ := os.ReadFile(filepath.Join(ws, "index.js")); string(b) != "v1" {
		t.Fatalf("files-only restore failed: %q", b)
	}
}

// rewriteEnvSnapshot returns the archive with garbage ciphertext in .rivetpanel/env.json.
func rewriteEnvSnapshot(t *testing.T, gzData []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gw)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		if h.Name == ".rivetpanel/env.json" {
			var snap map[string]any
			json.Unmarshal(b, &snap)
			for _, v := range snap["vars"].([]any) {
				v.(map[string]any)["ciphertext"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
			}
			b, _ = json.Marshal(snap)
			h.Size = int64(len(b))
		}
		tw.WriteHeader(h)
		tw.Write(b)
	}
	tw.Close()
	gw.Close()
	return out.Bytes()
}

func TestBackupLabelsVerificationAndReservedSlots(t *testing.T) {
	e, bs, dir := backupEnv(t)
	owner := e.user("own@x.io", domain.RoleUser)
	id := owner.createBot("b")
	base := "/api/v1/bots/" + id

	var b backupDTO
	json.Unmarshal(owner.mustStatus(202, "POST", base+"/backups", map[string]any{"label": "  before upgrade  "}), &b)
	b = waitBackup(t, owner, base, b.ID)
	if deref(b.Label) != "before upgrade" {
		t.Fatalf("label = %q", deref(b.Label))
	}
	owner.mustStatus(400, "PATCH", base+"/backups/"+b.ID, map[string]any{"label": strings.Repeat("x", 81)})
	json.Unmarshal(owner.mustStatus(200, "PATCH", base+"/backups/"+b.ID, map[string]any{"label": "renamed"}), &b)
	if deref(b.Label) != "renamed" {
		t.Fatalf("relabel = %q", deref(b.Label))
	}

	// Verification passes for a good archive and records damage otherwise.
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/backups/"+b.ID+"/verify", nil), &b)
	if b.VerifiedAtMS == nil || b.VerifyError != nil {
		t.Fatalf("verify ok: %+v", b)
	}
	os.WriteFile(filepath.Join(dir, id, b.ID+".tar.gz"), []byte("garbage"), 0o600)
	json.Unmarshal(owner.mustStatus(200, "POST", base+"/backups/"+b.ID+"/verify", nil), &b)
	if b.VerifyError == nil {
		t.Fatal("a damaged backup verified as good")
	}

	// Health is reported with the list.
	var l struct {
		Backups []backupDTO
		Health  map[string]any
	}
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/backups", nil), &l)
	if l.Health["last_success_ms"].(float64) == 0 || l.Health["manual_limit"].(float64) != float64(service.MaxBackupsPerBot-5) {
		t.Fatalf("health: %v", l.Health)
	}

	// Manual backups cannot take the slots reserved for scheduled ones...
	for i := 1; i < service.MaxBackupsPerBot-5; i++ {
		n := fmt.Sprintf("fill-%03d", i)
		e.db.Exec(`INSERT INTO bot_backups (id, bot_id, kind, status, file_name, created_at_ms) VALUES (?,?,?,?,?,?)`, n, id, "manual", "ready", n+".tar.gz", 1+i)
	}
	owner.mustStatus(400, "POST", base+"/backups", nil)
	// ...and a full budget makes scheduled backups replace their own oldest copy.
	for i := 0; i < 5; i++ {
		n := fmt.Sprintf("auto-%03d", i)
		e.db.Exec(`INSERT INTO bot_backups (id, bot_id, kind, status, file_name, created_at_ms) VALUES (?,?,?,?,?,?)`, n, id, "auto", "ready", n+".tar.gz", 100+i)
	}
	testClock.Add(int64(3 * time.Hour / time.Millisecond))
	bs.RunScheduledForTest(context.Background())
	json.Unmarshal(owner.mustStatus(200, "GET", base+"/backups", nil), &l)
	newest := l.Backups[0]
	if newest.Kind != "auto" || newest.Status != "ready" {
		t.Fatalf("scheduled backup at the cap: %+v", newest)
	}
	for _, x := range l.Backups {
		if x.ID == "auto-000" {
			t.Fatal("the oldest scheduled backup should have made room")
		}
	}
}
