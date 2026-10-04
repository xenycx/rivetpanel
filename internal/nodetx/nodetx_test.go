package nodetx

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/filesystem"
)

var limits = filesystem.BackupLimits{MaxEntries: 100, MaxBytes: 8 << 20, MaxFile: 4 << 20}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "o-r-abc/", Typeflag: tar.TypeDir, Mode: 0o755})
	for n, c := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "o-r-abc/" + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type rig struct {
	t     *testing.T
	files *filesystem.Manager
	bot   string
	reg   *Registry
}

func newRig(t *testing.T, ttl time.Duration) *rig {
	t.Helper()
	files, err := filesystem.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	bot := uuid.NewString()
	if err := files.Create(bot); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, files: files, bot: bot, reg: New(slog.New(slog.NewTextHandler(io.Discard, nil)), ttl)}
	r.write("index.js", "old")
	return r
}

func (r *rig) path(name string) string {
	dir, err := r.files.Path(r.bot)
	if err != nil {
		r.t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

func (r *rig) write(name, v string) {
	if err := os.WriteFile(r.path(name), []byte(v), 0o640); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read(name string) string {
	b, err := os.ReadFile(r.path(name))
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *rig) begin(archive []byte) (string, error) {
	w, err := r.files.Open(r.bot)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.reg.BeginDeploy(r.bot, w, func(gate func() error) (int, *filesystem.Commit, error) {
		return w.DeployTarGzCommit(bytes.NewReader(archive), "", limits, gate)
	})
}

func TestDeployIsStagedUntilApplied(t *testing.T) {
	r := newRig(t, time.Minute)
	archive := tarball(t, map[string]string{"index.js": "new", "lib/a.js": "a"})

	id, err := r.begin(archive)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("staging changed the workspace: %q", got)
	}
	if _, err := r.begin(archive); !errors.Is(err, ErrBusy) {
		t.Fatalf("a concurrent transaction = %v, want ErrBusy", err)
	}
	if err := r.reg.Complete(r.bot, id, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("committing an unapplied deployment = %v", err)
	}
	if _, err := r.reg.Apply("other-bot", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("applying another server's transaction = %v", err)
	}
	n, err := r.reg.Apply(r.bot, id)
	if err != nil || n != 2 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if got := r.read("index.js"); got != "new" {
		t.Fatalf("applied file = %q", got)
	}
	if _, err := r.reg.Apply(r.bot, id); !errors.Is(err, ErrConflict) {
		t.Fatalf("second apply = %v", err)
	}
	if err := r.reg.Complete(r.bot, id, true); err != nil {
		t.Fatal(err)
	}
	if err := r.reg.Complete(r.bot, id, true); err != nil {
		t.Fatalf("a retried confirmation must succeed: %v", err)
	}
	if err := r.reg.Complete(r.bot, id, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("an opposite confirmation = %v", err)
	}
	if got := r.read("index.js"); got != "new" || r.reg.Open() != 0 {
		t.Fatalf("committed file = %q, open = %d", got, r.reg.Open())
	}
}

func TestDeployAbandonedOrRolledBack(t *testing.T) {
	r := newRig(t, time.Minute)
	archive := tarball(t, map[string]string{"index.js": "new"})

	// Abandoned while staged: nothing ever changes.
	id, err := r.begin(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reg.Complete(r.bot, id, false); err != nil {
		t.Fatal(err)
	}
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("abandoned deployment changed files: %q", got)
	}

	// Rolled back after the swap (the panel's database update failed).
	id, err = r.begin(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.reg.Apply(r.bot, id); err != nil {
		t.Fatal(err)
	}
	if err := r.reg.Complete(r.bot, id, false); err != nil {
		t.Fatal(err)
	}
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("rolled back file = %q", got)
	}

	// An invalid archive fails before staging and frees the server.
	if _, err := r.begin([]byte("not gzip")); err == nil {
		t.Fatal("an invalid archive was accepted")
	}
	var ae *filesystem.ErrArchive
	if _, err := r.begin(tarball(t, map[string]string{})); !errors.As(err, &ae) {
		t.Fatalf("an empty repository = %v", err)
	}
	if r.reg.Open() != 0 {
		t.Fatalf("failed deployments left %d transactions", r.reg.Open())
	}
}

func TestUnconfirmedTransactionsExpire(t *testing.T) {
	r := newRig(t, 100*time.Millisecond)
	archive := tarball(t, map[string]string{"index.js": "new"})

	// A swapped deployment the panel never confirms is rolled back.
	id, err := r.begin(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.reg.Apply(r.bot, id); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, r.reg)
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("expired deployment kept its files: %q", got)
	}
	if err := r.reg.Complete(r.bot, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("confirming an expired transaction = %v", err)
	}

	// A staged deployment the panel never applies is abandoned.
	if _, err := r.begin(archive); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, r.reg)
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("expired staged deployment changed files: %q", got)
	}
}

func TestCrashedNodeRecoversPreviousFiles(t *testing.T) {
	r := newRig(t, time.Minute)
	id, err := r.begin(tarball(t, map[string]string{"index.js": "new"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.reg.Apply(r.bot, id); err != nil {
		t.Fatal(err)
	}
	// The agent process dies before the panel confirms: on its next start
	// the journal returns the previous files.
	rolled, err := r.files.Recover()
	if err != nil || len(rolled) != 1 || rolled[0] != r.bot {
		t.Fatalf("recover = %v, %v", rolled, err)
	}
	if got := r.read("index.js"); got != "old" {
		t.Fatalf("recovered file = %q", got)
	}
}

func waitClosed(t *testing.T, reg *Registry) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if reg.Open() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transaction did not expire")
}
