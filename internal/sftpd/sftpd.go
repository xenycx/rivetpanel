// Package sftpd embeds an SFTP server so external clients (FileZilla,
// Cyberduck) can manage bot files. Users sign in with their panel email and
// either their password or an API key. Each user sees one directory per bot
// they may edit; every operation runs through the same descriptor-relative
// filesystem containment as the web file manager and is re-authorized. Bots
// on remote nodes are served through their rivet-agent (NodeFiles), never
// from the panel's own disk.
package sftpd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// Authenticator verifies SFTP credentials (password or API key). The returned
// check re-validates the login (account enabled, key not revoked or expired,
// password unchanged) without keeping the secret.
type Authenticator interface {
	LoginSFTP(ctx context.Context, email, secret string) (domain.User, func(context.Context) (domain.User, error), error)
}

// Bots is the bot access surface the server needs.
type Bots interface {
	List(ctx context.Context, actor domain.User) ([]domain.Bot, error)
	Permissions(ctx context.Context, actor domain.User, b domain.Bot) int
	// FilesBlocked is non-nil while a deployment or restore replaces the files.
	FilesBlocked(botID string) error
}

// Server is the SFTP daemon.
type Server struct {
	Auth  Authenticator
	Bots  Bots
	Files *filesystem.Manager
	// Remote reports whether a bot's files live on a remote node and, if so,
	// returns the access to them (an offline node is an error). Nil fails
	// closed: every bot is refused.
	Remote func(b domain.Bot) (NodeFiles, bool, error)
	// SpoolDir holds remote transfers while they cross the panel ("" = the
	// system temporary directory). Spool files are unlinked when created.
	SpoolDir string
	HostKey  ssh.Signer
	MaxFile  int64 // largest file a client may write, in bytes
	MaxConns int   // concurrent connections (default 32)
	Log      *slog.Logger

	fails failLimiter
}

const (
	handshakeTimeout = 15 * time.Second
	opTimeout        = 10 * time.Second
	// authCacheTTL bounds how long a revoked login, credential or bot grant
	// keeps working: every operation (including reads and writes on files
	// opened earlier) re-validates once the cached result is this old.
	authCacheTTL = 5 * time.Second
	recheckEvery = 5 * time.Second
)

// LoadOrCreateHostKey returns the persistent ed25519 host key at file,
// generating it (mode 0600) on first use so clients can pin the fingerprint.
func LoadOrCreateHostKey(file string) (ssh.Signer, error) {
	if b, err := os.ReadFile(file); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, "rivetpanel-sftp")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := pem.Encode(f, blk); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// Fingerprint returns the SHA-256 fingerprint clients display for the host key.
func Fingerprint(s ssh.Signer) string { return ssh.FingerprintSHA256(s.PublicKey()) }

