<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Site, SiteBaseChoice, SiteDomain, SiteRelease, SitesInfo, WorkspaceRole } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Files from '$lib/components/Files.svelte';
	import Analytics from '$lib/components/Analytics.svelte';

	let {
		botId,
		botName,
		canEdit,
		canAdmin,
		stopped
	}: { botId: string; botName: string; canEdit: boolean; canAdmin: boolean; stopped: boolean } = $props();

	type Detail = {
		site: Site;
		role: WorkspaceRole;
		domains: SiteDomain[];
		releases: SiteRelease[];
		deploy: { running: boolean; last_error: string; finished_at_ms: number };
	};
	type View = 'design' | 'files' | 'insights';
	let view = $state<View>('design');
	let d = $state<Detail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let creating = $state(false);
	let saving = $state(false);
	let publishing = $state(false);
	let slug = $state('');
	let bases = $state<SiteBaseChoice[]>([]);
	let domainId = $state('');
	let previewKey = $state(0);
	let form = $state({
		page_title: '',
		page_description: '',
		page_theme: 'midnight' as Site['page_theme'],
		page_accent: '#5865f2',
		page_html: '',
		page_css: '',
		widgets_public: false
	});
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const suggestedSlug = $derived(
		(botName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 36) || 'bot') + '-page'
	);
	const live = $derived(!!d && !d.site.disabled && (d.site.mode === 'page' || !!d.site.current_release));
	const previewURL = $derived(d ? `${d.site.url}?preview=${previewKey}` : '');

	function sync() {
		if (!d) return;
		form = {
			page_title: d.site.page_title,
			page_description: d.site.page_description,
			page_theme: d.site.page_theme,
			page_accent: d.site.page_accent,
			page_html: d.site.page_html,
			page_css: d.site.page_css,
			widgets_public: d.site.widgets_public
		};
	}

	async function load() {
		if (!session.features.sites) {
			loading = false;
			return;
		}
		try {
			d = await api<Detail>('GET', `/bots/${botId}/site`);
			error = '';
			sync();
		} catch (e) {
			if (!(e instanceof ApiError && e.status === 404)) error = msg(e);
			else loadBases();
		} finally {
			loading = false;
		}
	}
	async function loadBases() {
		try {
			bases = (await api<SitesInfo>('GET', '/sites-info')).domains ?? [];
			domainId = bases.find((b) => b.primary)?.id ?? bases[0]?.id ?? '';
		} catch {
			bases = [];
		}
	}
	onMount(load);

	async function createPage(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			d = await api<Detail>('POST', `/bots/${botId}/site`, { slug: slug || suggestedSlug, domain_id: domainId });
			sync();
			previewKey++;
			toast('Public page created', 'success');
		} catch (e) {
			error = msg(e);
		} finally {
			creating = false;
		}
	}

	async function saveDesign(e?: SubmitEvent) {
		e?.preventDefault();
		if (!d) return;
		saving = true;
		try {
			d = await api<Detail>('PATCH', `/sites/${d.site.id}`, form);
			sync();
			previewKey++;
			toast('Public page updated', 'success');
		} catch (e) {
			error = msg(e);
		} finally {
			saving = false;
		}
	}

	async function setMode(mode: Site['mode']) {
		if (!d || !canEdit || d.site.mode === mode) return;
		try {
			d = await api<Detail>('PATCH', `/sites/${d.site.id}`, { mode });
			view = mode === 'files' ? 'files' : 'design';
			previewKey++;
			toast(mode === 'page' ? 'Generated page is live' : 'Custom files mode selected');
		} catch (e) {
			error = msg(e);
		}
	}

	async function publishFiles() {
		if (!d) return;
		publishing = true;
		try {
			const r = await api<{ files: number; bytes: number }>('POST', `/sites/${d.site.id}/files/publish`);
			await load();
			previewKey++;
			toast(`Published ${r.files} files (${fmtBytes(r.bytes)})`, 'success');
		} catch (e) {
			error = msg(e);
		} finally {
			publishing = false;
		}
	}
</script>

