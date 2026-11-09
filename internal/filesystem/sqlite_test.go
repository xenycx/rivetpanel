package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindSQLite(t *testing.T) {
	w, dir := setup(t)
	root := filepath.Join(dir, "bots", id)
	db := append([]byte("SQLite format 3\x00"), make([]byte, 100)...)
	write := func(p string, b []byte) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("volumes/data/vault.db", db)
	write("app.sqlite3", db)
	write("notes.db", []byte("not a database, just text")) // wrong header
	write("data.bin", db)                                  // no database extension
	write("node_modules/pkg/test.db", db)                  // dependency folder
	write("volumes/data/vault.db-wal", db)                 // WAL sidecar
	if err := os.Symlink(filepath.Join(root, "app.sqlite3"), filepath.Join(root, "link.db")); err != nil {
		t.Fatal(err)
	}
	got, err := w.FindSQLite(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "app.sqlite3" || got[1].Path != "volumes/data/vault.db" || got[1].Size != int64(len(db)) {
		t.Fatalf("%+v", got)
	}
	if got, _ := w.FindSQLite(1); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}

	rel, uid, gid, err := w.SQLiteOwner("./volumes/data/vault.db")
	if err != nil || rel != "volumes/data/vault.db" || uid != os.Getuid() || gid != os.Getgid() {
		t.Fatalf("%q %d %d %v", rel, uid, gid, err)
	}
	if _, _, _, err := w.SQLiteOwner("notes.db"); !errors.Is(err, ErrNotSQLite) {
		t.Fatalf("notes.db: %v", err)
	}
	if _, _, _, err := w.SQLiteOwner("link.db"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("a symbolic link must be refused: %v", err)
	}
	for _, bad := range []string{"../x.db", "/etc/x.db", ""} {
		if _, _, _, err := w.SQLiteOwner(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
