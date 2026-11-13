---
title: Automation API
lede: Let scripts and CI pipelines check, restart, deploy and back up your bots with scoped tokens that never outrank you.
icon: code
aliases: [automation]
glance:
  - 120 requests per minute per token
  - Tokens expire after 1 to 366 days
  - Up to 50 chosen bots per token
  - Idempotency keys replay for 24 hours
related: [apps/deploy-from-github, operate/backups, teams/sign-in]
---

## What it is for

The Automation API is a small HTTP API for scripts and CI. It covers four
actions on bots: reading status and history, power, GitHub deployment and
backups. The full contract is the OpenAPI document at
[`/api/v1/automation/openapi.yaml`](/api/v1/automation/openapi.yaml).

For the whole API rather than these four actions, use an API client instead;
see [Sign-in](../06-teams/03-sign-in.md).

## Create a token

1. Open **Settings → SFTP and API keys → Automation tokens**.
2. Name the token and choose its **actions**:

   | Action | Allows |
   | --- | --- |
   | `read` | Status and operation history |
   | `power` | Start, stop, restart |
   | `deploy` | GitHub deployment of the linked branch or one commit |
   | `backup` | Creating backups |

3. Choose its **bots**: every bot you can access, now and later, or a chosen
   list of up to 50.
4. Choose when it **expires**: 7, 30, 90 or 180 days, or 1 year. The API
   accepts 1 to 366 days.
5. Copy the token (it starts with `bpa_`). It is shown once; keep it in a
   secret store.

Each account can hold 20 tokens. The list shows when each was last used, and
**Revoke** stops a token on its next request.

### What a token can reach

- A token never grants more than its owner holds **at the time of the
  request**: if your access to a bot is reduced, the token loses it too.
  [enforced: panel]
- Bots outside the token's list answer `404`. [enforced: panel]
- Only the SHA-256 of a token is stored. [enforced: panel]
- The session cookie does not work on this API, and these tokens do not work
  on the browser API. SFTP keys never work here, and tokens never work for
  SFTP. [enforced: panel]

## Examples

```sh
export RIVET=https://panel.example.com
export RIVET_TOKEN=bpa_...

# List bots and their state
curl -s -H "Authorization: Bearer $RIVET_TOKEN" $RIVET/api/v1/automation/bots

# Restart a bot; the same Idempotency-Key within 24 h replays the first answer
curl -s -X POST -H "Authorization: Bearer $RIVET_TOKEN" \
  -H "Idempotency-Key: $CI_PIPELINE_ID-restart" \
  $RIVET/api/v1/automation/bots/$BOT_ID/restart

# Deploy the linked branch, or one commit
curl -s -X POST -H "Authorization: Bearer $RIVET_TOKEN" -H "Content-Type: application/json" \
  -d '{"sha":"'"$GITHUB_SHA"'"}' $RIVET/api/v1/automation/bots/$BOT_ID/deploy

# Follow the deployment
curl -s -H "Authorization: Bearer $RIVET_TOKEN" \
  "$RIVET/api/v1/automation/bots/$BOT_ID/operations?kind=deploy,rollback&limit=1"

# Back up before a risky change
curl -s -X POST -H "Authorization: Bearer $RIVET_TOKEN" -H "Content-Type: application/json" \
  -d '{"label":"before release"}' $RIVET/api/v1/automation/bots/$BOT_ID/backups
```

### GitHub Actions

Store the token as the repository secret `RIVET_TOKEN` and the bot's id as
the variable `BOT_ID`:

```yaml
- name: Deploy to RivetPanel
  env:
    RIVET_TOKEN: ${{ secrets.RIVET_TOKEN }}
  run: |
    curl -sf -X POST -H "Authorization: Bearer $RIVET_TOKEN" \
      -H "Idempotency-Key: ${{ github.run_id }}-${{ github.run_attempt }}" \
      -H "Content-Type: application/json" -d '{"sha":"${{ github.sha }}"}' \
      https://panel.example.com/api/v1/automation/bots/${{ vars.BOT_ID }}/deploy
```

## Behaviour

| You see | It means |
| --- | --- |
| `202` | A lifecycle, deploy or backup request was accepted and continues in the background. Poll the bot's `phase` or its operations |
| `409` | Another operation holds the bot (a deployment, restore or backup), a capacity budget is full, or the same `Idempotency-Key` is still running |
| `429` | The rate limit of 120 requests per minute per token was reached [enforced: panel] |
| `404` | The bot is outside the token's list |

- Send an `Idempotency-Key` header on every changing request, so a CI retry
  does not do the work twice: the same key within 24 hours replays the first
  answer.
- Every response carries `X-Request-ID`. Your own value is echoed when it is
  8–64 characters of `A-Za-z0-9._-`.
- Changes made through the API appear in the bot's activity record as
  "token: <name>".
