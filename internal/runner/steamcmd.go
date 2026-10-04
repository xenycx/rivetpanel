package runner

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xenycx/rivetpanel/internal/blueprint"
	"github.com/xenycx/rivetpanel/internal/runtimes"
)

// SteamCMDHome is where SteamCMD keeps its own client files, inside the
// server's files so later updates do not download the client again.
const SteamCMDHome = workspaceMount + "/.steamcmd"

// steamAttempts bounds SteamCMD retries: the first run after a client
// self-update regularly fails with a transient app state.
const steamAttempts = 3

// SteamCMDScript is the shell script that installs or updates a Steam app
// in the SteamCMD container. Every interpolated value is validated: the app
// id is a number and the beta branch matches blueprint.ValidBeta. The login
// is always anonymous; no credentials exist to leak.
//
// A watchdog stops SteamCMD when the server's files grow beyond the
// blueprint's max_size_gb. This is enforced by the install script inside the
// container, not by a disk quota.
func SteamCMDScript(st blueprint.SteamCMD, beta string) (string, error) {
	if st.AppID < 1 || st.AppID > 1<<32-1 {
		return "", fmt.Errorf("invalid Steam app id")
	}
	if !blueprint.ValidBeta(beta) {
		return "", fmt.Errorf("the beta branch %q is not a valid branch name", beta)
	}
	maxGB := st.MaxSizeGB
	if maxGB <= 0 {
		maxGB = 40
	}
	app := strconv.FormatInt(st.AppID, 10)
	update := "+app_update " + app
	if beta != "" {
		update += " -beta " + beta
	}
	if st.Validate {
		update += " validate"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `set -u
export HOME=%[1]s
mkdir -p "$HOME"
LIMIT_KB=%[2]d
LOG="$HOME/rivet-steamcmd.log"
FLAG="$HOME/rivet-size-exceeded"
rm -f "$FLAG"
(
  while sleep 15; do
    used=$(du -sk %[3]s 2>/dev/null | cut -f1)
    if [ "${used:-0}" -gt "$LIMIT_KB" ]; then
      echo "RivetPanel: the server's files are larger than %[4]d GB; stopping SteamCMD." >&2
      : > "$FLAG"
      kill -KILL -1 2>/dev/null
      exit 0
    fi
  done
) &
WATCH=$!
ok=0
i=0
while [ $i -lt %[5]d ]; do
  i=$((i + 1))
  echo "RivetPanel: SteamCMD app %[6]s, attempt $i"
  : > "$LOG"
  steamcmd +@sSteamCmdForcePlatformType linux +force_install_dir %[3]s +login anonymous %[7]s +quit > "$LOG" 2>&1 &
  STEAM=$!
  tail -n +1 -f "$LOG" --pid=$STEAM 2>/dev/null || wait $STEAM
  wait $STEAM 2>/dev/null
  [ -e "$FLAG" ] && break
  if grep -q "Success! App '%[6]s'" "$LOG"; then ok=1; break; fi
  sleep 3
done
kill $WATCH 2>/dev/null
if [ -e "$FLAG" ]; then
  echo "RivetPanel: installation stopped: the size limit of %[4]d GB was reached." >&2
  exit 3
fi
if [ $ok -ne 1 ]; then
  echo "RivetPanel: SteamCMD did not report a successful install of app %[6]s." >&2
  exit 2
fi
for a in 64 32; do
  src="$HOME/.local/share/Steam/steamcmd/linux$a/steamclient.so"
  if [ -f "$src" ]; then mkdir -p %[3]s/.steam/sdk$a && cp -f "$src" %[3]s/.steam/sdk$a/steamclient.so; fi
done
echo "RivetPanel: app %[6]s is installed."
`, SteamCMDHome, int64(maxGB)<<20, workspaceMount, maxGB, steamAttempts, app, update)
	return b.String(), nil
}

// steamRuntime is the builder runtime that runs SteamCMD.
func steamRuntime(st blueprint.SteamCMD, beta string) (runtimes.Runtime, error) {
	script, err := SteamCMDScript(st, beta)
	if err != nil {
		return runtimes.Runtime{}, err
	}
	timeout := time.Duration(st.TimeoutMinutes) * time.Minute
	if timeout <= 0 {
		timeout = time.Hour
	}
	return runtimes.Runtime{ID: "steamcmd", DisplayName: "SteamCMD", Image: st.Image, BuilderImage: st.Image,
		BuildArgv: []string{"/bin/sh", "-c", script}, BuildTimeout: timeout}, nil
}

// ACFValue returns the first value of key in a Steam app manifest
// (appmanifest_<id>.acf, a VDF text file): `"key"<whitespace>"value"`. The
// value is returned only when it is a short run of digits or letters.
func ACFValue(b []byte, key string) string {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) != 2 || f[0] != `"`+key+`"` {
			continue
		}
		v := strings.Trim(f[1], `"`)
		if v == "" || len(v) > 32 {
			return ""
		}
		for _, r := range v {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return ""
			}
		}
		return v
	}
	return ""
}
