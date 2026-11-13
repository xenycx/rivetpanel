# Documentation overhaul plan: the living manual

Internal planning document. Not part of the published guide.

**Status (2026-11-13):** phases 0 and 1 are done (52 pages in `docs/guide`,
the pipeline, routes, search and `make docs-check`), and so is phase 3 (ten
drawn figures with GitHub pictures). Phases 4 and 5 are not started.

Direction chosen: **Living manual**. Content source chosen: **Markdown single
source** (`docs/guide/*.md` rendered in the panel at build time).

## 1. What is wrong today (measured on 1.7.0 build, 1440×900)

| Problem | Evidence |
|---|---|
| One endless page | `/docs` is **31,105 px tall** and holds **9,558 words** in 30 flat sections. You can't link to a page, only to a `#hash`. |
| No hierarchy | Every topic gets the same weight: *Appearance* (43 words) sits beside *Game servers* (1,009 words). No categories, no sub-headings inside a section. |
| Wall-of-text sections | Each section is 1–8 run-on paragraphs stored as JS strings. No lists, tables, steps, code blocks, callouts or inline `code`. Env vars and menu paths (`Administration → Nodes`) look like ordinary prose. |
| Dead space | Text is capped at 52ch inside a full-width card, so the right ~45% of every card is empty. |
| Visuals are rare | Only 4 of 30 sections have a figure (sites pipeline, widget sample, limits legend, security split). |
| Generic identity | The hero ("Deploy it. Operate it. Give it a public home.") is marketing copy on a reference page. Half the nav items fall back to the same `book` icon. |
| Mobile | A 30-item contents list comes before any content. |
| Same facts written three times | README, the 68 KB `web/src/routes/docs/+page.svelte` string array, and `docs/*.md` (~9k lines) drift apart. The Ctrl+K docs index in `web/src/lib/search.ts` is a fourth hand-kept copy and already lists only 20 of 30 sections. |
| Version section is a changelog dump | "Version and implementation status" retells releases 0.6 to 1.0 in one paragraph. |

## 2. Principles

- **Read mode.** People come to the docs to understand something. Clear structure comes first. The "wow" goes into figures that explain, not into decoration.
- **Field-manual identity.** Use the panel's own vocabulary: IBM Plex Sans with Plex Mono for labels and paths, the `term` dark surface for schematics, and `run`/`warn`/`fail` meaning what they mean in the product. Number the sections (`4.2 Health and alerts`) and the figures (`Fig. 4.2-a`). Draw diagrams like schematics, not stock illustrations.
- **Enforcement honesty is visible.** AGENTS.md requires saying *where* each control is enforced. Make that a signature element: an inline **boundary badge** (`Panel` · `Docker` · `Kernel/host` · `Not enforced`) next to every security or limit claim.
- **One source.** Write each fact once, in Markdown. The panel, GitHub and the Ctrl+K index all read it from there.
- **Progressive.** Every enhancement (View Transitions, scroll-driven animation, live figures) falls back to a static page that still reads well. No new runtime dependencies.

## 3. Information architecture

URL scheme: `/docs` (hub) → `/docs/<category>/<page>` → `#<heading>`.
Seven categories and about 40 pages. Each page has an H1, a one-sentence lede, an
optional *At a glance* fact strip, then H2/H3 sections.

| # | Category | Pages (source today) |
|---|---|---|
| 1 | **Get started** | Overview: what RivetPanel runs (README, new) · Quick start (`quickstart`) · Core concepts: apps, workspaces, nodes, the sites origin (`architecture.md`) · What's new (generated from CHANGELOG's latest version + Unreleased; replaces `version`) |
| 2 | **Apps** | Bots and runtimes (`features.md` lifecycle/config/runtime) · Files, SFTP and packages · Environment and secrets · Build commands and logos (`build`) · Deploy from GitHub (`deploy`) · Publish to GitHub (`publish`) · Services: databases (`addons`) · Containers and Compose (`containers`, `containers.md`) · Scheduled actions |
| 3 | **Game servers** | Server types and creation · Steam games (SteamCMD) · Pterodactyl eggs · Ports and allocations · Players, properties, mods and plugins · Worlds, schedules and task chains · Server types for administrators (all from `game-servers.md`) |
| 4 | **Websites** | Bot sites and Page Studio · Sites from GitHub: static, build, app · Publishing and releases · Domains, DNS and TLS · Redirects and headers (all from `sites.md`) |
| 5 | **Observe** | Dashboard widgets · Widget data shapes (reference) · Telemetry limits (reference) · Health and alerts · Usage analytics · Logs and archive (`logs.md`) · Notifications (`support.md` §Notifications) |
| 6 | **Teams and access** | Workspaces · Roles and permissions (`permissions.md`) · Sign-in: SSO, passkeys, API clients (`auth.md`, `oauth.md`) · How limits work (`workspace-limits`) · Support tickets, help center, status page (`support.md`) · Appearance |
| 7 | **Operate** *(admin chip)* | Install and deploy (`deployment.md`, `container.md`) · Upgrades and rollback · Remote nodes (`agents.md`) · Host, logs and environment (`admin-tools`) · Email (Mailgun) · Backups and restore (`backup.md`) · Security and isolation (`isolation.md`) · Extensions (`extensions.md`) · AI assistant (`ai-operator.md`) · Automation API (`automation.md` + OpenAPI link) · Diagnostics and footprint (`footprint.md`) |

