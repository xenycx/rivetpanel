# Single sign-on, passkeys and API clients

This guide covers the identity features added after custom roles
(`permissions.md`): OpenID Connect sign-in, passkeys, and API clients. GitHub
and Discord sign-in are in `oauth.md`; the narrow automation API is in
`automation.md`.

## Single sign-on (OpenID Connect)

Administration → **Single sign-on** (permission `settings.manage`) adds
providers such as Keycloak, Authentik, Okta, Microsoft Entra ID or Google.

1. Set the panel address first (Administration → Panel settings): the
   callback URL is built from it, and sign-in buttons stay hidden without it.
2. Register a client at the provider with the redirect URI shown in the form:
   `https://<panel>/api/v1/auth/oidc/<url-name>/callback`.
3. Enter the issuer URL (the panel reads `/.well-known/openid-configuration`
   under it when you save, and refuses the provider if that fails), the client
   ID and, for a confidential client, the secret. The secret is sealed with the
   panel keyring (like other stored secrets) and never shown again; leave it
   empty for a public client. Scopes always include `openid`; ask for `email`.

The issuer must use `https`; plain `http` is accepted only for a provider on
the same machine (`localhost`, loopback addresses).

**What the panel checks.** Sign-in uses the authorization code flow with PKCE
(S256), a random `state` bound to the browser by a cookie, and a random
`nonce`. The ID token is verified with `github.com/coreos/go-oidc/v3`:
signature against the issuer's published keys, issuer, audience (the client
ID) and expiry; the nonce must match. Any failure ends on the sign-in page
with "could not verify" and is logged (never with secrets).

**How accounts are matched**, in this order:

1. The provider subject (`sub`) already linked to an account signs that
   account in.
2. Otherwise, when **Link to an existing account with the same address** is on,
   the provider says the address is verified (`email_verified: true`, also the
   string `"true"`) and the account holds no administration permission, the
   identity is linked to the account with that address and signs it in.
3. Otherwise, when an account with that address exists, sign-in is refused
   ("sign in another way, then connect the provider"): the owner signs in with
   a password or another method and links the provider under Settings →
   Connected accounts → Single sign-on. A link made from Settings joins the
   signed-in account whatever address the provider reports.
4. Otherwise, when **Create accounts for new people** is on, a passwordless
   account is created with the chosen role (the built-in User role or a custom
   role; never Administrator). Its address counts as verified only when the
   provider said so; otherwise the unverified-email policy applies until the
   person confirms it.

Two-step sign-in still applies: an account with TOTP enabled enters its code
(or uses a passkey) after the provider. Disabled accounts cannot sign in.

**Delegated administrators** with `settings.manage` but not built-in
administration can only choose a role whose permissions they hold (for the
User role, every resource permission), cannot edit a provider that already
hands out more, and a role named by a provider cannot be deleted.

Removing a provider removes every link through it. Accounts whose only sign-in
method it was keep existing; an account manager can send a password reset.
An account cannot unlink its last sign-in method.

Enforcement: application layer (panel). The identity provider's own policies
(MFA, groups) are not read; group-to-role mapping is not implemented.

## Passkeys

Settings → Security → **Passkeys** registers WebAuthn passkeys
(`github.com/go-webauthn/webauthn`). Adding one needs the current password (or,
without one, a sign-in within the last 10 minutes); rename and remove are on
the same page. The panel stores only the public key, sign counter and flags.

* **Sign in with a passkey** on the sign-in page needs no email or password:
  the browser offers the passkeys for this panel (discoverable credentials,
  user verification required). A passkey with user verification is already two
  factors, so no TOTP code follows.
* **As the second step**: with two-step sign-in on, "Use a passkey instead"
  replaces the code; only a passkey of the account that passed the first step
  is accepted.
* The relying party is the panel address: its host name is the RP ID and its
  origin the only accepted origin. Passkeys are unavailable until the address
  is set, and with an IP address (browsers refuse those as relying parties).
  Changing the panel's host name makes existing passkeys unusable.
* A counter that goes backwards (a possibly cloned authenticator) is refused.
* Removing the last sign-in method is refused.

Enforcement: application layer (signature, challenge, origin, RP ID, user
verification and counter checks); the private key never leaves the
authenticator.

## API clients

Settings → SFTP and API keys → **API clients** (permission
`api_keys.manage`) creates bearer tokens (`rvc_...`) for programs that need the
same API the panel uses, beyond the narrow automation API.

```sh
curl -H "Authorization: Bearer $RIVET_CLIENT" https://panel.example.com/api/v1/bots
```

* **Permissions**: choose names from the role catalog (`permissions.md`). Only
  permissions the creator holds right now can be chosen (the same
  no-escalation rule as granting roles), and every request intersects the
  client's list with the creator's *current* role, so a role change applies to
  its clients immediately.
* **Never an administrator**: a client created by an administrator does not
  act as one (no environment editor, modules, other accounts' bots, or making
  administrators); it holds only the administration permissions it carries,
  under the delegated-administration rules.
* **Reach**: optionally limited to some bots and/or workspaces (every bot in a
  listed workspace). A limited client gets `403` for anything else: other bots
  and workspaces, creating workspaces, creating bots outside a listed
  workspace, moving a bot out, the administration area, sites, account-wide
  activity and the AI assistant. The bot list and workspace list are filtered.
* **Session-only routes**: no client can manage passwords, two-step sign-in,
  passkeys, sessions, linked accounts, SFTP keys, automation tokens or API
  clients, change the profile or address, accept invitations, or sign out.
* **Expiry**: 1 to 366 days, or never. **Revocation**: by the owner, or by an
  account manager under Administration → API clients (a delegated manager only
  for accounts it outranks). Revocation, expiry, a disabled owner and role
  changes apply on the next request; nothing is cached.
* Shown once; only the SHA-256 is stored. Last use is recorded (to the minute).
  600 requests per minute per client.
* Audited: creation (name and permissions), revocation, changes made with a
  client (the actor reads "email via API client NAME"), and every refused
  change outside the client's scope (`account.api_client_refused`).

Enforcement: application layer. Permissions use the same route middleware and
service checks as browser sessions; the bot and workspace limits are checked
in the authentication middleware and again in the bot and workspace services.
