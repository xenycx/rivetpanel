# Workspaces

A workspace groups bots (and [hosted sites](sites.md)) for a person or a team.
Every account has exactly one **Personal** workspace, created with the account;
anything created without choosing a workspace goes there. Team workspaces are
created under **Settings → Workspaces** (at most 20 owned per account unless
Panel settings → Default allowance sets another number; built-in
administrators are exempt). Every workspace has a name that can be changed, the personal one
included; the stored name shows everywhere (switcher, lists, create forms),
and the personal workspace carries a **Personal** badge.

A workspace can have a **logo**: click the picture beside its name on its
Settings → Workspaces page (or on Administration → Workspaces) to upload,
replace or remove one (PNG, JPEG, WebP or GIF, cropped to a square and stored
as a PNG or JPEG of at most 256 KiB). Whoever may rename the workspace may
change its logo; every member sees it. Without one the panel draws a generated
picture, and **New picture** draws a different one for everyone.

The sidebar's workspace switcher scopes the Overview (its list and every
number at the top), Apps (bots, game servers, containers, stacks and
websites) and the default target of **New** to one workspace, or shows **All
workspaces**. Each row shows the workspace's logo and your role, a check marks
the current choice, and All workspaces shows the first two logos overlapping,
on the button and in its row. Typing a letter in the open menu jumps to the
next workspace starting with it. With only one workspace there is nothing to
choose: the switcher shows that workspace. The choice is remembered per
browser and only narrows what these pages show; it never changes access.

Pages that follow the choice name the workspace beside their title with
**Show all workspaces**; Activity, which always lists every workspace, says
**Showing all workspaces** while one is chosen. With a workspace chosen, an
administrator's Overview shows that workspace's reservations instead of the
host's usage. In Go to (Ctrl+K), **Switch to …** changes the workspace (and
opens the Overview from pages that do not follow it); a second entry per
workspace opens its settings. If the list cannot be loaded, the switcher says
so and offers **Try again**.

## Roles