// Serve accepts connections until ctx is cancelled or ln fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	max := s.MaxConns
	if max <= 0 {
		max = 32
	}
	sem := make(chan struct{}, max)
	go func() { <-ctx.Done(); ln.Close() }()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case sem <- struct{}{}:
		default:
			nc.Close() // over the connection cap
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.handleConn(ctx, nc)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, nc net.Conn) {
	defer nc.Close()
	ip := remoteIP(nc.RemoteAddr())
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-cctx.Done(); nc.Close() }() // shutdown ends live sessions

	var mu sync.Mutex
	var user domain.User
	var check func(context.Context) (domain.User, error)
	cfg := &ssh.ServerConfig{
		MaxAuthTries:  3,
		ServerVersion: "SSH-2.0-RivetPanel",
		PasswordCallback: func(md ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if s.fails.blocked(ip) {
				return nil, errors.New("too many failed attempts")
			}
			actx, acancel := context.WithTimeout(cctx, opTimeout)
			defer acancel()
			u, chk, err := s.Auth.LoginSFTP(actx, md.User(), string(pw))
			if err != nil {
				s.fails.add(ip)
				s.Log.Warn("sftp login failed", "ip", ip)
				return nil, errors.New("authentication failed")
			}
			mu.Lock()
			user, check = u, chk
			mu.Unlock()
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(s.HostKey)

	_ = nc.SetDeadline(time.Now().Add(handshakeTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	_ = nc.SetDeadline(time.Time{})
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	mu.Lock()
	u, chk := user, check
	mu.Unlock()
	s.Log.Info("sftp session", "user_id", u.ID, "ip", ip)
	sess := &session{user: u, check: chk, validUntil: time.Now().Add(authCacheTTL), end: func() { sc.Close() }}
	// Watchdog: a disabled account, a deleted or expired key, or a changed
	// password ends the connection within recheckEvery, even when idle.
	go func() {
		t := time.NewTicker(recheckEvery)
		defer t.Stop()
		for {
			select {
			case <-cctx.Done():
				return
			case <-t.C:
				vctx, cancel := context.WithTimeout(cctx, opTimeout)
				err := sess.refresh(vctx)
				cancel()
				if err != nil {
					s.Log.Info("sftp session revoked", "user_id", u.ID, "ip", ip)
					return
				}
			}
		}
	}()

	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go s.serveChannel(cctx, sess, ch, chReqs)
	}
}

// session is one authenticated SSH connection. Its validity is re-checked at
// most every authCacheTTL on use, and by the watchdog every recheckEvery.
type session struct {
	check      func(context.Context) (domain.User, error)
	end        func()
	mu         sync.Mutex
	user       domain.User
	validUntil time.Time
	dead       bool
}

// current returns the user when the login is still valid.
func (s *session) current(ctx context.Context) (domain.User, error) {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return domain.User{}, sftp.ErrSSHFxPermissionDenied
	}
	if time.Now().Before(s.validUntil) {
		u := s.user
		s.mu.Unlock()
		return u, nil
	}
	s.mu.Unlock()
	if err := s.refresh(ctx); err != nil {
		return domain.User{}, sftp.ErrSSHFxPermissionDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user, nil
}

// refresh re-validates now; on failure the connection is closed.
func (s *session) refresh(ctx context.Context) error {
	if s.check == nil {
		return nil
	}
	u, err := s.check(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return nil // a timeout is not a revocation; try again later
		}
		if !s.dead {
			s.dead = true
			go s.end()
		}
		return err
	}
	s.user, s.validUntil = u, time.Now().Add(authCacheTTL)
	return nil
}

func (s *Server) serveChannel(ctx context.Context, sess *session, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	started := false
	for r := range reqs {
		if r.Type != "subsystem" || started || len(r.Payload) < 4 || string(r.Payload[4:]) != "sftp" {
			r.Reply(false, nil) // no shell, exec, pty or port forwarding
			continue
		}
		started = true
		r.Reply(true, nil)
		h := &handler{s: s, ctx: ctx, sess: sess, cache: map[string]cacheEntry{}}
		rs := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
		go func() {
			if err := rs.Serve(); err != nil && !errors.Is(err, io.EOF) {
				s.Log.Debug("sftp session ended", "err", err)
			}
			ch.Close()
		}()
	}
}

// ---- failed-login limiter ----

type failLimiter struct {
	mu sync.Mutex
	m  map[string]*failEntry
}

type failEntry struct {
	n     int
	since time.Time
}

const (
	maxFails   = 10
	failWindow = time.Minute
)

func (f *failLimiter) blocked(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.m[ip]
	return e != nil && time.Since(e.since) < failWindow && e.n >= maxFails
}

func (f *failLimiter) add(ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*failEntry{}
	}
	if len(f.m) > 10000 { // bound memory under a spray from many addresses
		for k, v := range f.m {
			if time.Since(v.since) >= failWindow {
				delete(f.m, k)
			}
		}
	}
	e := f.m[ip]
	if e == nil || time.Since(e.since) >= failWindow {
		e = &failEntry{since: time.Now()}
		f.m[ip] = e
	}
	e.n++
}

func remoteIP(a net.Addr) string {
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}

// ---- request handler ----

type cacheEntry struct {
	ok      bool
	expires time.Time
}

