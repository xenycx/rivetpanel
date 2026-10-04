// Package reposcan inspects a repository snapshot to suggest how to host it:
// language, start and build commands, variables, databases and resources.
// It reads a GitHub tarball as a stream and keeps only small, well-known
// files plus regex matches; nothing from the repository is executed.
package reposcan

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Limits bound a scan. Zero values use DefaultLimits.
type Limits struct {
	MaxArchive  int64 // compressed bytes read
	MaxKeyFile  int64 // bytes kept per key file
	MaxKeyTotal int64 // bytes kept across key files
	MaxScanFile int64 // largest source file searched for variables
	MaxScanned  int64 // source bytes searched in total
	MaxPaths    int   // paths remembered for the file overview
}

// DefaultLimits suit repositories up to a few hundred MB.
var DefaultLimits = Limits{MaxArchive: 512 << 20, MaxKeyFile: 48 << 10, MaxKeyTotal: 512 << 10,
	MaxScanFile: 256 << 10, MaxScanned: 48 << 20, MaxPaths: 4000}

// Snapshot is what a scan keeps of a repository.
type Snapshot struct {
	Files      map[string]string // key files (path relative to the root directory) -> content (possibly cut)
	Paths      []string          // file paths, sorted (bounded)
	TotalFiles int
	TotalBytes int64
	// EnvRefs maps variable names the code reads to the first file reading it.
	EnvRefs map[string]string
	// MainPackages are Go directories holding `package main` with func main.
	MainPackages []string
	Truncated    bool // the archive or a bound was exceeded; the picture may be incomplete
}

// Has reports whether a file exists (case-sensitive path).
func (s *Snapshot) Has(p string) bool {
	if _, ok := s.Files[p]; ok {
		return true
	}
	i := sort.SearchStrings(s.Paths, p)
	return i < len(s.Paths) && s.Paths[i] == p
}

// keyNames are files worth reading whole (lower-case base names).
var keyNames = map[string]bool{
	"readme.md": true, "readme.rst": true, "readme": true, "readme.txt": true,
	"package.json": true, "tsconfig.json": true, ".nvmrc": true,
	"requirements.txt": true, "pyproject.toml": true, "setup.py": true, "setup.cfg": true, "pipfile": true,
	"runtime.txt": true, ".python-version": true,
	"go.mod": true, "cargo.toml": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "gemfile": true,
	"dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true, "compose.yml": true, "compose.yaml": true,
	".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true, "example.env": true,
	"sample.env": true, "env.example": true, "app.example.env": true, ".env.defaults": true,
	"procfile": true, "app.json": true, "railway.json": true, "railway.toml": true, "nixpacks.toml": true, "fly.toml": true,
	"render.yaml": true, "schema.prisma": true,
	"config.example.json": true, "config.example.yml": true, "config.example.yaml": true, "config.example.toml": true,
	"config.sample.json": true, "config.sample.yml": true, "config.sample.yaml": true, "config.json.example": true,
}

// entryNames are likely start files, kept (cut) for the AI's context.
var entryNames = map[string]bool{
	"index.js": true, "index.ts": true, "index.mjs": true, "bot.js": true, "bot.ts": true, "main.js": true, "app.js": true,
	"main.py": true, "bot.py": true, "app.py": true, "run.py": true, "launcher.py": true, "__main__.py": true,
	"main.go": true, "main.rs": true, "main.rb": true, "bot.rb": true,
}

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, ".git": true, "dist": true, "build": true, "venv": true,
	".venv": true, "target": true, "__pycache__": true, "test": true, "tests": true, "__tests__": true, "testdata": true,
	"docs": true, "examples": true, "example": true, ".github": true}

var sourceExt = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true, ".go": true, ".rs": true,
	".rb": true, ".java": true, ".kt": true}

