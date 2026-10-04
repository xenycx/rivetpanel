package blueprint

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// StartupCommand converts the startup template to a shell command line where
// {{VAR}} becomes "${VAR}". The values reach the process through the
// environment, so the shell expands them without re-parsing them as code.
func StartupCommand(tpl string) string {
	return templateRe.ReplaceAllString(tpl, `$${$1}`)
}

// StartupArgv is the container entrypoint and arguments for a startup
// template. `exec` makes the server process PID 1 so it receives signals and
// console input directly.
func StartupArgv(tpl string) (entrypoint, argv []string) {
	return []string{"/bin/sh", "-c"}, []string{"exec " + StartupCommand(tpl)}
}

// Render substitutes {{VAR}} with values. Unknown names render empty.
func Render(tpl string, vars map[string]string) string {
	return templateRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := templateRe.FindStringSubmatch(m)[1]
		return vars[name]
	})
}

// ApplyConfig returns content with the config file's edits applied. Values
// are rendered with vars and may not contain line breaks.
func ApplyConfig(content []byte, c ConfigFile, vars map[string]string) ([]byte, error) {
	switch c.Format {
	case "properties":
		return applyProperties(content, c.Set, vars)
	case "ini":
		return applyINI(content, c.Section, c.Set, vars)
	case "lines":
		return applyLines(content, c.Replace, vars)
	}
	return nil, fmt.Errorf("unsupported format %q", c.Format)
}

func oneLine(v string) string {
	return strings.NewReplacer("\n", "", "\r", "", "\x00", "").Replace(v)
}

// applyProperties sets keys in a Java .properties file, keeping comments,
// order and unrelated keys; missing keys are appended.
func applyProperties(content []byte, set map[string]string, vars map[string]string) ([]byte, error) {
	pending := make(map[string]string, len(set))
	for k, v := range set {
		pending[k] = oneLine(Render(v, vars))
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimLeft(line, " \t")
		if trim == "" || trim[0] == '#' || trim[0] == '!' {
			out.WriteString(line + "\n")
			continue
		}
		key := trim
		if i := strings.IndexAny(trim, "=:"); i >= 0 {
			key = strings.TrimRight(trim[:i], " \t")
		}
		if v, ok := pending[key]; ok {
			out.WriteString(key + "=" + v + "\n")
			delete(pending, key)
			continue
		}
		out.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.WriteString(k + "=" + pending[k] + "\n")
	}
	return out.Bytes(), nil
}

func applyLines(content []byte, rules []LineReplace, vars map[string]string) ([]byte, error) {
	res := make([]*regexp.Regexp, len(rules))
	for i, r := range rules {
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return nil, err
		}
		res[i] = re
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		for i, re := range res {
			if re.MatchString(line) {
				line = oneLine(Render(rules[i].With, vars))
				break
			}
		}
		out.WriteString(line + "\n")
	}
	return out.Bytes(), sc.Err()
}

// applyINI sets keys in one section of an INI file ("" = before the first
// section header), keeping comments, order and other sections. Missing keys
// are appended at the end of the section; a missing section is appended at
// the end of the file.
func applyINI(content []byte, section string, set map[string]string, vars map[string]string) ([]byte, error) {
	pending := make(map[string]string, len(set))
	for k, v := range set {
		pending[k] = oneLine(Render(v, vars))
	}
	flush := func(out *bytes.Buffer) {
		keys := make([]string, 0, len(pending))
		for k := range pending {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.WriteString(k + "=" + pending[k] + "\n")
			delete(pending, k)
		}
	}
	var out bytes.Buffer
	cur, seen := "", section == ""
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			if cur == section {
				flush(&out) // leaving the target section
			}
			cur = strings.TrimSpace(trim[1 : len(trim)-1])
			if cur == section {
				seen = true
			}
			out.WriteString(line + "\n")
			continue
		}
		if cur == section && trim != "" && trim[0] != ';' && trim[0] != '#' {
			if i := strings.IndexByte(trim, '='); i > 0 {
				key := strings.TrimSpace(trim[:i])
				if v, ok := pending[key]; ok {
					out.WriteString(key + "=" + v + "\n")
					delete(pending, key)
					continue
				}
			}
		}
		out.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		if cur == section && seen {
			flush(&out)
		} else if section == "" {
			// Top-level keys must come before the first header.
			var head bytes.Buffer
			flush(&head)
			return append(head.Bytes(), out.Bytes()...), nil
		} else {
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString("[" + section + "]\n")
			flush(&out)
		}
	}
	return out.Bytes(), nil
}
