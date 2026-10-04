<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import type { Blueprint, GameStatus } from '$lib/api/games';
	import { session, can as canDo } from '$lib/session.svelte';
	import { currentWorkspace, loadWorkspaces, workspaceName, workspaces } from '$lib/workspaces.svelte';
	import ResourceTable from '$lib/components/ResourceTable.svelte';
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

	const sorted = $derived([...shown].sort((a, b) => Number(b.favorite) - Number(a.favorite) || a.name.localeCompare(b.name)));
	const creates = $derived(session.features.games && canDo('bots.create'));
</script>

<svelte:head><title>Game servers · RivetPanel</title></svelte:head>

<header class="page-head">
	<h1 class="font-semibold">Game servers</h1>
	{#if servers}<p class="text-small text-muted">{scoped.filter((s) => s.phase === 'running').length} of {scoped.length} running{scope ? `, ${scope.personal ? 'personal workspace' : scope.name}` : ''}</p>{/if}
	{#if creates}<a class="btn btn-primary ml-auto" href="/servers/new"><Icon name="plus" />New server</a>{/if}
</header>

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
		{:else}
			<ResourceTable kind="game" label="Game servers" items={sorted} {now} {blueprints} players={status} note={(s) => (s.shared ? 'shared with you' : multi ? workspaceName(s.workspace_id) : '')} onChanged={load}>
				{#snippet empty()}
					{#if q.trim() && scoped.length}
						<span>No servers match “{q.trim()}”.</span><button class="link" onclick={() => (q = '')}>Clear search</button>
					{:else if servers?.length}
						<span>No servers in this workspace.</span>
					{:else if creates}
						<span>No game servers yet. Pick a game such as Minecraft Paper (plugins), Fabric (mods) or Valheim, choose the version and start it.</span>
						<a class="link" href="/servers/new?family=minecraft">Minecraft server</a><a class="link" href="/servers/new?family=steam">Steam game server</a>
					{:else}
						<span>No game servers yet. Servers shared with you, or in workspaces you belong to, appear here.</span>
					{/if}
				{/snippet}
			</ResourceTable>
		{/if}
	</section>
{/if}
