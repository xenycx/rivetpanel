---
title: Publish to GitHub
lede: Turn a bot's files into a new GitHub repository, and push later changes made in the panel or over SFTP back to its branch.
icon: upload
aliases: [publish]
glance:
  - At most 5,000 files and 200 MiB
  - 25 MiB per file
  - Never force-pushes
related: [apps/deploy-from-github, apps/files, operate/remote-nodes]
---

## Publish a bot

**Deployments → Publish to GitHub** creates a repository from the bot's
current files and links the bot to it.

1. Open the bot's **Deployments** tab and press **Publish to GitHub**.
2. Choose the **Owner** (your account or one of your organizations), the
   **Repository name** and an optional description. **Private repository**
   is on by default.
3. Check **What will be pushed**. The dialog lists every file before
   anything is created, so secrets in other files can be spotted.
4. Tick **Deploy on every push** to add a webhook that redeploys the bot,
   then publish.

The panel creates the repository, pushes the files as the first commit and
links the bot to it.

## Push changes

For a bot already linked to a repository, **Push to GitHub** commits the
current files to the linked branch, for example after editing them in the
panel or over SFTP.

- The push is never forced. If the branch moved on GitHub meanwhile, the push
  stops with an explanation; deploy those changes first.
- With a linked sub-folder, only that folder of the repository is replaced;
  the rest of the repository stays.
- Each push is recorded as a *Push to GitHub* operation in the deployment
  history. The pushed commit is recorded as deployed, and its own webhook
  delivery does not redeploy the bot.

## Who can publish

Pushing uses the acting person's own GitHub connection with repository access
(`repo` scope), never the token of whoever linked the repository.
[enforced: panel]

| Action | Needs on the bot |
|---|---|
| Push to GitHub | *Edit files* |
| Publish to GitHub (a new repository) | *Full admin* |

Connect GitHub under **Settings → Connected accounts**.

## What is sent

Every file is sent except:

- files excluded by `.gitignore` files, at the root and in sub-folders, with
  negation;
- `.git`, dependency and cache folders (`node_modules`, `.venv`, `venv`,
  `__pycache__`, `target` at the top, …) and panel metadata;
- `.env` and `.env.*` files. `.env.example`, `.env.sample`, `.env.template`
  and `.env.dist` are kept.

> [!IMPORTANT]
> Environment variables stored in the panel are never pushed. Secrets written
> into other files are: check the file list before you publish.

Nothing runs on the host: files become blobs, a tree and a commit through
GitHub's Git Data API. Files GitHub already has are not uploaded again.

| Limit | Value |
|---|---|
| Files | 5,000 [enforced: panel] |
| Total size | 200 MiB [enforced: panel] |
| Per file | 25 MiB; larger files are skipped and listed [enforced: panel] |

## Bots on a remote node

The node selects the files with the same rules and limits, and the panel
reads only those files, one at a time, over the authenticated agent
connection. Nothing is copied to the panel's disk. Publishing and pushing
are refused while the node is offline.
