<script lang="ts">
	import { fmtBytes, fmtCpu } from '$lib/api/client';
	import type { LocationInfo, NodeInfo } from '$lib/api/types';
	import CapacityMeter from '$lib/components/CapacityMeter.svelte';

	// Where a new app goes: one card per node with its state, load and the
	// largest limits an app may have there. Offline, disabled and draining
	// nodes are shown but cannot be chosen (the server refuses them too).
	let {
		nodes,
		locations,
		selected,
		onselect,
		defaultId,
		pinnedId,
		disabledIds = []
	}: {
		nodes: NodeInfo[];
		locations: LocationInfo[];
		selected: string;
		onselect: (id: string) => void;
		/** The panel's default node when the list does not carry `default`. */
		defaultId?: string;
		/** The node pinned to the workspace being created in. */
		pinnedId?: string;
		disabledIds?: string[];
	} = $props();

	const locationName = (n: NodeInfo) => locations.find((l) => l.id === n.location_id)?.name ?? '';
	const online = (n: NodeInfo) => n.transport === 'local' || !!n.agent?.connected;
	const blocker = (n: NodeInfo) => (!n.enabled ? 'Disabled — cannot receive new apps' : n.draining ? 'Draining — cannot receive new apps' : !online(n) ? 'Offline — cannot receive new apps' : disabledIds.includes(n.id) ? 'Not available here' : '');
	const selectable = (n: NodeInfo) => blocker(n) === '';
	const hostname = (n: NodeInfo) => (n.transport === 'local' ? 'this host' : n.agent?.hostname || n.public_address || 'remote agent');
	const isDefault = (n: NodeInfo) => n.default || (!!defaultId && n.id === defaultId);

	// Roving tabindex: the chosen card is the tab stop, else the first usable one.
	const tabStop = $derived(nodes.some((n) => n.id === selected && selectable(n)) ? selected : (nodes.find(selectable)?.id ?? ''));
	let buttons: Record<string, HTMLButtonElement> = {};

	function move(from: number, dir: 1 | -1) {
		if (!nodes.length) return;
		for (let step = 1; step <= nodes.length; step++) {
			const n = nodes[(((from + dir * step) % nodes.length) + nodes.length) % nodes.length];
			if (selectable(n)) {
				onselect(n.id);
				buttons[n.id]?.focus();
				return;
			}
		}
	}
	function onkeydown(e: KeyboardEvent, i: number) {
		if (e.key === 'ArrowRight' || e.key === 'ArrowDown') move(i, 1);
		else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') move(i, -1);
		else if (e.key === ' ' || e.key === 'Enter') {
			if (selectable(nodes[i])) onselect(nodes[i].id);
		} else return;
		e.preventDefault();
	}
</script>

<div role="radiogroup" aria-label="Node" class="grid gap-3 sm:grid-cols-2">
	{#each nodes as n, i (n.id)}
		{@const on = n.id === selected}
		{@const why = blocker(n)}
		{@const up = online(n)}
		<button
			type="button"
			role="radio"
			aria-checked={on}
			aria-disabled={why ? 'true' : undefined}
			tabindex={n.id === tabStop ? 0 : -1}
			bind:this={buttons[n.id]}
			class="min-w-0 rounded-tile border p-3.5 text-left transition-colors focus-visible:ring-2 focus-visible:ring-action/60 focus-visible:outline-none {on ? 'border-action bg-action/5' : 'border-rule-soft bg-panel'} {why ? 'cursor-not-allowed opacity-60' : 'hover:border-rule'}"
			onclick={() => selectable(n) && onselect(n.id)}
			onkeydown={(e) => onkeydown(e, i)}
		>
			<span class="flex flex-wrap items-center gap-x-2 gap-y-1">
				<span class="font-semibold">{n.name}</span>
				{#if locationName(n)}<span class="text-small text-muted">{locationName(n)}</span>{/if}
				<span class="pill">{n.transport === 'local' ? 'Local' : 'Remote'}</span>
				<span class="pill" data-tone={up ? 'run' : 'fail'}>{up ? 'Online' : 'Offline'}</span>
				{#if isDefault(n)}<span class="pill" data-tone="run">Default</span>{/if}
				{#if pinnedId && n.id === pinnedId}<span class="pill" data-tone="warn">Pinned to this workspace</span>{/if}
			</span>
			<span class="mt-1 block truncate text-small text-muted">{hostname(n)}</span>
			{#if why}
				<span class="mt-2 block text-small font-medium {up ? 'text-warn' : 'text-fail'}">{why}</span>
			{:else if n.latest}
				<span class="mt-3 block">
					<CapacityMeter label="Memory" value={n.latest.memory_used_bytes} max={n.latest.memory_total_bytes} format={fmtBytes} />
				</span>
				<span class="mt-2 block text-small text-muted">CPU {Math.round(n.latest.cpu_percent)}% · {n.latest.running_bots} running · {n.server_count} {n.server_count === 1 ? 'app' : 'apps'}</span>
			{:else}
				<span class="mt-2 block text-small text-muted">{n.server_count} {n.server_count === 1 ? 'app' : 'apps'}</span>
			{/if}
			{#if n.limits}
				<span class="mt-1.5 block text-small text-muted">Up to {fmtBytes(n.limits.max_memory_bytes)} · {fmtCpu(n.limits.max_nano_cpus)} per app</span>
			{/if}
		</button>
	{/each}
</div>
