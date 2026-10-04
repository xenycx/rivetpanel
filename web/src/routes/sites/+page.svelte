<script lang="ts">
	// Sites without any logo or favicon answer 404; show the globe instead.
	let noIcon = $state<Record<string, boolean>>({});
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Site, SitesInfo } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { creatable, currentWorkspace, loadWorkspaces, selectWorkspace, workspaceName, workspaces } from '$lib/workspaces.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let info = $state<SitesInfo | null>(null);
	let sites = $state<Site[] | null>(null);
	let error = $state('');
	let q = $state('');
	let open = $state(false);
	let form = $state({ name: '', slug: '', domain_id: '', workspace_id: '', spa: false });
	let slugTouched = $state(false);
	let formError = $state('');
	let busy = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			info = await api<SitesInfo>('GET', '/sites-info');
			if (info.enabled) sites = (await api<{ sites: Site[] }>('GET', '/sites')).sites;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		loadWorkspaces();
	});

	const scoped = $derived((sites ?? []).filter((s) => workspaces.selected === 'all' || s.workspace_id === workspaces.selected));
	const shown = $derived(scoped.filter((s) => !q.trim() || (s.name + ' ' + s.slug + ' ' + s.url).toLowerCase().includes(q.trim().toLowerCase())));
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	const suggest = (n: string) => n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30);
	const bases = $derived(info?.domains ?? []);
	const exampleURL = $derived(bases.find((b) => b.id === form.domain_id)?.example_url ?? info?.example_url ?? '');
	const preview = $derived(exampleURL.replace('://example.', `://${(slugTouched ? form.slug : suggest(form.name)) || 'your-site'}.`));

	function start() {
		const ws = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
		form = { name: '', slug: '', domain_id: bases.find((b) => b.primary)?.id ?? '', workspace_id: ws, spa: false };
		slugTouched = false;
		formError = '';
		open = true;
	}
	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			const s = await api<Site>('POST', '/sites', { ...form, slug: slugTouched ? form.slug : '' });
			open = false;
			goto(`/sites/${s.id}`);
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	const status = (s: Site) => (s.disabled ? { tone: 'fail', label: 'Suspended' } : s.current_release ? { tone: 'run', label: 'Live' } : { tone: undefined, label: 'Empty' });
</script>

<svelte:head><title>Sites · RivetPanel</title></svelte:head>

