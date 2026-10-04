package sftpd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// fakeNode stands in for noderoute.Router with the agent's file semantics
// (the real wire is covered in internal/noderoute). It only serves botR on
// nodeR and counts every call.
type fakeNode struct {
	files *filesystem.Manager
	calls atomic.Int64
	// beforeRemove, when set, runs just before a delete reaches the node
	// (to add a file concurrently with an rmdir).
	beforeRemove func()
}

func (n *fakeNode) ws(nodeID, botID string) (*filesystem.Workspace, error) {
	n.calls.Add(1)
	if nodeID != nodeR || botID != botR {
		return nil, errors.New("wrong node or bot")
	}
	return n.files.Open(botID)
}

func (n *fakeNode) ListDir(_ context.Context, nodeID, botID, dir string) ([]filesystem.Entry, error) {
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return w.List(dir)
}

func (n *fakeNode) ReadFileTo(_ context.Context, nodeID, botID, p string, max int64, dst io.Writer) (string, int64, error) {
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return "", 0, err
	}
	defer w.Close()
	f, size, err := w.OpenFile(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if size > max {
		return "", 0, filesystem.ErrTooLarge
	}
	rev, _ := filesystem.RevisionOf(f)
	c, err := io.Copy(dst, f)
	return rev, c, err
}

func (n *fakeNode) WriteFileFrom(_ context.Context, nodeID, botID, p string, src io.Reader, size int64, ifMatch string, createOnly bool) (string, error) {
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return "", err
	}
	defer w.Close()
	cur, err := w.Revision(p)
	exists := err == nil
	if (createOnly && exists) || (ifMatch != "" && (!exists || cur != ifMatch)) {
		return "", domain.ErrConflict
	}
	if err := w.Write(p, io.LimitReader(src, size), size); err != nil {
		return "", err
	}
	return w.Revision(p)
}

func (n *fakeNode) MakeDir(_ context.Context, nodeID, botID, p string) error {
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Mkdir(p)
}

func (n *fakeNode) Move(_ context.Context, nodeID, botID, from, to string) error {
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Rename(from, to)
}

func (n *fakeNode) RemoveOne(_ context.Context, nodeID, botID, p string) error {
	if n.beforeRemove != nil {
		n.beforeRemove()
	}
	w, err := n.ws(nodeID, botID)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.RemoveOne(p)
}

const remoteDir = "/Remote-dddddddd"

func (r *rig) panelDecoyIntact() {
	r.t.Helper()
	panel := filepath.Join(r.dir, "bots", botR)
	es, err := os.ReadDir(panel)
	if err != nil {
		r.t.Fatal(err)
	}
	if len(es) != 1 || es[0].Name() != "decoy.txt" {
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		r.t.Fatalf("the panel-local directory of a remote bot was changed: %v", names)
	}
	if b, _ := os.ReadFile(filepath.Join(panel, "decoy.txt")); string(b) != "stale panel copy" {
		r.t.Fatalf("decoy modified: %q", b)
	}
}

func (r *rig) nodeFile(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(r.dir, "node", botR, rel))
	return string(b), err
}

// TestRemoteBotNeverUsesPanelDisk is the regression test for SFTP serving a
// stale panel-local directory for a bot that runs on a remote node.
func TestRemoteBotNeverUsesPanelDisk(t *testing.T) {
	r := newRig(t, 1<<20)
	c := r.must("alice@x.io")

	infos, err := c.ReadDir(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("remote bot listing shows %d entries, want the node's (empty) workspace", len(infos))
	}
	if _, err := c.Stat(remoteDir + "/decoy.txt"); err == nil {
		t.Fatal("stat found the panel-local decoy")
	}
	if f, err := c.Open(remoteDir + "/decoy.txt"); err == nil {
		b, _ := io.ReadAll(f)
		f.Close()
		t.Fatalf("read the panel-local decoy: %q", b)
	}
	if err := c.Remove(remoteDir + "/decoy.txt"); err == nil {
		t.Fatal("removed a file that only exists on the panel")
	}
	if err := c.Rename(remoteDir+"/decoy.txt", remoteDir+"/moved.txt"); err == nil {
		t.Fatal("renamed a file that only exists on the panel")
	}
	f, err := c.Create(remoteDir + "/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("on the node"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := r.nodeFile("new.txt"); err != nil || got != "on the node" {
		t.Fatalf("node file %q %v", got, err)
	}
	if err := c.Mkdir(remoteDir + "/d"); err != nil {
		t.Fatal(err)
	}
	r.panelDecoyIntact()

	// Offline node: a clean error, and still never the panel's disk.
	r.sw.offline.Store(true)
	before := r.node.calls.Load()
	if _, err := c.ReadDir(remoteDir); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline list: %v", err)
	}
	if _, err := c.Open(remoteDir + "/decoy.txt"); err == nil {
		t.Fatal("offline read succeeded")
	}
	if _, err := c.Create(remoteDir + "/x.txt"); err == nil {
		t.Fatal("offline create succeeded")
	}
	if err := c.Mkdir(remoteDir + "/e"); err == nil {
		t.Fatal("offline mkdir succeeded")
	}
	if r.node.calls.Load() != before {
		t.Fatal("the node was called while it is offline")
	}
	r.sw.offline.Store(false)
	r.panelDecoyIntact()
}

// TestRemoteNotConfiguredFailsClosed: a server without the remote check
// refuses every bot instead of guessing it is local.
func TestRemoteNotConfiguredFailsClosed(t *testing.T) {
	h := &handler{s: &Server{}}
	if _, remote, err := h.remoteOf(target{bot: domain.Bot{ID: botA}}); !remote || err == nil {
		t.Fatalf("remote=%v err=%v", remote, err)
	}
}

