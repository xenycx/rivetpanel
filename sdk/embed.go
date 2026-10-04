// Package sdk embeds the bot-side telemetry snippets served by the panel.
package sdk

import "embed"

// FS holds the snippets: rivetpanel.js (discord.js), rivetpanel.py (discord.py),
// rivetpanel.rb (discordrb), go/rivetpanel.go (any Go library), rivetpanel.rs
// (serenity/poise) and RivetPanel.java (JDA and others).
//
//go:embed rivetpanel.js rivetpanel.py rivetpanel.rb go/rivetpanel.go rivetpanel.rs RivetPanel.java
var FS embed.FS

// Files maps the language names the API serves to snippet files.
var Files = map[string]string{
	"discordjs": "rivetpanel.js", "discordpy": "rivetpanel.py", "ruby": "rivetpanel.rb",
	"go": "go/rivetpanel.go", "rust": "rivetpanel.rs", "java": "RivetPanel.java",
}
