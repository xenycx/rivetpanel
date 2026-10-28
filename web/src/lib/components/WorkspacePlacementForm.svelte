<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { NodeInfo } from '$lib/api/types';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { can, session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';

	// The node a workspace's new apps are created on ("" = the panel default).
	let { workspaceId, nodeId, onsaved }: { workspaceId: string; nodeId: string; onsaved?: (nodeId: string) => void } = $props();

	const editable = $derived(can('nodes.manage'));
	let nodes = $state<NodeInfo[] | null>(null);
	let error = $state('');
	let busy = $state(false);
	let saved = $state('');
	let choice = $state('');
	$effect(() => {
		saved = choice = nodeId ?? '';
	});

	$effect(() => {
		if (!session.features.agents || !editable) return;
		api<{ nodes: NodeInfo[] }>('GET', '/nodes')
			.then((r) => ((nodes = r.nodes), (error = '')))
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The nodes could not be loaded.'));
	});

	const nodeState = (n: NodeInfo) => (n.transport === 'local' ? 'Local' : n.agent?.connected ? 'Online' : 'Offline');
	const unavailable = (n: NodeInfo) => !n.enabled || n.draining;
	const label = (n: NodeInfo) => `${n.name} · ${nodeState(n)}${!n.enabled ? ' · disabled' : n.draining ? ' · draining' : ''}`;
	const defaultName = $derived(nodes?.find((n) => n.default)?.name ?? 'this host');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		try {
			const r = await api<{ node_id: string }>('PUT', `/admin/workspaces/${workspaceId}/node`, { node_id: choice });
			saved = choice = r.node_id ?? choice;
			const n = nodes?.find((x) => x.id === saved);
			toast(n ? `New apps in this workspace go to ${n.name}` : 'New apps in this workspace use the panel default node', 'success');
			onsaved?.(saved);
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The node could not be saved.', 'fail');
		} finally {
			busy = false;
		}
	}
</script>

{#if session.features.agents}
	<section class="card mt-3 p-5" aria-labelledby="placement-h">
		<h4 id="placement-h" class="font-semibold">Node for new apps</h4>
		<p class="mt-1 mb-3 max-w-3xl text-small text-muted">New apps in this workspace are created on this node. Existing apps stay where they are.</p>
		{#if !editable}
			<p class="text-small">{saved ? 'Pinned to a node' : 'Panel default'}</p>
			<p class="mt-1 text-small text-muted">Only accounts that manage nodes can change it.</p>
		{:else if error}
			<Notice tone="fail" live>{error}</Notice>
		{:else if nodes}
			<form class="flex flex-wrap items-end gap-3" onsubmit={submit}>
				<label class="block min-w-0 flex-1 basis-60">
					<span class="label">Node</span>
					<select class="field" bind:value={choice} disabled={busy}>
						<option value="">Panel default ({defaultName})</option>
						{#each nodes as n (n.id)}<option value={n.id} disabled={unavailable(n) && n.id !== saved}>{label(n)}</option>{/each}
					</select>
				</label>
				<button class="btn btn-primary" type="submit" disabled={busy || choice === saved}>Save node</button>
			</form>
		{:else}
			<p class="text-small text-muted">Loading nodes…</p>
		{/if}
	</section>
{/if}
