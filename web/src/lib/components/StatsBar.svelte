<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import { fmtBytes } from '$lib/api/client';
	import type { Bot, Gauge } from '$lib/api/types';
	import Icon from '$lib/components/ui/Icon.svelte';

	// Live usage as one compact strip in the page header. One SSE stream per
	// open bot page; the lists never open these. `extra` adds items such as a
	// game server's address and players to the same row.
	let { bot, extra }: { bot: Bot; extra?: Snippet } = $props();

	let g = $state<Gauge | null>(null);
	let link = $state<'connecting' | 'live' | 'lost'>('connecting');
	onMount(() => {
		const es = new EventSource(`/api/v1/bots/${bot.id}/stats/stream`);
		es.addEventListener('stats', (e) => {
			g = JSON.parse((e as MessageEvent).data);
			link = 'live';
		});
		es.onerror = () => (link = es.readyState === EventSource.CLOSED ? 'lost' : 'connecting');
		return () => es.close();
	});

	const pct = (a: number, b: number) => (b > 0 ? Math.min(100, (a / b) * 100) : 0);
	const live = $derived(!!g?.running);
	const meters = $derived([
		{ k: 'CPU', p: live ? Math.min(100, g!.cpu_percent) : 0, v: live ? `${g!.cpu_percent.toFixed(1)}%` : '0%' },
		{ k: 'Memory', p: live ? pct(g!.mem_used_bytes, g!.mem_limit_bytes) : 0, v: live ? fmtBytes(g!.mem_used_bytes) : '0 B', of: g?.mem_limit_bytes ? fmtBytes(g.mem_limit_bytes) : '' },
		g?.disk_scope === 'node'
			? { k: 'Node disk', p: pct(g.disk_total_bytes - g.disk_free_bytes, g.disk_total_bytes), v: g.disk_total_bytes > 0 ? `${fmtBytes(g.disk_free_bytes)} free` : '–' }
			: { k: 'Disk', p: g ? pct(g.disk_used_bytes, g.disk_used_bytes + g.disk_free_bytes) : 0, v: g ? fmtBytes(g.disk_used_bytes) : '–' }
	]);
	const level = (p: number) => (p >= 90 ? 'high' : p >= 75 ? 'warn' : 'ok');
</script>

<div class="stat-strip" role="group" aria-label="Resource usage">
	{#each meters as m (m.k)}
		<div title="{m.k}: {m.v}{m.of ? ` of ${m.of}` : ''}">
			<div class="flex items-baseline justify-between gap-2"><span class="stat-k shrink-0">{m.k}</span><span class="truncate font-mono text-[12.5px] tabular-nums">{m.v}</span></div>
			<div class="meter mt-1.5" data-level={level(m.p)} role="meter" aria-label="{m.k} usage" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(m.p)} aria-valuetext={m.v}>
				<i style="width: {Math.max(m.p, m.p > 0 ? 3 : 0)}%"></i>
			</div>
		</div>
	{/each}
	<div title="Network received and sent since the container started">
		<span class="stat-k">Network</span>
		<span class="stat-v flex items-center gap-2 tabular-nums">
			<span class="inline-flex min-w-0 items-center gap-0.5"><Icon name="download" size={11} class="shrink-0 text-muted" /><span class="sr-only">In</span>{g ? fmtBytes(g.net_rx_bytes) : '0 B'}</span>
			<span class="inline-flex min-w-0 items-center gap-0.5"><Icon name="upload" size={11} class="shrink-0 text-muted" /><span class="sr-only">Out</span>{g ? fmtBytes(g.net_tx_bytes) : '0 B'}</span>
		</span>
	</div>
	{#if extra}{@render extra()}{/if}
	{#if link === 'lost'}<span class="col-span-full text-warn">Live usage unavailable</span>{/if}
</div>
