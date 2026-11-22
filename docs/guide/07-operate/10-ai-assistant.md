---
title: AI assistant
lede: Tell one private chat what you want and it does it with your permissions: deploy any repository, image or app, set variables, change settings, fix crashes and set up the panel.
icon: sparkle
aliases: [ai]
glance:
  - Deploys GitHub repos, Docker images, compose files and one-click apps
  - Ask first or Autopilot, remembered per browser
  - 40 rounds and 30 minutes per run
  - Works with any OpenAI-compatible provider
related: [apps/bots, apps/files, operate/remote-nodes, operate/security]
---

## Open the chat

Open it with the **Ask AI** (sparkle) button in the top bar of any page, or
press Ctrl+. (Ctrl and full stop). Ask in plain language, for example why a
bot is not starting.

It is one chat for the whole panel, not one per bot. Every message carries
what you were looking at: the bot or site, the tab or section in view, and in
the file editor the open file. A chip above the message box shows exactly what
is shared; dismiss it to leave the context out of a single question. Each sent
message keeps its chip in the transcript. Moving to another bot or page keeps
the same conversation and attaches the new context to the next message.

The assistant needs a provider that an administrator has set up. Hosting works
normally without one.

## What it can do

The assistant is an operator for the whole panel. Give it a task and it
works it through: it looks, acts, waits for the result, reads the console
and fixes what failed. For example:

- "Deploy github.com/acme/ticket-bot, my token is …": it analyzes the
  repository, creates the app with the right runtime, start and build
  commands, add-on databases and variables, pulls the code, starts it and
  checks the console until it runs.
- "Run `louislam/uptime-kuma` and give it a web address": it runs the image
  with its port published, or installs the matching one-click app.
- "Publish my portfolio repo as a site": it detects the framework (Vite,
  Astro, Next export, plain HTML or a server app) and publishes it.
- "Give my bot 1 GB and turn on daily backups", "stop everything in the
  Staging workspace", "switch me to dark mode with the teal accent".

### Knowing the panel

| Tool | Does | Needs |
| --- | --- | --- |
| `panel_docs` | Searches this guide (every page is built into the panel) and returns the best sections with their `/docs` links | Nothing |
| `read_doc` | Reads one guide page or one of its sections | Nothing |
| `panel_info` | Describes this panel now: version, enabled features and modules, your permissions, runtimes, add-on kinds, image policy, workspaces and capacity | Signed in |

Its instructions also carry a map of the panel's pages and an app's tabs, so
it links to the right place (for example the Variables tab of an app) and can
open it for you. Answers about how the panel works come from the guide, with a
link to the page.

### Deploying

| Tool | Does | Needs |
| --- | --- | --- |
| `analyze_repo` | Works out runtime, commands, variables, add-ons and ports of a GitHub repository (as an app or a site) | GitHub on this panel |
| `deploy_github` | Creates an app from a repository with auto-deploy, pulls it and starts it | Create permission |
| `deploy_site` | Creates and publishes a site from a repository | Site create permission |
| `deploy_image` | Runs a Docker image with published ports, variables and volumes | Container apps enabled |
| `deploy_compose` | Imports a compose file as a stack (with a dry run) | Container apps enabled |
| `list_app_templates`, `install_app_template` | Finds and installs one-click apps with generated passwords | Container apps enabled |
| `wait_for_ready` | Waits for a deploy or start to settle, then reports state and the last console lines | Access |
| `redeploy` | Deploys the latest commit of a linked repository | Configure permission |

### Managing what exists

