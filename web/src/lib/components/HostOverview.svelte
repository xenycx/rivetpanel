<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fmtBytes } from '$lib/api/client';
	import { fmtBps, fmtUptime, pct, tone, ranges, type History, type HistoryPoint, type HostSnapshot, type Range } from '$lib/api/admin';
	import type { NodeInfo, Sample } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { poll } from '$lib/poll';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import TimeChart from '$lib/components/TimeChart.svelte';

	let { snap, goto }: { snap: HostSnapshot | null; goto: (tab: string) => void } = $props();

	let range = $state<Range>('1h');
	let hist = $state<History | null>(null);
	let node = $state<NodeInfo | null>(null);
	let error = $state('');
	let loading = $state(false);

	async function loadNode() {
		try {
			node = (await api<{ nodes: NodeInfo[] }>('GET', '/nodes')).nodes[0] ?? null;
		} catch {
			/* the charts below report the failure */
		}
	}
	async function loadHistory() {
		if (!node) return;
		const asked = range;
		loading = true;
		try {
			const h = await api<History>('GET', `/nodes/${node.id}/history?range=${asked}`);
			if (asked === range) hist = h;
			error = '';
		} catch {
			error = 'Host history is unavailable right now.';
		} finally {
			loading = false;
		}
	}
	onMount(() => {
		const stopNode = poll(loadNode, 15_000);
		const stopHist = poll(loadHistory, 30_000);
		return () => {
			stopNode();
			stopHist();
		};
	});
	$effect(() => {
		void range;
		hist = null;
		void loadHistory();
	});
	$effect(() => {
		if (node) void loadHistory();
	});

	const pts = $derived<HistoryPoint[]>(hist?.points ?? []);
	const times = $derived(pts.map((p) => p.sampled_at_ms));
	const last = $derived<Sample | undefined>(node?.latest);
	const cores = $derived(last?.logical_cpus ?? snap?.host.cores ?? 1);
	const memPct = $derived(last ? pct(last.memory_used_bytes, last.memory_total_bytes) : 0);
	const diskPct = $derived(last ? pct(last.disk_used_bytes, last.disk_total_bytes) : 0);

	// Linear trend of disk use over the shown range: how soon the data disk fills.
	const diskFull = $derived.by(() => {
		if (pts.length < 8) return null;
		const t0 = pts[0].sampled_at_ms;
		if (pts.at(-1)!.sampled_at_ms - t0 < 6 * 3600_000) return null;
		const n = pts.length;
		let sx = 0, sy = 0, sxx = 0, sxy = 0;
		for (const p of pts) {
			const xv = (p.sampled_at_ms - t0) / 86400_000; // days
			sx += xv; sy += p.disk_used_bytes; sxx += xv * xv; sxy += xv * p.disk_used_bytes;
		}
		const d = n * sxx - sx * sx;
		if (d === 0) return null;
		const slope = (n * sxy - sx * sy) / d; // bytes per day
		const p = pts.at(-1)!;
		if (slope <= 0 || slope * 1 < p.disk_total_bytes * 0.0005) return null; // under 0.05%/day is noise
		return { perDay: slope, days: (p.disk_total_bytes - p.disk_used_bytes) / slope };
	});

	type Attn = { tone: 'warn' | 'fail'; text: string; tab?: string; label?: string };
	const attention = $derived.by<Attn[]>(() => {
		const out: Attn[] = [];
		if (!snap) return out;
		if (snap.docker.enabled && !snap.docker.ready) out.push({ tone: 'fail', text: `Docker is not usable: ${snap.docker.error ?? 'unknown error'}. Bots cannot start or build.` });
		if (snap.docker.problem) out.push({ tone: 'warn', text: snap.docker.problem });
		if (diskPct >= 90) out.push({ tone: 'fail', text: `The data disk is ${diskPct}% full. Builds, deployments and backups will fail.` });
		else if (diskPct >= 80) out.push({ tone: 'warn', text: `The data disk is ${diskPct}% full.` });
		if (diskFull && diskFull.days < 30) out.push({ tone: 'warn', text: `At the current rate the data disk fills in about ${Math.max(1, Math.round(diskFull.days))} day${Math.round(diskFull.days) === 1 ? '' : 's'} (${fmtBytes(diskFull.perDay)} per day).` });
		if (memPct >= 92) out.push({ tone: 'fail', text: `Host memory is ${memPct}% used. The kernel may stop processes.` });
		else if (snap.memory.swap_total > 0 && snap.memory.swap_used > snap.memory.swap_total * 0.5) out.push({ tone: 'warn', text: `Swap is ${pct(snap.memory.swap_used, snap.memory.swap_total)}% used, so the host is short on memory and slower than it looks.` });
		if (snap.host.load5 > snap.host.cores * 1.5) out.push({ tone: 'warn', text: `The 5-minute load average (${snap.host.load5.toFixed(1)}) is well above the ${snap.host.cores} CPU core${snap.host.cores === 1 ? '' : 's'}: work is waiting for a CPU.` });
		if (snap.panel.file_limit > 0 && snap.panel.open_files > snap.panel.file_limit * 0.8) out.push({ tone: 'warn', text: `The panel has ${snap.panel.open_files} of ${snap.panel.file_limit} open files allowed.` });
		if (snap.logs.errors > 0 && Date.now() - snap.logs.last_error_ms < 3600_000) out.push({ tone: 'warn', text: `${snap.logs.errors} error line${snap.logs.errors === 1 ? '' : 's'} in the panel log since it started; the latest ${fmtAgo(snap.logs.last_error_ms)}.`, tab: 'logs', label: 'Open the log' });
		for (const l of snap.storage) if (l.inodes_total > 0 && l.inodes_free / l.inodes_total < 0.05) { out.push({ tone: 'warn', text: `${l.label}: under 5% of inodes are free. Many small files (node_modules, caches) are using them up.` }); break; }
		return out;
	});

	// Storage rows share a bar per filesystem (device), not per directory.
	const devices = $derived.by(() => {
		const seen = new Map<number, NonNullable<typeof snap>['storage'][number]>();
		for (const l of snap?.storage ?? []) if (!seen.has(l.device)) seen.set(l.device, l);
		return [...seen.values()];
	});
	const shares = (device: number) => (snap?.storage ?? []).filter((l) => l.device === device).map((l) => l.label);

	const pctFmt = (n: number) => `${Math.round(n)}%`;
	const mem = $derived(snap?.memory);
	const dk = $derived(snap?.docker);
