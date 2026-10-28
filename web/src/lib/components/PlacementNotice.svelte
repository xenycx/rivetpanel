<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { Placement } from '$lib/api/types';
	import Notice from '$lib/components/ui/Notice.svelte';

	// Where the apps of a workspace are created, for people who cannot choose
	// a node themselves: the node, whether it is online, and why it is the one.
	let { workspaceId, onloaded }: { workspaceId: string; onloaded?: (p: Placement) => void } = $props();

	let placement = $state<Placement | null>(null);
	let error = $state('');
	$effect(() => {
		const ws = workspaceId;
		let live = true;
		api<Placement>('GET', `/placement?workspace=${encodeURIComponent(ws)}`)
			.then((p) => {
				if (!live) return;
				placement = p;
				error = '';
				onloaded?.(p);
			})
			.catch((e) => {
				if (live) error = e instanceof ApiError ? e.message : 'Where new apps run could not be determined.';
			});
		return () => {
			live = false;
		};
	});

	const reasonText: Record<Placement['reason'], string> = {
		workspace: 'It is pinned to this workspace by an administrator.',
		panel: "It is the panel's default node for new apps.",
		local: 'It is the host the panel itself runs on.'
	};
</script>

{#if error}
	<Notice tone="fail" live>{error}</Notice>
{:else if placement}
	<div class="rounded-tile border border-rule-soft bg-panel px-3.5 py-2.5 text-small">
		<p>
			Your apps run on <strong>{placement.node.name}</strong>{#if placement.node.location}&nbsp;({placement.node.location}){/if} ·
			<span class={placement.node.online ? 'text-run' : 'text-fail'}>{placement.node.online ? 'Online' : 'Offline'}</span>
		</p>
		<p class="mt-0.5 text-muted">{reasonText[placement.reason]}</p>
	</div>
	{#if !placement.node.online}
		<Notice tone="warn" class="mt-2" live>New apps cannot be created until the node reconnects.</Notice>
	{/if}
{:else}
	<p class="text-small text-muted" role="status">Checking where new apps run…</p>
{/if}
