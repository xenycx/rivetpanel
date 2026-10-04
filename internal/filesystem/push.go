package filesystem

import (
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PushLimits bound what may be pushed to a Git host in one commit.
type PushLimits struct {
	MaxFiles int
	MaxBytes int64 // total size of the files pushed
	MaxFile  int64 // larger files are skipped (reported), not pushed
}

// DefaultPushLimits stay well inside GitHub's API and file-size limits.
var DefaultPushLimits = PushLimits{MaxFiles: 5000, MaxBytes: 200 << 20, MaxFile: 25 << 20}

// PushFile is one file to push. Link is set for symbolic links (their
// target is pushed as the link, never followed).
type PushFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Exec bool   `json:"exec"`
	Link string `json:"link,omitempty"`
}

// PushSkip is a file left out, with a reason users can act on.
type PushSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PushSet is the result of selecting a workspace's files for a push.
type PushSet struct {
	Files   []PushFile
	Bytes   int64
	Skipped []PushSkip // bounded to the first 200
	Ignored int        // entries excluded by .gitignore and the built-in rules
}

// Built-in exclusions: dependency and build directories, version control
// and panel metadata, and environment files that usually hold secrets
// (examples and templates are kept).
var (
	pushSkipDirs  = map[string]bool{".git": true, MetaDir: true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".cache": true, ".npm": true, ".cargo": true, ".gopath": true, ".pytest_cache": true, ".mypy_cache": true}
	pushSkipTop   = map[string]bool{"target": true}
	envKeep       = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true}
	pushSkipFiles = map[string]bool{DeployManifest: true, LegacyDeployManifest: true, ".DS_Store": true, "Thumbs.db": true}
)

func builtinIgnored(p string, isDir bool) bool {
	base := path.Base(p)
	if isDir {
		return pushSkipDirs[base] || (!strings.Contains(p, "/") && pushSkipTop[base])
	}
	if pushSkipFiles[base] || strings.HasSuffix(base, ".pyc") || strings.HasPrefix(base, ".tmp-") || strings.HasPrefix(base, ".upload-") {
		return true
	}
	return (base == ".env" || strings.HasPrefix(base, ".env.")) && !envKeep[base]
}

// ignoreRule is one .gitignore line compiled to a regular expression over
// workspace-relative paths.
type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// globRe converts a gitignore glob to a regular expression fragment.
func globRe(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch ch := g[i]; {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "/**") && i+3 == len(g):
			b.WriteString("(?:/.*)?")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case ch == '*':
			b.WriteString("[^/]*")
		case ch == '?':
			b.WriteString("[^/]")
		case ch == '[':
			if j := strings.IndexByte(g[i+1:], ']'); j >= 0 {
				class := g[i+1 : i+1+j]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
				i += j + 1
			} else {
				b.WriteString(`\[`)
			}
		case ch == '\\' && i+1 < len(g):
			i++
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return b.String()
}

// parseIgnore compiles a .gitignore found in dir ("" for the root).
func parseIgnore(data []byte, dir string) []ignoreRule {
	var out []ignoreRule
	prefix := ""
	if dir != "" {
		prefix = regexp.QuoteMeta(dir) + "/"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \r\t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		expr := "^" + prefix
		if !anchored {
			expr += "(?:.*/)?"
		}
		re, err := regexp.Compile(expr + globRe(line) + "$")
		if err != nil {
			continue
		}
		r.re = re
		out = append(out, r)
	}
	return out
}

func ignoredBy(rules []ignoreRule, p string, isDir bool) bool {
	ignored := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(p) {
			ignored = !r.negate
		}
	}
	return ignored
}

// PushSet selects the workspace files a push should contain: everything
// except what .gitignore files (root and nested) and the built-in rules
// exclude. Files over lim.MaxFile are skipped and reported; exceeding
// MaxFiles or MaxBytes is an error.
func (w *Workspace) PushSet(lim PushLimits) (PushSet, error) {
	var set PushSet
	skip := func(p, reason string) {
		if len(set.Skipped) < 200 {
			set.Skipped = append(set.Skipped, PushSkip{p, reason})
		}
	}
	var walk func(dir string, rules []ignoreRule) error
	walk = func(dir string, rules []ignoreRule) error {
		rel := dir
		if rel == "" {
			rel = "."
		}
		if data, err := w.Read(path.Join(rel, ".gitignore"), 256<<10); err == nil {
			rules = append(append([]ignoreRule(nil), rules...), parseIgnore(data, dir)...)
		}
		entries, err := w.ListInfo(rel)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			p := e.Name()
			if dir != "" {
				p = dir + "/" + p
			}
			isDir := e.IsDir()
			if builtinIgnored(p, isDir) || ignoredBy(rules, p, isDir) {
				set.Ignored++
				continue
			}
			switch mode := e.Mode(); {
			case isDir:
				if err := walk(p, rules); err != nil {
					return err
				}
			case mode&fs.ModeSymlink != 0:
				target, err := w.root.Readlink(p)
				if err != nil {
					continue
				}
				if filepath.IsAbs(target) || !filepath.IsLocal(path.Join(path.Dir(p), target)) {
					skip(p, "symbolic link points outside the project")
					continue
				}
				set.Files = append(set.Files, PushFile{Path: p, Link: target, Size: int64(len(target))})
			case mode.IsRegular():
				if e.Size() > lim.MaxFile {
					skip(p, "larger than the per-file limit")
					continue
				}
				set.Files = append(set.Files, PushFile{Path: p, Size: e.Size(), Exec: mode.Perm()&0o111 != 0})
				set.Bytes += e.Size()
			default:
				skip(p, "not a regular file")
			}
			if len(set.Files) > lim.MaxFiles {
				return reject("the project has more than %d files to push; add a .gitignore for generated files", lim.MaxFiles)
			}
			if set.Bytes > lim.MaxBytes {
				return reject("the project is larger than %d MiB; add a .gitignore for data and build output", lim.MaxBytes>>20)
			}
		}
		return nil
	}
	return set, walk("", nil)
}