| Tool | Does | Needs |
| --- | --- | --- |
| `set_environment`, `delete_environment` | Sets or removes variables, optionally restarting | Environment permission |
| `power` | Start, stop, restart or kill | Power permission |
| `update_bot_settings` | Name, start and build command, runtime, memory, CPU, network, restart policy, automatic backups | Configure permission |
| `console_command` | Sends one line to a running console | Console permission |
| `delete_bot` | Deletes an app and its files | Owner or administrator |
| `panel_routes`, `panel_api` | Calls any panel API route: backups, schedules, domains, sites, workspaces, members, add-ons, notifications, profile, administration | The same permission as the page |
| `client_action` | Sets your theme, accent colour or corner style in this browser, or opens a page | Nothing; it only changes your view |
| `show_ui` | Shows an interactive view in the chat: choices, a short form, a plan to confirm, status metrics ([Interactive views](#interactive-views)) | Nothing; a button press is your next message |

### Looking and fixing

| Tool | Reads or changes | Needs |
| --- | --- | --- |
| `target_status` | State, last exit code, last error, startup command | Access to the bot or site |
| `read_logs` | The last 20–500 lines of console output, secrets redacted | Console permission |
| `build_output` | Recent builds, deployments and backups, and the tail of one operation's output | Console permission |
| `list_files`, `read_file`, `search_files` | Permitted workspace text | Access |
| `environment_names` | Variable names only | Access |
| `list_targets`, `focus_target` | Lists your bots and sites and points the file and log tools at one; it can switch at any time | Access |
| `web_search`, `web_fetch` | Public research | An administrator enabled it |
| `request_environment_values` | A secure form for a secret you have not given it; the values bypass the model | Environment permission and your input |
| `propose_file_change` | One text file, with a diff and Undo | File permission |
| `run_diagnostic` | One allowed command in an offline container | File permission |
| `restart_bot` | Restart | Power permission |

**Permissions.** Every tool, and every `panel_api` call, runs as you: the same
role permissions, per-bot grants, validation and activity log as the page
would. The assistant can never do more than you can. [enforced: panel]
`panel_api` calls the panel's own routes in-process with a one-request token
that never leaves the server.

**Out of reach by design.** Signing in and out, passwords, two-step sign-in,
passkeys, sessions, API keys and tokens, linked sign-in providers, your email
address, account deletion and the assistant's own approvals. [enforced: panel]
Streaming routes (consoles, terminals, live events) are not callable; it uses
`read_logs` instead.

## Interactive views

When choosing or filling something in beats typing, the assistant shows a
small interface in the chat instead of prose: a deploy plan with a name field,
a runtime choice, a memory slider and a **Deploy** button; a status board of
your apps with metrics and badges; options to pick from. It is drawn with the
panel's own controls, in your theme.

- **Buttons answer the assistant.** Pressing one sends your choice and the
  values in the view as your next message; the transcript shows it as a chip
  with the button's label and the values. Buttons wait while a run is working,
  and a button whose checks fail (an empty required field) stays disabled.
- **Views update in place.** When the assistant changes a view you can see
  (after a restart, say), the view moves into the newest turn and its parts
  glide to their new values. With reduced motion turned on, updates are
  instant.
- **Reloads keep them.** Views are rebuilt from the chat history. Values you
  edited but did not send are not kept.

The views follow [A2UI](https://a2ui.org) v1.0, an open protocol for agent
interfaces. The catalog is the A2UI basic catalog (Text, Image, Icon, Row,
Column, List, Card, Tabs, Modal, Divider, Button, TextField, CheckBox,
ChoicePicker, Slider, DateTimeInput) plus Heading, Metric, Badge, Code and
Link.

| Limit | Value |
| --- | --- |
| Messages per `show_ui` call | 20 |
| Components per view | 120 |
| Views kept in one chat | 24 |
| Data per update | 32 KiB |
| Values sent by one button | 8 KiB |

**Checked by the server.** Every view is validated before you see it: only
catalog components and functions, no Video or Audio, images only from the
panel's own addresses, links only to panel pages or `https://` addresses, and
no hidden password fields (secrets go through the secure input card). A broken
view goes back to the model as an error to fix. A button press is accepted
only for a button the chat really showed, with the event name it carries; the
server writes the message and its time itself. [enforced: panel] The browser
repeats the address checks before it draws an image or opens a link.
[enforced: browser]

## Ask first and Autopilot

Pick the mode with the switch above the message box. The choice is
remembered in this browser.

**Ask first** is the default. Status, logs, files, variable names, the API
catalog and web research are read without asking. Every change (a deploy, a
variable, a setting, a file, a restart, a `panel_api` write) stops on a review
card that shows what it will do; a rejection means it did not happen.

**Autopilot** does the whole job without stopping to ask, on any bot, site or
page, and needs nothing open. You chose it, so there is no extra confirmation
step. A secure input card for a secret you have not given it still waits for
you.

At most one changing run per target, two active runs per person and four on
the whole panel run at once. [enforced: panel]

### Secrets

Values you type in the chat, such as a token for a deploy, are set as
environment variables. In the stored tool records, variables whose names look
secret (tokens, keys, passwords, URLs) show as `[hidden]`; console output and
file reads have configured values redacted. The panel never sends stored
variable values to the model. [enforced: panel]

For a secret you have not given it, the assistant opens a secure input card.
Type the value there, or set it on the app's Environment page: either way the
card completes and the run goes on. The assistant reads environment templates
(`.env.example`, `.env.sample`, `.env.template`, `.env.dist`) to learn which
variables an app needs; real `.env` files are protected. [enforced: panel]

### Run limits

Every run, in both modes, counts against these limits. Approving a card never
raises them; a call over a limit is refused and the model is told to
summarize what it did and what is left. Send another message to continue.
[enforced: panel]

| Limit | Default | Counts |
| --- | --- | --- |
| Rounds | 40 | Model and tool rounds |
| Wall time | 30 minutes | From queueing to finish |
| Panel actions | 40 | Deploys, variables, power, settings, console lines and `panel_api` writes |
| Diagnostics | 12 | Diagnostic jobs started |
| Apply attempts | 24 | File changes the panel tried to apply, conflicts included |
| Restarts | 8 | `restart_bot` calls |
| Changed files | 48 | Distinct files changed by applied change sets |
| Changed text | 8 MiB | Written text (deleted text for deletions) |
| Retained output | 4 MiB | Stored tool output; a result that crosses it is cut short and later tools fail |

A read-only tool is stopped after 90 seconds, for example when a remote node
does not answer; the model and you get a "did not finish in time" error and
the run continues. `wait_for_ready` waits up to 5 minutes. Tools waiting for
approval are bounded by the wall time. When a run fails, is cancelled or is
interrupted by a restart, its open tool calls are closed as failed (or
cancelled, when they waited for approval). Runs active during a panel restart
are marked interrupted and can be retried from the chat.

When a run created or changed something, the page behind the chat reloads
its data, and a button under the run opens what it deployed.

## File changes and Undo

- Changes are limited to 1 MiB of text per file and stored as compressed
  before and after snapshots with a bounded diff. The diff you review has
  configured secret values redacted; Undo restores the exact snapshot.
- Applying checks the original revision, stages the complete set and uses the
  crash journal for recoverable renames. A concurrent editor, SFTP write or
  deployment causes a conflict instead of being overwritten.
  [enforced: panel]
- Undo works only while the file still matches the revision the change left.

## Diagnostics

A diagnostic runs one allowed command, such as a syntax check, the tests or
the bot's entry file, in a throwaway offline copy of the files.

- Allowed commands come from the runtime's `diagnostic_commands` prefixes in
  `runtimes/*.yaml`, controlled by administrators. The defaults cover version
  checks, syntax checks, the test, lint and build scripts, and running the
  entry file (`node index.js`, `python main.py`, …) so start-up, import and
  syntax errors surface. `python3` is treated as `python`.
- A refused command returns the full list of allowed ones and points the model
  at `read_file`, `read_logs` and `build_output`, so it corrects itself
  instead of retrying a shell command.
- Arguments are passed directly. Shell syntax, interpreter evaluation
  (`node -e`, `node -p`, `python -c`, `ruby -e`) and paths outside
  `/workspace` are rejected. [enforced: panel]
- The container sees only a private, safe snapshot of the files: no bot
  environment, ports, Docker socket, devices or extra mounts. It runs non-root
  with a read-only root, dropped capabilities, `no-new-privileges`, PID, CPU,
  memory and tmpfs limits, and no network. [enforced: docker]
- The snapshot skips symbolic links, so tools that rely on
  `node_modules/.bin` links must be run through a script that calls them by
  path. It is capped at 60,000 files and 512 MiB.
- The default timeout is 10 minutes and the hard ceiling 20.

For a bot on the panel's host, diagnostics need the local Docker runner. For
a bot on a remote node, diagnostics and file tools run on that node; see
[Remote nodes](04-remote-nodes.md#ai-diagnostics).

## Privacy and data

- **Chats are private** to the person who started them, including from
  administrators. Chats that older per-bot and per-site endpoints created keep
  working and keep their target; administrators can still inspect those.
- Chats are listed under the history button and kept for 90 days after the
  last activity, or until you delete them. Deleting removes messages, tool
  output and undo snapshots; activity-log entries remain.
- Reloading restores the active run, pending approvals, secure-input cards,
  change sets and Undo.
- The browser describes the page, but the server trusts only the bot or site
  **id**. It checks that you may open it, replaces any browser-supplied name
  with the real one, and clips everything else to short plain text. A bot you
  cannot open cannot be named as context. [enforced: panel]
- The model may receive bounded file snippets it asked for, status, redacted
  tool output, variable **names**, API responses it asked for (redacted) and
  public research. The panel never sends it stored variable values, provider or search keys, authentication headers, protected
  credential files, hidden reasoning or whole workspace archives. Values typed into a secure input card go straight to the
  environment service, bypassing the provider and the transcript.
  [enforced: panel]
- Every tool re-checks your account and target permission right before it
  runs, and again after an approval card is decided. [enforced: panel]

### Activity log

Every model-initiated action is recorded with the person who started the run,
labelled "via AI operator" (plus "(Auto)" for Autopilot): `ai.deploy_github`,
`ai.deploy_site`, `ai.deploy_image`, `ai.deploy_compose`, `ai.install_app`,
`ai.deploy`, `ai.env_set` and `ai.env_delete` (variable names only),
`ai.start`, `ai.stop`, `ai.restart`, `ai.kill`, `ai.settings`, `ai.console`,
`ai.delete`, `ai.api` (method and path), `ai.file_apply` (path),
`ai.diagnostic` (redacted command) and `ai.env_input`. `panel_api` writes are
also recorded under the route's own action, as if you had clicked it. Refused
and failed attempts are recorded as `denied` or `failed`.

## Web research

When an administrator has configured search, the assistant can search and
fetch public pages.

- Page fetches must be public. The address of every actual connection,
  redirects included, is checked after DNS resolution, which also defeats DNS
  rebinding: loopback, private, link-local, carrier-grade NAT
  (`100.64.0.0/10`), multicast, reserved, NAT64/6to4 and cloud-metadata
  addresses are refused, as are URLs with credentials and non-HTTP URLs.
  [enforced: panel]
- Page fetches always connect directly, never through an HTTP proxy, because
  a proxy would resolve the name itself.
- Fetched pages are stripped of scripts, navigation and forms.

## Providers and search

*For administrators.*

Add a provider in the optional **AI operator** step of `/setup`, or later
under **Administration → Panel settings**, which needs the `ai.manage` or
`settings.manage` permission. Providers use OpenAI-compatible Chat
Completions; DeepSeek is the first preset.

1. Start from a preset: DeepSeek, OpenAI, OpenRouter or a custom endpoint.
   Required fields are marked; tuning, pricing and paths are folded under
   **Optional**.
2. A profile holds the base URL, chat and models paths, model, context and
   output limits, temperature, timeout and optional token prices (USD per
   million tokens). By default there is no output-token cap (the model
   answers at full length; the panel sends no `max_tokens`) and the timeout
   is 5 minutes per request.
3. **Discover models** queries the provider's models path with the saved key.
   It is optional: you can type any model ID.
4. **Test capabilities** sends no project data. It checks authentication,
   streaming, usage reporting and a harmless tool call.

Several providers can be stored, edited, re-keyed, enabled, disabled and
deleted. Keys are sealed with AES-256-GCM and never shown again: every API
response exposes only `key_set`. [enforced: panel] Never put provider or
search keys in bot environment variables.

Before every provider request, the chat history is normalized for strict
OpenAI-compatible providers such as DeepSeek: every message carries
`content` (an empty string for an assistant turn that only calls tools), an
empty tool result is sent as "(the tool returned no output)", every tool call
has an id and exactly one result right after it (a synthesized failure when it
never produced one), and orphan tool results and empty turns are dropped.

### Search

Web research is optional as a whole. It uses a SearxNG or Risa endpoint
(`GET <base>/search?format=json`) with rotating `X-API-Key` credentials, and
**Test search** checks the saved settings.

- The search origin is trusted by the administrator and may be a private
  address, such as a self-hosted SearxNG; cloud-metadata addresses are still
  refused. [enforced: panel]
- The search client never follows a redirect to another origin, so keys go
  only to the configured origin. [enforced: panel]
- Search requests honour `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`.
- A key the service refuses (`401` or `403`) is skipped in favour of the next
  one.

## Repository analysis

**Analyze repository** in the new-bot flow can ask the default enabled
provider (the one marked default, else the first enabled one with a key) for
a deployment plan when **Refine with AI** is ticked. It is one completion
without tools, at temperature 0.1.

- The prompt holds the runtime and service catalog, the repository's file list
  (300 paths), detected variable names, about 60 KB of manifests, example
  configuration, entry files and README, and the deterministic plan.
  Repository text is marked as untrusted data.
- The reply must be JSON. It is parsed, merged with required variables the
  detection found, and validated like manual input: allowed start commands,
  known services, valid variable names, no prefilled secrets, resource
  limits. [enforced: panel]
- Nothing is created or run until you create the bot. The request is logged
  with the provider, model and token counts; it is not stored as a chat.
- Verified recipes for well-known bots skip the AI.

## API

`GET /api/v1/ai/conversations/:id/runs` returns the latest runs (5 by
default, at most 20 with `?limit=`) with their tool calls, change sets, target
and start event. Runs whose bot or site you can no longer open are left out.
Tool-call rows have their own UUIDs; the provider's call id is kept separately
and only echoed back to the provider. Live event streams of finished runs are
dropped after five minutes.
