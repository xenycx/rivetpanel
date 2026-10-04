<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Allocation, NodeInfo } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let allocs = $state<Allocation[] | null>(null);
	let nodes = $state<NodeInfo[]>([]);
	let error = $state('');
	let form = $state({ node_id: '', ip: '0.0.0.0', ports: '', notes: '' });
	let formError = $state('');
	let busy = $state(false);
	let filter = $state<'all' | 'free' | 'used'>('all');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			allocs = (await api<{ allocations: Allocation[] }>('GET', '/admin/allocations')).allocations;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		api<{ nodes: NodeInfo[] }>('GET', '/nodes')
			.then((r) => {
				nodes = r.nodes;
				form.node_id ||= r.nodes[0]?.id ?? '';
			})
			.catch(() => {});
	});

	const shown = $derived((allocs ?? []).filter((a) => filter === 'all' || (filter === 'free' ? !a.bot_id : !!a.bot_id)));
	const nodeName = (id: string) => nodes.find((n) => n.id === id)?.name ?? id.slice(0, 8);

	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			const r = await api<{ allocations: Allocation[] }>('POST', '/admin/allocations', form);
			toast(r.allocations.length ? `Added ${r.allocations.length} ${r.allocations.length === 1 ? 'port' : 'ports'}.` : 'Those ports already exist or are in use.');
			form.ports = '';
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function alias(a: Allocation) {
		const v = await promptDialog({ title: `Address for port ${a.port}`, body: 'The host name or address players use, for example play.example.com. Leave empty to show the IP.', label: 'Address', value: a.alias, placeholder: 'play.example.com', confirmLabel: 'Save', mono: true });
		if (v === null) return;
		try {
			await api('PATCH', `/admin/allocations/${a.id}`, { alias: v, notes: a.notes });
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(a: Allocation) {
		try {
			await api('DELETE', `/admin/allocations/${a.id}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
</script>

<svelte:head><title>Allocations · RivetPanel</title></svelte:head>

<h2 class="text-section">Allocations</h2>
<p class="mt-1 max-w-3xl text-muted">The ports game servers may use on each node. New servers take free ports from this pool; when a node has none, RivetPanel picks the next free port from the server type's default (for example 25565) and adds it here.</p>

{#if !session.features.games}
	<Notice tone="info" class="mt-5">Game servers are off. Enable the <code>game_servers</code> module under Modules to manage allocations.</Notice>
{:else}
	<form class="card mt-5 grid gap-3 p-4 sm:grid-cols-[1fr_1fr_1.4fr_1.4fr_auto] sm:items-end" onsubmit={create}>
		<label class="block"><span class="label">Node</span><select class="field" bind:value={form.node_id}>{#each nodes as n (n.id)}<option value={n.id}>{n.name}</option>{/each}</select></label>
		<label class="block"><span class="label">Bind address</span><input class="field font-mono" bind:value={form.ip} /></label>
		<label class="block"><span class="label">Ports</span><input class="field font-mono" bind:value={form.ports} placeholder="25565-25600, 19132" required /></label>
		<label class="block"><span class="label">Notes</span><input class="field" maxlength="200" bind:value={form.notes} placeholder="Optional" /></label>
		<button class="btn btn-primary" disabled={busy || !form.ports.trim()}><Icon name="plus" size={14} />Add</button>
		{#if formError}<Notice tone="fail" class="sm:col-span-5" live>{formError}</Notice>{/if}
	</form>

	{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
	<div class="mt-5 flex gap-1" role="group" aria-label="Filter allocations">
		{#each [['all', 'All'], ['free', 'Free'], ['used', 'In use']] as [k, l] (k)}
			<button class="btn btn-sm {filter === k ? 'btn-primary' : ''}" onclick={() => (filter = k as typeof filter)}>{l}</button>
		{/each}
	</div>
	{#if allocs === null && !error}
		<div class="mt-4"><Skeleton rows={5} label="Loading allocations" /></div>
	{:else if allocs}
		<ul class="list-card mt-3">
			{#each shown as a (a.id)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-2.5">
					<span class="min-w-0 flex-1 font-mono text-small">{a.ip}:{a.port}{#if a.alias}<span class="ml-2 text-muted">→ {a.alias}</span>{/if}</span>
					<span class="text-small text-muted">{nodeName(a.node_id)}</span>
					{#if a.bot_id}
						<a class="pill hover:underline" data-tone="run" href="/servers/{a.bot_id}">{a.primary ? 'Primary' : 'In use'}</a>
					{:else}
						<span class="pill">Free</span>
					{/if}
					<button class="btn btn-sm" onclick={() => alias(a)}>Address</button>
					{#if !a.bot_id}<button class="btn btn-sm btn-quiet text-fail" onclick={() => remove(a)} aria-label="Delete port {a.port}"><Icon name="trash" size={14} /></button>{/if}
				</li>
			{:else}
				<li class="px-4 py-3 text-small text-muted">No allocations yet.</li>
			{/each}
		</ul>
	{/if}
{/if}
