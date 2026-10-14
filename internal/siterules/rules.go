// Package siterules compiles a static site's _redirects and _headers files
// (the Netlify / Cloudflare Pages formats) when a release is published, and
// matches requests against them when it is served. Releases are immutable, so
// the rules are validated once at publish and stored inside the release.
package siterules

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/textproto"
	"path"
	"strconv"
	"strings"
)

// File names read from the root of a release, and the compiled output.
const (
	RedirectsFile = "_redirects"
	HeadersFile   = "_headers"
	Dir           = ".rivet"
	CompiledFile  = Dir + "/rules.json"
	GzipDir       = Dir + "/z"
)

const (
	MaxRules       = 1000
	MaxSourceBytes = 256 << 10
	maxHeaderValue = 2048
	maxHeadersRule = 64
	maxPattern     = 1024
)

// Redirect is one _redirects line.
type Redirect struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"`
	Force  bool   `json:"force,omitempty"`
}

// Header is one header set on matching paths.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HeaderRule is one _headers block.
type HeaderRule struct {
	Path    string   `json:"path"`
	Headers []Header `json:"headers"`
}

// Rules is the compiled form stored in a release.
type Rules struct {
	Redirects []Redirect   `json:"redirects,omitempty"`
	Headers   []HeaderRule `json:"headers,omitempty"`

	redirects []pattern
	headers   []pattern
}

// LineError points at the offending line of a rules file.
type LineError struct {
	File string
	Line int
	Msg  string
}

func (e *LineError) Error() string { return fmt.Sprintf("%s line %d: %s", e.File, e.Line, e.Msg) }

var redirectStatuses = map[int]bool{200: true, 301: true, 302: true, 303: true, 307: true, 308: true, 404: true, 410: true}

