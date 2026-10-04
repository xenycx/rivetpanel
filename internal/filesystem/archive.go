package filesystem

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// MetaDir holds panel metadata (environment snapshot) inside backup archives.
// It is never written to the workspace.
const MetaDir = ".rivetpanel"

// BackupLimits bound archive creation and restoration.
type BackupLimits struct {
	MaxEntries int
	MaxBytes   int64 // total uncompressed file bytes
	MaxFile    int64
}

// DefaultBackupLimits allow large projects while stopping runaway archives.
var DefaultBackupLimits = BackupLimits{MaxEntries: 200000, MaxBytes: 2 << 30, MaxFile: 1 << 30}

// rebuildable top-level directories are excluded from backups: they are
// recreated by the build step and can be huge.
var backupSkipTop = map[string]bool{
	"node_modules": true, ".venv": true, "target": true, ".cargo": true, ".gopath": true, ".cache": true, ".npm": true,
}

func skipBackup(p string, isDir bool) bool {
	top, _, nested := strings.Cut(p, "/")
	if !nested && backupSkipTop[top] {
		return true
	}
	base := path.Base(p)
	if isDir && base == "node_modules" { // any depth
		return true
	}
	return strings.HasPrefix(base, ".tmp-") // leftovers of interrupted atomic writes
}

