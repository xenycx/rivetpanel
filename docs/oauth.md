# Sign-in with GitHub and Discord

Generic OpenID Connect providers (Keycloak, Okta, Entra ID, Google...) are
configured in the panel instead; see `auth.md`.

OAuth is optional. Password sign-in always works; each provider is enabled
only when you give it a client ID and secret. Anyone self-hosting RivetPanel
registers **their own** OAuth apps: credentials are never shared between
installations.

## How it behaves

- **Login:** a linked GitHub/Discord identity signs in to its RivetPanel account.
- **Linking:** signed-in users connect providers in **Settings → Connected accounts**.
- **No merging by email.** An OAuth login never attaches to an existing account
  because the emails match. Sign in with your password first, then connect.
- **Signup is off by default.** Only administrator-created users can sign in.
  Set `RIVET_OAUTH_ALLOW_SIGNUP=1` to let unknown identities with a
  *verified* email create a normal-user account.
- **Last sign-in method:** a provider cannot be disconnected if it is the
  account's only way to sign in (a password counts as one).
- **Storage:** the GitHub access token and the Discord webhook URL are
  AES-256-GCM sealed with your encryption key (`RIVET_KEY_DIR`). Discord's
  access token is not stored. Encryption does not hide them from someone with
  root on the host.
- **Requested scopes:** GitHub `read:user user:email` for sign-in; the `repo`
  scope only when the user clicks *Grant repository access* (needed to deploy
  private repositories and to let the panel add the push webhook). Discord
  `identify email`, plus `webhook.incoming` only when the user clicks
  *Enable notifications*.
- **Discord notifications:** the webhook the user picks receives a message when a
  deployment succeeds or fails and when a bot needs attention (crashed with no
  restarts left). Crash alerts are throttled to one per bot per 10 minutes.
  Only Discord's own webhook URLs are ever called.

## 1. Decide the public URL

`RIVET_PUBLIC_URL` is the exact origin users open in the browser: scheme and
host (and port if non-default), **no path, no trailing slash**. It must be
`https://` (plain `http://` is accepted only for `localhost` / loopback).
The two callback URLs are derived from it:

```
<PUBLIC_URL>/api/v1/auth/github/callback
<PUBLIC_URL>/api/v1/auth/discord/callback
```

On start, RivetPanel logs the exact `redirect_uri` for each enabled provider.
Whatever you register with the provider must match it character for character.

## 2. Register the GitHub OAuth App

Use an **OAuth App** (not a GitHub App).

1. GitHub → your avatar → **Settings → Developer settings → OAuth Apps → New OAuth App**
   (for an organization: the organization's *Settings → Developer settings*).
2. Fill in:
   | Field | Value (example for `panel.xenyc.ge`) |
   |---|---|
   | Application name | `RivetPanel (panel.xenyc.ge)`, shown to users on the consent screen |
   | Homepage URL | `https://panel.xenyc.ge` (your `RIVET_PUBLIC_URL`) |
   | Application description | optional, e.g. `Discord bot hosting panel` |
   | Authorization callback URL | `https://panel.xenyc.ge/api/v1/auth/github/callback` |
   | Enable Device Flow | leave **unchecked** |
3. **Register application**.
4. Copy the **Client ID**. Click **Generate a new client secret** and copy it
   immediately; GitHub shows it once.

An OAuth App has a single callback URL. Register a separate app for local
development (`http://localhost:8080/...`) and for production.

## 3. Register the Discord application

1. https://discord.com/developers/applications → **New Application**. Name it
   `RivetPanel` (Discord rejects names containing "discord").
2. Open **OAuth2**. Copy the **Client ID**. Click **Reset Secret** and copy the
   **Client Secret** immediately.
3. Under **Redirects** click **Add Redirect**, enter
   `https://panel.xenyc.ge/api/v1/auth/discord/callback`, then **Save Changes**.
4. No bot user, intents, or permissions are needed.

## 4. Configure RivetPanel

In `/etc/rivetpanel/rivetpanel.env` (mode `0640`, group `rivetpanel`; it now holds secrets):

```sh
RIVET_PUBLIC_URL=https://panel.xenyc.ge
RIVET_GITHUB_CLIENT_ID=...
RIVET_GITHUB_CLIENT_SECRET=...
RIVET_DISCORD_CLIENT_ID=...
RIVET_DISCORD_CLIENT_SECRET=...
# RIVET_OAUTH_ALLOW_SIGNUP=1     # optional, see above
RIVET_PROXY_HEADER=CF-Connecting-IP   # only when behind Cloudflare/proxy on this host
```

Restart (`systemctl restart rivetpanel`) and check the journal for
`oauth provider enabled`. Startup fails with a clear message if only an ID or
only a secret is set, or if the public URL is malformed or not HTTPS.

## 5. First administrator

```sh
sudo -u rivetpanel rivetpanel create-admin you@example.com   # prompts for a password
```

Sign in with that password, open **Settings → Connected accounts**, and connect
GitHub and/or Discord. From then on the provider buttons on the login page work.

## Cloudflare (`panel.xenyc.ge`)

WebSockets (the live console) work through Cloudflare by default.

**Option A: Cloudflare Tunnel (recommended; no inbound ports).**
1. Cloudflare **Zero Trust → Networks → Tunnels → Create a tunnel** (Cloudflared),
   and install `cloudflared` on the server with the command it shows.
2. **Public hostname:** subdomain `panel`, domain `xenyc.ge`, service type `HTTP`,
   URL `localhost:8080`. Cloudflare creates the proxied DNS record for you.
3. Keep `RIVET_LISTEN=127.0.0.1:8080` and set `RIVET_PROXY_HEADER=CF-Connecting-IP`.

**Option B: DNS record + reverse proxy on the server.**
1. DNS → add `A` (and `AAAA`) record `panel` → server IP, proxied (orange cloud).
2. SSL/TLS mode **Full (strict)**, with a Cloudflare Origin Certificate or a
   Let's Encrypt certificate on the proxy (see `deployment.md` for Caddy).
3. Set `RIVET_PROXY_HEADER=CF-Connecting-IP`. Firewall the origin so only
   Cloudflare and you can reach it (otherwise anyone could send that header
   from another host on your network path; the panel trusts it only from
   loopback, so the proxy must run on the same machine).

## Troubleshooting

| Symptom | Cause |
|---|---|
| Provider page says `redirect_uri` mismatch / *Invalid OAuth2 redirect_uri* | Registered URL differs from the one logged at startup (scheme, host, port, trailing slash) |
| Login page shows no provider buttons | Its client ID *and* secret must both be set; check `oauth provider enabled` in the log |
| `state_invalid` after returning from the provider | Flow older than 10 minutes, reused, or the browser blocked the `rivetpanel_oauth` cookie; a restart also drops pending flows |
| `signup_disabled` | The identity is not linked to any account and `RIVET_OAUTH_ALLOW_SIGNUP` is off; link it from Settings |
| `email_in_use` | An account with the provider's verified email already exists; sign in with the password and connect from Settings |
| `email_unverified` | The provider shared no verified email (GitHub: make a primary email verified) |
| 429 on sign-in | Per-IP limit (30 per minute); behind a proxy set `RIVET_PROXY_HEADER` |

## Rotating a client secret

Generate the new secret at the provider, update `rivetpanel.env`, restart. Users
stay signed in; linked accounts are unaffected.