// ParseRedirects parses a _redirects file: "from to [status][!]" per line.
func ParseRedirects(data []byte) ([]Redirect, error) {
	var out []Redirect
	err := lines(RedirectsFile, data, func(n int, line string) error {
		f := strings.Fields(line)
		if len(f) < 2 || len(f) > 3 {
			return &LineError{RedirectsFile, n, "expected \"from to [status]\""}
		}
		r := Redirect{From: f[0], To: f[1], Status: 301}
		if len(f) == 3 {
			code := f[2]
			if strings.HasSuffix(code, "!") {
				r.Force = true
				code = strings.TrimSuffix(code, "!")
			}
			st, err := strconv.Atoi(code)
			if err != nil || !redirectStatuses[st] {
				return &LineError{RedirectsFile, n, "status must be one of 200, 301, 302, 303, 307, 308, 404 or 410"}
			}
			r.Status = st
		}
		if _, err := compile(r.From); err != nil {
			return &LineError{RedirectsFile, n, err.Error()}
		}
		if err := checkTarget(r); err != nil {
			return &LineError{RedirectsFile, n, err.Error()}
		}
		if len(out) >= MaxRules {
			return &LineError{RedirectsFile, n, fmt.Sprintf("more than %d rules", MaxRules)}
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

func checkTarget(r Redirect) error {
	if len(r.To) > maxPattern || strings.ContainsAny(r.To, "\r\n\x00\\") {
		return fmt.Errorf("invalid target")
	}
	switch {
	case strings.HasPrefix(r.To, "/"):
		if strings.HasPrefix(r.To, "//") {
			return fmt.Errorf("protocol-relative targets are not allowed; use https://")
		}
	case strings.HasPrefix(r.To, "https://"), strings.HasPrefix(r.To, "http://"):
		if r.Status == 200 || r.Status == 404 || r.Status == 410 {
			return fmt.Errorf("status %d needs a path inside the site, not a URL (proxying is not supported)", r.Status)
		}
	default:
		return fmt.Errorf("target must start with / or https://")
	}
	return nil
}

// forbidden headers can't be set from _headers: they would break framing,
// sessions or the listener's own guarantees.
var forbidden = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true, "Upgrade": true, "Te": true, "Trailer": true,
	"Proxy-Authenticate": true, "Proxy-Authorization": true, "Proxy-Connection": true,
	"Content-Length": true, "Content-Encoding": true, "Content-Range": true, "Set-Cookie": true, "Date": true,
	"Etag": true, "Last-Modified": true, "Location": true, "Vary": true, "Accept-Ranges": true,
}

// ParseHeaders parses a _headers file: an unindented path line followed by
// indented "Name: value" lines.
func ParseHeaders(data []byte) ([]HeaderRule, error) {
	var out []HeaderRule
	var cur *HeaderRule
	err := linesRaw(HeadersFile, data, func(n int, raw string) error {
		indented := raw[0] == ' ' || raw[0] == '\t'
		line := strings.TrimSpace(raw)
		if !indented {
			if cur != nil && len(cur.Headers) == 0 {
				return &LineError{HeadersFile, n, "the previous path has no headers"}
			}
			if _, err := compile(line); err != nil {
				return &LineError{HeadersFile, n, err.Error()}
			}
			if len(out) >= MaxRules {
				return &LineError{HeadersFile, n, fmt.Sprintf("more than %d rules", MaxRules)}
			}
			out = append(out, HeaderRule{Path: line})
			cur = &out[len(out)-1]
			return nil
		}
		if cur == nil {
			return &LineError{HeadersFile, n, "a header must follow a path line"}
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !validHeaderName(name) {
			return &LineError{HeadersFile, n, "expected \"Name: value\""}
		}
		name = textproto.CanonicalMIMEHeaderKey(name)
		if forbidden[name] {
			return &LineError{HeadersFile, n, name + " cannot be set from _headers"}
		}
		if len(value) > maxHeaderValue || strings.ContainsAny(value, "\x00") {
			return &LineError{HeadersFile, n, "header value is too long or invalid"}
		}
		if len(cur.Headers) >= maxHeadersRule {
			return &LineError{HeadersFile, n, fmt.Sprintf("more than %d headers for one path", maxHeadersRule)}
		}
		cur.Headers = append(cur.Headers, Header{Name: name, Value: value})
		return nil
	})
	if err == nil && cur != nil && len(cur.Headers) == 0 {
		return nil, &LineError{HeadersFile, bytes.Count(data, []byte("\n")) + 1, "the last path has no headers"}
	}
	return out, err
}

func validHeaderName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// lines calls fn for every non-empty, non-comment line, trimmed.
func lines(file string, data []byte, fn func(n int, line string) error) error {
	return linesRaw(file, data, func(n int, raw string) error { return fn(n, strings.TrimSpace(raw)) })
}

func linesRaw(file string, data []byte, fn func(n int, raw string) error) error {
	if len(data) > MaxSourceBytes {
		return &LineError{file, 1, fmt.Sprintf("the file is larger than %d KiB", MaxSourceBytes>>10)}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), MaxSourceBytes)
	n := 0
	for sc.Scan() {
		n++
		raw := strings.TrimRight(sc.Text(), "\r")
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if err := fn(n, raw); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Load decodes compiled rules; nil data means an empty rule set.
func Load(data []byte) (*Rules, error) {
	r := &Rules{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, r); err != nil {
			return nil, err
		}
	}
	for _, x := range r.Redirects {
		p, err := compile(x.From)
		if err != nil {
			return nil, err
		}
		r.redirects = append(r.redirects, p)
	}
	for _, x := range r.Headers {
		p, err := compile(x.Path)
		if err != nil {
			return nil, err
		}
		r.headers = append(r.headers, p)
	}
	return r, nil
}

// Empty reports whether there is nothing to apply.
func (r *Rules) Empty() bool { return r == nil || len(r.Redirects) == 0 && len(r.Headers) == 0 }

// Match is a redirect that applies to a request.
type Match struct {
	Redirect
	Target string // To with placeholders and the splat substituted
}

// Redirect returns the first rule matching p (a cleaned path) that may apply:
// unforced rules are skipped when a file exists at the path (shadowing).
func (r *Rules) Redirect(p string, fileExists bool) (Match, bool) {
	if r == nil {
		return Match{}, false
	}
	for i, pat := range r.redirects {
		x := r.Redirects[i]
		if fileExists && !x.Force {
			continue
		}
		if vars, ok := pat.match(p); ok {
			return Match{Redirect: x, Target: substitute(x.To, vars)}, true
		}
	}
	return Match{}, false
}

// Apply sets the headers of every rule matching p; later rules win.
func (r *Rules) Apply(h http.Header, p string) {
	if r == nil {
		return
	}
	for i, pat := range r.headers {
		if _, ok := pat.match(p); ok {
			for _, x := range r.Headers[i].Headers {
				h.Set(x.Name, x.Value)
			}
		}
	}
}

// pattern is a compiled path pattern: literal segments, ":name" segments
// that match exactly one segment, and an optional trailing "*" splat.
type pattern struct {
	segs  []string
	splat bool
}

func compile(p string) (pattern, error) {
	if !strings.HasPrefix(p, "/") || len(p) > maxPattern || strings.ContainsAny(p, "?#\\\x00") {
		return pattern{}, fmt.Errorf("path must start with / and contain no query or fragment")
	}
	var pat pattern
	segs := split(p)
	for i, s := range segs {
		if strings.Contains(s, "*") {
			if s != "*" || i != len(segs)-1 {
				return pattern{}, fmt.Errorf("* is only allowed as the whole last segment")
			}
			pat.splat = true
			break
		}
		if strings.HasPrefix(s, ":") && !ident(s[1:]) {
			return pattern{}, fmt.Errorf("invalid placeholder %q", s)
		}
		pat.segs = append(pat.segs, s)
	}
	return pat, nil
}

func split(p string) []string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (pat pattern) match(p string) (map[string]string, bool) {
	segs := split(p)
	if len(segs) < len(pat.segs) || !pat.splat && len(segs) != len(pat.segs) {
		return nil, false
	}
	var vars map[string]string
	for i, s := range pat.segs {
		if strings.HasPrefix(s, ":") {
			if vars == nil {
				vars = map[string]string{}
			}
			vars[s[1:]] = segs[i]
			continue
		}
		if s != segs[i] {
			return nil, false
		}
	}
	if pat.splat {
		if vars == nil {
			vars = map[string]string{}
		}
		vars["splat"] = strings.Join(segs[len(pat.segs):], "/")
	}
	return vars, true
}

func ident(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// substitute replaces ":name" tokens in to with captured values.
func substitute(to string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(to, ":") {
		return to
	}
	var b strings.Builder
	for i := 0; i < len(to); {
		if to[i] == ':' {
			j := i + 1
			for j < len(to) && (to[j] == '_' || to[j] >= 'a' && to[j] <= 'z' || to[j] >= 'A' && to[j] <= 'Z' || j > i+1 && to[j] >= '0' && to[j] <= '9') {
				j++
			}
			if v, ok := vars[to[i+1:j]]; ok && j > i+1 {
				b.WriteString(v)
				i = j
				continue
			}
		}
		b.WriteByte(to[i])
		i++
	}
	return b.String()
}
