package filesystem

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// LargeExtractLimits apply to archives already in a workspace (worlds,
// modpacks): larger than upload extraction, still bounded.
var LargeExtractLimits = ExtractLimits{MaxEntries: 200000, MaxTotalSize: 8 << 30, MaxFileSize: 4 << 30}

// maxZipRatio rejects zip entries that expand suspiciously (a zip bomb).
const maxZipRatio = 200

// ZipLimits bound archives the panel creates.
type ZipLimits struct {
	MaxEntries int
	MaxBytes   int64 // total uncompressed bytes read
}

// DefaultZipLimits fit large worlds while bounding work.
var DefaultZipLimits = ZipLimits{MaxEntries: 200000, MaxBytes: 16 << 30}

// WriteZip writes the given workspace paths (files or directories) to dst as
// a zip. Symlinks and special files are skipped, never followed. Names in the
// archive are relative to the workspace root.
func (w *Workspace) WriteZip(dst io.Writer, paths []string, lim ZipLimits) (int, error) {
	zw := zip.NewWriter(dst)
	entries := 0
	var total int64
	add := func(p string, d fs.DirEntry) error {
		info, err := w.root.Lstat(p)
		if err != nil {
			return err
		}
		if entries++; entries > lim.MaxEntries {
			return reject("too many files to archive (limit %d)", lim.MaxEntries)
		}
		switch {
		case info.IsDir():
			_, err := zw.CreateHeader(&zip.FileHeader{Name: p + "/", Modified: info.ModTime(), Method: zip.Store})
			return err
		case info.Mode().IsRegular():
			if total += info.Size(); total > lim.MaxBytes {
				return reject("the files are larger than the %d GiB archive limit", lim.MaxBytes>>30)
			}
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name, h.Method = p, zip.Deflate
			h.SetMode(info.Mode().Perm())
			out, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			f, err := w.root.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.CopyN(out, f, info.Size())
			if errors.Is(err, io.EOF) {
				err = nil // shrank while reading
			}
			return err
		}
		return nil // symlinks, sockets, devices
	}
	for _, raw := range paths {
		p, err := clean(raw)
		if err != nil {
			return entries, err
		}
		err = fs.WalkDir(w.root.FS(), p, func(q string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			// The panel's own in-progress files (including the archive being
			// written) are never archived.
			if b := path.Base(q); strings.HasPrefix(b, ".tmp-") || strings.HasPrefix(b, ".extract-") || strings.HasPrefix(b, ".upload-") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return add(q, d)
		})
		if err != nil {
			return entries, err
		}
	}
	return entries, zw.Close()
}

// ArchiveKind reports the archive format of a file name ("" = unsupported).
func ArchiveKind(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".zip"), strings.HasSuffix(l, ".mrpack"):
		return "zip"
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(l, ".tar"):
		return "tar"
	}
	return ""
}

// ExtractArchive unpacks an archive that is already in the workspace into dir.
// Like ExtractZip it stages everything first, so a rejected archive changes
// nothing.
func (w *Workspace) ExtractArchive(rel, dir string, lim ExtractLimits) (int, error) {
	kind := ArchiveKind(rel)
	if kind == "" {
		return 0, reject("only .zip, .tar.gz, .tgz and .tar archives can be extracted")
	}
	f, size, err := w.OpenFile(rel)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if kind == "zip" {
		zr, err := zip.NewReader(f, size)
		if err != nil {
			return 0, reject("not a valid zip archive")
		}
		for _, e := range zr.File {
			if e.CompressedSize64 > 0 && e.UncompressedSize64 > 1<<20 && e.UncompressedSize64/e.CompressedSize64 > maxZipRatio {
				return 0, reject("archive entry %q expands suspiciously (more than %dx)", e.Name, maxZipRatio)
			}
		}
		return w.ExtractZip(zr, dir, lim)
	}
	var r io.Reader = f
	if kind == "tar.gz" {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, reject("not a valid gzip archive")
		}
		defer gz.Close()
		r = gz
	}
	return w.extractTar(tar.NewReader(r), dir, lim)
}

func (w *Workspace) extractTar(tr *tar.Reader, dir string, lim ExtractLimits) (int, error) {
	if dir != "." {
		var err error
		if dir, err = clean(dir); err != nil {
			return 0, err
		}
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return 0, err
	}
	stage := ".extract-" + hex.EncodeToString(rnd[:])
	if err := w.root.Mkdir(stage, 0o700); err != nil {
		return 0, err
	}
	defer w.root.RemoveAll(stage)
	type staged struct {
		name string
		dir  bool
	}
	var list []staged
	var written int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, reject("the archive is damaged: %v", err)
		}
		if n >= lim.MaxEntries {
			return 0, reject("archive has too many entries (limit %d)", lim.MaxEntries)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if name == "" || name == "." {
			continue
		}
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || !filepath.IsLocal(path.Clean(name)) {
			return 0, reject("unsafe path in archive: %q", h.Name)
		}
		name = path.Clean(name)
		dst := path.Join(stage, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := w.root.MkdirAll(dst, 0o750); err != nil {
				return 0, err
			}
			list = append(list, staged{name, true})
		case tar.TypeReg:
			if h.Size > lim.MaxFileSize {
				return 0, reject("file %q exceeds the per-file limit", h.Name)
			}
			if err := w.root.MkdirAll(path.Dir(dst), 0o750); err != nil {
				return 0, err
			}
			perm := fs.FileMode(0o640)
			if h.Mode&0o111 != 0 {
				perm = 0o750
			}
			out, err := w.root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
			if err != nil {
				return 0, err
			}
			c, cerr := io.Copy(out, io.LimitReader(tr, lim.MaxFileSize+1))
			if e := out.Close(); cerr == nil {
				cerr = e
			}
			if cerr != nil {
				return 0, reject("cannot unpack %q: %v", h.Name, cerr)
			}
			if written += c; c > lim.MaxFileSize || written > lim.MaxTotalSize {
				return 0, reject("archive expands beyond its limits")
			}
			list = append(list, staged{name, false})
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
		default:
			return 0, reject("archive contains a link or special file (%q); these are not allowed", h.Name)
		}
	}
	files := 0
	for _, e := range list {
		final := e.name
		if dir != "." {
			final = path.Join(dir, e.name)
		}
		if e.dir {
			if err := w.mkdirAll(final); err != nil {
				return files, err
			}
			continue
		}
		if err := w.mkdirAll(path.Dir(final)); err != nil {
			return files, err
		}
		src := path.Join(stage, e.name)
		w.chown(src)
		if err := w.root.Rename(src, final); err != nil {
			return files, fmt.Errorf("place %q: %w", e.name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveName suggests a name for a new archive of paths.
func ArchiveName(paths []string, now time.Time) string {
	base := "archive"
	if len(paths) == 1 {
		base = path.Base(path.Clean(paths[0]))
		if base == "." || base == "/" {
			base = "files"
		}
	}
	return fmt.Sprintf("%s-%s.zip", base, now.UTC().Format("2006-01-02-150405"))
}
