package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "former")
	t.Cleanup(func() { dataDir = DataDir })
	db := filepath.Join(dir, "rivetpanel.db")
	if got := Detect([]string{"RIVET_ENV=production", "PATH=/bin"}, db); len(got) != 0 {
		t.Fatalf("clean install reported %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, DBName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	got := Detect([]string{"BOTPANEL_LISTEN=:8080", "BOTFORGE_KEY_DIR=/x", "RIVET_ENV=production"}, db)
	if len(got) != 3 {
		t.Fatalf("findings = %v", got)
	}
	if !strings.Contains(got[0].What, "BOTFORGE_KEY_DIR, BOTPANEL_LISTEN") || !strings.Contains(got[1].What, "former data directory") || !strings.Contains(got[2].What, DBName) {
		t.Fatalf("findings = %v", got)
	}
}

func TestIsBackup(t *testing.T) {
	dir := t.TempDir()
	if IsBackup(dir, map[string]string{"rivetpanel.db": "x"}, "rivetpanel.db") {
		t.Fatal("current backup reported as legacy")
	}
	if !IsBackup(dir, map[string]string{DBName: "x"}, "rivetpanel.db") {
		t.Fatal("legacy manifest not recognised")
	}
	if IsBackup(dir, map[string]string{}, "rivetpanel.db") {
		t.Fatal("empty manifest reported as legacy")
	}
}
