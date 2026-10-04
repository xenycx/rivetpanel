<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { can, Perm, type Bot, type Capacity, type RuntimeInfo, type Sample } from '$lib/api/types';
	import { power } from '$lib/api/bots';
	import { can as canDo, session } from '$lib/session.svelte';
	import { describe } from '$lib/status';
	import StatusBadge from '$lib/components/ui/StatusBadge.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { currentWorkspace, selectWorkspace, workspaceName, workspaces } from '$lib/workspaces.svelte';

	let bots = $state<Bot[] | null>(null);
	let runtimes = $state<RuntimeInfo[]>([]);
	let cap = $state<Capacity | null>(null);
	// Administrators see the host's real usage in the capacity strip.
	let host = $state<{ cpu: number; cpus: number; mem: number; memTotal: number; disk: number; diskTotal: number } | null>(null);
	const limited = $derived(!!cap && !cap.exempt && (cap.max_bots > 0 || cap.max_memory_bytes > 0));
	let error = $state('');
	let now = $state(Date.now());
	let busy = $state<Record<string, boolean>>({});

	// Filters live in the URL so a filtered view can be bookmarked and shared.
	const params = page.url.searchParams;
	let q = $state(params.get('q') ?? '');
	let stateF = $state(params.get('state') ?? '');
	let runtimeF = $state(params.get('runtime') ?? '');
	let ownerF = $state(params.get('owner') ?? '');
	let sort = $state(params.get('sort') ?? 'attention');
	let tagF = $state(params.get('tag') ?? '');
	let filtersOpen = $state(false);
	// Cards (default) or the compact list; a per-browser preference.
	let view = $state<'grid' | 'list'>(
		(() => {
			try {
				return localStorage.getItem('rivetpanel.fleetView') === 'list' ? 'list' : 'grid';
			} catch {
				return 'grid';
			}
		})()
	);
	function setView(v: 'grid' | 'list') {
		view = v;
		try {
			localStorage.setItem('rivetpanel.fleetView', v);
		} catch {
			/* not remembered */
		}
	}

	// Batch actions: selection, review, then per-bot results.
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
			// Game servers have their own page.
			bots = (await api<{ bots: Bot[] }>('GET', '/bots')).bots.filter((b) => b.kind !== 'game');
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The bot list could not be loaded. Check your connection.';
		}
	}

	onMount(() => {
		refresh();
		api<{ runtimes: RuntimeInfo[] }>('GET', '/runtimes').then((r) => (runtimes = r.runtimes)).catch(() => {});
		api<Capacity>('GET', '/me/capacity').then((r) => (cap = r)).catch(() => {});
		if (session.user?.role === 'admin') {
			api<{ nodes: { id: string }[] }>('GET', '/nodes')
				.then((r) => (r.nodes[0] ? api<{ samples: Sample[] }>('GET', `/nodes/${r.nodes[0].id}/telemetry?limit=1`) : null))
				.then((r) => {
					const x = r?.samples.at(-1);
					if (x) host = { cpu: x.cpu_percent, cpus: x.logical_cpus, mem: x.memory_used_bytes, memTotal: x.memory_total_bytes, disk: x.disk_used_bytes, diskTotal: x.disk_total_bytes };
				})
				.catch(() => {});
		}
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

	// The workspace chosen in the sidebar scopes everything on this page.
	const scoped = $derived((bots ?? []).filter((b) => workspaces.selected === 'all' || b.workspace_id === workspaces.selected));
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	const shown = $derived.by(() => {
		if (!bots) return [];
		const needle = q.trim().toLowerCase();
		const out = scoped.filter(
			(b) =>
				(!needle || b.name.toLowerCase().includes(needle) || b.runtime.includes(needle)) &&
				(!stateF || group(b) === stateF) &&
				(!runtimeF || b.runtime === runtimeF) &&
				(!tagF || b.tags.includes(tagF)) &&
				(!ownerF || (ownerF === 'mine' ? mine(b) : ownerF === 'shared' ? b.shared : !mine(b) && !b.shared))
		);
		out.sort((a, b) =>
			Number(b.favorite) - Number(a.favorite) ||
			(sort === 'name'
				? a.name.localeCompare(b.name)
				: sort === 'newest'
					? b.created_at_ms - a.created_at_ms
					: rank[group(a)] - rank[group(b)] || a.name.localeCompare(b.name))
		);
		return out;
	});
	const counts = $derived.by(() => {
		const c = { running: 0, attention: 0, progress: 0, stopped: 0 };
		for (const b of scoped) c[group(b)]++;
		return c;
	});
	const filtered = $derived(!!(q.trim() || stateF || runtimeF || ownerF || tagF));
	const allTags = $derived([...new Set(scoped.flatMap((b) => b.tags))].sort());
	const selectedBots = $derived((bots ?? []).filter((b) => selected[b.id]));
	const selectable = $derived(shown.filter((b) => can(b, Perm.power)));
	const allSelected = $derived(selectable.length > 0 && selectable.every((b) => selected[b.id]));

	async function star(b: Bot) {
		const on = !b.favorite;
		b.favorite = on;
		try {
			await api('PUT', `/bots/${b.id}/favorite`, { favorite: on });
		} catch (e) {
			b.favorite = !on;
			toast(e instanceof ApiError ? e.message : 'The favorite could not be saved.', 'fail');
		}
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
	const hasShared = $derived(scoped.some((b) => b.shared));
	// Capacity strip: node reservation for administrators, account limits otherwise.
	const capRows = $derived.by(() => {
		const rows: { label: string; value: number; max: number; fmt: (n: number) => string }[] = [];
		const all = bots ?? [];
		const assigned = all.reduce((n, b) => n + b.memory_bytes, 0);
		const runningMem = all.filter((b) => b.desired_state === 'running').reduce((n, b) => n + b.memory_bytes, 0);
		const pctFmt = (n: number) => `${Math.round(n)}%`;
		if (host) {
			// The host as it is now, plus the admission budget when one is set.
			rows.push({ label: 'RAM', value: host.mem, max: host.memTotal, fmt: fmtBytes });
			rows.push({ label: 'CPU', value: host.cpu, max: 100, fmt: pctFmt });
			rows.push({ label: 'Disk', value: host.disk, max: host.diskTotal, fmt: fmtBytes });
			if (cap?.node?.budget_bytes) rows.push({ label: 'Reserved', value: cap.node.reserved_bytes, max: cap.node.budget_bytes, fmt: fmtBytes });
			else rows.push({ label: 'Bots', value: counts.running, max: all.length, fmt: (n) => String(n) });
			return rows;
		}
		rows.push({ label: 'RAM', value: cap && !cap.exempt && cap.max_memory_bytes ? cap.memory_bytes : runningMem, max: cap && !cap.exempt ? cap.max_memory_bytes : 0, fmt: fmtBytes });
		rows.push({ label: 'Assigned', value: assigned, max: 0, fmt: fmtBytes });
		rows.push({ label: 'Running', value: counts.running, max: all.length, fmt: (n) => String(n) });
		rows.push({ label: 'Bots', value: all.length, max: cap && !cap.exempt ? cap.max_bots : 0, fmt: (n) => String(n) });
		return rows;
	});
	const runtimeTag: Record<string, string> = { nodejs: 'JS', python: 'PY', rust: 'RS', go: 'GO', java: 'JV', ruby: 'RB' };
	const isAdmin = $derived(session.user?.role === 'admin');
	const runtimeName = (id: string) => runtimes.find((r) => r.id === id)?.display_name ?? id;
	const source = (b: Bot) =>
		b.source_type === 'github' ? 'GitHub' : b.source_type === 'template' ? `${runtimeName(b.runtime)} template` : runtimeName(b.runtime);

	async function act(b: Bot, action: 'start' | 'stop' | 'restart' | 'kill') {
		busy[b.id] = true;
		if (await power(b, action)) await refresh();
		busy[b.id] = false;
	}
	function clearFilters() {
		q = stateF = runtimeF = ownerF = tagF = '';
	}

	function menuFor(b: Bot): MenuItem[] {
		const items: MenuItem[] = [{ label: 'Open', onselect: () => goto(`/bots/${b.id}`) }];
		if (can(b, Perm.console)) items.push({ label: 'Console', onselect: () => goto(`/bots/${b.id}?tab=manage`) });
		if (can(b, Perm.files)) items.push({ label: 'Files', onselect: () => goto(`/bots/${b.id}?tab=files`) });
		if (can(b, Perm.power) && session.features.runner) {
			items.push('separator');
			if (b.phase === 'failed' || b.phase === 'exited') {
				items.push({ label: 'Start again', onselect: () => act(b, 'start') }, { label: 'Stop', onselect: () => act(b, 'stop') });
			} else if (b.desired_state === 'running') {
				items.push({ label: 'Restart', onselect: () => act(b, 'restart') }, { label: 'Stop', onselect: () => act(b, 'stop') });
			} else items.push({ label: 'Start', onselect: () => act(b, 'start') });
			items.push({ label: 'Kill', danger: true, onselect: () => act(b, 'kill'), hint: 'Ends the process immediately' });
		}
		return items;
	}
</script>

<svelte:head><title>Bots · RivetPanel</title></svelte:head>

<section class="card card-glow grid gap-6 p-6 sm:p-8 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
	<div>
		<p class="eyebrow">Overview{scope ? ` · ${scope.personal ? 'Personal workspace' : scope.name}` : ''}</p>
		<h1 class="mt-2 text-[2rem] leading-tight font-semibold tracking-tight sm:text-[2.25rem]">Manage your <span class="text-action">fleet</span>.</h1>
		<p class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-muted">
			{#if bots}
				<span class="inline-flex items-center gap-1.5"><span class="side-dot" data-tone={counts.running ? 'run' : 'idle'}></span><span class="font-medium text-ink">{counts.running}</span> running</span>
				<span aria-hidden="true">·</span><span><span class="font-medium text-ink">{scoped.length}</span> bot{scoped.length === 1 ? '' : 's'}</span>
				{#if counts.attention}<span aria-hidden="true">·</span><button class="font-medium text-fail underline decoration-fail/40 underline-offset-2" onclick={() => (stateF = 'attention')}>{counts.attention} need{counts.attention === 1 ? 's' : ''} attention</button>{/if}
				{#if counts.progress}<span aria-hidden="true">·</span><span class="text-warn">{counts.progress} in progress</span>{/if}
			{:else}&nbsp;{/if}
		</p>
	</div>
	{#if canDo('bots.create')}<div>
		<p class="eyebrow">Deploy a new bot</p>
		<div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-2 xl:grid-cols-4">
			<a href="/bots/new?source=github" class="quick"><Icon name="github" class="text-action" />GitHub</a>
			<a href="/templates" class="quick"><Icon name="layers" class="text-action" />Template</a>
			<a href="/bots/new?source=blank" class="quick"><Icon name="upload" class="text-action" />Upload ZIP</a>
			<a href="/bots/new?source=blank" class="quick"><Icon name="file" class="text-action" />Empty bot</a>
		</div>
	</div>{/if}
</section>

{#if bots && bots.length > 0}
	<section class="card mt-4 px-6 py-4 sm:px-8" aria-label="Capacity">
		<div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
			<p class="eyebrow">Capacity</p>
			{#if cap?.node && cap.node.budget_bytes}
				{@const pct = Math.round((cap.node.reserved_bytes / cap.node.budget_bytes) * 100)}
				<p class="text-small"><span class="font-mono font-medium {pct >= 90 ? 'text-fail' : 'text-action'}">{pct}%</span> <span class="text-muted">of the server's memory budget reserved</span></p>
			{:else if limited && cap}
				<p class="text-small text-muted">Your account's limits</p>
			{:else if host}
				<p class="text-small text-muted">This server right now</p>
			{:else}
				<p class="text-small text-muted">No limits configured</p>
			{/if}
			{#if isAdmin}<a href="/admin/host" class="ml-auto btn btn-sm">Host details<Icon name="chevronRight" size={13} /></a>{/if}
		</div>
		<div class="mt-3 grid gap-x-8 gap-y-3 sm:grid-cols-2 xl:grid-cols-4">
			{#each capRows as r (r.label)}
				<div class="flex items-center gap-3">
					<span class="eyebrow w-16 shrink-0">{r.label}</span>
					<div class="meter min-w-8 flex-1" data-level={r.max && r.value / r.max >= 0.9 ? 'high' : 'ok'} data-open={!r.max}><i style="width: {r.max ? Math.min(100, (r.value / r.max) * 100) : 0}%"></i></div>
					<span class="shrink-0 font-mono text-small"><span class="font-medium">{r.fmt(r.value)}</span><span class="text-muted"> / {r.max ? r.fmt(r.max) : '∞'}</span></span>
				</div>
			{/each}
		</div>
	</section>
{/if}

{#if bots && bots.length > 0}
	<div class="mt-5 flex flex-wrap items-center gap-2" role="search" aria-label="Filter bots">
		<label class="relative min-w-0 flex-1 basis-40">
			<span class="sr-only">Search bots</span>
			<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
			<input class="field pl-8" type="search" placeholder="Search by name" bind:value={q} />
		</label>
		<button class="btn sm:hidden" aria-expanded={filtersOpen} onclick={() => (filtersOpen = !filtersOpen)}>
			Filters{#if [stateF, runtimeF, ownerF].filter(Boolean).length} ({[stateF, runtimeF, ownerF].filter(Boolean).length}){/if}
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
		<div class="flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Layout">
			<button class="grid size-8 place-items-center rounded-inner {view === 'grid' ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={view === 'grid'} aria-label="Cards" onclick={() => setView('grid')}><Icon name="grid" size={15} /></button>
			<button class="grid size-8 place-items-center rounded-inner {view === 'list' ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={view === 'list'} aria-label="List" onclick={() => setView('list')}><Icon name="list" size={15} /></button>
		</div>
	</div>
{/if}

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if selectedBots.length}
	<div class="sticky top-0 z-20 mt-4 flex flex-wrap items-center gap-2 border border-rule-soft bg-raised px-3 py-2 shadow-overlay" role="region" aria-label="Selected bots">
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
	{:else if bots.length === 0}
		<EmptyState title={canDo('bots.create') ? 'Create your first bot' : 'No bots yet'}>
			<p>{canDo('bots.create') ? 'Start from a template if this is your first bot: it comes with a working project and tells you which values to add. You can also deploy an existing repository from GitHub or start with an empty workspace.' : 'Your role cannot create bots. Bots shared with you, or in workspaces you belong to, appear here.'}</p>
			{#snippet actions()}{#if canDo('bots.create')}
				<a href="/bots/new?source=template" class="btn btn-primary">Start from a template</a>
				<a href="/bots/new?source=github" class="btn"><Icon name="github" />Deploy from GitHub</a>
				<a href="/bots/new?source=blank" class="btn">Empty bot</a>
			{/if}{/snippet}
		</EmptyState>
	{:else if scoped.length === 0}
		<EmptyState title="No bots in {scope?.personal ? 'your personal workspace' : (scope?.name ?? 'this workspace')} yet" compact>
			<p>Bots you create while this workspace is selected go into it. Members of the workspace can see and operate them according to their role.</p>
			{#snippet actions()}
				<a href="/bots/new" class="btn btn-primary"><Icon name="plus" />New bot here</a>
				<button class="btn" onclick={() => selectWorkspace('all')}>Show all workspaces</button>
			{/snippet}
		</EmptyState>
	{:else if shown.length === 0}
		<EmptyState title="No bots match these filters" compact>
			{#snippet actions()}<button class="btn" onclick={clearFilters}>Clear filters</button>{/snippet}
		</EmptyState>
	{:else}
		{#if selectable.length > 1 && session.features.runner}
			<label class="mb-2 inline-flex items-center gap-2 text-small text-muted">
				<input type="checkbox" checked={allSelected} onchange={(e) => { const on = e.currentTarget.checked; for (const b of selectable) selected[b.id] = on; }} />Select all {selectable.length} shown
			</label>
		{/if}
		{#if view === 'grid'}
			<ul class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
				<li>
					<a href="/bots/new" class="grid h-full min-h-72 place-items-center rounded-card border border-dashed border-rule p-6 text-center transition-colors hover:border-action/60 hover:bg-panel">
						<span>
							<span class="mx-auto grid size-11 place-items-center rounded-pill bg-paper-2 text-ink"><Icon name="plus" size={18} /></span>
							<span class="mt-4 block text-title font-semibold">New deployment</span>
							<span class="mt-1 block text-small text-muted">GitHub, template, ZIP upload or an empty bot.</span>
						</span>
					</a>
				</li>
				{#each shown as b (b.id)}
					{@const d = describe(b, now)}
					<li class="card flex flex-col p-5">
						<div class="flex items-start justify-between gap-2">
							{#if b.logo_url}<img src={b.logo_url} alt="" class="size-10 shrink-0 rounded-tile object-cover" referrerpolicy="no-referrer" />{:else}<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 font-mono text-small font-medium text-action" aria-hidden="true">{b.template_id === 'discordts' ? 'TS' : (runtimeTag[b.runtime] ?? b.runtime.slice(0, 2).toUpperCase())}</span>{/if}
							<div class="flex items-center gap-1.5">
								<span class="pill" data-tone={d.tone} title={d.detail || undefined}><span class="side-dot !m-0 !size-1.5" data-tone={d.tone}></span>{d.label}</span>
								{#if can(b, Perm.power) && session.features.runner}
									<input type="checkbox" class="ml-1" aria-label="Select {b.name}" bind:checked={selected[b.id]} />
								{/if}
							</div>
						</div>
						<div class="mt-4 flex min-w-0 items-center gap-1.5">
							<a href="/bots/{b.id}" class="min-w-0 truncate text-title font-semibold hover:underline">{b.name}</a>
							<button class="shrink-0 {b.favorite ? 'text-warn' : 'text-rule hover:text-muted'}" aria-label={b.favorite ? `Remove ${b.name} from favorites` : `Add ${b.name} to favorites`} aria-pressed={b.favorite} onclick={() => star(b)}>
								<svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.8l1.9 3.9 4.3.6-3.1 3 .7 4.3L8 11.6l-3.8 2 .7-4.3-3.1-3 4.3-.6z" fill={b.favorite ? 'currentColor' : 'none'} stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /></svg>
							</button>
						</div>
						<p class="truncate text-small text-muted">{source(b)}{b.shared ? ' · shared with you' : multi && workspaceName(b.workspace_id) ? ` · ${workspaceName(b.workspace_id)}` : ''}</p>
						<dl class="mt-4 grid grid-cols-3 gap-2 border-t border-rule-soft pt-4">
							<div><dt class="eyebrow">RAM</dt><dd class="mt-0.5 font-mono text-small font-medium">{fmtBytes(b.memory_bytes)}</dd></div>
							<div><dt class="eyebrow">CPU</dt><dd class="mt-0.5 font-mono text-small font-medium">{fmtCpu(b.nano_cpus)}</dd></div>
							<div><dt class="eyebrow">Restarts</dt><dd class="mt-0.5 font-mono text-small font-medium">{b.restart_count ?? 0}</dd></div>
						</dl>
						{#if d.detail && d.tone !== 'run' && d.tone !== 'idle'}<p class="mt-3 line-clamp-2 text-small text-muted">{d.detail}</p>{/if}
						{#if b.tags.length}
							<p class="mt-3 flex flex-wrap gap-1">
								{#each b.tags as t (t)}<button class="rounded-control border border-rule-soft bg-paper px-1.5 text-small text-muted hover:border-rule hover:text-ink" onclick={() => (tagF = t)} aria-label="Show bots tagged {t}">{t}</button>{/each}
							</p>
						{/if}
						<p class="mt-3 truncate font-mono text-[11px] text-muted" title={b.id}>{b.id}</p>
						<div class="mt-auto flex items-center gap-2 pt-4">
							<a href="/bots/{b.id}" class="btn btn-primary flex-1">Manage</a>
							{#if can(b, Perm.power) && session.features.runner}
								{#if b.phase === 'failed' || b.phase === 'exited'}
									<button class="btn btn-icon" disabled={busy[b.id]} onclick={() => act(b, 'start')} aria-label="Start {b.name} again" title="Start again"><Icon name="play" size={12} /></button>
								{:else if b.desired_state === 'running'}
									<button class="btn btn-icon" disabled={busy[b.id]} onclick={() => act(b, 'stop')} aria-label="Stop {b.name}" title="Stop"><Icon name="stop" size={12} /></button>
								{:else}
									<button class="btn btn-icon" disabled={busy[b.id]} onclick={() => act(b, 'start')} aria-label="Start {b.name}" title="Start"><Icon name="play" size={12} /></button>
								{/if}
							{/if}
							<Menu label="Actions for {b.name}" items={menuFor(b)} />
						</div>
					</li>
				{/each}
			</ul>
		{:else}
		<ul class="overflow-hidden rounded-tile border border-rule-soft bg-panel">
			{#each shown as b (b.id)}
				{@const d = describe(b, now)}
				<li class="spine grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 border-b border-rule-soft py-3 pr-2 pl-5 last:border-b-0 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1.6fr)_10rem_auto]" data-tone={d.tone} data-busy={d.busy}>
					<div class="flex min-w-0 items-start gap-2">
						{#if b.logo_url}<img src={b.logo_url} alt="" class="size-8 shrink-0 rounded-lg object-cover" referrerpolicy="no-referrer" />{/if}
						{#if can(b, Perm.power) && session.features.runner}
							<input type="checkbox" class="mt-1.5 shrink-0" aria-label="Select {b.name}" bind:checked={selected[b.id]} />
						{/if}
						<button class="mt-0.5 shrink-0 {b.favorite ? 'text-warn' : 'text-rule hover:text-muted'}" aria-label={b.favorite ? `Remove ${b.name} from favorites` : `Add ${b.name} to favorites`} aria-pressed={b.favorite} onclick={() => star(b)}>
							<svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.8l1.9 3.9 4.3.6-3.1 3 .7 4.3L8 11.6l-3.8 2 .7-4.3-3.1-3 4.3-.6z" fill={b.favorite ? 'currentColor' : 'none'} stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /></svg>
						</button>
						<div class="min-w-0">
							<a href="/bots/{b.id}" class="block truncate text-title font-semibold hover:underline">{b.name}</a>
							<p class="truncate text-small text-muted">
								{source(b)}{#if b.shared}, shared with you{:else if !mine(b)}, owned by another user{/if}
							</p>
							{#if b.tags.length}
								<p class="mt-1 flex flex-wrap gap-1">
									{#each b.tags as t (t)}<button class="rounded-control border border-rule-soft bg-paper px-1.5 text-small text-muted hover:border-rule hover:text-ink" onclick={() => (tagF = t)} aria-label="Show bots tagged {t}">{t}</button>{/each}
								</p>
							{/if}
						</div>
					</div>
					<div class="col-start-1 row-start-2 min-w-0 md:col-start-2 md:row-start-1">
						<StatusBadge bot={b} {now} />
						{#if d.detail && d.tone !== 'run' && d.tone !== 'idle'}<p class="line-clamp-1 text-small text-muted" title={d.detail}>{d.detail}</p>{/if}
					</div>
					<div class="hidden text-small text-muted md:block">{fmtBytes(b.memory_bytes)} memory<br />{fmtCpu(b.nano_cpus)}</div>
					<div class="col-start-2 row-span-2 row-start-1 flex items-center justify-end gap-1 md:col-start-4 md:row-span-1">
						{#if can(b, Perm.power) && session.features.runner}
							{#if b.phase === 'failed' || b.phase === 'exited'}
								<button class="btn btn-sm hidden sm:inline-flex" disabled={busy[b.id]} onclick={() => act(b, 'start')}><Icon name="play" size={12} />Start again</button>
							{:else if b.desired_state === 'running'}
								<button class="btn btn-sm hidden sm:inline-flex" disabled={busy[b.id]} onclick={() => act(b, 'stop')}>Stop</button>
							{:else}
								<button class="btn btn-sm hidden sm:inline-flex" disabled={busy[b.id]} onclick={() => act(b, 'start')}><Icon name="play" size={12} />Start</button>
							{/if}
						{/if}
						<Menu label="Actions for {b.name}" items={menuFor(b)} />
					</div>
				</li>
			{/each}
		</ul>
		{/if}
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
