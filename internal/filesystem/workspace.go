// Package filesystem provides contained access to per-bot workspaces. All
// operations are descriptor-relative through os.Root, so ".." components and
// symlinks that escape the workspace are rejected by the kernel-facing
// implementation rather than by string prefix checks.
package filesystem

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

var (
	ErrInvalidPath = errors.New("invalid path")
	ErrTooLarge    = errors.New("file too large")
	ErrInvalidID   = errors.New("invalid bot id")
)

// Manager owns the data root directory, opened once so later replacement of the
// path (e.g. by symlink) cannot redirect operations.
type Manager struct {
	path     string
	root     *os.Root
	uid, gid int // owner for files the panel creates; -1 = leave as is
}

// NewManager creates dataRoot if needed and opens it.
func NewManager(dataRoot string) (*Manager, error) {
	dataRoot, err := filepath.Abs(dataRoot) // bind mount sources must be absolute
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o750); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dataRoot)
	if err != nil {
		return nil, err
	}
	return &Manager{path: dataRoot, root: r, uid: -1, gid: -1}, nil
}

// Close releases the data root descriptor.
func (m *Manager) Close() error { return m.root.Close() }

// SetOwner makes files and directories created through workspaces owned by
// uid:gid (best effort: requires CAP_CHOWN unless it is the caller's own ID), so
// the unprivileged container user can use them immediately.
func (m *Manager) SetOwner(uid, gid int) { m.uid, m.gid = uid, gid }

func checkID(botID string) error {
	u, err := uuid.Parse(botID)
	if err != nil || u.String() != botID {
		return ErrInvalidID
	}
	return nil
}

// Create makes the workspace directory for a bot. It fails if it exists.
func (m *Manager) Create(botID string) error {
	if err := checkID(botID); err != nil {
		return err
	}
	return m.root.Mkdir(botID, 0o750)
}

// Open returns a contained handle on an existing workspace. The entry must be a
// real directory, not a symlink.
func (m *Manager) Open(botID string) (*Workspace, error) {
	if err := checkID(botID); err != nil {
		return nil, err
	}
	st, err := m.root.Lstat(botID)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("workspace %s is not a directory", botID)
	}
	r, err := m.root.OpenRoot(botID)
	if err != nil {
		return nil, err
	}
	return &Workspace{root: r, uid: m.uid, gid: m.gid, m: m, id: botID}, nil
}

