package filesystem

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
)

// SQLiteFile is a SQLite database found in a workspace.
type SQLiteFile struct {
	Path    string // workspace-relative, forward slashes
	Size    int64
	ModTime time.Time
}

// SQLite discovery bounds: the walk is cheap and predictable even in large
// workspaces (dependency trees are skipped, and only files with a database
// extension are opened to check their header).
const (
	sqliteMaxVisits = 20000
	sqliteMaxDepth  = 8
)

var (
	sqliteHeader = []byte("SQLite format 3\x00")
	sqliteExts   = map[string]bool{".db": true, ".sqlite": true, ".sqlite3": true, ".db3": true, ".s3db": true, ".sl3": true}
	sqliteSkip   = map[string]bool{"node_modules": true, ".git": true, ".venv": true, "venv": true, "__pycache__": true,
		".cache": true, ".npm": true, ".m2": true, ".gradle": true, "vendor": true, "target": true, "site-packages": true}
	// ErrNotSQLite is returned for a path that is not a SQLite database.
	ErrNotSQLite = errors.New("not a SQLite database")
)

// IsSQLiteName reports whether a file name has a SQLite database extension.
func IsSQLiteName(name string) bool {
	return sqliteExts[strings.ToLower(path.Ext(name))]
}

// FindSQLite lists up to max SQLite databases in the workspace, sorted by
// path. Symbolic links are not followed and dependency folders are skipped.
func (w *Workspace) FindSQLite(max int) ([]SQLiteFile, error) {
	var out []SQLiteFile
	visits := 0
	err := fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == "." {
				return err
			}
			return nil // unreadable entry: skip it, keep looking
		}
		visits++
		if visits > sqliteMaxVisits || len(out) >= max {
			return fs.SkipAll
		}
		if d.IsDir() {
			if p != "." && (sqliteSkip[d.Name()] || strings.Count(p, "/") >= sqliteMaxDepth) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !IsSQLiteName(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < int64(len(sqliteHeader)) {
			return nil
		}
		if ok, _ := w.hasSQLiteHeader(p); ok {
			out = append(out, SQLiteFile{Path: p, Size: info.Size(), ModTime: info.ModTime()})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func (w *Workspace) hasSQLiteHeader(p string) (bool, error) {
	f, err := w.root.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(sqliteHeader))
	if _, err := io.ReadFull(f, head); err != nil {
		return false, nil
	}
	return bytes.Equal(head, sqliteHeader), nil
}

// SQLiteOwner checks that p is a regular file (not a symbolic link) holding a
// SQLite database and returns its owner, so a client can open it as the
// user the app writes it as. It returns the cleaned path.
func (w *Workspace) SQLiteOwner(p string) (rel string, uid, gid int, err error) {
	if rel, err = clean(p); err != nil {
		return "", 0, 0, err
	}
	st, err := w.root.Lstat(rel)
	if err != nil {
		return "", 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, 0, fmt.Errorf("%w: not a regular file", ErrInvalidPath)
	}
	ok, err := w.hasSQLiteHeader(rel)
	if err != nil {
		return "", 0, 0, err
	}
	if !ok {
		return "", 0, 0, ErrNotSQLite
	}
	uid, gid = -1, -1
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		uid, gid = int(sys.Uid), int(sys.Gid)
	}
	return rel, uid, gid, nil
}
