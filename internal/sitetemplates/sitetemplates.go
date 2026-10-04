// Package sitetemplates embeds the built-in static site starters offered when
// creating a site. Each template is a directory: template.yaml plus files/,
// whose text files may use the placeholders below. A new site's first release
// is made from the rendered files.
//
// Placeholders (values are HTML-escaped, so they are safe in text and in
// attribute values): {{site_name}}, {{year}}, {{panel_url}}, {{status_url}}
// (the panel's public status page) and {{status_api}} (its JSON endpoint).
package sitetemplates

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

//go:embed all:data
var data embed.FS

// Template describes one site starter.
type Template struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Tags        []string `yaml:"tags" json:"tags"`
	// Accent and Theme ("light" | "dark") let a picker draw a card that
	// matches the design without loading the preview.
	Accent string `yaml:"accent" json:"accent"`
	Theme  string `yaml:"theme" json:"theme"`
	// StatusWidget: the site shows the panel's public status page (it needs
	// the status page to be enabled and the panel reachable over HTTPS).
	StatusWidget bool `yaml:"status_widget" json:"status_widget"`
	SPA          bool `yaml:"spa" json:"spa"`
	// Pages are the HTML pages, for the preview's page switcher.
	Pages []string `yaml:"-" json:"pages"`
	Files int      `yaml:"-" json:"files"`
	Bytes int64    `yaml:"-" json:"bytes"`
}

// File is one rendered file of a template.
type File struct {
	Path string
	Data []byte
}

// Vars fill the placeholders.
type Vars struct {
	SiteName string
	Year     int
	PanelURL string // e.g. https://panel.example.com ("" leaves status links empty)
}

var idRe = lazyre.New(`^[a-z0-9][a-z0-9-]{0,39}$`)

// List returns all templates sorted by name.
func List() []Template {
	ents, _ := fs.ReadDir(data, "data")
	out := []Template{}
	for _, e := range ents {
		if t, err := load(e.Name()); err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a template by ID.
func Get(id string) (Template, bool) {
	if !idRe.MatchString(id) {
		return Template{}, false
	}
	t, err := load(id)
	return t, err == nil
}

func load(id string) (Template, error) {
	b, err := fs.ReadFile(data, path.Join("data", id, "template.yaml"))
	if err != nil {
		return Template{}, err
	}
	var t Template
	if err := yaml.Unmarshal(b, &t); err != nil {
		return Template{}, fmt.Errorf("site template %s: %w", id, err)
	}
	if t.ID != id || t.Name == "" {
		return Template{}, fmt.Errorf("site template %s: id or name mismatch", id)
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	t.Pages = []string{}
	err = walk(id, func(rel string, b []byte) error {
		t.Files++
		t.Bytes += int64(len(b))
		if strings.HasSuffix(rel, ".html") {
			t.Pages = append(t.Pages, rel)
		}
		return nil
	})
	if err != nil {
		return Template{}, err
	}
	if t.Files == 0 {
		return Template{}, fmt.Errorf("site template %s: no files", id)
	}
	sort.Strings(t.Pages)
	return t, nil
}

func walk(id string, fn func(rel string, b []byte) error) error {
	root := path.Join("data", id, "files")
	return fs.WalkDir(data, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(data, p)
		if err != nil {
			return err
		}
		return fn(p[len(root)+1:], b)
	})
}

var textExt = map[string]bool{".html": true, ".css": true, ".js": true, ".txt": true, ".xml": true, ".json": true, ".md": true, ".svg": true}

// Render returns the template's files with the placeholders filled.
func Render(id string, v Vars) ([]File, error) {
	if _, ok := Get(id); !ok {
		return nil, fmt.Errorf("unknown site template %q", id)
	}
	panel := strings.TrimRight(v.PanelURL, "/")
	statusURL, statusAPI := "", ""
	if panel != "" {
		statusURL, statusAPI = panel+"/status", panel+"/api/v1/status"
	}
	r := strings.NewReplacer(
		"{{site_name}}", html.EscapeString(v.SiteName),
		"{{year}}", fmt.Sprint(v.Year),
		"{{panel_url}}", html.EscapeString(panel),
		"{{status_url}}", html.EscapeString(statusURL),
		"{{status_api}}", html.EscapeString(statusAPI),
	)
	var out []File
	err := walk(id, func(rel string, b []byte) error {
		if textExt[path.Ext(rel)] {
			b = []byte(r.Replace(string(b)))
		}
		out = append(out, File{Path: rel, Data: b})
		return nil
	})
	return out, err
}

var stylesheet = lazyre.New(`<link rel="stylesheet" href="([a-z0-9/_.-]+\.css)">`)

// Preview renders one page as a self-contained document: local stylesheets
// are inlined so it can be shown from a sandboxed frame that loads nothing
// else. Scripts do not run in the preview.
func Preview(id, page string, v Vars) ([]byte, error) {
	files, err := Render(id, v)
	if err != nil {
		return nil, err
	}
	if page == "" {
		page = "index.html"
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	doc, ok := byPath[page]
	if !ok || !strings.HasSuffix(page, ".html") {
		return nil, fmt.Errorf("site template %s has no page %q", id, page)
	}
	dir := path.Dir(page)
	return stylesheet.ReplaceAllFunc(doc, func(m []byte) []byte {
		href := string(stylesheet.FindSubmatch(m)[1])
		css, ok := byPath[path.Clean(path.Join(dir, href))]
		if !ok {
			return m
		}
		return append(append([]byte("<style>"), bytes.ReplaceAll(css, []byte("</"), []byte(`<\/`))...), []byte("</style>")...)
	}), nil
}
