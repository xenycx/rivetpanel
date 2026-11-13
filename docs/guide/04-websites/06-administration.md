---
title: Site administration
lede: Know who can do what with a site, suspend one without deleting it, understand the isolation limits, and drive sites through the API.
icon: shield
audience: admin
related: [websites/domains, teams/workspaces, teams/roles-and-permissions, operate/security]
---

## Who can do what

Sites belong to a [workspace](../06-teams/01-workspaces.md). The workspace
role decides what a member can do [enforced: panel]:

| Workspace role | Can |
|---|---|
| Viewer | Look at the site |
| Developer | Publish, deploy, roll back and manage domains |
| Admin | Everything a developer can, plus delete and move sites |

## Suspend a site

Panel administrators see every site under
**Administration → Sites and domains**.

**Suspend** stops a site from being served: visitors get an "unavailable"
page on every address until it is restored [enforced: panel]. Nothing is
deleted, and its members can still manage it. **Restore** serves it again.

## What sites do not do

- Apps share one Docker network: an app can reach another app's port
  directly, the same as bots on the default network [not enforced]. Use
  separate panels for tenants that must not see each other.
- Sites under one sites domain can set cookies for each other's host names
  unless the domain is on the Public Suffix List [not enforced]. Give
  untrusted tenants their own registrable domain.
- No Dockerfile builds, form handling, password protection, bandwidth limits
  or per-site analytics.
- Uploaded content is not scanned for malware.

App limits (panel node only, no blue-green deploys) are in
[Sites from GitHub](02-from-github.md#limits). See
`docs/implementation-status.md` for how each control is enforced.

## API

All paths are under `/api/v1`.

| Method and path | Purpose |
| --- | --- |
| `GET /sites-info` | Whether hosting is on, how addresses look, and the sites domains new sites can use (`domains`) |
| `GET /bots/{bot}/site`, `POST /bots/{bot}/site` `{slug?, domain_id?}` | Get or create a bot's integrated site |
| `GET /sites`, `POST /sites` `{name, slug?, domain_id?, workspace_id?, spa?, template_id?}` | List and create standalone sites. `domain_id` is a sites domain id or name (default: the primary); `template_id` publishes a site template as the first release |
| `GET /site-presets` | The frameworks sites can be built or run with, and the public build-variable prefixes |
| `POST /sites/analyze` `{repo, branch?, root_dir?}` | Download a branch and propose a site type and preset (creates nothing) |
| `POST /sites/from-repo` | Create a static, static build or app site from GitHub and queue its first deployment (fields below) |
| `GET /sites/by-app/{app}` | The site an app unit belongs to |
| `GET /site-templates` | Built-in site templates: `{templates:[{id, name, description, tags, accent, theme, status_widget, spa, pages, files, bytes, preview_url}]}` |
| `GET /site-templates/{id}/preview?page=` | One page of a template as a sandboxed, script-free HTML document (stylesheets inlined) that the panel may frame |
| `GET /sites/{id}` | Site, role, domains with DNS records, releases, GitHub deploy state; for sites from GitHub also `app` (the app unit) and `repo` |
| `PATCH /sites/{id}` | Address (`slug`, `domain_id`), site, generated-page, privacy, mode and repository settings; for sites from GitHub also `build_output_dir`, `app_port` (app stopped), `health_path`, `max_body_bytes` |
| `DELETE /sites/{id}` | Delete with every release and domain (workspace admin) |
| `POST /sites/{id}/upload` | Publish a ZIP (raw body) |
| `GET/PUT/DELETE /sites/{id}/files...` | List, read, edit, move, extract and delete private draft files |
| `POST /sites/{id}/files/publish` | Snapshot and activate the private draft |
| `POST /sites/{id}/deploy` `{sha?}` | Deploy the linked branch head in the background. Sites from GitHub build and publish, or rebuild and restart the app, and accept a commit to deploy again |
| `POST /sites/{id}/releases/{release}/activate` | Serve an earlier release |
| `POST /sites/{id}/domains` `{domain}` | Add a custom domain |
| `POST /sites/{id}/domains/{domain}/verify` | Check its TXT record now |
| `DELETE /sites/{id}/domains/{domain}` | Remove it |
| `GET /admin/sites`, `PATCH /admin/sites/{id}` `{disabled}` | Administrators: list, suspend or restore |
| `GET /admin/site-base-domains`, `POST /admin/site-base-domains` `{domain, label?, dns_target?}` | Administrators: list sites domains with their DNS records, add one |
| `POST /admin/site-base-domains/{domain}/verify` | Check its `_rivetpanel-domain` TXT record now |
| `PATCH /admin/site-base-domains/{domain}` `{enabled?, primary?, label?, dns_target?}` | Turn on or off, make primary, rename the label, set the DNS target shown |
| `POST /admin/site-base-domains/{domain}/move-sites` `{to}` | Move every site to another sites domain (all or nothing) |
| `DELETE /admin/site-base-domains/{domain}` | Remove an unused, non-primary domain not set in the environment file |

`POST /sites/from-repo` takes `{name, slug?, domain_id?, workspace_id?, kind,
framework?, repo:{full_name, branch, root_dir?, auto_deploy?}, runtime,
build_command?, output_dir?, spa?, argv?, port?, health_path?,
release_command?, memory_bytes?, nano_cpus?, max_body_bytes?, env?, addons?}`.
`kind` is `static`, `static_build` or `app`.

### App units

An app unit is a bot of kind `web`. Its variables, services (`addons`),
console, files, backups, deployments (`/bots/{app}/github`,
`/bots/{app}/deploy`), operations, and start, stop and restart use the
`/bots/{app}/…` endpoints.

Deleting it, publishing ports, moving it to another workspace or node, and
unlinking its repository are refused there [enforced: panel]. Do those through
the site.
