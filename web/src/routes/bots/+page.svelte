<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import { type Bot, type RuntimeInfo } from '$lib/api/types';
	import { can as canDo, session } from '$lib/session.svelte';
	import { describe } from '$lib/status';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import ResourceTable from '$lib/components/ResourceTable.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { currentWorkspace, selectWorkspace, workspaceName, workspaces } from '$lib/workspaces.svelte';

	// Every Discord bot this account can see, with filters and batch power.
	let bots = $state<Bot[] | null>(null);
	let runtimes = $state<RuntimeInfo[]>([]);
	let error = $state('');
	let now = $state(Date.now());

	// Filters live in the URL so a filtered view can be bookmarked and shared.
	const params = page.url.searchParams;
	let q = $state(params.get('q') ?? '');
	let stateF = $state(params.get('state') ?? '');
	let runtimeF = $state(params.get('runtime') ?? '');
	let ownerF = $state(params.get('owner') ?? '');
	let sort = $state(params.get('sort') ?? 'attention');
	let tagF = $state(params.get('tag') ?? '');
	let filtersOpen = $state(false);

	let selected = $state<Record<string, boolean>>({});
	let batchAction = $state<'start' | 'stop' | 'restart' | null>(null);
	let batchOpen = $state(false);
	let batchResults = $state<{ bot_id: string; name: string; ok: boolean; message: string }[] | null>(null);
	let batchBusy = $state(false);

	$effect(() => {
		const u = new URL(location.href);
		for (const [k, v, d] of [
			['q', q.trim(), ''],
			['state', stateF, ''],
			['runtime', runtimeF, ''],
			['owner', ownerF, ''],
			['tag', tagF, ''],
			['sort', sort, 'attention']
		] as const) {
			if (v && v !== d) u.searchParams.set(k, v);
			else u.searchParams.delete(k);
		}
		if (u.href !== location.href) history.replaceState(history.state, '', u);
	});

	async function refresh() {
		try {
			bots = (await api<{ bots: Bot[] }>('GET', '/bots')).bots.filter((b) => b.kind !== 'game');
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The bot list could not be loaded. Check your connection.';
		}
	}
	onMount(() => {
		refresh();
		api<{ runtimes: RuntimeInfo[] }>('GET', '/runtimes').then((r) => (runtimes = r.runtimes)).catch(() => {});
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') refresh();
		}, 3000);
		return () => clearInterval(t);
	});

	const group = (b: Bot): 'running' | 'attention' | 'progress' | 'stopped' => {
		const d = describe(b, now);
		if (b.phase === 'running') return 'running';
		if (d.tone === 'fail' || b.phase === 'retrying') return 'attention';
		if (d.busy) return 'progress';
		return 'stopped';
	};
	const rank = { attention: 0, progress: 1, running: 2, stopped: 3 };
	const mine = (b: Bot) => b.owner_id === session.user?.id;
	const scoped = $derived((bots ?? []).filter((b) => workspaces.selected === 'all' || b.workspace_id === workspaces.selected));
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	const shown = $derived.by(() => {
		const needle = q.trim().toLowerCase();
		const out = scoped.filter(
			(b) =>
				(!needle || b.name.toLowerCase().includes(needle) || b.runtime.includes(needle)) &&
				(!stateF || group(b) === stateF) &&
				(!runtimeF || b.runtime === runtimeF) &&
				(!tagF || b.tags.includes(tagF)) &&
				(!ownerF || (ownerF === 'mine' ? mine(b) : ownerF === 'shared' ? b.shared : !mine(b) && !b.shared))
		);
		out.sort(
			(a, b) =>
				Number(b.favorite) - Number(a.favorite) ||
				(sort === 'name' ? a.name.localeCompare(b.name) : sort === 'newest' ? b.created_at_ms - a.created_at_ms : rank[group(a)] - rank[group(b)] || a.name.localeCompare(b.name))
		);
		return out;
	});
	const running = $derived(scoped.filter((b) => b.phase === 'running').length);
	const filtered = $derived(!!(q.trim() || stateF || runtimeF || ownerF || tagF));
	const allTags = $derived([...new Set(scoped.flatMap((b) => b.tags))].sort());
	const selectedBots = $derived((bots ?? []).filter((b) => selected[b.id]));
	const hasShared = $derived(scoped.some((b) => b.shared));
	const isAdmin = $derived(session.user?.role === 'admin');
	const note = (b: Bot) => (b.shared ? 'shared with you' : !mine(b) ? 'owned by another user' : multi ? workspaceName(b.workspace_id) : '');

	function clearFilters() {
		q = stateF = runtimeF = ownerF = tagF = '';
	}
	function openBatch(a: 'start' | 'stop' | 'restart') {
		batchAction = a;
		batchResults = null;
		batchOpen = true;
	}
	async function runBatch() {
		if (!batchAction) return;
		batchBusy = true;
		try {
			const r = await api<{ results: { bot_id: string; name: string; ok: boolean; message: string }[] }>('POST', '/bots/batch', {
				action: batchAction,
				ids: selectedBots.map((b) => b.id)
			});
			batchResults = r.results.map((x) => ({ ...x, name: x.name || bots?.find((b) => b.id === x.bot_id)?.name || x.bot_id }));
			if (r.results.every((x) => x.ok)) selected = {};
			await refresh();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The batch request failed.', 'fail');
			batchOpen = false;
		} finally {
			batchBusy = false;
		}
	}