The panel does not render these internal files: `implementation-status.md`,
`implementation-handoff.md`, this plan, and `upgrading-from-0.4.md` (linked from
*Upgrades* instead). `features.md` is split across the pages above and then
deleted. Its catch-all role ends.

## 4. Content pipeline (single source)

1. **Layout.** `docs/guide/<NN-category>/<NN-page>.md` with frontmatter:
   `title`, `lede`, `icon`, `audience` (`everyone`|`admin`), `glance` (2–4
   facts), `related`. A `docs/guide/<NN-category>/_category.md` holds each category's title, blurb and
   glyph. `docs/README.md` stays the GitHub index and links into `guide/`.
2. **Build step.** A Vite plugin (`web/vite-docs.ts`) reads the tree at build
   time and parses it with `marked`'s lexer (devDependency only, zero runtime).
   It emits:
   - `manifest.json`: categories, pages, headings, prev/next, old-anchor map
   - one JSON block tree per page, code-split per route
   - `search-index.json`: heading-level entries with snippets, lazy-loaded
3. **Directives** for things Markdown cannot say, kept GitHub-readable:
   - `:::callout note|warn|admin` … `:::`
   - `:::steps` (ordered list rendered as a numbered stepper)
   - `{boundary=docker}` after a claim → boundary badge
   - `:::figure health-signals` → a registered Svelte figure. On GitHub it falls
     back to the static PNG/SVG named in the directive, so the source never shows
     a broken embed.
   - Menu paths in the form `Administration → Nodes` become styled path chips
     automatically, and `RIVET_*` / `${NAME}` become `code`.
4. **Renderer.** `web/src/lib/docs/Blocks.svelte` walks the block tree. Figures
   resolve through `web/src/lib/docs/figures/index.ts`.
5. **Consumers.** `/docs` routes, the Ctrl+K docs group (`search.ts` reads the
   manifest instead of its hand list), the AI assistant's page context, and
   `make docs-check` (broken links, unknown figures, orphan pages,
   missing frontmatter).
6. **Old links keep working.** `/docs#sites` and the other 30 hashes are
   redirected client-side through the manifest's anchor map. The ~12 in-app
   links (`LIMITS_HELP`, Alerts, admin/sites, templates…) move to direct paths.

## 5. Page anatomy (three-pane)

```
┌ Category rail ───┐┌ Article (max 68ch prose, figures may break wider) ┐┌ On this page ┐
│ 1 Get started    ││ OBSERVE / 5.4                                     ││ Three signals│
│ 2 Apps           ││ Health and alerts                                 ││ ▸ Heartbeat  │
│ …                ││ Know whether the code is alive, not just the      ││   Probe      │
│ 5 Observe   ▾    ││ container.                                        ││   Alerts     │
│   5.1 Widgets    ││ ┌ At a glance ─────────────────────────────────┐  ││ ─────────    │
│   5.4 Health ●   ││ │ 3 signals · 60 pushes/min · 2-min node alert │  ││ Edit on      │
│                  ││ └──────────────────────────────────────────────┘  ││ GitHub       │
│                  ││ [Fig. 5.4-a live]  …  ← Prev    Next →           ││              │
└──────────────────┘└───────────────────────────────────────────────────┘└──────────────┘
```

- Rail: categories collapse and the current page is marked. Each category shows its page count.
- On this page: a scroll-spy driven by IntersectionObserver, with a progress tick per heading.
- Bottom of the article: prev/next by manifest order, related pages, "Last changed in 1.x"
  (from git at build time, if cheap; otherwise omitted).
- Mobile: a sticky top bar shows the breadcrumb and a **Contents** button that opens a sheet with the rail.
  On this page becomes a collapsible at the top of the article. Content comes first.
- Width: three panes from 1280px up, rail + article at 1024px, single column below that.

## 6. The hub (`/docs`)

Replace the marketing hero with a calm, useful front page:

- Title "Documentation", the version chip, and a **large search field** that opens the palette.
- A **Start here** path: Quick start → Core concepts → your first bot / server / site (three
  cards keyed to `/new` kinds).
- **Category grid**: seven tiles. Each has its schematic glyph (a tiny diagram
  fragment drawn in the figure style), a one-line blurb and its top 4 pages. This is where
  the page gets its character.
- **What's new in 1.x**: three bullets from the changelog, linked.
- Reference links: OpenAPI spec, widget data shapes, telemetry limits, changelog.

## 7. Visual system

### 7a. Static schematics (SVG Svelte components, token-colored, light/dark)