// WriteTarGz streams the workspace as a gzip-compressed tar. Symlinks are
// stored as symlinks (never followed); extra adds in-memory files under
// MetaDir. It returns the number of entries and total file bytes.
func (w *Workspace) WriteTarGz(dst io.Writer, extra map[string][]byte, lim BackupLimits) (entries int, size int64, err error) {
	gz, _ := gzip.NewWriterLevel(dst, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	walkErr := fs.WalkDir(w.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // vanished while running
			}
			return err
		}
		if p == "." {
			return nil
		}
		if skipBackup(p, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entries++; entries > lim.MaxEntries {
			return reject("workspace has too many files to back up (limit %d)", lim.MaxEntries)
		}
		info, err := w.root.Lstat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		hdr := &tar.Header{Name: p, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
		switch {
		case info.IsDir():
			hdr.Typeflag, hdr.Name = tar.TypeDir, p+"/"
			return tw.WriteHeader(hdr)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := w.root.Readlink(p)
			if err != nil {
				return nil
			}
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, target
			return tw.WriteHeader(hdr)
		case info.Mode().IsRegular():
			if info.Size() > lim.MaxFile {
				return reject("file %q is too large to back up", p)
			}
			f, err := w.root.Open(p)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			defer f.Close()
			hdr.Typeflag, hdr.Size = tar.TypeReg, info.Size()
			if size += info.Size(); size > lim.MaxBytes {
				return reject("workspace is too large to back up (limit %d bytes)", lim.MaxBytes)
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			// The file may grow while it is read; never write past the header size.
			_, err = io.Copy(tw, io.LimitReader(f, info.Size()))
			return err
		}
		return nil // sockets, devices, fifos are not backed up
	})
	if walkErr != nil {
		return 0, 0, walkErr
	}
	for name, data := range extra {
		if name == "" || path.Clean(name) != name || !filepath.IsLocal(name) || strings.ContainsAny(name, "\\\x00") {
			return 0, 0, reject("invalid backup metadata name")
		}
		if len(data) > maxMetaBytes {
			return 0, 0, reject("backup metadata entry is too large")
		}
		hdr := &tar.Header{Name: path.Join(MetaDir, name), Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, 0, err
		}
		if _, err := tw.Write(data); err != nil {
			return 0, 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, 0, err
	}
	return entries, size, gz.Close()
}

const maxMetaBytes = 1 << 20

// RestoreTarGz replaces the workspace contents with the archive. The archive is
// fully validated and unpacked into a private staging directory first, so a
// bad archive changes nothing. Only then are existing entries (other than
// rebuildable directories) removed and the staged ones moved in. Entries under
// MetaDir are returned in memory instead of being written.
func (w *Workspace) RestoreTarGz(src io.Reader, lim BackupLimits) (meta map[string][]byte, err error) {
	meta, c, err := w.RestoreTarGzCommit(src, lim)
	if err != nil {
		return nil, err
	}
	return meta, c.Finish()
}

// RestoreTarGzCommit is RestoreTarGz that leaves the previous contents in
// place until the caller calls Finish (after its database update) or Rollback.
// A crash before Finish is rolled back by Manager.Recover.
func (w *Workspace) RestoreTarGzCommit(src io.Reader, lim BackupLimits) (meta map[string][]byte, commit *Commit, err error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, nil, reject("not a gzip archive")
	}
	defer gz.Close()
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, nil, err
	}
	stage := ".restore-" + hex.EncodeToString(rnd[:])
	if err := w.root.Mkdir(stage, 0o700); err != nil {
		return nil, nil, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			w.root.RemoveAll(stage)
		}
	}()
	stageRoot, err := w.root.OpenRoot(stage)
	if err != nil {
		return nil, nil, err
	}
	defer stageRoot.Close()

	meta = map[string][]byte{}
	tr := tar.NewReader(gz)
	var entries int
	var total int64
	type link struct{ name, target string }
	var links []link
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, reject("archive is corrupt")
		}
		if entries++; entries > lim.MaxEntries {
			return nil, nil, reject("archive has too many entries")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return nil, nil, reject("unsafe path in archive: %q", hdr.Name)
		}
		name = path.Clean(name)
		if !filepath.IsLocal(name) {
			return nil, nil, reject("unsafe path in archive: %q", hdr.Name)
		}
		if top, _, _ := strings.Cut(name, "/"); strings.HasPrefix(top, ".restore-") {
			return nil, nil, reject("reserved name in archive: %q", hdr.Name)
		}
		if name == MetaDir || strings.HasPrefix(name, MetaDir+"/") {
			if hdr.Typeflag == tar.TypeReg {
				if hdr.Size > maxMetaBytes {
					return nil, nil, reject("metadata entry is too large")
				}
				b, err := io.ReadAll(io.LimitReader(tr, maxMetaBytes))
				if err != nil {
					return nil, nil, reject("archive is corrupt")
				}
				meta[strings.TrimPrefix(name, MetaDir+"/")] = b
			}
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllIn(stageRoot, name); err != nil {
				return nil, nil, err
			}
		case tar.TypeReg:
			if hdr.Size > lim.MaxFile {
				return nil, nil, reject("file %q exceeds the per-file limit", hdr.Name)
			}
			if total += hdr.Size; total > lim.MaxBytes {
				return nil, nil, reject("archive exceeds the size limit")
			}
			if dir := path.Dir(name); dir != "." {
				if err := mkdirAllIn(stageRoot, dir); err != nil {
					return nil, nil, err
				}
			}
			mode := os.FileMode(hdr.Mode).Perm() // drops setuid/setgid/sticky
			f, err := stageRoot.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode|0o600)
			if err != nil {
				return nil, nil, err
			}
			n, err := io.Copy(f, io.LimitReader(tr, hdr.Size))
			cerr := f.Close()
			if err != nil || n != hdr.Size || cerr != nil {
				return nil, nil, reject("archive is corrupt")
			}
		case tar.TypeSymlink:
			// Only relative links that stay inside the workspace are restored.
			t := hdr.Linkname
			if t == "" || strings.HasPrefix(t, "/") || strings.ContainsRune(t, 0) ||
				!filepath.IsLocal(path.Join(path.Dir(name), t)) {
				continue // skip unsafe links rather than fail a whole restore
			}
			links = append(links, link{name, t})
		default:
			return nil, nil, reject("archive contains a special file (%q); not allowed", hdr.Name)
		}
	}
	for _, l := range links { // after files, so a link never shadows a later entry
		if dir := path.Dir(l.name); dir != "." {
			if err := mkdirAllIn(stageRoot, dir); err != nil {
				return nil, nil, err
			}
		}
		if err := stageRoot.Symlink(l.target, l.name); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, nil, err
		}
	}

	// Commit, journaled: move every current entry (except rebuildable
	// directories the archive does not replace) aside, then move the staged
	// entries in. Nothing is deleted until Finish.
	top, err := fs.ReadDir(w.root.FS(), ".")
	if err != nil {
		return nil, nil, err
	}
	staged, err := fs.ReadDir(stageRoot.FS(), ".")
	if err != nil {
		return nil, nil, err
	}
	incoming := map[string]bool{}
	in := make([]string, 0, len(staged))
	for _, e := range staged {
		incoming[e.Name()] = true
		in = append(in, e.Name())
	}
	old := ".restore-old-" + hex.EncodeToString(rnd[:])
	c, err := w.m.begin(w.root, w.id, "restore", stage, old, in)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (map[string][]byte, *Commit, error) {
		keepStage = errors.Is(err, errSimulatedCrash) // a dead process removes nothing
		if rerr := c.Rollback(); rerr != nil {
			return nil, nil, errors.Join(err, rerr)
		}
		return nil, nil, err
	}
	for _, e := range top {
		n := e.Name()
		if n == stage || n == old || strings.HasPrefix(n, ".restore-") || strings.HasPrefix(n, ".deploy-") ||
			(backupSkipTop[n] && !incoming[n]) {
			continue
		}
		if err := c.moveAside(n); err != nil {
			return fail(err)
		}
	}
	for _, n := range in {
		if err := c.moveIn(n); err != nil {
			return fail(err)
		}
	}
	if err := c.swapped(); err != nil {
		return fail(err)
	}
	if w.uid >= 0 {
		for _, n := range in {
			_ = fs.WalkDir(w.root.FS(), n, func(p string, d fs.DirEntry, err error) error {
				if err == nil {
					_ = w.root.Lchown(p, w.uid, w.gid)
				}
				return nil
			})
		}
	}
	keepStage = true // Finish or Rollback removes it
	return meta, c, nil
}

