// Package paneldocs indexes the embedded RivetPanel guide for the AI
// assistant: it searches pages and returns a page or one of its sections.
package paneldocs

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	guide "github.com/xenycx/rivetpanel/docs/guide"
)

// Section is one "##" or "###" part of a page (the intro has no heading).
type Section struct {
	Heading string
	Anchor  string
	Text    string
}

// Page is one guide page.
type Page struct {
	ID       string // category/page, for example websites/domains
	URL      string // /docs/websites/domains
	Title    string
	Lede     string
	Category string
	Aliases  []string
	Sections []Section
}

var (
	once  sync.Once
	pages []Page
)

var numPrefix = regexp.MustCompile(`^\d+-`)

// Pages returns every page, loaded once.
func Pages() []Page {
	once.Do(func() { pages = load(guide.FS) })
	return pages
}

func load(fsys fs.FS) []Page {
	var out []Page
	cats := map[string]string{}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		dir, file := path.Split(p)
		dir = strings.TrimSuffix(dir, "/")
		cat := numPrefix.ReplaceAllString(dir, "")
		meta, body := frontmatter(string(raw))
		if file == "_category.md" {
			cats[cat] = meta["title"]
			return nil
		}
		id := cat + "/" + numPrefix.ReplaceAllString(strings.TrimSuffix(file, ".md"), "")
		pg := Page{ID: id, URL: "/docs/" + id, Title: meta["title"], Lede: meta["lede"], Category: cat, Sections: sections(body)}
		if a := strings.Trim(meta["aliases"], "[] "); a != "" {
			for _, x := range strings.Split(a, ",") {
				pg.Aliases = append(pg.Aliases, strings.TrimSpace(x))
			}
		}
		out = append(out, pg)
		return nil
	})
	for i := range out {
		if t := cats[out[i].Category]; t != "" {
			out[i].Category = t
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// frontmatter splits "---" metadata (one-line keys only) from the body.
func frontmatter(s string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(s, "---\n") {
		return meta, s
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return meta, s
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, " ") {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return meta, strings.TrimLeft(s[4+end+4:], "\n")
}

var anchorStrip = regexp.MustCompile(`[^a-z0-9 -]`)

func anchor(h string) string {
	return strings.ReplaceAll(anchorStrip.ReplaceAllString(strings.ToLower(h), ""), " ", "-")
}

func sections(body string) []Section {
	var out []Section
	cur := Section{}
	var b strings.Builder
	flush := func() {
		cur.Text = strings.TrimSpace(b.String())
		if cur.Text != "" || cur.Heading != "" {
			out = append(out, cur)
		}
		b.Reset()
	}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")) {
			flush()
			h := strings.TrimSpace(strings.TrimLeft(line, "#"))
			cur = Section{Heading: h, Anchor: anchor(h)}
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return out
}

// Hit is a search result: a page and its best-matching section.
type Hit struct {
	Page    Page
	Section Section
	Score   int
}

var wordRe = regexp.MustCompile(`[a-z0-9][a-z0-9.+_-]*`)

var stop = map[string]bool{"the": true, "a": true, "an": true, "to": true, "of": true, "and": true, "or": true, "in": true, "on": true, "for": true, "how": true, "do": true, "i": true, "my": true, "is": true, "can": true, "what": true, "with": true, "it": true, "does": true, "be": true}

func terms(q string) []string {
	var out []string
	for _, w := range wordRe.FindAllString(strings.ToLower(q), -1) {
		if !stop[w] && len(w) > 1 {
			out = append(out, strings.TrimRight(w, "s"))
		}
	}
	return out
}

// Search ranks sections by how many query words they contain, weighting
// titles and headings, and returns the best section of the best pages.
func Search(q string, n int) []Hit {
	ts := terms(q)
	if len(ts) == 0 {
		return nil
	}
	var hits []Hit
	for _, p := range Pages() {
		head := strings.ToLower(p.Title + " " + p.Lede + " " + strings.Join(p.Aliases, " ") + " " + p.ID)
		best := Hit{Page: p, Score: -1}
		for _, s := range p.Sections {
			h := strings.ToLower(s.Heading)
			t := strings.ToLower(s.Text)
			score, matched := 0, 0
			for _, w := range ts {
				m := false
				if strings.Contains(head, w) {
					score += 4
					m = true
				}
				if strings.Contains(h, w) {
					score += 6
					m = true
				}
				if c := strings.Count(t, w); c > 0 {
					score += min(c, 5) + 2
					m = true
				}
				if m {
					matched++
				}
			}
			score += matched * matched * 3 // sections matching more of the words win
			if score > best.Score {
				best.Score, best.Section = score, s
			}
		}
		if best.Score > 0 {
			hits = append(hits, best)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > n {
		hits = hits[:n]
	}
	return hits
}

// Find resolves "websites/domains", "domains", a /docs/ URL or an alias.
func Find(ref string) (Page, bool) {
	r := strings.ToLower(strings.TrimSpace(ref))
	r = strings.TrimPrefix(strings.TrimPrefix(r, "/"), "docs/")
	r, _, _ = strings.Cut(r, "#")
	r = strings.TrimSuffix(r, ".md")
	var byName []Page
	for _, p := range Pages() {
		if p.ID == r {
			return p, true
		}
		_, name, _ := strings.Cut(p.ID, "/")
		if name == r {
			byName = append(byName, p)
		}
		for _, a := range p.Aliases {
			if a == r {
				return p, true
			}
		}
	}
	if len(byName) == 1 {
		return byName[0], true
	}
	return Page{}, false
}

// Text renders a page, or only the sections whose heading or anchor
// contains section.
func (p Page) Text(section string) string {
	var b strings.Builder
	b.WriteString("# " + p.Title + "\n")
	if p.Lede != "" {
		b.WriteString(p.Lede + "\n")
	}
	b.WriteString("(" + p.URL + ")\n\n")
	want := strings.ToLower(strings.TrimSpace(section))
	n := 0
	for _, s := range p.Sections {
		if want != "" && !strings.Contains(strings.ToLower(s.Heading), want) && !strings.Contains(s.Anchor, want) {
			continue
		}
		n++
		if s.Heading != "" {
			b.WriteString("## " + s.Heading + " (" + p.URL + "#" + s.Anchor + ")\n")
		}
		b.WriteString(s.Text + "\n\n")
	}
	if want != "" && n == 0 {
		b.WriteString("No section matches; the sections are:")
		for _, s := range p.Sections {
			if s.Heading != "" {
				b.WriteString("\n- " + s.Heading)
			}
		}
	}
	return b.String()
}
