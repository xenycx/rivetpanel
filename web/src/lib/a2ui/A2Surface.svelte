<script lang="ts" module>
	import type { Action, Surface } from './model.svelte';
	import { ChartGroup } from '$lib/charts/sync.svelte';
	export type Host = {
		surface: Surface;
		/** Sends a button's event to the assistant. */
		act: (a: Action, label: string) => void;
		/** True while the assistant is working: events wait. */
		locked: () => boolean;
		/** Opens a panel page or an https address. */
		open: (href: string) => void;
		/** Charts on one surface share a crosshair and zoom. */
		group: ChartGroup;
	};
	export const HOST = Symbol('a2ui-host');
</script>

<script lang="ts">
	import { setContext } from 'svelte';
	import A2Node from './A2Node.svelte';

	// One A2UI surface, drawn with the panel's own controls.
	let { surface, act, locked, open }: { surface: Surface; act: Host['act']; locked: Host['locked']; open: Host['open'] } = $props();
	const group = new ChartGroup();
	setContext<Host>(HOST, {
		group,
		get surface() {
			return surface;
		},
		act: (a, l) => act(a, l),
		locked: () => locked(),
		open: (h) => open(h)
	});
</script>

<section class="a2-surface" class:a2-fresh={surface.fresh} data-a2-surface={surface.id} aria-label="Interactive reply">
	{#if surface.components.root}
		<A2Node id="root" scope={{ path: '/', index: null }} />
	{:else}
		<div class="a2-pending" aria-hidden="true"></div>
	{/if}
</section>
