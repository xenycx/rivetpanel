<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { SitesInfo, SiteTemplate, Template } from '$lib/api/types';
	import type { Blueprint } from '$lib/api/games';
	import { gameBlurb } from '$lib/gameIcons';
	import { can, session } from '$lib/session.svelte';
	import { loadWorkspaces } from '$lib/workspaces.svelte';
	import NewSiteDialog from '$lib/components/NewSiteDialog.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import GameIcon from '$lib/components/ui/GameIcon.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import TabBar from '$lib/components/ui/TabBar.svelte';

	// Every starting point in one place: bot templates, game server types
	// (built-in and imported eggs) and site templates. Each kind keeps its own
	// loading and error state so one failing source does not hide the others.
	type Kind = 'bots' | 'games' | 'sites';
	type Load<T> = { list: T[] | null; error: string };

	let bots = $state<Load<Template>>({ list: null, error: '' });
	let games = $state<Load<Blueprint>>({ list: null, error: '' });
	let sites = $state<Load<SiteTemplate>>({ list: null, error: '' });
	let sitesInfo = $state<SitesInfo | null>(null);
	let hiddenTypes = $state(0);

	let q = $state(page.url.searchParams.get('q') ?? '');
	let lang = $state(page.url.searchParams.get('lang') ?? '');
	let family = $state('');

	const msg = (e: unknown, what: string) => (e instanceof ApiError ? e.message : `The ${what} could not be loaded. Check your connection and try again.`);
	const gamesOn = $derived(session.features.games);
	const sitesOn = $derived(session.features.sites);
	const manager = $derived(session.user?.role === 'admin' || can('blueprints.manage'));

	const tabs = $derived<{ id: Kind; label: string; icon: IconName; href: string }[]>([
		{ id: 'bots', label: 'Bot templates', icon: 'box', href: '/templates' },
		...(gamesOn ? [{ id: 'games' as Kind, label: 'Game servers', icon: 'gamepad' as IconName, href: '/templates?tab=games' }] : []),
		...(sitesOn ? [{ id: 'sites' as Kind, label: 'Sites', icon: 'globe' as IconName, href: '/templates?tab=sites' }] : [])
	]);
	const tab = $derived.by<Kind>(() => {
		const t = page.url.searchParams.get('tab');
		return tabs.some((x) => x.id === t) ? (t as Kind) : 'bots';
	});

	async function loadBots() {
		bots = { list: null, error: '' };
		try {
			bots = { list: (await api<{ templates: Template[] }>('GET', '/templates')).templates, error: '' };
		} catch (e) {
			bots = { list: [], error: msg(e, 'bot templates') };
		}
	}
	async function loadGames() {
		games = { list: null, error: '' };
		try {
			const all = (await api<{ blueprints: Blueprint[] }>('GET', '/blueprints')).blueprints;
			hiddenTypes = all.filter((b) => !b.enabled).length;
			games = { list: all.filter((b) => b.enabled), error: '' };
		} catch (e) {
			games = { list: [], error: msg(e, 'server types') };
		}
	}
	async function loadSites() {
		sites = { list: null, error: '' };
		try {
			sitesInfo = await api<SitesInfo>('GET', '/sites-info');
			if (!sitesInfo.enabled) {
				sites = { list: [], error: '' };
				return;
			}
			sites = { list: (await api<{ templates: SiteTemplate[] }>('GET', '/site-templates')).templates, error: '' };
		} catch (e) {
			sites = { list: [], error: msg(e, 'site templates') };
		}
	}

	onMount(() => {
		loadBots();
		if (gamesOn) loadGames();
		if (sitesOn) {
			loadWorkspaces();
			loadSites();
		}
	});
	// From Go to: /templates?tab=sites&create=<id> opens the form for that template.
	let handled = '';
	$effect(() => {
		const id = page.url.searchParams.get('create') ?? '';
		if (!id) handled = '';
		else if (id !== handled && sitesInfo?.enabled && can('sites.create') && sites.list?.some((t) => t.id === id)) {
			handled = id;
			createSite(id);
		}
	});

	// Search: one box for the whole page. Each tab filters its own list, and
	// matches in the other tabs are pointed out below the results.
	const needle = $derived(q.trim().toLowerCase());
	const hit = (text: string) => !needle || needle.split(/\s+/).every((w) => text.toLowerCase().includes(w));
	const botHay = (t: Template) => `${t.name} ${t.description} ${t.language} ${t.runtime} ${t.tested_with}`;
	const gameHay = (b: Blueprint) => `${b.name} ${b.slug} ${b.category} ${b.description} ${gameBlurb(b.slug)} ${b.source === 'custom' ? 'imported egg pterodactyl custom' : ''}`;
	const siteHay = (t: SiteTemplate) => `${t.name} ${t.description} ${t.tags.join(' ')} ${t.status_widget ? 'status' : ''}`;

	const langs = $derived([...new Set((bots.list ?? []).map((t) => t.language))].sort());
	const shownBots = $derived((bots.list ?? []).filter((t) => (!lang || t.language === lang) && hit(botHay(t))));

	const preferred = ['minecraft-paper', 'minecraft-vanilla', 'minecraft-fabric', 'minecraft-forge', 'minecraft-neoforge', 'minecraft-purpur', 'minecraft-folia', 'minecraft-velocity', 'steam-valheim', 'steam-rust', 'steam-project-zomboid'];
	const rank = (b: Blueprint) => (b.source === 'custom' ? 50 : preferred.indexOf(b.slug) + 1 || 40);
	const familyOf = (b: Blueprint) => (b.source === 'custom' ? 'imported' : b.category.toLowerCase().includes('minecraft') ? 'minecraft' : b.category.toLowerCase().includes('steam') ? 'steam' : 'other');
	const families = $derived.by(() => {
		const seen = new Set((games.list ?? []).map(familyOf));
		return [
			{ id: '', label: 'All' },
			...(['minecraft', 'steam', 'other', 'imported'] as const).filter((f) => seen.has(f)).map((f) => ({ id: f, label: { minecraft: 'Minecraft', steam: 'Steam', other: 'Other', imported: 'Imported' }[f] }))
		];
	});
	const shownGames = $derived(
		[...(games.list ?? [])].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name)).filter((b) => (!family || familyOf(b) === family) && hit(gameHay(b)))
	);
	const shownSites = $derived((sites.list ?? []).filter((t) => hit(siteHay(t))));

	// Matches elsewhere, so a search never dead-ends on the wrong tab.
	const elsewhere = $derived.by(() => {
		if (!needle) return [];
		const out: { id: Kind; n: number; noun: [string, string] }[] = [];
		const add = (id: Kind, n: number, noun: [string, string]) => id !== tab && n > 0 && tabs.some((t) => t.id === id) && out.push({ id, n, noun });
		add('bots', (bots.list ?? []).filter((t) => hit(botHay(t))).length, ['bot template', 'bot templates']);
		add('games', (games.list ?? []).filter((b) => hit(gameHay(b))).length, ['server type', 'server types']);
		add('sites', (sites.list ?? []).filter((t) => hit(siteHay(t))).length, ['site template', 'site templates']);
		return out;
	});
	function switchTo(id: Kind) {
		const u = new URL(page.url);
		if (id === 'bots') u.searchParams.delete('tab');
		else u.searchParams.set('tab', id);
		goto(u.pathname + u.search, { noScroll: true, keepFocus: true });
	}

	const accent: Record<string, string> = { JavaScript: '#c99a1c', TypeScript: '#3f7fd6', Python: '#3f7fb0', Rust: '#c9622c', Java: '#c95a40', Go: '#1f9e93', Ruby: '#c23b33' };
	const abbr: Record<string, string> = { JavaScript: 'JS', TypeScript: 'TS', Python: 'PY', Rust: 'RS', Java: 'JV', Go: 'GO', Ruby: 'RB' };

	// Site creation and the larger preview.
	let creating = $state(false);
	let createFor = $state('');
	function createSite(id: string) {
		createFor = id;
		creating = true;
	}
	let previewing = $state<SiteTemplate | null>(null);
	let previewOpen = $state(false);
	let previewPage = $state('index.html');
	function openPreview(t: SiteTemplate) {
		previewing = t;
		previewPage = t.pages[0] ?? 'index.html';
		previewOpen = true;
	}
	const previewSrc = (t: SiteTemplate, p = '') => (p && p !== t.pages[0] ? `${t.preview_url}?page=${encodeURIComponent(p)}` : t.preview_url);
	const canSite = $derived(can('sites.create') && !!sitesInfo?.enabled);

	// Previews are framed with sandbox="allow-same-origin" and nothing else: no
	// scripts, forms, popups or navigation of the panel. The server's own
	// `sandbox` CSP still gives the document an opaque origin, so it never
	// shares the panel's; allow-same-origin only keeps embedded browsers that
	// refuse opaque-origin frames outright from blocking the preview.
	// Thumbnails render the page at desktop width and scale it to the card.
	function fit(node: HTMLElement) {
		const ro = new ResizeObserver(() => node.style.setProperty('--fit', String(node.clientWidth / 1280)));
		ro.observe(node);
		return { destroy: () => ro.disconnect() };
	}
