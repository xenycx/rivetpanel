<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import type { Blueprint, GameStatus } from '$lib/api/games';
	import { joinAddress, resourceHref } from '$lib/api/games';
	import { power } from '$lib/api/bots';
	import { session } from '$lib/session.svelte';
	import { isStopped } from '$lib/status';
	import { toast } from '$lib/ui/toast.svelte';
	import { currentWorkspace, loadWorkspaces, workspaceName, workspaces } from '$lib/workspaces.svelte';
	import StatusBadge from '$lib/components/ui/StatusBadge.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let servers = $state<Bot[] | null>(null);
	let blueprints = $state<Record<string, Blueprint>>({});
	let status = $state<Record<string, GameStatus>>({});
	let error = $state('');
	let q = $state('');
	let now = $state(Date.now());
	let busy = $state<Record<string, boolean>>({});
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'Connection to the panel was lost. Retrying…');

	async function load() {
		try {
			const r = await api<{ bots: Bot[] }>('GET', '/bots');
			servers = r.bots.filter((b) => b.kind === 'game');
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	async function loadBlueprints() {
		try {
			const r = await api<{ blueprints: Blueprint[] }>('GET', '/blueprints');
			blueprints = Object.fromEntries(r.blueprints.map((b) => [b.id, b]));
		} catch {
			/* names fall back to the runtime */
		}
	}
	// Player counts for running servers; a server that does not answer is shown as starting.
	async function loadStatus() {
		for (const s of servers ?? []) {
			if (s.observed_state !== 'running' || document.visibilityState !== 'visible') continue;
			try {
				status[s.id] = await api<GameStatus>('GET', `/bots/${s.id}/game/query`);
			} catch {
				/* best effort */
			}
		}
	}
	onMount(() => {
		load().then(loadStatus);
		loadBlueprints();
		loadWorkspaces();
		const poll = setInterval(() => document.visibilityState === 'visible' && load(), 4000);
		const players = setInterval(loadStatus, 15000);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(players);
			clearInterval(tick);
		};
	});

	const scoped = $derived((servers ?? []).filter((s) => workspaces.selected === 'all' || s.workspace_id === workspaces.selected));
	const shown = $derived(
		scoped.filter((s) => !q.trim() || (s.name + ' ' + (blueprints[s.blueprint_id ?? '']?.name ?? '')).toLowerCase().includes(q.trim().toLowerCase()))
	);
	const scope = $derived(currentWorkspace());
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');

	async function act(s: Bot, a: 'start' | 'stop') {
		busy[s.id] = true;
		if (await power(s, a)) await load();
		busy[s.id] = false;
	}
	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast('Address copied', 'success');
		} catch {
			toast(text, 'info');
		}
	}
</script>

<svelte:head><title>Game servers · RivetPanel</title></svelte:head>

<section class="card card-glow grid gap-6 p-6 sm:p-8 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
	<div>
		<p class="eyebrow">Game servers{scope ? ` · ${scope.personal ? 'Personal workspace' : scope.name}` : ''}</p>
		<h1 class="mt-2 text-[2rem] leading-tight font-semibold tracking-tight">Host a <span class="text-action">Minecraft</span> server.</h1>
		<p class="mt-2 max-w-prose text-muted">Paper, Purpur, Vanilla, Fabric, Forge, NeoForge, Folia and Velocity. Pick a version, accept the EULA and start: RivetPanel downloads the server, chooses the right Java, and gives you a console, files, backups and schedules.</p>
	</div>
	{#if session.features.games}<a class="btn btn-primary" href="/servers/new"><Icon name="plus" />New server</a>{/if}
</section>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if !session.features.games}
	<div class="mt-5">
		<EmptyState title="Game servers are turned off on this panel">
			<p>An administrator can enable them with <code>RIVET_MODULES=game_servers=preview</code> and a restart.</p>
		</EmptyState>
	</div>
{:else}
	{#if servers && servers.length > 0}
		<div class="mt-5 flex flex-wrap items-center gap-2">
			<label class="relative min-w-0 flex-1 basis-60">
				<span class="sr-only">Search servers</span>
				<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
				<input class="field pl-8" type="search" placeholder="Search by name or type" bind:value={q} />
			</label>
		</div>
	{/if}
	<section class="mt-4" aria-label="Game servers">
		{#if servers === null}
			<Skeleton rows={3} label="Loading servers" />
		{:else if scoped.length === 0}
			<EmptyState title={servers.length ? 'No servers in this workspace' : 'Create your first game server'}>
				<p>Choose a server type such as Paper (plugins) or Fabric (mods), pick the Minecraft version, and start it. The first start downloads the server software.</p>
				{#snippet actions()}<a class="btn btn-primary" href="/servers/new"><Icon name="plus" />New server</a>{/snippet}
			</EmptyState>
		{:else}
			<ul class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{#each shown as s (s.id)}
					{@const bp = blueprints[s.blueprint_id ?? '']}
					{@const st = status[s.id]}
					{@const addr = joinAddress(s)}
					<li class="card flex flex-col p-5">
						<div class="flex items-start justify-between gap-2">
							{#if s.logo_url}
								<img src={s.logo_url} alt="" class="size-10 shrink-0 rounded-tile object-cover" />
							{:else}
								<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 text-action" aria-hidden="true"><Icon name="cube" /></span>
							{/if}
							<StatusBadge bot={s} {now} size="sm" />
						</div>
						<a href={resourceHref(s)} class="mt-4 truncate text-title font-semibold hover:underline">{s.name}</a>
						<p class="truncate text-small text-muted">{bp ? `${bp.name} · ${bp.category}` : 'Game server'}</p>
						{#if addr}
							<button class="mt-2 flex min-w-0 items-center gap-1.5 text-left font-mono text-small text-action hover:underline" onclick={() => copy(addr)} title="Copy the address players connect to">
								<span class="truncate">{addr}</span><Icon name="copy" size={12} />
							</button>
						{/if}
						<dl class="mt-4 grid grid-cols-3 gap-2 border-t border-rule-soft pt-4">
							<div><dt class="eyebrow">Players</dt><dd class="mt-0.5 font-mono text-small font-medium">{st?.online ? `${st.players}/${st.max_players}` : '—'}</dd></div>
							<div><dt class="eyebrow">Memory</dt><dd class="mt-0.5 font-mono text-small font-medium">{fmtBytes(s.memory_bytes)}</dd></div>
							<div><dt class="eyebrow">CPU</dt><dd class="mt-0.5 font-mono text-small font-medium">{fmtCpu(s.nano_cpus)}</dd></div>
						</dl>
						{#if st?.version || multi}
							<p class="mt-3 truncate text-small text-muted">{st?.version ?? ''}{st?.version && multi && workspaceName(s.workspace_id) ? ' · ' : ''}{multi ? workspaceName(s.workspace_id) : ''}</p>
						{/if}
						<div class="mt-auto flex gap-2 pt-4">
							<a href={resourceHref(s)} class="btn btn-primary flex-1">Manage</a>
							{#if can(s, Perm.power) && session.features.runner}
								{#if isStopped(s)}
									<button class="btn btn-icon" aria-label="Start {s.name}" title="Start" disabled={busy[s.id]} onclick={() => act(s, 'start')}><Icon name="play" size={14} /></button>
								{:else}
									<button class="btn btn-icon" aria-label="Stop {s.name}" title="Stop" disabled={busy[s.id] || s.desired_state === 'stopped'} onclick={() => act(s, 'stop')}><Icon name="stop" size={14} /></button>
								{/if}
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
{/if}
