// Package builtin embeds the first-party extension bundles built by
// `make extensions`. They are trusted because they ship inside the binary;
// a build without them (go build without make) simply offers none.
package builtin

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Bundles returns the embedded .rpx files by name.
func Bundles() map[string][]byte {
	out := map[string][]byte{}
	entries, _ := fs.ReadDir(dist, "dist")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rpx") {
			continue
		}
		if b, err := dist.ReadFile("dist/" + e.Name()); err == nil {
			out[e.Name()] = b
		}
	}
	return out
}
