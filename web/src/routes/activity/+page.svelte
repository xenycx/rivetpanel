<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { OpPage, Operation, Bot } from '$lib/api/types';
	import OperationRow from '$lib/components/OperationRow.svelte';
	import ChangeList from '$lib/components/ChangeList.svelte';
	import type { AuditEvent, AuditPage } from '$lib/audit';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let ops = $state<Operation[] | null>(null);
	let next = $state<number | undefined>();
	let error = $state('');
	let now = $state(Date.now());
	let bots = $state<Bot[]>([]);
	let kind = $state(page.url.searchParams.get('kind') ?? '');
	let botF = $state(page.url.searchParams.get('bot') ?? '');
	let loadingMore = $state(false);
	let view = $state<'work' | 'changes'>(page.url.searchParams.get('view') === 'changes' ? 'changes' : 'work');
	let changes = $state<AuditEvent[] | null>(null);
	let nextChange = $state<number | undefined>();

	async function loadChanges(before?: number) {
		const q = new URLSearchParams({ limit: '40' });
		if (before) q.set('before', String(before));
		try {
			const p = await api<AuditPage>('GET', botF ? `/bots/${botF}/changes?${q}` : `/activity/changes?${q}`);
			changes = before ? [...(changes ?? []), ...p.events] : p.events;
			nextChange = p.next_before;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Changes could not be loaded.';
			changes = changes ?? [];
		}
	}

	function url(before?: number) {
		const q = new URLSearchParams({ limit: '30' });
		if (kind) q.set('kind', kind === 'deploy' ? 'deploy,rollback' : kind);
		if (before) q.set('before', String(before));
		return botF ? `/bots/${botF}/operations?${q}` : `/operations?${q}`;
	}
	async function load() {
		try {
			const p = await api<OpPage>('GET', url());
			ops = p.operations;
			next = p.next_before;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Activity could not be loaded.';
			ops = ops ?? [];
		}
	}
	async function more() {
		if (!next) return;
		loadingMore = true;
		try {
			const p = await api<OpPage>('GET', url(next));
			ops = [...(ops ?? []), ...p.operations];
			next = p.next_before;
		} finally {
			loadingMore = false;
		}
	}
	$effect(() => {
		const u = new URL(location.href);
		kind ? u.searchParams.set('kind', kind) : u.searchParams.delete('kind');
		botF ? u.searchParams.set('bot', botF) : u.searchParams.delete('bot');
		view === 'changes' ? u.searchParams.set('view', 'changes') : u.searchParams.delete('view');
		if (u.href !== location.href) history.replaceState(history.state, '', u);
		if (view === 'changes') {
			changes = null;
			loadChanges();
		} else {
			ops = null;
			load();
		}
	});
	onMount(() => {
		api<{ bots: Bot[] }>('GET', '/bots').then((r) => (bots = r.bots)).catch(() => {});
		const t = setInterval(() => {
			now = Date.now();
			// Refresh the first page while something is still running.
			if (document.visibilityState === 'visible' && ops?.some((o) => o.status === 'running' || o.status === 'queued')) load();
		}, 3000);
		return () => clearInterval(t);
	});
	const canOutput = (o: Operation) => {
		const b = bots.find((x) => x.id === o.bot_id);
		return !!b && ((b.permissions & 16) !== 0 || (b.permissions & 1) !== 0);
	};
</script>

<svelte:head><title>Activity · RivetPanel</title></svelte:head>

<h1 class="text-page">Activity</h1>
<p class="mt-0.5 max-w-prose text-muted">What happened on the bots you can see: long-running work, and who changed what.</p>

<div class="mt-5 flex flex-wrap items-center gap-2">
	<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Show">
		<button class="rounded-inner px-3 py-1 font-medium {view === 'work' ? 'bg-ink text-paper' : 'text-ink/75 hover:bg-paper'}" aria-pressed={view === 'work'} onclick={() => (view = 'work')}>Work</button>
		<button class="rounded-inner px-3 py-1 font-medium {view === 'changes' ? 'bg-ink text-paper' : 'text-ink/75 hover:bg-paper'}" aria-pressed={view === 'changes'} onclick={() => (view = 'changes')}>Changes</button>
	</div>
	<select class="field w-auto" bind:value={botF} aria-label="Bot">
		<option value="">All bots</option>
		{#each bots as b (b.id)}<option value={b.id}>{b.name}</option>{/each}
	</select>
	{#if view === 'work'}
	<select class="field w-auto" bind:value={kind} aria-label="Kind">
		<option value="">Everything</option>
		<option value="build">Builds</option>
		<option value="deploy">Deployments</option>
		<option value="backup">Backups</option>
		<option value="restore">Restores</option>
	</select>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
<div class="mt-4">
	{#if view === 'changes'}
		{#if changes === null}
			<Skeleton rows={5} label="Loading changes" />
		{:else if changes.length === 0}
			<EmptyState title="No changes recorded yet" compact><p>Starting and stopping bots, editing files and variables, sharing and account changes are recorded here, without any values.</p></EmptyState>
		{:else}
			<ChangeList events={changes} showBot={!botF} {now} />
			{#if nextChange}<div class="mt-3"><button class="btn" onclick={() => loadChanges(nextChange)}>Show older</button></div>{/if}
			<p class="mt-3 text-small text-muted">Kept for 180 days. Secret values, file contents and passwords are never recorded, only names.</p>
		{/if}
	{:else if ops === null}
		<Skeleton rows={5} label="Loading activity" />
	{:else if ops.length === 0}
		<EmptyState title="Nothing here yet" compact>
			<p>Starting a bot runs its build; deployments, backups and restores are recorded as they happen.</p>
		</EmptyState>
	{:else}
		<ul class="list-card">
			{#each ops as op (op.id)}
				<OperationRow {op} {now} showBot={!botF} canOutput={canOutput(op)} />
			{/each}
		</ul>
		{#if next}<div class="mt-3"><button class="btn" disabled={loadingMore} onclick={more}>{loadingMore ? 'Loading…' : 'Show older'}</button></div>{/if}
	{/if}
</div>
