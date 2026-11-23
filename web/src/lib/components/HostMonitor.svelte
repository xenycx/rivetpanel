<script lang="ts">
	import { untrack } from 'svelte';
	import { fmtBytes } from '$lib/api/client';
	import { fmtBps } from '$lib/api/admin';
	import { fmtCores } from '$lib/api/usage';
	import { metrics, eventLabel, type LiveSample, type MetricEvent, type NodeBotsSeries, type NodeSeries, type ProcessList, type Spike } from '$lib/api/metrics';
	import { poll } from '$lib/poll';
	import UChart, { type Annotation, type ChartData, type ChartSeries } from '$lib/charts/UChart.svelte';
	import RangePicker from '$lib/charts/RangePicker.svelte';
	import { ChartGroup } from '$lib/charts/sync.svelte';
	import { SeriesStore } from '$lib/charts/series.svelte';
	import { TimeRange, fmtStamp } from '$lib/charts/timeRange.svelte';
	import { cat } from '$lib/charts/colors';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import MomentDrawer from './MomentDrawer.svelte';
	import ProcessTable from './ProcessTable.svelte';

	// Host history: synced charts over one time range, CPU spikes and what
	// caused them, events, and the live process list.
	let {
		nodeId,
		nodeName,
		cores = 1,
		series = $bindable(null)
	}: { nodeId: string; nodeName: string; cores?: number; series?: NodeSeries | null } = $props();

	const range = new TimeRange({ preset: '1h', url: true, refreshMs: 30_000 });
	range.bindUrl();
	let lastPreset = untrack(() => range.preset?.id ?? '1h');
	$effect(() => {
		if (range.preset) lastPreset = range.preset.id;
	});
	const group = new ChartGroup({ onrange: (s) => range.setAbsolute(s.start, s.end), onreset: () => range.setPreset(lastPreset) });

	const host = new SeriesStore<NodeSeries>((q, s) => metrics.nodeSeries(nodeId, q, s), { errorText: 'Host history is unavailable right now.' });
	const cpuApps = new SeriesStore<NodeBotsSeries>((q, s) => metrics.nodeBotsSeries(nodeId, { ...q, metric: 'cpu' }, s), { incremental: false, errorText: 'Usage per app is unavailable right now.' });
	const memApps = new SeriesStore<NodeBotsSeries>((q, s) => metrics.nodeBotsSeries(nodeId, { ...q, metric: 'mem' }, s), { incremental: false, errorText: 'Usage per app is unavailable right now.' });

	let win = $state.raw<{ start: number; end: number } | null>(null);
	let events = $state.raw<MetricEvent[]>([]);
	let spikes = $state.raw<Spike[]>([]);
	let threshold = $state(80);
	let slowAt = 0;
	let slowCtrl: AbortController | null = null;

	$effect(() => {
		series = host.data;
	});

	/** Events and spikes, and the per-app breakdowns: at most every 15 s. */
	async function loadSlow(w: { start: number; end: number }, force: boolean) {
		if (!force && Date.now() - slowAt < 15_000) return;
		slowAt = Date.now();
		slowCtrl?.abort();
		const c = (slowCtrl = new AbortController());
		const [ev, sp] = await Promise.allSettled([metrics.events(nodeId, w, c.signal), metrics.spikes(nodeId, w, c.signal)]);
		if (c.signal.aborted) return;
		if (ev.status === 'fulfilled') events = ev.value.events ?? [];
		if (sp.status === 'fulfilled') {
			spikes = sp.value.spikes ?? [];
			if (sp.value.threshold_pct) threshold = sp.value.threshold_pct;
		}
	}

	async function loadAll(full: boolean) {
		const w = range.resolve();
		const key = `${nodeId}|${range.key}`;
		win = w;
		if (full) {
			await Promise.all([host.load(key, w, range.relative), cpuApps.load(key, w, range.relative), memApps.load(key, w, range.relative), loadSlow(w, true)]);
		} else {
			const slow = Date.now() - slowAt >= 15_000;
			await Promise.all([host.refresh(w), slow ? cpuApps.refresh(w) : null, slow ? memApps.refresh(w) : null, loadSlow(w, false)]);
		}
	}

	function stream(id: string): () => void {
		let es: EventSource | null = null;
		const open = () => {
			if (es) return;
			es = new EventSource(metrics.streamPath(id));
			es.addEventListener('sample', (e) => {
				let p: LiveSample;
				try {
					p = JSON.parse((e as MessageEvent).data);
				} catch {
					return;
				}
				const w = range.resolve(Math.max(Date.now(), p.t));
				win = w;
				host.push(p.t, { cpu: p.cpu, cpu_max: p.cpu, mem: p.mem, mem_max: p.mem, load1: p.load1, net_rx: p.net_rx, net_tx: p.net_tx, disk_read: p.disk_read, disk_write: p.disk_write, swap: p.swap }, w.start);
				cpuApps.trim(w.start);
				memApps.trim(w.start);
			});
		};
		const close = () => {
			es?.close();
			es = null;
		};
		const vis = () => {
			if (document.visibilityState === 'visible') {
				void loadAll(false);
				open();
			} else close();
		};
		if (document.visibilityState === 'visible') open();
		document.addEventListener('visibilitychange', vis);
		// The per-app charts and events still refresh, more slowly.
		const stopSlow = poll(() => loadAll(false), 30_000, undefined, { immediate: false });
		return () => {
			close();
			stopSlow();
			document.removeEventListener('visibilitychange', vis);
		};
	}

	$effect(() => {
		const id = nodeId;
		void range.key;
		const ms = range.refreshMs;
		const live = range.live && range.canLive;
		const moving = range.relative;
		if (!id) return;
		untrack(() => void loadAll(true));
		if (live) return stream(id);
		if (!ms || !moving) return;
		return poll(() => loadAll(false), ms, undefined, { immediate: false });
	});

	// Stable colours per app across both "by app" charts and the drawer.
	const colorIdx = new Map<string, number>();
	const colorOf = (id: string) => {
		if (!colorIdx.has(id)) colorIdx.set(id, colorIdx.size);
		return colorIdx.get(id)!;
	};

	const h = $derived(host.data);
	const times = $derived(h?.times ?? []);
	const xr = $derived(win);
	const pctFmt = (n: number) => `${Math.round(n)}%`;
	const loadFmt = (n: number) => n.toFixed(n < 10 ? 2 : 1);

	const annotations = $derived.by<Annotation[]>(() => [
		...spikes.map((s) => ({ t: s.started_at_ms, end: s.ended_at_ms, kind: 'spike', label: `CPU spike: peak ${s.peak_cpu_pct.toFixed(0)}%${s.top_bots[0] ? `, mostly ${s.top_bots[0].name}` : topCommand(s) ? `, mostly ${topCommand(s)}` : ''}` })),
		...events.filter((e) => e.kind !== 'spike').map((e) => ({ t: e.t_ms, kind: e.kind, label: `${eventLabel[e.kind] ?? e.kind}${e.bot_name ? ` · ${e.bot_name}` : ''}: ${e.text}` }))
	]);

	const col = (k: keyof NodeSeries) => (h?.[k] as (number | null)[] | undefined) ?? [];
	const cpuData = $derived<ChartData>({ times, series: [col('cpu'), col('cpu_max')] });
	const memData = $derived<ChartData>({ times, series: [col('mem'), col('mem_max')] });
	const loadData = $derived<ChartData>({ times, series: [col('load1')] });
	const netData = $derived<ChartData>({ times, series: [col('net_rx'), col('net_tx')] });
	const ioData = $derived<ChartData>({ times, series: [col('disk_read'), col('disk_write')] });
	const diskData = $derived<ChartData>({ times, series: [col('disk_used')] });
	const swapData = $derived<ChartData>({ times, series: [col('swap')] });
	const hasSwap = $derived(col('swap').some((v) => v !== null && v > 0));

	function byApp(d: NodeBotsSeries | null): { data: ChartData; series: ChartSeries[] } {
		if (!d) return { data: { times: [], series: [] }, series: [] };
		const bots = d.bots ?? [];
		return {
			data: { times: d.times, series: [...bots.map((b) => b.values), d.other ?? [], d.unattributed ?? []] },
			series: [
				...bots.map((b) => ({ label: b.name, color: cat(colorOf(b.bot_id)), stacked: true })),
				{ label: 'Other apps', color: 'muted', stacked: true },
				{ label: 'Host and system', color: 'data', stacked: true }
			]
		};
	}
	const cpuByApp = $derived(byApp(cpuApps.data));
	const memByApp = $derived(byApp(memApps.data));

	// The drawer.
	let moment = $state<number | null>(null);
	function pick(t: number, a: Annotation | null) {
		moment = a?.kind === 'spike' && a.end !== undefined ? Math.max(a.t, Math.min(t, a.end ?? t)) : t;
	}
	function zoomTo(start: number, end: number) {
		range.setAbsolute(start, end);
	}
	function zoomSpike(s: Spike) {
		const end = s.ended_at_ms ?? Date.now();
		const pad = Math.max(60_000, (end - s.started_at_ms) * 0.5);
		zoomTo(s.started_at_ms - pad, Math.min(Date.now(), end + pad));
	}
	const dur = (s: Spike) => {
		const ms = (s.ended_at_ms ?? Date.now()) - s.started_at_ms;
		return ms < 60_000 ? `${Math.round(ms / 1000)} s` : ms < 3600_000 ? `${Math.round(ms / 60_000)} min` : `${(ms / 3600_000).toFixed(1)} h`;
	};
	// The busiest command: processes with the same name in the same app add
	// up, so eleven workers at one core each outrank one process at 1.5.
	function topCommand(s: Spike): string | null {
		const groups = new Map<string, { comm: string; n: number; cpu: number }>();
		for (const p of s.top_processes) {
			const k = `${p.comm}\u0000${p.bot_id ?? ''}`;
			const g = groups.get(k) ?? { comm: p.comm, n: 0, cpu: 0 };
			g.n++;
			g.cpu += p.cpu_pct;
			groups.set(k, g);
		}
		let best: { comm: string; n: number; cpu: number } | null = null;
		for (const g of groups.values()) if (!best || g.cpu > best.cpu) best = g;
		return best ? (best.n > 1 ? `${best.comm} ×${best.n}` : best.comm) : null;
	}
	const cause = (s: Spike) => s.top_bots[0] ? `${s.top_bots[0].name} (${s.top_bots[0].share_pct.toFixed(0)}%)` : (topCommand(s) ?? 'unknown');
	const recentSpikes = $derived([...spikes].sort((a, b) => b.started_at_ms - a.started_at_ms).slice(0, 12));

	// Live processes.
	let procs = $state.raw<ProcessList | null>(null);
	let procErr = $state('');
	let procBusy = $state(false);
	async function loadProcs() {
		procBusy = true;
		try {
			procs = await metrics.processes(nodeId, 25);
			procErr = '';
		} catch {
			procErr = 'The process list is unavailable right now.';
		} finally {
			procBusy = false;
		}
	}
	$effect(() => {
		const ms = range.live ? 5_000 : range.refreshMs;
		if (!nodeId) return;
		if (!ms) {
			untrack(() => void loadProcs());
			return;
		}
		return poll(loadProcs, Math.max(5_000, ms));
	});

	const loading = $derived(host.loading);
	const span = $derived(xr ? xr.end - xr.start : 0);
