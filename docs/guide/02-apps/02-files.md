---
title: Files, SFTP and packages
lede: Edit an app's files in the browser or over SFTP, and manage its dependencies without touching a manifest by hand.
icon: folder
glance:
  - SFTP is off until RIVET_SFTP_LISTEN is set
  - 256 MiB per SFTP file by default
  - 32 SFTP connections at once
related: [apps/bots, apps/deploy-from-github, operate/configuration]
---

## The file manager

The **Files** tab is a web file manager for the app's workspace:
breadcrumbs, a filter, uploads with progress and cancel, zip extraction, and
a code editor that loads when you open a file.

### Sorting

Sort by name, size, date modified or type, ascending or descending, with
folders first or mixed in. Names sort in natural order, so `file2` comes
before `file10`. The browser remembers your choice.

### Create files and folders

**New file** and **New folder** accept a path below the current folder, such
as `src/index.js` or `src/lib`, and create any missing folders on the way.
Leading slashes, `.` and `..` are refused.

### Move and rename

Files and folders move three ways:

- **Move to…** in a row's menu opens a folder browser. Its destination field
  also takes a typed path.
- **Rename** accepts a path as well as a name: `data/.env.example`, `data/` to
  keep the name, `/` to start at the workspace root, `..` for the parent
  folder.
- Drag a row onto a folder, the "Up one folder" row or a breadcrumb.

Missing folders are created on the way. A move never replaces something
already at the destination (the API answers `409`), an open file follows its
move, and the confirmation offers **Undo**.

### Saving and conflicts

A save only succeeds if the file has not changed since you opened it. If
SFTP, a deployment or another editor changed it, the editor lets you
download your version, load theirs, or replace it deliberately. A failed save
keeps your text.

While a deployment or a restore replaces files, edits wait with a clear `409`.

### Site files

Bot Sites use the same explorer and conflict-aware editor for a private site
draft. Unlike an app's files, a save there never changes the live site:
**Publish draft** creates and activates an immutable release. Generated pages
use structured Page Studio settings with bounded custom HTML and CSS instead.

A new standalone site can start from a site template (landing page, docs and
blog, portfolio, coming soon, or a game server community with live status
from the public status page) instead of an empty folder. The template becomes
the first release and is edited like any other site. See
[Bot Sites](../04-websites/01-bot-sites.md) and
[Publishing](../04-websites/03-publishing.md).

## SFTP

SFTP is off unless an administrator sets `RIVET_SFTP_LISTEN`, for example to
`0.0.0.0:2022`. See [Configuration](../07-operate/02-configuration.md).

1. Connect to the panel's SFTP address with any SFTP client.
2. Sign in with your panel email and either your password or an SFTP key
   added under **Settings → SFTP and API keys**.
3. Open the folder of the app you want. You see one folder per app you may
   edit.

SFTP uses the same containment as the web file manager. [enforced: panel]
Rename keeps SFTP's standard replace semantics.

| Limit | Value |
|---|---|
| Access re-checked | About every 5 seconds, including open file handles [enforced: panel] |
| Shell, exec and port forwarding | Refused [enforced: panel] |
| Sign-in attempts | 3 per connection, 10 failures per minute per address [enforced: panel] |
| Connections at once | 32 [enforced: panel] |
| Largest file | `RIVET_SFTP_MAX_FILE_BYTES`, 256 MiB by default [enforced: panel] |

The host key is kept next to the database (`RIVET_SFTP_HOST_KEY`).

### Apps on a remote node

Folders of apps on a remote node are served through that node's agent:

- transfers pass through a private temporary file on the panel;
- an upload reaches the node in one atomic write when the file is closed;
- listings show no modification times;
- an offline node answers with an error.

> [!WARNING]
> SFTP does not work through Cloudflare's proxy or a Cloudflare Tunnel.
> Point the SFTP host name at the server directly.

## Package manager

The **Packages** tab reads and edits the project's manifest:

| Ecosystem | Manifest | Search |
|---|---|---|
| npm | `package.json` | Search on npm |
| Python | `requirements.txt`, or `[project].dependencies` in `pyproject.toml` | PyPI resolves an exact name |
| Cargo | `Cargo.toml` | Search on crates.io |
| Go modules | `go.mod` | The Go proxy resolves an exact name |

Names and version specs are matched against a pattern per ecosystem, so
nothing can be injected. [enforced: panel] Dependencies install the next time
the app starts. Java (Maven) and Ruby (Bundler) manifests are not edited by
the panel.

The tab stays greyed out until the project has its manifest, for example
`package.json` for a Node.js bot. Create the file in **Files** and the tab
becomes available straight away.

### Lockfiles

The package manager edits only the manifest, never a lockfile. For Node.js,
the build installs with `npm ci` when `package-lock.json` exists. When npm
refuses because the lockfile no longer matches `package.json` (npm error
code `EUSAGE`, for example after a version change here or in **Files**):

1. the build logs a warning;
2. it runs `npm install` once instead, which rewrites `package-lock.json` in
   the workspace;
3. the next build is a clean `npm ci` again.

Imported Node.js projects whose build command the panel generated behave the
same way. A custom build command runs exactly as written.
