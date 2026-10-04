#!/bin/sh
# Installs a built rivet-agent binary and its systemd unit on a remote node.
set -eu
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ -x bin/rivet-agent ] || { echo "build first: make build" >&2; exit 1; }
install -m 0755 bin/rivet-agent /usr/local/bin/rivet-agent
install -d -m 0750 /etc/rivet-agent /var/lib/rivet-agent /var/lib/rivet-agent/servers
[ -e /etc/rivet-agent/rivet-agent.env ] || install -m 0640 deploy/systemd/rivet-agent.env.example /etc/rivet-agent/rivet-agent.env
install -m 0644 deploy/systemd/rivet-agent.service /etc/systemd/system/rivet-agent.service
systemctl daemon-reload
cat <<MSG
Installed rivet-agent. Next steps:
  1. In RivetPanel, open Administration -> Nodes -> Add remote node.
  2. Run the one-time enrollment command shown there on this host.
  3. Start the agent: systemctl enable --now rivet-agent
  4. Check it: systemctl status rivet-agent
MSG
