# Extensions (preview)

Extensions add features to the panel without changing it: pages in the
sidebar, tabs and overview cards on bot pages, scheduled jobs, reactions to bot
events and HTTPS endpoints for other services. Each extension is a signed
WebAssembly bundle that runs sandboxed inside the panel process and can do only
what an administrator granted it.

Three extensions ship with the panel:

| Extension | What it does |
| --- | --- |
| **Discord bot insights** | A Discord card on each bot's overview: the bot user, application, approximate server count, invite link and token state. Keeps the bot's name and avatar current and notifies the owner when Discord resets the bot's token. |
| **Discord ChatOps** | Slash commands in your Discord server: `/status`, `/logs`, `/start`, `/stop`, `/restart`, `/deploy`. Each person acts with their own panel permissions after linking Discord and allowing ChatOps. |
| **Database browser** | A **Data** tab on bots with database add-ons: browse PostgreSQL and MariaDB tables, Redis keys and MongoDB collections, and run queries. Queries are read-only; a change runs only after you confirm it in a panel dialog. |

## Turning extensions on

Extensions are a preview module, off by default. Turn it on under
**Administration → Modules** (or `RIVET_MODULES=extensions=preview`) and
restart. **Administration → Extensions** then lists the built-in extensions
and any uploaded ones.

Installing is a review step. The page lists every capability the extension
asks for, in plain language, with the hosts it may contact. Required
capabilities must all be granted; optional ones can be left off. **Install and
turn on** stores the grant and enables the extension. A later version that asks
for more capabilities is not used until an administrator reviews it again.

Who may do what:

| Action | Who |
| --- | --- |
| Install, upgrade, switch version, uninstall, grant capabilities, trust or revoke publisher keys | Built-in administrators |
| Turn installed extensions on and off, change settings, run jobs, view status | The `extensions.manage` permission |
| Turn an extension on for one bot | People who may manage that bot's environment variables |
| Allow an extension to act for them (ChatOps) | Each person, under **Settings → Extensions** |

API clients cannot reach any of these routes; they are session-only.

### Uploading extensions

An uploaded bundle (`.rpx`, at most 16 MiB) must be signed with an Ed25519 key
that an administrator trusts under **Administration → Extensions → Trusted
publisher keys**. Revoking a key stops every extension running a version it
signed. Unsigned bundles are accepted only when the panel runs with
`RIVET_ENV=development`.

Built-in extensions need no key: they ship inside the panel binary, and the
binary is what you already trust. Their bundles are stored in the database
with every upload, so `rivetpanel backup` covers installed extensions and you
can switch back to any of the three newest versions without uploading again.

### Recovery

If an extension misbehaves, start the panel with
`RIVET_EXTENSIONS_SAFE_MODE=1`: no extension code runs and no extension appears
in the interface, while everything stays installed. An extension that fails
five times in a row stops by itself; its managers get a notification and
**Try again** restarts it.

## Discord bot insights

1. Install it under **Administration → Extensions**.
2. On a bot's **Manage** tab, the **Discord** card asks to be turned on for
   that bot. Someone who may manage the bot's variables turns it on.

The panel finds the bot token in the bot's variables (`DISCORD_TOKEN`,
`DISCORD_BOT_TOKEN`, `BOT_TOKEN` and similar, or any variable that looks like a
token) and calls Discord's read-only API with it. **The extension never sees
the token**: the panel attaches it, and the extension only learns whether a
token was present, what Discord answered, and a fingerprint that changes when
the token changes.

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
   bots, read output, start/stop/restart, deploy.

How it is protected:

- The panel checks every request's Ed25519 signature and timestamp (at most
  five minutes old) **before** any extension code runs; forged requests are
  answered 401 and never reach the extension.
- A command runs as the panel account linked to the Discord user who ran it,
  re-read on every request, so its current role, bot access and disabled state
  apply. Consent is per person and per scope, and never adds permissions.
- `/start`, `/stop`, `/restart` and `/deploy` reply with a confirmation button
  that only the same Discord user can press, within one minute, once.
- Replies are visible only to the person who ran the command.
- The client secret is used only to register commands; the extension cannot
  read it, and the panel sends it only to `discord.com`.

## Database browser

Install it, then open the **Data** tab on a bot that has a database add-on
(the tab needs permission to manage the bot's environment, the same as
revealing the database password). The bot must be running, because its add-ons
run while it runs.

| Add-on | Browse | Query |
| --- | --- | --- |
| PostgreSQL | Tables | SQL |
| MariaDB | Tables | SQL |
| Redis | Keys (first scan page) | One command per line, quotes allowed |
| MongoDB | Databases and collections | find, count, aggregate (JSON filter or pipeline) |

Each query runs in a **disposable client container** started from the add-on's
own image on the bot's private network: no workspace, a read-only root, no
capabilities, 128 MiB of memory, 64 processes, 20 seconds. The password reaches
it only through its environment, and the query text never through a shell
command line. Results are limited to 500 rows and 1 MiB.

