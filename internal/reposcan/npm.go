package reposcan

import "strings"

// NPMCleanInstall is the shell snippet that installs a Node project's
// dependencies. With a package-lock.json it runs npm ci; when npm ci refuses
// because the lockfile no longer matches package.json (code EUSAGE, e.g.
// after a version change in Packages), it runs npm install once instead,
// which rewrites the lockfile so the next build is a clean install again.
// Any other npm ci failure stops the script with npm's exit code. The
// snippet is safe under `set -e`. flags are extra npm options such as
// "--omit=dev"; the runtime catalog's nodejs recipe uses the same logic.
func NPMCleanInstall(flags string) string {
	opts := strings.TrimSpace(flags + " --no-audit --no-fund")
	return strings.Join([]string{
		"if [ -f package-lock.json ]; then",
		"  { npm ci " + opts + " 2>&1 && echo 0 >/tmp/.npm-ci.rc || echo $? >/tmp/.npm-ci.rc; } | tee /tmp/.npm-ci.log",
		"  rc=$(cat /tmp/.npm-ci.rc)",
		"  if [ \"$rc\" != 0 ]; then",
		"    if grep -q 'code EUSAGE' /tmp/.npm-ci.log; then",
		"      echo 'RivetPanel warning: package-lock.json is out of sync with package.json; running npm install to update it.'",
		"      npm install " + opts,
		"    else",
		"      exit \"$rc\"",
		"    fi",
		"  fi",
		"else",
		"  npm install " + opts,
		"fi",
	}, "\n")
}