// Remove deletes a workspace and everything in it. A missing workspace is not an error.
func (m *Manager) Remove(botID string) error {
	if err := checkID(botID); err != nil {
		return err
	}
	err := m.root.RemoveAll(botID)
	if errors.Is(err, fs.ErrPermission) {
		// Toolchains leave read-only trees behind (Go's module cache is 0555);
		// the owner may still make them writable, then removal succeeds.
		m.makeWritable(botID)
		err = m.root.RemoveAll(botID)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// makeWritable adds owner write/execute permission to every directory under
// the workspace, without following symlinks. Best effort, bounded.
func (m *Manager) makeWritable(botID string) {
	r, err := m.root.OpenRoot(botID)
	if err != nil {
		return
	}
	defer r.Close()
	_ = r.Chmod(".", 0o750)
	n := 0
	_ = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if n++; n > maxChownEntries {
			return fs.SkipAll
		}
		if err != nil {
			// An unreadable directory: fix it and let the retry walk into it.
			_ = r.Chmod(p, 0o750)
			return nil
		}
		if d.IsDir() {
			if info, err := d.Info(); err == nil && info.Mode().Perm()&0o300 != 0o300 {
				_ = r.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
}

// Workspace is a contained view of one bot's files.
type Workspace struct {
	m        *Manager // journals crash-safe commits; nil in tests that build a Workspace directly
	id       string
	root     *os.Root
	uid, gid int
}

// chown applies the configured owner to p; lack of privilege is not an error
// here (the runner repairs ownership when it starts the bot and reports failure).
func (w *Workspace) chown(p string) {
	if w.uid >= 0 {
		_ = w.root.Lchown(p, w.uid, w.gid)
	}
}

// mkdirAll creates p and missing parents, owning each directory it creates.
func (w *Workspace) mkdirAll(p string) error {
	if p == "." || p == "" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(p, "/") {
		cur = path.Join(cur, part)
		if _, err := w.root.Stat(cur); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := w.root.Mkdir(cur, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		w.chown(cur)
	}
	return nil
}

// Close releases the workspace descriptor.
func (w *Workspace) Close() error { return w.root.Close() }

// clean validates a workspace-relative path.
func clean(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || strings.ContainsRune(p, '\\') {
		return "", ErrInvalidPath
	}
	p = path.Clean(p)
	if !filepath.IsLocal(p) {
		return "", ErrInvalidPath
	}
	return p, nil
}

// Read returns file contents, failing with ErrTooLarge beyond max bytes.
func (w *Workspace) Read(p string, max int64) ([]byte, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	f, err := w.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrInvalidPath)
	}
	if st.Size() > max {
		return nil, ErrTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Write atomically replaces (or creates) a file from r, reading at most max
// bytes. Parent directories are created. On any failure the original is intact.
func (w *Workspace) Write(p string, r io.Reader, max int64) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	dir := path.Dir(p)
	if dir != "." {
		if err := w.mkdirAll(dir); err != nil {
			return err
		}
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := path.Join(dir, ".tmp-"+hex.EncodeToString(rnd[:]))
	f, err := w.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	cleanup := func() { f.Close(); w.root.Remove(tmp) }
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err != nil {
		cleanup()
		return err
	}
	if n > max {
		cleanup()
		return ErrTooLarge
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		w.root.Remove(tmp)
		return err
	}
	w.chown(tmp)
	if err := w.root.Rename(tmp, p); err != nil {
		w.root.Remove(tmp)
		return err
	}
	return nil
}

// Entry describes a directory entry.
type Entry struct {
	Name    string
	IsDir   bool
	Symlink bool
	Size    int64
}

// List returns the entries of a directory ("." for the workspace root).
func (w *Workspace) List(p string) ([]Entry, error) {
	if p != "." {
		var err error
		if p, err = clean(p); err != nil {
			return nil, err
		}
	}
	d, err := w.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	des, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		e := Entry{Name: de.Name(), IsDir: de.IsDir(), Symlink: de.Type()&fs.ModeSymlink != 0}
		if info, err := de.Info(); err == nil && info.Mode().IsRegular() {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	return out, nil
}

// Mkdir creates a directory and any missing parents.
func (w *Workspace) Mkdir(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	return w.mkdirAll(p)
}

// Remove deletes a file or directory tree.
func (w *Workspace) Remove(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	if p == "." {
		return fmt.Errorf("%w: cannot remove the workspace root", ErrInvalidPath)
	}
	return w.root.RemoveAll(p)
}

// Exists reports whether a workspace-relative path exists (without following
// symlinks that leave the workspace; those report an error, not true).
func (w *Workspace) Exists(p string) (bool, error) {
	p, err := clean(p)
	if err != nil {
		return false, err
	}
	if _, err := w.root.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// maxChownEntries bounds ownership repair walks.
const maxChownEntries = 200000

// Chown sets ownership of the workspace root and everything below it without
// following symlinks. It requires privilege unless uid/gid equal the caller's.
func (w *Workspace) Chown(uid, gid int) error {
	n := 0
	return fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n++; n > maxChownEntries {
			return errors.New("workspace has too many entries to repair ownership")
		}
		return w.root.Lchown(p, uid, gid)
	})
}

// Path returns the absolute host path of a bot workspace for use as a bind
// mount source. The entry must be a real directory.
func (m *Manager) Path(botID string) (string, error) {
	if err := checkID(botID); err != nil {
		return "", err
	}
	st, err := m.root.Lstat(botID)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("workspace %s is not a directory", botID)
	}
	return filepath.Join(m.root.Name(), botID), nil
}

// Prepare makes everything in the workspace owned by the container user so a
// nonroot process can write it. Only entries whose owner differs are changed,
// so it is cheap when nothing drifted. It needs CAP_CHOWN unless uid:gid is the
// caller's own, and then fails clearly instead of leaving a broken workspace.
func (m *Manager) Prepare(botID string, uid, gid int) error {
	w, err := m.Open(botID)
	if err != nil {
		return err
	}
	defer w.Close()
	n := 0
	return fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n++; n > maxChownEntries {
			return errors.New("workspace has too many entries to repair ownership")
		}
		info, err := w.root.Lstat(p)
		if err != nil {
			return err
		}
		if sys, ok := info.Sys().(*syscall.Stat_t); ok && int(sys.Uid) == uid && int(sys.Gid) == gid {
			return nil
		}
		return w.root.Lchown(p, uid, gid)
	})
}

// Exists reports whether a path exists inside a bot's workspace.
func (m *Manager) Exists(botID, rel string) (bool, error) {
	w, err := m.Open(botID)
	if err != nil {
		return false, err
	}
	defer w.Close()
	return w.Exists(rel)
}

// OpenFile returns a regular file for streaming reads together with its size.
// Directories, devices and symlinks leaving the workspace are refused.
func (w *Workspace) OpenFile(p string) (*os.File, int64, error) {
	p, err := clean(p)
	if err != nil {
		return nil, 0, err
	}
	f, err := w.root.Open(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("%w: not a regular file", ErrInvalidPath)
	}
	return f, st.Size(), nil
}

// Revision identifies the current content of a regular file: inode, size and
// modification time. Any write through the panel (atomic replace), SFTP or a
// deployment changes it, so an editor can detect that its copy is stale.
func (w *Workspace) Revision(p string) (string, error) {
	p, err := clean(p)
	if err != nil {
		return "", err
	}
	st, err := w.root.Lstat(p)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%w: not a regular file", ErrInvalidPath)
	}
	return revisionOf(st), nil
}

func revisionOf(st fs.FileInfo) string {
	var ino uint64
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		ino = sys.Ino
	}
	return fmt.Sprintf("%x-%x-%x", ino, st.Size(), st.ModTime().UnixNano())
}

