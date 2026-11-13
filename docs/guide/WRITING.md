# Writing the RivetPanel guide

`docs/guide` is the single source of the RivetPanel documentation. The panel's
`/docs` pages, their search index and the Ctrl+K entries are all built from
these files, and the same files read as ordinary Markdown on GitHub. Edit the
facts here, and only here.

## Layout

```text
docs/guide/
  01-get-started/
    _category.md        title, blurb and icon of the category
    01-overview.md      one page
    02-quick-start.md
  02-apps/
  …
```

The `NN-` prefixes set the order and are dropped from addresses, so
`05-observe/04-health.md` is served at `/docs/observe/health`. Renaming a file
changes its address: add the old address to `aliases` (below) when you do.
Files without a number prefix, like this one, are not published.

## Frontmatter

Every page starts with:

```yaml
---
title: Health and alerts
lede: One sentence that says what the reader gets from this page.
icon: shield            # a name from web/src/lib/components/ui/Icon.svelte
audience: admin         # optional; marks pages only administrators can act on
aliases: [health]       # optional; old /docs#ids that should open this page
glance:                 # optional; 2–4 short facts shown above the page
  - 3 separate health signals
  - Probes reach only 127.0.0.1
related: [observe/telemetry, observe/notifications]
---
```

Do not start the body with a `#` title: the page title comes from `title`.

## Structure

- Start with what the reader is trying to do, then how, then the limits.
- Use `##` for the main sections and `###` below them. Use sentence case for
  headings. A heading's link id is GitHub's: lower case, punctuation removed,
  spaces become hyphens.
- Keep paragraphs to 1–4 sentences. If a paragraph lists settings, fields or
  limits, it should be a table or a list.
- Put a procedure in an ordered list: the panel shows each item as a numbered
  step.
- Link other pages with relative links to their files, for example
  `[Ports](../03-game-servers/04-ports.md#forwarding)`. Links to anything else
  in the repository open on GitHub.

## Conventions the panel renders specially

| Write | Shows as |
|---|---|
| `**Administration → Nodes**` | A menu path (bold text that contains `→`) |
| `` `RIVET_SITES_LISTEN` `` | Code: environment variables, paths, commands, API routes, JSON keys |
| `[enforced: panel]`, `[enforced: docker]`, `[enforced: host]`, `[not enforced]` | A badge saying which layer enforces the claim before it |
| `> [!NOTE]`, `> [!TIP]`, `> [!IMPORTANT]`, `> [!WARNING]`, `> [!CAUTION]` | A callout (GitHub renders these too) |
| `<!-- figure: limits-legend -->` | A drawn or interactive figure; GitHub shows its picture instead (below) |

Use enforcement badges on every security control and limit. Never describe a
control as enforced when it is only stored as configuration.

Use callouts sparingly, at most two on a page: WARNING for data loss or a
security consequence, NOTE for a fact people otherwise trip over.

Figures are registered in `web/src/lib/docs/figures/index.ts`. Put the
figure's picture for GitHub on the line right after the comment; the panel
draws the figure and skips the picture:

```markdown
<!-- figure: system-map -->
![What the figure shows, in one sentence](../figures/system-map.png)
```

The alt text is what screen readers and GitHub's text view get, so describe
the content, not the picture. After changing a figure, run `npm run dev` in
`web/` and then `make docs-figures` to photograph it again into
`docs/guide/figures/`. An image anywhere else in the guide is refused.

## Checking

`make docs-check` reports missing frontmatter, broken links between pages,
unknown figures and icons, missing figure pictures, and duplicate aliases.
The panel build shows the same problems as warnings.
