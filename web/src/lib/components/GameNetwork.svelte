<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Allocation, type Bot } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { onMount } from 'svelte';
	import { loadGameHosts, nodeHost } from '$lib/connect.svelte';

	let { bot, stopped, onSaved }: { bot: Bot; stopped: boolean; onSaved: (b: Bot) => void } = $props();
	let error = $state('');
	let busy = $state(false);
	const admin = $derived(can(bot, Perm.admin));
	const allocs = $derived(bot.allocations ?? []);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	onMount(() => void loadGameHosts());
	// Never the panel's own host: players are given the node's address.
	const host = (a: Allocation) => a.alias || (a.ip === '0.0.0.0' || a.ip === '::' ? nodeHost(bot.node_id) : a.ip);

	async function refresh() {
		onSaved(await api<Bot>('GET', `/bots/${bot.id}`));
	}
	async function run(fn: () => Promise<unknown>, done: string) {
		busy = true;
		error = '';
		try {
			await fn();
			await refresh();
			toast(done);
		} catch (e) {
			error = msg(e);
		} finally {
			busy = false;
		}
	}
	const add = () => run(() => api('POST', `/bots/${bot.id}/allocations`), 'Allocation added. It is published on the next start.');
	const primary = (a: Allocation) => run(() => api('PUT', `/bots/${bot.id}/allocations/${a.id}/primary`), `Port ${a.port} is now the primary allocation.`);
	async function remove(a: Allocation) {
		if (!(await confirmDialog({ title: `Remove port ${a.port}?`, body: 'The port goes back to the pool and stops being published on the next start.', confirmLabel: 'Remove', tone: 'danger' }))) return;
		await run(() => api('DELETE', `/bots/${bot.id}/allocations/${a.id}`), `Port ${a.port} removed.`);
	}
	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast('Copied', 'success');
		} catch {
			toast(text, 'info');
		}
	}
</script>

<div class="grid max-w-3xl gap-4">
	<section class="card p-5">
		<div class="flex flex-wrap items-start justify-between gap-3">
			<div>
				<h2 class="text-section font-semibold">Allocations</h2>
				<p class="mt-1 text-small text-muted">Each allocation is an address and port on this node, published for TCP and UDP. Players connect to the primary one; the server receives it as <code>SERVER_PORT</code>. Extra ports are for plugins such as voice chat or web maps.</p>
			</div>
			{#if admin}<button class="btn" disabled={!stopped || busy} onclick={add}><Icon name="plus" size={14} />Add port</button>{/if}
		</div>
		<ul class="mt-4 divide-y divide-rule-soft rounded-tile border border-rule-soft">
			{#each allocs as a (a.id)}
				{@const addr = host(a) ? `${host(a)}:${a.port}` : `port ${a.port}`}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<button class="flex items-center gap-1.5 font-mono text-small font-medium hover:underline" onclick={() => copy(addr)} title="Copy address">{addr}<Icon name="copy" size={12} class="text-muted" /></button>
						<p class="text-small text-muted">Bound on {a.ip}{a.notes ? ` · ${a.notes}` : ''}</p>
					</div>
					{#if a.primary}
						<span class="pill" data-tone="run">Primary</span>
					{:else if admin}
						<button class="btn btn-sm" disabled={!stopped || busy} onclick={() => primary(a)}>Make primary</button>
						<button class="btn btn-sm btn-quiet text-fail" disabled={!stopped || busy} onclick={() => remove(a)} aria-label="Remove port {a.port}"><Icon name="trash" size={14} /></button>
					{/if}
				</li>
			{:else}
				<li class="px-4 py-3 text-small text-muted">No allocation. Add one so players can connect.</li>
			{/each}
		</ul>
		{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
	</section>
	<Notice tone="info" title="Reaching the server from the internet">
		Forward the primary port (TCP and UDP) on your router or firewall to this machine. An administrator can set a friendlier address (such as play.example.com) for each port under Administration → Allocations.
	</Notice>
</div>