<section class="card card-glow grid gap-6 p-6 sm:p-8 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
	<div>
		<p class="eyebrow">Static sites{scope ? ` · ${scope.personal ? 'Personal workspace' : scope.name}` : ''}</p>
		<h1 class="mt-2 text-[2rem] leading-tight font-semibold tracking-tight">Publish a <span class="text-action">website</span>.</h1>
		<p class="mt-2 max-w-prose text-muted">Host a bot's dashboard, documentation or landing page: upload a ZIP of built files or deploy a GitHub branch, then add your own domain.</p>
	</div>
	{#if info?.enabled}<button class="btn btn-primary" onclick={start}><Icon name="plus" />New site</button>{/if}
</section>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if info && !info.enabled}
	<div class="mt-5">
		<EmptyState title="Site hosting is not enabled on this panel">
			{#if session.user?.role === 'admin'}
				<p>Set <code>RIVET_SITES_LISTEN</code> and <code>RIVET_SITES_BASE_URL</code>, point a wildcard DNS record and your reverse proxy at the sites listener, and restart the panel. The documentation explains the setup, including automatic certificates for custom domains.</p>
			{:else}
				<p>An administrator has to turn on static site hosting first.</p>
			{/if}
			{#snippet actions()}<a class="btn" href="/docs#sites"><Icon name="book" size={14} />Read the hosting guide</a>{/snippet}
		</EmptyState>
	</div>
{:else if info}
	{#if sites && sites.length > 0}
		<div class="mt-5 flex flex-wrap items-center gap-2">
			<label class="relative min-w-0 flex-1 basis-60">
				<span class="sr-only">Search sites</span>
				<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
				<input class="field pl-8" type="search" placeholder="Search by name or address" bind:value={q} />
			</label>
		</div>
	{/if}
	<section class="mt-4" aria-label="Sites">
		{#if sites === null}
			<Skeleton rows={3} label="Loading sites" />
		{:else if scoped.length === 0}
			<EmptyState title={sites.length ? 'No sites in this workspace yet' : 'Create your first site'}>
				<p>A site gets its own address at <code>{info.example_url?.replace('://example.', '://your-site.')}</code>. Upload a ZIP of your built site (for example the <code>dist</code> folder) and it is live within seconds.</p>
				{#snippet actions()}
					<button class="btn btn-primary" onclick={start}><Icon name="plus" />New site</button>
					{#if sites?.length}<button class="btn" onclick={() => selectWorkspace('all')}>Show all workspaces</button>{/if}
				{/snippet}
			</EmptyState>
		{:else}
			<ul class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{#each shown as s (s.id)}
					{@const st = status(s)}
					<li class="card flex flex-col p-5">
						<div class="flex items-start justify-between gap-2">
							{#if !noIcon[s.id]}
								<img src={s.icon_url} alt="" class="size-10 shrink-0 rounded-tile object-cover" referrerpolicy="no-referrer" onerror={() => (noIcon[s.id] = true)} />
							{:else}
								<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 text-action" aria-hidden="true"><Icon name="globe" /></span>
							{/if}
							<span class="pill" data-tone={st.tone}>{st.label}</span>
						</div>
						<a href="/sites/{s.id}" class="mt-4 truncate text-title font-semibold hover:underline">{s.name}</a>
						<a href={s.url} target="_blank" rel="noopener" class="truncate font-mono text-small text-action hover:underline">{s.url.replace(/^https?:\/\//, '')}</a>
						<dl class="mt-4 grid grid-cols-3 gap-2 border-t border-rule-soft pt-4">
							<div><dt class="eyebrow">Size</dt><dd class="mt-0.5 font-mono text-small font-medium">{s.current_release ? fmtBytes(s.release_bytes) : '—'}</dd></div>
							<div><dt class="eyebrow">Domains</dt><dd class="mt-0.5 font-mono text-small font-medium">{s.domains}</dd></div>
							<div><dt class="eyebrow">Source</dt><dd class="mt-0.5 truncate text-small font-medium">{s.repo_full_name ? 'GitHub' : 'Upload'}</dd></div>
						</dl>
						<p class="mt-3 truncate text-small text-muted">Updated {fmtAgo(s.updated_at_ms)}{multi && workspaceName(s.workspace_id) ? ` · ${workspaceName(s.workspace_id)}` : ''}</p>
						<div class="mt-auto flex gap-2 pt-4">
							<a href="/sites/{s.id}" class="btn btn-primary flex-1">Manage</a>
							<a href={s.url} target="_blank" rel="noopener" class="btn btn-icon" aria-label="Open {s.name}" title="Open site"><Icon name="external" size={14} /></a>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
{/if}

<Dialog bind:open title="New site" size="md">
	<form id="site-form" class="grid gap-4" onsubmit={create}>
		<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={form.name} placeholder="Bot dashboard" /></label>
		<label class="block">
			<span class="label">Address</span>
			<div class="flex items-stretch">
				<input class="field min-w-0 font-mono {bases.length > 1 ? 'rounded-r-none' : ''}" maxlength="40" value={slugTouched ? form.slug : suggest(form.name)} oninput={(e) => { slugTouched = true; form.slug = e.currentTarget.value.toLowerCase(); }} placeholder="bot-dashboard" />
				{#if bases.length > 1}
					<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={form.domain_id} aria-label="Sites domain">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}{b.label ? ` (${b.label})` : ''}</option>{/each}</select>
				{/if}
			</div>
			<span class="help">Served at <code class="text-ink">{preview}</code>. Lower-case letters, digits and hyphens. You can change it, or add your own domain, afterwards.</span>
		</label>
		{#if creatable().length > 1}
			<label class="block"><span class="label">Workspace</span>
				<select class="field" bind:value={form.workspace_id}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{w.personal ? 'Personal' : w.name}</option>{/each}</select>
			</label>
		{/if}
		<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={form.spa} /><span>Single-page application<span class="help mt-0">Unknown paths serve <code>index.html</code>, for React, Vue or Svelte apps with client-side routing.</span></span></label>
		{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="site-form" disabled={busy || !form.name.trim()}>Create site</button>
	{/snippet}
</Dialog>
