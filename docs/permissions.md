# Roles, permissions and email verification

RivetPanel accounts hold one **role**. A role is a named set of
**permissions**; the panel checks them on the server for every request, so a
permission missing from a role is refused with `403 Forbidden` even if a
client calls the API directly. The interface hides what a role cannot do, but
it is never the enforcement point.

## Built-in roles

Two roles are seeded by migration `0044` and cannot be changed or deleted:

| Role | Holds | Notes |
| --- | --- | --- |
| Administrator (`admin`) | Every permission | Also the only role that opens Administration → Environment and Modules and that can make, change or remove administrators. Never restricted by the unverified-email policy. |
| User (`user`) | Every resource permission, no administration permission | Exactly the behaviour regular accounts had before roles existed. |

Existing accounts keep their built-in role. Permissions added in later
versions apply to the built-in roles automatically (they are computed by the
application, not stored).

## Custom roles

Administration → Roles creates, edits and deletes custom roles (permission
`roles.manage`). Assign a role under Administration → Users → ⋯ → Change role
(permission `users.manage`), or pick it when adding a user. Account
invitations still grant a built-in role; assign a custom role after the
account exists.

* A change to a role applies on the account's next request; sessions are not
  signed out.
* A role that any account holds cannot be deleted.
* Creating, changing and deleting roles and assigning them are recorded in the
  activity log (`admin.role_create`, `admin.role_update`,
  `admin.role_delete`, `admin.user_role`), with the role name and permission
  list; refused attempts are recorded as `denied`.

### Delegated administration rules

Accounts with a custom role that includes administration permissions can never
hand out more than they hold:

* Only administrators make administrators, change an administrator's role,
  email address or verified flag, or disable an administrator.
* An account manager cannot change the role, email address or verified flag
  of, or enable/disable, an account whose role holds an administration
  permission the manager lacks (otherwise an address change followed by a
  password reset would take that account over). Accounts with the same or
  fewer administration permissions, and plain users, stay manageable.
* A role manager can only create or edit roles whose permissions it holds
  itself, cannot edit the role it holds, and cannot edit or delete a role that
  holds permissions it lacks.
* An account manager can only assign (or invite with) a role whose
  permissions it holds; the built-in User role counts as every resource
  permission. It cannot change its own role.
* The last active administrator cannot be given another role.

## Permission catalog

Resource permissions (what an account may do with what it owns, with bots
shared with it and in workspaces it belongs to):

| Permission | Allows |
| --- | --- |
| `bots.create` | Create bots and game servers |
| `bots.delete` | Delete bots and servers |
| `bots.console` | Read console output and logs |
| `bots.power` | Start, stop, restart, kill; console input and one-shot commands; batch actions |
| `bots.files` | File manager, SFTP, package manager and AI file changes |
| `bots.env` | Read and change environment variables |
| `bots.backups` | Create, restore, verify, download and delete backups |
| `bots.deploy` | Link GitHub repositories, deploy, publish and push |
| `bots.share` | Share bots, bot invitations and ownership transfer |
| `sites.create` | Create static sites |
| `workspaces.create` | Create team workspaces |
| `api_keys.manage` | Create SFTP API keys, automation tokens and API clients |
| `ai.use` | Use the AI assistant |
| `tickets.create` | Open support tickets and reply to your own |

Administration permissions:

| Permission | Opens |
| --- | --- |
| `users.view` | Account list and account details |
| `users.manage` | Create, enable/disable accounts, assign roles, change addresses, mark addresses verified, account invitations |
| `roles.manage` | Create, change and delete custom roles |
| `nodes.manage` | Locations, node enrollment, certificates and settings; placing servers on nodes |
| `blueprints.manage` | Game server types (import, enable, disable, delete) |
| `allocations.manage` | Port allocations |
| `settings.manage` | Panel address, sign-in providers, registration, email, unverified-account policy |
| `mail.announce` | Email announcements |
| `ai.manage` | AI provider profiles and web search |
| `sites.manage` | Every site, sites domains |
| `workspaces.view` | Every workspace |
| `system.view` | Host monitoring, panel logs, diagnostics |
| `tickets.view_all` | Read every support ticket, internal notes included |
| `tickets.manage` | Answer every support ticket, internal notes, status, priority, assignment |
| `kb.manage` | Write, publish and delete help articles and categories; turn the public help center on or off; preview drafts |
| `status.manage` | Configure the public status page, its components, incidents and maintenance (nodes only with `nodes.manage`, bots only those the account can open) |
| `analytics.view` | Administration → Analytics: panel-wide usage trends (see below) |