**Run** uses a read-only session: PostgreSQL `default_transaction_read_only`,
MariaDB `SET SESSION TRANSACTION READ ONLY`, an allowlist of Redis read
commands, and only find, count and aggregate (without `$out` or `$merge`) for
MongoDB. **Run as change…** asks the panel to confirm: the panel shows the exact
statement in its own dialog, and only that dialog can approve it (the
extension's page cannot). Each query is recorded in the activity log by kind,
mode and a hash, never by its text.

> Read-only mode protects against accidents, not against the person querying:
> anyone allowed to use the Data tab can also reveal the database password.

Not available yet: apps on remote nodes (the query has to run on the node; this
needs a new agent route), and Redis or MongoDB schemas beyond the first page of
keys.

## Security model

| Boundary | Enforced by |
| --- | --- |
| Bundle integrity and publisher | Ed25519 signature over every file's SHA-256, checked on upload and again before the code runs (application) |
| Extension code | WebAssembly sandbox (wazero): no files, environment variables, arguments or sockets. Memory is capped per extension (at most 128 MiB), each call has a wall-clock deadline (at most 30 s), at most 200 host calls and 1 MiB of output per call, and 4 calls at once (16 for all extensions). Runtime |
| What the code may do | Every host call is checked against the capabilities granted, the person a route runs for (their role and bot access, through the same checks as the API), the per-bot switch and per-person consent (application) |
| HTTPS | Exact host allowlist from the manifest, HTTPS only, redirects re-checked, private, local and metadata addresses refused when connecting, 120 requests a minute, 4 MiB responses (application) |
| Secrets | Secret settings are sealed with the panel keyring, write-only, and attached only to requests to the hosts the manifest lists; bot tokens are attached by the panel (application) |
| Custom pages | Served with `Content-Security-Policy: sandbox allow-scripts`, an opaque origin, only the page's own hashed inline script, `connect-src 'none'`: no cookies, no CSRF token, no network, no access to the panel's pages. They talk to the panel by `postMessage`, and the panel only calls that extension's own routes as the signed-in person (browser) |
| Overview cards | Declarative widget JSON rendered as text by the panel; images only from `cdn.discordapp.com` (browser) |
| Public endpoints | Signature (Discord Ed25519 or HMAC-SHA256) checked before code runs, 120 requests a minute per address, 64 KiB bodies (application) |

There is no CPU metering inside a call: the wall-clock deadline is the bound.

## Memory and engine

Extension code is compiled when first used. With wazero's compiler a typical
Go extension costs about 35 MB while loaded. Under the systemd unit, whose
`MemoryDenyWriteExecute` forbids executable memory, the panel falls back to
the interpreter automatically. That is about ten times larger (around 120 MB
per loaded extension) and slower, but still well within Discord's deadline.
**Administration → Extensions** says which engine runs.

Extensions unused for ten minutes are unloaded and the memory is returned to
the system. Extensions with public endpoints (ChatOps) are compiled at start-up
and stay loaded, so a first Discord command is answered in time.

## Writing an extension

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
`make extensions` builds them. Their logic runs on the host too: the SDK
provides a fake host for `go test`.

### Manifest

`manifest.json` is read strictly; unknown fields are refused.

| Field | Meaning |
| --- | --- |
| `manifest_version` | `1` |
| `id`, `name`, `version`, `publisher`, `description` | Identity; `id` is reverse-DNS (`dev.example.tool`); `version` is semantic |
| `compat` | `{"panel": ">=0.9.0 <2.0.0", "abi": 1}` |
| `runtime` | `{"memory_mb": 1–128, "timeout_ms": 100–30000}` |
| `capabilities` | `[{name, reason, optional?, hosts?}]`; see below |
| `settings` | `[{key, label, type, description?, required?, options?, default?, inject_hosts?}]`; type is `string`, `number`, `boolean`, `enum` or `secret` |
| `routes` | `[{id, method, path, auth, perm?, bot_perm?, verify?, max_body?, mutating?}]`; `auth` is `user` (`/api/v1/ext/{id}{path}`), `bot` (`/api/v1/bots/{bot}/ext/{id}{path}`) or `public` (`/api/v1/ext-public/{id}{path}`, POST with `verify`) |
| `jobs` | `[{id, every, timeout_ms?}]`; every 5 minutes to 7 days |
| `events` | `bot.status` (for bots the extension is switched on for) |
| `contributes` | `nav`, `bot_tabs`, `bot_cards`, `user_consent`, `bot_enablement` |
| `ui` | `[{id, file: "ui/<name>.html"}]`; single-file pages |

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

Bot calls act as the person a route runs for, or (with `as`) as a person
resolved by `identity.discord`, with the scope they allowed. Jobs, events and
public routes have no person; they see only bots the extension was switched on
for.

### Custom pages

A page is one HTML file with inline script and style. It has no network. It
talks to the panel by `postMessage` with this protocol:

1. On load, send `{rivet: 1, type: "ready"}` to `parent`.
2. The panel answers `{type: "init", nonce, theme, context}`. Keep the nonce;
   `theme` maps CSS variables (`--paper`, `--ink`, `--action`, …).
3. Send `{rivet: 1, nonce, id, type: "rpc", method, path, body}` to call the
   extension's own routes (on the current bot inside a bot tab). The answer is
   `{type: "reply", id, ok, body}`.
4. Other messages: `confirm-write` (`token` from `addons.query` in write mode),
   `toast` (`text`, `tone`), `navigate` (`path`), `resize` (`height`).
