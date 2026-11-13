---
title: Support desk, help center and status page
lede: Ask for help with a ticket, answer tickets as staff, publish help articles, and show the public how your services are doing.
icon: lifebuoy
aliases: [support]
glance:
  - 10 open tickets per account
  - 10,000 characters per message
  - 90 days of public uptime history
related: [observe/notifications, teams/roles-and-permissions, teams/appearance]
---

## Support tickets

**Support** in the sidebar (`/support`) lists your tickets and opens new ones.
A ticket has:

| Field | Values |
|---|---|
| Subject | Up to 150 characters |
| Category | General, technical, billing, account, abuse |
| Priority | Low, normal, high, urgent |
| App | Optional: a bot or server you can access right now |
| Message | Up to 10,000 characters of Markdown |

Replies are threaded. Staff answers show as "Support team", and staff
addresses are not revealed to requesters. Attachments are not supported.

While you type a subject, up to five related [help articles](#help-center)
are suggested.

### Ticket keys

Every ticket has a key: `RT-` and its number padded to six digits (`RT-000001`
to `RT-999999`; later numbers simply grow wider). Keys appear in lists,
notifications, email subjects and the activity log.

- `/support/RT-000042` opens the ticket, and the API accepts the key wherever
  it takes a ticket id.
- Search matches `RT-000042`, `rt-42`, `#42` or `42`.

### Statuses

| Status | Means |
|---|---|
| Open | Waiting for staff |
| Pending | Waiting for the requester |
| Resolved | Solved |
| Closed | Finished; the requester cannot reply |

- A staff reply moves an open ticket to pending.
- A requester reply moves a pending or resolved ticket back to open.
- Requesters can close their own ticket and reopen a resolved one. Closed
  tickets take no more requester replies; staff can reopen them.

### Find tickets

The list has tabs for **Active** (open and pending), each status and **All**,
each with its count. The search box (`/`) matches the subject, the ticket key,
the linked app and, for staff, the requester and assignee addresses.

Staff can also narrow the queue by assignee (anyone, me, nobody, tickets they
watch) and by labels.

Each row shows how long ago the ticket last changed. On an **open** ticket the
time turns amber after 4 hours and red after 24. This is time since any
activity on the thread, internal notes and staff changes included; it is not a
measured response time.

## Layouts

**Settings → Appearance → Support tickets**, or the switcher at the top of the
Support page, chooses how Support and the staff queue lay out tickets. The
choice is kept per browser, like the theme.

| Layout | What it shows |
|---|---|
| **Split** (default) | Tickets on the left and the selected ticket on the right, without leaving the page. The address keeps the selected ticket (`&t=<id>`) |
| **Table** | Sortable columns: key, subject with labels, status, priority, for staff the requester and assignee with their pictures, and the last update. A ticket opens in a panel that slides in from the right; the table stays usable behind it |
| **Board** | A column per status. Pick a card up with the mouse (or hold it on a touch screen), carry it to another column and drop it; it glides into place, with **Undo**. Focusing a card and pressing `1`–`4` does the same. Staff who manage tickets can move a card to any status; a requester can drag their own ticket to Closed, or a resolved one back to Open. Columns a card cannot go to are dimmed while it is carried; Esc or a drop elsewhere returns it. Closed shows its 30 most recent tickets |
| **Classic list** | A full-width list; a ticket opens on its own page |

The Support page uses the whole width of the window; other pages keep a
readable column. Windows with a content area under 880 px, and phones, always show the classic
list.

A ticket's own page shows the conversation beside a **Details** sidebar:
status, assignee, requester, priority, labels, category, linked app, watchers
and dates. On narrow screens the details become a compact row under the title.

- Statuses show as colored lozenges: open in the accent color, waiting amber,
  resolved green, closed grey.
- Priorities show as marks: urgent a red double chevron, high an amber chevron,
  normal two bars, low a blue chevron down.
- People show with their pictures. Tickets assigned to you have an accent dot
  before the key.

### Keyboard

| Key | Action |
|---|---|
| `J` / `K` (↓ / ↑ on a row) | Next or previous ticket (split and table) |
| `Enter` on a row or card | Split: open the ticket's page. Table and board: open the panel |
| `Esc` | Close the panel, leave the reply box, or clear the search |
| `R` | Reply |
| `N` | Internal note (the reply box turns amber) |
| `1` – `4` | Status: open, pending, resolved, closed (with **Undo**) |
| `A` | Assign to yourself ("Take it") |
| `E` | Edit the subject |
| `/` | Search; in an empty reply box (staff), a saved reply |
| `Ctrl+Enter` (⌘+Enter) | Send the reply or note; save an edited message |

Shortcuts are ignored while you type in a field or while a dialog is open. The
ones that change a ticket need `tickets.manage`, and replying needs the right
to reply.

The reply box stays docked at the bottom of a ticket. A reply shows in the
thread as soon as it is sent and is put back in the box if the server refuses
it. Status, priority and assignment also change on screen at once and are
rolled back with an error if refused. A ticket whose status no longer matches
the tab stays in the list, dimmed, until the tab or filter changes.

An unsent reply or note is kept per ticket while the panel stays open (rows
show **Draft**). It is held in the browser tab only and is lost on reload.

## Conversations

- **Markdown:** messages render bold, italics, code, lists, quotes, tables and
  links (`http(s)` and panel paths only). HTML shows as text and never runs.
  [enforced: panel]
- **Quote** puts a message into the reply box as a quote.
- **Copy link** links to one message (`/support/RT-000042#m-<id>`), which
  scrolls to it and highlights it.
- **Edit:** authors edit their own replies and notes (`Ctrl+Enter` saves, `Esc`
  cancels). The message shows **edited**. Earlier versions are kept and shown
  to the author and to staff, never to a requester for a staff message.
  Requesters cannot edit once the ticket is closed; editing a staff message
  needs `tickets.manage`.
- **Remove:** authors remove their own messages (requesters while the ticket is
  not closed), and staff with `tickets.manage` remove any message. The text,
  its earlier versions and its reactions are deleted from the database. A
  "Message removed" line keeps the conversation in order, and staff see who
  removed it. The first message cannot be removed; close the ticket instead.
- **Reactions:** Agree, Thanks, Seen and Solved it, one of each per person and
  message, from the message's toolbar or the chips under it. Staff see who
  reacted; requesters see staff as "Support team". Nobody can react on a closed
  ticket or to a note they cannot read.
- **Mentions** (internal notes only): type `@` and pick a colleague. The
  mentioned account must be staff that can read the ticket, and it is notified
  ("You were mentioned on RT-…"). Mentions in replies the requester sees are
  refused.

## Staff tools

### Details

The requester changes the subject, category and linked app while the ticket is
open or pending, and can link only apps it can reach at that moment. Staff with
`tickets.manage` change them at any time, but can only remove a linked app,
not link one. Every change is recorded in the conversation.

### Internal notes

Notes (`N`) are visible to staff only. Notes and assignment events are never
returned to a requester, and requester views leave out the assignee and the
requester's address. [enforced: panel]

### Labels, saved replies and watchers

- **Labels** (staff only): colored tags created under **Tools → Manage labels**
  (`tickets.manage`; at most 200, and 10 per ticket). Every staff account sees
  them; requesters never do. Deleting a label removes it from every ticket.
- **Saved replies** (`tickets.manage`): answers shared by the support team,
  inserted from the reply box (the **Saved replies** button, or `/` in an empty
  box) and managed under **Tools → Saved replies** (at most 300).
- **Watchers** (staff only): **Watch** follows a ticket without being
  assigned. Watchers get the assignee's notifications (requester replies,
  internal notes, status changes) for as long as they can still read the
  ticket. Requesters never see watchers.

