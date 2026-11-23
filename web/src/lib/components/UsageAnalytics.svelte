<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { fmtBps } from '$lib/api/admin';
	import type { Bot } from '$lib/api/types';
	import { botRanges, fmtCores, fmtCount, fmtDuration, fmtPct, fmtResolution, type BotUsage } from '$lib/api/usage';
	import { eventLabel, metrics, type BotSeries, type MetricEvent, type ProcessList } from '$lib/api/metrics';
	import { poll } from '$lib/poll';
	import UChart, { type Annotation, type ChartData } from '$lib/charts/UChart.svelte';
	import { ChartGroup, type Span } from '$lib/charts/sync.svelte';
	import { SeriesStore } from '$lib/charts/series.svelte';
	import { fmtStamp } from '$lib/charts/timeRange.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import ProcessTable from './ProcessTable.svelte';

	// A bot's or server's own usage over time: resources, uptime, crashes,
	// deployments and backups. Ranges up to 48 hours draw the 15-second
	// container samples; longer ones the panel's usage rollups. Charts share
	// one crosshair; drag to zoom, double-click to go back.
	let { bot }: { bot: Bot } = $props();

	const spans: Record<string, number> = { '1h': 3600_000, '24h': 86400_000, '7d': 7 * 86400_000, '30d': 30 * 86400_000, '90d': 90 * 86400_000 };
	const FINE_MAX = 48 * 3600_000;

	let range = $state<string>('24h');
	let data = $state.raw<BotUsage | null>(null);
	let error = $state('');
	let usageLoading = $state(false);
	const noun = $derived(bot.kind === 'game' ? 'server' : 'bot');

	async function load() {
		const asked = range;
		usageLoading = true;
		try {
			const d = await api<BotUsage>('GET', `/bots/${bot.id}/usage?range=${asked}`);
			if (asked === range) data = d;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Usage analytics are unavailable right now.';
		} finally {
			if (asked === range) usageLoading = false;
		}
	}
	onMount(() => poll(load, 60_000, ['bots', 'workspaces']));
	// A new range reloads at once (the poll loaded the first one); the old
	// figures stay on screen until the new ones arrive.
	let shown = '24h';
	$effect(() => {
		if (range === shown) return;
		shown = range;
		zoom = null;
		void load();
	});

	// Fine-grained series (ranges up to 48 h, and zooms inside them).
	const span = $derived(spans[range] ?? 86400_000);
	const fine = $derived(span <= FINE_MAX);
	let zoom = $state.raw<Span | null>(null);
	let win = $state.raw<Span>({ start: Date.now() - 86400_000, end: Date.now() });
	const stepFor = (ms: number) => Math.max(15_000, Math.ceil(ms / 600 / 15_000) * 15_000);
	const fineStore = new SeriesStore<BotSeries>((q, s) => metrics.botSeries(bot.id, { start: q.start, end: q.end, since: q.since, step: stepFor(q.end - q.start) }, s), { errorText: 'Detailed samples are unavailable right now.' });
	const group = new ChartGroup({
		onrange: (s) => (zoom = { start: s.start, end: Math.min(s.end, Date.now()) }),
		onreset: () => (zoom = null)
	});

	let events = $state.raw<MetricEvent[]>([]);
	let eventsOff = false; // no access to the node's events: stop asking
	async function loadEvents(w: Span) {
		if (eventsOff || !bot.node_id) return;
		try {
			const r = await metrics.events(bot.node_id, w);
			events = (r.events ?? []).filter((e) => e.bot_id === bot.id);
		} catch (e) {
			if (e instanceof ApiError && (e.status === 403 || e.status === 404)) eventsOff = true;
		}
	}

	async function loadFine(full: boolean) {
		const w = zoom ?? { start: Date.now() - span, end: Date.now() };
		win = w;
		const key = `bot:${bot.id}|${zoom ? `${Math.round(zoom.start)}-${Math.round(zoom.end)}` : `now-${range}`}`;
		const jobs: Promise<unknown>[] = [loadEvents(w)];
		if (fine) jobs.push(full ? fineStore.load(key, w, !zoom) : fineStore.refresh(w));
		await Promise.all(jobs);
	}
	$effect(() => {
		void range;
		void zoom;
		untrack(() => void loadFine(true));
		if (zoom) return;
		return poll(() => loadFine(false), 30_000, undefined, { immediate: false });
	});

	// Processes inside the container.
	let procs = $state.raw<ProcessList | null>(null);
	let procErr = $state('');
	onMount(() =>
		poll(async () => {
			try {
				procs = await metrics.botProcesses(bot.id);
				procErr = '';
			} catch (e) {
				procErr = e instanceof ApiError && e.status === 409 ? `The ${noun} is not running.` : 'The process list is unavailable right now.';
			}
		}, 15_000)
	);

	const pts = $derived(data?.points ?? []);
	const times = $derived(pts.map((p) => p.t));
	const ev = $derived(data?.events ?? []);
	const evTimes = $derived(ev.map((e) => e.t));
	const tot = $derived(data?.totals);
	const memLimit = $derived(fineStore.data?.mem_limit || Math.max(0, ...pts.map((p) => p.mem_limit)) || bot.memory_bytes || undefined);
	const cpuLimit = $derived(fineStore.data?.cpu_limit || (bot.nano_cpus ? bot.nano_cpus / 1e9 : undefined));
	const hasDisk = $derived(pts.some((p) => p.disk !== null));
	const nothing = $derived(!!data && pts.every((p) => p.cpu === null && p.uptime === null && p.net_rx === null) && ev.every((e) => !e.deploys_ok && !e.deploys_failed && !e.backups_ok && !e.backups_failed));
	const csv = $derived(`/api/v1/bots/${bot.id}/usage?range=${range}&format=csv`);

	const annotations = $derived<Annotation[]>(events.map((e) => ({ t: e.t_ms, kind: e.kind, label: `${eventLabel[e.kind] ?? e.kind}: ${e.text}` })));
	const xr = $derived<Span | null>(zoom ?? (fine ? win : data ? { start: data.from_ms, end: data.to_ms } : null));
	const f = $derived(fineStore.data);
	const fcol = (k: keyof BotSeries) => (f?.[k] as (number | null)[] | undefined) ?? [];
	const fTimes = $derived(f?.times ?? []);
	const col = (k: keyof (typeof pts)[number]) => pts.map((p) => p[k] as number | null);
	const usageData = $derived({
		cpu: { times, series: [col('cpu'), col('cpu_max')] },
		mem: { times, series: [col('mem'), col('mem_max')] },
		net: { times, series: [col('net_rx'), col('net_tx')] },
		uptime: { times, series: [col('uptime')] },
		crashes: { times, series: [col('crashes'), col('starts')] },
		disk: { times, series: [col('disk')] },
		deploys: { times: evTimes, series: [ev.map((e) => e.deploys_ok), ev.map((e) => e.deploys_failed)] },
		backups: { times: evTimes, series: [ev.map((e) => e.backups_ok), ev.map((e) => e.backups_failed)] }
	} satisfies Record<string, ChartData>);
	const fineData = $derived({
		cpu: { times: fTimes, series: [fcol('cpu')] },
		mem: { times: fTimes, series: [fcol('mem')] },
		net: { times: fTimes, series: [fcol('net_rx'), fcol('net_tx')] },
		io: { times: fTimes, series: [fcol('blk_read'), fcol('blk_write')] },
		pids: { times: fTimes, series: [fcol('pids')] }
	} satisfies Record<string, ChartData>);
	const pidsFmt = (n: number) => String(Math.round(n));