func TestRemoteFileOperations(t *testing.T) {
	r := newRig(t, 64)
	c := r.must("alice@x.io")

	put := func(p, data string) error {
		f, err := c.Create(p)
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(data)); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	if err := c.Mkdir(remoteDir + "/src"); err != nil {
		t.Fatal(err)
	}
	if err := c.Mkdir(remoteDir + "/src"); err == nil {
		t.Fatal("mkdir of an existing directory succeeded")
	}
	if err := c.Mkdir(remoteDir + "/a/b"); err == nil {
		t.Fatal("mkdir created missing parents")
	}
	if err := put(remoteDir+"/src/a.txt", "hello world"); err != nil {
		t.Fatal(err)
	}
	st, err := c.Stat(remoteDir + "/src/a.txt")
	if err != nil || st.Size() != 11 || st.IsDir() {
		t.Fatalf("stat %v %v", st, err)
	}
	rf, err := c.Open(remoteDir + "/src/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rf)
	rf.Close()
	if string(b) != "hello world" {
		t.Fatalf("read %q", b)
	}

	// A write without truncation keeps the rest of the file.
	wf, err := c.OpenFile(remoteDir+"/src/a.txt", os.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.WriteAt([]byte("HELLO"), 0); err != nil {
		t.Fatal(err)
	}
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.nodeFile("src/a.txt"); got != "HELLO world" {
		t.Fatalf("partial write: %q", got)
	}

	// Exclusive create of an existing file is refused.
	if _, err := c.OpenFile(remoteDir+"/src/a.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL); err == nil {
		t.Fatal("exclusive create of an existing file")
	}

	// rmdir refuses a non-empty directory (the node deletes non-recursively).
	if err := c.RemoveDirectory(remoteDir + "/src"); err == nil {
		t.Fatal("rmdir removed a non-empty directory")
	}
	if _, err := r.nodeFile("src/a.txt"); err != nil {
		t.Fatal("file lost after a refused rmdir:", err)
	}
	if err := c.Remove(remoteDir + "/src"); err == nil {
		t.Fatal("remove deleted a directory")
	}

	// Rename stays inside the bot.
	if err := c.Rename(remoteDir+"/src/a.txt", remoteDir+"/b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename(remoteDir+"/b.txt", "/My_Bot-aaaaaaaa/b.txt"); err == nil {
		t.Fatal("cross-bot rename accepted")
	}
	if err := c.Rename("/My_Bot-aaaaaaaa", remoteDir+"/x"); err == nil {
		t.Fatal("renamed a bot directory")
	}
	if err := c.Remove(remoteDir + "/b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory(remoteDir + "/src"); err != nil {
		t.Fatal(err)
	}

	// Per-file size cap: nothing reaches the node.
	big := put(remoteDir+"/big.bin", string(bytes.Repeat([]byte("z"), 200)))
	if big == nil {
		t.Fatal("write past the size cap succeeded")
	}
	if _, err := r.nodeFile("big.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("oversized file reached the node: %v", err)
	}

	// Writes and changes wait while a deployment or restore holds the bot.
	if err := put(remoteDir+"/keep.txt", "k"); err != nil {
		t.Fatal(err)
	}
	r.sw.blocked.Store(true)
	if err := put(remoteDir+"/y.txt", "y"); err == nil {
		t.Fatal("create during a restore")
	}
	if err := c.Remove(remoteDir + "/keep.txt"); err == nil {
		t.Fatal("remove during a restore")
	}
	if err := c.Mkdir(remoteDir + "/z"); err == nil {
		t.Fatal("mkdir during a restore")
	}
	r.sw.blocked.Store(false)

	// A handle opened before the lock cannot finish its upload under it.
	wf, err = c.Create(remoteDir + "/late.txt")
	if err != nil {
		t.Fatal(err)
	}
	wf.Write([]byte("late"))
	r.sw.blocked.Store(true)
	if err := wf.Close(); err == nil {
		t.Fatal("upload committed during a restore")
	}
	r.sw.blocked.Store(false)
	if _, err := r.nodeFile("late.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("blocked upload reached the node: %v", err)
	}

	// Read-only users and other accounts see nothing of the remote bot.
	bob := r.must("bob@x.io")
	if _, err := bob.ReadDir(remoteDir); err == nil {
		t.Fatal("bob listed alice's remote bot")
	}

	// Spool files never stay on the panel.
	if es, _ := os.ReadDir(filepath.Join(r.dir, "spool")); len(es) != 0 {
		t.Fatalf("%d spool files left", len(es))
	}
	r.panelDecoyIntact()
}

// TestRemoteRmdirConcurrentFileSurvives is the regression test for the rmdir
// race: a file created in the directory after the panel looked at it but
// before the delete reaches the node must not be deleted with it.
func TestRemoteRmdirConcurrentFileSurvives(t *testing.T) {
	r := newRig(t, 1<<20)
	c := r.must("alice@x.io")
	if err := c.Mkdir(remoteDir + "/racy"); err != nil {
		t.Fatal(err)
	}
	r.node.beforeRemove = func() {
		r.node.beforeRemove = nil
		if err := os.WriteFile(filepath.Join(r.dir, "node", botR, "racy", "late.txt"), []byte("keep"), 0o600); err != nil {
			t.Error(err)
		}
	}
	if err := c.RemoveDirectory(remoteDir + "/racy"); err == nil {
		t.Fatal("rmdir succeeded although the directory was no longer empty")
	}
	if got, err := r.nodeFile("racy/late.txt"); err != nil || got != "keep" {
		t.Fatalf("concurrently added file lost: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(r.dir, "node", botR, "racy", "late.txt")); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory(remoteDir + "/racy"); err != nil {
		t.Fatal("rmdir of an empty directory:", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "node", botR, "racy")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("directory still present: %v", err)
	}
}
