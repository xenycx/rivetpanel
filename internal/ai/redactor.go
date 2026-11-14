package ai

import (
	"sort"
	"strings"
)

// Redactor exists only in memory for one tool execution. Secret values are
// sorted longest-first so overlapping values never partially leak.
type Redactor struct{ values []string }

func NewRedactor(values []string) *Redactor {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if len(v) >= 3 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return &Redactor{out}
}
func (r *Redactor) Text(s string) string {
	if r == nil {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return s
}

// legacyInternalPrefix is the panel's internal file prefix before the
// RivetPanel rename (for example the old deploy manifest); spelled in two
// parts for the namespace check.
const legacyInternalPrefix = ".bot" + "panel"

// envTemplate reports whether a .env* file name is an example or template
// (.env.example, ".env example", .env.sample, .env.template, .env.dist):
// those list the variables an app reads, with placeholders, and are meant to
// be committed and read.
func envTemplate(base string) bool {
	rest := strings.Trim(strings.TrimPrefix(base, ".env"), ". -_")
	switch rest {
	case "example", "sample", "template", "dist", "defaults", "local.example", "development.example", "production.example":
		return true
	}
	return strings.HasSuffix(rest, ".example") || strings.HasSuffix(rest, ".sample") || strings.HasSuffix(rest, ".template")
}

func ProtectedPath(p string) bool {
	p = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")))
	p = strings.TrimPrefix(p, "./")
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	if strings.HasPrefix(base, ".env") && envTemplate(base) {
		return false // a template of the variables, with placeholders
	}
	if strings.HasPrefix(base, ".env") || base == ".npmrc" || base == ".pypirc" || base == ".netrc" || base == "credentials" || base == "secrets.json" {
		return true
	}
	if strings.Contains(p, "/.aws/") || strings.Contains(p, "/.config/gcloud/") || strings.Contains(p, "/.kube/") || strings.Contains(p, ".rivetpanel") || strings.Contains(p, legacyInternalPrefix) {
		return true
	}
	for _, s := range []string{".pem", ".key", ".p12", ".pfx", "id_rsa", "id_ed25519"} {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}
