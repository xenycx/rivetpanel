package filesystem

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const id = "6f1c0a52-3b7e-4d0e-9a41-0c5b7d2e8f10"

func setup(t *testing.T) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if err := m.Create(id); err != nil {
		t.Fatal(err)
	}
	w, err := m.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}

func TestBotIDValidation(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	for _, bad := range []string{"", "..", "../x", "a/b", strings.ToUpper(id), "{" + id + "}", id + "/x"} {
		if err := m.Create(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("%q: %v", bad, err)
		}
		if _, err := m.Open(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("open %q: %v", bad, err)
		}
	}
	if err := m.Create(id); err != nil {
		t.Fatal(err)
	}
	if err := m.Create(id); err == nil {
		t.Fatal("duplicate create must fail")
	}
	if err := m.Remove(uuid.NewString()); err != nil {
		t.Fatal("removing missing workspace should be a no-op:", err)
	}
}

func TestWriteReadListRemove(t *testing.T) {
	w, _ := setup(t)
	if err := w.Write("src/main.js", strings.NewReader("hello"), 100); err != nil {
		t.Fatal(err)
	}
	b, err := w.Read("src/main.js", 100)
	if err != nil || string(b) != "hello" {
		t.Fatal(err, b)
	}
	if err := w.Write("src/main.js", strings.NewReader("v2"), 100); err != nil {
		t.Fatal(err)
	}
	b, _ = w.Read("src/main.js", 100)
	if string(b) != "v2" {
		t.Fatal("replace failed")
	}
	es, _ := w.List("src")
	if len(es) != 1 || es[0].Name != "main.js" || es[0].Size != 2 {
		t.Fatalf("list = %+v (temp file leaked?)", es)
	}
	if err := w.Remove("src"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Read("src/main.js", 100); err == nil {
		t.Fatal("still readable")
	}
}

func TestSizeLimits(t *testing.T) {
	w, _ := setup(t)
	w.Write("f", strings.NewReader("ok"), 10)
	if err := w.Write("f", bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if b, _ := w.Read("f", 10); string(b) != "ok" {
		t.Fatal("failed write corrupted original")
	}
	es, _ := w.List(".")
	if len(es) != 1 {
		t.Fatalf("temp file leaked: %+v", es)
	}
	if _, err := w.Read("f", 1); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	w, _ := setup(t)
	for _, p := range []string{"", "..", "../x", "a/../../x", "/etc/passwd", "a\\b", "a\x00b"} {
		if _, err := w.Read(p, 10); err == nil {
			t.Errorf("read %q allowed", p)
		}
		if err := w.Write(p, strings.NewReader("x"), 10); err == nil {
			t.Errorf("write %q allowed", p)
		}
		if err := w.Remove(p); err == nil {
			t.Errorf("remove %q allowed", p)
		}
	}
}

func TestSymlinkEscapeBlocked(t *testing.T) {
	w, dir := setup(t)
	secret := filepath.Join(dir, "secret.txt")
	os.WriteFile(secret, []byte("top secret"), 0o600)
	outsideDir := filepath.Join(dir, "outside")
	os.Mkdir(outsideDir, 0o750)
	ws := filepath.Join(dir, "bots", id)
	os.Symlink(secret, filepath.Join(ws, "link-file"))
	os.Symlink(outsideDir, filepath.Join(ws, "link-dir"))
	os.Symlink("../"+id+"/../../secret.txt", filepath.Join(ws, "link-rel"))

	if b, err := w.Read("link-file", 100); err == nil {
		t.Fatalf("read through symlink: %q", b)
	}
	if _, err := w.Read("link-rel", 100); err == nil {
		t.Fatal("relative symlink escape")
	}
	if err := w.Write("link-dir/pwn", strings.NewReader("x"), 10); err == nil {
		t.Fatal("wrote through directory symlink")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "pwn")); err == nil {
		t.Fatal("file created outside workspace")
	}
	// Writing onto a symlink must replace the link, never follow it.
	if err := w.Write("link-file", strings.NewReader("mine"), 10); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(secret); string(b) != "top secret" {
		t.Fatal("symlink target modified")
	}
	if err := w.Remove("link-dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outsideDir); err != nil {
		t.Fatal("remove followed symlink")
	}
}

func TestWorkspaceEntryMustNotBeSymlink(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(filepath.Join(dir, "bots"))
	other := filepath.Join(dir, "other")
	os.Mkdir(other, 0o750)
	os.Symlink(other, filepath.Join(dir, "bots", id))
	if _, err := m.Open(id); err == nil {
		t.Fatal("opened symlinked workspace")
	}
}

func TestRemoveHandlesReadOnlyTrees(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	id := "7b8f6c1e-2d3a-4e5b-9c7d-0a1b2c3d4e5f"
	if err := m.Create(id); err != nil {
		t.Fatal(err)
	}
	p, _ := m.Path(id)
	ro := filepath.Join(p, ".gopath", "pkg", "mod", "example.com", "x@v1.0.0")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ro, "x.go"), []byte("package x"), 0o444)
	// Like Go's module cache: read-only files in read-only directories.
	for d := ro; d != p; d = filepath.Dir(d) {
		os.Chmod(d, 0o555)
	}
	if err := m.Remove(id); err != nil {
		t.Fatalf("remove read-only tree: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("workspace still exists")
	}
}

func TestForeignOwnedAndRepair(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs an unprivileged user")
	}
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	const id = "11111111-2222-3333-4444-555555555555"
	if err := m.Create(id); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if ids, err := m.ForeignOwned(uid, gid); err != nil || len(ids) != 0 {
		t.Fatalf("own workspace reported foreign: %v %v", ids, err)
	}
	if ids, _ := m.ForeignOwned(65532, 65532); len(ids) != 1 || ids[0] != id {
		t.Fatalf("foreign = %v", ids)
	}
	if n, failed, err := m.RepairOwnership(uid, gid); err != nil || n != 0 || len(failed) != 0 {
		t.Fatalf("repair own: %d %v %v", n, failed, err)
	}
	// An unprivileged process cannot hand files to another user: the repair
	// reports the workspace, and Prepare explains what to run.
	if _, failed, _ := m.RepairOwnership(65532, 65532); len(failed) != 1 {
		t.Fatalf("failed = %v", failed)
	}
	if err := m.Prepare(id, 65532, 65532); err == nil || !strings.Contains(err.Error(), "CAP_CHOWN") {
		t.Fatalf("prepare error = %v", err)
	}
}