type handler struct {
	s    *Server
	ctx  context.Context
	sess *session

	mu    sync.Mutex
	cache map[string]cacheEntry // bot id -> may still edit files
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// dirName is the directory a bot appears as: readable name plus an id fragment
// that keeps it unique and stable across renames.
func dirName(b domain.Bot) string {
	n := strings.Trim(unsafeName.ReplaceAllString(b.Name, "_"), "._-")
	if n == "" {
		n = "bot"
	}
	if len(n) > 40 {
		n = n[:40]
	}
	return n + "-" + b.ID[:8]
}

func (h *handler) editable(ctx context.Context) ([]domain.Bot, error) {
	u, err := h.sess.current(ctx)
	if err != nil {
		return nil, err
	}
	bots, err := h.s.Bots.List(ctx, u)
	if err != nil {
		return nil, err
	}
	out := bots[:0]
	for _, b := range bots {
		if domain.HasPerm(h.s.Bots.Permissions(ctx, u, b), domain.PermEditFiles) {
			out = append(out, b)
		}
	}
	return out, nil
}

type target struct {
	root bool // "/"
	bot  domain.Bot
	dir  string // the bot's directory name
	rel  string // workspace-relative path; "." for the bot root
}

func (t target) botRoot() bool { return !t.root && t.rel == "." }

func (h *handler) resolve(ctx context.Context, vpath string) (target, error) {
	p := path.Clean("/" + vpath)
	if p == "/" {
		return target{root: true}, nil
	}
	first, rest, _ := strings.Cut(p[1:], "/")
	bots, err := h.editable(ctx)
	if err != nil {
		return target{}, err
	}
	for _, b := range bots {
		if dirName(b) == first {
			rel := "."
			if rest != "" {
				rel = rest
			}
			return target{bot: b, dir: first, rel: rel}, nil
		}
	}
	return target{}, sftp.ErrSSHFxNoSuchFile
}

// allowed re-checks (with a short cache) that the login is valid and the bot
// is still editable by it.
func (h *handler) allowed(ctx context.Context, botID string) error {
	if _, err := h.sess.current(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	c, ok := h.cache[botID]
	h.mu.Unlock()
	if !ok || time.Now().After(c.expires) {
		bots, err := h.editable(ctx)
		if err != nil {
			return err
		}
		c = cacheEntry{expires: time.Now().Add(authCacheTTL)}
		for _, b := range bots {
			if b.ID == botID {
				c.ok = true
			}
		}
		h.mu.Lock()
		h.cache[botID] = c
		h.mu.Unlock()
	}
	if !c.ok {
		return sftp.ErrSSHFxPermissionDenied
	}
	return nil
}

// allowedNow is allowed with its own operation timeout.
func (h *handler) allowedNow(botID string) error {
	ctx, cancel := h.opCtx()
	defer cancel()
	return h.allowed(ctx, botID)
}

// open re-checks that the login is valid and the bot is still editable
// (revocation takes effect within authCacheTTL) and opens its local
// workspace. It refuses a bot on a remote node: its directory on this
// panel's disk, if any, is stale and must never be served.
func (h *handler) open(ctx context.Context, t target) (*filesystem.Workspace, error) {
	if err := h.allowed(ctx, t.bot.ID); err != nil {
		return nil, err
	}
	if _, remote, _ := h.remoteOf(t); remote {
		return nil, sftp.ErrSSHFxPermissionDenied
	}
	w, err := h.s.Files.Open(t.bot.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return w, nil
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sftp.ErrSSHFxNoSuchFile), errors.Is(err, sftp.ErrSSHFxPermissionDenied), errors.Is(err, sftp.ErrSSHFxOpUnsupported):
		return err
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, domain.ErrNotFound):
		return sftp.ErrSSHFxNoSuchFile
	case errors.Is(err, filesystem.ErrInvalidPath), strings.Contains(err.Error(), "path escapes"):
		return sftp.ErrSSHFxPermissionDenied
	case errors.Is(err, fs.ErrPermission):
		return sftp.ErrSSHFxPermissionDenied
	}
	return err
}

func (h *handler) opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(h.ctx, opTimeout)
}

