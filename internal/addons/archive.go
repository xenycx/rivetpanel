package addons

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// MaxArchiveBytes bounds the add-on data one move copies (uncompressed).
const MaxArchiveBytes int64 = 64 << 30

// ErrArchive is an add-on data archive the node refuses; its message is safe
// to show.
type ErrArchive struct{ Msg string }

func (e *ErrArchive) Error() string { return e.Msg }

// WriteTarGz streams every add-on data directory of a bot as a gzipped tar:
// entries are <kind>/<path>. The add-ons must be stopped (their bot is
// stopped), so the files are a consistent copy. A bot without add-on data
// writes an empty archive.
func (d DataRoot) WriteTarGz(botID string, dst io.Writer) (int64, error) {
	dir, err := d.botDir(botID)
	if err != nil {
		return 0, err
	}
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	var total int64
	err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == dir {
				return filepath.SkipDir
			}
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := Get(strings.SplitN(rel, "/", 2)[0]); !ok {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil // not an add-on's data
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		var link string
		switch {
		case info.Mode().IsRegular(), info.IsDir():
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		default:
			return nil // sockets, pipes and lock files are recreated by the database
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name, h.Uname, h.Gname = rel, "", ""
		if info.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if total += info.Size(); total > MaxArchiveBytes {
			return &ErrArchive{fmt.Sprintf("the add-on data is larger than %d GiB", MaxArchiveBytes>>30)}
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, info.Size())
		return err
	})
	if err != nil {
		return total, err
	}
	if err := tw.Close(); err != nil {
		return total, err
	}
	return total, gz.Close()
}

// Restore replaces every add-on data directory of a bot with an archive
// written by WriteTarGz. Files are given to uid:gid, the user the node runs
// add-on containers as (it may differ between nodes). The archive is
// extracted beside the data first and swapped in only when it is complete,
// so a broken transfer leaves the existing data untouched.
func (d DataRoot) Restore(botID string, src io.Reader, uid, gid int) (int64, error) {
	dir, err := d.botDir(botID)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return 0, err
	}
	stage := filepath.Join(d.Dir, ".restore-"+botID+"-"+uuid.NewString()[:8])
	if err := os.Mkdir(stage, 0o700); err != nil {
		return 0, err
	}
	n, err := extract(stage, src, uid, gid)
	if err != nil {
		_ = removeAll(stage)
		return n, err
	}
	if err := removeAll(dir); err != nil {
		_ = removeAll(stage)
		return n, err
	}
	if err := os.Rename(stage, dir); err != nil {
		_ = removeAll(stage)
		return n, err
	}
	return n, nil
}

func extract(root string, src io.Reader, uid, gid int) (int64, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return 0, &ErrArchive{"the add-on data archive is not a gzip file"}
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return total, &ErrArchive{"the add-on data archive is damaged"}
		}
		name := path.Clean(strings.TrimSuffix(h.Name, "/"))
		if name == "." || strings.HasPrefix(name, "../") || name == ".." || path.IsAbs(name) {
			return total, &ErrArchive{"the add-on data archive has an unsafe path"}
		}
		if _, ok := Get(strings.SplitN(name, "/", 2)[0]); !ok {
			return total, &ErrArchive{"the add-on data archive holds an unknown add-on"}
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return total, err
		}
		mode := fs.FileMode(h.Mode) & fs.ModePerm
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return total, err
			}
			dirs = append(dirs, dirMode{target, mode})
		case tar.TypeReg:
			if total += h.Size; total > MaxArchiveBytes {
				return total, &ErrArchive{fmt.Sprintf("the add-on data is larger than %d GiB", MaxArchiveBytes>>30)}
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode|0o600)
			if err != nil {
				return total, err
			}
			_, err = io.CopyN(f, tr, h.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return total, err
			}
			if err := os.Chmod(target, mode); err != nil {
				return total, err
			}
		case tar.TypeSymlink:
			// Database data holds no links that leave its own directory.
			if path.IsAbs(h.Linkname) || strings.HasPrefix(path.Clean(path.Join(path.Dir(name), h.Linkname)), "../") {
				return total, &ErrArchive{"the add-on data archive has a link that leaves its directory"}
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return total, err
			}
		default:
			continue
		}
		if err := os.Lchown(target, uid, gid); err != nil {
			return total, fmt.Errorf("add-on data cannot be given to the container user %d:%d: %w", uid, gid, err)
		}
	}
	// Directories get their own modes last: a database's read-only
	// directory would otherwise refuse the files written into it.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return total, err
		}
	}
	return total, nil
}

// OwnedData is a node's add-on data root with the host user its add-on
// containers write as (the runner's workspace owner), which restored files
// are given to.
type OwnedData struct {
	DataRoot
	UID, GID int
}

// Archive streams a bot's add-on data (see WriteTarGz).
func (o OwnedData) Archive(botID string, dst io.Writer) (int64, error) {
	return o.WriteTarGz(botID, dst)
}

// Replace swaps in a bot's add-on data from an archive (see Restore).
func (o OwnedData) Replace(botID string, src io.Reader) (int64, error) {
	return o.Restore(botID, src, o.UID, o.GID)
}
