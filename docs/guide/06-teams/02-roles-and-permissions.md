---
title: Roles and permissions
lede: Decide what each account may do with a role, share single apps with other people, and delegate parts of the administration safely.
icon: key
aliases: [roles]
glance:
  - 2 built-in roles, plus custom roles
  - 6 per-app sharing permissions
  - Every check runs on the server
related: [teams/workspaces, teams/sign-in, teams/limits]
---

## How access is decided

Three layers decide what someone may do with an app:

| Layer | Set where | Grants |
|---|---|---|
| Account role | **Administration → Users** | What the account may do at all. It is the ceiling for everything below |
| Workspace role | The workspace's settings | Access to every app and site in a workspace (see [Workspaces](01-workspaces.md)) |
| Per-app sharing | An app's **Access** tab | Access to one app, on top of the workspace |

The panel checks the role on the server for every request. A permission
missing from a role is refused with `403 Forbidden`, for owners too, even when
a client calls the API directly. [enforced: panel] The interface hides what a
role cannot do, but it is never the enforcement point.

Roles do not change container isolation, network policy or resource limits.

## Built-in roles

Every account holds one role. Two are built in and cannot be deleted:

| Role | Holds | Notes |
|---|---|---|
| Administrator (`admin`) | Every permission, including ones added in later versions | Fixed. The only role that opens **Administration → Environment** and **Modules** and that can make, change or remove administrators. Never restricted by the unverified-email policy |
| User (`user`) | By default every resource permission and no administration permission | Built-in administrators can change its list |

The User role's list is stored (migration `0057` wrote it, with
`workspaces.rename`). A permission added in a later version reaches it only
when that version's migration adds it or an administrator switches it on.

### Edit the User role

*Built-in administrators only.*

1. Open **Administration → Roles → User**.
2. Switch resource permissions off or on. **Restore defaults** puts back every
   resource permission.

The list applies to every account holding the role on its next request, and
to everyone invited with it later. Its name and description are fixed. A
delegated role manager (`roles.manage`) is refused, as is anyone trying to
change the Administrator role. [enforced: panel]

## Custom roles

