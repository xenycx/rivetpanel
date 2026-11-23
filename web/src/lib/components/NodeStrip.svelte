<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fmtBytes } from '$lib/api/client';
	import { fmtAgo } from '$lib/args';
	import type { NodeInfo, Sample } from '$lib/api/types';
	import { poll } from '$lib/poll';
	import Sparkline from './Sparkline.svelte';

	let nodes = $state.raw<NodeInfo[]>([]);
	let series = $state.raw<Record<string, Sample[]>>({});
	let error = $state('');

	async function load() {
		try {
			const list = (await api<{ nodes: NodeInfo[] }>('GET', '/nodes')).nodes;
			// All nodes at once; one slow or failing node keeps its last samples.
			const got = await Promise.allSettled(list.map((n) => api<{ samples: Sample[] }>('GET', `/nodes/${n.id}/telemetry?limit=60`)));
			const next: Record<string, Sample[]> = {};
			list.forEach((n, i) => {
				const r = got[i];
				next[n.id] = r.status === 'fulfilled' ? r.value.samples : (series[n.id] ?? []);
			});
			nodes = list;
			series = next;
			error = '';
		} catch {
			if (!nodes.length) error = 'Node metrics are unavailable right now.';
		}
	}
	// Paused while the tab is hidden; never two loads at once.
	onMount(() => poll(load, 30_000));
	const pct = (a: number, b: number) => (b ? Math.round((a / b) * 100) : 0);
</script>

{#if error}
	<p class="text-muted">{error}</p>
{:else}
	{#each nodes as n (n.id)}
		{@const s = series[n.id] ?? []}
		{@const last = s.at(-1)}
		<section class="card p-4 sm:p-5 [&+&]:mt-4" aria-label="Node {n.name}">
			<div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
				<h3 class="text-title font-semibold">Node {n.name}</h3>
				{#if last}<p class="text-small text-muted">Last sample {fmtAgo(last.sampled_at_ms)} · last hour shown</p>{/if}
			</div>
			{#if last}
				{@const disk = pct(last.disk_used_bytes, last.disk_total_bytes)}
				<dl class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
					<div class="stat">
						<dt class="eyebrow">CPU · {last.logical_cpus} cores</dt>
						<dd class="mt-1 font-mono text-section font-semibold">{last.cpu_percent.toFixed(0)}%</dd>
						<dd class="mt-2"><Sparkline fluid height={32} values={s.map((x) => x.cpu_percent)} label="CPU history" /></dd>
					</div>
					<div class="stat">
						<dt class="eyebrow">Memory</dt>
						<dd class="mt-1 font-mono text-section font-semibold">{pct(last.memory_used_bytes, last.memory_total_bytes)}%<span class="ml-2 text-small font-normal text-muted">{fmtBytes(last.memory_used_bytes)} of {fmtBytes(last.memory_total_bytes)}</span></dd>
						<dd class="mt-2"><Sparkline fluid height={32} values={s.map((x) => pct(x.memory_used_bytes, x.memory_total_bytes))} label="Memory history" /></dd>
					</div>
					<div class="stat">
						<dt class="eyebrow">Disk</dt>
						<dd class="mt-1 font-mono text-section font-semibold">{disk}%<span class="ml-2 text-small font-normal text-muted">{fmtBytes(last.disk_used_bytes)} of {fmtBytes(last.disk_total_bytes)}</span></dd>
						<dd class="mt-4 h-1.5 overflow-hidden rounded-pill bg-rule-soft" role="meter" aria-label="Disk used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={disk}>
							<div class="h-full rounded-pill {disk >= 90 ? 'bg-fail' : disk >= 75 ? 'bg-warn' : 'bg-ink/50'}" style="width: {disk}%"></div>
						</dd>
						<dd class="mt-1.5 text-small text-muted">{fmtBytes(last.disk_total_bytes - last.disk_used_bytes)} free</dd>
					</div>
					<div class="stat">
						<dt class="eyebrow">Running apps</dt>
						<dd class="mt-1 font-mono text-section font-semibold">{last.running_bots}</dd>
						<dd class="mt-2"><Sparkline fluid height={32} max={Math.max(1, ...s.map((x) => x.running_bots))} values={s.map((x) => x.running_bots)} label="Running apps history" /></dd>
					</div>
				</dl>
			{:else}
				<p class="mt-2 text-muted">Collecting the first samples. Metrics appear after about a minute.</p>
			{/if}
		</section>
	{/each}
{/if}
