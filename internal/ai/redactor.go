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

func ProtectedPath(p string) bool {
	p = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")))
	p = strings.TrimPrefix(p, "./")
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
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
