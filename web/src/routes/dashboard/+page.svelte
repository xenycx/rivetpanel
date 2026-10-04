<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Bot, Capacity, RuntimeInfo, Sample } from '$lib/api/types';
	import { can as canDo, session } from '$lib/session.svelte';
	import { describe } from '$lib/status';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import NewMenu from '$lib/components/NewMenu.svelte';
	import ResourceTable from '$lib/components/ResourceTable.svelte';
	import { currentWorkspace, workspaceName, workspaces } from '$lib/workspaces.svelte';
	import type { Blueprint, GameStatus } from '$lib/api/games';

	// The overview: four numbers about capacity, then the game servers and
	// bots that need a look first. Full lists with filters live on their pages.
	let all = $state<Bot[] | null>(null);
	let blueprints = $state<Record<string, Blueprint>>({});
	let runtimes = $state<RuntimeInfo[]>([]);
	let players = $state<Record<string, GameStatus>>({});
	let cap = $state<Capacity | null>(null);
	// Administrators see the host's real usage.
	let host = $state<{ cpu: number; cpus: number; mem: number; memTotal: number; disk: number; diskTotal: number } | null>(null);
	let error = $state('');
	let now = $state(Date.now());
	const SHOWN = 6;

	async function refresh() {
		try {
			all = (await api<{ bots: Bot[] }>('GET', '/bots')).bots;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The list could not be loaded. Check your connection.';
		}
	}
	async function loadPlayers() {
		for (const s of servers.slice(0, SHOWN)) {
			if (s.observed_state !== 'running' || document.visibilityState !== 'visible') continue;
			try {
				players[s.id] = await api<GameStatus>('GET', `/bots/${s.id}/game/query`);
			} catch {
				/* best effort */
			}
		}
	}
	function loadHost() {
		api<{ nodes: { id: string }[] }>('GET', '/nodes')
			.then((r) => (r.nodes[0] ? api<{ samples: Sample[] }>('GET', `/nodes/${r.nodes[0].id}/telemetry?limit=1`) : null))
			.then((r) => {
				const x = r?.samples.at(-1);
				if (x) host = { cpu: x.cpu_percent, cpus: x.logical_cpus, mem: x.memory_used_bytes, memTotal: x.memory_total_bytes, disk: x.disk_used_bytes, diskTotal: x.disk_total_bytes };
			})
			.catch(() => {});
	}

	onMount(() => {
		refresh().then(loadPlayers);
		api<{ runtimes: RuntimeInfo[] }>('GET', '/runtimes').then((r) => (runtimes = r.runtimes)).catch(() => {});
		api<Capacity>('GET', '/me/capacity').then((r) => (cap = r)).catch(() => {});
		if (session.features.games)
			api<{ blueprints: Blueprint[] }>('GET', '/blueprints')
				.then((r) => (blueprints = Object.fromEntries(r.blueprints.map((b) => [b.id, b]))))
				.catch(() => {});
		if (isAdmin) loadHost();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') refresh();
		}, 3000);
		const slow = setInterval(() => {
			loadPlayers();
			if (isAdmin) loadHost();
		}, 15000);
		return () => {
			clearInterval(t);
			clearInterval(slow);
		};
	});

	const isAdmin = $derived(session.user?.role === 'admin');
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	const scoped = $derived((all ?? []).filter((b) => workspaces.selected === 'all' || b.workspace_id === workspaces.selected));
	const bots = $derived(scoped.filter((b) => b.kind !== 'game'));
	const servers = $derived(scoped.filter((b) => b.kind === 'game'));

	// What needs a look comes first: failures, then work in progress, then running.
	const rank = (b: Bot) => {
		const d = describe(b, now);
		if (d.tone === 'fail' || b.phase === 'retrying') return 0;
		if (d.busy) return 1;
		if (b.phase === 'running') return 2;
		return 3;
	};
	const order = (list: Bot[]) => [...list].sort((a, b) => Number(b.favorite) - Number(a.favorite) || rank(a) - rank(b) || a.name.localeCompare(b.name));
	const attention = $derived(scoped.filter((b) => rank(b) === 0).length);
	const running = (list: Bot[]) => list.filter((b) => b.phase === 'running').length;
	const note = (b: Bot) => (b.shared ? 'shared with you' : multi ? workspaceName(b.workspace_id) : '');

	type Kpi = { label: string; value: string; of?: string; pct?: number; hint?: string };
	const pct = (a: number, b: number) => (b > 0 ? Math.min(100, (a / b) * 100) : undefined);
	const kpis = $derived.by<Kpi[]>(() => {
		const list = all ?? [];
		const up = running(list);
		const runningKpi: Kpi = { label: 'Running', value: String(up), of: `of ${list.length}`, pct: pct(up, list.length) };
		if (host)
			return [
				{ label: 'Memory', value: fmtBytes(host.mem), of: `of ${fmtBytes(host.memTotal)}`, pct: pct(host.mem, host.memTotal) },
				{ label: 'CPU', value: `${host.cpu.toFixed(1)}%`, of: `${host.cpus} cores`, pct: Math.min(100, host.cpu) },
				{ label: 'Disk', value: fmtBytes(host.disk), of: `of ${fmtBytes(host.diskTotal)}`, pct: pct(host.disk, host.diskTotal) },
				runningKpi
			];
		// Without host access: what this account reserves against its limits.
		const reservedMem = list.filter((b) => b.desired_state === 'running').reduce((n, b) => n + b.memory_bytes, 0);
		const reservedCpu = list.filter((b) => b.desired_state === 'running').reduce((n, b) => n + b.nano_cpus, 0) / 1e9;
		const limited = !!cap && !cap.exempt;
		const memMax = limited && cap!.max_memory_bytes ? cap!.max_memory_bytes : 0;
		const botMax = limited && cap!.max_bots ? cap!.max_bots : 0;
		return [
			{ label: 'Memory reserved', value: fmtBytes(limited && memMax ? cap!.memory_bytes : reservedMem), of: memMax ? `of ${fmtBytes(memMax)}` : 'no limit', pct: memMax ? pct(cap!.memory_bytes, memMax) : undefined },
			{ label: 'CPU reserved', value: `${reservedCpu.toFixed(2).replace(/\.?0+$/, '')}`, of: reservedCpu === 1 ? 'core' : 'cores' },
			{ label: 'Bots and servers', value: String(list.length), of: botMax ? `of ${botMax}` : 'no limit', pct: botMax ? pct(list.length, botMax) : undefined },
			runningKpi
		];
	});
	const level = (p?: number) => (p === undefined ? 'ok' : p >= 90 ? 'high' : p >= 75 ? 'warn' : 'ok');
