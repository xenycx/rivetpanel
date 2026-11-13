---
title: Workspaces
lede: Group apps and sites for yourself or a team, give people a role in them, and narrow the panel to one workspace at a time.
icon: users
aliases: [workspaces]
glance:
  - 1 personal workspace per account
  - Up to 20 team workspaces owned per account by default
  - 4 workspace roles
related: [teams/roles-and-permissions, teams/limits, apps/bots]
---

## What a workspace is

A workspace groups apps (bots, game servers, containers) and
[sites](../04-websites/01-bot-sites.md) for a person or a team. Membership
gives access to everything in the workspace, by role.

Every account has exactly one **Personal** workspace, created with the account.
Anything created without choosing a workspace goes there. Team workspaces hold
the apps and sites a group of people share.

## Create a team workspace

1. Open **Settings → Workspaces** and create a workspace. Your role needs the
   `workspaces.create` permission.
2. Open the workspace and add people by the email address they sign in with.
   They must already have an active account.
3. Give each person a role: admin, developer or viewer.

One account can own at most 20 team workspaces unless **Administration → Panel
settings → Default allowance → Team workspaces per account** sets another
number; 0 stops people from creating team workspaces. [enforced: panel]
Built-in administrators are exempt. See [How limits work](04-limits.md) for why
this number matters.

## Roles in a workspace

| Role | Apps | Sites | The workspace |
|---|---|---|---|
| Owner | Everything, as the app's owner | Everything | Rename\*, logo\*, members, delete (when empty) |
| Admin | Everything, as the app's owner: settings, delete, sharing, transfer | Everything, including delete and move | Rename\*, logo\*, add and remove members (never the owner) |
| Developer | View the console, start, stop and kill, files, packages, deployments, backups, environment variables; create apps | Create, publish, deploy, roll back, domains, settings | None |
| Viewer | Console output, status and history | View | None |

\* Renaming and changing the logo also need the account's role to hold
`workspaces.rename`. The built-in User role holds it by default, and so did
every custom role when the permission was introduced.

A developer cannot change resources or startup, delete an app, share or
transfer it, or publish host ports. Those need *Full admin* on the app.

Workspace roles are limited by the account's own role: a role without console,
power, files or environment permissions removes them in every workspace too.
See [Roles and permissions](02-roles-and-permissions.md).

Every app and site request is checked against workspace membership and per-app
grants. SFTP folders, automation tokens for "all your bots" and the activity
feed follow the same rules. [enforced: panel]

### Sharing one app on top