*Needs `roles.manage`.* **Administration → Roles** creates, edits and deletes
custom roles from named permissions such as `bots.create`, `bots.files`,
`nodes.manage` or `users.manage` (the [catalog](#permission-catalog) is below).

Assign a role under **Administration → Users → ⋯ → Change role**
(`users.manage`), when adding a user, or in an
[invitation](03-sign-in.md#invitations).

- A change to a role applies on the account's next request. Sessions are not
  signed out.
- A role that any account holds cannot be deleted, and neither can a role a
  single sign-on provider gives new accounts.
- Custom roles made before `workspaces.rename` existed gained it in migration
  `0057`. Custom roles made before `tickets.create` existed do not include it.
- Creating, changing, deleting and assigning roles are in the activity log
  (`admin.role_create`, `admin.role_update`, `admin.role_delete`,
  `admin.user_role`) with the role name and permission list. Refused attempts
  are recorded as `denied`.

### Extension permissions

While the extensions module is on, a role can hold **Use <extension name>**
(`ext:<extension id>`) for each installed extension, listed under
**Extensions**. It shows and opens that extension's pages and routes marked
`"perm": "extension"`; setup pages stay with the Extensions administration
permission. See [Extensions](../07-operate/09-extensions.md).

These are resource permissions: they do not open the administration area or
change who outranks whom, and they are not in the User role's defaults.
Uninstalling the extension removes its permission from every role.

## Delegated administration

A custom role can hold some administration permissions without making the
account an administrator. A delegate can never hand out more than it holds.
[enforced: panel]

- Only administrators make administrators, or change an administrator's role,
  email address or verified flag, or disable one.
- An account manager cannot change the role, address or verified flag of, or
  enable or disable, an account whose role holds an administration permission
  the manager lacks. Otherwise an address change followed by a password reset
  could take that account over. Accounts with the same or fewer administration
  permissions, and plain users, stay manageable.
- A role manager can only create or edit roles whose permissions it holds,
  cannot edit its own role, and cannot edit or delete a role that holds
  permissions it lacks.
- An account manager can only assign (or invite with) a role whose permissions
  it holds. The User role counts as the permissions currently stored for it, so
  a delegated manager can invite as User only while it holds all of them. It
  cannot change its own role.
- The last active administrator cannot be given another role, removed or
  disabled.

The same rule bounds support staff, usage analytics and API clients: they only
see or act on accounts they outrank.

## Permission catalog

### Resource permissions

What an account may do with what it owns, with apps shared with it and in
workspaces it belongs to:

| Permission | Allows |
|---|---|
| `bots.create` | Create bots and game servers |
| `bots.delete` | Delete bots and servers |
| `bots.console` | Read console output and logs |
| `bots.power` | Start, stop, restart, kill; console input and one-shot commands; batch actions |
| `bots.files` | File manager, SFTP, package manager and AI file changes |
| `bots.env` | Read and change environment variables |
| `bots.configure` | A bot's **Configuration** tab (see [below](#configuration-without-full-access)) |
| `bots.backups` | Create, restore, verify, download and delete backups |
| `bots.deploy` | Link GitHub repositories, deploy, publish and push |
| `bots.share` | Share bots, bot invitations and ownership transfer |
| `sites.create` | Create static sites |
| `workspaces.create` | Create team workspaces |
| `workspaces.rename` | Rename workspaces where the account is the workspace owner or admin, the personal workspace included |
| `api_keys.manage` | Create SFTP keys, automation tokens and API clients |
| `ai.use` | Use the AI assistant |
| `tickets.create` | Open support tickets and reply to your own |

### Administration permissions

| Permission | Opens |
|---|---|
| `users.view` | The account list and account details |
| `users.manage` | Create, enable and disable accounts, assign roles, change addresses, mark addresses verified, create, list and revoke account invitations |
| `roles.manage` | Create, change and delete custom roles |
| `nodes.manage` | Locations, node enrollment, certificates and settings; placing servers on nodes, the default node, a workspace's node pin and a node on an invitation |
| `blueprints.manage` | Game server types (import, enable, disable, delete) |
| `allocations.manage` | Port allocations |
| `settings.manage` | Panel address, sign-in providers, registration, email, unverified-account policy |
| `mail.announce` | Email announcements |
| `ai.manage` | AI provider profiles and web search |
| `sites.manage` | Every site, and sites domains |
| `workspaces.view` | Every workspace, read only |
| `workspaces.manage` | List, rename and delete any workspace, and manage its members, from **Administration → Workspaces**. Limits stay with built-in administrators and node pins with `nodes.manage` |
| `system.view` | Host monitoring, panel logs, diagnostics |
| `tickets.view_all` | Read every support ticket, internal notes included |
| `tickets.manage` | Answer every support ticket, internal notes, status, priority, assignment |
| `kb.manage` | Write, publish and delete help articles and categories; turn the public help center on or off; preview drafts |
| `status.manage` | The public status page, its components, incidents and maintenance (nodes only with `nodes.manage`, apps only those the account can open) |
| `analytics.view` | **Administration → Analytics** (see [Usage analytics](../05-observe/05-usage.md)) |

### Per-extension permissions

| Permission | Allows |
|---|---|
| `ext:<extension id>` | See and use the extension's navigation items, pages and routes marked `"perm": "extension"` |

`GET /api/v1/admin/permissions` returns the catalog with labels, followed by
one `ext:<id>` entry per installed extension (group `extensions`) while the
extensions module is on. `GET /api/v1/auth/me` returns the signed-in account's
effective `permissions`, extension permissions included; administrators hold
every one.

## Share one app

The owner of an app, and administrators, share it from its **Access** tab, by
email or with an invitation link. Each share carries a set of permissions:

| Permission | Allows |
|---|---|
| View console | Live output, analytics, resource gauges, health |
| Start / stop | Start, stop, restart, kill, console input |
| Edit files | File manager, SFTP, packages, deployments, backups (create and download) |
| Manage env vars | List, reveal, set and delete environment variables |
| Configuration | Bots only: the **Configuration** tab (runtime, start command, entrypoint, build command, auto-restart), without full admin |
| Full admin | Everything above, plus settings, the telemetry key, the GitHub link, alert rules, and restoring and deleting backups |

Only the owner or an administrator can share an app, delete it, transfer it or
publish host ports. [enforced: panel]

- Someone with no grant gets `404`, the same answer as for an app that does
  not exist. Someone whose grant lacks a permission gets `403`.
- Console input needs *Start / stop* and is re-checked on every line.
- Console and SFTP sessions lose access within about 5 seconds of a revoked
  grant, a disabled account or a deleted key. [enforced: panel]
- Invitation links work once, expire after 1–14 days, carry the permissions
  chosen when they were made, and only work for someone signed in to this
  panel. The token is in the URL fragment, which browsers never send to the
  server. An app can have at most 20 open invitations, and they can be revoked.
- Transferring ownership removes the GitHub link (it uses the old owner's
  GitHub access) and moves the app into the new owner's personal workspace.

### The role is the ceiling

The four basic permissions (`bots.console`, `bots.power`, `bots.files`,
`bots.env`) mask every path to an app: ownership, workspace roles and shares.
A full-access share cannot give back what the role removes. SFTP uses the same
mask and re-reads the account during a session; the console checks the role
when it connects and re-checks app access while open. [enforced: panel]

Full access (owner, workspace owner or admin, or a full-access share) survives
a role only when the role holds all four.

> [!WARNING]
> Known gap: for owners and workspace owners or admins, the API does not apply
> the role to full-access-only actions (settings, network, startup). The
> interface hides those tabs, but a direct API call succeeds. Shared users are
> fully masked. [not enforced]

### Configuration without full access

A role that leaves out one of the four basic permissions can still hold
`bots.configure`. It allows a bot's **Configuration** tab (runtime, start
command, entrypoint, build command and auto-restart) on bots the account owns,
has full access to, or was shared with **Configuration** ticked. Roles that
hold all four already have it.

The API then accepts `PATCH /bots/:id` with only runtime, `argv`, entrypoint,
build command and restart fields, and refuses anything else (name, limits,
network, backups) with `403`. [enforced: panel] Game servers, containers and
site apps still need full access.

A share or invitation can grant **Configuration** (bit 32) on its own. It
counts only when the role holds `bots.configure` or all four basic
permissions; otherwise it is masked off like any bit the role lacks.

## Email verification

New accounts start with an unverified address. Confirm it from **Settings →
Profile → Email address**, which sends a one-use link (when email is set up
and the panel address is known). Accounts registered from an invitation get
one automatically.

| Rule | Detail |
|---|---|
| Counts as verified | Accounts from before migration `0044`; GitHub and Discord sign-ups (those providers only hand over verified addresses); using a password-reset link |
| The link | A random token in the URL fragment, so it never reaches server logs. Only its SHA-256 is stored. Works once, expires after 24 hours, and a newer link replaces the older one |
| Resending | One link a minute and five requests an hour per account [enforced: panel] |
| Confirming | Limited per IP address [enforced: panel] |

### Change your address

Changing your own address needs your current password (or, without a password,
a sign-in within the last 10 minutes). The address changes only when the link
sent to the new address is used. The old address is told about the request and
about the change.

### Restrict unverified accounts

*Needs `settings.manage`.* **Administration → Panel settings → Unverified email
addresses** chooses which permissions to withhold from accounts whose address
is not verified. It is off by default. When turned on it suggests creating
bots, sites and workspaces, API keys and the AI assistant; any permission can
be chosen.

Withheld permissions are refused (`403`, "verify your email address") until
the address is verified. [enforced: panel] Affected accounts see a banner with
a link to verify. Administrators are never restricted.

### Manage addresses for others

*Needs `users.manage`.* On the **Users** page, account managers change an
account's address (it becomes unverified again, and the old address is told)
and mark an address verified or not verified, for panels without email.

## Delete an account

*Built-in administrators only.*

1. Open **Administration → Users →** the account's menu **→ Delete account…**
   (`DELETE /api/v1/users/{id}`).
2. Type the account's address to confirm.

The account is removed permanently with everything it owns: its bots and game
servers (containers, files, backups and add-on data), its sites, the
workspaces it owns and their memberships, sessions, keys, tokens,
notifications, support tickets and allowance. Repository links on other
accounts' bots made with its GitHub token are removed; the webhook on GitHub
stays but can no longer deploy.

- It is refused for your own account, for administrators (change the role to
  user first), and while a team workspace the account owns holds other
  accounts' apps or sites (move those out first).
- The account is disabled first, so it is signed out at once. If a step fails,
  for example a server on an offline remote node, it stays disabled and
  **Delete** can be repeated.
- Operations, backups and releases it started keep their records without the
  name, and the activity log keeps its events (`admin.user_delete`).
- The address can register again afterwards.
