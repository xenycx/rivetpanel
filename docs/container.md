# Container deployment

Published images are available from `ghcr.io/xenycx/rivetpanel`. Tags named
`latest`, `X.Y.Z`, `X.Y`, and the commit SHA are produced by GitHub Actions.

```sh
curl -O https://raw.githubusercontent.com/xenycx/rivetpanel/main/compose.yaml
curl -O https://raw.githubusercontent.com/xenycx/rivetpanel/main/deploy/systemd/rivetpanel.env.example
mv rivetpanel.env.example .env
docker compose run --rm rivetpanel keygen
docker compose run --rm rivetpanel create-admin you@example.com
docker compose up -d
```

The Compose file stores the database, keys, backups, and bot workspaces in the
`rivetpanel-data` volume. Back up that volume. The Docker socket mount is required
by the in-process runner and gives the panel root-equivalent control of the
host. Do not describe the container as a security boundary for the panel.

Use a reverse proxy for HTTPS and set `RIVET_PUBLIC_URL` to its public origin.
Pin a version tag instead of `latest` for controlled upgrades.

The image also contains `rivet-agent`. For remote nodes, enable the agents
module on the panel and publish raw TCP port 8444 (the commented Compose port),
then run the agent on each remote Docker host with its own state/data volume.
Do not send the agent port through the HTTP reverse proxy. The supported
systemd enrollment path and current remote-feature limits are in
[`agents.md`](agents.md).
