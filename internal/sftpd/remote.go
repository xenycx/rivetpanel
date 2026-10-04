package sftpd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"

	"github.com/xenycx/rivetpanel/internal/domain"
	"github.com/xenycx/rivetpanel/internal/filesystem"
)

// NodeFiles reaches the workspace of a server that runs on a remote node
// through its rivet-agent's file routes (noderoute.Router). The panel keeps
// authentication, the per-user permission check, the size limits and the
// deployment/restore lock; path containment is enforced by the node's
// filesystem layer. A missing path is fs.ErrNotExist.
type NodeFiles interface {
	ListDir(ctx context.Context, nodeID, botID, dir string) ([]filesystem.Entry, error)
	// ReadFileTo streams at most max bytes of one file into dst.
	ReadFileTo(ctx context.Context, nodeID, botID, p string, max int64, dst io.Writer) (string, int64, error)
	// WriteFileFrom atomically replaces one file with exactly size bytes.
	WriteFileFrom(ctx context.Context, nodeID, botID, p string, src io.Reader, size int64, ifMatch string, createOnly bool) (string, error)
	MakeDir(ctx context.Context, nodeID, botID, p string) error
	Move(ctx context.Context, nodeID, botID, from, to string) error
	// RemoveOne deletes a file, a symlink or an empty directory; the node
	// checks emptiness atomically and never deletes a directory tree.
	RemoveOne(ctx context.Context, nodeID, botID, p string) error
}

// transferTimeout bounds one whole remote download or upload (the spool
// between the client and the node), unlike opTimeout for metadata calls.
const transferTimeout = 30 * time.Minute

