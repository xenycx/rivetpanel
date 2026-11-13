---
title: Variables and network
lede: Give an app its tokens and settings safely, and decide whether it can reach the internet and which ports it publishes.
icon: key
glance:
  - Values encrypted with AES-256-GCM
  - Reveal hides a value again after 30 s
  - Host ports 20000–29999 by default
related: [apps/services, apps/containers, game-servers/ports, operate/configuration]
---

## Environment variables

The **Variables** tab holds the app's environment: tokens, API keys and
settings. The app must be stopped to change them.

- Values are encrypted at rest with AES-256-GCM. [enforced: panel]
- Values are masked in every list. **Reveal** fetches one value on request
  and hides it again after 30 seconds.
- **Import .env** takes pasted `KEY=VALUE` lines (an optional `export `,
  quotes and `#` comments are understood) and shows the names for review
  before anything is saved.

Encryption protects the database and its backups, not the running container:
anyone with Docker access on the host can read the values from the
container's environment. See [Concepts](../01-get-started/03-concepts.md#data-at-rest).

### Reserved names

| Name | Why |
|---|---|
| Names starting with `RIVET_` | Reserved for the panel [enforced: panel] |
| Names starting with `LD_`, and `PATH` | Refused for bots, whose runtime limits which commands run [enforced: panel] |
| `RIVET_TELEMETRY_KEY`, `RIVET_URL` | Set by the panel for the telemetry SDK |

Container apps may use `PATH` and `LD_*`, because a container runs whatever
its image does anyway. See [Containers and stacks](08-containers.md).

Companion databases add their own connection variables, and your variables
can reference them as `${NAME}`. See [Services](07-services.md).

## Network

The **Network** tab sets outbound access and published ports. The app must be
stopped, and the tab needs full admin rights on the app. Only the app's owner or
an administrator can publish host ports. [enforced: panel]

### Outbound access

**Outbound internet access** is on by default. Turning it off puts the app on
Docker's `none` network, which also disables its published ports.
[enforced: docker] A Discord bot needs outbound access to reach Discord.

### Published ports

Ports are not published by default. To reach a web server, dashboard or
health check inside the app, publish a port:

1. Open **Network** and press **Add port**.
2. Enter the **Container port** and the **Protocol**.
3. Enter a **Host port** inside the panel's port range.
4. Press **Save network settings**.

| Rule | Value |
|---|---|
| Host port range | `RIVET_PORT_RANGE`, `20000-29999` by default [enforced: panel] |
| Each host port | Used by one app only [enforced: panel] |
| Bound to | `127.0.0.1`, unless an administrator sets `RIVET_PORT_PUBLIC_BIND=1` [enforced: docker] |

With public binding on, published ports are reachable from outside the host;
limit them with a firewall. Game servers manage their ports as allocations:
see [Ports and allocations](../03-game-servers/04-ports.md).

### Bandwidth

> [!NOTE]
> The **Bandwidth limit** is recorded but not enforced. [not enforced] Docker
> has no built-in bandwidth cap; enforcing one needs traffic shaping (`tc`)
> on the host.
