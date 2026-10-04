// Package lazyre holds package-level regular expressions that are compiled on
// first use instead of at program start.
//
// A compiled expression costs far more memory than its source: bounded
// repetitions such as {0,213} are expanded instruction by instruction, and the
// one-pass matcher keeps a second copy. Compiling every pattern in the binary
// at init kept about 1.2 MiB of heap live in an idle panel (and raised the
// collector's goal by twice that), although most patterns only serve rare
// requests. New compiles nothing; the first call of any method does, once.
//
// An invalid pattern still panics, like regexp.MustCompile, but on first use.
// CompileAll compiles every registered pattern so a test can catch that early.
package lazyre

import (
	"regexp"
	"sync"
	"sync/atomic"
)

// Regexp is a regular expression compiled on first use. It is safe for
// concurrent use.
type Regexp struct {
	expr string
	once sync.Once
	re   atomic.Pointer[regexp.Regexp]
}

var (
	registryMu sync.Mutex
	registry   []*Regexp
)

// New returns a lazily compiled expression. Call it for package-level
// variables only: every value is remembered for CompileAll.
func New(expr string) *Regexp {
	r := &Regexp{expr: expr}
	registryMu.Lock()
	registry = append(registry, r)
	registryMu.Unlock()
	return r
}

// Get returns the compiled expression, compiling it on the first call. It
// panics if the expression is invalid.
func (r *Regexp) Get() *regexp.Regexp {
	if re := r.re.Load(); re != nil {
		return re
	}
	r.once.Do(func() { r.re.Store(regexp.MustCompile(r.expr)) })
	return r.re.Load()
}

// Compiled reports whether the expression has been compiled yet.
func (r *Regexp) Compiled() bool { return r.re.Load() != nil }

// CompileAll compiles every expression created with New in the packages linked
// into the program and returns how many there are. It panics on the first
// invalid pattern. Meant for tests.
func CompileAll() int {
	registryMu.Lock()
	all := append([]*Regexp(nil), registry...)
	registryMu.Unlock()
	for _, r := range all {
		r.Get()
	}
	return len(all)
}

// String returns the source text of the expression without compiling it.
func (r *Regexp) String() string { return r.expr }

// MatchString reports whether s contains any match of the expression.
func (r *Regexp) MatchString(s string) bool { return r.Get().MatchString(s) }

// Match reports whether b contains any match of the expression.
func (r *Regexp) Match(b []byte) bool { return r.Get().Match(b) }

// FindStringSubmatch is regexp.Regexp.FindStringSubmatch.
func (r *Regexp) FindStringSubmatch(s string) []string { return r.Get().FindStringSubmatch(s) }

// FindSubmatch is regexp.Regexp.FindSubmatch.
func (r *Regexp) FindSubmatch(b []byte) [][]byte { return r.Get().FindSubmatch(b) }

// FindAllStringSubmatch is regexp.Regexp.FindAllStringSubmatch.
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.Get().FindAllStringSubmatch(s, n)
}

// FindAll is regexp.Regexp.FindAll.
func (r *Regexp) FindAll(b []byte, n int) [][]byte { return r.Get().FindAll(b, n) }

// FindAllString is regexp.Regexp.FindAllString.
func (r *Regexp) FindAllString(s string, n int) []string { return r.Get().FindAllString(s, n) }

// FindString is regexp.Regexp.FindString.
func (r *Regexp) FindString(s string) string { return r.Get().FindString(s) }

// ReplaceAllString is regexp.Regexp.ReplaceAllString.
func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.Get().ReplaceAllString(src, repl)
}

// ReplaceAllStringFunc is regexp.Regexp.ReplaceAllStringFunc.
func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.Get().ReplaceAllStringFunc(src, repl)
}

// ReplaceAllFunc is regexp.Regexp.ReplaceAllFunc.
func (r *Regexp) ReplaceAllFunc(src []byte, repl func([]byte) []byte) []byte {
	return r.Get().ReplaceAllFunc(src, repl)
}