</script>

<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-title font-semibold">Analytics</h2>
		<p class="text-small text-muted">
			Resource use, uptime, crashes{bot.kind === 'game' ? '' : ', deployments'} and backups of this {noun}{#if data}, per {fmtResolution(data.resolution_s)}{/if}. Sampled once a minute while the panel runs.
		</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<div class="seg" role="group" aria-label="Time range">
			{#each botRanges as r (r.id)}
				<button class="seg-item" aria-pressed={range === r.id} onclick={() => (range = r.id)}>{r.label}</button>
			{/each}
		</div>
		<a class="btn btn-sm" href={csv} download>Export CSV</a>
	</div>
</div>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if nothing}
	<Notice class="mt-4">No usage recorded in this range yet. Figures appear once the {noun} has run while the panel was sampling (the current five minutes are included as they are collected).</Notice>
{/if}

{#if tot}
	<dl class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
		<div class="stat">
			<dt class="eyebrow">CPU</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.cpu_avg !== null ? fmtCores(tot.cpu_avg) : '—'}</dd>
			<dd class="text-small text-muted">{tot.cpu_max !== null ? `peak ${fmtCores(tot.cpu_max)}` : 'not measured'}{#if cpuLimit}{' '}· limit {fmtCores(cpuLimit)}{/if}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Memory</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.mem_avg !== null ? fmtBytes(tot.mem_avg) : '—'}</dd>
			<dd class="text-small text-muted">{tot.mem_max !== null ? `peak ${fmtBytes(tot.mem_max)}` : 'not measured'}{#if memLimit}{' '}· limit {fmtBytes(memLimit)}{/if}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Network</dt>
			<dd class="mt-1 font-mono text-body font-semibold">↓ {fmtBytes(tot.net_rx)} <span class="text-muted">·</span> ↑ {fmtBytes(tot.net_tx)}</dd>
			<dd class="text-small text-muted">received · sent in this range</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Uptime</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.uptime !== null ? fmtPct(tot.uptime) : '—'}</dd>
			<dd class="text-small text-muted">{tot.uptime !== null ? 'of the time it was meant to run' : `the ${noun} was not meant to run`}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Crashes and starts</dt>
			<dd class="mt-1 font-mono text-section font-semibold"><span class={tot.crashes ? 'text-fail' : ''}>{fmtCount(tot.crashes)}</span> <span class="text-small font-normal text-muted">crashes</span> · {fmtCount(tot.starts)} <span class="text-small font-normal text-muted">starts</span></dd>
			<dd class="text-small text-muted">crashes counted by the restart policy</dd>
		</div>
		{#if bot.kind !== 'game'}
			<div class="stat">
				<dt class="eyebrow">Deployments</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(tot.deploys_ok)} <span class="text-small font-normal text-muted">ok</span> · <span class={tot.deploys_failed ? 'text-fail' : ''}>{fmtCount(tot.deploys_failed)}</span> <span class="text-small font-normal text-muted">failed</span></dd>
				<dd class="text-small text-muted">{tot.deploy_avg_ms !== null ? `average ${fmtDuration(tot.deploy_avg_ms)}` : 'none in this range'}</dd>
			</div>
		{/if}
		<div class="stat">
			<dt class="eyebrow">Backups</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(tot.backups_ok)} <span class="text-small font-normal text-muted">ok</span> · <span class={tot.backups_failed ? 'text-fail' : ''}>{fmtCount(tot.backups_failed)}</span> <span class="text-small font-normal text-muted">failed</span></dd>
			<dd class="text-small text-muted">{tot.backup_bytes ? `${fmtBytes(tot.backup_bytes)} written` : 'none in this range'}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Disk</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.disk !== null ? fmtBytes(tot.disk) : '—'}</dd>
			<dd class="text-small text-muted">{tot.disk !== null ? 'largest workspace size seen' : 'measured for this panel’s own node only'}</dd>
		</div>
	</dl>

	<div class="mt-6 flex flex-wrap items-baseline justify-between gap-2">
		<p class="text-small text-muted">
			{#if zoom}<span class="font-mono tabular-nums">{fmtStamp(zoom.start, zoom.end - zoom.start)} – {fmtStamp(zoom.end, zoom.end - zoom.start)}</span> · <button class="link" onclick={() => (zoom = null)}>Back to {botRanges.find((r) => r.id === range)?.label}</button> ·{/if}
			{fine ? 'Samples every 15 seconds.' : `Per ${data ? fmtResolution(data.resolution_s) : 'bucket'}.`} Drag across a chart to zoom, double-click to go back.
		</p>
	</div>
	{#if fine && fineStore.error}<Notice tone="fail" class="mt-3">{fineStore.error}</Notice>{/if}
	<div class="mt-2 grid gap-3 lg:grid-cols-2">
		{#if fine}
			<div class="stat"><p class="eyebrow mb-2">CPU (cores)</p>
				<UChart label="CPU use" data={fineData.cpu} {group} xRange={xr} loading={fineStore.loading} ySoftMax={cpuLimit} format={fmtCores} {annotations} threshold={cpuLimit ? { value: cpuLimit, label: 'limit', color: 'fail' } : null} series={[{ label: 'CPU', color: 'data', fill: true }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Memory</p>
				<UChart label="Memory use" data={fineData.mem} {group} xRange={xr} loading={fineStore.loading} ySoftMax={memLimit} format={fmtBytes} {annotations} threshold={memLimit ? { value: memLimit, label: 'limit', color: 'fail' } : null} series={[{ label: 'Memory', color: 'run', fill: true }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Network throughput</p>
				<UChart label="Network throughput" data={fineData.net} {group} xRange={xr} loading={fineStore.loading} format={fmtBps} {annotations} series={[{ label: 'Received', color: 'run' }, { label: 'Sent', color: 'action' }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Disk I/O</p>
				<UChart label="Disk throughput" data={fineData.io} {group} xRange={xr} loading={fineStore.loading} format={fmtBps} {annotations} series={[{ label: 'Read', color: 'run' }, { label: 'Write', color: 'action' }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Processes and threads</p>
				<UChart label="Processes" data={fineData.pids} {group} xRange={xr} loading={fineStore.loading} format={pidsFmt} {annotations} series={[{ label: 'PIDs', color: 'muted', fill: true }]} />
			</div>
		{:else}
			<div class="stat"><p class="eyebrow mb-2">CPU (cores)</p>
				<UChart label="CPU use" data={usageData.cpu} {group} xRange={xr} loading={usageLoading} ySoftMax={cpuLimit} format={fmtCores} {annotations} series={[{ label: 'Average', color: 'data', fill: true }, { label: 'Peak', color: 'muted', dashed: true }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Memory</p>
				<UChart label="Memory use" data={usageData.mem} {group} xRange={xr} loading={usageLoading} ySoftMax={memLimit} format={fmtBytes} {annotations} series={[{ label: 'Average', color: 'run', fill: true }, { label: 'Peak', color: 'muted', dashed: true }]} />
			</div>
			<div class="stat"><p class="eyebrow mb-2">Network per {data ? fmtResolution(data.resolution_s) : 'bucket'}</p>
				<UChart label="Network traffic" data={usageData.net} {group} xRange={xr} loading={usageLoading} format={fmtBytes} {annotations} series={[{ label: 'Received', color: 'data', fill: true }, { label: 'Sent', color: 'muted' }]} />
			</div>
		{/if}
		<div class="stat"><p class="eyebrow mb-2">Uptime</p>
			<UChart label="Uptime" data={usageData.uptime} {group} xRange={xr} loading={usageLoading} yMax={100} format={fmtPct} {annotations} series={[{ label: 'Uptime', color: 'run', fill: true }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Crashes and starts</p>
			<UChart label="Crashes and starts" data={usageData.crashes} {group} xRange={xr} loading={usageLoading} format={fmtCount} {annotations} series={[{ label: 'Crashes', color: 'fail' }, { label: 'Starts', color: 'action' }]} />
		</div>
		{#if hasDisk}
			<div class="stat"><p class="eyebrow mb-2">Workspace size</p>
				<UChart label="Workspace size" data={usageData.disk} {group} xRange={xr} loading={usageLoading} format={fmtBytes} series={[{ label: 'Disk', color: 'muted', fill: true }]} />
			</div>
		{/if}
		{#if bot.kind !== 'game'}
			<div class="stat"><p class="eyebrow mb-2">Deployments per {data ? fmtResolution(data.event_resolution_s) : 'hour'}</p>
				<UChart label="Deployments" data={usageData.deploys} {group} xRange={xr} loading={usageLoading} format={fmtCount} series={[{ label: 'Succeeded', color: 'run' }, { label: 'Failed', color: 'fail' }]} />
			</div>
		{/if}
		<div class="stat"><p class="eyebrow mb-2">Backups per {data ? fmtResolution(data.event_resolution_s) : 'hour'}</p>
			<UChart label="Backups" data={usageData.backups} {group} xRange={xr} loading={usageLoading} format={fmtCount} series={[{ label: 'Succeeded', color: 'run' }, { label: 'Failed', color: 'fail' }]} />
		</div>
	</div>

	<section class="mt-8" aria-labelledby="bot-procs-h">
		<div class="flex flex-wrap items-baseline justify-between gap-2">
			<h3 id="bot-procs-h" class="text-title font-semibold">Processes inside this {noun}</h3>
			{#if procs}<p class="text-small text-muted">Sampled <span class="font-mono tabular-nums">{fmtStamp(procs.sampled_at_ms, 0)}</span> · refreshed every 15 seconds</p>{/if}
		</div>
		<div class="card mt-3 p-2 sm:p-3">
			{#if procs}<ProcessTable items={procs.items} showApp={false} stable label="Processes inside this {noun}" />
			{:else if procErr}<p class="flex items-center gap-2 p-2 text-small text-muted"><Icon name="info" size={14} />{procErr}</p>
			{:else}<p class="p-2 text-small text-muted">Loading…</p>{/if}
		</div>
	</section>
{:else if !error}
	<p class="mt-4 text-muted">Loading…</p>
{/if}