var envPatterns = []*regexp.Regexp{
	regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]{1,63})\b`),
	regexp.MustCompile(`process\.env\[\s*['"]([A-Z][A-Z0-9_]{1,63})['"]\s*\]`),
	regexp.MustCompile(`(?:os\.getenv|os\.environ\.get|environ\.get|getenv)\(\s*['"]([A-Z][A-Z0-9_]{1,63})['"]`),
	regexp.MustCompile(`(?:os\.)?environ\[\s*['"]([A-Z][A-Z0-9_]{1,63})['"]\s*\]`),
	regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\(\s*"([A-Z][A-Z0-9_]{1,63})"`),
	regexp.MustCompile(`env::var(?:_os)?\(\s*"([A-Z][A-Z0-9_]{1,63})"`),
	regexp.MustCompile(`ENV(?:\.fetch\(|\[)\s*['"]([A-Z][A-Z0-9_]{1,63})['"]`),
	regexp.MustCompile(`System\.getenv\(\s*"([A-Z][A-Z0-9_]{1,63})"`),
}

// ignoredEnv are platform variables nobody needs to set.
var ignoredEnv = map[string]bool{"NODE_ENV": true, "HOME": true, "PATH": true, "PWD": true, "USER": true, "SHELL": true,
	"TERM": true, "LANG": true, "TZ": true, "CI": true, "HOSTNAME": true, "TMPDIR": true, "DEBUG": true, "VERBOSE": true,
	"PYTHONPATH": true, "GOPATH": true, "GOOS": true, "GOARCH": true, "NODE_OPTIONS": true, "XDG_CONFIG_HOME": true,
	"XDG_DATA_HOME": true, "APPDATA": true, "LOCALAPPDATA": true, "USERPROFILE": true, "COLUMNS": true, "LINES": true,
	"FORCE_COLOR": true, "NO_COLOR": true, "EDITOR": true}

var errNotGzip = errors.New("the repository archive is not gzip")

// Scan reads a GitHub tarball (one top-level directory) and returns what it
// learned about rootDir ("" for the whole repository).
func Scan(r io.Reader, rootDir string, lim Limits) (*Snapshot, error) {
	if lim.MaxArchive == 0 {
		lim = DefaultLimits
	}
	rootDir = strings.Trim(path.Clean("/"+rootDir), "/")
	lr := &io.LimitedReader{R: r, N: lim.MaxArchive}
	gz, err := gzip.NewReader(lr)
	if err != nil {
		return nil, errNotGzip
	}
	defer gz.Close()
	s := &Snapshot{Files: map[string]string{}, EnvRefs: map[string]string{}}
	var keyTotal, scanned int64
	mains := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.Truncated = true // cut by the archive limit, or corrupt: keep what was read
			break
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		_, rel, ok := strings.Cut(path.Clean(hdr.Name), "/")
		if !ok || rel == "" {
			continue
		}
		if rootDir != "" {
			if rel, ok = strings.CutPrefix(rel, rootDir+"/"); !ok {
				continue
			}
		}
		s.TotalFiles++
		s.TotalBytes += hdr.Size
		if len(s.Paths) < lim.MaxPaths {
			s.Paths = append(s.Paths, rel)
		} else {
			s.Truncated = true
		}
		parts := strings.Split(rel, "/")
		skip := false
		for _, p := range parts[:len(parts)-1] {
			if skipDirs[strings.ToLower(p)] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		base := strings.ToLower(parts[len(parts)-1])
		depth := len(parts) - 1
		isKey := keyNames[base] && depth <= 2
		isEntry := entryNames[base] && depth <= 2
		ext := path.Ext(base)
		isSource := sourceExt[ext] && hdr.Size <= lim.MaxScanFile && !strings.HasSuffix(base, "_test.go") && !strings.Contains(base, ".test.") && !strings.Contains(base, ".spec.")
		if !isKey && !isEntry && !isSource {
			continue
		}
		want := hdr.Size
		if isSource && scanned+want > lim.MaxScanned {
			isSource = false
			s.Truncated = true
		}
		if !isSource && !isKey && !isEntry {
			continue
		}
		if !isSource {
			want = min(want, lim.MaxKeyFile)
		}
		b, err := io.ReadAll(io.LimitReader(tr, want))
		if err != nil {
			s.Truncated = true
			break
		}
		if isSource {
			scanned += int64(len(b))
			text := string(b)
			for _, re := range envPatterns {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					n := m[1]
					if ignoredEnv[n] || strings.HasPrefix(n, "NPM_") || strings.HasPrefix(n, "GITHUB_") || strings.HasPrefix(n, "RIVET_") {
						continue
					}
					if _, seen := s.EnvRefs[n]; !seen && len(s.EnvRefs) < 200 {
						s.EnvRefs[n] = rel
					}
				}
			}
			if ext == ".go" && strings.Contains(text, "package main") && strings.Contains(text, "func main(") {
				mains[path.Dir(rel)] = true
			}
		}
		if (isKey || isEntry) && keyTotal < lim.MaxKeyTotal {
			if int64(len(b)) > lim.MaxKeyFile {
				b = b[:lim.MaxKeyFile]
			}
			keyTotal += int64(len(b))
			s.Files[rel] = string(b)
		}
	}
	if lr.N <= 0 {
		s.Truncated = true
	}
	sort.Strings(s.Paths)
	for d := range mains {
		s.MainPackages = append(s.MainPackages, d)
	}
	sort.Strings(s.MainPackages)
	if s.TotalFiles == 0 {
		if rootDir != "" {
			return nil, errors.New("the folder " + rootDir + " was not found in the repository")
		}
		return nil, errors.New("the repository is empty")
	}
	return s, nil
}
