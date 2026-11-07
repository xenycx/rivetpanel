#!/bin/sh
# Installs the pinned Herdr release into ~/.local/bin (checked against the
# SHA-256 published in its release manifest) and runs the Herdr server, which
# keeps panes alive between terminal sessions.
set -eu
V=0.9.5
case "$(uname -m)" in
  x86_64|amd64) A=x86_64 SUM=14d9e8bc8b4a9e0118d77c32266f76d461f09b40342eb23752960bd89f59929d ;;
  aarch64|arm64) A=aarch64 SUM=17d7536f2da50859523680ef279dffcac8e4a235f4e46fe8da6c2ec44141897c ;;
  *) echo "Herdr has no Linux release for $(uname -m)" >&2; exit 1 ;;
esac
BIN="$HOME/.local/bin"
mkdir -p "$BIN" "$HOME/work" "$NPM_CONFIG_PREFIX"
if [ ! -x "$BIN/herdr" ] || [ "$(cat "$BIN/.herdr-release" 2>/dev/null)" != "$V-$A" ]; then
  echo "Downloading Herdr $V ($A)..."
  i=0
  until wget -q -O /tmp/herdr "https://github.com/herdrdev/herdr/releases/download/v$V/herdr-linux-$A"; do
    i=$((i+1)); [ "$i" -lt 5 ] || { echo "Could not download Herdr" >&2; exit 1; }
    sleep 5
  done
  echo "$SUM  /tmp/herdr" | sha256sum -c -
  chmod 755 /tmp/herdr
  mv /tmp/herdr "$BIN/herdr"
  echo "$V-$A" > "$BIN/.herdr-release"
fi
herdr --version
# Sockets left by the previous container would block the new server.
rm -f "$HOME/.config/herdr/herdr.sock" "$HOME/.config/herdr/herdr-client.sock"
cd "$HOME/work"
herdr server &
SERVER=$!
# Stop through the API so Herdr saves its layout before the container stops.
trap 'herdr server stop >/dev/null 2>&1 || kill "$SERVER" 2>/dev/null; wait "$SERVER"; exit 0' TERM INT
wait "$SERVER"