</script>

<svelte:head><title>Templates · RivetPanel</title></svelte:head>

<header class="page-head">
	<div class="min-w-0">
		<h1 class="font-semibold">Templates</h1>
		<p class="text-small text-muted">Start a bot, a game server or a site from a setup that already works.</p>
	</div>
	<label class="relative ml-auto w-full min-w-0 sm:w-72">
		<span class="sr-only">Search templates</span>
		<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
		<input class="field pl-8" type="search" placeholder="Search templates" bind:value={q} />
	</label>
</header>

<div class="mt-3 border-b border-rule-soft">
	<TabBar {tabs} current={tab} label="Template kinds" />
</div>

{#snippet noMatch(what: string, clear: () => void)}
	<div class="rows mt-4"><div class="rows-empty">
		<span>No {what} {needle ? `match “${q.trim()}”` : 'in this filter'}.</span>
		<button class="link" onclick={clear}>Clear the search</button>
	</div></div>
{/snippet}

{#snippet retry(error: string, again: () => void)}
	<Notice tone="fail" class="mt-4" live>
		{error}
		{#snippet action()}<button class="btn btn-sm" onclick={again}>Try again</button>{/snippet}
	</Notice>
{/snippet}

<section class="mt-4" aria-label={tabs.find((t) => t.id === tab)?.label}>
	{#if tab === 'bots'}
		{#if bots.error}{@render retry(bots.error, loadBots)}{/if}
		{#if bots.list === null}
			<Skeleton rows={3} label="Loading bot templates" />
		{:else if bots.list.length === 0 && !bots.error}
			<div class="rows"><div class="rows-empty">No bot templates are installed on this panel. Create an empty bot and upload your code instead.{#if can('bots.create')}<a class="link" href="/bots/new?source=blank">New empty bot</a>{/if}</div></div>
		{:else if bots.list.length}
			<div class="flex flex-wrap items-center justify-between gap-2">
				<p class="max-w-2xl text-small text-muted">Each starter registers a <code>/ping</code> command and reads its token from the environment. Pick one, paste your token, start it.</p>
				{#if langs.length > 1}
					<div class="flex flex-wrap gap-1 rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Language">
						{#each ['', ...langs] as l (l)}
							<button class="rounded-inner px-2.5 py-1 text-small font-medium {lang === l ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={lang === l} onclick={() => (lang = l)}>{l || 'All'}</button>
						{/each}
					</div>
				{/if}
			</div>
			{#if shownBots.length === 0}
				{@render noMatch('bot templates', () => ((q = ''), (lang = '')))}
			{:else}
				<ul class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
					{#each shownBots as t (t.id)}
						{@const c = accent[t.language] ?? 'var(--color-data)'}
						<li class="flex flex-col rounded-tile border border-rule-soft bg-panel p-4">
							<div class="flex items-start gap-3">
								<span class="grid size-9 shrink-0 place-items-center rounded-control font-mono text-[0.75rem] font-medium" style="background: color-mix(in srgb, {c} 14%, transparent); color: {c}" aria-hidden="true">{abbr[t.language] ?? t.language.slice(0, 2).toUpperCase()}</span>
								<div class="min-w-0">
									<h2 class="font-medium">{t.name}</h2>
									<p class="text-small text-muted">{t.language}, {t.tested_with}</p>
								</div>
							</div>
							<p class="mt-3 text-small">{t.description}</p>
							<dl class="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-small">
								<div class="flex gap-1.5"><dt class="text-muted">Memory</dt><dd class="font-mono">{fmtBytes(t.default_memory_bytes)}</dd></div>
								{#if t.build_memory_bytes}<div class="flex gap-1.5"><dt class="text-muted">Build</dt><dd class="font-mono">{fmtBytes(t.build_memory_bytes)}</dd></div>{/if}
								{#if t.privileged_intents.length}<div class="flex gap-1.5"><dt class="text-muted">Intents</dt><dd>{t.privileged_intents.join(', ')}</dd></div>{/if}
							</dl>
							<div class="mt-auto flex items-center justify-between gap-2 pt-4">
								<span class="truncate text-small text-muted">{t.env.filter((e) => e.required).length} required {t.env.filter((e) => e.required).length === 1 ? 'setting' : 'settings'}</span>
								{#if can('bots.create')}<a href="/bots/new?source=template&template={t.id}" class="btn btn-sm">Create bot</a>{/if}
							</div>
						</li>
					{/each}
				</ul>
			{/if}
		{/if}
	{:else if tab === 'games'}
		{#if games.error}{@render retry(games.error, loadGames)}{/if}
		<div class="flex flex-wrap items-center gap-2">
			<p class="mr-auto max-w-2xl text-small text-muted">Server types install the game and pick the right Java or SteamCMD image automatically.{manager ? '' : ' Administrators can add more by importing Pterodactyl eggs.'}</p>
			{#if families.length > 2}
				<div class="flex flex-wrap gap-1 rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Game family">
					{#each families as f (f.id)}
						<button class="rounded-inner px-2.5 py-1 text-small font-medium {family === f.id ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={family === f.id} onclick={() => (family = f.id)}>{f.label}</button>
					{/each}
				</div>
			{/if}
			{#if manager}
				<a class="btn btn-sm" href="/admin/blueprints?import=egg"><Icon name="upload" size={14} />Import egg</a>
				<a class="btn btn-sm btn-quiet" href="/admin/blueprints">Manage server types{hiddenTypes ? ` (${hiddenTypes} hidden)` : ''}</a>
			{/if}
		</div>
		{#if games.list === null}
			<div class="mt-4"><Skeleton rows={5} label="Loading server types" /></div>
		{:else if games.list.length === 0 && !games.error}
			<div class="rows mt-4"><div class="rows-empty">
				<span>{manager ? 'No server type is offered. Offer a built-in type or import a Pterodactyl egg.' : 'An administrator has not enabled any server types yet.'}</span>
				{#if manager}<a class="link" href="/admin/blueprints">Open Server types</a>{/if}
			</div></div>
		{:else if shownGames.length === 0 && games.list.length}
			{@render noMatch('server types', () => ((q = ''), (family = '')))}
		{:else if shownGames.length}
			<ul class="rows mt-4" aria-label="Server types">
				{#each shownGames as b (b.id)}
					<li class="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
						<GameIcon type={b} size={40} />
						<div class="min-w-0 flex-1 basis-56">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<h2 class="font-medium">{b.name}</h2>
								<span class="pill">{b.category}</span>
								{#if b.source === 'custom'}<span class="pill" title="Imported from a Pterodactyl egg by an administrator">Imported</span>{/if}
								{#if b.spec.agreements?.length}<span class="pill" title="The game's licence must be accepted when creating the server">Licence to accept</span>{/if}
							</div>
							<p class="mt-0.5 line-clamp-2 text-small text-muted">{gameBlurb(b.slug) || b.description}</p>
						</div>
						<span class="hidden w-28 text-right text-small text-muted md:block">{b.spec.resources.memory_mb >= 1024 ? `${+(b.spec.resources.memory_mb / 1024).toFixed(1)} GiB` : `${b.spec.resources.memory_mb} MiB`} memory</span>
						{#if can('bots.create')}<a class="btn btn-sm ml-auto" href="/servers/new?type={encodeURIComponent(b.slug)}">Create server</a>{/if}
					</li>
				{/each}
			</ul>
		{/if}
	{:else if tab === 'sites'}
		{#if sites.error}{@render retry(sites.error, loadSites)}{/if}
		{#if sites.list === null}
			<Skeleton rows={3} label="Loading site templates" />
		{:else if sitesInfo && !sitesInfo.enabled}
			<div class="rows"><div class="rows-empty">
				<span>Site hosting is not enabled on this panel{session.user?.role === 'admin' ? '. Set RIVET_SITES_LISTEN and RIVET_SITES_BASE_URL and restart the panel to use site templates.' : '. An administrator has to turn it on first.'}</span>
				<a class="link" href="/docs#sites">Read the hosting guide</a>
			</div></div>
		{:else if sites.list.length === 0 && !sites.error}
			<div class="rows"><div class="rows-empty">No site templates are available. Create an empty site and upload your files instead.</div></div>
		{:else if shownSites.length === 0 && sites.list.length}
			{@render noMatch('site templates', () => (q = ''))}
		{:else if shownSites.length}
			<p class="max-w-2xl text-small text-muted">Static starters: the files become your site's first release, live at its own address right away. Edit them under Files or replace them with an upload or a GitHub repository.</p>
			<ul class="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{#each shownSites as t (t.id)}
					<li class="flex flex-col overflow-hidden rounded-tile border border-rule-soft bg-panel">
						<button type="button" class="group relative block aspect-[16/10] w-full overflow-hidden border-b border-rule-soft text-left" style="background: {t.theme === 'dark' ? '#16161a' : '#fafafa'}" onclick={() => openPreview(t)} aria-label="Preview {t.name}">
							<div class="absolute inset-0" use:fit>
								<iframe
									src={t.preview_url}
									title="{t.name} preview"
									sandbox="allow-same-origin"
									loading="lazy"
									tabindex="-1"
									aria-hidden="true"
									referrerpolicy="no-referrer"
									class="pointer-events-none absolute top-0 left-0 origin-top-left border-0"
									style="width: 1280px; height: 800px; transform: scale(var(--fit, 0.25))"
								></iframe>
							</div>
							<span class="absolute right-2 bottom-2 rounded-control bg-raised/95 px-2 py-1 text-small font-medium text-ink opacity-0 shadow-overlay transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">Open preview</span>
						</button>
						<div class="flex flex-1 flex-col p-4">
							<div class="flex items-center gap-2">
								<span class="size-2.5 shrink-0 rounded-pill" style="background: {t.accent}" aria-hidden="true"></span>
								<h2 class="font-medium">{t.name}</h2>
							</div>
							<p class="mt-1 text-small text-muted">{t.description}</p>
							<div class="mt-3 flex flex-wrap gap-1.5">
								{#each t.tags as tag (tag)}<span class="pill">{tag}</span>{/each}
								{#if t.status_widget}<span class="pill" title="Shows this panel's public status page">Live status</span>{/if}
							</div>
							<div class="mt-auto flex items-center justify-between gap-2 pt-4">
								<span class="text-small text-muted">{t.pages.length} {t.pages.length === 1 ? 'page' : 'pages'}, {fmtBytes(t.bytes)}</span>
								<div class="flex gap-1.5">
									<button class="btn btn-sm btn-quiet" onclick={() => openPreview(t)}><Icon name="eye" size={14} />Preview</button>
									{#if canSite}<button class="btn btn-sm" onclick={() => createSite(t.id)}>Create site</button>{/if}
								</div>
							</div>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}

	{#if elsewhere.length}
		<p class="mt-4 text-small text-muted">
			Also matching:
			{#each elsewhere as e, i (e.id)}{i ? ', ' : ' '}<button class="link" onclick={() => switchTo(e.id)}>{e.n} {e.n === 1 ? e.noun[0] : e.noun[1]}</button>{/each}
		</p>
	{/if}
</section>

<Dialog bind:open={previewOpen} title={previewing ? `${previewing.name} preview` : 'Preview'} size="lg">
	{#if previewing}
		{#if previewing.pages.length > 1}
			<div class="mb-3 flex flex-wrap gap-1 rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Page">
				{#each previewing.pages as p (p)}
					<button class="rounded-inner px-2.5 py-1 font-mono text-small {previewPage === p ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={previewPage === p} onclick={() => (previewPage = p)}>{p}</button>
				{/each}
			</div>
		{/if}
		<div class="overflow-hidden rounded-control border border-rule-soft">
			<iframe src={previewSrc(previewing, previewPage)} title="{previewing.name}, {previewPage}" sandbox="allow-same-origin" referrerpolicy="no-referrer" class="block h-[min(65vh,40rem)] w-full border-0 bg-white"></iframe>
		</div>
		<p class="mt-2 text-small text-muted">A static rendering without scripts. Your site shows its real name, year{previewing.status_widget ? ' and this panel’s live status' : ''}.</p>
	{/if}
	{#snippet footer()}
		<button class="btn" onclick={() => (previewOpen = false)}>Close</button>
		{#if canSite && previewing}<button class="btn btn-primary" onclick={() => { previewOpen = false; createSite(previewing!.id); }}>Create site from this template</button>{/if}
	{/snippet}
</Dialog>

{#if sitesInfo?.enabled}<NewSiteDialog bind:open={creating} info={sitesInfo} templates={sites.list ?? []} template={createFor} />{/if}
