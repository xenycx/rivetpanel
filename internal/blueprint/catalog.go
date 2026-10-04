package blueprint

import (
	"fmt"
	"io/fs"
	"sort"
)

// Builtin is one first-party blueprint with its exact YAML.
type Builtin struct {
	Spec Spec
	Raw  []byte
}

// LoadBuiltin parses every *.yaml in fsys. Slugs must be unique.
func LoadBuiltin(fsys fs.FS) ([]Builtin, error) {
	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	seen := map[string]bool{}
	out := make([]Builtin, 0, len(files))
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		s, err := Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if seen[s.Slug] {
			return nil, fmt.Errorf("%s: duplicate blueprint %q", f, s.Slug)
		}
		seen[s.Slug] = true
		out = append(out, Builtin{Spec: s, Raw: b})
	}
	return out, nil
}
