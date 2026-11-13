<script lang="ts">
	import TicketThread from '$lib/components/TicketThread.svelte';
	import type { TicketView } from '$lib/api/support';
	import { shortcutAllowed } from '$lib/api/support';

	// The slide-over panel of the table and board layouts: the ticket opens
	// beside the list without hiding it (not modal). It slides in from the
	// right and, closed with Escape or its button, slides back out faster;
	// moving to another ticket cross-fades the content in place.
	let { id, initial = null, onview, onclose }: { id: string; initial?: TicketView | null; onview?: (v: TicketView) => void; onclose: () => void } = $props();

	let panel = $state<HTMLElement>();
	let closing = false;
	function close() {
		if (closing) return;
		closing = true;
		const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
		const a = panel?.animate(
			[
				{ transform: 'none', opacity: 1 },
				{ transform: still ? 'none' : 'translateX(2.25rem)', opacity: 0 }
			],
			{ duration: 160, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' }
		);
		// Close when the slide-out ends, or after its length regardless (a
		// background tab may never report the end).
		let done = false;
		const finish = () => {
			if (done) return;
			done = true;
			onclose();
		};
		a?.finished.then(finish, finish);
		setTimeout(finish, 200);
	}
	function key(e: KeyboardEvent) {
		if (e.key === 'Escape' && shortcutAllowed(e)) {
			e.preventDefault();
			close();
		}
	}
</script>

<svelte:window onkeydown={key} />

<aside bind:this={panel} class="peek" aria-label="Ticket">
	{#key id}
		<div class="peek-body h-full">
			<TicketThread {id} mode="peek" {initial} {onview} onclose={close} />
		</div>
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
		animation: peek-in 280ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes peek-in {
		from {
			transform: translateX(3rem);
			opacity: 0;
		}
	}
	/* Another ticket in the open panel: the content fades through. */
	.peek-body {
		animation: fade 180ms ease-out both;
	}
	@media (prefers-reduced-motion: reduce) {
		.peek {
			animation: fade 180ms ease-out both;
		}
	}
</style>
