package pkgmgr

import (
	"fmt"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

var cargoGroups = []string{"dependencies", "dev-dependencies", "build-dependencies"}

var cargo = &Ecosystem{ID: "cargo", File: "Cargo.toml", Groups: cargoGroups, Parse: parseCargo, Apply: applyCargo}

var (
	cargoKV     = lazyre.New(`^(\s*)([A-Za-z_][A-Za-z0-9_-]*|"[^"]+")\s*=\s*(.*?)\s*$`)
	cargoVerStr = lazyre.New(`^"([^"]*)"\s*(#.*)?$`)
	cargoInlVer = lazyre.New(`(\bversion\s*=\s*)"[^"]*"`)
	cargoInlGet = lazyre.New(`\bversion\s*=\s*"([^"]*)"`)
	cargoDotted = lazyre.New(`^(dependencies|dev-dependencies|build-dependencies)\.([A-Za-z_][A-Za-z0-9_-]*|"[^"]+")$`)
)

// tomlSection records where a header's body lives in the line slice.
type tomlSection struct {
	name       string
	start, end int // body lines: start <= i < end
}

func sections(lines []string) []tomlSection {
	var out []tomlSection
	cur := tomlSection{name: "", start: 0}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[[") {
			if m := tomlHeader.FindStringSubmatch(l); m != nil {
				cur.end = i
				out = append(out, cur)
				cur = tomlSection{name: strings.TrimSpace(m[1]), start: i + 1}
				continue
			}
		} else if strings.HasPrefix(t, "[[") {
			cur.end = i
			out = append(out, cur)
			cur = tomlSection{name: "[[array]]", start: i + 1}
		}
	}
	cur.end = len(lines)
	return append(out, cur)
}

func unquote(k string) string { return strings.Trim(k, `"`) }

func parseCargo(data []byte) ([]Dependency, error) {
	lines := strings.Split(string(data), "\n")
	var out []Dependency
	for _, sec := range sections(lines) {
		group := ""
		for _, g := range cargoGroups {
			if sec.name == g {
				group = g
			}
		}
		if m := cargoDotted.FindStringSubmatch(sec.name); m != nil { // [dependencies.serde]
			d := Dependency{Name: unquote(m[2]), Group: m[1]}
			for _, l := range lines[sec.start:sec.end] {
				if kv := cargoKV.FindStringSubmatch(l); kv != nil && kv[2] == "version" {
					if v := cargoVerStr.FindStringSubmatch(kv[3]); v != nil {
						d.Spec = v[1]
					}
				}
			}
			out = append(out, d) // table form is read-only
			continue
		}
		if group == "" {
			continue
		}
		for _, l := range lines[sec.start:sec.end] {
			kv := cargoKV.FindStringSubmatch(l)
			if kv == nil {
				continue
			}
			d := Dependency{Name: unquote(kv[2]), Group: group}
			if v := cargoVerStr.FindStringSubmatch(kv[3]); v != nil {
				d.Spec, d.Editable = v[1], true
			} else if strings.HasPrefix(kv[3], "{") {
				if v := cargoInlGet.FindStringSubmatch(kv[3]); v != nil {
					d.Spec = v[1]
					d.Editable = !strings.Contains(kv[3], "path") && !strings.Contains(kv[3], "git")
				}
			}
			out = append(out, d)
		}
	}
	return out, nil
}

func applyCargo(data []byte, ops []Op) ([]byte, error) {
	if err := checkOps("cargo", ops, cargoGroups, cargoName, cargoSpec, false); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, op := range ops {
		// Locate the entry: a key line in a dependency section, or a dotted table.
		secs := sections(lines)
		lineIdx, tableStart, tableEnd, group := -1, -1, -1, ""
		for _, sec := range secs {
			if m := cargoDotted.FindStringSubmatch(sec.name); m != nil && unquote(m[2]) == op.Name {
				tableStart, tableEnd, group = sec.start-1, sec.end, m[1]
				continue
			}
			for _, g := range cargoGroups {
				if sec.name != g {
					continue
				}
				for i := sec.start; i < sec.end; i++ {
					if kv := cargoKV.FindStringSubmatch(lines[i]); kv != nil && unquote(kv[2]) == op.Name {
						lineIdx, group = i, g
					}
				}
			}
		}
		exists := lineIdx >= 0 || tableStart >= 0
		switch op.Action {
		case "add":
			if exists {
				return nil, fmt.Errorf("%s is already a dependency", op.Name)
			}
			g := op.Group
			if g == "" {
				g = "dependencies"
			}
			entry := fmt.Sprintf("%s = %q", op.Name, op.Spec)
			lines = insertInSection(lines, g, entry)
		case "update":
			if !exists {
				return nil, fmt.Errorf("%s is not a dependency", op.Name)
			}
			_ = group
			if lineIdx < 0 {
				return nil, fmt.Errorf("%s uses a [dependencies.%s] table; edit Cargo.toml manually", op.Name, op.Name)
			}
			kv := cargoKV.FindStringSubmatch(lines[lineIdx])
			switch {
			case cargoVerStr.MatchString(kv[3]):
				lines[lineIdx] = fmt.Sprintf("%s%s = %q", kv[1], kv[2], op.Spec)
			case strings.HasPrefix(kv[3], "{") && cargoInlVer.MatchString(kv[3]):
				nv := cargoInlVer.ReplaceAllString(kv[3], `${1}`+fmt.Sprintf("%q", op.Spec))
				lines[lineIdx] = fmt.Sprintf("%s%s = %s", kv[1], kv[2], nv)
			default:
				return nil, fmt.Errorf("%s has no simple version to update; edit Cargo.toml manually", op.Name)
			}
		case "remove":
			if !exists {
				return nil, fmt.Errorf("%s is not a dependency", op.Name)
			}
			if lineIdx >= 0 {
				lines = append(lines[:lineIdx], lines[lineIdx+1:]...)
			} else {
				lines = append(lines[:tableStart], lines[tableEnd:]...)
			}
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// insertInSection appends entry to the [group] table, creating it at the end
// of the file when missing.
func insertInSection(lines []string, group, entry string) []string {
	for _, sec := range sections(lines) {
		if sec.name != group {
			continue
		}
		at := sec.end
		for at > sec.start && strings.TrimSpace(lines[at-1]) == "" {
			at-- // keep blank separator lines after the table
		}
		out := append([]string{}, lines[:at]...)
		out = append(out, entry)
		return append(out, lines[at:]...)
	}
	out := append([]string{}, lines...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	return append(out, "["+group+"]", entry, "")
}
