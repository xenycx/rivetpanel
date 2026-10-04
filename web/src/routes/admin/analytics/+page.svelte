<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { fmtBps } from '$lib/api/admin';
	import { fmtCores, fmtCount, fmtDuration, fmtPct, fmtResolution, overviewRanges, type Overview, type OverviewConsumer } from '$lib/api/usage';
	import { poll } from '$lib/poll';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import TimeChart from '$lib/components/TimeChart.svelte';

	// Panel-wide usage trends (analytics.view). The server decides what this
	// account may see: delegated viewers get only accounts they could manage.
	let range = $state<string>('7d');
	let data = $state<Overview | null>(null);
	let error = $state('');

	async function load() {
		const asked = range;
		try {
			const d = await api<Overview>('GET', `/admin/analytics?range=${asked}`);
			if (asked === range) data = d;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Analytics are unavailable right now.';
		}
	}
	onMount(() => poll(load, 120_000));
	let shown = '7d';
	$effect(() => {
		if (range === shown) return;
		shown = range;
		data = null;
		void load();
	});

	const pts = $derived(data?.points ?? []);
	const times = $derived(pts.map((p) => p.t));
	const tot = $derived(data?.totals);
	const per = $derived(data ? fmtResolution(data.resolution_s) : 'bucket');
	const href = (c: OverviewConsumer) => `${c.kind === 'game' ? '/servers' : '/bots'}/${c.bot_id}?tab=usage`;
	const tables = $derived(
		data
			? [
					{ id: 'cpu', title: 'Most CPU', rows: data.top_cpu, value: (c: OverviewConsumer) => fmtCores(c.cpu) },
					{ id: 'mem', title: 'Most memory', rows: data.top_mem, value: (c: OverviewConsumer) => fmtBytes(c.mem) },
					{ id: 'net', title: 'Most network traffic', rows: data.top_net, value: (c: OverviewConsumer) => fmtBytes(c.net_bytes) }
				]
			: []
	);
	const csv = $derived(`/api/v1/admin/analytics?range=${range}&format=csv`);
	const pctOf = (a: number, b: number) => (b > 0 ? Math.round((a / b) * 100) : 0);
</script>

<svelte:head><title>Analytics · Administration</title></svelte:head>

