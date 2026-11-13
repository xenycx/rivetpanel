<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot, Site } from '$lib/api/types';
	import type { Person } from '$lib/api/support';
	import type { ActivityCategory, ActivityItem, ActivityOutcome, ActivityPage, ActivityStats, Filters, Range } from '$lib/activity';
	import {
		activeFilterCount,
		categories,
		categoryOf,
		dayHeading,
		dayKey,
		emptyFilters,
		filterQuery,
		filtersFromURL,
		filtersToURL,
		outcomes,
		presets,
		rangeText,
		resolveRange
	} from '$lib/activity';
	import { logoSeed } from '$lib/avatar';
	import WorkspaceScope from '$lib/components/WorkspaceScope.svelte';
	import ActivityHistogram from '$lib/components/activity/ActivityHistogram.svelte';
	import ActivityReport from '$lib/components/activity/ActivityReport.svelte';
	import ActivityRow from '$lib/components/activity/ActivityRow.svelte';
	import Picker from '$lib/components/support/Picker.svelte';
	import Popover from '$lib/components/support/Popover.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// One timeline for everything that happened on the apps you can see:
	// what people changed and what the panel did by itself, with a report of
	// the window above it. Every filter lives in the URL so a view can be
	// shared or bookmarked.

	let f = $state<Filters>(filtersFromURL(page.url.searchParams));
	let items = $state<ActivityItem[] | null>(null);
	let next = $state<ActivityPage['next']>();
	let stats = $state<ActivityStats | null>(null);
	let statsLoading = $state(false);
	let error = $state('');
	let loadingMore = $state(false);
	let now = $state(Date.now());
	let bots = $state<Bot[]>([]);
	let sites = $state<Site[]>([]);
	let people = $state<Person[]>([]);
	/** Earlier windows, for zooming back out after a brush or a bar click. */
	let zoomStack = $state<Range[]>([]);
	let reportOpen = $state(false);
	let readout = $state<{ when: string; people: number; automatic: number; failed: number } | null>(null);

	// ---- Loading ---------------------------------------------------------------------
	let gen = 0; // drops responses for filters that changed meanwhile

	async function loadFirst(quiet = false) {
		const g = ++gen;
		const t = Date.now();
		const q = filterQuery(f, t);
		if (!quiet) {
			items = null;
			statsLoading = true;
		}
		const tz = String(-new Date().getTimezoneOffset());
		const [feed, st] = await Promise.allSettled([
			api<ActivityPage>('GET', `/activity?${q}&limit=40`),
			api<ActivityStats>('GET', `/activity/stats?${q}&tz=${tz}`)
		]);
		if (g !== gen) return;
		now = t;
		if (feed.status === 'fulfilled') {
			const fresh = feed.value.items;
			if (quiet && items?.length && next) {
				// Keep older pages already loaded below the refreshed first page.
				const last = fresh[fresh.length - 1];
				const older = last ? items.filter((i) => i.at_ms < last.at_ms || (i.at_ms === last.at_ms && i.key < last.key)) : [];
				items = [...fresh, ...older];
			} else {
				items = fresh;
				next = feed.value.next;
			}
			error = '';
		} else {
			error = feed.reason instanceof ApiError ? feed.reason.message : 'Activity could not be loaded.';
			items = items ?? [];
		}
		if (st.status === 'fulfilled') stats = st.value;
		statsLoading = false;
	}

	async function more() {
		if (!next || loadingMore) return;
		loadingMore = true;
		const g = gen;
		try {
			const q = filterQuery(f, now);
			q.set('before', String(next.before));
			q.set('before_key', next.before_key);
			const p = await api<ActivityPage>('GET', `/activity?${q}&limit=40`);
			if (g !== gen) return;
			const seen = new Set(items?.map((i) => i.key));
			items = [...(items ?? []), ...p.items.filter((i) => !seen.has(i.key))];
			next = p.next;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Older activity could not be loaded.';
		} finally {
			loadingMore = false;
		}
	}

	// Filters drive the URL and the data. The search box waits for a pause.
	let qTimer: ReturnType<typeof setTimeout> | undefined;
	let qLive = $state(f.q);
	$effect(() => {
		const v = qLive;
		clearTimeout(qTimer);
		qTimer = setTimeout(() => untrack(() => f.q !== v && (f.q = v)), 300);
	});
	$effect(() => {
		const snapshot = JSON.stringify(f);
		untrack(() => {
			const u = new URL(location.href);
			filtersToURL(JSON.parse(snapshot), u);
			if (u.href !== location.href) history.replaceState(history.state, '', u);
			loadFirst();
		});
	});

	// ---- Live refresh ----------------------------------------------------------------
	onMount(() => {
		api<{ bots: Bot[] }>('GET', '/bots').then((r) => (bots = r.bots)).catch(() => {});
		api<{ sites: Site[] }>('GET', '/sites').then((r) => (sites = r.sites)).catch(() => {});
		api<{ people: Person[] }>('GET', '/activity/people').then((r) => (people = r.people)).catch(() => {});
		let last = Date.now();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState !== 'visible' || !('preset' in f.range)) return;
			// Running work refreshes quickly; otherwise every half minute.
			const busy = items?.some((i) => i.outcome === 'running');
			if (now - last >= (busy ? 4000 : 30_000)) {
				last = now;
				loadFirst(true);
			}
		}, 2000);
		const back = () => document.visibilityState === 'visible' && 'preset' in f.range && loadFirst(true);
		document.addEventListener('visibilitychange', back);
		return () => {
			clearInterval(t);
			document.removeEventListener('visibilitychange', back);
		};
	});

	// Older pages load as the end of the timeline scrolls into view.
	let sentinel = $state<HTMLElement>();
	$effect(() => {
		if (!sentinel) return;
		const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && more(), { rootMargin: '600px 0px' });
		io.observe(sentinel);
		return () => io.disconnect();
	});

	// ---- Filtering -------------------------------------------------------------------
	function setRange(r: Range, remember = false) {
		if (remember) zoomStack = [...zoomStack, f.range];
		else zoomStack = [];
		f.range = r;
	}
	function zoomOut() {
		const prev = zoomStack[zoomStack.length - 1];
		zoomStack = zoomStack.slice(0, -1);
		f.range = prev ?? { preset: '7d' };
	}
	const toggle = <T,>(list: T[], v: T) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
	function narrow(kind: 'actor' | 'app' | 'category' | 'who', id: string) {
		if (kind === 'actor') f.actors = toggle(f.actors, id);
		else if (kind === 'app') f.apps = toggle(f.apps, id);
		else if (kind === 'category') f.categories = toggle(f.categories, id as ActivityCategory);
		else f.who = f.who === id ? 'all' : (id as Filters['who']);
	}
	function clearAll() {
		const range = f.range;
		Object.assign(f, emptyFilters(), { range });
		qLive = '';
	}

	// Custom range: two local date-time fields.
	const toLocal = (ms: number) => {
		const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60_000);
		return d.toISOString().slice(0, 16);
	};
	let customFrom = $state('');
	let customTo = $state('');
	let customOpen = $state(false);
	$effect(() => {
		if (!customOpen) return;
		const { from, to } = resolveRange(untrack(() => f.range), Date.now());
		customFrom = toLocal(from);
		customTo = toLocal(to);
	});
	function applyCustom(close: () => void) {
		const from = new Date(customFrom).getTime();
		const to = new Date(customTo).getTime();
		if (!(from > 0) || !(to > from)) return;
		setRange({ from, to });
		close();
	}

	// ---- Options for the pickers ---------------------------------------------------------
	type AppOpt = { id: string; name: string; kind: 'bot' | 'game' | 'web' | 'site'; logo: string; variant: number };
	const appOptions = $derived.by(() => {
		const list: AppOpt[] = [
			...bots.filter((b) => b.kind !== 'web').map((b) => ({ id: b.id, name: b.name, kind: (b.kind === 'game' ? 'game' : 'bot') as AppOpt['kind'], logo: b.logo_url, variant: b.logo_variant })),
			...sites.map((s) => ({ id: s.id, name: s.name, kind: 'site' as const, logo: s.icon_url, variant: s.logo_variant }))
		];
		// Apps only seen in the report (deleted since, or not listed) stay choosable.
		for (const a of stats?.apps ?? []) if (!list.some((x) => x.id === a.app.id)) list.push({ id: a.app.id, name: a.app.name, kind: a.app.kind, logo: a.app.logo_url, variant: a.app.logo_variant });
		return list.sort((a, b) => a.name.localeCompare(b.name)).map((a) => ({ value: a.id, text: a.name, data: a }));
	});
	const peopleOptions = $derived(people.map((p) => ({ value: p.id, text: p.name || p.email, data: p })));
	const catOptions = categories.map((c) => ({ value: c.id, text: c.label, data: c }));
	const outcomeOptions = outcomes.map((o) => ({ value: o.id, text: o.label, data: o }));

	const pickedPeople = $derived(f.actors.map((id) => people.find((p) => p.id === id)).filter(Boolean) as Person[]);
	const pickedApps = $derived(f.apps.map((id) => appOptions.find((a) => a.value === id)?.data).filter(Boolean) as AppOpt[]);
	const filterCount = $derived(activeFilterCount(f));

	// ---- Grouping ----------------------------------------------------------------------
	const days = $derived.by(() => {
		const out: { key: string; at: number; items: ActivityItem[] }[] = [];
		for (const it of items ?? []) {
			const k = dayKey(it.at_ms);
			const last = out[out.length - 1];
			if (last?.key === k) last.items.push(it);
			else out.push({ key: k, at: it.at_ms, items: [it] });
		}
		return out;
	});
	const canOutput = (it: ActivityItem) => {
		const b = it.op && bots.find((x) => x.id === it.op!.bot_id);
		return !!b && ((b.permissions & 16) !== 0 || (b.permissions & 1) !== 0);
	};
	const exportHref = $derived(`/api/v1/activity/export?${filterQuery(f, now)}`);
	const windowText = $derived(rangeText(f.range, now));
