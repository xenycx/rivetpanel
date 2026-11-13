<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Site, SitesInfo, SiteTemplate } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { can, session } from '$lib/session.svelte';
	import { currentWorkspace, loadWorkspaces, selectWorkspace, workspaceName, workspaces } from '$lib/workspaces.svelte';
	import NewSiteDialog from '$lib/components/NewSiteDialog.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import { logoSeed } from '$lib/avatar';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let info = $state<SitesInfo | null>(null);
	let sites = $state<Site[] | null>(null);
	let error = $state('');
	let q = $state('');
	let open = $state(false);
	let templates = $state<SiteTemplate[]>([]);
	let template = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			info = await api<SitesInfo>('GET', '/sites-info');
			if (info.enabled) {
				sites = (await api<{ sites: Site[] }>('GET', '/sites')).sites;
				// Templates are optional: "Start from" is simply not offered without them.
				api<{ templates: SiteTemplate[] }>('GET', '/site-templates')
					.then((r) => (templates = r.templates))
					.catch(() => {});
			}
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(async () => {
		loadWorkspaces();
		await load();
		// Opened from the "New" menu: start with the new-site form
		// (?template=<id> preselects a site template).
		const qs = new URLSearchParams(location.search);
		if (info?.enabled && can('sites.create') && qs.get('new') === '1') {
			if (qs.get('template'))
				templates = await api<{ templates: SiteTemplate[] }>('GET', '/site-templates')
					.then((r) => r.templates)
					.catch(() => templates);
			start(qs.get('template') ?? '');
		}
	});

	const scoped = $derived((sites ?? []).filter((s) => workspaces.selected === 'all' || s.workspace_id === workspaces.selected));
	const shown = $derived(scoped.filter((s) => !q.trim() || (s.name + ' ' + s.slug + ' ' + s.url).toLowerCase().includes(q.trim().toLowerCase())));
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	function start(id = '') {
		template = id;
		open = true;
	}
	const appStates: Record<string, { tone?: string; label: string }> = {
		running: { tone: 'run', label: 'Running' },
		building: { tone: 'warn', label: 'Building' },
		starting: { tone: 'warn', label: 'Starting' },
		failed: { tone: 'fail', label: 'Failed' }
	};
	const status = (s: Site) =>
		s.disabled
			? { tone: 'fail', label: 'Suspended' }
			: s.mode === 'app'
				? (appStates[s.app_state] ?? { tone: undefined, label: 'Stopped' })
				: s.current_release
					? { tone: 'run', label: 'Live' }
					: { tone: undefined, label: 'Empty' };
	const kindLabel = (s: Site) => (s.mode === 'app' ? `App${s.framework ? ` · ${s.framework}` : ''}` : s.app_id ? `Build${s.framework ? ` · ${s.framework}` : ''}` : s.repo_full_name ? 'GitHub' : 'Upload');
</script>