| Fig. | Page | Shows |
|---|---|---|
| 1.3-a | Core concepts | System map: panel ↔ local runner, agents dialing out over TLS, sites origin separated from panel cookies, add-on private networks |
| 2.7-a | Services | Bot ⇄ postgres/redis on an internet-less private network; the injected env vars as chips |
| 2.8-a | Containers | Compose services as a graph, reaching each other by name; published ports at the edge |
| 3.4-a | Ports | Allocation pool → primary + extra ports → router forward (TCP+UDP) |
| 4.1-a | Bot sites | Bot → Page Studio → Sites origin (existing figure, redrawn) |
| 4.3-a | Releases | Draft → Publish → immutable release → rollback timeline |
| 6.3-a | Sign-in | OIDC sequence: PKCE, state, nonce, token checks |
| 7.3-a | Remote nodes | Enrollment sequence: one-time command → agent → outward TLS → placement |
| 7.6-a | Backups | Restore flow with the automatic safety backup |
| 7.7-a | Security | Layered boundary: Panel → Docker → Kernel/host, with the boundary badges |

### 7b. Living figures (the overdrive moments: the panel's own code, running in the docs)

1. **Widget playground** (Observe → Dashboard widgets, Widget data shapes).
   A kind picker and an editable JSON payload. The real `WidgetGrid` renders it live, and
   validation errors use the same messages as the API. Every row of the data-shapes
   reference has a "Try it" link that preloads the playground.
2. **Limits calculator** (How limits work). Sliders for app memory/CPU, the workspace
   allowance, the node budget and the node maximum. Shows which limit refuses which action
   (create / resize / start), reusing `LimitsLegend` and `lib/limits.ts`.
3. **Health signal timeline** (Health and alerts). A scrubbable timeline with process
   state, heartbeat and probe as three lanes. Scenarios ("container up, Discord
   disconnected", "host can't reach port") show which signal fires and which alert is sent.
4. **Permission matrix** (Roles and permissions). The permission catalog from
   `lib/permissions.ts` as a filterable grid of Administrator / User / custom roles.
   Hover a permission to see what it unlocks.
5. **Log-day ruler** (Logs and archive). A 48-hour ruler with the archive time and time zone.
   Move the cutoff to see how a day file is named, compressed and expired.

Rule: at most **one** living figure per page, lazy-mounted when it nears the
viewport. Each has a static fallback (a caption with the default state) for
no-JS and print.

### 7c. Motion (restrained)
- View Transitions on page change: the article cross-fades and the rail marker slides.
  Firefox and reduced-motion users get an instant swap.
- Schematics draw in once with a scroll-driven stroke reveal
  (`animation-timeline: view()` inside `@supports`). When the figure has
  numbered steps, the step the reader is on highlights.
- Nothing loops. Everything respects `prefers-reduced-motion`.

## 8. Search

- The palette (`/` or Ctrl+K inside docs) searches the build-time index at heading level.
  Results are grouped by category, show the matched snippet highlighted, and support keyboard
  navigation. Enter jumps straight to `/docs/<cat>/<page>#<heading>`.
- The global Ctrl+K docs group uses the same manifest, which fixes the 10 missing sections.
- The index loads on first open. Scoring is tokenized prefix matching with a title boost; no dependency.

## 9. Phases

| Phase | Scope | Done when |
|---|---|---|
| **0. Pipeline** | Vite plugin, manifest, `Blocks.svelte`, directives, `/docs/[cat]/[page]` route, three-pane shell, anchor redirects, `make docs-check` | One migrated page renders; old `#hash` links land correctly; build has no runtime dep added |
| **1. Content** | Move and split all guides into `docs/guide/`, dissolve `features.md`, rewrite run-on prose into lede + glance + H2/H3 + steps/callouts/tables, add boundary badges, delete the Svelte string array | All 30 old sections reachable; `docs-check` clean; README trimmed to an overview that links in |
| **2. Hub and nav** | New `/docs` hub, category glyphs, prev/next, mobile Contents sheet, search palette, `search.ts` on the manifest | Hub and three widths verified in the browser |
| **3. Schematics** | The 10 static figures plus GitHub PNG fallbacks (exported once with `magick`) | Light/dark both checked; figures readable at 390px |
| **4. Living figures** | Playground, calculator, health timeline, permission matrix, log ruler; View Transitions and scroll reveals | 60fps scroll on a throttled CPU; reduced-motion and no-JS fallbacks checked |
| **5. Ship** | CHANGELOG (Unreleased: *Changed* the documentation is now multi-page…), AGENTS.md note that `docs/guide` is the single source, e2e test for routes and redirects, `make version-check` | Checks pass; old links verified |

Phases 0 and 1 belong together, because the shell is untestable without content.
After them, phases 3 and 4 can run in parallel sessions, since they touch separate
figure files.

## 10. Risks and decisions to confirm while building

- **The content rewrite is the biggest job** (about 9.5k words of in-app prose plus about 7k
  lines of Markdown, merged and deduplicated). Where the in-app text and the `.md`
  disagree, the newer behavior wins; check those against the code, not by guessing.
- **GitHub readability**: directives must degrade to readable Markdown. `docs-check`
  should render a sample through a plain Markdown parser in CI.
- **Bundle size**: per-page JSON is code-split and the search index is lazy. Target is
  under 40 KB gzip for the docs shell and under 25 KB gzip per page excluding figures.
- **Public route**: `/docs` is public. Living figures must not call authenticated
  APIs. They run on local sample data only.