</script>

{#if error}<Notice tone="fail" class="mb-4">{error}</Notice>{/if}

{#if attention.length}
	<ul class="mb-5 grid gap-2" aria-label="Needs attention">
		{#each attention as a (a.text)}
			<li>
				<Notice tone={a.tone}>
					{a.text}
					{#snippet action()}{#if a.tab}<button class="btn btn-sm" onclick={() => goto(a.tab!)}>{a.label}</button>{/if}{/snippet}
				</Notice>
			</li>
		{/each}
	</ul>
{/if}

<!-- Right now -->
<section class="card p-4 sm:p-5" aria-label="Current host usage">
	<div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
		<h3 class="text-title font-semibold">{snap?.host.hostname || 'This host'}{#if snap?.host.os}<span class="ml-2 text-small font-normal text-muted">{snap.host.os}</span>{/if}</h3>
		{#if last}<p class="text-small text-muted">Sampled {fmtAgo(last.sampled_at_ms)}</p>{/if}
	</div>
	{#if last}
		<dl class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
			<div class="stat">
				<dt class="eyebrow">CPU · {last.logical_cpus} cores</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{last.cpu_percent.toFixed(0)}%<span class="ml-2 text-small font-normal text-muted">load {last.load1.toFixed(2)}</span></dd>
				<dd class="mt-2"><Sparkline fluid height={28} values={pts.map((p) => p.cpu_percent)} label="CPU history" /></dd>
			</div>
			<div class="stat">
				<dt class="eyebrow">Memory</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{memPct}%<span class="ml-2 text-small font-normal text-muted">{fmtBytes(last.memory_used_bytes)} of {fmtBytes(last.memory_total_bytes)}</span></dd>
				<dd class="mt-2"><Sparkline fluid height={28} values={pts.map((p) => pct(p.memory_used_bytes, p.memory_total_bytes))} label="Memory history" /></dd>
			</div>
			<div class="stat">
				<dt class="eyebrow">Disk</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{diskPct}%<span class="ml-2 text-small font-normal text-muted">{fmtBytes(last.disk_used_bytes)} of {fmtBytes(last.disk_total_bytes)}</span></dd>
				<dd class="mt-3.5 h-1.5 overflow-hidden rounded-pill bg-rule-soft" role="meter" aria-label="Disk used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={diskPct}>
					<div class="h-full rounded-pill {diskPct >= 90 ? 'bg-fail' : diskPct >= 75 ? 'bg-warn' : 'bg-action'}" style="width: {diskPct}%"></div>
				</dd>
				<dd class="mt-1 text-small text-muted">{fmtBytes(last.disk_total_bytes - last.disk_used_bytes)} free</dd>
			</div>
			<div class="stat">
				<dt class="eyebrow">Network</dt>
				<dd class="mt-1 font-mono text-body font-semibold">↓ {fmtBps(last.net_rx_bps)}</dd>
				<dd class="font-mono text-body font-semibold">↑ {fmtBps(last.net_tx_bps)}</dd>
				<dd class="mt-1 text-small text-muted">physical interfaces</dd>
			</div>
			<div class="stat">
				<dt class="eyebrow">Disk I/O</dt>
				<dd class="mt-1 font-mono text-body font-semibold">R {fmtBps(last.disk_read_bps)}</dd>
				<dd class="font-mono text-body font-semibold">W {fmtBps(last.disk_write_bps)}</dd>
				<dd class="mt-1 text-small text-muted">block devices</dd>
			</div>
			<div class="stat">
				<dt class="eyebrow">Running bots</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{last.running_bots}</dd>
				<dd class="mt-2 text-small"><button class="link" onclick={() => goto('bots')}>Usage per bot</button></dd>
			</div>
		</dl>
	{:else}
		<p class="mt-2 text-muted">Collecting the first samples. Metrics appear after about a minute.</p>
	{/if}
</section>

<!-- History -->
<section class="mt-8" aria-labelledby="hist-h">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<h3 id="hist-h" class="text-title font-semibold">History</h3>
			<p class="text-small text-muted">{hist ? `Averages over ${fmtBucket(hist.bucket_ms)} buckets` : 'Averages per time bucket'}; dashed lines show the highest reading in each bucket.</p>
		</div>
		<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Time range">
			{#each ranges as r (r.id)}
				<button class="rounded-inner px-2.5 py-1 text-small font-medium {range === r.id ? 'bg-action text-action-ink' : 'text-muted hover:text-ink'}" aria-pressed={range === r.id} onclick={() => (range = r.id)}>{r.label}</button>
			{/each}
		</div>
	</div>
	<div class="mt-3 grid gap-3 lg:grid-cols-2 {loading && !hist ? 'opacity-60' : ''}">
		<div class="stat"><p class="eyebrow mb-2">CPU</p>
			<TimeChart label="CPU use" {times} max={100} format={pctFmt} series={[{ label: 'Average', values: pts.map((p) => p.cpu_percent), area: true }, { label: 'Peak', values: pts.map((p) => p.cpu_max), dashed: true, color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Memory</p>
			<TimeChart label="Memory use" {times} max={last?.memory_total_bytes} format={fmtBytes} series={[{ label: 'Used', values: pts.map((p) => p.memory_used_bytes), area: true, color: 'run' }, { label: 'Peak', values: pts.map((p) => p.memory_max), dashed: true, color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Load average (1 min) · {cores} core{cores === 1 ? '' : 's'}</p>
			<TimeChart label="Load average" {times} format={(n) => n.toFixed(n < 10 ? 2 : 1)} series={[{ label: 'Load', values: pts.map((p) => p.load1), area: true, color: 'warn' }]} />
			<p class="mt-1 text-small text-muted">A load above {cores} means work is waiting for a CPU.</p>
		</div>
		<div class="stat"><p class="eyebrow mb-2">Network throughput</p>
			<TimeChart label="Network throughput" {times} format={fmtBps} series={[{ label: 'Received', values: pts.map((p) => p.net_rx_bps), color: 'run' }, { label: 'Sent', values: pts.map((p) => p.net_tx_bps), color: 'action' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Disk throughput</p>
			<TimeChart label="Disk throughput" {times} format={fmtBps} series={[{ label: 'Read', values: pts.map((p) => p.disk_read_bps), color: 'run' }, { label: 'Write', values: pts.map((p) => p.disk_write_bps), color: 'action' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Disk space used</p>
			<TimeChart label="Disk space used" {times} max={last?.disk_total_bytes} format={fmtBytes} series={[{ label: 'Used', values: pts.map((p) => p.disk_used_bytes), area: true, color: 'warn' }]} />
			{#if diskFull}<p class="mt-1 text-small text-muted">Growing about {fmtBytes(diskFull.perDay)} per day.</p>{/if}
		</div>
	</div>
	{#if pts.length && pts.some((p) => p.swap_total_bytes > 0)}
		<div class="stat mt-3"><p class="eyebrow mb-2">Swap used</p>
			<TimeChart label="Swap used" {times} max={Math.max(...pts.map((p) => p.swap_total_bytes))} format={fmtBytes} height={110} series={[{ label: 'Swap', values: pts.map((p) => p.swap_used_bytes), area: true, color: 'fail' }]} />
		</div>
	{/if}
</section>

<!-- Machine, process, runtime -->
<section class="mt-8 grid gap-3 lg:grid-cols-3" aria-label="Details">
	<div class="card p-4 sm:p-5">
		<h3 class="flex items-center gap-2 text-title font-semibold"><Icon name="monitor" class="text-muted" />System</h3>
		{#if snap}
			<dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-small [&>dd]:min-w-0 [&>dd]:text-right [&>dd]:break-words [&>dt]:text-muted">
				<dt>Host name</dt><dd class="font-mono">{snap.host.hostname || '—'}</dd>
				<dt>Operating system</dt><dd>{snap.host.os || '—'}</dd>
				<dt>Kernel</dt><dd class="font-mono">{snap.host.kernel || '—'} · {snap.host.arch}</dd>
				<dt>Processor</dt><dd>{snap.host.cpu_model || '—'}</dd>
				<dt>Cores</dt><dd class="font-mono">{snap.host.cores}</dd>
				<dt>Up for</dt><dd>{fmtUptime(snap.host.uptime_sec)}</dd>
				<dt>Load (1/5/15 min)</dt><dd class="font-mono">{snap.host.load1.toFixed(2)} / {snap.host.load5.toFixed(2)} / {snap.host.load15.toFixed(2)}</dd>
			</dl>
			<h4 class="eyebrow mt-4">Memory breakdown</h4>
			{#if mem}
				<div class="mt-2 flex h-2 overflow-hidden rounded-pill bg-rule-soft" role="img" aria-label="Memory: {fmtBytes(mem.used - mem.buffers)} in use by programs, {fmtBytes(mem.buffers + mem.cached)} cache, {fmtBytes(mem.available)} available">
					<div class="bg-action" style="width: {pct(Math.max(mem.total - mem.available - 0, 0), mem.total)}%"></div>
					<div class="bg-run/50" style="width: {pct(mem.buffers + mem.cached, mem.total)}%"></div>
				</div>
				<dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-small [&>dd]:text-right [&>dt]:text-muted">
					<dt>Total</dt><dd class="font-mono">{fmtBytes(mem.total)}</dd>
					<dt>Used (total − available)</dt><dd class="font-mono">{fmtBytes(mem.used)}</dd>
					<dt>Cache and buffers</dt><dd class="font-mono">{fmtBytes(mem.buffers + mem.cached)}</dd>
					<dt>Free</dt><dd class="font-mono">{fmtBytes(mem.free)}</dd>
					<dt>Swap</dt><dd class="font-mono">{mem.swap_total ? `${fmtBytes(mem.swap_used)} of ${fmtBytes(mem.swap_total)}` : 'none'}</dd>
				</dl>
			{/if}
		{:else}<p class="mt-3 text-small text-muted">Loading…</p>{/if}
	</div>

	<div class="card p-4 sm:p-5">
		<h3 class="flex items-center gap-2 text-title font-semibold"><Icon name="panel" class="text-muted" />RivetPanel process</h3>
		{#if snap}
			{@const p = snap.panel}
			<dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-small [&>dd]:text-right [&>dt]:text-muted">
				<dt>Version</dt><dd class="font-mono">{p.version}</dd>
				<dt>Running for</dt><dd>{fmtUptime(p.uptime_ms / 1000)}</dd>
				<dt>Process memory (RSS)</dt><dd class="font-mono">{fmtBytes(p.rss_bytes)}</dd>
				<dt>Go heap in use</dt><dd class="font-mono">{fmtBytes(p.heap_bytes)}</dd>
				<dt>Goroutines · threads</dt><dd class="font-mono">{p.goroutines} · {p.threads}</dd>
				<dt>Open files</dt><dd class="font-mono">{p.open_files}{p.file_limit ? ` of ${p.file_limit}` : ''}</dd>
				<dt>Garbage collections</dt><dd class="font-mono">{p.gc_count} · {p.gc_pause_ms.toFixed(1)} ms paused</dd>
				<dt>Database</dt><dd class="font-mono">{fmtBytes(p.db_bytes)}</dd>
				<dt>Process ID</dt><dd class="font-mono">{p.pid}</dd>
				<dt>Go</dt><dd class="font-mono">{p.go_version}</dd>
			</dl>
			<h4 class="eyebrow mt-4">Requests since start</h4>
			<dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-small [&>dd]:text-right [&>dt]:text-muted">
				<dt>Handled</dt><dd class="font-mono">{p.http.requests.toLocaleString()}</dd>
				<dt>Server errors</dt><dd class="font-mono {p.http.errors ? 'text-fail' : ''}">{p.http.errors.toLocaleString()}</dd>
				<dt>In flight now</dt><dd class="font-mono">{p.http.in_flight}</dd>
				<dt>Average time</dt><dd class="font-mono">{p.http.avg_ms.toFixed(1)} ms</dd>
			</dl>
		{:else}<p class="mt-3 text-small text-muted">Loading…</p>{/if}
	</div>

	<div class="card p-4 sm:p-5">
		<h3 class="flex items-center gap-2 text-title font-semibold"><Icon name="box" class="text-muted" />Docker runner
			{#if dk?.enabled}<span class="pill ml-auto" data-tone={dk.ready ? 'run' : 'fail'}>{dk.ready ? 'Ready' : 'Unavailable'}</span>{/if}
		</h3>
		{#if dk && !dk.enabled}
			<p class="mt-3 text-small text-muted">The local runner is turned off (<code>RIVET_RUNNER_MODE=none</code>). Bots cannot start on this panel.</p>
		{:else if dk}
			{#if dk.error}<p class="mt-2 text-small text-fail">{dk.error}</p>{/if}
			<dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-small [&>dd]:text-right [&>dt]:text-muted">
				<dt>Docker Engine</dt><dd class="font-mono">{dk.version || '—'}</dd>
				<dt>cgroups</dt><dd>{dk.cgroup_v2 ? 'v2' : 'v1'}{dk.rootless ? ' · rootless' : ''}</dd>
				<dt>Swap limits</dt><dd>{dk.swap_limit ? 'enforced' : 'not enforced'}</dd>
				<dt>Bot containers</dt><dd class="font-mono">{dk.containers_running} running · {dk.containers_stopped} stopped</dd>
				<dt>Builds running</dt><dd class="font-mono">{dk.builders}</dd>
				<dt>AI diagnostics running</dt><dd class="font-mono">{dk.diagnostics}</dd>
				<dt>Workers</dt><dd class="font-mono">{dk.workers}</dd>
				<dt>Waiting for a worker</dt><dd class="font-mono {dk.queued ? 'text-warn' : ''}">{dk.queued}</dd>
				<dt>Bots tracked in memory</dt><dd class="font-mono">{dk.tracked}</dd>
				<dt>Containers run as</dt><dd class="font-mono">{dk.user || '—'}</dd>
			</dl>
		{:else}<p class="mt-3 text-small text-muted">Loading…</p>{/if}
	</div>
</section>

<!-- Storage -->
<section class="mt-8" aria-labelledby="stor-h">
	<h3 id="stor-h" class="text-title font-semibold">Storage</h3>
	<p class="mt-1 max-w-3xl text-small text-muted">What the panel keeps on disk. Directory sizes are counted in the background about every five minutes, so a large tree never slows this page.</p>
	{#if snap}
		<div class="card mt-3 overflow-x-auto">
			<table class="w-full min-w-[40rem] text-left">
				<thead class="border-b border-rule-soft"><tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal"><th class="eyebrow">Data</th><th class="eyebrow">Path</th><th class="eyebrow text-right">Size</th><th class="eyebrow text-right">Counted</th></tr></thead>
				<tbody class="divide-y divide-rule-soft text-small">
					{#each snap.storage as l (l.key)}
						<tr class="[&>td]:px-4 [&>td]:py-2.5">
							<td><span class="font-medium">{l.label}</span>{#if l.note}<span class="block text-[12px] text-muted">{l.note}</span>{/if}</td>
							<td class="max-w-72 font-mono text-[12px] break-all text-muted">{l.path}</td>
							<td class="text-right font-mono">{l.bytes < 0 ? 'counting…' : (l.partial ? '≥ ' : '') + fmtBytes(l.bytes)}</td>
							<td class="text-right text-muted">{l.counted_at_ms ? fmtAgo(l.counted_at_ms) : '—'}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<ul class="mt-3 grid gap-3 md:grid-cols-2">
			{#each devices as d (d.device)}
				{@const used = d.fs_total - d.fs_free}
				{@const p = pct(used, d.fs_total)}
				<li class="stat">
					<div class="flex items-baseline justify-between gap-2"><span class="eyebrow">Filesystem · {shares(d.device).join(', ')}</span><span class="font-mono text-small"><span class="font-medium text-ink">{fmtBytes(used)}</span><span class="text-muted"> / {fmtBytes(d.fs_total)}</span></span></div>
					<div class="mt-1.5 h-1.5 overflow-hidden rounded-pill bg-rule-soft" role="meter" aria-label="Filesystem used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={p}><div class="h-full rounded-pill {tone(p) === 'fail' ? 'bg-fail' : tone(p) === 'warn' ? 'bg-warn' : 'bg-action'}" style="width: {p}%"></div></div>
					<p class="mt-1.5 text-small text-muted">{fmtBytes(d.fs_free)} free · {d.inodes_total ? `${pct(d.inodes_total - d.inodes_free, d.inodes_total)}% of inodes used` : 'inodes not tracked'}</p>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<script lang="ts" module>
	function fmtBucket(ms: number): string {
		if (ms >= 3600_000) return `${+(ms / 3600_000).toFixed(1)} hour`;
		if (ms >= 60_000) return `${Math.round(ms / 60_000)} minute`;
		return `${Math.round(ms / 1000)} second`;
	}
</script>
