#!/bin/sh
# Installs a built rivetpanel binary and the systemd units. Run as root from the
# repository root (after `make build`) or from an unpacked release archive; both
# keep the binaries in bin/. It never generates or overwrites secrets: create
# keys with `rivetpanel keygen`. rivet-agent is not installed here: it runs on
# remote nodes, see deploy/install-agent.sh and docs/agents.md.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ -x bin/rivetpanel ] || { echo "bin/rivetpanel not found: run make build, or run this from an unpacked release archive" >&2; exit 1; }
install -m 0755 bin/rivetpanel /usr/local/bin/rivetpanel
install -d -m 0750 /etc/rivetpanel /var/lib/rivetpanel /var/backups/rivetpanel
install -d -m 0700 /etc/rivetpanel/keys
[ -e /etc/rivetpanel/rivetpanel.env ] || install -m 0640 deploy/systemd/rivetpanel.env.example /etc/rivetpanel/rivetpanel.env
install -m 0644 deploy/systemd/rivetpanel.service deploy/systemd/rivetpanel-backup.service deploy/systemd/rivetpanel-backup.timer /etc/systemd/system/
systemctl daemon-reload
cat <<MSG
Installed. Next steps:
  1. Create an encryption key:   RIVET_KEY_DIR=/etc/rivetpanel/keys rivetpanel keygen
     (also creates the agent certificate authority; the service cannot write
     /etc/rivetpanel). Back the keys up separately from the database backups.
  2. Create the first admin:     set -a; . /etc/rivetpanel/rivetpanel.env; set +a; rivetpanel create-admin you@example.com
  3. Start:                      systemctl enable --now rivetpanel rivetpanel-backup.timer
  4. Put a TLS reverse proxy in front of 127.0.0.1:8080 (WebSocket upgrade required).
     Instead of step 2 you can open the panel and enter the one-time setup code
     printed in the log: journalctl -u rivetpanel | grep setup_code
Remote nodes: copy this release to each node and run deploy/install-agent.sh there.
MSG
