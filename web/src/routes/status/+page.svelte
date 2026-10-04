<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { type StatusPage, type StatusIncident, stateLabel, overallLabel, stateTone, words } from '$lib/api/kb';
	import PublicShell from '$lib/components/PublicShell.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { fmtWhen } from '$lib/args';

	let data = $state<StatusPage | null>(null);
	let missing = $state(false);
	let error = $state('');

	async function load() {
		try {
			data = await api<StatusPage>('GET', '/status');
			error = '';
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) missing = true;
			else error = e instanceof ApiError ? e.message : 'The status could not be loaded.';
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => document.visibilityState === 'visible' && load(), 60_000);
		return () => clearInterval(t);
	});

	const open = $derived((data?.incidents ?? []).filter((i) => i.kind === 'incident' && !i.resolved_at_ms));
	const maint = $derived((data?.incidents ?? []).filter((i) => i.kind === 'maintenance' && !i.resolved_at_ms));
	const past = $derived((data?.incidents ?? []).filter((i) => i.resolved_at_ms));
	const dayTitle = (d: { date: string; state: string; uptime: number | null }) =>
		`${d.date}: ${d.uptime === null ? stateLabel[d.state] : `${d.uptime}% up` + (d.state !== 'operational' ? ` (${stateLabel[d.state].toLowerCase()})` : '')}`;
</script>

<svelte:head><title>{data?.title ?? 'Status'}</title></svelte:head>

{#snippet incident(i: StatusIncident)}
	<article class="rounded-tile border border-rule-soft bg-panel p-5">
		<div class="flex flex-wrap items-baseline gap-2">
			<h3 class="font-semibold">{i.title}</h3>
			<span class="pill capitalize">{words(i.status)}</span>
			{#if i.kind === 'incident' && i.impact !== 'none'}<span class="text-[12px] text-muted">{i.impact} impact</span>{/if}
		</div>
		{#if i.kind === 'maintenance' && i.starts_at_ms}<p class="mt-1 text-small text-muted">Window: {fmtWhen(i.starts_at_ms)}{#if i.ends_at_ms} – {fmtWhen(i.ends_at_ms)}{/if}</p>{/if}
		{#if i.components.length}<p class="mt-1 text-small text-muted">Affects {i.components.map((c) => c.name).join(', ')}</p>{/if}
		<ol class="mt-3 grid gap-3 border-l-2 border-rule-soft pl-4">
			{#each i.updates as u}
				<li><p class="text-small"><span class="font-medium capitalize">{words(u.status)}</span> <span class="text-muted">· {fmtWhen(u.created_at_ms)}</span></p><p class="mt-0.5 whitespace-pre-line">{u.body}</p></li>
			{/each}
		</ol>
	</article>
{/snippet}

<PublicShell always title={data?.title ?? 'Status'}>
	{#if missing}
		<EmptyState title="No status page here">This panel does not publish a status page.</EmptyState>
	{:else if error && !data}
		<Notice tone="fail" live>{error}</Notice>
	{:else if data}
		<h1 class="text-page font-semibold">{data.title}</h1>
		{#if data.intro}<p class="mt-1 max-w-prose whitespace-pre-line text-muted">{data.intro}</p>{/if}
		<div class="mt-6 flex flex-wrap items-center gap-3 rounded-tile border border-rule-soft bg-panel px-5 py-4" role="status">
			<span class="size-3.5 shrink-0 rounded-pill {stateTone[data.overall]?.bg}"></span>
			<p class="text-title font-semibold">{overallLabel[data.overall] ?? stateLabel[data.overall]}</p>
			<p class="ml-auto text-small text-muted">Checked {fmtWhen(data.updated_at_ms)}</p>
		</div>

		{#if open.length}<section class="mt-8"><h2 class="text-title font-semibold">Current incidents</h2><div class="mt-3 grid gap-3">{#each open as i (i.id)}{@render incident(i)}{/each}</div></section>{/if}
		{#if maint.length}<section class="mt-8"><h2 class="text-title font-semibold">Maintenance</h2><div class="mt-3 grid gap-3">{#each maint as i (i.id)}{@render incident(i)}{/each}</div></section>{/if}

		<section class="mt-8">
			<h2 class="text-title font-semibold">Components</h2>
			{#if !data.components.length}
				<p class="mt-2 text-muted">No components are published yet.</p>
			{:else}
				<ul class="list-card mt-3">
					{#each data.components as c (c.id)}
						<li class="p-4">
							<div class="flex flex-wrap items-baseline gap-x-3">
								<span class="font-medium">{c.name}</span>
								<span class="ml-auto text-small font-medium {stateTone[c.state]?.text}">{stateLabel[c.state]}</span>
							</div>
							{#if c.description}<p class="text-small text-muted">{c.description}</p>{/if}
							<div class="mt-2 flex h-7 gap-px" role="img" aria-label="{c.name}: {c.uptime === null ? 'no history yet' : `${c.uptime}% uptime`} over {data.retention_days} days">
								{#each c.days as d (d.date)}<span class="min-w-0 flex-1 rounded-[1px] {stateTone[d.state]?.bg ?? 'bg-rule-soft'}" title={dayTitle(d)}></span>{/each}
							</div>
							<div class="mt-1 flex text-[11px] text-muted"><span>{data.retention_days} days ago</span><span class="mx-auto">{c.uptime === null ? 'No history yet' : `${c.uptime}% uptime`}</span><span>Today</span></div>
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		{#if past.length}<section class="mt-8"><h2 class="text-title font-semibold">Past incidents (14 days)</h2><div class="mt-3 grid gap-3">{#each past as i (i.id)}{@render incident(i)}{/each}</div></section>{/if}
		<p class="mt-10 text-[12px] text-muted">States are sampled every 5 minutes; days without samples show as no data. Times are shown in your browser's time zone.</p>
	{/if}
</PublicShell>
