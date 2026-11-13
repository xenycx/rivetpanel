---
title: Extensions
lede: Add signed, sandboxed features to the panel, such as Discord insights, Discord ChatOps and a database browser, and write your own.
icon: package
audience: admin
aliases: [extensions]
glance:
  - 3 extensions ship with the panel
  - Uploads up to 16 MiB, signed with Ed25519
  - Up to 128 MiB and 30 s per call
related: [operate/security, operate/host-and-logs, teams/roles-and-permissions, apps/services]
---

## What extensions do

Extensions add features without changing the panel: pages in the sidebar,
tabs and overview cards on bot pages, scheduled jobs, reactions to bot events
and HTTPS endpoints for other services. Each one is a signed WebAssembly
bundle that runs sandboxed inside the panel process and can do only what an
administrator granted it.

| Extension | What it does |
| --- | --- |
| **Discord bot insights** | A Discord card on each bot's **Manage** tab: the bot user, application, approximate server count, invite link and token state. Keeps the bot's name and avatar current and tells the owner when Discord resets the token |
| **Discord ChatOps** | Slash commands in your Discord server: `/status`, `/logs`, `/start`, `/stop`, `/restart`, `/deploy`, each run with the person's own panel permissions |
| **Database browser** | A **Data** tab on bots with database services or SQLite files: browse and query PostgreSQL, MariaDB and SQLite tables, Redis keys and MongoDB collections. Reads are read-only; a change runs only after you confirm it in a panel dialog |

## Turn extensions on

Extensions are a preview module, off by default.

1. Switch **Extensions** to Preview under **Administration → Modules** (or set
   `RIVET_MODULES=extensions=preview`) and restart.
2. Open **Administration → Extensions**. It lists the built-in extensions and
   any uploaded ones.
3. Choose an extension and review it. The page first states how this exact
   bundle is contained: where it comes from (shipped with the panel, or the
   publisher key that signed it, with its SHA-256), its sandbox limits, the
   internet hosts it can reach (including Discord's API when the panel calls
   it on the extension's behalf), and that uninstalling deletes everything it
   stored.
4. Review the permissions. Each is grouped by what it touches (bots, people,
   internet, its own data) and shown with the extension's reason and the
   safeguard the panel enforces on every use. Permissions are not ranked as
   risky or safe: each is shown with its limit. Required ones must all be
   granted; optional ones can be left off.
5. Press **Install and turn on**. The grant is stored and the extension
   enabled.

A later version that asks for more is not used until an administrator reviews
it again. [enforced: panel]

### Who may do what

| Action | Who |
| --- | --- |
| Install, upgrade, switch version, uninstall, grant capabilities, trust or revoke publisher keys | Built-in administrators |
| Turn installed extensions on and off, change settings, run jobs, view status | The `extensions.manage` permission |
| Use an extension's pages marked `"perm": "extension"` | Roles holding **Use <extension name>** (`ext:<extension id>`) |
| Turn an extension on for one bot | People who may manage that bot's environment variables |
| Let an extension act for them (ChatOps) | Each person, under **Settings → Extensions** |

API clients cannot reach any of these routes; they are session-only.
[enforced: panel]

### Give a role access to one extension

Every installed extension has its own permission, `ext:<extension id>` (for
example `ext:dev.rivetpanel.db-browser`). While the extensions module is on,
**Administration → Roles** lists it under **Extensions** as
**Use <extension name>**. A custom role, or the built-in User role, that holds
it sees and can use the nav items, pages and routes the extension marks with
`"perm": "extension"`. Nothing else changes: the extension still acts only
with the capabilities an administrator granted and, for bots, with the
person's own permissions.

- It is a resource permission. It does not open the administration area, does
  not make an account outrank another, and is not part of the User role's
  factory defaults. Pages that configure an extension should keep
  `"perm": "extensions.manage"`.
- Built-in administrators hold every extension permission. A delegated role
  manager can grant only the ones it holds itself.
- Uninstalling an extension removes its permission from every role and API
  client, so reinstalling it, or another extension with the same id, starts
  with nobody holding it. Turning an extension or the module off keeps the
  permission on roles; it just has nothing to open.
- An API client needs the permission in its own list too.

### Upload an extension

An uploaded bundle (`.rpx`, at most 16 MiB) must be signed with an Ed25519
key an administrator trusts under
**Administration → Extensions → Trusted publisher keys**. [enforced: panel]
Revoking a key stops every extension running a version it signed. Unsigned
bundles are accepted only when the panel runs with `RIVET_ENV=development`.

Built-in extensions need no key: they ship inside the panel binary, which you
already trust. Every bundle, built-in or uploaded, is stored in the database,
so `rivetpanel backup` covers installed extensions and you can switch back to
any of the three newest versions without uploading again.

### Recovery

If an extension misbehaves, start the panel with
`RIVET_EXTENSIONS_SAFE_MODE=1`: no extension code runs and no extension
appears in the interface, while everything stays installed. An extension that
fails five times in a row stops by itself; its managers get a notification,
and **Try again** restarts it.

## Discord bot insights

1. Install it under **Administration → Extensions**.
2. On a bot's **Manage** tab, the **Discord** card asks to be turned on for
   that bot. Someone who may manage the bot's variables turns it on.

The panel finds the bot token in the bot's variables (`DISCORD_TOKEN`,
`DISCORD_BOT_TOKEN`, `BOT_TOKEN` and similar, or any variable that looks like
a token) and calls Discord's read-only API with it. **The extension never sees
the token**: the panel attaches it, and the extension learns only whether a
token was present, what Discord answered, and a fingerprint that changes when
the token changes. [enforced: panel]

