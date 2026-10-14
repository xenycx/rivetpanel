package siterules

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

const (
	minCompress = 1 << 10
	maxCompress = 16 << 20
)

var compressible = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".cjs": true, ".json": true, ".map": true,
	".svg": true, ".xml": true, ".txt": true, ".md": true, ".csv": true, ".wasm": true, ".ico": true,
	".webmanifest": true, ".ttf": true, ".otf": true, ".eot": true,
}

// Compressible reports whether files with this (lower-case) extension are
// worth sending compressed.
func Compressible(ext string) bool { return compressible[ext] }

// Publish prepares a staged release for serving: it compiles _redirects and
// _headers into CompiledFile (returning a *LineError for invalid rules) and
// writes gzip copies of compressible files under GzipDir. Files that already
// ship a .gz or .br sibling are left alone.
func Publish(root *os.Root) error {
	if err := root.RemoveAll(Dir); err != nil {
		return err
	}
	var rules Rules
	if data, err := readSmall(root, RedirectsFile); err != nil {
		return err
	} else if data != nil {
		if rules.Redirects, err = ParseRedirects(data); err != nil {
			return err
		}
	}
	if data, err := readSmall(root, HeadersFile); err != nil {
		return err
	} else if data != nil {
		if rules.Headers, err = ParseHeaders(data); err != nil {
			return err
		}
	}
	if !rules.Empty() {
		data, err := json.Marshal(rules)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(Dir, 0o755); err != nil {
			return err
		}
		if err := root.WriteFile(CompiledFile, data, 0o644); err != nil {
			return err
		}
	}
	return fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !Compressible(strings.ToLower(path.Ext(p))) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < minCompress || info.Size() > maxCompress {
			return nil
		}
		for _, sib := range []string{p + ".gz", p + ".br"} {
			if _, err := root.Lstat(sib); err == nil {
				return nil
			}
		}
		return gzipFile(root, p, info.Size())
	})
}

func readSmall(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSourceBytes {
		return nil, &LineError{name, 1, "the file is too large"}
	}
	return data, nil
}

// gzipFile writes GzipDir/<p>.gz, keeping it only when it saves at least 10%.
func gzipFile(root *os.Root, p string, size int64) error {
	src, err := root.Open(p)
	if err != nil {
		return err
	}
	defer src.Close()
	dst := path.Join(GzipDir, p+".gz")
	if err := root.MkdirAll(path.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	_, err = io.Copy(zw, io.LimitReader(src, maxCompress))
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	var out int64
	if st, serr := f.Stat(); serr == nil {
		out = st.Size()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || out*10 > size*9 {
		_ = root.Remove(dst)
	}
	return err
}
