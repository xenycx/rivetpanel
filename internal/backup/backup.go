// Package backup creates and restores consistent RivetPanel backups.
//
// A backup directory holds three independent restore inputs:
//
//	rivetpanel.db    SQLite snapshot made with VACUUM INTO (WAL-consistent; never a raw file copy)
//	bots.tar.gz    every bot workspace
//	keys/          encryption keys (only with IncludeKeys; store them separately from the rest)
//	keys/agent-ca/ the agent certificate authority, when one exists (only with IncludeKeys)
//	manifest.json  checksums and metadata
//
// Without the keys, stored environment values cannot be decrypted.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xenycx/rivetpanel/internal/legacy"
	"github.com/xenycx/rivetpanel/internal/store/sqlite"
)

const (
	manifestName = "manifest.json"
	dbName       = "rivetpanel.db"
	botsName     = "bots.tar.gz"
	keysDirName  = "keys"
	agentCADir   = "agent-ca"
	// maxRestoreBytes bounds the total size extracted from bots.tar.gz.
	maxRestoreBytes = 256 << 30
)

// Manifest describes a backup.
type Manifest struct {
	Version      int      `json:"version"`
	CreatedAtMS  int64    `json:"created_at_ms"`
	Bots         int      `json:"bots"`
	IncludesKeys bool     `json:"includes_keys"`
	KeyIDs       []string `json:"key_ids"`
	// IncludesAgentCA is set when keys/agent-ca/ holds the agent CA key and
	// certificate; their checksums are in SHA256.
	IncludesAgentCA bool              `json:"includes_agent_ca,omitempty"`
	SHA256          map[string]string `json:"sha256"`
}

// Source names what to back up.
type Source struct {
	DB       *sqlite.DB
	DataRoot string
	KeyDir   string
}

func sumFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Create writes a backup into dest, which must not exist or be empty.
func Create(ctx context.Context, src Source, dest string, includeKeys bool, now time.Time) (Manifest, error) {
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return Manifest{}, fmt.Errorf("destination %s is not empty", dest)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: 1, CreatedAtMS: now.UnixMilli(), SHA256: map[string]string{}}

	// 1. Consistent database snapshot.
	dbPath := filepath.Join(dest, dbName)
	if _, err := src.DB.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dbPath, "'", "''")+"'"); err != nil {
		return m, fmt.Errorf("snapshot database: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return m, err
	}

	// 2. Bot workspaces.
	n, err := archiveBots(ctx, src.DataRoot, filepath.Join(dest, botsName))
	if err != nil {
		return m, fmt.Errorf("archive bot files: %w", err)
	}
	m.Bots = n

	// 3. Keys (optional, kept separate).
	ids, err := listKeyIDs(src.KeyDir)
	if err != nil {
		return m, err
	}
	m.KeyIDs = ids
	if includeKeys {
		kd := filepath.Join(dest, keysDirName)
		if err := os.MkdirAll(kd, 0o700); err != nil {
			return m, err
		}
		for _, id := range ids {
			b, err := os.ReadFile(filepath.Join(src.KeyDir, id+".key"))
			if err != nil {
				return m, err
			}
			if err := os.WriteFile(filepath.Join(kd, id+".key"), b, 0o600); err != nil {
				return m, err
			}
		}
		m.IncludesKeys = true
		ok, err := copyAgentCA(filepath.Join(src.KeyDir, agentCADir), filepath.Join(kd, agentCADir), false)
		if err != nil {
			return m, fmt.Errorf("copy agent CA: %w", err)
		}
		if ok {
			m.IncludesAgentCA = true
			for _, name := range agentCAFiles() {
				if m.SHA256[name], err = sumFile(filepath.Join(dest, name)); err != nil {
					return m, err
				}
			}
		}
	}

	for _, name := range []string{dbName, botsName} {
		if m.SHA256[name], err = sumFile(filepath.Join(dest, name)); err != nil {
			return m, err
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return m, os.WriteFile(filepath.Join(dest, manifestName), b, 0o600)
}

var agentCAModes = map[string]os.FileMode{"ca.key": 0o600, "ca.crt": 0o644}

// agentCAFiles are the manifest names of the agent CA files.
func agentCAFiles() []string {
	return []string{keysDirName + "/" + agentCADir + "/ca.key", keysDirName + "/" + agentCADir + "/ca.crt"}
}

// copyAgentCA copies a complete agent CA from src to dst. It reports false
// when src holds no CA, and refuses a partial one. Files are written to
// temporaries and renamed so an interrupted copy never leaves a partial CA.
func copyAgentCA(src, dst string, force bool) (bool, error) {
	present := 0
	for name := range agentCAModes {
		if _, err := os.Stat(filepath.Join(src, name)); err == nil {
			present++
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	switch present {
	case 0:
		return false, nil
	case 1:
		return false, errors.New("agent CA is incomplete: ca.key and ca.crt must both exist")
	}
	if !force {
		for name := range agentCAModes {
			if _, err := os.Stat(filepath.Join(dst, name)); err == nil {
				return false, fmt.Errorf("%s already exists (use --force)", filepath.Join(dst, name))
			}
		}
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return false, err
	}
	for _, name := range []string{"ca.key", "ca.crt"} {
		if err := copyFile(filepath.Join(src, name), filepath.Join(dst, name+".tmp"), agentCAModes[name]); err != nil {
			return false, err
		}
		if err := os.Chmod(filepath.Join(dst, name+".tmp"), agentCAModes[name]); err != nil {
			return false, err
		}
	}
	for _, name := range []string{"ca.key", "ca.crt"} {
		if err := os.Rename(filepath.Join(dst, name+".tmp"), filepath.Join(dst, name)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func listKeyIDs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".key"); ok && e.Type().IsRegular() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func isBotID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}

func archiveBots(ctx context.Context, dataRoot, out string) (int, error) {
	root, err := os.OpenRoot(dataRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			root = nil
		} else {
			return 0, err
		}
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	count := 0
	fail := func(err error) (int, error) { tw.Close(); gz.Close(); f.Close(); return 0, err }
	if root != nil {
		defer root.Close()
		tops, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			return fail(err)
		}
		for _, top := range tops {
			if !top.IsDir() || !isBotID(top.Name()) {
				continue
			}
			count++
			err := fs.WalkDir(root.FS(), top.Name(), func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				info, err := root.Lstat(p)
				if err != nil {
					return err
				}
				hdr := &tar.Header{Name: p, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime(), Format: tar.FormatPAX}
				switch {
				case info.IsDir():
					hdr.Typeflag, hdr.Name = tar.TypeDir, p+"/"
				case info.Mode().IsRegular():
					hdr.Typeflag, hdr.Size = tar.TypeReg, info.Size()
				case info.Mode()&fs.ModeSymlink != 0:
					target, err := root.Readlink(p)
					if err != nil {
						return err
					}
					hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, target
				default:
					return nil // sockets, devices, fifos are not data
				}
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				if hdr.Typeflag == tar.TypeReg {
					in, err := root.Open(p)
					if err != nil {
						return err
					}
					defer in.Close()
					// A file that grows while we read must not corrupt the archive.
					if _, err := io.CopyN(tw, in, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return fail(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return 0, err
	}
	return count, f.Close()
}

// ReadManifest loads and validates manifest.json.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if legacy.IsBackup(dir, m.SHA256, dbName) {
		return m, ErrLegacyBackup
	}
	if m.Version != 1 {
		return m, fmt.Errorf("unsupported backup version %d", m.Version)
	}
	return m, nil
}

// ErrLegacyBackup is returned for backups written by the former product,
// which RivetPanel deliberately cannot restore.
var ErrLegacyBackup = errors.New("this is a " + legacy.Release + " backup; RivetPanel cannot restore it " +
	"(the rename was a clean break, see " + legacy.DocsPath + "). Keep it for the old panel, and re-create accounts and bots in RivetPanel")

// Verify checks every file listed in the manifest against its checksum.
func Verify(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return m, err
	}
	for _, name := range []string{dbName, botsName} {
		want, ok := m.SHA256[name]
		if !ok {
			return m, fmt.Errorf("manifest lacks a checksum for %s", name)
		}
		got, err := sumFile(filepath.Join(dir, name))
		if err != nil {
			return m, err
		}
		if got != want {
			return m, fmt.Errorf("%s does not match its checksum; the backup is corrupt or was modified", name)
		}
	}
	if m.IncludesAgentCA {
		for _, name := range agentCAFiles() {
			want, ok := m.SHA256[name]
			if !ok {
				return m, fmt.Errorf("manifest lacks a checksum for %s", name)
			}
			got, err := sumFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				return m, err
			}
			if got != want {
				return m, fmt.Errorf("%s does not match its checksum; the backup is corrupt or was modified", name)
			}
		}
	}
	return m, nil
}

// RestoreOptions control Restore.
type RestoreOptions struct {
	DBPath      string
	DataRoot    string
	KeyDir      string
	Force       bool // replace an existing database and existing bot directories
	RestoreKeys bool // copy keys from the backup into KeyDir
}

// Restore verifies the backup then restores database, bot files and (optionally)
// keys. The panel must be stopped.
func Restore(ctx context.Context, dir string, o RestoreOptions) (Manifest, error) {
	m, err := Verify(dir)
	if err != nil {
		return m, err
	}
	for _, p := range []string{o.DBPath, o.DBPath + "-wal", o.DBPath + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if !o.Force {
				return m, fmt.Errorf("%s already exists; refusing to overwrite (use --force)", p)
			}
		}
	}
	if !o.Force {
		if ents, _ := os.ReadDir(o.DataRoot); len(ents) > 0 {
			return m, fmt.Errorf("%s is not empty; refusing to restore over it (use --force)", o.DataRoot)
		}
	}
	if o.RestoreKeys && m.IncludesAgentCA && !o.Force {
		for name := range agentCAModes {
			if p := filepath.Join(o.KeyDir, agentCADir, name); fileExists(p) {
				return m, fmt.Errorf("%s already exists; refusing to overwrite the agent CA (use --force)", p)
			}
		}
	}
	// Bot files first: a failure leaves the database untouched.
	if err := extractBots(ctx, filepath.Join(dir, botsName), o.DataRoot, o.Force); err != nil {
		return m, fmt.Errorf("restore bot files: %w", err)
	}
	if o.RestoreKeys {
		if !m.IncludesKeys {
			return m, errors.New("this backup does not include keys")
		}
		if err := os.MkdirAll(o.KeyDir, 0o700); err != nil {
			return m, err
		}
		for _, id := range m.KeyIDs {
			dst := filepath.Join(o.KeyDir, id+".key")
			if _, err := os.Stat(dst); err == nil && !o.Force {
				return m, fmt.Errorf("key %s already exists (use --force)", id)
			}
			b, err := os.ReadFile(filepath.Join(dir, keysDirName, id+".key"))
			if err != nil {
				return m, err
			}
			if err := os.WriteFile(dst, b, 0o600); err != nil {
				return m, err
			}
		}
		if m.IncludesAgentCA {
			if _, err := copyAgentCA(filepath.Join(dir, keysDirName, agentCADir), filepath.Join(o.KeyDir, agentCADir), o.Force); err != nil {
				return m, fmt.Errorf("restore agent CA: %w", err)
			}
		}
	}
	// Database last, atomically.
	for _, p := range []string{o.DBPath + "-wal", o.DBPath + "-shm"} {
		os.Remove(p)
	}
	if err := os.MkdirAll(filepath.Dir(o.DBPath), 0o750); err != nil {
		return m, err
	}
	tmp := o.DBPath + ".restore"
	if err := copyFile(filepath.Join(dir, dbName), tmp, 0o600); err != nil {
		return m, err
	}
	return m, os.Rename(tmp, o.DBPath)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// extractBots unpacks the archive with descriptor-relative operations: entry
// names must be "<bot uuid>/relative/path", nothing can land outside its bot
// directory, and setuid/setgid bits are dropped. Symlinks are recreated as
// links but never followed.
func extractBots(ctx context.Context, archive, dataRoot string, force bool) error {
	if err := os.MkdirAll(dataRoot, 0o750); err != nil {
		return err
	}
	root, err := os.OpenRoot(dataRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	seen := map[string]*os.Root{}
	defer func() {
		for _, r := range seen {
			r.Close()
		}
	}()
	var total int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		bot, rel, _ := strings.Cut(name, "/")
		if !isBotID(bot) || strings.ContainsAny(name, "\\\x00") || (rel != "" && !filepath.IsLocal(rel)) {
			return fmt.Errorf("unsafe or unexpected archive entry %q", hdr.Name)
		}
		br := seen[bot]
		if br == nil {
			if force {
				if err := root.RemoveAll(bot); err != nil {
					return err
				}
			}
			if err := root.Mkdir(bot, 0o750); err != nil {
				return fmt.Errorf("create %s: %w", bot, err)
			}
			if br, err = root.OpenRoot(bot); err != nil {
				return err
			}
			seen[bot] = br
		}
		if rel == "" {
			continue
		}
		rel = path.Clean(rel)
		perm := fs.FileMode(hdr.Mode).Perm() // drops setuid/setgid/sticky
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := br.MkdirAll(rel, 0o750); err != nil {
				return err
			}
			_ = br.Chmod(rel, perm|0o700)
		case tar.TypeReg:
			if total += hdr.Size; total > maxRestoreBytes {
				return errors.New("archive exceeds the restore size limit")
			}
			if d := path.Dir(rel); d != "." {
				if err := br.MkdirAll(d, 0o750); err != nil {
					return err
				}
			}
			out, err := br.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if err != nil {
				return fmt.Errorf("create %s: %w", name, err)
			}
			_, cerr := io.CopyN(out, tr, hdr.Size)
			if e := out.Close(); cerr == nil {
				cerr = e
			}
			if cerr != nil {
				return cerr
			}
		case tar.TypeSymlink:
			if strings.ContainsRune(hdr.Linkname, 0) {
				return fmt.Errorf("invalid symlink target in %q", hdr.Name)
			}
			if d := path.Dir(rel); d != "." {
				if err := br.MkdirAll(d, 0o750); err != nil {
					return err
				}
			}
			if err := br.Symlink(hdr.Linkname, rel); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry type in %q", hdr.Name)
		}
	}
}
