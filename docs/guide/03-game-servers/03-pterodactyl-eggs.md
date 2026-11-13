---
title: Pterodactyl eggs
lede: Convert a Pterodactyl egg into a RivetPanel server type, review what could not be mapped, and save it only when it is right.
icon: package
audience: admin
glance:
  - PTDL_v1 and PTDL_v2 eggs
  - Eggs up to 512 KiB
  - Nothing saved until you press Save
related: [game-servers/administration, game-servers/overview]
---

## Import an egg

You need the `blueprints.manage` permission.

1. Open **Administration → Server types → Import Pterodactyl egg**.
2. Paste the egg export: `PTDL_v1` or `PTDL_v2` JSON, at most 512 KiB
   [enforced: panel].
3. Review the draft YAML and the list of warnings. Edit the YAML if needed.
   Eggs declare no resources or ports, so the draft uses 2 GiB, 2 CPUs and one
   automatic port: adjust them now.
4. Press **Save**.

Nothing is saved or downloaded until **Save**. The draft is then stored
through the normal server type import, with the same validation and immutable
revisions. See [Administration](08-administration.md).

## An egg is untrusted input

- It must be exactly one JSON object within the size limit. Unknown top-level
  fields are reported, never silently dropped [enforced: panel].
- Nothing it references is fetched while converting: no update URL, no image
  pull, no script run [enforced: panel].
- Its install script only ever runs later, like any server type's script: in
  the install container, as the unprivileged server user (not root, unlike
  Pterodactyl), with the usual hardening [enforced: docker].

## How an egg maps

| Egg | Server type |
| --- | --- |
| `docker_images` (or v1 `image`) | `images`: invalid references are skipped, at most 12; "Java N" labels set the Java version |
| `startup` with `{{VAR}}` | `startup.command` (same syntax); `{{server.build.default.port}}` and similar placeholders are mapped, unknown ones removed with a warning |
| `config.stop` | `startup.stop`, or `stop_signal: SIGINT` for `^C` |
| `config.startup.done` | `startup.done` (first marker only) |
| `variables` | `variables`: env name, name, description, default, `user_editable` → `editable` |
| `scripts.installation` | `install.script` run with the egg's shell (`bash` or `sh`), `install.image` from the egg's container; `/mnt/server` becomes `/workspace` |
| `config.files` parsers `properties`, `ini`, `file` | `config_files` formats `properties`, `ini` (one entry per section), `lines` |
| `features: [eula]` | A Minecraft EULA agreement |

Variable rules are mapped from these Laravel rules: `required`, `nullable`,
`string`, `integer`, `numeric`, `boolean`, `in:`, `min:`, `max:`, `between:`,
`regex:` (RE2-compatible only), `alpha_dash`, `alpha_num` and `url`.

## What is not imported

These are reported as warnings and left out:

- `yaml`, `json` and `xml` config parsers, and find-and-replace objects
- placeholders RivetPanel does not provide
- hidden variables (`user_viewable: false`): RivetPanel shows every variable
  on the **Configuration** page
- other features (`java_version`, `pid_limit`, …)
- `file_denylist` and `userInteraction`
- package-manager calls in install scripts, because they need root
- variables whose names are not `UPPER_SNAKE_CASE` or are provided by the
  panel
