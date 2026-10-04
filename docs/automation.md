# Automation API

A small HTTP API for scripts and CI. The full contract is the OpenAPI document
served by the panel at `/api/v1/automation/openapi.yaml` (source:
`internal/api/openapi.yaml`).

## Tokens

Create one in **Settings → SFTP and API keys → Automation tokens**:

- **Actions**: `read` (status and history), `power` (start, stop, restart),
  `deploy` (GitHub deployment), `backup` (create backups).
- **Bots**: all bots you can access, now and later, or a chosen list (up to 50).
- **Expiry**: 7 days to 1 year. Revoke at any time; it stops working on the
  next request.

A token never grants more than its owner holds *at the time of the request*:
if your access to a bot is reduced, the token loses it too. Bots outside the
token's list answer `404`. Only the SHA-256 of a token is stored; the plaintext
is shown once.

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

- Lifecycle, deploy and backup requests answer `202`: the work continues in the
  background. Poll the bot's `phase` or its operations.
- `409` means another operation holds the bot (a deployment, restore or
  backup), a capacity budget is full, or the same Idempotency-Key is still
  running.
- `X-Request-ID` is on every response (your own value is echoed when it is
  8-64 characters of `A-Za-z0-9._-`).
- Rate limit: 120 requests per minute per token (`429`).
- Changes made through the API appear in the bot's activity record as
  "token: <name>".
- The session cookie does not work here, and these tokens do not work on the
  browser API or for SFTP.
