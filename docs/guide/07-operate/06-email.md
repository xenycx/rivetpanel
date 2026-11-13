---
title: Email
lede: Send password resets, invitations, alerts, security notices and announcements through Mailgun, or run without email at all.
icon: send
audience: admin
aliases: [email]
glance:
  - Off until an administrator sets it up
  - Reset links work once and expire after 60 minutes
  - At most 20 emails per recipient and 300 in total per hour
related: [teams/sign-in, observe/notifications, operate/configuration]
---

## What email is for

RivetPanel sends email through Mailgun's HTTP API. It is off until an
administrator configures it, and nothing depends on it: without email every
flow keeps its other path, such as copying an invitation link, running
`rivetpanel reset-password EMAIL` on the host, or Discord alerts.

| Email | Sent when | Can people turn it off? |
| --- | --- | --- |
| Password reset link | Someone uses **Forgot your password?** on the sign-in page | Sent only on request |
| Account invitation | An administrator ticks **Email the link to this address** when inviting. It names the role, the team workspace when the invitation adds one, and how many days the link lasts | Sent only when ticked |
| Email verification link | A new account registers from an invitation, someone presses **Send verification link** under **Settings → Profile**, or asks to change their address (sent to the new address) | Sent only on request |
| Bot alerts | A bot crashes or stops reporting, or a deployment or backup fails: the same events that post to Discord, following the bot's crash, deploy and backup switches and its heartbeat settings | Yes: **Settings → Profile → Email** |
| Notifications | Support ticket replies and status changes; access changes (sharing, transfers, workspaces, accepted invitations); node offline and online for accounts that manage nodes | Yes: **Settings → Notifications**, per category |
| Security notices | Password changed (also by reset link), two-step sign-in turned on or off, an email change requested or completed (sent to the old address) | No: people must be able to learn their account changed |
| Announcements | Sent from **Administration → Announcements** | Notices: no. News: yes, under **Settings → Profile** (**Email me news and announcements**) |

Alerts go to the bot's **owner** at the address they sign in with, in
addition to their Discord channel if they connected one. Alerts and
notifications are emailed only to **verified** addresses and follow
**Settings → Notifications** per category; alerts, deployments, backups and
node notices also need the profile's **Alert emails** switch. The same events
appear in the notification inbox; see [Notifications](../05-observe/07-notifications.md).

## Set up Mailgun

1. In Mailgun, add and **verify a sending domain** (SPF and DKIM DNS records)
   and note its **region**, US or EU. A sandbox domain works for a first test
   but only delivers to recipients you authorize in Mailgun, so it cannot send
   password resets to anyone else.
2. Create an API key. A private API key works; a **domain sending key**
   limited to that one domain is the safer choice for production.
3. Open **Administration → Panel settings → Email (Mailgun)**. Enter the key,
   the domain, the region and a sender on the sending domain, such as
   `RivetPanel <noreply@mg.example.com>`, then press **Save and apply**.
4. Press **Send test**. It first checks the key, region and domain
   (read-only) and says whether the domain's DNS records are verified, then
   sends one message. A wrong key or region is explained in words.
5. Set the **panel address** on the same page: reset and invitation links
   point at it. **Forgot your password?** appears on the sign-in page only
   when both email and the panel address are set.

The same values can be fixed in the environment file with
`RIVET_MAILGUN_API_KEY`, `RIVET_MAILGUN_DOMAIN`, `RIVET_MAILGUN_REGION` (`us`
or `eu`) and `RIVET_MAIL_FROM`. They then show read-only in the panel and
always win.

### Troubleshooting the test

| The test says | Meaning |
| --- | --- |
| Mailgun rejected the API key | Wrong key, or the key belongs to the other region. Try the other region |
| Mailgun does not know that domain in this region | A typo in the domain, or the wrong region: an EU domain is invisible to the US API |
| Mailgun refused the message … (HTTP 400) | Usually a sender not on the sending domain, or a sandbox domain with an unauthorized recipient |
| Sent, but "DNS records are not all verified" | Finish the SPF and DKIM records in Mailgun; mail may land in spam until then |
| Sent, but nothing arrives | Check the Mailgun logs (accepted against delivered), spam folders, and that the domain is not a sandbox |

## Announcements

**Administration → Announcements** sends custom HTML to people with accounts:
news, policy updates, maintenance notices. It needs the announcement
permission (`mail.announce`); built-in administrators have it.

1. Write the subject and the message as HTML. The preview beside it is a
   sandboxed frame with scripts off; **Insert an example** gives a starting
   point.
2. Choose the **kind**. A *notice* (policy, service, security) goes to
   everyone in the audience whatever their preferences; *news* skips accounts
   that turned news emails off.
