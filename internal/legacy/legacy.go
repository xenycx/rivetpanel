// Package legacy recognises traces of the product's former identity
// (BotPanel 0.4.0, also published as BotForge). RivetPanel is a deliberate
// clean break from it: nothing here reads, converts or changes legacy data.
// It only finds it so the operator gets an explanation instead of a panel that
// silently ignores an existing installation.
//
// This package and docs/upgrading-from-0.4.md are the only places outside
// the changelog that may spell the former namespace (see `make namespace-check`).
package legacy

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Release is how the former product is named in messages.
	Release = "BotPanel 0.4.0"
	// DataDir is the former default state directory.
	DataDir = "/var/lib/botpanel"
	// DBName is the former database (and backup snapshot) file name.
	DBName = "botpanel.db"
	// ContainerLabel marks containers the former panel managed.
	ContainerLabel = "botpanel.managed"
	// Service is the former systemd unit.
	Service = "botpanel.service"
	// DocsPath is where the migration and clean-up steps are documented.
	DocsPath = "docs/upgrading-from-0.4.md"
)

var envPrefixes = []string{"BOTPANEL_", "BOTFORGE_"}

// dataDir is DataDir; tests point it elsewhere.
var dataDir = DataDir

// Finding is one trace of a former installation.
type Finding struct {
	What string // short description for logs and diagnostics
}

// Detect looks for former environment variables, the former state directory
// and a former database beside dbPath. environ is os.Environ() in production.
func Detect(environ []string, dbPath string) []Finding {
	var out []Finding
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		for _, p := range envPrefixes {
			if strings.HasPrefix(name, p) {
				names = append(names, name)
			}
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		out = append(out, Finding{What: "environment variables with the former prefix are set and ignored: " + strings.Join(names, ", ")})
	}
	if st, err := os.Stat(dataDir); err == nil && st.IsDir() {
		out = append(out, Finding{What: "the former data directory " + dataDir + " exists"})
	}
	if dbPath != "" {
		p := filepath.Join(filepath.Dir(dbPath), DBName)
		if _, err := os.Stat(p); err == nil {
			out = append(out, Finding{What: "a former database " + p + " is next to the RivetPanel database"})
		}
	}
	return out
}

// Explanation is appended to every warning about a finding.
const Explanation = "RivetPanel does not read, convert or modify " + Release + " installations (a deliberate clean break). " +
	"Bots, accounts and settings from it are not imported, and its containers keep running with their own Discord tokens. " +
	"See " + DocsPath + " for the clean-up and re-creation steps."

// IsBackup reports whether dir looks like a backup written by the former
// panel (its database snapshot has the former name and no RivetPanel one).
func IsBackup(dir string, sha256 map[string]string, currentDB string) bool {
	if _, ok := sha256[currentDB]; ok {
		return false
	}
	if _, ok := sha256[DBName]; ok {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, DBName))
	return err == nil
}
