# Notifications and support tickets

## Notifications

Every account has a notification inbox: the **bell** in the top bar shows the
unread count and the latest notifications; **All notifications**
(`/notifications`) lists them with paging, "unread only", mark read, mark all
read, delete and "delete read".

What creates notifications (the existing alert and event paths, nothing new
is polled):

| Category | Who receives it | Events |
| --- | --- | --- |
| Bot and server alerts | The bot's owner | Crash (failed state while it should run, at most one per bot per 10 minutes), heartbeat lost / resumed |
| Deployments | The bot's owner | GitHub deployment succeeded or failed |
| Backups | The bot's owner | Backup failed |
| Nodes | Accounts holding `nodes.manage` (administrators included) | A node's agent has been disconnected for 2 minutes; it is back (only after an offline notice). Reconnects within 2 minutes send nothing |
| Access and invitations | The account affected | A bot was shared with you, transferred to you, you were added to a workspace, someone accepted your bot invitation |
| Announcements | The announcement's audience | An administrator posted an announcement to the inbox |
| Support tickets | Requester / staff (see below) | New ticket, replies, status changes, assignment |

The per-bot alert switches (crash, deploy, backup) still decide whether an
alert happens at all; the Discord webhook keeps working as before.

### Preferences

**Settings → Notifications** chooses, per category, *in panel* and *email*
(both on by default). Email:

* needs email to be set up (Mailgun, `docs/email.md`);
* goes only to a **verified** address (an unverified address may not belong
  to the account holder). This also applies to bot alert emails, which were
  previously sent to unverified addresses too;
* for alert categories (alerts, deployments, backups, nodes) also needs the
  existing **Alert emails** switch on the profile;
* announcement email keeps its own notice/news rules, so that column only
  controls the inbox;
* security notices are always emailed and are not notifications.

### Delivery and limits

* Live update is **polling**: the bell asks for the unread count every 30
  seconds while the tab is visible and after every page change (there is no
  account-wide event stream). A hidden tab does not poll.
* Each account keeps at most **200** notifications (older ones are dropped
  when new ones arrive) and notifications older than **90 days** are pruned
  hourly.
* Notifications are plain text with an optional same-origin link; the
  interface never renders them as HTML.
* Email is best effort through the existing mail limits (2 sends in flight,
  20 per recipient per hour, 300 per hour); messages that cannot start are
  dropped and logged, never queued.
* A notice to all holders of a permission reaches at most 1,000 accounts.

### Who can see what

Enforced by the panel application: every inbox query is keyed by the
signed-in account, so one account can never list, mark or delete another
account's notifications (a foreign id answers 404 / changes nothing). The
inbox and preferences are **browser-session only**: API clients are refused
(403), because notifications carry ticket replies and alerts about every
resource of the account and preferences could silence them.

## Support tickets

**Support** in the sidebar (`/support`) lists your tickets and opens new
ones: subject, category (general, technical, billing, account, abuse),
priority (low, normal, high, urgent), an optional bot or server **you can
access right now**, and the message. Replies are threaded; staff answers are
shown as "Support team" (staff addresses are not revealed to requesters).

Statuses: **open** (waiting for staff), **pending** (waiting for the
requester), **resolved**, **closed**. A staff reply moves an open ticket to
pending; a requester reply moves a pending or resolved ticket back to open.
Requesters can close their ticket and reopen a resolved one; closed tickets
take no more requester replies (staff can reopen them).

### Permissions

| Permission | Group | Allows |
| --- | --- | --- |
| `tickets.create` | resource (built-in User role) | Open tickets, read and reply to your own tickets |
| `tickets.view_all` | administration | Read every ticket (that you outrank, see below), internal notes included; no replies or changes |
| `tickets.manage` | administration | Read and answer every ticket you outrank, write internal notes, change status and priority, assign |

Administrators hold all three. Custom roles may delegate the staff
permissions; role managers can only grant what they hold (the existing
role rules).

### Authorization decisions

* **Requesters see only their own tickets.** Workspace members do not see each
  other's tickets, even about a bot in a shared workspace: tickets can contain
  account matters, and the linked bot is only context.
* **Internal notes** (`internal` messages and assignment events) are never
  returned to a requester; the API omits the assignee and requester address
  from requester views.
* **Delegated staff follow the account-management rule** (`manageable`): a
  staff account that is not a built-in administrator cannot see or act on
  tickets of an administrator or of an account holding administration
  permissions it lacks (404, filtered from the queue). Assignees must hold
  `tickets.manage` over the requester.
* A ticket you cannot see answers **404**, never 403, so ticket ids cannot be
  probed.
* **API clients** reach tickets only when their permission list carries the
  `tickets.*` permission needed and never when limited to some bots or
  workspaces (403).
* Staff see the linked bot's name, not its files; no access to the bot is
  granted by a ticket.

### Notifications and email

* New ticket: every account holding `tickets.manage` over the requester.
* Requester reply: the assignee, or all such staff when unassigned.
* Public staff reply and staff status changes: the requester.
* Internal note: the assignee (not the requester).
* Assignment: the new assignee.

Email follows each recipient's *Support tickets* preference (verified address
only). Messages carry a 300-character excerpt and a link to the ticket.

