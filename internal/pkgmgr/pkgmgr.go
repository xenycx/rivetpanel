// Package pkgmgr reads and edits dependency manifests (package.json,
// requirements.txt, pyproject.toml, Cargo.toml, go.mod) for the visual package
// manager. Edits are minimal and validated: names and version specs are checked
// against per-ecosystem patterns so a client can never inject extra lines,
// keys or options into a manifest.
package pkgmgr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/xenycx/rivetpanel/internal/lazyre"
)

// MaxManifestBytes bounds the manifests this package will parse.
const MaxManifestBytes = 1 << 20

// Dependency is one declared dependency.
type Dependency struct {
	Name     string `json:"name"`
	Spec     string `json:"spec"`     // version requirement as written; "" if not simple
	Group    string `json:"group"`    // e.g. dependencies, devDependencies, main, dev, direct, indirect
	Editable bool   `json:"editable"` // false for path/git/complex entries
}

// Op is one requested change.
type Op struct {
	Action string `json:"action"` // add | update | remove
	Name   string `json:"name"`
	Spec   string `json:"spec"`
	Group  string `json:"group"`
}

// Ecosystem describes how to handle one manifest kind.
type Ecosystem struct {
	ID     string   // npm | pip | cargo | gomod
	File   string   // manifest path relative to the workspace root
	Groups []string // groups a dependency can be added to; first is the default
	Parse  func(data []byte) ([]Dependency, error)
	Apply  func(data []byte, ops []Op) ([]byte, error)
}

// ErrUnsupported means the runtime has no visual package manager.
var ErrUnsupported = errors.New("package manager is not available for this runtime")

// ParseError is a user-facing manifest problem.
type ParseError struct{ File, Msg string }

func (e *ParseError) Error() string { return e.File + ": " + e.Msg }

// ForRuntime selects the ecosystem for a runtime. existing lists manifest
// files present in the workspace root (used to pick pyproject.toml when there
// is no requirements.txt).
func ForRuntime(runtime string, existing func(name string) bool) (*Ecosystem, error) {
	switch runtime {
	case "nodejs":
		return npm, nil
	case "python":
		if !existing("requirements.txt") && existing("pyproject.toml") {
			return pyproject, nil
		}
		return requirements, nil
	case "rust":
		return cargo, nil
	case "go":
		return gomod, nil
	}
	return nil, ErrUnsupported
}

var (
	npmName   = lazyre.New(`^(@[a-z0-9][a-z0-9._-]{0,213}/)?[a-z0-9][a-z0-9._-]{0,213}$`)
	pipName   = lazyre.New(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)
	cargoName = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
	goPath    = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._~/-]{0,199}$`)
	npmSpec   = lazyre.New(`^[A-Za-z0-9.*^~<>=|, +-]{1,128}$`)
	pipSpec   = lazyre.New(`^(?:[<>=!~]{1,3}[A-Za-z0-9.*+!_-]+)(?:\s*,\s*[<>=!~]{1,3}[A-Za-z0-9.*+!_-]+)*$`)
	cargoSpec = lazyre.New(`^[A-Za-z0-9.*^~<>=, +-]{1,64}$`)
	goVersion = lazyre.New(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.+-]+)?(?:\+incompatible)?$`)
)

// checkOps validates the structure common to all ecosystems.
func checkOps(eco string, ops []Op, groups []string, name, spec *lazyre.Regexp, specOptional bool) error {
	if len(ops) == 0 || len(ops) > 50 {
		return errors.New("provide between 1 and 50 changes")
	}
	for _, o := range ops {
		switch o.Action {
		case "add", "update", "remove":
		default:
			return fmt.Errorf("unknown action %q", o.Action)
		}
		if !name.MatchString(o.Name) {
			return fmt.Errorf("invalid package name %q", o.Name)
		}
		if o.Action != "remove" {
			if o.Spec == "" && !specOptional {
				return fmt.Errorf("a version is required for %s", o.Name)
			}
			if o.Spec != "" && !spec.MatchString(o.Spec) {
				return fmt.Errorf("invalid version %q for %s", o.Spec, o.Name)
			}
		}
		if o.Group != "" {
			ok := false
			for _, g := range groups {
				ok = ok || g == o.Group
			}
			if !ok {
				return fmt.Errorf("invalid group %q", o.Group)
			}
		}
	}
	return nil
}

func normPy(n string) string {
	return strings.ToLower(regexp.MustCompile(`[-_.]+`).ReplaceAllString(n, "-"))
}
