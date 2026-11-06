package bundle

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// ParseVersion accepts a Semantic Version without a leading "v" and returns
// it in the canonical form golang.org/x/mod/semver compares ("v1.2.3").
func ParseVersion(v string) (string, error) {
	if v == "" || strings.HasPrefix(v, "v") || len(v) > 64 {
		return "", fmt.Errorf("%q is not a semantic version like 1.2.3", v)
	}
	c := "v" + v
	if !semver.IsValid(c) || semver.Canonical(c) != strings.SplitN(c, "+", 2)[0] {
		return "", fmt.Errorf("%q is not a semantic version like 1.2.3", v)
	}
	return c, nil
}

// CompareVersions orders two valid versions like strings.Compare.
func CompareVersions(a, b string) int {
	ca, _ := ParseVersion(a)
	cb, _ := ParseVersion(b)
	return semver.Compare(ca, cb)
}

// Range is a space-separated conjunction of comparisons such as
// ">=0.9.0 <1.0.0". An empty range matches every version.
type Range []rangeTerm

type rangeTerm struct {
	op, v string
}

// ParseRange parses a compatibility range.
func ParseRange(s string) (Range, error) {
	var r Range
	fields := strings.Fields(s)
	if len(fields) > 4 {
		return nil, fmt.Errorf("at most 4 comparisons")
	}
	for _, f := range fields {
		op := ""
		for _, o := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(f, o) {
				op = o
				break
			}
		}
		if op == "" {
			return nil, fmt.Errorf("%q must start with >=, <=, >, < or =", f)
		}
		v, err := ParseVersion(strings.TrimPrefix(f, op))
		if err != nil {
			return nil, err
		}
		r = append(r, rangeTerm{op, v})
	}
	return r, nil
}

// Allows reports whether version satisfies every comparison. A development
// build ("dev" or any non-semver version) is allowed so local builds can load
// extensions; release builds always carry a real version.
func (r Range) Allows(version string) bool {
	v, err := ParseVersion(version)
	if err != nil {
		return true
	}
	// A prerelease of the panel counts as its release for compatibility.
	if p := semver.Prerelease(v); p != "" {
		v = strings.TrimSuffix(v, p)
	}
	for _, t := range r {
		c := semver.Compare(v, t.v)
		ok := false
		switch t.op {
		case ">=":
			ok = c >= 0
		case "<=":
			ok = c <= 0
		case ">":
			ok = c > 0
		case "<":
			ok = c < 0
		case "=":
			ok = c == 0
		}
		if !ok {
			return false
		}
	}
	return true
}
