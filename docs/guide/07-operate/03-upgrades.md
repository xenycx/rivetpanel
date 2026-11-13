---
title: Upgrades and rollback
lede: Upgrade RivetPanel so that every upgrade can be undone by restoring the backup you took just before it.
icon: history
audience: admin
glance:
  - Migrations are forward-only
  - Bots keep running during a panel restart
related: [operate/backups, operate/install, operate/remote-nodes]
---

## Before you upgrade

Database migrations run automatically at start and are **forward-only**: there
are no down migrations, and an older binary refuses a database whose schema is
newer than it knows. [enforced: panel] A rollback is therefore always a
restore of the backup taken before the upgrade.

Panel and `rivet-agent` must speak the same protocol version, so upgrade every
remote node together with the panel; see
[Remote nodes](04-remote-nodes.md#protocol-version).

## Upgrade with systemd

1. Read the release notes in [`CHANGELOG.md`](../../../CHANGELOG.md) for
   migrations and changed defaults.
2. Back up:
   `sudo rivetpanel backup /var/backups/rivetpanel/pre-X.Y.Z`. This holds the
   database snapshot and workspaces. Keep the key directory backed up
   separately, or add `--include-keys` and protect the result.
3. Check the backup: `rivetpanel backup-verify /var/backups/rivetpanel/pre-X.Y.Z`.
4. Copy per-app backups in `RIVET_BACKUP_DIR` and site releases too if you
   rely on them: they are not part of `rivetpanel backup`.
5. Keep the old binary:
   `sudo cp /usr/local/bin/rivetpanel /usr/local/bin/rivetpanel.prev`.
6. Install the new release with `deploy/install.sh` and start it.
7. Check the log, `rivetpanel doctor` and `/api/v1/readyz`, and confirm the
   schema migration ran (the **Database** check in Diagnostics shows the
   schema version).

Bots keep running during the restart; the panel adopts their containers.

## Upgrade the container image

Pin a version tag in `compose.yaml` instead of `latest`, so upgrades happen
only when you change it. Back up before each upgrade exactly as above, then
change the tag and run `docker compose up -d`.

## Roll back

1. Stop the panel.
2. Put the previous binary back (or pin the previous image tag).
3. Restore the backup taken before the upgrade, with that previous binary:
   `sudo rivetpanel restore --force /var/backups/rivetpanel/pre-X.Y.Z`.
   For the image, place the backup inside the mounted data directory and run
   `docker compose run --rm rivetpanel restore --force …`.
4. Start the panel.

> [!WARNING]
> Changes made after the upgrade (new bots, settings, accounts) are lost by a
> rollback. Bot containers started by the newer version are reconciled to the
> restored state.

## Coming from the previous product (0.4.0)

RivetPanel is a deliberate clean break from its 0.4.0 predecessor. It does
not read, convert or modify the former installation: the service, data
directory, environment prefix, container labels and backup format all
changed, and former backups cannot be restored (`backup-verify` and `restore`
say so). Accounts, bots, environment variables, schedules and settings must
be re-created.

At start the panel looks for traces of a former installation and changes
nothing:

- environment variables with the former prefixes;
- the former data directory;
- a former database next to the RivetPanel database;
- **running** containers with the former management label.

Each one is logged as a `WARNING` and shown in
**Administration → Diagnostics** as "Former installation".

The step-by-step clean-up is in
[`upgrading-from-0.4.md`](../../upgrading-from-0.4.md): stopping and disabling
the former service, removing its containers so that no Discord token runs
twice, moving its data aside, the new SFTP host key, and re-creating accounts
and bots.