### Limits and audit

* 10 open/pending tickets per account, 10 new tickets per hour, 60 replies
  per 10 minutes per account, 500 messages per ticket, 10,000 characters per
  message, 150-character subjects.
* No attachments (deliberately left out: they would need bounded storage
  outside the web root, content sniffing and download-only serving).
* Audited: `support.ticket_create`, `support.ticket_reply` (target notes
  "internal note"), `support.ticket_update` (status/priority/assignee);
  refused attempts are kept as `denied`. Message bodies are never recorded.

## Knowledgebase (help center)

**Help center** (`/help`, also under Help & resources in the sidebar) lists and
searches help articles. Administration → **Knowledgebase** (permission
`kb.manage`) writes them.

* Articles have a title, an address (`/help/<slug>`, derived from the title
  unless chosen), an optional one-line summary, a Markdown body (100,000
  characters at most), a category (or none), an order, a state and a
  visibility. Categories have a name, address, description and order;
  deleting a category leaves its articles uncategorized.
* **Draft** articles are visible to knowledgebase managers only (they can
  open `/help/<slug>` to preview them). **Published** articles follow their
  visibility:

| Visibility | Readers |
| --- | --- |
| Public | Every signed-in account; visitors without an account too, but only while **Public help center** is on |
| Signed-in accounts | Every signed-in account |
| Support staff only | Accounts holding `kb.manage`, `tickets.view_all` or `tickets.manage` |

* With the public help center off (the default), anonymous requests to the
  help center answer 401 and the page asks the visitor to sign in.
* An article a reader may not see answers 404 and never appears in lists or
  search results.
* **Related articles**: while a ticket subject is typed on the new-ticket
  form, up to five readable articles that match its words (common words
  dropped) are suggested.
* Search matches words in titles, summaries and bodies (title matches rank
  first). It reads at most 200 candidate articles per query; it is not a
  full-text index.

### Safe Markdown

Supported: headings, bold, italic, inline code, code blocks, lists, quotes,
tables and links. Article bodies are stored as text and returned inside JSON;
the interface renders them as text nodes, so HTML in an article (for example
`<script>` or `<img onerror=…>`) is displayed literally and never runs.
Saving refuses control characters and any link that is not an `https://` or
`http://` URL or a path of this panel (`/support`); `javascript:`, `data:`,
protocol-relative (`//host`) and similar links are refused outside code.
External links open in a new tab without a referrer.

### Limits and audit

Public help center requests are limited to 120 a minute per client address.
Audited: `kb.article_create|update|delete`, `kb.category_create|update|delete`
and `kb.settings` (refusals kept as `denied`); article bodies are not
recorded.

## Public status page

Administration → **Status page** (permission `status.manage`) publishes
`/status` and its JSON at `GET /api/v1/status`. While **Publish the status
page** is off, both answer 404 and no samples are taken.

### What it shows (and never shows)

The public page carries only: the title and introduction, each component's
public name and description (typed by the manager) with its current state,
90 daily bars and uptime percentage, and incidents and maintenance (title,
impact, status, window, affected component names and the update texts with
times). Component ids on the page are the status page's own ids. It never
includes the node or bot behind a component, their ids or real names, owners,
addresses, error messages or who wrote an update. Choose public names
accordingly: the name is published as typed.

### Components and states

A component follows the panel, a node or a bot/game server:

| Source | Automatic state |
| --- | --- |
| Panel | Operational (the panel is answering) |
| Local node | Operational (it is the panel's host) |
| Agent node | Operational while its agent is connected; major outage while disconnected or disabled |
| Bot or game server | Operational while running; degraded while starting/building/stopping or when its health check is failing; major outage when stopped or failed; unknown when deleted or on a disconnected node |

An open incident raises the state of its components to its impact (minor →
degraded, major → partial outage, critical → major outage). Maintenance shows
its components as *under maintenance* while marked in progress or while its
scheduled window is running, until it is completed.

Delegated managers can add nodes only with `nodes.manage`, and only bots and
servers they can open; the management page names each source for them.

### Incidents and maintenance

* Incidents: investigating → identified → monitoring → resolved (any order;
  reopening is allowed). Each change adds a timeline entry (with your text or
  a default one). Resolved incidents stay on the public page for 14 days.
* Maintenance: scheduled → in progress → completed, with a start time and an
  optional end time.
* Deleting an incident removes it and its timeline (prefer resolving).

### Uptime history

Every 5 minutes (while the page is enabled) the state of each component is
counted for the current UTC day; 90 days are kept and older days are pruned.
A day's bar shows the worst it saw (operational, degraded, partial or major
outage) and its uptime is (operational + degraded) / (those + outages);
maintenance and unknown samples do not count. Hours or days without samples
(the panel itself was down, or the page was disabled) show as no data — the
panel cannot observe its own downtime.

### Limits and audit

`GET /api/v1/status` is limited to 60 requests a minute per client address
and may be cached for 30 seconds. At most 50 components. Audited:
`status.config`, `status.component_create|update|delete`,
`status.incident_create|update|post|delete` (refusals kept). Subscriber
notifications (email/webhook) are not implemented.
