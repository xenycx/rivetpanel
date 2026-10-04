package sftpd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

const (
	botA  = "aaaaaaaa-0000-4000-8000-000000000001"
	botB  = "bbbbbbbb-0000-4000-8000-000000000002"
	botC  = "cccccccc-0000-4000-8000-000000000003" // alice may not edit files here
	botR  = "dddddddd-0000-4000-8000-000000000004" // runs on a remote node
	nodeR = "node-remote"
)

// switches lets tests revoke access while a session is open.
type switches struct {
	revoked atomic.Bool // the login (account/key/password) is no longer valid
	noGrant atomic.Bool // alice lost the files permission on botA
	blocked atomic.Bool // a deployment/restore holds botA (and botR)
	offline atomic.Bool // botR's node is not connected
}

type fakeAuth struct{ sw *switches }

func (a fakeAuth) LoginSFTP(_ context.Context, email, secret string) (domain.User, func(context.Context) (domain.User, error), error) {
	if secret == "s3cret" && (email == "alice@x.io" || email == "bob@x.io") {
		u := domain.User{ID: email, Email: email}
		return u, func(context.Context) (domain.User, error) {
			if a.sw != nil && a.sw.revoked.Load() {
				return domain.User{}, domain.ErrUnauthorized
			}
			return u, nil
		}, nil
	}
	return domain.User{}, nil, domain.ErrUnauthorized
}

type fakeBots struct{ sw *switches }

func (fakeBots) List(_ context.Context, u domain.User) ([]domain.Bot, error) {
	if u.ID == "alice@x.io" {
		return []domain.Bot{{ID: botA, Name: "My Bot"}, {ID: botB, Name: "../evil"}, {ID: botC, Name: "readonly"},
			{ID: botR, Name: "Remote", NodeID: nodeR}}, nil
	}
	return nil, nil
}
func (f fakeBots) Permissions(_ context.Context, _ domain.User, b domain.Bot) int {
	if b.ID == botC || (b.ID == botA && f.sw != nil && f.sw.noGrant.Load()) {
		return domain.PermViewConsole
	}
	return domain.PermEditFiles
}
func (f fakeBots) FilesBlocked(botID string) error {
	if (botID == botA || botID == botR) && f.sw != nil && f.sw.blocked.Load() {
		return &domain.BusyError{What: "A restore"}
	}
	return nil
}

type rig struct {
	sw      *switches
	node    *fakeNode
	t       *testing.T
	addr    string
	files   *filesystem.Manager
	dir     string
	outside string
}