// remoteOf reports whether the bot's files live on a remote node and returns
// the access to them. An offline node (or a panel without remote file access)
// is a clean error; the panel's own disk is never used for a remote bot.
func (h *handler) remoteOf(t target) (NodeFiles, bool, error) {
	if h.s.Remote == nil {
		// Fail closed: without the remote check no bot's location is known.
		return nil, true, errors.New("SFTP is not configured for remote nodes")
	}
	nf, remote, err := h.s.Remote(t.bot)
	if !remote {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if nf == nil {
		return nil, true, errors.New("SFTP is not available for servers on remote nodes on this panel")
	}
	return nf, true, nil
}

// remoteInfo is the FileInfo of an entry a node listed. Nodes report no
// modification time or permission bits; they are shown as fixed values.
type remoteInfo struct{ e filesystem.Entry }

func (r remoteInfo) Name() string { return r.e.Name }
func (r remoteInfo) Size() int64  { return r.e.Size }
func (r remoteInfo) Mode() fs.FileMode {
	switch {
	case r.e.Symlink:
		return fs.ModeSymlink | 0o777
	case r.e.IsDir:
		return fs.ModeDir | 0o750
	}
	return 0o640
}
func (r remoteInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (r remoteInfo) IsDir() bool        { return r.e.IsDir && !r.e.Symlink }
func (r remoteInfo) Sys() any           { return nil }

// remoteStat finds one entry by listing its parent directory on the node.
func remoteStat(ctx context.Context, nf NodeFiles, t target) (filesystem.Entry, error) {
	es, err := nf.ListDir(ctx, t.bot.NodeID, t.bot.ID, path.Dir(t.rel))
	if err != nil {
		return filesystem.Entry{}, err
	}
	name := path.Base(t.rel)
	for _, e := range es {
		if e.Name == name {
			return e, nil
		}
	}
	return filesystem.Entry{}, fs.ErrNotExist
}

// spool is a private, already unlinked temporary file that holds one remote
// transfer between the SFTP client and the node.
func (h *handler) spool() (*os.File, error) {
	f, err := os.CreateTemp(h.s.SpoolDir, "rivetpanel-sftp-*")
	if err != nil {
		return nil, fmt.Errorf("spool transfer: %w", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, fmt.Errorf("spool transfer: %w", err)
	}
	return f, nil
}

func (h *handler) maxFile() int64 {
	if h.s.MaxFile > 0 {
		return h.s.MaxFile
	}
	return 256 << 20
}

func (h *handler) remoteRead(t target, nf NodeFiles) (io.ReaderAt, error) {
	f, err := h.spool()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(h.ctx, transferTimeout)
	defer cancel()
	if _, _, err := nf.ReadFileTo(ctx, t.bot.NodeID, t.bot.ID, t.rel, h.maxFile(), f); err != nil {
		f.Close()
		if errors.Is(err, filesystem.ErrTooLarge) {
			return nil, fmt.Errorf("file exceeds the %d byte limit", h.maxFile())
		}
		return nil, mapErr(err)
	}
	return &guardedFile{f: f, h: h, bot: t.bot.ID}, nil
}

func (h *handler) remoteWrite(ctx context.Context, t target, nf NodeFiles, pf sftp.FileOpenFlags) (io.WriterAt, error) {
	cur, err := remoteStat(ctx, nf, t)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, mapErr(err)
	}
	switch {
	case exists && (cur.IsDir || cur.Symlink):
		return nil, sftp.ErrSSHFxFailure
	case !exists && !pf.Creat:
		return nil, sftp.ErrSSHFxNoSuchFile
	case exists && pf.Excl && pf.Creat:
		return nil, sftp.ErrSSHFxFailure
	}
	max := h.maxFile()
	f, err := h.spool()
	if err != nil {
		return nil, err
	}
	ifMatch := ""
	if exists && !pf.Trunc {
		// A partial write or an append keeps the rest of the file: start
		// from the node's copy and only replace it if nobody changed it since.
		rctx, cancel := context.WithTimeout(h.ctx, transferTimeout)
		rev, _, err := nf.ReadFileTo(rctx, t.bot.NodeID, t.bot.ID, t.rel, max, f)
		cancel()
		if err != nil {
			f.Close()
			if errors.Is(err, filesystem.ErrTooLarge) {
				return nil, fmt.Errorf("file exceeds the %d byte limit", max)
			}
			return nil, mapErr(err)
		}
		ifMatch = rev
	}
	createOnly := !exists && pf.Excl && pf.Creat
	commit := func(f *os.File) error {
		if err := h.allowedNow(t.bot.ID); err != nil {
			return err
		}
		if err := h.s.Bots.FilesBlocked(t.bot.ID); err != nil {
			return sftp.ErrSSHFxFailure
		}
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.Size() > max {
			return fmt.Errorf("file exceeds the %d byte limit", max)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(h.ctx, transferTimeout)
		defer cancel()
		if _, err := nf.WriteFileFrom(wctx, t.bot.NodeID, t.bot.ID, t.rel, f, st.Size(), ifMatch, createOnly); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				return errors.New("the file changed on the server while it was being written; nothing was saved")
			}
			if errors.Is(err, filesystem.ErrTooLarge) {
				return errors.New("the node refused the file as too large")
			}
			return mapErr(err)
		}
		return nil
	}
	return &guardedFile{f: f, h: h, bot: t.bot.ID, max: max, commit: commit}, nil
}

func (h *handler) remoteCmd(ctx context.Context, r *sftp.Request, t target, nf NodeFiles) error {
	node, bot := t.bot.NodeID, t.bot.ID
	switch r.Method {
	case "Rename":
		dst, err := h.resolve(ctx, r.Target)
		if err != nil {
			return mapErr(err)
		}
		if dst.root || dst.botRoot() || dst.bot.ID != t.bot.ID {
			return sftp.ErrSSHFxPermissionDenied // never across bots
		}
		if e, err := remoteStat(ctx, nf, dst); err == nil && e.IsDir {
			return sftp.ErrSSHFxFailure
		}
		if _, err := remoteStat(ctx, nf, t); err != nil {
			return mapErr(err)
		}
		return mapErr(nf.Move(ctx, node, bot, t.rel, dst.rel))
	case "Remove":
		e, err := remoteStat(ctx, nf, t)
		if err != nil {
			return mapErr(err)
		}
		if e.IsDir && !e.Symlink {
			return sftp.ErrSSHFxFailure
		}
		return mapErr(nf.RemoveOne(ctx, node, bot, t.rel))
	case "Rmdir":
		e, err := remoteStat(ctx, nf, t)
		if err != nil {
			return mapErr(err)
		}
		if !e.IsDir || e.Symlink {
			return sftp.ErrSSHFxFailure
		}
		// Non-recursive: the node's rmdir refuses a directory that is not
		// empty at that moment, so a file added concurrently is never lost.
		return mapErr(nf.RemoveOne(ctx, node, bot, t.rel))
	case "Mkdir":
		// SFTP mkdir creates one directory whose parent exists; the agent's
		// route would also create parents, so check first.
		es, err := nf.ListDir(ctx, node, bot, path.Dir(t.rel))
		if err != nil {
			return mapErr(err)
		}
		for _, e := range es {
			if e.Name == path.Base(t.rel) {
				return sftp.ErrSSHFxFailure
			}
		}
		return mapErr(nf.MakeDir(ctx, node, bot, t.rel))
	}
	return sftp.ErrSSHFxOpUnsupported
}

func (h *handler) remoteList(ctx context.Context, r *sftp.Request, t target, nf NodeFiles) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		es, err := nf.ListDir(ctx, t.bot.NodeID, t.bot.ID, t.rel)
		if err != nil {
			return nil, mapErr(err)
		}
		out := make(listerAt, 0, len(es))
		for _, e := range es {
			out = append(out, remoteInfo{e})
		}
		return out, nil
	case "Stat":
		e, err := remoteStat(ctx, nf, t)
		if err != nil {
			return nil, mapErr(err)
		}
		return listerAt{remoteInfo{e}}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

// remote authorizes the user for the bot (as open does for local bots) and
// reports whether the bot is on a remote node. Errors are SFTP-ready.
func (h *handler) remote(ctx context.Context, t target) (NodeFiles, bool, error) {
	if err := h.allowed(ctx, t.bot.ID); err != nil {
		return nil, true, err
	}
	nf, remote, err := h.remoteOf(t)
	if !remote {
		return nil, false, nil
	}
	return nf, true, err
}