{#if !session.features.sites}
	<Notice title="Public sites are not enabled on this panel">
		The administrator needs to configure the separate Sites listener before this bot can have a public page. Private telemetry and widgets are still available below.
	</Notice>
	{#if session.features.analytics}<div class="mt-5"><Analytics {botId} {canAdmin} {stopped} /></div>{/if}
{:else if loading}
	<Skeleton rows={5} label="Loading public page" />
{:else if !d}
	<div class="overflow-hidden rounded-card border border-rule bg-term text-term-ink">
		<div class="grid min-h-[30rem] items-center gap-8 p-7 sm:p-10 lg:grid-cols-[minmax(0,1.15fr)_minmax(18rem,.85fr)]">
			<div>
				<div class="inline-flex items-center gap-2 rounded-pill border border-white/15 px-3 py-1 text-small text-white/70"><Icon name="globe" size={14} />A site made for this bot</div>
				<h2 class="mt-6 max-w-[10ch] text-4xl font-semibold leading-[.95] tracking-[-.045em] sm:text-6xl">Give {botName} a place of its own.</h2>
				<p class="mt-5 max-w-xl text-base leading-7 text-white/65">Create a public home for commands, live status, community links and the widgets your bot publishes. No console logs, secrets, owner details or private analytics are exposed.</p>
			</div>
			{#if canEdit}
				<form class="rounded-tile border border-white/12 bg-white/6 p-5 backdrop-blur" onsubmit={createPage}>
					<h3 class="text-title font-semibold">Choose the address</h3>
					<p class="mt-1 text-small text-white/55">You can add a custom domain after creation.</p>
					<label class="mt-5 block"><span class="label !text-white/65">Site address</span><div class="flex items-stretch"><input class="field rounded-r-none border-white/20 bg-black/20 text-white" bind:value={slug} placeholder={suggestedSlug} />{#if bases.length > 1}<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 border-white/20 bg-black/20 text-small text-white/80" bind:value={domainId} aria-label="Sites domain">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}</select>{:else}<span class="flex items-center rounded-r-control border border-l-0 border-white/20 px-3 text-small text-white/45">.{bases[0]?.domain ?? 'sites'}</span>{/if}</div></label>
					<button class="btn btn-primary mt-4 w-full" disabled={creating}>{creating ? 'Creating…' : 'Create public page'}</button>
				</form>
			{:else}
				<p class="rounded-tile border border-white/12 bg-white/6 p-5 text-white/65">A developer with file access can create this bot’s public page.</p>
			{/if}
		</div>
	</div>
	{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{:else}
	<div class="mb-5 flex flex-wrap items-center gap-3 border-b border-rule pb-4">
		<div class="min-w-48 flex-1">
			<div class="flex flex-wrap items-center gap-2"><h2 class="text-section">Public page</h2><span class="pill" data-tone={d.site.disabled ? 'fail' : live ? 'run' : 'warn'}>{d.site.disabled ? 'Suspended' : live ? 'Live' : 'Draft'}</span></div>
			<a class="mt-0.5 inline-flex max-w-full items-center gap-1 font-mono text-small text-action hover:underline" href={d.site.url} target="_blank" rel="noopener"><span class="truncate">{d.site.url}</span><Icon name="external" size={12} /></a>
		</div>
		<a class="btn btn-sm" href={`/sites/${d.site.id}`}><Icon name="sliders" size={14} />Address, domains & releases</a>
		<a class="btn btn-sm" href={d.site.url} target="_blank" rel="noopener"><Icon name="external" size={14} />Open page</a>
	</div>

	<div class="mb-5 flex flex-wrap gap-1 border-b border-rule-soft" role="tablist" aria-label="Public page tools">
		{#each [{ id: 'design', label: 'Page studio', icon: 'pencil' }, ...(canEdit ? [{ id: 'files', label: 'Site files', icon: 'file' }] : []), ...(session.features.analytics ? [{ id: 'insights', label: 'Private insights', icon: 'chart' }] : [])] as item}
			<button role="tab" aria-selected={view === item.id} class="flex items-center gap-2 border-b-2 px-3 py-2.5 {view === item.id ? 'border-action text-ink' : 'border-transparent text-muted hover:text-ink'}" onclick={() => (view = item.id as View)}><Icon name={item.icon as 'pencil'} size={14} />{item.label}</button>
		{/each}
	</div>
	{#if error}<Notice tone="fail" class="mb-4">{error}</Notice>{/if}

	{#if view === 'design'}
		<div class="grid gap-5 xl:grid-cols-[minmax(22rem,.82fr)_minmax(30rem,1.18fr)]">
			<div class="min-w-0">
				<div class="mb-4 grid grid-cols-2 gap-1 rounded-control bg-paper-2 p-1" role="group" aria-label="Site mode">
					<button class="rounded-inner px-3 py-2 text-small font-medium {d.site.mode === 'page' ? 'bg-raised shadow-sm' : 'text-muted'}" onclick={() => setMode('page')} disabled={!canEdit}>Generated page</button>
					<button class="rounded-inner px-3 py-2 text-small font-medium {d.site.mode === 'files' ? 'bg-raised shadow-sm' : 'text-muted'}" onclick={() => setMode('files')} disabled={!canEdit}>Custom files</button>
				</div>
				{#if d.site.mode === 'page'}
					<form class="grid gap-4" onsubmit={saveDesign}>
						<fieldset disabled={!canEdit} class="contents">
						<label class="block"><span class="label">Headline</span><input class="field" maxlength="80" bind:value={form.page_title} placeholder={botName} /></label>
						<label class="block"><span class="label">Introduction</span><textarea class="field min-h-24 resize-y" maxlength="500" bind:value={form.page_description}></textarea><span class="help">This is public. Keep private operations and support details out.</span></label>
						<div class="grid grid-cols-[minmax(0,1fr)_7rem] gap-3">
							<label><span class="label">Visual theme</span><select class="field" bind:value={form.page_theme}><option value="midnight">Midnight</option><option value="daylight">Daylight</option><option value="system">Visitor’s system</option></select></label>
							<label><span class="label">Accent</span><input class="field h-[2.6rem] p-1" type="color" bind:value={form.page_accent} /></label>
						</div>
						<label class="rounded-tile border border-rule-soft bg-panel p-3"><span class="flex items-start gap-3"><input class="mt-0.5" type="checkbox" bind:checked={form.widgets_public} /><span><strong class="block font-medium">Publish bot widgets</strong><span class="text-small text-muted">Only declarative widgets are exposed. Raw stats, command usage, events, console output and configuration remain private.</span></span></span></label>
						<details class="rounded-tile border border-rule-soft bg-panel p-3">
							<summary class="font-medium">Custom HTML section</summary>
							<p class="mt-2 text-small text-muted">Rendered after the hero on the isolated Sites origin. Scripts are allowed there, never inside the panel.</p>
							<textarea class="field mt-3 min-h-48 resize-y font-mono text-[12px]" maxlength="65536" bind:value={form.page_html} placeholder={'<section class="community-links">…</section>'}></textarea>
						</details>
						<details class="rounded-tile border border-rule-soft bg-panel p-3">
							<summary class="font-medium">Custom CSS</summary>
							<p class="mt-2 text-small text-muted">Target a widget with <code>[data-widget="latency"]</code> or a kind with <code>.kind-metric</code>. The page exposes <code>--accent</code>.</p>
							<textarea class="field mt-3 min-h-56 resize-y font-mono text-[12px]" maxlength="32768" bind:value={form.page_css} placeholder={'[data-widget="latency"] { grid-column: span 2; }'}></textarea>
						</details>
						<button class="btn btn-primary" disabled={saving}>{saving ? 'Saving…' : 'Save and publish'}</button>
						</fieldset>
					</form>
					{#if !canEdit}<p class="mt-3 text-small text-muted">You have read-only access. A developer can change and publish this page.</p>{/if}
				{:else}
					<Notice title="Custom files are serving this address">Use Site files to edit HTML, CSS, JavaScript and assets, then publish an immutable release. Switch back to Generated page at any time without losing the files.</Notice>
					{#if canEdit}<button class="btn btn-primary mt-3" onclick={() => (view = 'files')}>Open site files</button>{/if}
				{/if}
			</div>
			<div class="min-w-0 xl:sticky xl:top-28 xl:self-start">
				<div class="flex items-center justify-between gap-3 pb-2"><p class="text-small font-medium">Live preview</p><button class="btn btn-sm btn-quiet" onclick={() => previewKey++}><Icon name="restart" size={13} />Refresh</button></div>
				<div class="overflow-hidden rounded-card border border-rule bg-[#090c14] shadow-[0_24px_80px_-40px_rgba(0,0,0,.7)]">
					<div class="flex items-center gap-1.5 border-b border-white/10 bg-[#111724] px-3 py-2"><span class="size-2 rounded-pill bg-[#ff6b63]"></span><span class="size-2 rounded-pill bg-[#ffbd4a]"></span><span class="size-2 rounded-pill bg-[#35c98b]"></span><span class="ml-2 truncate font-mono text-[10px] text-white/45">{d.site.url}</span></div>
					<iframe class="h-[38rem] w-full bg-white" title="Public page preview" src={previewURL}></iframe>
				</div>
				<p class="mt-2 text-small text-muted">The preview is isolated on the public Sites origin. It cannot read this panel or its session.</p>
			</div>
		</div>
	{:else if view === 'files'}
		<div class="mb-4 flex flex-wrap items-center gap-3 rounded-tile border border-rule-soft bg-panel p-3">
			<div class="min-w-48 flex-1"><p class="font-medium">Private working draft</p><p class="text-small text-muted">Saving a file does not change the live site. Publish when the draft is ready; every publish remains available for rollback.</p></div>
			<button class="btn btn-primary" onclick={publishFiles} disabled={publishing}><Icon name="rocket" size={14} />{publishing ? 'Publishing…' : 'Publish draft'}</button>
			{#if d.site.mode !== 'files'}<button class="btn" onclick={() => setMode('files')}>Use custom files live</button>{/if}
		</div>
		<Files basePath={`/sites/${d.site.id}/files`} rootLabel="site draft" saveNote="Publish the draft when it is ready." />
	{:else if view === 'insights' && session.features.analytics}
		<Notice class="mb-4" title="Private operator data">This view is visible only to people with bot access. The public page can receive declarative widgets only when you enable them in Page studio.</Notice>
		<Analytics {botId} {canAdmin} {stopped} />
	{/if}
{/if}