## Who can see tickets

| Permission | Group | Allows |
|---|---|---|
| `tickets.create` | Resource (in the User role) | Open tickets, read and reply to your own |
| `tickets.view_all` | Administration | Read every ticket you outrank, internal notes included; no replies or changes |
| `tickets.manage` | Administration | Read and answer every ticket you outrank, write internal notes, change status and priority, assign |

Administrators hold all three. Custom roles may delegate the staff
permissions; role managers can only grant what they hold.

All of these rules are checked on the server. [enforced: panel]

- **Requesters see only their own tickets.** Workspace members do not see each
  other's tickets, even about an app in a shared workspace: tickets can contain
  account matters, and the linked app is only context.
- **Delegated staff** follow the account-management rule: a staff account that
  is not a built-in administrator cannot see or act on tickets of an
  administrator, or of an account holding administration permissions it lacks.
  Those tickets answer `404` and are left out of the queue. Assignees must hold
  `tickets.manage` over the requester.
- A ticket you cannot see answers `404`, never `403`, so ticket ids cannot be
  probed.
- **API clients** reach tickets only when their permission list carries the
  `tickets.*` permission needed, and never when limited to some bots or
  workspaces (`403`).
- Staff see the linked app's name, not its files. A ticket grants no access to
  the app.

### Ticket notifications