Support staff permissions follow the delegated-administration rule above: a
staff account that is not a built-in administrator never sees tickets of an
administrator or of an account holding administration permissions it lacks.
Custom roles created before `tickets.create` existed do not include it. See
`docs/support.md`.

Help articles marked *staff only* are readable with `kb.manage`,
`tickets.view_all` or `tickets.manage`. Reading the help center needs no
permission (and no account for public articles while the public help center
is on).

Usage analytics (`analytics.view`, see `docs/features.md`) follow the same
delegated-administration rule: a viewer that is not a built-in administrator
only counts accounts it could manage (not administrators, not accounts holding
administration permissions it lacks) and the bots those accounts own. Nodes
and locations need `nodes.manage` as well, ticket figures `tickets.view_all`
or `tickets.manage`, owners' addresses `users.view`. A bot's own Analytics tab
needs console access to that bot (`bots.console` and a grant that includes
console), like the live resource gauges.

`GET /api/v1/admin/permissions` returns this catalog with labels;
`GET /api/v1/auth/me` returns the signed-in account's effective `permissions`.

## Enforcement boundary

Enforced by the **panel application** (not the container runtime or the
kernel):

* API route middleware refuses requests whose permission the account lacks,
  including the automation API (`bots.power`, `bots.deploy`,
  `bots.backups` on top of the token's scope).
* Services re-check administration permissions, so internal callers cannot
  skip them.
* The four per-bot permissions (`bots.console`, `bots.power`, `bots.files`,
  `bots.env`) also mask every path to a bot: ownership, workspace roles and
  per-bot sharing grants. A full-access share cannot give back what the role
  removes. SFTP uses the same mask and re-reads the account periodically
  during a session; the console checks the role when it connects and
  re-checks bot access while open.

Roles do not change container isolation, network policy or resource limits.

## Email verification

Accounts created from now on (registration, invitations, administrators
adding users) start with an unverified email address. Accounts that existed
before migration `0044` count as verified, and accounts created through GitHub
or Discord count as verified because those providers only hand over verified
addresses. Using a password-reset link also verifies the address.

* Settings → Profile → Email address sends a verification link (when email is
  set up and the panel address is known). Accounts registered from an
  invitation get one automatically.
* The link carries a random token in the URL fragment (it never reaches server
  logs); only its SHA-256 is stored. It works once, expires after 24 hours and
  a newer link replaces the older one.
* Resending is limited to one link a minute and five requests an hour per
  account; confirming is limited per IP address.
* Changing your own email address needs your current password (or, without a
  password, a sign-in within the last 10 minutes). The address changes only
  when the link sent to the new address is used; the old address is told about
  the request and about the change. An account manager changing an address
  makes it unverified again.
* Account managers can change an account's address (it becomes unverified;
  the old address is told) and mark an address verified or not verified on
  the Users page, for panels without email.

### Restricting unverified accounts

Administration → Panel settings → **Unverified email addresses** chooses which
permissions are withheld from accounts whose address is not verified (off by
default). When turned on it suggests creating bots, sites and workspaces, API
keys and the AI assistant; any permission can be chosen. Withheld permissions
are refused by the server (`403`, "verify your email address") until the
address is verified; administrators are never restricted. Affected accounts
see a banner with a link to verify.

## API clients

API clients (`docs/auth.md`) are bearer credentials that carry a subset of
their creator's permissions:

* Only permissions the creator holds when creating it can be chosen
  (`api_keys.manage` is needed to create one); every request intersects the
  client's list with the creator's **current** role, so a role change applies
  to the creator's clients on the next request.
* A client never acts as a built-in administrator, even one an administrator
  created: administrator-only pages are refused, other accounts' bots are not
  visible, and the delegated-administration rules above apply to whatever
  administration permissions it carries.
* A client limited to bots or workspaces is refused (`403`) outside them.
* Clients cannot manage credentials, sessions, sign-in methods or the
  profile. Account managers can list every client and revoke those of
  accounts they outrank (Administration → API clients, `users.view` to list,
  `users.manage` to revoke).

A single sign-on provider that creates accounts gives them a default role;
delegated `settings.manage` holders can only choose roles whose permissions
they hold, and a role a provider uses cannot be deleted.