</script>

<svelte:head><title>Activity · RivetPanel</title></svelte:head>

<header class="flex flex-wrap items-end gap-x-4 gap-y-2">
	<div class="min-w-0 flex-[1_1_20rem]">
		<h1 class="text-page">Activity</h1>
		<p class="mt-0.5 max-w-prose text-muted">Everything that happened on the apps you can see: what people changed, and what schedules, GitHub pushes and the panel did by themselves.</p>
		<WorkspaceScope follows={false} class="mt-1" />
	</div>
	<a class="btn" href={exportHref} download title="Download the filtered timeline as a spreadsheet (up to 10,000 rows)"><Icon name="download" size={15} />Export CSV</a>
</header>

<!-- Window: presets, a custom range, and the chart you can brush to zoom. -->
<section class="window mt-6" aria-label="Time window">
	<div class="flex flex-wrap items-center gap-2">
		<div class="seg" role="radiogroup" aria-label="Time range">
			{#each presets as p (p.id)}
				<button type="button" role="radio" class="seg-item tabular-nums" aria-checked={'preset' in f.range && f.range.preset === p.id} title={p.text} onclick={() => setRange({ preset: p.id })}>{p.label}</button>
			{/each}
		</div>
		<Popover label="Choose dates" triggerClass="btn btn-sm h-[2.125rem]" width="19rem" bind:open={customOpen}>
			{#snippet trigger()}<Icon name="clock" size={14} />Dates…{/snippet}
			{#snippet children(close)}
				<form class="grid gap-3 p-2" onsubmit={(e) => (e.preventDefault(), applyCustom(close))}>
					<label class="block"><span class="label">From</span><input class="field" type="datetime-local" bind:value={customFrom} data-autofocus required /></label>
					<label class="block"><span class="label">To</span><input class="field" type="datetime-local" bind:value={customTo} required /></label>
					<div class="flex justify-end gap-2"><button type="button" class="btn btn-sm" onclick={close}>Cancel</button><button class="btn btn-sm btn-primary">Show</button></div>
				</form>
			{/snippet}
		</Popover>
		{#if 'from' in f.range}
			<span class="range-chip">
				<span class="tabular-nums">{windowText}</span>
				{#if zoomStack.length}
					<button type="button" class="range-x" onclick={zoomOut} title="Zoom back out"><Icon name="minimize" size={13} /><span class="sr-only">Zoom back out</span></button>
				{/if}
				<button type="button" class="range-x" onclick={() => setRange({ preset: '7d' })} title="Back to the last 7 days"><Icon name="x" size={13} /><span class="sr-only">Back to the last 7 days</span></button>
			</span>
		{/if}
		<p class="readout ml-auto text-small text-muted" aria-live="polite">
			{#if readout}
				<span class="font-medium text-ink">{readout.when}</span>
				<span><i class="k-people"></i>{readout.people} by people</span>
				<span><i class="k-auto"></i>{readout.automatic} automatic</span>
				{#if readout.failed}<span class="text-fail"><i class="k-fail"></i>{readout.failed} failed</span>{/if}
			{:else}
				<span class="hidden sm:inline">Drag across the chart to zoom in, or click a bar.</span>
			{/if}
		</p>
	</div>
	<div class="mt-4">
		<ActivityHistogram {stats} loading={statsLoading} bind:readout onselect={(from, to) => setRange({ from, to }, true)} />
	</div>
	<div class="hist-key" aria-hidden="true">
		<span><i class="k-people"></i>People</span>
		<span><i class="k-auto"></i>Automatic</span>
		<span><i class="k-fail"></i>Failed or refused</span>
	</div>
</section>

<!-- Filters. -->
<div class="filters mt-5" role="toolbar" aria-label="Filter the timeline">
	<div class="seg" role="radiogroup" aria-label="Performed by">
		<button type="button" role="radio" class="seg-item" aria-checked={f.who === 'all'} onclick={() => (f.who = 'all')}>Everyone</button>
		<button type="button" role="radio" class="seg-item" aria-checked={f.who === 'people'} onclick={() => (f.who = 'people')}><Icon name="users" size={13} />People</button>
		<button type="button" role="radio" class="seg-item" aria-checked={f.who === 'automatic'} onclick={() => (f.who = 'automatic')}><Icon name="bolt" size={13} />Automatic</button>
	</div>

	<Picker label="People" multi options={peopleOptions} value={f.actors} onpick={(v) => (f.actors = v)} triggerClass="picker-trigger filter-trigger" empty="Nobody has done anything here yet" width="17rem">
		{#snippet trigger()}
			{#if pickedPeople.length}
				<span class="stack">{#each pickedPeople.slice(0, 3) as p (p.id)}<Logo src={p.avatar_url} kind="user" seed={logoSeed(p.id, p.logo_variant)} class="size-5 rounded-pill" />{/each}</span>
				{pickedPeople.length === 1 ? pickedPeople[0].name || pickedPeople[0].email : `${pickedPeople.length} people`}
			{:else}<Icon name="users" size={14} />People{/if}
			<Icon name="plus" size={12} class="opacity-50" />
		{/snippet}
		{#snippet row(o)}
			<Logo src={o.data!.avatar_url} kind="user" seed={logoSeed(o.data!.id, o.data!.logo_variant)} class="size-5 rounded-pill" />
			<span class="min-w-0 flex-1 truncate">{o.text}</span>
		{/snippet}
	</Picker>

	<Picker label="Apps and sites" multi options={appOptions} value={f.apps} onpick={(v) => (f.apps = v)} triggerClass="picker-trigger filter-trigger" empty="No apps yet" width="17rem">
		{#snippet trigger()}
			{#if pickedApps.length}
				<span class="stack">{#each pickedApps.slice(0, 3) as a (a.id)}<Logo src={a.logo} kind={a.kind === 'site' ? 'site' : 'bot'} seed={logoSeed(a.id, a.variant)} class="size-5 rounded-[5px]" />{/each}</span>
				{pickedApps.length === 1 ? pickedApps[0].name : `${pickedApps.length} apps`}
			{:else}<Icon name="box" size={14} />Apps{/if}
			<Icon name="plus" size={12} class="opacity-50" />
		{/snippet}
		{#snippet row(o)}
			<Logo src={o.data!.logo} kind={o.data!.kind === 'site' ? 'site' : 'bot'} seed={logoSeed(o.data!.id, o.data!.variant)} class="size-5 rounded-[5px]" />
			<span class="min-w-0 flex-1 truncate">{o.text}</span>
			<span class="text-[11px] text-muted">{o.data!.kind === 'site' ? 'Site' : o.data!.kind === 'game' ? 'Server' : 'App'}</span>
		{/snippet}
	</Picker>

	<Picker label="Kind of activity" multi options={catOptions} value={f.categories} onpick={(v) => (f.categories = v as ActivityCategory[])} triggerClass="picker-trigger filter-trigger" width="16rem">
		{#snippet trigger()}
			{#if f.categories.length === 1}<Icon name={categoryOf[f.categories[0]].icon} size={14} />{categoryOf[f.categories[0]].label}
			{:else if f.categories.length}<Icon name="layers" size={14} />{f.categories.length} kinds
			{:else}<Icon name="layers" size={14} />Kind{/if}
			<Icon name="plus" size={12} class="opacity-50" />
		{/snippet}
		{#snippet row(o)}
			<Icon name={o.data!.icon} size={14} class="text-muted" />
			<span class="min-w-0 flex-1 truncate">{o.text}</span>
			{#if stats?.by_category[o.data!.id]}<span class="text-[11px] text-muted tabular-nums">{stats.by_category[o.data!.id]}</span>{/if}
		{/snippet}
	</Picker>

	<Picker label="Result" multi options={outcomeOptions} value={f.outcomes} onpick={(v) => (f.outcomes = v as ActivityOutcome[])} triggerClass="picker-trigger filter-trigger" width="13rem">
		{#snippet trigger()}
			<Icon name="check" size={14} />{f.outcomes.length === 1 ? outcomes.find((o) => o.id === f.outcomes[0])?.label : f.outcomes.length ? `${f.outcomes.length} results` : 'Result'}
			<Icon name="plus" size={12} class="opacity-50" />
		{/snippet}
		{#snippet row(o)}
			<span class="outcome-dot" data-o={o.value}></span>
			<span class="min-w-0 flex-1 truncate">{o.text}</span>
			{#if stats?.by_outcome[o.data!.id]}<span class="text-[11px] text-muted tabular-nums">{stats.by_outcome[o.data!.id]}</span>{/if}
		{/snippet}
	</Picker>

	<label class="search">
		<span class="sr-only">Search the timeline</span>
		<Icon name="search" size={14} class="pointer-events-none absolute left-2.5 text-muted" />
		<input class="field h-[2.125rem] min-h-0 py-0 pl-8 text-small" type="search" bind:value={qLive} placeholder="Search names, files, messages…" autocomplete="off" />
	</label>

	{#if filterCount}
		<button type="button" class="btn btn-sm btn-quiet text-muted" onclick={clearAll}><Icon name="x" size={13} />Clear {filterCount === 1 ? 'filter' : `${filterCount} filters`}</button>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}

<div class="layout mt-5">
	<div class="min-w-0">
		<!-- On narrow screens the report folds away above the timeline. -->
		{#if stats}
			<details class="report-fold" bind:open={reportOpen}>
				<summary class="btn btn-sm w-full justify-between"><span class="inline-flex items-center gap-1.5"><Icon name="chart" size={14} />Report for {windowText.charAt(0).toLowerCase() + windowText.slice(1)}</span><Icon name="more" size={14} /></summary>
				<div class="mt-3 mb-5 border-b border-rule-soft pb-5"><ActivityReport {stats} filters={f} onfilter={narrow} /></div>
			</details>
		{/if}

		{#if items === null}
			<Skeleton rows={6} label="Loading activity" />
		{:else if items.length === 0}
			<EmptyState title={filterCount || 'from' in f.range ? 'Nothing matches' : 'Nothing happened in this window'} compact>
				{#if filterCount}
					<p>No activity in {windowText.toLowerCase()} matches these filters.</p>
					<button class="btn btn-sm mt-3" onclick={clearAll}>Clear filters</button>
				{:else}
					<p>Starting, stopping and deploying apps, editing files and variables, backups, schedules and sharing are all recorded here, without any values. Try a longer range.</p>
					{#if !('preset' in f.range && f.range.preset === '90d')}<button class="btn btn-sm mt-3" onclick={() => setRange({ preset: '90d' })}>Show the last 90 days</button>{/if}
				{/if}
			</EmptyState>
		{:else}
			{#each days as d (d.key)}
				<section class="day" aria-label={dayHeading(d.at, now)}>
					<h2 class="day-h">
						<span>{dayHeading(d.at, now)}</span>
						<span class="text-muted tabular-nums">{d.items.length}{next && d === days[days.length - 1] ? '+' : ''}</span>
					</h2>
					<ol class="thread">
						{#each d.items as it (it.key)}
							<ActivityRow item={it} {now} canOutput={canOutput(it)} onfilter={narrow} />
						{/each}
					</ol>
				</section>
			{/each}
			<div bind:this={sentinel} class="h-px"></div>
			{#if next}
				<div class="mt-3 flex justify-center"><button class="btn btn-sm" disabled={loadingMore} onclick={more}>{loadingMore ? 'Loading…' : 'Show older'}</button></div>
			{:else}
				<p class="mt-4 text-center text-small text-muted">That is everything in {windowText.toLowerCase()}. Changes are kept for 180 days; secret values, file contents and passwords are never recorded, only names.</p>
			{/if}
		{/if}
	</div>

	<aside class="report-rail" aria-label="Report">
		{#if stats}
			<ActivityReport {stats} filters={f} onfilter={narrow} />
		{:else}
			<Skeleton rows={4} label="Loading the report" />
		{/if}
	</aside>
</div>

<style>
	.window {
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-card);
		background: var(--color-panel);
		padding: 0.875rem 1rem 0.75rem;
	}
	.range-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.125rem;
		min-height: 2.125rem;
		border-radius: var(--radius-control);
		padding-left: 0.625rem;
		font-size: var(--text-small);
		font-weight: 500;
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-action) 10%, transparent);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-action) 30%, transparent);
		animation: var(--animate-enter);
	}
	.range-x {
		display: grid;
		place-items: center;
		width: 1.875rem;
		height: 1.875rem;
		border-radius: var(--radius-control);
		color: var(--color-muted);
	}
	.range-x:hover {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-action) 12%, transparent);
	}
	.readout {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: flex-end;
		gap: 0.25rem 0.875rem;
		min-height: 1.25rem;
		font-variant-numeric: tabular-nums;
	}
	.readout span {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
	}
	.readout i {
		width: 8px;
		height: 8px;
		border-radius: 2px;
	}
	.hist-key {
		display: flex;
		flex-wrap: wrap;
		gap: 1rem;
		margin-top: 0.25rem;
		font-size: 0.6875rem;
		color: var(--color-muted);
	}
	.hist-key span {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
	}
	.hist-key i {
		width: 8px;
		height: 8px;
		border-radius: 2px;
	}
	.k-people {
		background: var(--color-ink);
		opacity: 0.82;
	}
	.k-auto {
		background: color-mix(in srgb, var(--color-data) 70%, var(--color-panel));
	}
	.k-fail {
		background: var(--color-fail);
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
	}
	.filters :global(.filter-trigger) {
		min-height: 2.125rem;
		border-color: var(--color-rule);
		background: var(--color-raised);
		font-weight: 500;
	}
	.filters :global(.filter-trigger:has(.stack)) {
		border-color: color-mix(in srgb, var(--color-action) 45%, var(--color-rule));
		background: color-mix(in srgb, var(--color-action) 6%, var(--color-raised));
	}
	.stack {
		display: inline-flex;
	}
	.stack > :global(* + *) {
		margin-left: -6px;
	}
	.stack > :global(*) {
		box-shadow: 0 0 0 1.5px var(--color-raised);
	}
	.search {
		position: relative;
		display: flex;
		align-items: center;
		flex: 1 1 14rem;
		max-width: 22rem;
	}
	.outcome-dot {
		--c: var(--color-run);
		width: 8px;
		height: 8px;
		margin-inline: 3px;
		border-radius: 50%;
		background: var(--c);
	}
	.outcome-dot[data-o='failed'],
	.outcome-dot[data-o='denied'] {
		--c: var(--color-fail);
	}
	.outcome-dot[data-o='running'] {
		--c: var(--color-warn);
	}
	.outcome-dot[data-o='cancelled'] {
		--c: var(--color-rule);
	}
	.layout {
		display: grid;
		gap: 2rem;
	}
	.report-rail {
		display: none;
	}
	@media (min-width: 72rem) {
		.layout {
			grid-template-columns: minmax(0, 1fr) 17.5rem;
			align-items: start;
		}
		.report-rail {
			display: block;
			position: sticky;
			top: 1rem;
			max-height: calc(100dvh - 2rem);
			overflow-y: auto;
			scrollbar-width: thin;
			padding: 0.25rem 0.25rem 1rem 0;
		}
		.report-fold {
			display: none;
		}
	}
	.report-fold > summary {
		list-style: none;
	}
	.report-fold > summary::-webkit-details-marker {
		display: none;
	}
	.day + .day {
		margin-top: 1.5rem;
	}
	.day-h {
		position: sticky;
		top: 0;
		z-index: 2;
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		padding: 0.375rem 0 0.5rem;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		font-weight: 600;
		background: linear-gradient(var(--color-paper) 75%, transparent);
	}
	.thread {
		position: relative;
		overflow: hidden;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-tile);
		background: var(--color-panel);
	}
	.thread > :global(* + *) {
		border-top: 1px solid var(--color-rule-soft);
	}
</style>