// RevisionSize returns the file size recorded in a revision (see
// revisionOf), so a caller that wrote a file elsewhere can check that the
// whole file arrived.
func RevisionSize(rev string) (int64, bool) {
	parts := strings.Split(rev, "-")
	if len(parts) != 3 {
		return 0, false
	}
	n, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// RevisionOf returns the revision of an open file.
func RevisionOf(f *os.File) (string, error) {
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return revisionOf(st), nil
}

// Rename moves a file or directory within the workspace, refusing to replace
// an existing directory.
func (w *Workspace) Rename(from, to string) error {
	from, err := clean(from)
	if err != nil {
		return err
	}
	to, err = clean(to)
	if err != nil {
		return err
	}
	if from == "." || to == "." {
		return fmt.Errorf("%w: cannot move the workspace root", ErrInvalidPath)
	}
	if from == to {
		return nil
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return fmt.Errorf("%w: cannot move a directory into itself", ErrInvalidPath)
	}
	if dir := path.Dir(to); dir != "." {
		if err := w.mkdirAll(dir); err != nil {
			return err
		}
	}
	return w.root.Rename(from, to)
}

// Stat describes a path without following a symlink out of the workspace.
func (w *Workspace) Stat(p string) (Entry, error) {
	if p != "." {
		var err error
		if p, err = clean(p); err != nil {
			return Entry{}, err
		}
	}
	st, err := w.root.Lstat(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: path.Base(p), IsDir: st.IsDir(), Size: st.Size()}, nil
}

// OpenWrite opens a regular file for offset writes (SFTP), creating it and any
// parents. It is not atomic, unlike Write; callers use it only for protocols
// that stream writes by offset. flags may contain O_TRUNC, O_APPEND and O_EXCL.
func (w *Workspace) OpenWrite(p string, flags int) (*os.File, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	if p == "." {
		return nil, fmt.Errorf("%w: not a file", ErrInvalidPath)
	}
	if dir := path.Dir(p); dir != "." {
		if err := w.mkdirAll(dir); err != nil {
			return nil, err
		}
	}
	flags &= os.O_TRUNC | os.O_APPEND | os.O_EXCL
	_, statErr := w.root.Lstat(p)
	f, err := w.root.OpenFile(p, os.O_WRONLY|os.O_CREATE|flags, 0o640)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%w: not a regular file", ErrInvalidPath)
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		w.chown(p)
	}
	return f, nil
}