<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section font-semibold">Analytics</h2>
		<p class="text-small text-muted">Usage across the panel{#if data}, per {per}{/if}: accounts, bots and servers, resources, deployments and backups.</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Time range">
			{#each overviewRanges as r (r.id)}
				<button class="rounded-inner px-2.5 py-1 text-small font-medium {range === r.id ? 'bg-paper-2 text-ink shadow-[inset_0_0_0_1px_var(--color-rule)]' : 'text-muted hover:text-ink'}" aria-pressed={range === r.id} onclick={() => (range = r.id)}>{r.label}</button>
			{/each}
		</div>
		<a class="btn btn-sm" href={csv} download>Export CSV</a>
	</div>
</div>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if data?.scope === 'delegated'}
	<Notice class="mt-4">Figures cover the accounts your role can manage and their bots and servers; administrators and accounts with administration permissions you lack are left out.</Notice>
{/if}

{#if data && tot}
	<dl class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
		<div class="stat">
			<dt class="eyebrow">Accounts</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(data.users_total)}</dd>
			<dd class="text-small text-muted">{fmtCount(data.users_new)} new in this range</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Bots</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(data.bots.bot?.total ?? 0)}</dd>
			<dd class="text-small text-muted">{fmtCount(data.bots.bot?.running ?? 0)} running now</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Game servers</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(data.bots.game?.total ?? 0)}</dd>
			<dd class="text-small text-muted">{fmtCount(data.bots.game?.running ?? 0)} running now</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Uptime</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.uptime !== null ? fmtPct(tot.uptime) : '—'}</dd>
			<dd class="text-small text-muted"><span class={tot.crashes ? 'text-fail' : ''}>{fmtCount(tot.crashes)} crashes</span> · {fmtCount(tot.starts)} starts</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">CPU in use</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.cpu_avg !== null ? fmtCores(tot.cpu_avg) : '—'}</dd>
			<dd class="text-small text-muted">{tot.cpu_max !== null ? `busiest ${per}: ${fmtCores(tot.cpu_max)}` : 'not measured yet'}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Memory in use</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{tot.mem_avg !== null ? fmtBytes(tot.mem_avg) : '—'}</dd>
			<dd class="text-small text-muted">{tot.mem_max !== null ? `busiest ${per}: ${fmtBytes(tot.mem_max)}` : 'not measured yet'}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Deployments</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(tot.deploys_ok)} <span class="text-small font-normal text-muted">ok</span> · <span class={tot.deploys_failed ? 'text-fail' : ''}>{fmtCount(tot.deploys_failed)}</span> <span class="text-small font-normal text-muted">failed</span></dd>
			<dd class="text-small text-muted">{tot.deploy_avg_ms !== null ? `average ${fmtDuration(tot.deploy_avg_ms)}` : 'none in this range'}</dd>
		</div>
		<div class="stat">
			<dt class="eyebrow">Backups</dt>
			<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(tot.backups_ok)} <span class="text-small font-normal text-muted">ok</span> · <span class={tot.backups_failed ? 'text-fail' : ''}>{fmtCount(tot.backups_failed)}</span> <span class="text-small font-normal text-muted">failed</span></dd>
			<dd class="text-small text-muted">{tot.backup_bytes ? `${fmtBytes(tot.backup_bytes)} written` : 'none in this range'} · ↓ {fmtBytes(tot.net_rx)} ↑ {fmtBytes(tot.net_tx)} network</dd>
		</div>
		{#if data.tickets}
			<div class="stat">
				<dt class="eyebrow">Support tickets</dt>
				<dd class="mt-1 font-mono text-section font-semibold">{fmtCount(data.tickets.opened)} <span class="text-small font-normal text-muted">opened</span> · {fmtCount(data.tickets.closed)} <span class="text-small font-normal text-muted">closed</span></dd>
				<dd class="text-small text-muted">{fmtCount(data.tickets.open_now)} waiting now{#if data.tickets.median_response_ms !== null} · first reply in {fmtDuration(data.tickets.median_response_ms)} (median){/if}</dd>
			</div>
		{/if}
	</dl>

	<div class="mt-6 grid gap-3 lg:grid-cols-2">
		<div class="stat"><p class="eyebrow mb-2">CPU in use (cores, all bots)</p>
			<TimeChart label="CPU in use" {times} format={fmtCores} series={[{ label: 'CPU', values: pts.map((p) => p.cpu), area: true }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Memory in use (all bots)</p>
			<TimeChart label="Memory in use" {times} format={fmtBytes} series={[{ label: 'Memory', values: pts.map((p) => p.mem), area: true, color: 'run' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Network per {per}</p>
			<TimeChart label="Network traffic" {times} format={fmtBytes} series={[{ label: 'Received', values: pts.map((p) => p.net_rx), area: true }, { label: 'Sent', values: pts.map((p) => p.net_tx), color: 'muted' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Uptime</p>
			<TimeChart label="Uptime" {times} max={100} format={fmtPct} series={[{ label: 'Uptime', values: pts.map((p) => p.uptime), area: true, color: 'run' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Deployments per {per}</p>
			<TimeChart label="Deployments" {times} format={fmtCount} series={[{ label: 'Succeeded', values: pts.map((p) => p.deploys_ok), color: 'run' }, { label: 'Failed', values: pts.map((p) => p.deploys_failed), color: 'fail' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">Backups per {per}</p>
			<TimeChart label="Backups" {times} format={fmtCount} series={[{ label: 'Succeeded', values: pts.map((p) => p.backups_ok), color: 'run' }, { label: 'Failed', values: pts.map((p) => p.backups_failed), color: 'fail' }]} />
		</div>
		<div class="stat"><p class="eyebrow mb-2">{data.tickets ? 'New accounts, tickets and crashes' : 'New accounts and crashes'} per {per}</p>
			<TimeChart label="New accounts" {times} format={fmtCount} series={[{ label: 'New accounts', values: pts.map((p) => p.new_users), color: 'action' }, ...(data.tickets ? [{ label: 'Tickets opened', values: pts.map((p) => p.tickets ?? 0), color: 'muted' as const }] : []), { label: 'Crashes', values: pts.map((p) => p.crashes), color: 'fail' }]} />
		</div>
	</div>

	<section class="mt-8" aria-labelledby="top-h">
		<h3 id="top-h" class="text-title font-semibold">Top consumers</h3>
		<p class="text-small text-muted">Averages over the range while the bot or server was measured.{#if data.top_cpu.length && !data.top_cpu[0].owner} Owners are shown to accounts that can view accounts.{/if}</p>
		<div class="mt-3 grid gap-3 xl:grid-cols-3">
			{#each tables as t (t.id)}
				<div class="card p-4">
					<h4 class="eyebrow">{t.title}</h4>
					{#if t.rows.length}
						<ol class="mt-2 grid gap-1.5">
							{#each t.rows as c (c.bot_id)}
								<li class="flex items-baseline justify-between gap-3">
									<span class="min-w-0 truncate"><a class="link" href={href(c)}>{c.name}</a>{#if c.kind === 'game'}<span class="ml-1.5 text-small text-muted">server</span>{/if}{#if c.owner}<span class="block truncate text-small text-muted">{c.owner}</span>{/if}</span>
									<span class="shrink-0 font-mono text-small">{t.value(c)}</span>
								</li>
							{/each}
						</ol>
					{:else}
						<p class="mt-2 text-small text-muted">Nothing measured in this range.</p>
					{/if}
				</div>
			{/each}
		</div>
	</section>

	{#if data.nodes}
		<section class="mt-8" aria-labelledby="nodes-h">
			<h3 id="nodes-h" class="text-title font-semibold">Nodes and locations</h3>
			<p class="text-small text-muted">From node telemetry (this panel's host and connected agents), rolled up per hour and day.</p>
			<div class="mt-3 overflow-x-auto">
				<table class="w-full min-w-[46rem] text-left">
					<thead class="border-b border-rule-soft">
						<tr><th class="eyebrow">Node</th><th class="eyebrow">CPU (avg · peak)</th><th class="eyebrow">Memory</th><th class="eyebrow">Disk</th><th class="eyebrow">Network</th><th class="eyebrow">Running</th><th class="eyebrow w-40">CPU trend</th></tr>
					</thead>
					<tbody>
						{#each data.nodes as n (n.id)}
							<tr class="border-b border-rule-soft">
								<td class="py-2">{n.name}{#if n.location}<span class="block text-small text-muted">{n.location}</span>{/if}</td>
								{#if n.samples}
									<td class="font-mono text-small">{n.cpu_avg?.toFixed(1)}% · {n.cpu_max?.toFixed(0)}%</td>
									<td class="font-mono text-small">{fmtBytes(n.mem_avg ?? 0)} of {fmtBytes(n.mem_total)} <span class="text-muted">({pctOf(n.mem_avg ?? 0, n.mem_total)}%)</span></td>
									<td class="font-mono text-small">{fmtBytes(n.disk_used)} of {fmtBytes(n.disk_total)}</td>
									<td class="font-mono text-small">↓ {fmtBps(n.net_rx_bps ?? 0)}<br />↑ {fmtBps(n.net_tx_bps ?? 0)}</td>
									<td class="font-mono text-small">{n.running_max}</td>
									<td><Sparkline fluid height={24} values={n.cpu_series.map((v) => v ?? 0)} label="CPU trend of {n.name}" /></td>
								{:else}
									<td colspan="6" class="text-small text-muted">No telemetry in this range.</td>
								{/if}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			{#if data.locations && data.locations.length > 1}
				<div class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
					{#each data.locations as l (l.name)}
						<div class="stat">
							<p class="eyebrow">{l.name || 'No location'} · {l.nodes} node{l.nodes === 1 ? '' : 's'}</p>
							<p class="mt-1 font-mono text-small">CPU {l.cpu_avg !== null ? `${l.cpu_avg.toFixed(1)}%` : '—'} · memory {fmtBytes(l.mem_avg)} of {fmtBytes(l.mem_total)} · disk {fmtBytes(l.disk_used)} of {fmtBytes(l.disk_total)}</p>
						</div>
					{/each}
				</div>
			{/if}
		</section>
	{/if}
{:else if !error}
	<p class="mt-4 text-muted">Loading…</p>
{/if}
