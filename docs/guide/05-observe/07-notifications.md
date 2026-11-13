---
title: Notifications
lede: Choose what the panel tells you about and where: the bell in the top bar, email, or a Discord channel.
icon: bell
glance:
  - 200 notifications kept per account
  - Kept for 90 days
  - Bell refreshes every 30 seconds
related: [observe/health, teams/support, operate/email]
---

## The bell

The bell in the top bar shows how many notifications you have not read, and
the latest ones. **All notifications** (`/notifications`) lists them with
paging and an **unread only** filter, and marks one or all read, deletes one or
deletes every read notification.

Only you see your notifications. API clients cannot read them.

## What you are told about

| Category | Who receives it | Events |
|---|---|---|
| App alerts | The app's owner | A crash (failed while it should run; at most one per bot every 10 minutes), heartbeat lost and resumed |
| Deployments | The bot's owner | A GitHub deployment succeeded or failed |
| Backups | The app's owner | A backup failed |
| Nodes | Accounts holding `nodes.manage`, administrators included | A node's agent has been disconnected for 2 minutes, and when it is back |
| Access and invitations | The account affected | An app was shared with you or transferred to you, you were added to a workspace, someone accepted your invitation |
| Announcements | The announcement's audience | An administrator posted an announcement |
| Support tickets | Requester or support staff | New tickets, replies, status changes and assignment (see [Support](../06-teams/05-support.md#ticket-notifications)) |
| Extensions | Accounts that manage extensions | Messages from panel extensions, and extensions that stopped after repeated failures |

A node that reconnects within 2 minutes sends nothing, and a "back online"
notice follows only an offline notice.

Alerts come from the existing alert paths; nothing new is polled. Each bot's or
game server's own switches under **Settings → Notifications** still decide
whether an alert happens at all (see [Health and alerts](04-health.md)).

## Choose where they go

Your account's **Settings → Notifications** turns each category on or off *in
panel* and by *email*. Both are on by default.

Email for a category is sent only when:

- email is set up on the panel (see [Email](../07-operate/06-email.md)),
- your address is **verified**, since an unverified address may not belong to
  you, and
- for App alerts, Deployments, Backups and Nodes, **Alert emails** is also on in
  your profile (**Settings → Profile**).

Announcement email follows its own notice and news rules, so for announcements
the setting controls only the bell. Security notices, such as a password or
two-step sign-in change, are always emailed and are not notifications.

## Discord notifications

Alerts about your apps can also go to a Discord channel, through a webhook you
choose.

1. Open **Settings → Connected accounts → Discord** and connect Discord.
2. Choose **Enable notifications** and pick the channel. Discord asks for the
   `webhook.incoming` permission only at this step.
3. Use **Send test** to check the connection.

The webhook receives the alerts each bot or game server chooses under its
**Settings → Notifications**: crashes, deployments, backup failures and the
heartbeat rule. Crash alerts are limited to one per bot every 10 minutes.
Messages come from your account's webhook, not from a Discord bot.

Only Discord's own `https://…/api/webhooks/` addresses are ever called, so a
stored webhook cannot make the panel contact another host. [enforced: panel]
The webhook URL is encrypted at rest with the panel's key (`RIVET_KEY_DIR`).
Encryption does not hide it from someone with root on the host.

## Delivery and limits

| Limit | Value | |
|---|---|---|
| Notifications kept per account | 200; older ones are dropped as new ones arrive | [enforced: panel] |
| Age | Notifications older than 90 days are pruned hourly | [enforced: panel] |
| One notice to every holder of a permission | Reaches at most 1,000 accounts | [enforced: panel] |
| Email | Best effort within the mail limits: 2 sends at a time, 20 per recipient per hour, 300 per hour | [enforced: panel] |

- The bell **polls**. It asks for the unread count every 30 seconds while the
  tab is visible, and after every page change. A hidden tab does not poll, and
  there is no account-wide event stream.
- Email that cannot start is dropped and logged, never queued.
- Notifications are plain text with an optional link to a panel page. The
  interface never renders them as HTML.

## Who can see what

Every inbox query is tied to the signed-in account, so one account can never
list, mark or delete another account's notifications. A foreign id answers
`404` and changes nothing. [enforced: panel]

The inbox and preferences work in a browser session only. API clients are
refused (`403`), because notifications carry ticket replies and alerts about
every resource of the account, and changing preferences could silence them.
[enforced: panel]
