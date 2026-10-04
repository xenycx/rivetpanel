package noderoute_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
	"github.com/xenycx/rivetpanel/internal/noderoute"
	"github.com/xenycx/rivetpanel/internal/sftpd"
)

// TestRemoteFileCommands drives the mkdir/move/delete and streamed read calls
// SFTP uses through a real hub and agent.
func TestRemoteFileCommands(t *testing.T) {
	r, nodeID, files := remoteNode(t)
	ctx := t.Context()
	bot := uuid.NewString()

	if err := r.MakeDir(ctx, nodeID, bot, "a/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteFile(ctx, nodeID, bot, "a/b/f.txt", []byte("hello"), "", true); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	rev, n, err := r.ReadFileTo(ctx, nodeID, bot, "a/b/f.txt", 1<<20, &buf)
	if err != nil || n != 5 || buf.String() != "hello" || rev == "" {
		t.Fatalf("read: %q %d %q %v", buf.String(), n, rev, err)
	}
	if _, _, err := r.ReadFileTo(ctx, nodeID, bot, "a/b/f.txt", 4, io.Discard); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("over max: %v", err)
	}
	if _, _, err := r.ReadFileTo(ctx, nodeID, bot, "missing", 1<<20, io.Discard); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := r.ReadFileTo(ctx, nodeID, bot, "../../etc/passwd", 1<<20, io.Discard); err == nil {
		t.Fatal("read escaped the workspace")
	}
	if err := r.Move(ctx, nodeID, bot, "a/b/f.txt", "g.txt"); err != nil {
		t.Fatal(err)
	}
	if got, ok := nodeFile(t, files, bot, "g.txt"); !ok || got != "hello" {
		t.Fatalf("moved file %q %v", got, ok)
	}
	if err := r.Move(ctx, nodeID, bot, "g.txt", "../escape.txt"); err == nil {
		t.Fatal("move escaped the workspace")
	}
	if err := r.Move(ctx, nodeID, bot, "missing", "x"); err == nil {
		t.Fatal("moved a missing file")
	}
	// Non-recursive delete: a non-empty directory is refused with its
	// contents intact; an empty one and a file are removed.
	if _, err := r.WriteFile(ctx, nodeID, bot, "a/keep.txt", []byte("k"), "", true); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveOne(ctx, nodeID, bot, "a"); !errors.Is(err, noderoute.ErrDirNotEmpty) {
		t.Fatalf("non-empty RemoveOne: %v", err)
	}
	if got, ok := nodeFile(t, files, bot, "a/keep.txt"); !ok || got != "k" {
		t.Fatalf("file lost after refused RemoveOne: %q %v", got, ok)
	}
	if err := r.RemoveOne(ctx, nodeID, bot, "a/keep.txt"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveOne(ctx, nodeID, bot, "a/b"); err != nil {
		t.Fatal("empty dir:", err)
	}
	if err := r.RemoveOne(ctx, nodeID, bot, "a/b"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err := r.RemoveOne(ctx, nodeID, bot, "."); err == nil {
		t.Fatal("RemoveOne deleted the workspace root")
	}
	if err := r.MakeDir(ctx, nodeID, bot, "a/b"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemovePath(ctx, nodeID, bot, "a"); err != nil {
		t.Fatal(err)
	}
	if es, err := r.ListDir(ctx, nodeID, bot, "."); err != nil || len(es) != 1 || es[0].Name != "g.txt" {
		t.Fatalf("after delete: %+v %v", es, err)
	}
	if err := r.RemovePath(ctx, nodeID, bot, "."); err == nil {
		t.Fatal("deleted the workspace root")
	}
	if err := r.MakeDir(ctx, nodeID, bot, "../out"); err == nil {
		t.Fatal("mkdir escaped the workspace")
	}

	other := uuid.NewString()
	for name, err := range map[string]error{
		"mkdir":  r.MakeDir(ctx, other, bot, "x"),
		"move":   r.Move(ctx, other, bot, "g.txt", "h.txt"),
		"delete": r.RemovePath(ctx, other, bot, "g.txt"),
	} {
		if err == nil || !strings.Contains(err.Error(), "offline") {
			t.Errorf("offline %s: %v", name, err)
		}
	}
	if _, _, err := r.ReadFileTo(ctx, other, bot, "g.txt", 1<<20, io.Discard); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline read: %v", err)
	}
}

type sftpUser struct{}

func (sftpUser) LoginSFTP(_ context.Context, email, secret string) (domain.User, func(context.Context) (domain.User, error), error) {
	if email != "alice@x.io" || secret != "s3cret" {
		return domain.User{}, nil, domain.ErrUnauthorized
	}
	u := domain.User{ID: "alice", Email: email}
	return u, func(context.Context) (domain.User, error) { return u, nil }, nil
}

type sftpBots struct{ bots []domain.Bot }

func (b sftpBots) List(context.Context, domain.User) ([]domain.Bot, error) { return b.bots, nil }
func (sftpBots) Permissions(context.Context, domain.User, domain.Bot) int {
	return domain.PermEditFiles
}
func (sftpBots) FilesBlocked(string) error { return nil }

// TestSFTPRemoteBotThroughAgent runs the real SFTP server against a real hub
// and agent: a remote bot's files are read and written on the node, its
// stale panel-local directory is never touched, and a bot on a node that is
// not connected is refused cleanly.
func TestSFTPRemoteBotThroughAgent(t *testing.T) {
	r, nodeID, files := remoteNode(t)
	bot := uuid.NewString()
	gone := uuid.NewString()
	if err := files.Create(bot); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	panelFiles, err := filesystem.NewManager(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { panelFiles.Close() })
	if err := panelFiles.Create(bot); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(dir, "bots", bot, "decoy.txt")
	if err := os.WriteFile(decoy, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := sftpd.LoadOrCreateHostKey(filepath.Join(dir, "host"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &sftpd.Server{Auth: sftpUser{}, Files: panelFiles, HostKey: key, MaxFile: 1 << 20,
		Bots: sftpBots{bots: []domain.Bot{{ID: bot, Name: "edge", NodeID: nodeID}, {ID: gone, Name: "gone", NodeID: uuid.NewString()}}},
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.Remote = func(b domain.Bot) (sftpd.NodeFiles, bool, error) {
		if !r.Remote(b.NodeID) {
			return nil, false, nil
		}
		if !r.Online(b.NodeID) {
			return nil, true, domain.Invalid("the server's node is offline; try again when its agent reconnects")
		}
		return r, true, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	sc, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "alice@x.io", Auth: []ssh.AuthMethod{ssh.Password("s3cret")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sc.Close() })
	c, err := sftp.NewClient(sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	root := "/edge-" + bot[:8]
	if es, err := c.ReadDir(root); err != nil || len(es) != 0 {
		t.Fatalf("remote listing %v %v", es, err)
	}
	if _, err := c.Open(root + "/decoy.txt"); err == nil {
		t.Fatal("read the panel-local decoy")
	}
	if err := c.Mkdir(root + "/plugins"); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640 KB, many SFTP packets
	f, err := c.Create(root + "/plugins/p.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w, _ := files.Open(bot)
	got, err := w.Read("plugins/p.jar", 1<<20)
	w.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("node file: %d bytes, %v", len(got), err)
	}
	rf, err := c.Open(root + "/plugins/p.jar")
	if err != nil {
		t.Fatal(err)
	}
	back, err := io.ReadAll(rf)
	rf.Close()
	if err != nil || !bytes.Equal(back, payload) {
		t.Fatalf("download: %d bytes, %v", len(back), err)
	}
	if st, err := c.Stat(root + "/plugins/p.jar"); err != nil || st.Size() != int64(len(payload)) {
		t.Fatalf("stat %v %v", st, err)
	}
	if err := c.Rename(root+"/plugins/p.jar", root+"/p.jar"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory(root + "/plugins"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(root + "/p.jar"); err != nil {
		t.Fatal(err)
	}
	if es, err := c.ReadDir(root); err != nil || len(es) != 0 {
		t.Fatalf("after cleanup %v %v", es, err)
	}

	// A bot on a node without a connection: a clean error.
	if _, err := c.ReadDir("/gone-" + gone[:8]); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline node: %v", err)
	}

	es, _ := os.ReadDir(filepath.Join(dir, "bots", bot))
	if len(es) != 1 {
		t.Fatalf("panel-local directory changed: %d entries", len(es))
	}
	if b, _ := os.ReadFile(decoy); string(b) != "stale" {
		t.Fatalf("decoy changed: %q", b)
	}
}

// TestRemoteAddonObservation reads a remote server's add-on states and logs
// through a real hub and agent (protocol 6).
func TestRemoteAddonObservation(t *testing.T) {
	r, nodeID, _ := remoteNode(t)
	ctx := t.Context()
	bot := uuid.NewString()
	st, err := r.AddonStates(ctx, nodeID, bot)
	if err != nil || st["redis"].State != "running" || st["redis"].Health != "healthy" {
		t.Fatalf("states %+v %v", st, err)
	}
	out, err := r.AddonLogs(ctx, nodeID, bot, "redis", 9999)
	if err != nil || out != bot+" redis 500" {
		t.Fatalf("logs %q %v", out, err)
	}
	var invalid *domain.ValidationError
	if _, err := r.AddonLogs(ctx, nodeID, bot, "oracle", 10); !errors.As(err, &invalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := r.AddonLogs(ctx, nodeID, "nope", "redis", 10); !errors.As(err, &invalid) {
		t.Fatalf("invalid id: %v", err)
	}
	other := uuid.NewString()
	if _, err := r.AddonLogs(ctx, other, bot, "redis", 10); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline logs: %v", err)
	}
	if _, err := r.AddonStates(ctx, other, bot); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline states: %v", err)
	}
	if _, err := r.AddonLogs(ctx, domain.LocalNodeID, bot, "redis", 10); err == nil {
		t.Fatal("the local node was sent to an agent")
	}
}