// Lstat returns file info for p (workspace root for "."), not following a final symlink.
func (w *Workspace) Lstat(p string) (fs.FileInfo, error) {
	if p != "." {
		var err error
		if p, err = clean(p); err != nil {
			return nil, err
		}
	}
	return w.root.Lstat(p)
}

// ListInfo returns FileInfo for each entry of a directory.
func (w *Workspace) ListInfo(p string) ([]fs.FileInfo, error) {
	if p != "." {
		var err error
		if p, err = clean(p); err != nil {
			return nil, err
		}
	}
	d, err := w.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	des, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(des))
	for _, de := range des {
		if info, err := de.Info(); err == nil {
			out = append(out, info)
		}
	}
	return out, nil
}

// RemoveOne deletes a single file or an empty directory.
func (w *Workspace) RemoveOne(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	if p == "." {
		return fmt.Errorf("%w: cannot remove the workspace root", ErrInvalidPath)
	}
	return w.root.Remove(p)
}

// MkdirOne creates one directory; the parent must exist.
func (w *Workspace) MkdirOne(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	if p == "." {
		return fs.ErrExist
	}
	if err := w.root.Mkdir(p, 0o750); err != nil {
		return err
	}
	w.chown(p)
	return nil
}

// maxUsageEntries bounds a disk-usage walk.
const maxUsageEntries = 500000

// Usage returns the bytes of regular files in a bot's workspace (symlinks are
// not followed). It fails with ErrTooLarge past maxUsageEntries files.
func (m *Manager) Usage(botID string) (int64, error) {
	w, err := m.Open(botID)
	if err != nil {
		return 0, err
	}
	defer w.Close()
	var total int64
	n := 0
	err = fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // raced with a delete
			}
			return err
		}
		if n++; n > maxUsageEntries {
			return ErrTooLarge
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// Statfs reports total and free bytes of the filesystem holding the data root.
func (m *Manager) Statfs() (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.path, &st); err != nil {
		return 0, 0, err
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
}

// Inodes reports total and free inodes of the data root's filesystem (0, 0
// when the filesystem does not track them).
func (m *Manager) Inodes() (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.path, &st); err != nil {
		return 0, 0, err
	}
	return st.Files, st.Ffree, nil
}

// ProbeOwnership checks, with a throwaway file in the data root, whether this
// process can give files to uid:gid (what Prepare does to every workspace).
func (m *Manager) ProbeOwnership(uid, gid int) error {
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	name := ".probe-" + hex.EncodeToString(rnd[:])
	f, err := m.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	defer m.root.Remove(name)
	return m.root.Lchown(name, uid, gid)
}

// ReadFile reads one file of a bot's workspace (at most max bytes).
func (m *Manager) ReadFile(botID, rel string, max int64) ([]byte, error) {
	w, err := m.Open(botID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	return w.Read(rel, max)
}

// WriteFile atomically replaces one file of a bot's workspace from r. When r
// fails (for example a download whose checksum does not match), the original
// file is left untouched.
func (m *Manager) WriteFile(botID, rel string, r io.Reader, max int64) error {
	w, err := m.Open(botID)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Write(rel, r, max)
}
