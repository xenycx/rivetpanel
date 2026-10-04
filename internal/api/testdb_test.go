package api

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/xenycx/rivetpanel/internal/migrations"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

// Migrating a fresh database is the most expensive part of a test's setup
// (dozens of migrations, many times slower under -race). The package migrates
// one template database once and every test starts from a copy of it.
var (
	templateDir  string
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rivetpanel-api-test-")
	if err != nil {
		panic(err)
	}
	templateDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func buildTemplateDB() (string, error) {
	ctx := context.Background()
	p := filepath.Join(templateDir, "template.db")
	db, err := sqlite.Open(ctx, p, 1)
	if err != nil {
		return "", err
	}
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		db.Close()
		return "", err
	}
	db.EnsureLocalNode(ctx)
	// Fold the WAL into the main file so the file alone is the database.
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		return "", err
	}
	return p, db.Close()
}

// openTestDB opens a migrated copy of the template database at path.
func openTestDB(t *testing.T, path string) *sqlite.DB {
	t.Helper()
	templateOnce.Do(func() { templatePath, templateErr = buildTemplateDB() })
	if templateErr != nil {
		t.Fatal(templateErr)
	}
	src, err := os.Open(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = io.Copy(dst, src)
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
	}
	src.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, path, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// A no-op on the copy; kept so the schema check still runs per test.
	if err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return db
}
