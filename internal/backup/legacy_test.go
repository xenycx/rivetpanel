package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xenycx/rivetpanel/internal/legacy"
)

// A backup written by the former product is recognised and refused with an
// explanation instead of a missing-checksum error.
func TestLegacyBackupIsRefusedClearly(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"version":1,"created_at_ms":1,"bots":0,"includes_keys":false,"key_ids":["k1"],"sha256":{"` + legacy.DBName + `":"00","bots.tar.gz":"00"}}`
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacy.DBName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); !errors.Is(err, ErrLegacyBackup) {
		t.Fatalf("Verify = %v, want ErrLegacyBackup", err)
	}
	dst := t.TempDir()
	_, err := Restore(context.Background(), dir, RestoreOptions{DBPath: filepath.Join(dst, "rivetpanel.db"), DataRoot: filepath.Join(dst, "bots"), KeyDir: filepath.Join(dst, "keys")})
	if !errors.Is(err, ErrLegacyBackup) {
		t.Fatalf("Restore = %v, want ErrLegacyBackup", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "rivetpanel.db")); !os.IsNotExist(err) {
		t.Fatal("a database was written for a legacy backup")
	}
}
