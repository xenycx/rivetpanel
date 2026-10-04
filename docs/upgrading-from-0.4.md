# Upgrading from BotPanel 0.4.0

RivetPanel is the renamed successor of BotPanel 0.4.0 (also published as
BotForge), and the rename is a **deliberate clean break**. RivetPanel does not
read, convert or modify a BotPanel installation:

| | BotPanel 0.4.0 | RivetPanel |
| --- | --- | --- |
| Service | `botpanel.service`, `botpanel-backup.timer` | `rivetpanel.service`, `rivetpanel-backup.timer` |
| Binary | `/usr/local/bin/botpanel` | `/usr/local/bin/rivetpanel` |
| Environment | `/etc/botpanel/botpanel.env`, `BOTPANEL_*` | `/etc/rivetpanel/rivetpanel.env`, `RIVET_*` |
| Keys | `/etc/botpanel/keys` | `/etc/rivetpanel/keys` |
| Data | `/var/lib/botpanel` (`botpanel.db`, `bots/`) | `/var/lib/rivetpanel` (`rivetpanel.db`, `bots/`) |
| Containers | `botpanel-<bot>-runtime`, label `botpanel.managed` | `rivetpanel-<bot>-runtime`, label `rivetpanel.managed` |
| Backups | `botpanel.db` snapshot | `rivetpanel.db` snapshot; BotPanel backups are refused |

Accounts, bots, environment variables, schedules and settings are **not**
imported. Plan to re-create them. RivetPanel warns at start (and in
Administration → Diagnostics, "Former installation") when it finds `BOTPANEL_*`
or `BOTFORGE_*` variables, `/var/lib/botpanel`, a `botpanel.db` next to its
own database, or running containers labelled `botpanel.managed`. It never
touches them.

## 1. Write down what you need

While the old panel still runs, note each bot's runtime, start command,
memory/CPU limits, repository and branch, schedules, and its environment
variables (Discord tokens and other secrets are encrypted in the old
database; copy them from their original source or from the old panel's
interface). Download any bot files you need from the old panel or copy them
from `/var/lib/botpanel/bots/<bot-id>/` as root.

## 2. Stop and disable the old panel

```sh
sudo systemctl disable --now botpanel.service botpanel-backup.timer
```

For a container install, stop the old Compose project (`docker compose down`
in its directory; keep its volume until you are done).

## 3. Remove the old bot containers

Stopping the old panel does **not** stop its bots: their containers keep
running. Remove them before starting the same bots in RivetPanel; two
processes connected with the same Discord token fight over the gateway
session (each disconnects the other, and Discord may rate-limit the token).

```sh
docker ps -a --filter label=botpanel.managed                 # review first
docker ps -aq --filter label=botpanel.managed | xargs -r docker rm -f
```

Old add-on (database) networks, if any, can then be removed with
`docker network prune` after checking what it lists.

## 4. Move the old data aside

Keep the old data until the new installation works, but move it out of the
way so nothing picks it up by accident:

```sh
sudo mv /var/lib/botpanel /var/lib/botpanel.0.4.0
sudo mv /etc/botpanel /etc/botpanel.0.4.0        # contains the old keys: keep it protected
sudo rm /etc/systemd/system/botpanel.service /etc/systemd/system/botpanel-backup.*
sudo systemctl daemon-reload
```

Remove `BOTPANEL_*` / `BOTFORGE_*` variables from any environment file,
Compose `.env` or shell profile; RivetPanel ignores them and warns while they
are set.

Old backups (`/var/backups/botpanel`) can only be restored by the BotPanel
0.4.0 binary. `rivetpanel backup-verify` and `rivetpanel restore` refuse them
with "this is a BotPanel 0.4.0 backup; RivetPanel cannot restore it".

## 5. Install RivetPanel

Follow [`deployment.md`](deployment.md) (systemd) or
[`container.md`](container.md) (container). Create a new encryption key with
`rivetpanel keygen`; the old key cannot decrypt anything RivetPanel stores.

* **SFTP:** RivetPanel creates a new SSH host key
  (`/var/lib/rivetpanel/sftp_host_ed25519`). SFTP clients will report a changed
  host key: remove the old entry (`ssh-keygen -R '[panel.example.com]:2022'`)
  and accept the new fingerprint after checking it.
* **OAuth / GitHub:** callback URLs keep the same paths under
  `RIVET_PUBLIC_URL`; move the client IDs and secrets to the `RIVET_*`
  variables. GitHub webhooks must be re-created by configuring each bot's
  repository again.
* **DNS verification for sites** uses the new record names; verify custom
  domains again.

## 6. Re-create accounts and bots

Create the first administrator (`rivetpanel create-admin`, or the setup code
on `/setup`), invite the other users, and re-create each bot with the values
from step 1. Start each bot only after its old container is gone (step 3).

When everything runs, delete `/var/lib/botpanel.0.4.0`, `/etc/botpanel.0.4.0`
and `/usr/local/bin/botpanel` (or keep an off-host copy for a while). The
startup warnings disappear once no trace is left.