// Fileread implements sftp.FileReader.
func (h *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	ctx, cancel := h.opCtx()
	defer cancel()
	t, err := h.resolve(ctx, r.Filepath)
	if err != nil {
		return nil, mapErr(err)
	}
	if t.root || t.botRoot() {
		return nil, sftp.ErrSSHFxFailure
	}
	if nf, remote, err := h.remote(ctx, t); remote {
		if err != nil {
			return nil, err
		}
		return h.remoteRead(t, nf)
	}
	w, err := h.open(ctx, t)
	if err != nil {
		return nil, mapErr(err)
	}
	defer w.Close()
	f, _, err := w.OpenFile(t.rel)
	if err != nil {
		return nil, mapErr(err)
	}
	return &guardedFile{f: f, h: h, bot: t.bot.ID}, nil
}

// guardedFile re-checks access on every read or write of an already-open
// handle, so revoking a grant, key or account also stops transfers in flight.
// For a remote bot f is a private spool file and commit sends a written file
// to the node when the client closes it.
type guardedFile struct {
	f      *os.File
	h      *handler
	bot    string
	max    int64 // writes: per-file cap
	commit func(*os.File) error
	failed atomic.Bool // a refused write: the spooled file is never committed
}

func (g *guardedFile) check() error {
	ctx, cancel := g.h.opCtx()
	defer cancel()
	return g.h.allowed(ctx, g.bot)
}

func (g *guardedFile) ReadAt(p []byte, off int64) (int, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	return g.f.ReadAt(p, off)
}

func (g *guardedFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := g.writeAt(p, off)
	if err != nil {
		g.failed.Store(true)
	}
	return n, err
}

func (g *guardedFile) writeAt(p []byte, off int64) (int, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	if err := g.h.s.Bots.FilesBlocked(g.bot); err != nil {
		return 0, sftp.ErrSSHFxFailure
	}
	if off < 0 || off+int64(len(p)) > g.max {
		return 0, fmt.Errorf("file exceeds the %d byte limit", g.max)
	}
	return g.f.WriteAt(p, off)
}

func (g *guardedFile) Close() error {
	if g.commit != nil {
		var err error
		if g.failed.Load() {
			err = errors.New("the upload failed; nothing was saved on the server")
		} else {
			err = g.commit(g.f)
		}
		g.commit = nil
		if cerr := g.f.Close(); err == nil {
			err = cerr
		}
		return err
	}
	return g.f.Close()
}

// Filewrite implements sftp.FileWriter.
func (h *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	ctx, cancel := h.opCtx()
	defer cancel()
	t, err := h.resolve(ctx, r.Filepath)
	if err != nil {
		return nil, mapErr(err)
	}
	if t.root || t.botRoot() {
		return nil, sftp.ErrSSHFxPermissionDenied
	}
	if err := h.s.Bots.FilesBlocked(t.bot.ID); err != nil {
		return nil, sftp.ErrSSHFxFailure // a deployment or restore is replacing the files
	}
	if nf, remote, err := h.remote(ctx, t); remote {
		if err != nil {
			return nil, err
		}
		return h.remoteWrite(ctx, t, nf, r.Pflags())
	}
	w, err := h.open(ctx, t)
	if err != nil {
		return nil, mapErr(err)
	}
	defer w.Close()
	pf := r.Pflags()
	if !pf.Creat {
		if _, err := w.Lstat(t.rel); err != nil {
			return nil, mapErr(err)
		}
	}
	flags := 0
	if pf.Trunc {
		flags |= os.O_TRUNC
	}
	if pf.Append {
		flags |= os.O_APPEND
	}
	if pf.Excl && pf.Creat {
		flags |= os.O_EXCL
	}
	f, err := w.OpenWrite(t.rel, flags)
	if err != nil {
		return nil, mapErr(err)
	}
	max := h.s.MaxFile
	if max <= 0 {
		max = 256 << 20
	}
	return &guardedFile{f: f, h: h, bot: t.bot.ID, max: max}, nil
}

