---
title: AI assistant
lede: Ask one private chat about whatever you are looking at, and let it read logs and files, propose fixes and run checks, with every change waiting for your approval.
icon: sparkle
aliases: [ai]
glance:
  - 12 rounds and 20 minutes per run
  - Chats kept 90 days after last use
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

| Tool | Reads or changes | Needs |
| --- | --- | --- |
| `target_status` | State, last exit code, last error, startup command | Access to the bot or site |
| `read_logs` | The last 20–500 lines of console output, secrets redacted | Console permission |
| `build_output` | Recent builds, deployments and backups, and the tail of one operation's output | Console permission |
| `list_files`, `read_file`, `search_files` | Permitted workspace text | Access |
| `environment_names` | Variable names only | Access |
| `list_targets`, `focus_target` | The bots and sites you can open; picks a target for a run that has none | Access |
| `web_search`, `web_fetch` | Public research | An administrator enabled it |
| `request_environment_values` | A secure form; the values bypass the model | Environment permission and your input |
| `propose_file_change` | One text file, with a diff and Undo | File permission and approval |
| `run_diagnostic` | One allowed command in an offline container | File permission and approval |
| `restart_bot` | Restart | Power permission and approval |

`read_logs` and `build_output` are what make debugging work: the assistant
sees the crash, not just the files. Its instructions give it a method (status,
console output, build output, then files), rules for secrets and untrusted
text, what it cannot do, and the current context.

It has **no** tools for the startup command, deployments, site publication,
GitHub pushes, kill, accounts and access, keys, two-step sign-in, domains,
resource or network policy, backups and restores, deletion or revealing
secrets. Neither mode can do those. [enforced: panel]

## Approval and Auto repair

**Ask first** is the default mode. Status, permitted files, variable
names and web research are read without asking. A diagnostic, a file change,
a restart and secure environment input each stop on a review card.

**Auto repair** is available while a bot or site is open. It first shows its
envelope (the target, the tools it covers and the run limits) and needs one
approval. It then covers diagnostics, file changes and restarts; secure
environment input still waits for you.

The target of a run is fixed when it starts. With no bot or site in view, the
assistant answers general questions and can use `list_targets` and
`focus_target` to look at one of your bots in Ask first mode. A run with a
target cannot move to another, and Auto repair always needs a target, so an
approval covers exactly one bot or site. [enforced: panel]

At most one changing run per target, two active runs per person and four on
the whole panel run at once. [enforced: panel]

### Run limits

Every run, in both modes, counts against these limits. Approving a card never
raises them; a call over a limit is refused and the model is told to
summarize and stop. [enforced: panel] The limits are stored with each run and
shown on the Auto repair envelope.

| Limit | Default | Counts |
| --- | --- | --- |
| Rounds | 12 | Model and tool rounds |
| Wall time | 20 minutes | From queueing to finish |
| Diagnostics | 5 | Diagnostic jobs started |
| Apply attempts | 3 | File changes the panel tried to apply, conflicts included |
| Restarts | 2 | Bot restarts |
| Changed files | 32 | Distinct files changed by applied change sets |
| Changed text | 8 MiB | Written text (deleted text for deletions) |
| Retained output | 2 MiB | Stored tool output; a result that crosses it is cut short and later tools fail |

A read-only tool is stopped after 90 seconds, for example when a remote node
does not answer; the model and you get a "did not finish in time" error and
the run continues. Tools waiting for approval are bounded by the wall time.
When a run fails, is cancelled or is interrupted by a restart, its open tool
calls are closed as failed (or cancelled, when they waited for approval) and
never shown as running afterwards. Runs active during a panel restart are
marked interrupted and can be retried from the chat.

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
  tool output, variable **names** and public research. It never receives
  variable values, provider or search keys, authentication headers, protected
  credential files, hidden reasoning, panel metadata or whole workspace
  archives. Values typed into a secure input card go straight to the
  environment service, bypassing the provider and the transcript.
  [enforced: panel]
- Every tool re-checks your account and target permission right before it
  runs, and again after an approval card is decided. [enforced: panel]

### Activity log

Every model-initiated action is recorded with the person who started the run,
labelled "via AI operator" (plus "(Auto)" for Auto repair), whether or not a
card was approved: `ai.file_apply` (path), `ai.diagnostic` (redacted
command), `ai.restart` and `ai.env_input` (variable names only). Refused and
failed attempts are recorded as `denied` or `failed`. Chat requests,
approvals, secure input and Undo are recorded too.

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
   output limits, temperature, timeouts and optional token prices (USD per
   million tokens).
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
