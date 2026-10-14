package sitehost

import (
	"path"
	"strings"
)

// Directories whose files build tools name by content hash.
var immutableDirs = []string{"_app/immutable/", "_astro/", "_next/static/", "_nuxt/"}

var hashedExt = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true,
	".ico": true, ".wasm": true, ".map": true, ".mp4": true, ".webm": true,
}

// cacheControl picks the Cache-Control for a file by name. HTML revalidates
// on every visit so a new release shows immediately; fingerprinted build
// output is cached for a year (its name changes when its content does); other
// assets are cached for an hour and may be served stale for a day while the
// browser or CDN revalidates.
func cacheControl(name, ext string) string {
	if ext == ".html" || ext == ".htm" || ext == "" {
		return "no-cache"
	}
	if immutable(name, ext) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600, stale-while-revalidate=86400"
}

func immutable(name, ext string) bool {
	for _, d := range immutableDirs {
		if strings.HasPrefix(name, d) || strings.Contains(name, "/"+d) {
			return true
		}
	}
	// Vite/Rollup style "assets/index-B4x9kQ2a.js": a hash of at least eight
	// letters and digits (both present) inside an assets/ directory.
	if !hashedExt[ext] || !(strings.HasPrefix(name, "assets/") || strings.Contains(name, "/assets/")) {
		return false
	}
	base := strings.TrimSuffix(path.Base(name), ext)
	// Vite's hashes are eight base64url characters and may contain "-".
	if n := len(base); n > 9 && (base[n-9] == '-' || base[n-9] == '.') && hashLike(base[n-8:]) {
		return true
	}
	i := strings.LastIndexAny(base, ".-")
	if i < 0 {
		return false
	}
	return hashLike(base[i+1:])
}

func hashLike(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	var digit, letter bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			letter = true
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return digit && letter
}