<div class="mt-1 flex flex-wrap items-center gap-2">
	{#if sites}<p class="text-small text-muted">{scoped.length} {scoped.length === 1 ? 'website' : 'websites'}{scope ? `, ${scope.personal ? 'personal workspace' : scope.name}` : ''}</p>{/if}
	{#if info?.enabled && can('sites.create')}
		<div class="ml-auto flex flex-wrap gap-2">
			<a class="btn btn-sm" href="/templates?tab=sites"><Icon name="layers" size={14} />Templates</a>
			{#if info.builds_enabled}<a class="btn btn-sm" href="/sites/new"><Icon name="github" size={14} />From GitHub</a>{:else}<span class="btn btn-sm" aria-disabled="true" title="Building and running sites from GitHub needs the Docker runner, which is not running on this panel"><Icon name="github" size={14} />From GitHub</span>{/if}
			<button class="btn btn-sm" onclick={() => start()}><Icon name="upload" size={14} />Upload files</button>
		</div>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if info && !info.enabled}
	<div class="mt-5">
		<EmptyState title="Site hosting is not enabled on this panel">
			{#if session.user?.role === 'admin'}
				<p>Set <code>RIVET_SITES_LISTEN</code> and <code>RIVET_SITES_BASE_URL</code>, point a wildcard DNS record and your reverse proxy at the sites listener, and restart the panel. The documentation explains the setup, including automatic certificates for custom domains.</p>
			{:else}
				<p>An administrator has to turn on site hosting first.</p>
			{/if}
			{#snippet actions()}<a class="btn" href="/docs/websites"><Icon name="book" size={14} />Read the hosting guide</a>{/snippet}
		</EmptyState>
	</div>
{:else if info}
	{#if sites && sites.length > 0}
		<div class="mt-5 flex flex-wrap items-center gap-2">
			<label class="relative min-w-0 flex-1 basis-60">
				<span class="sr-only">Search websites</span>
				<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
				<input class="field pl-8" type="search" placeholder="Search by name or address" bind:value={q} />
			</label>
		</div>
	{/if}
	<section class="mt-4" aria-label="Websites">
		{#if sites === null}
			<Skeleton rows={3} label="Loading websites" />
		{:else if scoped.length === 0}
			<div class="rows"><div class="rows-empty">
				<span>{sites.length ? 'No websites in this workspace yet.' : 'No websites yet.'} A site gets its own address at <code>{info.example_url?.replace('://example.', '://your-site.')}</code>; upload a ZIP of your built files and it is live within seconds{info.builds_enabled ? ', or deploy a GitHub repository that is built on every push or runs as a web app (Node.js, PHP, Python, Go, Rust)' : ''}{templates.length ? ', or start from one of the ready-made templates' : ''}.</span>
				{#if templates.length && can('sites.create')}<a class="link" href="/templates?tab=sites">Browse site templates</a>{/if}
				{#if sites?.length}<button class="link" onclick={() => selectWorkspace('all')}>Show all workspaces</button>{/if}
			</div></div>
		{:else}
			<div class="rows" role="table" aria-label="Websites">
				<div class="rows-head hidden grid-cols-[minmax(0,1.4fr)_7rem_minmax(0,1.4fr)_5rem_4.5rem_7rem_2.25rem] items-center gap-x-4 px-4 py-2 md:grid" role="row">
					<span role="columnheader">Name</span><span role="columnheader">Status</span><span role="columnheader">Address</span><span role="columnheader">Size</span><span role="columnheader">Domains</span><span role="columnheader">Updated</span><span role="columnheader" class="sr-only">Open</span>
				</div>
				{#each shown as s (s.id)}
					{@const st = status(s)}
					<div class="row-link grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-4 px-4 py-2.5 md:grid-cols-[minmax(0,1.4fr)_7rem_minmax(0,1.4fr)_5rem_4.5rem_7rem_2.25rem]" role="row">
						<div class="flex min-w-0 items-center gap-3" role="cell">
							<Logo src={s.icon_url} kind="site" seed={logoSeed(s.id, s.logo_variant)} class="size-8 rounded-control" />
							<div class="min-w-0">
								<a href="/sites/{s.id}" class="block truncate font-medium hover:underline">{s.name}</a>
								<p class="truncate text-small text-muted">{kindLabel(s)}{multi && workspaceName(s.workspace_id) ? `, ${workspaceName(s.workspace_id)}` : ''}</p>
							</div>
						</div>
						<span role="cell"><span class="pill" data-tone={st.tone}>{st.label}</span></span>
						<a href={s.url} target="_blank" rel="noopener" class="hidden truncate font-mono text-small hover:underline md:block" role="cell">{s.url.replace(/^https?:\/\//, '')}</a>
						<span class="hidden font-mono text-small md:block" role="cell">{s.mode !== 'app' && s.current_release ? fmtBytes(s.release_bytes) : '–'}</span>
						<span class="hidden font-mono text-small md:block" role="cell">{s.domains}</span>
						<span class="hidden text-small text-muted md:block" role="cell">{fmtAgo(s.updated_at_ms)}</span>
						<a href={s.url} target="_blank" rel="noopener" class="btn btn-quiet btn-icon btn-sm justify-self-end" aria-label="Open {s.name} in a new tab" title="Open site" role="cell"><Icon name="external" size={14} /></a>
					</div>
				{/each}
			</div>
		{/if}
	</section>
{/if}

{#if info?.enabled}<NewSiteDialog bind:open {info} {templates} {template} />{/if}
