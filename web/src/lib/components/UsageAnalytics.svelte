<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { botRanges, fmtCores, fmtCount, fmtDuration, fmtPct, fmtResolution, type BotUsage } from '$lib/api/usage';
	import { poll } from '$lib/poll';
	import Notice from '$lib/components/ui/Notice.svelte';
	import TimeChart from './TimeChart.svelte';

	// A bot's or server's own usage over time: resources, uptime, crashes,
	// deployments and backups, from the panel's usage rollups.
	let { bot }: { bot: Bot } = $props();

	let range = $state<string>('24h');
	let data = $state<BotUsage | null>(null);
	let error = $state('');
	const noun = $derived(bot.kind === 'game' ? 'server' : 'bot');

	async function load() {
		const asked = range;
		try {
			const d = await api<BotUsage>('GET', `/bots/${bot.id}/usage?range=${asked}`);
			if (asked === range) data = d;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Usage analytics are unavailable right now.';
		}
	}
	onMount(() => poll(load, 60_000));
	// A new range reloads at once (the poll loaded the first one).
	let shown = '24h';
	$effect(() => {
		if (range === shown) return;
		shown = range;
		data = null;
		void load();
	});

	const pts = $derived(data?.points ?? []);
	const times = $derived(pts.map((p) => p.t));
	const ev = $derived(data?.events ?? []);
	const evTimes = $derived(ev.map((e) => e.t));
	const tot = $derived(data?.totals);
	const memLimit = $derived(Math.max(0, ...pts.map((p) => p.mem_limit)) || bot.memory_bytes || undefined);
	const cpuLimit = $derived(bot.nano_cpus ? bot.nano_cpus / 1e9 : undefined);
	const hasDisk = $derived(pts.some((p) => p.disk !== null));
	const nothing = $derived(!!data && pts.every((p) => p.cpu === null && p.uptime === null && p.net_rx === null) && ev.every((e) => !e.deploys_ok && !e.deploys_failed && !e.backups_ok && !e.backups_failed));
	const csv = $derived(`/api/v1/bots/${bot.id}/usage?range=${range}&format=csv`);
</script>

<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-title font-semibold">Analytics</h2>
		<p class="text-small text-muted">
			Resource use, uptime, crashes{bot.kind === 'game' ? '' : ', deployments'} and backups of this {noun}{#if data}, per {fmtResolution(data.resolution_s)}{/if}. Sampled once a minute while the panel runs.
		</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Time range">
			{#each botRanges as r (r.id)}
				<button class="rounded-inner px-2.5 py-1 text-small font-medium {range === r.id ? 'bg-paper-2 text-ink shadow-[inset_0_0_0_1px_var(--color-rule)]' : 'text-muted hover:text-ink'}" aria-pressed={range === r.id} onclick={() => (range = r.id)}>{r.label}</button>
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
			<dd class="text-small text-muted">{tot.cpu_max !== null ? `peak ${fmtCores(tot.cpu_max)}` : 'not measured'}{#if cpuLimit} · limit {fmtCores(cpuLimit)}{/if}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Memory</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.mem_avg !== null ? fmtBytes(tot.mem_avg) : '—'}</dd>
			<dd class="text-small text-muted">{tot.mem_max !== null ? `peak ${fmtBytes(tot.mem_max)}` : 'not measured'}{#if memLimit} · limit {fmtBytes(memLimit)}{/if}</dd>
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

	<div class="mt-6 grid gap-3 lg:grid-cols-2">
		<div class="stat"><p class="eyebrow mb-2">CPU (cores)</p>
			<TimeChart label="CPU use" {times} max={cpuLimit} format={fmtCores} series={[{ label: 'Average', values: pts.map((p) => p.cpu), area: true }, { label: 'Peak', values: pts.map((p) => p.cpu_max), dashed: true, color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Memory</p>
			<TimeChart label="Memory use" {times} max={memLimit} format={fmtBytes} series={[{ label: 'Average', values: pts.map((p) => p.mem), area: true, color: 'run' }, { label: 'Peak', values: pts.map((p) => p.mem_max), dashed: true, color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Network per {data ? fmtResolution(data.resolution_s) : 'bucket'}</p>
			<TimeChart label="Network traffic" {times} format={fmtBytes} series={[{ label: 'Received', values: pts.map((p) => p.net_rx), area: true }, { label: 'Sent', values: pts.map((p) => p.net_tx), color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Uptime</p>
			<TimeChart label="Uptime" {times} max={100} format={fmtPct} series={[{ label: 'Uptime', values: pts.map((p) => p.uptime), area: true, color: 'run' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Crashes and starts</p>
			<TimeChart label="Crashes and starts" {times} format={fmtCount} series={[{ label: 'Crashes', values: pts.map((p) => p.crashes), color: 'fail' }, { label: 'Starts', values: pts.map((p) => p.starts), color: 'action' }]} />
		</div>
		{#if hasDisk}
			<div class="stat"><p class="eyebrow mb-2">Workspace size</p>
				<TimeChart label="Workspace size" {times} format={fmtBytes} series={[{ label: 'Disk', values: pts.map((p) => p.disk), area: true, color: 'muted' }]} />
			</div>
		{/if}
		{#if bot.kind !== 'game'}
			<div class="stat"><p class="eyebrow mb-2">Deployments per {data ? fmtResolution(data.event_resolution_s) : 'hour'}</p>
				<TimeChart label="Deployments" times={evTimes} format={fmtCount} series={[{ label: 'Succeeded', values: ev.map((e) => e.deploys_ok), color: 'run' }, { label: 'Failed', values: ev.map((e) => e.deploys_failed), color: 'fail' }]} />
			</div>
		{/if}
		<div class="stat"><p class="eyebrow mb-2">Backups per {data ? fmtResolution(data.event_resolution_s) : 'hour'}</p>
			<TimeChart label="Backups" times={evTimes} format={fmtCount} series={[{ label: 'Succeeded', values: ev.map((e) => e.backups_ok), color: 'run' }, { label: 'Failed', values: ev.map((e) => e.backups_failed), color: 'fail' }]} />
		</div>
	</div>
{:else if !error}
	<p class="mt-4 text-muted">Loading…</p>
{/if}