</script>

<svelte:head><title>Bots · RivetPanel</title></svelte:head>

<header class="page-head">
	<h1 class="font-semibold">Bots</h1>
	{#if bots}<p class="text-small text-muted">{running} of {scoped.length} running{scope ? `, ${scope.personal ? 'personal workspace' : scope.name}` : ''}</p>{/if}
	{#if canDo('bots.create')}<a href="/bots/new" class="btn btn-primary ml-auto"><Icon name="plus" />New bot</a>{/if}
</header>

{#if bots && bots.length > 0}
	<div class="mt-4 flex flex-wrap items-center gap-2" role="search" aria-label="Filter bots">
		<label class="relative min-w-0 flex-1 basis-40">
			<span class="sr-only">Search bots</span>
			<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
			<input class="field pl-8" type="search" placeholder="Search by name" bind:value={q} />
		</label>
		<button class="btn sm:hidden" aria-expanded={filtersOpen} onclick={() => (filtersOpen = !filtersOpen)}>
			Filters{#if [stateF, runtimeF, ownerF, tagF].filter(Boolean).length} ({[stateF, runtimeF, ownerF, tagF].filter(Boolean).length}){/if}
		</button>
		<div class="{filtersOpen ? 'grid' : 'hidden'} w-full grid-cols-2 gap-2 sm:contents">
			<select class="field w-auto" bind:value={stateF} aria-label="State">
				<option value="">All states</option>
				<option value="attention">Needs attention</option>
				<option value="running">Running</option>
				<option value="progress">In progress</option>
				<option value="stopped">Stopped</option>
			</select>
			<select class="field w-auto" bind:value={runtimeF} aria-label="Runtime">
				<option value="">All runtimes</option>
				{#each runtimes as r (r.id)}<option value={r.id}>{r.display_name}</option>{/each}
			</select>
			{#if allTags.length}
				<select class="field w-auto" bind:value={tagF} aria-label="Tag">
					<option value="">All tags</option>
					{#each allTags as t (t)}<option value={t}>{t}</option>{/each}
				</select>
			{/if}
			{#if hasShared || isAdmin}
				<select class="field w-auto" bind:value={ownerF} aria-label="Owner">
					<option value="">Everyone's</option>
					<option value="mine">Mine</option>
					{#if hasShared}<option value="shared">Shared with me</option>{/if}
					{#if isAdmin}<option value="others">Other users'</option>{/if}
				</select>
			{/if}
			<select class="field w-auto" bind:value={sort} aria-label="Sort">
				<option value="attention">Needs attention first</option>
				<option value="name">Name</option>
				<option value="newest">Newest first</option>
			</select>
		</div>
	</div>
{/if}

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if selectedBots.length}
	<div class="sticky top-16 z-20 mt-4 flex flex-wrap items-center gap-2 rounded-tile border border-rule-soft bg-raised px-3 py-2 shadow-overlay" role="region" aria-label="Selected bots">
		<span class="font-medium">{selectedBots.length} selected</span>
		<span class="flex-1"></span>
		<button class="btn btn-sm" onclick={() => openBatch('start')}><Icon name="play" size={12} />Start</button>
		<button class="btn btn-sm" onclick={() => openBatch('restart')}><Icon name="restart" size={14} />Restart</button>
		<button class="btn btn-sm" onclick={() => openBatch('stop')}><Icon name="stop" size={12} />Stop</button>
		<button class="btn btn-sm btn-quiet" onclick={() => (selected = {})}>Clear</button>
	</div>
{/if}

<section class="mt-4" aria-label="Bot list">
	{#if bots === null}
		{#if !error}<Skeleton rows={4} label="Loading bots" />{/if}
	{:else}
		<ResourceTable kind="bot" label="Bots" items={shown} {now} {runtimes} {note} bind:selected onChanged={refresh} onTag={(t) => (tagF = t)}>
			{#snippet empty()}
				{#if filtered}
					<span>No bots match these filters.</span><button class="link" onclick={clearFilters}>Clear filters</button>
				{:else if bots?.length}
					<span>No bots in {scope?.personal ? 'your personal workspace' : (scope?.name ?? 'this workspace')} yet.</span>
					<button class="link" onclick={() => selectWorkspace('all')}>Show all workspaces</button>
				{:else if canDo('bots.create')}
					<span>No bots yet. A template comes with a working project and tells you which values to add.</span>
					<a class="link" href="/bots/new?source=template">Start from a template</a><a class="link" href="/bots/new?source=github">Deploy from GitHub</a>
				{:else}
					<span>No bots yet. Bots shared with you, or in workspaces you belong to, appear here.</span>
				{/if}
			{/snippet}
		</ResourceTable>
	{/if}
</section>

<Dialog bind:open={batchOpen} title={batchResults ? 'Results' : `${batchAction === 'start' ? 'Start' : batchAction === 'stop' ? 'Stop' : 'Restart'} ${selectedBots.length} bot${selectedBots.length === 1 ? '' : 's'}?`} size="sm">
	{#if batchResults}
		<ul class="list-card">
			{#each batchResults as r (r.bot_id)}
				<li class="flex items-baseline justify-between gap-3 px-3 py-1.5"><span class="min-w-0 truncate">{r.name}</span><span class="shrink-0 text-small {r.ok ? 'text-run' : 'text-fail'}">{r.ok ? 'Requested' : r.message}</span></li>
			{/each}
		</ul>
	{:else}
		<p>{batchAction === 'stop' ? 'Each bot is asked to exit.' : batchAction === 'restart' ? 'Each bot gets a fresh container.' : 'Each bot is started; builds run one after another.'} Bots you may not control are skipped and listed afterwards.</p>
		<ul class="mt-2 max-h-56 list-disc overflow-y-auto pl-5">{#each selectedBots as b (b.id)}<li>{b.name}</li>{/each}</ul>
	{/if}
	{#snippet footer()}
		{#if batchResults}
			<button class="btn btn-primary" onclick={() => (batchOpen = false)}>Done</button>
		{:else}
			<button class="btn" onclick={() => (batchOpen = false)}>Cancel</button>
			<button class="btn {batchAction === 'stop' ? 'btn-danger-solid' : 'btn-primary'}" disabled={batchBusy} onclick={runBatch}>{batchAction === 'start' ? 'Start' : batchAction === 'stop' ? 'Stop' : 'Restart'} {selectedBots.length}</button>
		{/if}
	{/snippet}
</Dialog>
