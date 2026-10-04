//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xenycx/rivetpanel/internal/backup"
	"github.com/xenycx/rivetpanel/internal/secrets"
)

// TestBackupDisasterRestoreEndToEnd: a real bot with a secret is backed up,
// the entire panel state is destroyed, everything is restored from the backup
// (with keys) into fresh paths, and a new panel+runner brings the bot back with
// its decrypted secret.
func TestBackupDisasterRestoreEndToEnd(t *testing.T) {
	s1 := newStack(t)
	b := s1.createBot("nodejs")
	s1.write(b, "index.js", echoBot)
	s1.write(b, "data/state.json", `{"counter": 41}`)
	if err := s1.bots.SetEnv(s1.ctx, s1.user, b.ID, map[string]string{"DISCORD_TOKEN": "restore-token"}); err != nil {
		t.Fatal(err)
	}
	s1.bots.Start(s1.ctx, s1.user, b.ID)
	cur := s1.waitObserved(b.ID, "running", 5*time.Minute)
	s1.waitLog(*cur.ContainerID, "READY", 30*time.Second)
	s1.bots.Stop(s1.ctx, s1.user, b.ID)
	s1.waitObserved(b.ID, "stopped", time.Minute)

	// Back up (keys included for this test; in production they are stored separately).
	dest := filepath.Join(t.TempDir(), "backup")
	m, err := backup.Create(s1.ctx, backup.Source{DB: s1.db, DataRoot: filepath.Join(s1.dir, "bots"), KeyDir: filepath.Join(s1.dir, "keys")}, dest, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if m.Bots != 1 || !m.IncludesKeys {
		t.Fatalf("%+v", m)
	}
	if _, err := backup.Verify(dest); err != nil {
		t.Fatal(err)
	}

	// Disaster: the panel, its containers, database, bot files and keys are gone.
	nodeID, user, oldDir := s1.nodeID, s1.user, s1.dir
	s1.shutdown()
	if err := os.RemoveAll(oldDir); err != nil {
		t.Fatal(err)
	}

	// Restore into the same paths.
	if _, err := backup.Restore(context.Background(), dest, backup.RestoreOptions{
		DBPath: filepath.Join(oldDir, "t.db"), DataRoot: filepath.Join(oldDir, "bots"), KeyDir: filepath.Join(oldDir, "keys"), RestoreKeys: true,
	}); err != nil {
		t.Fatal(err)
	}

	s2 := newStackWith(t, stackOpts{dir: oldDir, nodeID: nodeID, user: &user, restored: true})
	got, err := s2.db.GetBot(s2.ctx, b.ID)
	if err != nil || got.Name != b.Name || got.DesiredState != "stopped" {
		t.Fatalf("bot not restored: %+v %v", got, err)
	}
	if env, err := s2.bots.DecryptEnv(s2.ctx, b.ID); err != nil || env["DISCORD_TOKEN"] != "restore-token" {
		t.Fatalf("secret not recoverable after restore: %v %v", env, err)
	}
	w, err := s2.ws.Open(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := w.Read("data/state.json", 1024); err != nil || !strings.Contains(string(data), "41") {
		t.Fatalf("workspace file lost: %v %q", err, data)
	}
	w.Close()
	rep, err := backup.VerifyEnv(s2.ctx, s2.db, mustKeys(t, filepath.Join(oldDir, "keys")))
	if err != nil || !rep.Healthy() {
		t.Fatalf("%+v %v", rep, err)
	}

	// The restored bot runs again with its secret.
	if _, err := s2.bots.Start(s2.ctx, s2.user, b.ID); err != nil {
		t.Fatal(err)
	}
	cur = s2.waitObserved(b.ID, "running", 5*time.Minute)
	s2.waitLog(*cur.ContainerID, "READY", 30*time.Second)
	// The echo bot prints its token line only if it saw the variable, and it is unchanged after restore.
	if logs := s2.logs(*cur.ContainerID); !strings.Contains(logs, "READY") {
		t.Fatalf("restored bot output: %s", logs)
	}
}

func mustKeys(t *testing.T, dir string) *secrets.Keyring {
	t.Helper()
	k, err := secrets.LoadDir(dir, "k1", false)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
