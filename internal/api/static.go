package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"io/fs"
	"mime"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
)

// The SPA's HTML has two inline scripts: the theme bootstrap from app.html
// (it must run before first paint) and SvelteKit's start script (its asset
// names change with every build). Instead of allowing every inline script,
// the policy of each HTML file lists the SHA-256 hashes of exactly the inline
// scripts it contains, computed from the embedded file on first use.
// style-src keeps 'unsafe-inline': Svelte sets style attributes and its
// transitions insert <style> rules at runtime, which hashes cannot cover.
const (
	cspHead = "default-src 'self'; script-src 'self'"
	cspTail = "; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: https:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
)

var inlineScriptRe = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script>`)

// cspFor returns the Content-Security-Policy for an HTML document: scripts
// from the panel itself plus the hashes of its inline scripts.
func cspFor(html []byte) string {
	var b strings.Builder
	b.WriteString(cspHead)
	seen := map[string]bool{}
	for _, m := range inlineScriptRe.FindAllSubmatch(html, -1) {
		if strings.Contains(strings.ToLower(string(m[1])), "src=") {
			continue // external script: covered by 'self'
		}
		sum := sha256.Sum256(m[2])
		h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !seen[h] {
			seen[h] = true
			b.WriteString(" " + h)
		}
	}
	b.WriteString(cspTail)
	return b.String()
}

// htmlCSP caches the policy of each embedded HTML file (the files never
// change while the panel runs).
type htmlCSP struct {
	fsys fs.FS
	mu   sync.Mutex
	byNm map[string]string
}

func (h *htmlCSP) get(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := h.byNm[name]; ok {
		return v
	}
	v := cspHead + cspTail // unreadable: no inline script may run
	if f, err := h.fsys.Open(name); err == nil {
		if b, err := io.ReadAll(io.LimitReader(f, 8<<20)); err == nil {
			v = cspFor(b)
		}
		_ = f.Close()
	}
	if h.byNm == nil {
		h.byNm = map[string]string{}
	}
	h.byNm[name] = v
	return v
}

// staticHandler serves the built SPA from fsys. Existing files are served
// as-is; extensionless paths fall back to the SPA shell. Missing files that
// look like assets (have an extension) return 404 instead of HTML.
func staticHandler(fsys fs.FS) fiber.Handler {
	policies := &htmlCSP{fsys: fsys}
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return fiber.ErrMethodNotAllowed
		}
		p := path.Clean("/" + strings.TrimPrefix(c.Path(), "/"))
		name := strings.TrimPrefix(p, "/")
		isRoute := path.Ext(p) == "" // extensionless paths are client-side routes
		if name == "" {
			name = "index.html"
		}
		if fsys == nil {
			return fiber.ErrNotFound
		}

		// Files are streamed from the embedded image in small chunks rather
		// than copied whole into the heap for every request.
		file, size, ok := openStatic(fsys, name)
		if !ok {
			// Prerendered route (e.g. /about -> about.html), then SPA fallback.
			if f, n, found := openStatic(fsys, name+".html"); found {
				file, size, name, ok = f, n, name+".html", true
			} else if isRoute {
				for _, fb := range []string{"200.html", "index.html"} {
					if f, n, found := openStatic(fsys, fb); found {
						file, size, name, ok = f, n, fb, true
						break
					}
				}
			}
			if !ok {
				return fiber.ErrNotFound
			}
		}

		ct := mime.TypeByExtension(path.Ext(name))
		switch path.Ext(name) { // not in every system MIME table
		case ".webmanifest":
			ct = "application/manifest+json"
		case ".ico":
			ct = "image/x-icon"
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		c.Set(fiber.HeaderContentType, ct)
		if strings.HasPrefix(name, "_app/immutable/") {
			c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
		} else {
			c.Set(fiber.HeaderCacheControl, "no-cache")
		}
		if strings.HasSuffix(name, ".html") {
			c.Set("Content-Security-Policy", policies.get(name))
		}
		if c.Method() == fiber.MethodHead {
			_ = file.Close()
			return nil
		}
		return c.SendStream(file, int(size)) // closed by the server once sent
	}
}

// openStatic opens a regular file of fsys.
func openStatic(fsys fs.FS, name string) (fs.File, int64, bool) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, 0, false
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0, false
	}
	return f, st.Size(), true
}