Per-app sharing (an app's **Access** tab and invitation links) still works and
only ever adds access on top of membership. Someone who leaves a workspace
keeps any direct grants they were given.

## Switch between workspaces

The workspace switcher in the sidebar narrows the panel to one workspace, or
shows **All workspaces**.

| Page | With a workspace chosen |
|---|---|
| Overview | Its list and every number at the top. An administrator sees that workspace's reservations instead of the host's usage |
| Apps | Bots, game servers, containers, stacks and websites of that workspace |
| **New** | Creates in that workspace by default |
| Activity | Always lists every workspace, and says **Showing all workspaces** |

Pages that follow the choice name the workspace beside their title, with
**Show all workspaces**.

- Each row shows the workspace's logo and your role there; a check marks the
  current choice. **All workspaces** shows the first two logos overlapping.
- Typing a letter in the open menu jumps to the next workspace starting with
  it.
- With only one workspace there is nothing to choose: the switcher shows that
  workspace.
- If the list cannot be loaded, the switcher says so and offers **Try again**.
- In **Go to** (Ctrl+K), **Switch to …** changes the workspace, and opens the
  Overview from pages that do not follow it. A second entry per workspace
  opens its settings.

The choice is remembered per browser. It only narrows what these pages show;
it never changes access.

## Name and logo

Every workspace has a name that can be changed, the personal one included. The
stored name shows everywhere (switcher, lists, create forms), and the personal
workspace carries a **Personal** badge.

To add a logo, click the picture beside the workspace's name on its
**Settings → Workspaces** page (or on **Administration → Workspaces**), then
upload, replace or remove one.

- Accepted: PNG, JPEG, WebP or GIF, cropped to a square and stored as a PNG or
  JPEG of at most 256 KiB, 16–512 px.
- Whoever may rename the workspace may change its logo. Every member sees it.
- Without a logo the panel draws a generated picture. **New picture** draws a
  different one for everyone.

## Move and transfer apps

- **Move to another workspace:** an app's **Settings → Workspace**, or a site's
  settings. It needs owner-level control of the app, or the admin role for a
  site, plus at least the developer role in the target workspace. Access
  changes immediately.
- **Transfer ownership** moves an app into the new owner's personal workspace,
  so the previous owner loses access through their workspace. Choosing **keep
  access** adds a direct grant instead.
- **Allowance:** a workspace's allowance counts every member's apps and sites
  together. Moving an app or site into a workspace with no room for it is
  refused. Members see the usage and allowance on the workspace's settings
  page. See [How limits work](04-limits.md).
- **Node pin:** a workspace can be pinned to a node so its new apps are created
  there. Existing apps stay where they are. See [How limits
  work](04-limits.md#node-placement).

## Delete a workspace

The owner of a team workspace can delete it once it has no apps (including
apps still being deleted) and no sites. [enforced: panel] Personal workspaces
cannot be deleted; they go with their account.

## Administration

These sections are for administrators and accounts with the named permissions.

### Oversee every workspace

**Administration → Workspaces** lists every workspace with its owner, members,
running and total apps, memory assigned to running apps, sites and last
activity. Opening one shows its members, every app with its owner and state,
its sites (with suspend and restore) and the latest 40 operations
(deployments, builds, backups, restores and GitHub pushes).

**Administration → Users → an account** shows the workspaces it belongs to, the
apps it owns and recent operations on them. Administrators can open any app or
site and manage any workspace's members.

### Rename or delete any workspace

Accounts with `workspaces.manage` (built-in administrators included) rename any
workspace, personal ones too, and manage its members.

They can also delete a team workspace with everything in it:

1. Open **Administration → Workspaces →** the workspace **→ Delete workspace**.
2. Type the workspace's name to confirm.
3. Its sites, its apps (containers, files, backups and add-ons) and its
   memberships are deleted, then the workspace itself.

> [!WARNING]
> This deletion cannot be undone. It is refused for personal workspaces (delete
> the account instead) and while any app of the workspace runs on a remote node
> that is offline; the message names the node and nothing is changed.

If removals are still finishing, for example on a node that went away mid-way,
the request fails with a message to try again, and repeating it completes the
deletion. Renames and deletions are in the activity log
(`admin.workspace_rename`, and `admin.workspace_delete` with the numbers of
apps, sites and members).

`workspaces.manage` does not include limits or node pins: a workspace's
allowance stays with built-in administrators and its node pin with
`nodes.manage`. [enforced: panel]

## API

All routes are under `/api/v1` and use the session cookie and CSRF header, like
the rest of the panel API.

| Method and path | Purpose |
|---|---|
| `GET /workspaces` | The caller's workspaces, with their role and usage |
| `POST /workspaces` `{name}` | Create a team workspace |
| `GET /workspaces/{id}` | The workspace and its members (any member) |
| `PATCH /workspaces/{id}` `{name}` | Rename (workspace owner or admin holding `workspaces.rename`) |
| `DELETE /workspaces/{id}` | Delete an empty team workspace (owner) |
| `GET /workspaces/{id}/logo` | The custom logo (any member; `404` when none) |
| `PUT /workspaces/{id}/logo` `{image}` | Set the logo: base64 PNG or JPEG, 16–512 px, at most 256 KiB (same permission as renaming; audited `workspace.logo_set`) |
| `DELETE /workspaces/{id}/logo` | Remove the logo (same permission; audited `workspace.logo_remove`) |
| `POST /workspaces/{id}/logo/shuffle` | Draw a different generated picture; answers `{logo_variant}` (same permission; audited `workspace.logo_shuffle`) |
| `PUT /workspaces/{id}/members` `{email, role}` | Add a member or change a role (admin) |
| `PATCH /workspaces/{id}/members/{user}` `{role}` | Change a role (admin) |
| `DELETE /workspaces/{id}/members/{user}` | Remove a member (admin), or leave (yourself) |
| `POST /bots` `{..., workspace_id}` | Create a bot in a workspace (developer) |
| `PUT /bots/{id}/workspace` `{workspace_id}` | Move a bot |
| `GET /placement[?workspace=ID]` | The node new apps in a workspace go to, and why (any member) |
| `GET /admin/workspaces[?user=ID]` | Every workspace, or one account's (administrators) |
| `GET /admin/workspaces/{id}` | Members, apps with owners, sites, recent operations |
| `PATCH /admin/workspaces/{id}` `{name}` | Rename any workspace (`workspaces.manage`; audited `admin.workspace_rename`) |
| `POST /admin/workspaces/{id}/delete` `{confirm}` | Delete a team workspace and everything in it; `confirm` is its exact name (`workspaces.manage`; audited `admin.workspace_delete`) |
| `PUT /admin/workspaces/{id}/node` `{node_id}` | Pin new apps to a node; `""` clears (`nodes.manage`; audited `admin.workspace_node`) |
| `GET /admin/users/{id}` | An account's workspaces, owned apps and recent operations |

Workspace objects include `logo_url` (the custom logo, cache-busted by the time
it was set; `""` when none) and `logo_variant`. Member objects include the
member's `logo_variant`, and bot objects include `workspace_id`. Migration
`0024_workspaces.sql` created a personal workspace for every existing account
and moved each bot into its owner's.