3. Choose the **audience**: every enabled account, or administrators only.
   The page shows how many people each choice reaches.
4. Choose **Send by**: email, the notification inbox (the bell; stored as
   plain text and following each account's Announcements preference) or
   both. Without Mailgun only the inbox is available and no test is needed.
5. Press **Send test to me**. It emails the exact message to you only, marked
   `[Test]`. Sending to everyone stays locked until you have sent a test of
   the current text, and editing anything locks it again. [enforced: panel]
6. Press **Send**. The confirmation names the subject, kind and audience.

### How announcements are sent

- Messages go out in batches of up to 100 through Mailgun's recipient
  variables, so **each person sees only their own address**.
- Every message gets the standard layout, a plain-text alternative made from
  your HTML, and a footer saying why it was received; news adds how to opt
  out.
- Scripts, frames, forms, event-handler attributes and `javascript:` links are
  stripped before sending. That is hygiene, not a security boundary: only
  permitted accounts can send, and email apps do not run scripts anyway.
- The activity record notes who sent which subject to which audience, never
  the body. Nothing else is stored: no draft, history or queue.
- Sending runs in one pass while you wait, so a large batch or a slow Mailgun
  makes the request take a while. Failures are counted and the first error is
  shown. Mailgun accepting a batch does not mean delivery.

| Limit | Value |
| --- | --- |
| HTML size | 80 KB [enforced: panel] |
| Recipients per announcement | 1,000 accounts [enforced: panel] |
| Concurrent announcements | 1 [enforced: panel] |
| Gap after an announcement | 1 minute before the next, so a double click cannot send twice; tests are exempt [enforced: panel] |

There is no unsubscribe link in the message (people opt out of news in their
profile), no scheduling, drafts, attachments or open and click tracking, and
no targeting by person or workspace. Whether you may send news to your users
at all, and what your notices must contain, is your legal responsibility.

## Security properties

- **The Mailgun key is a credential.** It is sealed with the panel's
  encryption keys, never returned by the API, never logged and never included
  in an error message. [enforced: panel]
- **Password reset cannot be used to find accounts.** The request answers the
  same way whether or not the address has an account, and disabled accounts
  get no link. [enforced: panel]
- **Reset links** are 256-bit random, stored only as a SHA-256 hash, work
  once and expire after 60 minutes; a new request replaces the old link, at
  most one per account per minute. The token travels in the URL fragment
  (`/reset#…`), so the browser does not send it to the server and it does not
  appear in proxy logs. A password that is too weak is refused before the
  link is used up. [enforced: panel]
- **Resetting a password signs the account out everywhere.** Two-step sign-in
  still applies at the next sign-in; a reset does not bypass it.
  [enforced: panel]
- **Verification links** (`/verify-email#…`) are 256-bit random, stored only
  as a SHA-256 hash, work once and expire after 24 hours; a new link replaces
  the old one. Sending is limited to one per account per minute and five per
  hour; confirming is limited per client address. [enforced: panel]
- **An address change** happens only when the link sent to the new address is
  used, and the old address is told. An account manager changing someone's
  address on the Users page makes it unverified and tells the old address.
  Without email, people cannot verify themselves: an account manager marks
  addresses verified on the Users page. See
  [Roles and permissions](../06-teams/02-roles-and-permissions.md).
- **Rate limits.** Reset endpoints allow 10 requests per 10 minutes per client
  address, and sending is capped at 20 emails per recipient and 300 in total
  per hour, so a loop or abuse cannot drain the Mailgun account. These are
  application limits, not delivery guarantees. [enforced: panel]
- The Mailgun domain, sender and recipients are validated and cannot contain
  line breaks, so a value cannot inject headers. [enforced: panel]
- Email is not a second factor: two-step sign-in remains TOTP.

## Limits and idle cost

- Delivery depends on Mailgun and your domain's DNS reputation. The panel
  does **not** receive Mailgun webhooks, so bounces, complaints and
  unsubscribes are not tracked or acted on: check the Mailgun dashboard.
- Alert and notice emails are sent in the background and are not retried. A
  failure is logged by kind only, never with the address or key.
- Invitation emails go only to the address typed on the invitation, and never
  state limits or node placement.
- There is one sender and one language. Messages are plain text with a simple
  HTML alternative. Mailgun templates, attachments, inbound mail and senders
  per workspace are not supported.

Email adds no background work. It uses the Go standard library only, with no
queue, connection pool or background goroutine, and reads its settings from
the database when a message is sent. Each message is sent from a goroutine
that lives only for that one HTTPS request, at most two at a time; if Mailgun
is slow and both are busy, further messages are dropped and logged rather
than buffered. Idle memory does not change; see
[Diagnostics and footprint](12-diagnostics.md#email-and-idle-memory).