func mkdirAllIn(r *os.Root, p string) error {
	cur := ""
	for _, part := range strings.Split(p, "/") {
		cur = path.Join(cur, part)
		if err := r.Mkdir(cur, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}

// DeployManifest is the workspace file listing the files of the last deploy,
// so a later deploy can remove files the repository no longer contains while
// leaving everything else (data, .env files, node_modules) alone.
const DeployManifest = ".rivetpanel-deploy"

// DeployTarGz applies a repository tarball (GitHub layout: one top-level
// directory) to the workspace. rootDir selects a subdirectory of the
// repository to deploy ("" or "." for the whole tree). The tarball is fully
// validated and unpacked into a private staging directory first; existing
// files are then replaced one by one, and files that the previous deploy wrote
// but this one does not contain are removed. It returns the file count.
func (w *Workspace) DeployTarGz(src io.Reader, rootDir string, lim BackupLimits) (int, error) {
	return w.DeployTarGzCheck(src, rootDir, lim, nil)
}

// DeployTarGzCheck is DeployTarGz with a gate: check runs after the archive
// is fully unpacked and validated in staging and before any workspace entry
// changes. A non-nil error aborts with the workspace untouched.
func (w *Workspace) DeployTarGzCheck(src io.Reader, rootDir string, lim BackupLimits, check func() error) (int, error) {
	n, c, err := w.DeployTarGzCommit(src, rootDir, lim, check)
	if err != nil {
		return 0, err
	}
	return n, c.Finish()
}

// DeployTarGzCommit is DeployTarGzCheck that keeps the replaced files until
// the caller calls Finish (after recording the deployment) or Rollback. A
// crash before Finish is rolled back by Manager.Recover.
func (w *Workspace) DeployTarGzCommit(src io.Reader, rootDir string, lim BackupLimits, check func() error) (int, *Commit, error) {
	rootDir = strings.Trim(path.Clean("/"+strings.TrimSpace(rootDir)), "/")
	if rootDir != "" && !filepath.IsLocal(rootDir) {
		return 0, nil, reject("invalid root directory")
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return 0, nil, reject("not a gzip archive")
	}
	defer gz.Close()
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return 0, nil, err
	}
	stage := ".deploy-" + hex.EncodeToString(rnd[:])
	if err := w.root.Mkdir(stage, 0o700); err != nil {
		return 0, nil, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			w.root.RemoveAll(stage)
		}
	}()
	sr, err := w.root.OpenRoot(stage)
	if err != nil {
		return 0, nil, err
	}
	defer sr.Close()

	type link struct{ name, target string }
	var links []link
	newFiles := map[string]bool{}
	var entries int
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, nil, reject("archive is corrupt")
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if entries++; entries > lim.MaxEntries {
			return 0, nil, reject("repository has too many files (limit %d)", lim.MaxEntries)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return 0, nil, reject("unsafe path in archive: %q", hdr.Name)
		}
		for _, c := range strings.Split(name, "/") {
			if c == ".." {
				return 0, nil, reject("unsafe path in archive: %q", hdr.Name)
			}
		}
		_, rest, ok := strings.Cut(path.Clean(name), "/") // drop owner-repo-sha/
		if !ok || rest == "" {
			continue // the top-level directory itself
		}
		if rootDir != "" {
			r, ok := strings.CutPrefix(rest, rootDir+"/")
			if !ok {
				continue
			}
			rest = r
		}
		name = path.Clean(rest)
		if !filepath.IsLocal(name) {
			return 0, nil, reject("unsafe path in archive: %q", hdr.Name)
		}
		top, _, _ := strings.Cut(name, "/")
		if name == DeployManifest || strings.HasPrefix(top, ".restore-") || strings.HasPrefix(top, ".deploy-") {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllIn(sr, name); err != nil {
				return 0, nil, err
			}
		case tar.TypeReg:
			if hdr.Size > lim.MaxFile {
				return 0, nil, reject("file %q is too large", name)
			}
			if total += hdr.Size; total > lim.MaxBytes {
				return 0, nil, reject("repository exceeds the size limit")
			}
			if dir := path.Dir(name); dir != "." {
				if err := mkdirAllIn(sr, dir); err != nil {
					return 0, nil, err
				}
			}
			f, err := sr.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(hdr.Mode).Perm()|0o600)
			if err != nil {
				return 0, nil, err
			}
			n, err := io.Copy(f, io.LimitReader(tr, hdr.Size))
			cerr := f.Close()
			if err != nil || n != hdr.Size || cerr != nil {
				return 0, nil, reject("archive is corrupt")
			}
			newFiles[name] = true
		case tar.TypeSymlink:
			t := hdr.Linkname
			if t == "" || strings.HasPrefix(t, "/") || strings.ContainsRune(t, 0) || !filepath.IsLocal(path.Join(path.Dir(name), t)) {
				continue
			}
			links = append(links, link{name, t})
		default:
			// git repositories only hold files, directories and symlinks;
			// anything else (submodule stubs, devices) is ignored.
		}
	}
	for _, l := range links {
		if dir := path.Dir(l.name); dir != "." {
			if err := mkdirAllIn(sr, dir); err != nil {
				return 0, nil, err
			}
		}
		if err := sr.Symlink(l.target, l.name); err != nil && !errors.Is(err, fs.ErrExist) {
			return 0, nil, err
		}
		newFiles[l.name] = true
	}
	if len(newFiles) == 0 {
		if rootDir != "" {
			return 0, nil, reject("root directory %q was not found in the repository", rootDir)
		}
		return 0, nil, reject("the repository is empty")
	}

	if check != nil {
		if err := check(); err != nil {
			return 0, nil, err
		}
	}

	// Commit, journaled. The new manifest is staged like any other file, so a
	// rollback restores the previous one too.
	list := make([]string, 0, len(newFiles))
	for p := range newFiles {
		list = append(list, p)
	}
	sort.Strings(list)
	mb, _ := json.Marshal(list)
	mf, err := sr.OpenFile(DeployManifest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, nil, err
	}
	_, werr := mf.Write(mb)
	if cerr := mf.Close(); werr != nil || cerr != nil {
		return 0, nil, errors.Join(werr, cerr)
	}
	in := append(append([]string(nil), list...), DeployManifest)
	old := ".deploy-old-" + hex.EncodeToString(rnd[:])
	c, err := w.m.begin(w.root, w.id, "deploy", stage, old, in)
	if err != nil {
		return 0, nil, err
	}
	fail := func(err error) (int, *Commit, error) {
		keepStage = errors.Is(err, errSimulatedCrash) // a dead process removes nothing
		if rerr := c.Rollback(); rerr != nil {
			return 0, nil, errors.Join(err, rerr)
		}
		return 0, nil, err
	}
	// Files the previous deploy wrote that are gone now are moved aside.
	var dirsToPrune []string
	if b, err := w.Read(DeployManifest, 8<<20); err == nil {
		var prev []string
		if json.Unmarshal(b, &prev) == nil && len(prev) <= lim.MaxEntries {
			for _, p := range prev {
				p = path.Clean(p)
				if !filepath.IsLocal(p) || newFiles[p] || p == DeployManifest {
					continue
				}
				if st, err := w.root.Lstat(p); err == nil && !st.IsDir() {
					if err := c.moveAside(p); err != nil {
						return fail(err)
					}
					dirsToPrune = append(dirsToPrune, path.Dir(p))
				}
			}
		}
	}
	for _, p := range in {
		if dir := path.Dir(p); dir != "." {
			if err := w.mkdirAll(dir); err != nil {
				return fail(err)
			}
		}
		if st, err := w.root.Lstat(p); err == nil && st.IsDir() {
			return fail(reject("cannot replace directory %q with a file", p))
		}
		if err := c.moveAside(p); err != nil {
			return fail(err)
		}
		if err := c.moveIn(p); err != nil {
			return fail(err)
		}
		w.chown(p)
	}
	if err := c.swapped(); err != nil {
		return fail(err)
	}
	for _, d := range dirsToPrune { // remove now-empty parents, deepest first, best effort
		for d != "." && d != "" {
			if w.root.Remove(d) != nil {
				break
			}
			d = path.Dir(d)
		}
	}
	keepStage = true // Finish or Rollback removes it
	return len(newFiles), c, nil
}

// ReadArchiveMeta scans a backup archive and returns only its MetaDir entries
// (the environment snapshot) without touching any workspace, so a restore can
// validate them before it changes anything.
func ReadArchiveMeta(src io.Reader, lim BackupLimits) (map[string][]byte, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, reject("not a gzip archive")
	}
	defer gz.Close()
	meta := map[string][]byte{}
	tr := tar.NewReader(gz)
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return meta, nil
		}
		if err != nil || n > lim.MaxEntries {
			return nil, reject("archive is corrupt")
		}
		name := path.Clean(strings.TrimSuffix(hdr.Name, "/"))
		if rest, ok := strings.CutPrefix(name, MetaDir+"/"); ok && hdr.Typeflag == tar.TypeReg {
			if hdr.Size > maxMetaBytes {
				return nil, reject("metadata entry is too large")
			}
			b, err := io.ReadAll(io.LimitReader(tr, maxMetaBytes))
			if err != nil {
				return nil, reject("archive is corrupt")
			}
			meta[rest] = b
		}
	}
}
