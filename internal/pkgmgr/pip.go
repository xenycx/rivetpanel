package pkgmgr

import (
	"fmt"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

var pyGroups = []string{"main"}

var requirements = &Ecosystem{ID: "pip", File: "requirements.txt", Groups: pyGroups, Parse: parseReqs, Apply: applyReqs}
var pyproject = &Ecosystem{ID: "pip", File: "pyproject.toml", Groups: pyGroups, Parse: parsePyproject, Apply: applyPyproject}

// reqLine matches "name[extras] spec ; marker  # comment".
var reqLine = lazyre.New(`^\s*([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(?:\[[^\]]*\])?\s*((?:[<>=!~][^;#]*?)?)\s*(;[^#]*)?\s*(#.*)?$`)

type reqEntry struct {
	line int // index into lines
	name string
	spec string
}

// scanReqs finds requirement lines; option lines (-r, -e, --index-url), URLs
// and anything unparseable are left untouched and not listed.
func scanReqs(lines []string) []reqEntry {
	var out []reqEntry
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.Contains(t, "://") || strings.Contains(t, " @ ") {
			continue
		}
		m := reqLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		out = append(out, reqEntry{line: i, name: m[1], spec: strings.TrimSpace(m[2])})
	}
	return out
}

func parseReqs(data []byte) ([]Dependency, error) {
	var out []Dependency
	for _, e := range scanReqs(strings.Split(string(data), "\n")) {
		out = append(out, Dependency{Name: e.name, Spec: e.spec, Group: "main", Editable: true})
	}
	return out, nil
}

func reqText(name, spec string) string {
	if spec == "" {
		return name
	}
	return name + spec
}

func applyReqs(data []byte, ops []Op) ([]byte, error) {
	if err := checkOps("pip", ops, pyGroups, pipName, pipSpec, true); err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	trailing := strings.HasSuffix(text, "\n") || text == ""
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	for _, op := range ops {
		idx := -1
		for _, e := range scanReqs(lines) {
			if normPy(e.name) == normPy(op.Name) {
				idx = e.line
			}
		}
		switch op.Action {
		case "add":
			if idx >= 0 {
				return nil, fmt.Errorf("%s is already listed", op.Name)
			}
			lines = append(lines, reqText(op.Name, op.Spec))
		case "update":
			if idx < 0 {
				return nil, fmt.Errorf("%s is not listed", op.Name)
			}
			m := reqLine.FindStringSubmatch(lines[idx])
			l := reqText(m[1], op.Spec)
			if m[3] != "" {
				l += " " + strings.TrimSpace(m[3])
			}
			if m[4] != "" {
				l += "  " + m[4]
			}
			lines[idx] = l
		case "remove":
			if idx < 0 {
				return nil, fmt.Errorf("%s is not listed", op.Name)
			}
			lines = append(lines[:idx], lines[idx+1:]...)
		}
	}
	out := strings.Join(lines, "\n")
	if trailing || len(lines) > 0 {
		out += "\n"
	}
	return []byte(out), nil
}

// ---- pyproject.toml ([project].dependencies, PEP 621) ----

var (
	tomlHeader = lazyre.New(`^\s*\[([^\[\]]+)\]\s*(#.*)?$`)
	depsKey    = lazyre.New(`^\s*dependencies\s*=\s*\[`)
	strItem    = lazyre.New(`"((?:[^"\\]|\\.)*)"|'([^']*)'`)
)

// projectDeps locates the dependencies array text inside [project]. It returns
// the byte offsets of '[' and the matching ']' in s.
func projectDeps(s string) (open, end int, ok bool) {
	pos, inProject := 0, false
	for _, l := range strings.SplitAfter(s, "\n") {
		if m := tomlHeader.FindStringSubmatch(strings.TrimRight(l, "\r\n")); m != nil {
			inProject = strings.TrimSpace(m[1]) == "project"
		} else if inProject && depsKey.MatchString(l) {
			open = pos + strings.Index(l, "[")
			depth, inStr := 0, byte(0)
			for i := open; i < len(s); i++ {
				c := s[i]
				switch {
				case inStr != 0:
					if c == '\\' && inStr == '"' {
						i++
					} else if c == inStr {
						inStr = 0
					}
				case c == '"' || c == '\'':
					inStr = c
				case c == '#':
					for i < len(s) && s[i] != '\n' {
						i++
					}
				case c == '[':
					depth++
				case c == ']':
					depth--
					if depth == 0 {
						return open, i, true
					}
				}
			}
			return 0, 0, false
		}
		pos += len(l)
	}
	return 0, 0, false
}

func arrayItems(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if i := strings.Index(l, "#"); i >= 0 && !strings.Contains(l[:i], `"`) && !strings.Contains(l[:i], `'`) {
			l = l[:i]
		}
		for _, m := range strItem.FindAllStringSubmatch(l, -1) {
			v := m[1]
			if v == "" {
				v = m[2]
			}
			out = append(out, v)
		}
	}
	return out
}

func parsePyproject(data []byte) ([]Dependency, error) {
	s := string(data)
	open, end, ok := projectDeps(s)
	if !ok {
		return nil, nil
	}
	var out []Dependency
	for _, it := range arrayItems(s[open+1 : end]) {
		m := reqLine.FindStringSubmatch(it)
		if m == nil || strings.Contains(it, "://") || strings.Contains(it, " @ ") {
			continue
		}
		out = append(out, Dependency{Name: m[1], Spec: strings.TrimSpace(m[2]), Group: "main", Editable: true})
	}
	return out, nil
}

func applyPyproject(data []byte, ops []Op) ([]byte, error) {
	if err := checkOps("pip", ops, pyGroups, pipName, pipSpec, true); err != nil {
		return nil, err
	}
	s := string(data)
	open, end, ok := projectDeps(s)
	if !ok {
		return nil, &ParseError{"pyproject.toml", "no [project] dependencies array found; add `dependencies = []` under [project] first"}
	}
	items := arrayItems(s[open+1 : end])
	find := func(name string) int {
		for i, it := range items {
			if m := reqLine.FindStringSubmatch(it); m != nil && normPy(m[1]) == normPy(name) {
				return i
			}
		}
		return -1
	}
	for _, op := range ops {
		i := find(op.Name)
		switch op.Action {
		case "add":
			if i >= 0 {
				return nil, fmt.Errorf("%s is already listed", op.Name)
			}
			items = append(items, reqText(op.Name, op.Spec))
		case "update":
			if i < 0 {
				return nil, fmt.Errorf("%s is not listed", op.Name)
			}
			m := reqLine.FindStringSubmatch(items[i])
			items[i] = reqText(m[1], op.Spec)
			if m[3] != "" {
				items[i] += " " + strings.TrimSpace(m[3])
			}
		case "remove":
			if i < 0 {
				return nil, fmt.Errorf("%s is not listed", op.Name)
			}
			items = append(items[:i], items[i+1:]...)
		}
	}
	var b strings.Builder
	b.WriteString("[")
	for _, it := range items {
		b.WriteString("\n    " + fmt.Sprintf("%q", it) + ",")
	}
	if len(items) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("]")
	return []byte(s[:open] + b.String() + s[end+1:]), nil
}
