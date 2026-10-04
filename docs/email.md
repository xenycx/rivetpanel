# Email (Mailgun)

RivetPanel can send email through [Mailgun](https://www.mailgun.com/) over its
HTTP API. It is **off until an administrator configures it**, and nothing in the
panel depends on it: without it, every flow keeps its non-email path (copy the
invitation link, `rivetpanel reset-password EMAIL`, Discord alerts).

## What it sends

| Email | When | Can the user turn it off? |
| --- | --- | --- |
| Password reset link | Someone asks on the sign-in page ("Forgot your password?") | n/a: only on request |
| Account invitation | An administrator ticks "Email the link to this address" when inviting | n/a: only when ticked |
| Bot alerts | A bot crashes or stops reporting, or a deployment or backup fails (exactly the events that already post to Discord; the per-bot crash, deploy and backup switches apply, and "stopped reporting" follows the bot's heartbeat settings) | Yes: Settings → Profile → Email |
| Security notices | Password changed (also by reset link), two-step sign-in turned on or off, email address change requested or completed (sent to the old address) | No: a person must be able to learn their account changed |
| Email verification link | A new account registers from an invitation, someone presses "Send verification link" in Settings → Profile, or asks to change their address (sent to the new address) | n/a: only on request; at most one a minute and five an hour per account |

| Announcements | An administrator writes one in Administration → Announcements | Notices: no. News: yes (Settings → Profile → "Email me news and announcements") |
| Notifications | Support ticket replies and status changes, access changes (sharing, transfers, workspaces, accepted invitations), node offline/online for accounts that manage nodes | Yes: Settings → Notifications, per category |

Bot alerts and notifications are emailed **only to verified addresses** and
follow Settings → Notifications per category; alerts, deployments, backups and
node notices also need the profile's Alert emails switch. The same events
appear in the in-panel notification inbox (`docs/support.md`).

Alert emails go to the bot's **owner**, at the address they sign in with, in
addition to their Discord webhook if they have one.

## Announcements (news and policy updates)

**Administration → Announcements** lets an administrator email custom HTML to the
people with accounts: news, policy updates, maintenance notices.

1. Write the subject and the message as HTML. The preview beside it is a
   sandboxed frame with scripts off. **Insert an example** gives a starting point.
2. Choose the **kind**: a *notice* (policy, service, security) goes to everyone in
   the audience whatever their preferences; *news* skips accounts that turned
   news emails off in their profile.
3. Choose the **audience**: every enabled account, or administrators only. The
   page shows how many people each choice reaches.
4. **Send test to me** emails the exact message to you only, marked `[Test]`.
   Sending to everyone stays locked until you have sent a test of the current
   text (editing anything locks it again).
5. **Send**: a confirmation names the subject, kind and audience.

**Send by** chooses email, the notification inbox (the bell; stored as plain
text, follows each account's Announcements preference) or both. Without
Mailgun, only the inbox is available and no test email is needed.

How it behaves: messages go out in batches of up to 100 through Mailgun's
recipient variables, so **each person sees only their own address**. Every
message gets the standard layout, a plain-text alternative made from your HTML,
and a footer saying why they received it (news adds how to opt out). Scripts,
frames, forms, event-handler attributes and `javascript:` links are stripped
before sending; that is hygiene, not a security boundary, because only
administrators can send announcements and email apps do not run scripts anyway.
The HTML is limited to 80 KB, one announcement can reach at most 1,000 accounts,
only one runs at a time, and a second one is refused for a minute after the
first (so a double click cannot send twice; tests are exempt). The activity
record notes who sent what subject to which audience, never the body. Nothing is
stored afterwards: no draft, no history, no queue. Opening the page costs nothing
while idle.

Limits: there is no unsubscribe link in the message itself (people opt out of
news in their profile), no scheduling, drafts, attachments or open/click
tracking, and no per-person or per-workspace targeting. Whether you may send news
to your users at all, and what your notices must contain, is your legal
responsibility, not something the panel decides.

## Set it up

1. In Mailgun, add and **verify a sending domain** (SPF and DKIM DNS records).
   A *sandbox* domain works for a first test but only delivers to recipients you
   authorize in the Mailgun dashboard, so it cannot send password resets to
   anyone else. Note which **region** the domain is in (US or EU).
2. Create an API key. A private API key works; a **domain sending key** limited
   to the one domain is the safer choice for production.
3. In the panel: **Administration → Panel settings → Email (Mailgun)**. Enter the
   key, the domain, the region and a sender such as
   `RivetPanel <noreply@mg.example.com>` (an address on the sending domain), then
   **Save and apply**.
4. Press **Send test**. It first checks the key, region and domain (read-only)
   and tells you whether the domain's DNS records are verified, then sends one
   message. A wrong key or region is explained in words.
5. Set the **panel address** in the same page: reset and invitation links point
   at it. "Forgot your password?" appears on the sign-in page only when both
   email and the panel address are set.

The same values can be fixed in the environment file (they then show as
read-only in the panel and always win): `RIVET_MAILGUN_API_KEY`,
`RIVET_MAILGUN_DOMAIN`, `RIVET_MAILGUN_REGION` (`us` or `eu`),
`RIVET_MAIL_FROM`.

## Security properties

- **The key is a credential.** It is sealed with the panel's encryption keys
  (like OAuth secrets), never returned by the API, never logged, and never
  included in an error message. Enforced by the application.
- **Password reset cannot be used to find accounts.** The request always answers
  the same way, whether or not the address has an account; disabled accounts get
  no link. Enforced by the application.
- Reset links are 256-bit random, stored only as a SHA-256 hash, work **once**,
  expire after **60 minutes**, and a new request replaces the old link (at most
  one per account per minute). The token travels in the URL **fragment**
  (`/reset#…`), so it is not sent to the server by the browser and does not
  appear in reverse-proxy logs. A password that is too weak is refused *before*
  the link is used up.
- Email verification links (`/verify-email#…`) are 256-bit random, stored
  only as a SHA-256 hash, work **once**, expire after **24 hours**, and a new
  link replaces the old one. Sending is limited to one per account per minute
  and five per hour; confirming is limited per client address. An email change
  happens only when the link sent to the new address is used, and the old
  address is told. Without email, people cannot verify themselves: an account
  manager marks addresses verified on the Users page. An account manager
  changing someone's address there makes it unverified and tells the old
  address. Enforced by the
  application. See [permissions.md](permissions.md).
- Resetting a password **signs the account out everywhere**. Two-step sign-in,
  if enabled, still applies at the next sign-in: a reset does not bypass it.
- Requests are rate limited per client address (10 per 10 minutes for the reset
  endpoints), and sending is capped at 20 emails per recipient and 300 in total
  per hour so a loop or abuse cannot drain the Mailgun account. These are
  application-level limits, not delivery guarantees.
- The Mailgun domain, sender and recipients are validated and cannot contain
  line breaks, so a value cannot inject headers.

## Memory and idle cost

The panel's idle footprint does not change: email uses the Go standard library
only (no SDK), keeps **no queue, connection pool or background goroutine**, and
reads its settings from SQLite when a message is sent. A message is sent from a
goroutine that exists only for that one HTTPS request, at most two at a time;
if Mailgun is slow and both slots are busy, further messages are dropped and
logged rather than buffered. Measured idle RSS with and without Mailgun
configured is in `footprint.md`.

## Limits and what it does not do

- Delivery depends on Mailgun and on your domain's DNS reputation. A message
  that Mailgun accepted can still be bounced or filtered later; the panel does
  **not** receive Mailgun webhooks, so bounces, complaints and unsubscribes are
  not tracked or acted on. Check the Mailgun dashboard.
- Alert and notice emails are sent in the background and are not retried. A
  failure is logged (kind only, never the address or key).
- Announcements are sent in one pass while you wait, so a very large batch or a
  slow Mailgun makes the request take a while; failures are counted and the first
  error is shown. Mailgun accepting a batch does not mean delivery.
- Invitation emails go only to the address typed on the invitation.
- There is one sender and one language; messages are plain text with a simple
  HTML alternative. Mailgun templates, attachments, inbound mail and
  per-workspace senders are not supported.
- Email is not a second factor: two-step sign-in remains TOTP.

## Troubleshooting

| Test says | Meaning |
| --- | --- |
| Mailgun rejected the API key | Wrong key, or the key belongs to a different region. Try the other region. |
| Mailgun does not know that domain in this region | Domain typo, or wrong region (an EU domain is invisible to the US API). |
| Mailgun refused the message … (HTTP 400) | Usually a sender not on the sending domain, or a sandbox domain with an unauthorized recipient. |
| Sent, but "DNS records are not all verified" | Finish the SPF/DKIM records in Mailgun; mail may reach spam until then. |
| Sent, but nothing arrives | Check the Mailgun logs (accepted vs delivered), spam folders, and that the domain is not a sandbox. |
