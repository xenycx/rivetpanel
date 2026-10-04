#!/bin/sh
# Installs a built rivetpanel binary and the systemd units. Run as root from the repository root.
# It never generates or overwrites secrets: create keys with `rivetpanel keygen`.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ -x bin/rivetpanel ] || { echo "build first: make build" >&2; exit 1; }
install -m 0755 bin/rivetpanel /usr/local/bin/rivetpanel
install -d -m 0750 /etc/rivetpanel /var/lib/rivetpanel /var/backups/rivetpanel
install -d -m 0700 /etc/rivetpanel/keys
[ -e /etc/rivetpanel/rivetpanel.env ] || install -m 0640 deploy/systemd/rivetpanel.env.example /etc/rivetpanel/rivetpanel.env
install -m 0644 deploy/systemd/rivetpanel.service deploy/systemd/rivetpanel-backup.service deploy/systemd/rivetpanel-backup.timer /etc/systemd/system/
systemctl daemon-reload
cat <<MSG
Installed. Next steps:
  1. Create an encryption key:   RIVET_KEY_DIR=/etc/rivetpanel/keys rivetpanel keygen
     Back the key up somewhere separate from the database backups.
  2. Create the first admin:     set -a; . /etc/rivetpanel/rivetpanel.env; set +a; rivetpanel create-admin you@example.com
  3. Start:                      systemctl enable --now rivetpanel rivetpanel-backup.timer
  4. Put a TLS reverse proxy in front of 127.0.0.1:8080 (WebSocket upgrade required).
MSG
