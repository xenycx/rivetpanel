# Backup and restore

```sh
rivetpanel backup /var/backups/rivetpanel/2026-09-30       # database + bot files
rivetpanel backup --include-keys DEST                     # also copy encryption keys
rivetpanel backup-verify DEST                             # checksums vs manifest
rivetpanel restore [--force] [--restore-keys] SRC         # panel must be stopped
rivetpanel verify                                         # can every stored secret be decrypted?
```

A backup directory contains:

| File | Notes |
| --- | --- |
| `rivetpanel.db` | `VACUUM INTO` snapshot: consistent including WAL contents. Never copy a live `.db` file by hand. |
| `bots.tar.gz` | Every bot workspace (regular files, directories, symlinks as links). |
| `keys/` | Only with `--include-keys`. **Required to read environment variables**; store separately from the database backup. |
| `manifest.json` | SHA-256 of the database and archive, key ids in use. |

Restore verifies checksums first, refuses to overwrite an existing database or
non-empty data root without `--force`, extracts bot files descriptor-relative
(entry names must be `<uuid>/...`; no traversal; symlinks are recreated but never
followed; setuid/setgid dropped), writes the database last and atomically, then
runs `verify`. Missing keys are reported by id, so you know exactly which key
file to bring back.

Hosted site files (`RIVET_SITES_DIR`, see `sites.md`) are **not** part of
this backup yet: the database restores the sites, their domains and release
history, but a site whose release directory is missing shows an "unavailable"
page until it is published again. Back that directory up with your host
backups, or keep the built files so they can be republished.

Bot files are archived while bots may be running; stop bots first for a
point-in-time consistent copy. Container state and Docker logs are not backed
up. Key rotation: add a new key, set `RIVET_ACTIVE_KEY_ID`, and keep the old
key files for as long as any stored value uses them.

This installation backup only sees workspaces on the panel host. For servers
assigned to `rivet-agent` nodes, use their manual or scheduled per-server
backups as well: those archives stream into `RIVET_BACKUP_DIR` on the panel,
but are intentionally not embedded in `rivetpanel backup`. Keep off-host copies
of both sets when remote workspaces must be recoverable after a node loss.