func newRig(t *testing.T, maxFile int64) *rig {
	dir := t.TempDir()
	fm, err := filesystem.NewManager(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fm.Close() })
	for _, id := range []string{botA, botB, botC} {
		if err := fm.Create(id); err != nil {
			t.Fatal(err)
		}
	}
	key, err := LoadOrCreateHostKey(filepath.Join(dir, "keys", "host"))
	if err != nil {
		t.Fatal(err)
	}
	// The remote bot's files live on the node. The panel keeps a stale
	// directory of the same id: it must never be listed, read or written.
	if err := fm.Create(botR); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bots", botR, "decoy.txt"), []byte("stale panel copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	nm, err := filesystem.NewManager(filepath.Join(dir, "node"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nm.Close() })
	if err := nm.Create(botR); err != nil {
		t.Fatal(err)
	}
	sw := &switches{}
	node := &fakeNode{files: nm}
	srv := &Server{Auth: fakeAuth{sw}, Bots: fakeBots{sw}, Files: fm, HostKey: key, MaxFile: maxFile, SpoolDir: filepath.Join(dir, "spool"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := os.MkdirAll(srv.SpoolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	srv.Remote = func(b domain.Bot) (NodeFiles, bool, error) {
		if b.NodeID == "" {
			return nil, false, nil
		}
		if sw.offline.Load() {
			return nil, true, domain.Invalid("the server's node is offline; try again when its agent reconnects")
		}
		return node, true, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &rig{sw: sw, node: node, t: t, addr: ln.Addr().String(), files: fm, dir: dir, outside: filepath.Join(dir, "outside.txt")}
}

func (r *rig) client(user, pw string) (*sftp.Client, error) {
	c, err := ssh.Dial("tcp", r.addr, &ssh.ClientConfig{
		User: user, Auth: []ssh.AuthMethod{ssh.Password(pw)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, err
	}
	r.t.Cleanup(func() { c.Close() })
	return sftp.NewClient(c)
}

func (r *rig) must(user string) *sftp.Client {
	c, err := r.client(user, "s3cret")
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	return c
}

func TestHostKeyIsPersistent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "k", "host")
	a, err := LoadOrCreateHostKey(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateHostKey(f)
	if err != nil || Fingerprint(a) != Fingerprint(b) {
		t.Fatal("host key changed between loads", err)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestAuthFailuresAndRateLimit(t *testing.T) {
	r := newRig(t, 1<<20)
	if _, err := r.client("alice@x.io", "wrong"); err == nil {
		t.Fatal("bad password accepted")
	}
	if _, err := r.client("mallory@x.io", "s3cret"); err == nil {
		t.Fatal("unknown user accepted")
	}
	for i := 0; i < 12; i++ {
		r.client("alice@x.io", "wrong")
	}
	if _, err := r.client("alice@x.io", "s3cret"); err == nil {
		t.Fatal("correct password accepted while the address is rate limited")
	}
}

func TestBrowseReadWriteAndContainment(t *testing.T) {
	r := newRig(t, 1024)
	c := r.must("alice@x.io")

	infos, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range infos {
		names = append(names, i.Name())
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "My_Bot-aaaaaaaa") || !strings.Contains(got, "evil-bbbbbbbb") || strings.Contains(got, "readonly") {
		t.Fatalf("root listing (readonly bot must be hidden, names sanitized): %s", got)
	}

	bot := "/My_Bot-aaaaaaaa"
	f, err := c.Create(bot + "/src/index.js")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("console.log(1)"))
	f.Close()
	if b, err := os.ReadFile(filepath.Join(r.dir, "bots", botA, "src", "index.js")); err != nil || string(b) != "console.log(1)" {
		t.Fatalf("file not on disk: %q %v", b, err)
	}
	rf, err := c.Open(bot + "/src/index.js")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rf)
	rf.Close()
	if string(b) != "console.log(1)" {
		t.Fatal(string(b))
	}

	// Rename in the same bot works; across bots and onto the root does not.
	if err := c.Rename(bot+"/src/index.js", bot+"/main.js"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename(bot+"/main.js", "/evil-bbbbbbbb/main.js"); err == nil {
		t.Fatal("cross-bot rename accepted")
	}
	if err := c.Mkdir(bot + "/d"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory(bot + "/d"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(bot + "/main.js"); err != nil {
		t.Fatal(err)
	}

	// Bot directories and the virtual root are read-only structure.
	for name, fn := range map[string]func() error{
		"mkdir root":     func() error { return c.Mkdir("/newbot") },
		"remove bot dir": func() error { return c.RemoveDirectory(bot) },
		"write in root":  func() error { _, err := c.Create("/x"); return err },
		"read-only bot":  func() error { _, err := c.Create("/readonly-cccccccc/x"); return err },
		"symlink":        func() error { return c.Symlink("/etc/passwd", bot+"/l") },
		"unknown bot":    func() error { _, err := c.Stat("/nope-12345678"); return err },
		"list unknown":   func() error { _, err := c.ReadDir("/nope-12345678"); return err },
	} {
		if fn() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	// Traversal is normalized away or refused, never resolved outside the bot.
	os.WriteFile(r.outside, []byte("host secret"), 0o600)
	for _, p := range []string{bot + "/../../outside.txt", bot + "/../" + "outside.txt", "/../outside.txt", bot + "/..%2f..%2foutside.txt"} {
		if rf, err := c.Open(p); err == nil {
			b, _ := io.ReadAll(rf)
			rf.Close()
			t.Errorf("traversal %q read %q", p, b)
		}
	}

	// A symlink planted inside the workspace cannot reach outside it.
	os.Symlink(r.outside, filepath.Join(r.dir, "bots", botA, "leak"))
	os.Symlink("/etc", filepath.Join(r.dir, "bots", botA, "etc"))
	if rf, err := c.Open(bot + "/leak"); err == nil {
		rf.Close()
		t.Error("symlink escape (file) succeeded")
	}
	if _, err := c.ReadDir(bot + "/etc"); err == nil {
		t.Error("symlink escape (dir) succeeded")
	}
	if wf, err := c.OpenFile(bot+"/leak", os.O_WRONLY|os.O_TRUNC); err == nil {
		wf.Close()
		t.Error("write through escaping symlink succeeded")
	}
	if b, _ := os.ReadFile(r.outside); string(b) != "host secret" {
		t.Fatalf("outside file modified: %q", b)
	}

	// Per-file size cap.
	wf, err := c.Create(bot + "/big")
	if err != nil {
		t.Fatal(err)
	}
	_, werr := wf.Write(make([]byte, 4096))
	cerr := wf.Close()
	if werr == nil && cerr == nil {
		t.Error("write past the size cap succeeded")
	}
}

func TestOnlySFTPSubsystem(t *testing.T) {
	r := newRig(t, 1024)
	c, err := ssh.Dial("tcp", r.addr, &ssh.ClientConfig{User: "alice@x.io", Auth: []ssh.AuthMethod{ssh.Password("s3cret")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run("id"); err == nil {
		t.Fatal("exec accepted")
	}
	if _, _, err := c.OpenChannel("direct-tcpip", nil); err == nil {
		t.Fatal("port forwarding accepted")
	}
}

func TestOpenHandlesAndSessionsFollowRevocation(t *testing.T) {
	r := newRig(t, 1<<20)
	c := r.must("alice@x.io")
	w, _ := r.files.Open(botA)
	w.Write("data.txt", strings.NewReader("0123456789"), 100)
	w.Close()
	dir := "/My_Bot-" + botA[:8]

	// A file opened before the grant is removed stops working afterwards.
	f, err := c.Open(dir + "/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	r.sw.noGrant.Store(true)
	time.Sleep(authCacheTTL + 200*time.Millisecond)
	if _, err := f.ReadAt(buf, 4); err == nil {
		t.Fatal("an open handle kept reading after the grant was removed")
	}
	r.sw.noGrant.Store(false)

	// Writes wait while a restore or deployment replaces the files.
	r.sw.blocked.Store(true)
	if err := c.Mkdir(dir + "/new"); err == nil {
		t.Fatal("mkdir during a restore")
	}
	if _, err := c.Create(dir + "/x.txt"); err == nil {
		t.Fatal("create during a restore")
	}
	r.sw.blocked.Store(false)

	// Revoking the login (disabled account, deleted key, changed password)
	// ends the whole connection within the recheck interval, even when idle.
	r.sw.revoked.Store(true)
	deadline := time.Now().Add(recheckEvery + 3*time.Second)
	for {
		if _, err := c.ReadDir("/"); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a revoked login kept its SFTP connection")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