| Event | Who is notified |
|---|---|
| New ticket | Every account holding `tickets.manage` over the requester |
| Requester reply | The assignee, or all such staff when unassigned |
| Public staff reply, staff status change | The requester |
| Internal note | The assignee (never the requester) |
| Assignment | The new assignee |
| Mention in a note | The mentioned colleague |

Watchers also get the assignee's notifications. Email follows each
recipient's **Support tickets** preference and goes to verified addresses only
(see [Notifications](../05-observe/07-notifications.md)). Messages carry a
300-character excerpt and a link to the ticket.

## Ticket limits and audit

| Limit | Value | |
|---|---|---|
| Open and pending tickets | 10 per account | [enforced: panel] |
| New tickets | 10 per hour per account | [enforced: panel] |
| Replies | 60 per 10 minutes per account | [enforced: panel] |
| Edits and removals | 60 per 10 minutes per account | [enforced: panel] |
| Reactions | 120 per 10 minutes per account | [enforced: panel] |
| Messages | 500 per ticket; 10,000 characters each | [enforced: panel] |
| Subject | 150 characters | [enforced: panel] |
| Mentions | 10 per note | [enforced: panel] |
| Attachments | None | [enforced: panel] |

Attachments are deliberately left out: they would need bounded storage outside
the web root, content sniffing and download-only serving.

The activity log records `support.ticket_create`, `support.ticket_reply` (the
target notes "internal note"), `support.ticket_update` (status, priority,
assignee, details), `support.message_edit`, `support.message_delete`,
`support.ticket_labels`, `support.label_create|update|delete` and
`support.saved_reply_create|update|delete`, and keeps refused attempts as
`denied`. Message bodies are never recorded. Reactions and watching are not
audited.

## Help center

The **Help center** (`/help`, also under **Help & resources** in the sidebar)
lists and searches help articles.

### Who can read an article

**Draft** articles are visible to knowledgebase managers only, who can open
`/help/<slug>` to preview them. **Published** articles follow their
visibility:

| Visibility | Readers |
|---|---|
| Public | Every signed-in account; visitors without an account too, but only while **Public help center** is on |
| Signed-in accounts | Every signed-in account |
| Support staff only | Accounts holding `kb.manage`, `tickets.view_all` or `tickets.manage` |

With the public help center off (the default), anonymous requests answer `401`
and the page asks the visitor to sign in. An article a reader may not see
answers `404` and never appears in lists or search results. [enforced: panel]

Search matches words in titles, summaries and bodies, with title matches first.
It reads at most 200 candidate articles per query; it is not a full-text index.
Ticket suggestions show up to five readable articles that match the subject's
words, with common words dropped.

### Write articles

*Needs `kb.manage`.* **Administration → Knowledgebase** writes articles and
categories and turns the public help center on or off.