</script>

<section class="mt-8" aria-labelledby="hist-h">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div class="min-w-0">
			<h3 id="hist-h" class="text-title font-semibold">History</h3>
			<p class="text-small text-muted">
				{#if h}<span class="font-mono tabular-nums">{range.label}</span> · {fmtStepLabel(h.step_ms)} buckets.{/if}
				Drag across a chart to zoom, double-click to go back, click a moment to see what caused it.
			</p>
		</div>
		<RangePicker {range} />
	</div>

	{#if host.error}<Notice tone="fail" class="mt-3">{host.error}</Notice>{/if}

	<!-- Spike timeline -->
	<div class="mt-3 stat" aria-labelledby="spikes-h">
		<div class="flex flex-wrap items-baseline justify-between gap-2">
			<p id="spikes-h" class="eyebrow">CPU spikes in this range</p>
			<p class="text-small text-muted">A spike is host CPU at or above {threshold}% for 10 seconds or more.</p>
		</div>
		{#if recentSpikes.length}
			<ul class="mt-2 grid gap-1 sm:grid-cols-2 xl:grid-cols-3">
				{#each recentSpikes as s (s.id)}
					<li class="flex min-w-0 items-center gap-1 rounded-control hover:bg-paper-2/60">
						<button class="flex min-w-0 flex-1 items-baseline gap-2 px-2 py-1 text-left text-small" onclick={() => zoomSpike(s)} title="Zoom the charts to this spike">
							<span class="inline-block size-2 shrink-0 self-center rounded-pill {s.ended_at_ms === null ? 'bg-fail motion-safe:animate-pulse' : 'bg-warn'}"></span>
							<span class="shrink-0 font-mono tabular-nums">{fmtStamp(s.started_at_ms, span)}</span>
							<span class="shrink-0 font-mono text-muted tabular-nums">{dur(s)} · {s.peak_cpu_pct.toFixed(0)}%</span>
							<span class="min-w-0 truncate">{cause(s)}</span>
						</button>
						<button class="btn btn-quiet btn-icon btn-sm shrink-0" aria-label="What happened at {fmtStamp(s.started_at_ms, span)}" title="What happened here?" onclick={() => (moment = s.started_at_ms)}><Icon name="info" size={14} /></button>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="mt-1 text-small text-muted">No CPU spikes in this range.</p>
		{/if}
	</div>

	<div class="mt-3 grid gap-3 lg:grid-cols-2">
		<div class="stat"><p class="eyebrow mb-2">CPU · {cores} core{cores === 1 ? '' : 's'}</p>
			<UChart label="CPU use" data={cpuData} {group} xRange={xr} {loading} yMax={100} format={pctFmt} {annotations} threshold={{ value: threshold, label: `spike ≥ ${threshold}%` }} onpick={pick}
				series={[{ label: 'Average', color: 'data', fill: true }, { label: 'Peak', color: 'muted', dashed: true }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">CPU by app (cores)</p>
			<UChart label="CPU by app" data={cpuByApp.data} series={cpuByApp.series} {group} xRange={xr} loading={cpuApps.loading} format={fmtCores} {annotations} onpick={pick} empty={cpuApps.error || 'No per-app samples in this range yet.'} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Memory</p>
			<UChart label="Memory use" data={memData} {group} xRange={xr} {loading} yMax={h?.mem_total || undefined} format={fmtBytes} {annotations} onpick={pick}
				series={[{ label: 'Used', color: 'run', fill: true }, { label: 'Peak', color: 'muted', dashed: true }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Memory by app</p>
			<UChart label="Memory by app" data={memByApp.data} series={memByApp.series} {group} xRange={xr} loading={memApps.loading} format={fmtBytes} {annotations} onpick={pick} empty={memApps.error || 'No per-app samples in this range yet.'} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Load average (1 min)</p>
			<UChart label="Load average" data={loadData} {group} xRange={xr} {loading} format={loadFmt} {annotations} threshold={{ value: cores, label: `${cores} cores`, color: 'muted' }} onpick={pick}
				series={[{ label: 'Load', color: 'warn', fill: true }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Network throughput</p>
			<UChart label="Network throughput" data={netData} {group} xRange={xr} {loading} format={fmtBps} {annotations} onpick={pick}
				series={[{ label: 'Received', color: 'run' }, { label: 'Sent', color: 'action' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Disk I/O</p>
			<UChart label="Disk throughput" data={ioData} {group} xRange={xr} {loading} format={fmtBps} {annotations} onpick={pick}
				series={[{ label: 'Read', color: 'run' }, { label: 'Write', color: 'action' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Disk space used</p>
			<UChart label="Disk space used" data={diskData} {group} xRange={xr} {loading} yMax={h?.disk_total || undefined} format={fmtBytes} {annotations} onpick={pick}
				series={[{ label: 'Used', color: 'warn', fill: true }]} />
		</div>
		{#if hasSwap}
			<div class="stat lg:col-span-2"><p class="eyebrow mb-2">Swap used</p>
				<UChart label="Swap used" data={swapData} {group} xRange={xr} {loading} height={110} format={fmtBytes} {annotations} onpick={pick}
					series={[{ label: 'Swap', color: 'fail', fill: true }]} />
			</div>
		{/if}
	</div>
</section>

<section class="mt-8" aria-labelledby="procs-h">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<h3 id="procs-h" class="text-title font-semibold">Processes</h3>
			<p class="text-small text-muted">
				The busiest processes on the host right now{#if procs}, sampled <span class="font-mono tabular-nums">{fmtStamp(procs.sampled_at_ms, 0)}</span>{/if}.
				{range.live ? 'Refreshed every 5 seconds.' : range.refreshMs ? `Refreshed with the charts.` : 'Refresh is off.'}
			</p>
		</div>
		<button class="btn btn-sm" onclick={loadProcs} disabled={procBusy}><Icon name="restart" size={14} />Refresh</button>
	</div>
	{#if procErr}<Notice tone="fail" class="mt-3">{procErr}</Notice>{/if}
	{#if procs?.source === 'docker-top'}<Notice class="mt-3">The panel cannot see host processes, so only processes inside app containers are listed.</Notice>{/if}
	<div class="card mt-3 p-2 sm:p-3">
		{#if procs}<ProcessTable items={procs.items} stable label="Live top processes" />{:else if !procErr}<p class="p-2 text-small text-muted">Loading…</p>{/if}
	</div>
</section>

<MomentDrawer {nodeId} {nodeName} bind:t={moment} onzoom={zoomTo} {colorOf} />

<script lang="ts" module>
	function fmtStepLabel(ms: number): string {
		if (!ms) return 'Raw';
		if (ms >= 3600_000) return `${+(ms / 3600_000).toFixed(1)}-hour`;
		if (ms >= 60_000) return `${Math.round(ms / 60_000)}-minute`;
		return `${Math.round(ms / 1000)}-second`;
	}
</script>
