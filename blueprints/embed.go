// Package blueprints embeds the first-party game-server blueprints.
package blueprints

import "embed"

// FS holds one YAML file per built-in blueprint.
//
//go:embed *.yaml
var FS embed.FS