</script>

<svelte:head><title>Overview · RivetPanel</title></svelte:head>

<header class="page-head">
	<h1 class="font-semibold">Overview</h1>
	{#if scope}<p class="text-small text-muted">{scope.personal ? 'Personal workspace' : scope.name}</p>{/if}
	<div class="ml-auto"><NewMenu primary /></div>
</header>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

<section class="mt-4 grid grid-cols-2 gap-3 lg:grid-cols-4" aria-label="Capacity">
	{#each kpis as k (k.label)}
		<div class="kpi">
			<p class="eyebrow flex items-center justify-between gap-2">{k.label}{#if k.label === 'Memory' && isAdmin}<a href="/admin/host" class="font-normal hover:text-ink">Host details</a>{/if}</p>
			<p class="kpi-value">{all || host ? k.value : '–'}{#if k.of}<small>{k.of}</small>{/if}</p>
			<div class="meter mt-2" data-level={level(k.pct)} data-open={k.pct === undefined} role={k.pct === undefined ? undefined : 'meter'} aria-label={k.pct === undefined ? undefined : `${k.label} used`} aria-valuemin={k.pct === undefined ? undefined : 0} aria-valuemax={k.pct === undefined ? undefined : 100} aria-valuenow={k.pct === undefined ? undefined : Math.round(k.pct)}>
				<i style="width: {k.pct ?? 0}%"></i>
			</div>
		</div>
	{/each}
</section>

{#if attention}
	<p class="mt-3 flex items-center gap-2 text-small" role="status">
		<span class="side-dot !m-0" data-tone="fail"></span>
		<span><span class="font-medium">{attention}</span> {attention === 1 ? 'needs' : 'need'} attention.</span>
		<a class="link" href="/bots?state=attention">Show bots</a>{#if session.features.games}<a class="link" href="/servers">Show servers</a>{/if}
	</p>
{/if}

{#if all === null}
	{#if !error}<div class="mt-6"><Skeleton rows={4} label="Loading" /></div>{/if}
{:else}
	{#if session.features.games}
		<section class="mt-7" aria-labelledby="games-h">
			<div class="mb-2 flex items-baseline gap-3">
				<h2 id="games-h" class="text-section font-semibold">Game servers</h2>
				{#if servers.length}<p class="text-small text-muted">{running(servers)} of {servers.length} running</p>{/if}
				{#if servers.length > SHOWN}<a href="/servers" class="ml-auto text-small text-muted hover:text-ink">View all {servers.length}</a>{:else if servers.length}<a href="/servers" class="ml-auto text-small text-muted hover:text-ink">Open list</a>{/if}
			</div>
			<ResourceTable kind="game" label="Game servers" items={order(servers).slice(0, SHOWN)} {now} {blueprints} {players} {note} onChanged={refresh}>
				{#snippet empty()}
					<span>No game servers{scope ? ' in this workspace' : ''} yet.</span>
					{#if canDo('bots.create')}<a class="link" href="/servers/new">Create a game server</a>{/if}
				{/snippet}
			</ResourceTable>
		</section>
	{/if}

	<section class="mt-7" aria-labelledby="bots-h">
		<div class="mb-2 flex items-baseline gap-3">
			<h2 id="bots-h" class="text-section font-semibold">Discord bots</h2>
			{#if bots.length}<p class="text-small text-muted">{running(bots)} of {bots.length} running</p>{/if}
			{#if bots.length > SHOWN}<a href="/bots" class="ml-auto text-small text-muted hover:text-ink">View all {bots.length}</a>{:else if bots.length}<a href="/bots" class="ml-auto text-small text-muted hover:text-ink">Open list</a>{/if}
		</div>
		<ResourceTable kind="bot" label="Discord bots" items={order(bots).slice(0, SHOWN)} {now} {runtimes} {note} onChanged={refresh}>
			{#snippet empty()}
				<span>No bots{scope ? ' in this workspace' : ''} yet.</span>
				{#if canDo('bots.create')}<a class="link" href="/bots/new?source=template">Start from a template</a><a class="link" href="/bots/new?source=github">Deploy from GitHub</a>{/if}
			{/snippet}
		</ResourceTable>
	</section>
{/if}

{#if !all?.length && all !== null}
	<p class="mt-6 flex items-center gap-2 text-small text-muted"><Icon name="book" size={14} />New here? The <a class="link" href="/docs">documentation</a> walks through a first bot and a first server.</p>
{/if}
