# RivetPanel documentation

RivetPanel is a control plane for Discord bots, Minecraft servers and static
sites, with an embedded local runner and preview remote execution nodes. The
current project version is **0.4.0**. Start with
the guides below.

## Install and operate

- [Deployment](deployment.md) — binary, systemd, reverse proxy, and first admin
- [Container deployment](container.md) — GHCR image and Docker Compose
- [OAuth setup](oauth.md) — GitHub and Discord sign-in
- [Email (Mailgun)](email.md) — password reset, invitations, alert emails and security notices
- [Notifications and support](support.md) — the bell, notification preferences and support tickets
- [Backups](backup.md) — installation backup, verification, and restore
- [Diagnostics and footprint](footprint.md) — memory expectations and measurement

## Use and extend

- [Features](features.md) — supported behavior and limits
- [Game servers](game-servers.md) — Minecraft server types, installation, allocations, blueprints and limits
- [Workspaces](workspaces.md) — teams, roles, and administrator oversight
- [Roles, permissions and email verification](permissions.md) — custom account roles, delegated administration, the permission catalog and the unverified-account policy
- [Bot Sites and static hosting](sites.md) — Page Studio, editable drafts, custom domains, DNS, and TLS through the proxy
- [AI operator](ai-operator.md) — providers, modes, tools, approvals, research, secret boundaries, diagnostics, and recovery
- [Custom dashboard widgets](widgets.md) — one JSON API for all supported languages
- [Automation API](automation.md) — scoped tokens and examples
- [Architecture](architecture.md) — state model and runner design
- [Remote nodes (preview)](agents.md) — install, enroll, place workloads, administer certificates, and understand current limits
- [Isolation](isolation.md) — container boundaries and explicit non-goals
- [Implementation status](implementation-status.md) — completed and remaining platform-overhaul work
- [Changelog](../CHANGELOG.md) — versioned release notes

The source, releases, issue tracker, and container package live at
[github.com/xenycx/rivetpanel](https://github.com/xenycx/rivetpanel).