| Article field | Notes |
|---|---|
| Title | |
| Address | `/help/<slug>`, from the title unless you choose one |
| Summary | Optional, one line |
| Body | Markdown, at most 100,000 characters |
| Category | Optional |
| Order, state, visibility | State is draft or published |

Categories have a name, an address, a description and an order. Deleting a
category leaves its articles uncategorized.

### Safe Markdown

Articles support headings, bold, italic, inline code, code blocks, lists,
quotes, tables and links. Bodies are stored as text and rendered as text
nodes, so HTML in an article (for example `<script>` or `<img onerror=…>`)
shows literally and never runs. [enforced: panel]

Saving refuses control characters and any link that is not an `https://` or
`http://` URL or a path of this panel (such as `/support`). `javascript:`,
`data:`, protocol-relative (`//host`) and similar links are refused outside
code. [enforced: panel] External links open in a new tab without a referrer.

### Help center limits and audit

Public help center requests are limited to 120 a minute per client address.
[enforced: panel] The activity log records `kb.article_create|update|delete`,
`kb.category_create|update|delete` and `kb.settings`, with refusals kept as
`denied`; article bodies are not recorded.

## Public status page

*Needs `status.manage`.* **Administration → Status page** publishes `/status`
and its JSON at `GET /api/v1/status`. While **Publish the status page** is off,
both answer `404` and no samples are taken.

### What it shows, and never shows

The public page carries only the title and introduction; each component's
public name and description (typed by the manager) with its current state, 90
daily bars and uptime percentage; and incidents and maintenance (title,
impact, status, window, affected component names, and update texts with
times). Component ids on the page are the status page's own ids.

It never includes the node or app behind a component, their ids or real names,
owners, addresses, error messages or who wrote an update. [enforced: panel]

> [!WARNING]
> A component's public name is published exactly as typed. Choose names that
> reveal nothing you would not show the public.

### Components and states

A component follows the panel, a node, or a bot or game server:

| Source | Automatic state |
|---|---|
| Panel | Operational (the panel is answering) |
| Local node | Operational (it is the panel's host) |
| Agent node | Operational while its agent is connected; major outage while disconnected or disabled |
| Bot or game server | Operational while running; degraded while starting, building or stopping, or when its health check fails; major outage when stopped or failed; unknown when deleted or on a disconnected node |

An open incident raises its components' state to its impact: minor →
degraded, major → partial outage, critical → major outage. Maintenance shows
its components as *under maintenance* while marked in progress or while its
scheduled window runs, until it is completed.

Delegated managers can add nodes only with `nodes.manage`, and only apps they
can open; the management page names each source for them.

### Incidents and maintenance

- **Incidents** move through investigating → identified → monitoring →
  resolved, in any order; reopening is allowed. Each change adds a timeline
  entry, with your text or a default one. Resolved incidents stay on the public
  page for 14 days.
- **Maintenance** moves through scheduled → in progress → completed, with a
  start time and an optional end time.
- Deleting an incident removes it and its timeline; prefer resolving it.

### Uptime history

Every 5 minutes, while the page is enabled, each component's state is counted
for the current UTC day. 90 days are kept by default, and older days are
pruned; **Administration → Logs and retention** can keep up to 730 days (see
[Log files and retention](../05-observe/06-logs.md#graph-and-analytics-retention)).

- A day's bar shows the worst state it saw: operational, degraded, partial or
  major outage.
- Uptime is (operational + degraded) ÷ (those + outages). Maintenance and
  unknown samples do not count.
- Hours or days without samples, because the panel itself was down or the page
  was disabled, show as no data. The panel cannot observe its own downtime.

### Status page limits and audit

| Limit | Value | |
|---|---|---|
| `GET /api/v1/status` | 60 requests a minute per client address; may be cached for 30 seconds | [enforced: panel] |
| Components | 50 | [enforced: panel] |

The activity log records `status.config`,
`status.component_create|update|delete` and
`status.incident_create|update|post|delete`, and keeps refusals. Subscriber
notifications by email or webhook are not implemented.
