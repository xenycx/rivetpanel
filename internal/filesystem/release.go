package filesystem

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
)

// Usage counts the regular files under the workspace and their total size.
// Symlinks are counted as files of size zero; nothing is followed.
func (w *Workspace) Usage() (files int, bytes int64, err error) {
	err = fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		files++
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
			}
		}
		return nil
	})
	return files, bytes, err
}

// FlattenSingleDir lifts the contents of a lone top-level directory to the
// root (an archive of "dist/" becomes the site itself). It does nothing when
// the root holds anything besides that one directory. It reports whether it
// flattened.
func (w *Workspace) FlattenSingleDir() (bool, error) {
	entries, err := w.List(".")
	if err != nil {
		return false, err
	}
	if len(entries) != 1 || !entries[0].IsDir || entries[0].Symlink {
		return false, nil
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return false, err
	}
	// Move the directory aside first: it may contain an entry of its own name.
	tmp := ".flatten-" + hex.EncodeToString(rnd[:])
	if err := w.root.Rename(entries[0].Name, tmp); err != nil {
		return false, err
	}
	children, err := w.List(tmp)
	if err != nil {
		return false, err
	}
	for _, c := range children {
		if err := w.root.Rename(path.Join(tmp, c.Name), c.Name); err != nil {
			return false, err
		}
	}
	return true, w.root.RemoveAll(tmp)
}

// CopyTree copies the directories and regular files under sub (a
// workspace-relative directory, "." for the root) into the root of dst, for
// publishing a build's output as a site release. Symlinks are skipped, never
// followed. Limits bound the entries, total bytes and file size; exceeding
// them returns an *ErrArchive.
func (w *Workspace) CopyTree(sub string, dst *Workspace, lim BackupLimits) (files int, bytes int64, err error) {
	if sub != "." {
		if sub, err = clean(sub); err != nil {
			return 0, 0, err
		}
	}
	st, err := w.root.Lstat(sub)
	if err != nil || !st.IsDir() {
		return 0, 0, reject("%s is not a directory", sub)
	}
	src, err := w.root.OpenRoot(sub)
	if err != nil {
		return 0, 0, err
	}
	defer src.Close()
	entries := 0
	err = fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		entries++
		if lim.MaxEntries > 0 && entries > lim.MaxEntries {
			return reject("the output has more than %d files", lim.MaxEntries)
		}
		switch {
		case d.IsDir():
			return dst.mkdirAll(p)
		case !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if lim.MaxFile > 0 && info.Size() > lim.MaxFile {
			return reject("%s is larger than %d MiB", p, lim.MaxFile>>20)
		}
		if lim.MaxBytes > 0 && bytes+info.Size() > lim.MaxBytes {
			return reject("the output is larger than %d MiB", lim.MaxBytes>>20)
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := dst.root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(in, info.Size()+1))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		dst.chown(p)
		files++
		bytes += n
		return nil
	})
	return files, bytes, err
}
