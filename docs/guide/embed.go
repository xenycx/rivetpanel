// Package guide embeds the RivetPanel guide so the panel can read it at run
// time (the AI assistant searches and quotes it). The Markdown files stay
// the single source; see WRITING.md.
package guide

import "embed"

// FS holds every published page (<category>/<page>.md, _category.md files).
//
//go:embed */*.md
var FS embed.FS
