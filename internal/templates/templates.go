// Package templates embeds the quickstart bot projects offered when creating a
// bot. Each template is a directory: template.yaml plus files/ copied verbatim
// into the new bot's workspace.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/xenycx/rivetpanel/internal/lazyre"
	"github.com/xenycx/rivetpanel/sdk"
)

//go:embed all:data
var data embed.FS

// Template describes one starter project.
type Template struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Runtime     string `yaml:"runtime" json:"runtime"`
	Language    string `yaml:"language" json:"language"`
	SDK         string `yaml:"sdk" json:"-"` // js | py | "" : also copy the telemetry snippet
	// Argv overrides the runtime's default command (validated like any argv).
	Argv []string `yaml:"argv" json:"argv,omitempty"`

	// Setup guidance. Never contains secret values: Default is only allowed
	// for non-secret variables.
	Version           int      `yaml:"version" json:"version"`
	TestedWith        string   `yaml:"tested_with" json:"tested_with"`
	FirstStart        string   `yaml:"first_start" json:"first_start"`
	PrivilegedIntents []string `yaml:"privileged_intents" json:"privileged_intents"`
	Env               []EnvVar `yaml:"env" json:"env"`
	Setup             []string `yaml:"setup" json:"setup"`
}

// EnvVar is a variable the template's code reads.
type EnvVar struct {
	Name        string `yaml:"name" json:"name"`
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description" json:"description"`
	Secret      bool   `yaml:"secret" json:"secret"`
	Required    bool   `yaml:"required" json:"required"`
	Default     string `yaml:"default" json:"default,omitempty"`
}

var envName = lazyre.New(`^[A-Z_][A-Z0-9_]{0,63}$`)

// File is one file to create in the workspace.
type File struct {
	Path string
	Data []byte
}

// List returns all templates sorted by name.
func List() []Template {
	ents, _ := fs.ReadDir(data, "data")
	var out []Template
	for _, e := range ents {
		if t, err := load(e.Name()); err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func load(id string) (Template, error) {
	b, err := fs.ReadFile(data, path.Join("data", id, "template.yaml"))
	if err != nil {
		return Template{}, err
	}
	var t Template
	if err := yaml.Unmarshal(b, &t); err != nil {
		return Template{}, fmt.Errorf("template %s: %w", id, err)
	}
	if t.ID != id {
		return Template{}, fmt.Errorf("template %s: id mismatch", id)
	}
	for _, e := range t.Env {
		if !envName.MatchString(e.Name) || (e.Secret && e.Default != "") {
			return Template{}, fmt.Errorf("template %s: invalid variable %q", id, e.Name)
		}
	}
	if t.PrivilegedIntents == nil {
		t.PrivilegedIntents = []string{}
	}
	return t, nil
}

// Get returns a template by ID.
func Get(id string) (Template, bool) {
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return Template{}, false // IDs are plain lowercase words: no path tricks
		}
	}
	t, err := load(id)
	return t, err == nil
}

// Files returns every file of a template (plus its SDK snippet).
func Files(id string) ([]File, error) {
	t, ok := Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown template %q", id)
	}
	root := path.Join("data", id, "files")
	var out []File
	err := fs.WalkDir(data, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(data, p)
		if err != nil {
			return err
		}
		// go:embed skips directories containing go.mod, so those ship as .tmpl.
		out = append(out, File{Path: strings.TrimSuffix(p[len(root)+1:], ".tmpl"), Data: b})
		return nil
	})
	if err != nil {
		return nil, err
	}
	switch t.SDK {
	case "js", "py", "rb":
		name := map[string]string{"js": "rivetpanel.js", "py": "rivetpanel.py", "rb": "rivetpanel.rb"}[t.SDK]
		b, err := sdk.FS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: name, Data: b})
	}
	return out, nil
}