A job refreshes every enabled bot every 30 minutes. When Discord rejects a
token (it resets tokens it finds published), the bot's owner gets one
notification per token.

## Discord ChatOps

ChatOps uses Discord's HTTP interactions: Discord sends each command to the
panel as a signed request, so no gateway connection or bot process is needed.
The panel must be reachable from the internet over HTTPS.

1. In the [Discord Developer Portal](https://discord.com/developers/applications),
   create an application, or reuse the one for Discord sign-in.
2. Install **Discord ChatOps** and fill in its settings: **Application ID** and
   **Public key** (General Information) and **Client secret** (OAuth2).
   Optionally set a **Server ID** to register the commands in one server,
   where they appear at once.
3. Set the application's **Interactions Endpoint URL** to the address shown on
   the extension's page, for example
   `https://panel.example.com/api/v1/ext-public/dev.rivetpanel.discord-chatops/interactions`.
4. Open **ChatOps** in the sidebar and press **Register commands**.
5. Install the application in your server with the `applications.commands`
   scope.
6. Each person links Discord under **Settings → Connected accounts**, then
   chooses what ChatOps may do for them under **Settings → Extensions**: see
   bots, read output, start, stop and restart, deploy.

How it is protected:

- Every request's Ed25519 signature and timestamp (at most five minutes old)
  are checked **before** any extension code runs; forged requests get `401`
  and never reach the extension. [enforced: panel]
- A command runs as the panel account linked to the Discord user who ran it,
  re-read on every request, so its current role, bot access and disabled
  state apply. Consent is per person and per scope, and never adds
  permissions. [enforced: panel]
- `/start`, `/stop`, `/restart` and `/deploy` reply with a confirmation button
  that only the same Discord user can press, once, within one minute.
  [enforced: panel]
- Replies are visible only to the person who ran the command.
- The client secret is used only to register commands. The extension cannot
  read it, and the panel sends it only to `discord.com`.

## Database browser

Install it, then open the **Data** tab on a bot that has a database service
or a SQLite database in its files. The tab needs permission to manage the
bot's environment, the same as revealing the database password. For services
the bot must be running, because its services run while it runs; SQLite files
can be browsed while it is stopped.

| Source | Browse | Query |
| --- | --- | --- |
| PostgreSQL | Tables | SQL |
| MariaDB | Tables | SQL |
| Redis | Keys (first scan page) | One command per line, quotes allowed |
| MongoDB | Databases and collections | `find`, `count`, `aggregate` (JSON filter or pipeline) |
| SQLite file | Tables and views | SQL |

Pick a database on the left, then a table. **Data** pages through its rows,
100 at a time, sortable by any column; **Columns** lists its columns and
types; **SQL** runs your own queries, and recent ones are kept while the tab
is open. Click a row to read and copy every value in full; **Copy CSV** copies
the rows shown. Redis and MongoDB open in their command and query editors.
The full-screen button (top right) lets the tab fill the panel's window;
**Exit full screen** or Esc brings it back.

### How queries run

Each query runs in a **disposable client container** started from the
service's own image on the bot's private network: no workspace, a read-only
root, no capabilities, 128 MiB of memory, 64 processes and 20 seconds.
[enforced: docker] The password reaches it only through its environment, and
the query text never through a shell command line. Results are limited to 500
rows and 1 MiB.

**Run** is read-only, enforced by the database where it can be:

- PostgreSQL and MariaDB reads run as a separate `rivet_reader` account with
  only read rights (`pg_read_all_data`; `SELECT` and `SHOW VIEW` on the `bot`
  database). The panel re-creates it for every query with a new random
  password that expires within minutes, so a query that switches its own
  session to read-write still cannot change, drop or create anything.
- Redis reads are limited to an allowlist of read commands, and MongoDB to
  `find`, `count` and `aggregate` without `$out` or `$merge`.
  [enforced: panel]

**Run as change…** asks the panel to confirm. The dialog names the app and
database and shows the exact statement that will run. The request is reduced
to the fields that database reads, so nothing else can ride along, and a
statement too long to show in full cannot be confirmed. Only that dialog can
approve it; the extension's page cannot. [enforced: panel] Each query is
recorded in the activity log by kind, mode and a hash, never by its text.

> [!NOTE]
> Read-only mode binds the extension and the page, not the person querying:
> anyone allowed to use the **Data** tab can also reveal the database
> password. A PostgreSQL function the bot itself defined as
> `SECURITY DEFINER` runs with its owner's rights when a read calls it.

### SQLite files

The tab lists up to 50 SQLite databases in the app's files, a container app's
volumes included: files named `.db`, `.sqlite`, `.sqlite3`, `.db3`, `.s3db` or
`.sl3` whose header says SQLite. It does not follow symbolic links and skips
dependency folders such as `node_modules`, `.git` and `.venv`.

Each query runs in a disposable `python:3.11-alpine` container (Python's
`sqlite3` module, pulled on first use) with no network, a read-only root, no
capabilities, 128 MiB of memory, 64 processes and a 10-second query limit. It
runs as the user that owns the file, the user the app writes it as, never as
root. The query text reaches it only through its environment.

| Boundary | Enforced by |
| --- | --- |
| **Run** cannot change the file | The app's files are bind-mounted read-only [enforced: docker], and the file is opened read-only with `PRAGMA query_only` (SQLite) |
| A query reaches only the chosen file | `ATTACH` and `DETACH` are refused by an authorizer (SQLite); only the app's own files are mounted [enforced: docker] |
| **Run as change…** | The files are mounted writable only after you confirm the exact file and statement in the panel dialog; all statements run in one transaction, rolled back on any error [enforced: panel] |

Reads of a WAL database see changes still in its `-wal` file while the app
runs. If the app is stopped and the `-wal` and `-shm` files are gone, the file
is opened as immutable, since there is no writer to coordinate with. A change
waits up to five seconds for the app's own write lock.

Not available yet: apps on remote nodes, SQLite included, because the query
has to run on the node and needs a new agent route; and Redis or MongoDB
schemas beyond the first page of keys.

## Security model

| Boundary | How it is held |
| --- | --- |
| Bundle integrity and publisher | Ed25519 signature over every file's SHA-256, checked on upload and again before the code runs [enforced: panel] |
| Extension code | WebAssembly sandbox (wazero): no files, environment variables, arguments or sockets. At most 128 MiB per running copy, 4 copies of one extension and 16 across all extensions; each call has a wall-clock deadline (at most 30 s), at most 200 host calls and 1 MiB of output [enforced: panel] |
| What the code may do | Every host call is checked against the granted capabilities, the person a route runs for (their role and bot access, through the same checks as the API), the per-bot switch and per-person consent [enforced: panel] |
| HTTPS | Exact host allowlist from the manifest, HTTPS on port 443 only, no credentials in addresses. Every redirect is held to the same rules and loses the `Authorization` header. Private, local and metadata addresses are refused when connecting. 120 requests a minute, 4 MiB responses [enforced: panel] |
| Acting for others | `identity.discord` works only in a verified public request, a job or an event, never in a request a signed-in person made, which always acts as that person [enforced: panel] |
| Secrets | Secret settings are sealed with the panel keyring, write-only, and attached only to requests to the manifest's hosts; bot tokens are attached by the panel [enforced: panel] |
| Custom pages | Served with `Content-Security-Policy: sandbox allow-scripts`, an opaque origin, only the page's own hashed inline script and `connect-src 'none'`: no cookies, CSRF token, network requests or access to the panel's pages. They talk to the panel by `postMessage`, and the panel calls only that extension's own routes as the signed-in person. The bridge answers the first document only: if the frame loads anything else, the panel removes it and ignores it from then on (browser) |
| Overview cards | Declarative widget JSON rendered as text by the panel; images only from `cdn.discordapp.com` (browser) |
| Public endpoints | Signature (Discord Ed25519 or HMAC-SHA256) checked before code runs. Each signed request is accepted once: Discord's within its five-minute window; an identical HMAC delivery is refused for a day, so senders include a timestamp or id in the body. 120 requests a minute per address, budgeted apart from the extension's pages; 64 KiB bodies unless the manifest allows up to 256 KiB [enforced: panel] |

There is no CPU metering inside a call: the wall-clock deadline is the bound.
[not enforced] Browsers cannot stop a frame from navigating itself, so a
custom page could still put what it shows into an address it opens. Review a
page's code as you would any code with access to that data.

## Memory and engine

Extension code is compiled when first used. With wazero's compiler a typical
Go extension costs about 35 MB while loaded. Under the systemd unit, whose
`MemoryDenyWriteExecute` forbids executable memory, the panel falls back to
the interpreter automatically: about 115–120 MB per loaded extension and
slower, but still well within Discord's deadline. The container image has no
such restriction and uses the compiler. **Administration → Extensions** says
which engine runs.

Extensions unused for ten minutes are unloaded and their memory returned to
the system. Extensions with public endpoints (ChatOps) are compiled at start
and stay loaded so a first Discord command is answered in time. Measurements
are in [Diagnostics and footprint](12-diagnostics.md#extensions).

## Write an extension

An extension is a Go program built for WebAssembly with the SDK in
`extensions/sdk`:

```go
//go:build wasip1

package main

import "github.com/xenycx/rivetpanel/extensions/sdk"

func init() {
	sdk.Handle(func(r *sdk.Request) *sdk.Response {
		var bots []sdk.Bot
		if err := sdk.Call("bots.list", map[string]string{}, &bots); err != nil {
			return sdk.Error(403, err.Error())
		}
		return sdk.JSON(200, map[string]int{"bots": len(bots)})
	})
}

func main() {}
```

Build, pack and sign it:

```sh
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm ./my-ext
cp my-ext/manifest.json build/
go run ./cmd/rivet-ext keygen -out publisher.key   # once; prints the public key to trust
go run ./cmd/rivet-ext pack -dir build -out my-ext.rpx -key publisher.key
go run ./cmd/rivet-ext verify -in my-ext.rpx -pub ed25519:…
```

The three built-in extensions under `extensions/` are complete examples, and
`make extensions` builds them. Their logic also runs on the host: the SDK
provides a fake host for `go test`.

## Manifest reference

`manifest.json` is read strictly; unknown fields are refused.

| Field | Meaning |
| --- | --- |
| `manifest_version` | `1` |
| `id`, `name`, `version`, `publisher`, `description` | Identity. `id` is reverse-DNS (`dev.example.tool`); `version` is semantic |
| `compat` | `{"panel": ">=0.9.0 <2.0.0", "abi": 1}` |
| `runtime` | `{"memory_mb": 1–128, "timeout_ms": 100–30000}` |
| `capabilities` | `[{name, reason, optional?, hosts?}]`; see below |
| `settings` | `[{key, label, type, description?, required?, options?, default?, inject_hosts?}]`; `type` is `string`, `number`, `boolean`, `enum` or `secret` |
| `routes` | `[{id, method, path, auth, perm?, bot_perm?, verify?, max_body?, mutating?}]` |
| `jobs` | `[{id, every, timeout_ms?}]`; every 5 minutes to 7 days |
| `events` | `bot.status`, for bots the extension is switched on for |
| `contributes` | `nav` (`[{label, page, perm?}]`), `bot_tabs` (`[{id, label, page, bot_perm?}]`), `bot_cards`, `user_consent`, `bot_enablement` |
| `ui` | `[{id, file: "ui/<name>.html"}]`; single-file pages |

### Routes

| `auth` | Served at |
| --- | --- |
| `user` | `/api/v1/ext/{id}{path}` |
| `bot` | `/api/v1/bots/{bot}/ext/{id}{path}` |
| `public` | `/api/v1/ext-public/{id}{path}`, `POST` with `verify` |

`bot_perm` on a bot route is `view`, `console`, `power`, `files` or
`manage_env`. `perm` on a `user` route or a nav item is one of:

| Value | Who passes |
| --- | --- |
| absent | Any signed-in account |
| `"extension"` | Accounts whose role holds this extension's `ext:<id>`; built-in administrators always |
| `"ext:<own id>"` | The same, spelled out |
| A panel permission, such as `"extensions.manage"` | Accounts holding that permission |

Another extension's `ext:` permission is refused when the manifest is read.
The check runs on the server before any extension code: a nav item is listed
only for accounts that pass it, a route refuses the others with `403`, and a
page is served only to an account that passes the `perm` of at least one nav
item opening it. [enforced: panel] Pages carry no permission of their own: a
page that a bot tab opens, or that no nav item opens, is served to any
signed-in account (bot tabs are checked per bot by their routes), so keep data
behind routes with the right `perm` or `bot_perm`.

### Capabilities and host calls

| Capability | Host calls |
| --- | --- |
| (always) | `log` |
| `kv` | `kv.get`, `kv.set`, `kv.delete` (64 KiB per value, 8 MiB per extension, optional expiry) |
| `settings` | `settings.get` (non-secret values; a secret shows as `<key>_set`) |
| `http.fetch` | `http.fetch` to the listed hosts; `auth_setting` attaches a secret setting |
| `bots.read` | `bots.list`, `bots.get` |
| `bot.logs` | `bot.logs` (up to 200 lines) |
| `bot.power` | `bot.power` (`start`, `stop`, `restart`) |
| `bot.deploy` | `bot.deploy` |
| `notify` | `notify` (a bot's owner, or the person using it; 30 a minute) |
| `bot.discord_api` | `bot.discord_api` (`users/@me`, `applications/@me`, `oauth2/applications/@me`, `users/@me/guilds`) |
| `bot.identity` | `bot.identity` (the bot's Discord name and avatar in the panel) |
| `identity.discord` | `identity.discord` (a consenting panel account for a Discord user; returns a handle valid for that request) |
| `addons.query` | `addons.list`, `addons.query` |

Bot calls act as the person a route runs for or, with `as`, as a person
resolved by `identity.discord`, within the scope they allowed. Jobs, events
and public routes have no person; they see only bots the extension was
switched on for.

### Custom pages

A page is one HTML file with inline script and style. It cannot make network
requests, and talks to the panel by `postMessage`:

1. On load, send `{rivet: 1, type: "ready"}` to `parent`.
2. The panel answers `{type: "init", nonce, theme, context}`. Keep the nonce;
   `theme` maps CSS variables (`--paper`, `--ink`, `--action`, …).
3. Send `{rivet: 1, nonce, id, type: "rpc", method, path, body}` to call the
   extension's own routes (on the current bot inside a bot tab). The answer is
   `{type: "reply", id, ok, body}`.
4. Other messages: `confirm-write` (`token` from `addons.query` in write
   mode), `toast` (`text`, `tone`), `navigate` (`path`), `resize` (`height`)
   and `expand` (`on`: fill the panel's window, or return).

The panel answers `expand`, and the person leaving with its own
**Exit full screen** button or Esc, with `{type: "expanded", on}`. While
expanded, the panel always shows its own bar above the page naming the
extension, so a page cannot pass itself off as the panel. It is not the
browser's full screen.