// Filecmd implements sftp.FileCmder.
func (h *handler) Filecmd(r *sftp.Request) error {
	ctx, cancel := h.opCtx()
	defer cancel()
	switch r.Method {
	case "Setstat":
		return nil // modes and ownership are managed by the panel; accept silently
	case "Symlink", "Link":
		return sftp.ErrSSHFxOpUnsupported
	}
	t, err := h.resolve(ctx, r.Filepath)
	if err != nil {
		return mapErr(err)
	}
	if t.root || t.botRoot() {
		return sftp.ErrSSHFxPermissionDenied // bot directories are managed in the panel
	}
	if err := h.s.Bots.FilesBlocked(t.bot.ID); err != nil {
		return sftp.ErrSSHFxFailure
	}
	if nf, remote, err := h.remote(ctx, t); remote {
		if err != nil {
			return err
		}
		return h.remoteCmd(ctx, r, t, nf)
	}
	w, err := h.open(ctx, t)
	if err != nil {
		return mapErr(err)
	}
	defer w.Close()
	switch r.Method {
	case "Rename":
		dst, err := h.resolve(ctx, r.Target)
		if err != nil {
			return mapErr(err)
		}
		if dst.root || dst.botRoot() || dst.bot.ID != t.bot.ID {
			return sftp.ErrSSHFxPermissionDenied // never across bots
		}
		if info, err := w.Lstat(dst.rel); err == nil && info.IsDir() {
			return sftp.ErrSSHFxFailure
		}
		return mapErr(w.Rename(t.rel, dst.rel))
	case "Remove":
		if info, err := w.Lstat(t.rel); err == nil && info.IsDir() {
			return sftp.ErrSSHFxFailure
		}
		return mapErr(w.RemoveOne(t.rel))
	case "Rmdir":
		if info, err := w.Lstat(t.rel); err == nil && !info.IsDir() {
			return sftp.ErrSSHFxFailure
		}
		return mapErr(w.RemoveOne(t.rel))
	case "Mkdir":
		return mapErr(w.MkdirOne(t.rel))
	}
	return sftp.ErrSSHFxOpUnsupported
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(f []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(f, l[off:])
	if n < len(f) {
		return n, io.EOF
	}
	return n, nil
}

// virtDir is the FileInfo of a synthetic directory.
type virtDir struct{ name string }

func (v virtDir) Name() string       { return v.name }
func (v virtDir) Size() int64        { return 0 }
func (v virtDir) Mode() fs.FileMode  { return fs.ModeDir | 0o750 }
func (v virtDir) ModTime() time.Time { return time.Unix(0, 0) }
func (v virtDir) IsDir() bool        { return true }
func (v virtDir) Sys() any           { return nil }

// Filelist implements sftp.FileLister.
func (h *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	ctx, cancel := h.opCtx()
	defer cancel()
	t, err := h.resolve(ctx, r.Filepath)
	if err != nil {
		return nil, mapErr(err)
	}
	switch r.Method {
	case "List":
		if t.root {
			bots, err := h.editable(ctx)
			if err != nil {
				return nil, mapErr(err)
			}
			out := make(listerAt, 0, len(bots))
			for _, b := range bots {
				out = append(out, virtDir{dirName(b)})
			}
			return out, nil
		}
		if nf, remote, err := h.remote(ctx, t); remote {
			if err != nil {
				return nil, err
			}
			return h.remoteList(ctx, r, t, nf)
		}
		w, err := h.open(ctx, t)
		if err != nil {
			return nil, mapErr(err)
		}
		defer w.Close()
		infos, err := w.ListInfo(t.rel)
		if err != nil {
			return nil, mapErr(err)
		}
		return listerAt(infos), nil
	case "Stat":
		if t.root {
			return listerAt{virtDir{"/"}}, nil
		}
		if t.botRoot() {
			return listerAt{virtDir{t.dir}}, nil
		}
		if nf, remote, err := h.remote(ctx, t); remote {
			if err != nil {
				return nil, err
			}
			return h.remoteList(ctx, r, t, nf)
		}
		w, err := h.open(ctx, t)
		if err != nil {
			return nil, mapErr(err)
		}
		defer w.Close()
		info, err := w.Lstat(t.rel)
		if err != nil {
			return nil, mapErr(err)
		}
		return listerAt{info}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}
