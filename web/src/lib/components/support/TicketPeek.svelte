<script lang="ts">
	import TicketThread from '$lib/components/TicketThread.svelte';
	import type { TicketView } from '$lib/api/support';
	import { shortcutAllowed } from '$lib/api/support';

	// The slide-over panel of the table and board layouts: the ticket opens
	// beside the list without hiding it (not modal). Escape or the close
	// button closes it; the address keeps the open ticket.
	let { id, initial = null, onview, onclose }: { id: string; initial?: TicketView | null; onview?: (v: TicketView) => void; onclose: () => void } = $props();

	function key(e: KeyboardEvent) {
		if (e.key === 'Escape' && shortcutAllowed(e)) {
			e.preventDefault();
			onclose();
		}
	}
</script>

<svelte:window onkeydown={key} />

<aside class="peek" aria-label="Ticket">
	{#key id}
		<TicketThread {id} mode="peek" {initial} {onview} {onclose} />
	{/key}
</aside>

<style>
	.peek {
		position: fixed;
		top: calc(3.5rem + 1px);
		right: 0;
		bottom: 0;
		z-index: 25;
		width: min(46rem, 94vw);
		border-left: 1px solid var(--color-rule);
		background: var(--color-panel);
		box-shadow: -18px 0 40px -18px rgb(20 18 16 / 0.28);
		animation: peek-in 260ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes peek-in {
		from {
			transform: translateX(2.5rem);
			opacity: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.peek {
			animation: fade 180ms ease-out both;
		}
	}
</style>
