# Workspaces

A workspace groups bots (and [hosted sites](sites.md)) for a person or a team.
Every account has exactly one **Personal** workspace, created with the account;
anything created without choosing a workspace goes there. Team workspaces are
created under **Settings → Workspaces** (at most 20 owned per account;
administrators are exempt).

The sidebar's workspace switcher scopes the overview, the Sites page and the
default target of **New** to one workspace, or shows **All workspaces**. The
choice is remembered per browser.

## Roles

Membership grants access to **every** bot and site in the workspace. Roles map
to the per-bot permission bits described in [features.md](features.md#sharing-and-permissions-rbac):

| Role | Bots | Sites | Workspace |
| --- | --- | --- | --- |
| Owner | everything, as the bot's owner | everything | rename, members, delete (when empty) |
| Admin | everything, as the bot's owner (settings, delete, sharing, transfer) | everything, including delete and move | rename, add/remove members (never the owner) |
| Developer | view console, start/stop/kill, files, packages, deployments, backups, environment variables; create bots | create, publish, deploy, roll back, domains, settings | — |
| Viewer | view console output, status and history | view | — |

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
* Quotas (`RIVET_MAX_BOTS_PER_USER`, `RIVET_USER_MEMORY_BYTES`) count the
  bot's **owner** (its creator or transfer recipient), not the workspace.

A workspace can be deleted only when it has no bots (including bots still being
deleted) and no sites. Personal workspaces cannot be deleted.

## Administrator oversight

**Administration → Workspaces** lists every workspace on the installation with
its owner, members, running/total bots, memory assigned to running bots, sites
and last activity. Opening one shows its members, every bot with its owner and
state, its sites (with suspend/restore) and the latest 40 operations
(deployments, builds, backups, restores and GitHub pushes).

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
| `PATCH /workspaces/{id}` `{name}` | rename (admin) |
| `DELETE /workspaces/{id}` | delete an empty team workspace (owner) |
| `PUT /workspaces/{id}/members` `{email, role}` | add a member or change a role (admin) |
| `PATCH /workspaces/{id}/members/{user}` `{role}` | change a role (admin) |
| `DELETE /workspaces/{id}/members/{user}` | remove a member (admin), or leave (self) |
| `POST /bots` `{..., workspace_id}` | create a bot in a workspace (developer) |
| `PUT /bots/{id}/workspace` `{workspace_id}` | move a bot |
| `GET /admin/workspaces[?user=ID]` | all workspaces, or one account's (administrators) |
| `GET /admin/workspaces/{id}` | members, bots with owners, sites, recent operations |
| `GET /admin/users/{id}` | an account's workspaces, owned bots and recent operations |

Bot objects include `workspace_id`. Migration `0024_workspaces.sql` creates a
personal workspace for every existing account and moves each bot into its
owner's.