Membership grants access to **every** bot and site in the workspace. Roles map
to the per-bot permission bits described in [features.md](features.md#sharing-and-permissions-rbac):

| Role | Bots | Sites | Workspace |
| --- | --- | --- | --- |
| Owner | everything, as the bot's owner | everything | rename\*, members, delete (when empty) |
| Admin | everything, as the bot's owner (settings, delete, sharing, transfer) | everything, including delete and move | rename\*, add/remove members (never the owner) |
| Developer | view console, start/stop/kill, files, packages, deployments, backups, environment variables; create bots | create, publish, deploy, roll back, domains, settings | — |
| Viewer | view console output, status and history | view | — |

\* Renaming also needs the account's role to hold `workspaces.rename`
(`docs/permissions.md`); the built-in User role does by default, and so did
every custom role when the permission was introduced. Accounts with
`workspaces.manage` rename any workspace from Administration.

A developer cannot change resources or startup, delete a bot, share it,
transfer it or publish host ports: those need *Full admin* on the bot.

Per-bot sharing (a bot's **Users** tab and invitation links) still works and
only ever **adds** access on top of workspace membership. Someone who leaves a
workspace keeps direct grants they were given.

Enforcement: application level (every bot and site request is authorized
against workspace membership and per-bot grants). SFTP folders, the automation
API's "all your bots" tokens and the activity feed follow the same rules.

## Moving and transferring

* **Move to another workspace** (bot **Settings → Workspace**, or a site's
  Settings): needs owner-level control of the bot or admin role for the site,
  and at least developer role in the target workspace. Access changes
  immediately.
* **Transfer ownership** of a bot moves it into the new owner's personal
  workspace, so the previous owner does not keep access through their
  workspace (unless "keep access" is chosen, which adds a direct grant).
* The **workspace allowance** belongs to the workspace: every member's apps
  and sites in it count together against the default allowance or the
  workspace's own (see "Workspace allowance" in `features.md`). Moving an app
  or site into a workspace with no room for it is refused. Members see the
  usage and allowance on the workspace's settings page. Only built-in
  administrators are exempt, whoever owns the workspace.
* A workspace can be **pinned to a node** so its new apps are created there
  (see "Node placement" in `features.md`). Existing apps stay where they are.

A member can delete a workspace only when it has no bots (including bots still
being deleted) and no sites. Personal workspaces cannot be deleted; they go
with their account.

## Administrator oversight

**Administration → Workspaces** lists every workspace on the installation with
its owner, members, running/total bots, memory assigned to running bots, sites
and last activity. Opening one shows its members, every bot with its owner and
state, its sites (with suspend/restore) and the latest 40 operations
(deployments, builds, backups, restores and GitHub pushes).

### Renaming and deleting from Administration

Accounts with `workspaces.manage` (built-in administrators included) can
rename any workspace, personal ones too, and manage its members. They can also
**delete a workspace with everything in it**: Administration → Workspaces → the
workspace → Delete workspace asks for its name and then deletes its sites, its
apps (containers, files, backups and add-ons) and its memberships, followed by
the workspace itself. It is refused for personal workspaces (delete the
account instead) and while any app of the workspace runs on a remote node that
is offline (the message names the node; nothing is changed). If removals are
still finishing, for example on a node that went away mid-way, the request
fails with a message to try again, and repeating it completes the deletion.
Renames and deletions are audited (`admin.workspace_rename`,
`admin.workspace_delete` with the numbers of apps, sites and members). Limits
stay with built-in administrators and node pins with `nodes.manage`.
Enforcement: application level.

**Administration → Users → (account)** shows the workspaces an account belongs
to, the bots it owns and recent operations on them. Administrators can open any
bot or site and manage any workspace's members.

## API

All routes are under `/api/v1` and use the session cookie and CSRF header like
the rest of the panel API.

| Method and path | Purpose |
| --- | --- |
| `GET /workspaces` | the caller's workspaces with their role and usage |
| `POST /workspaces` `{name}` | create a team workspace |
| `GET /workspaces/{id}` | workspace and members (any member) |
| `PATCH /workspaces/{id}` `{name}` | rename (workspace owner or admin holding `workspaces.rename`) |
| `DELETE /workspaces/{id}` | delete an empty team workspace (owner) |
| `GET /workspaces/{id}/logo` | the custom logo (any member; 404 when none) |
| `PUT /workspaces/{id}/logo` `{image}` | set the custom logo, base64 PNG or JPEG, 16–512 px, at most 256 KiB (same permission as renaming; audited `workspace.logo_set`) |
| `DELETE /workspaces/{id}/logo` | remove the custom logo (same permission; audited `workspace.logo_remove`) |
| `POST /workspaces/{id}/logo/shuffle` | draw a different generated picture; answers `{logo_variant}` (same permission; audited `workspace.logo_shuffle`) |
| `PUT /workspaces/{id}/members` `{email, role}` | add a member or change a role (admin) |
| `PATCH /workspaces/{id}/members/{user}` `{role}` | change a role (admin) |
| `DELETE /workspaces/{id}/members/{user}` | remove a member (admin), or leave (self) |
| `POST /bots` `{..., workspace_id}` | create a bot in a workspace (developer) |
| `PUT /bots/{id}/workspace` `{workspace_id}` | move a bot |
| `GET /admin/workspaces[?user=ID]` | all workspaces, or one account's (administrators) |
| `GET /admin/workspaces/{id}` | members, bots with owners, sites, recent operations |
| `PATCH /admin/workspaces/{id}` `{name}` | rename any workspace (`workspaces.manage`; audited `admin.workspace_rename`) |
| `POST /admin/workspaces/{id}/delete` `{confirm}` | delete a team workspace and everything in it; `confirm` is its exact name (`workspaces.manage`; audited `admin.workspace_delete`) |
| `PUT /admin/workspaces/{id}/node` `{node_id}` | pin new apps to a node, `""` clears (`nodes.manage`; audited `admin.workspace_node`) |
| `GET /placement[?workspace=ID]` | the node new apps in a workspace go to, and why (any member) |
| `GET /admin/users/{id}` | an account's workspaces, owned bots and recent operations |

Workspace objects include `logo_url` (the custom logo, cache-busted by the
time it was set; `""` when none) and `logo_variant`; member objects include the
member's `logo_variant`. Bot objects include `workspace_id`. Migration `0024_workspaces.sql` creates a
personal workspace for every existing account and moves each bot into its
owner's.
