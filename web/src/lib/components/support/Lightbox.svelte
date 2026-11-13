<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtBytes } from '$lib/support/markdown';
	import type { Attachment } from '$lib/api/support';

	// A full-window picture viewer on <dialog> (focus trap, Escape, inert
	// page). Arrow keys or the side buttons move between a message's
	// pictures; clicking the backdrop closes it; Z toggles actual size.
	let { items, index = $bindable(0), onclose }: { items: Attachment[]; index?: number; onclose: () => void } = $props();

	let dialog = $state<HTMLDialogElement>();
	let actual = $state(false);
	const f = $derived(items[index]);

	onMount(() => {
		dialog?.showModal();
		dialog?.focus(); // the viewer itself, so no control starts with a focus ring
		return () => dialog?.open && dialog.close();
	});
	function go(d: number) {
		if (items.length < 2) return;
		index = (index + d + items.length) % items.length;
		actual = false;
	}
	function key(e: KeyboardEvent) {
		if (e.key === 'ArrowRight') (e.preventDefault(), go(1));
		else if (e.key === 'ArrowLeft') (e.preventDefault(), go(-1));
		else if (e.key === 'z' || e.key === 'Z') (e.preventDefault(), (actual = !actual));
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
<dialog bind:this={dialog} tabindex="-1" class="lightbox" aria-label="Picture {index + 1} of {items.length}: {f?.name}" onclose={onclose} onkeydown={key} onclick={(e) => e.target === e.currentTarget && dialog?.close()}>
	{#if f}
		<header class="lb-head">
			<span class="min-w-0 truncate font-medium">{f.name}</span>
			<span class="shrink-0 text-white/60 tabular-nums">{fmtBytes(f.size)}{#if items.length > 1} · {index + 1} / {items.length}{/if}</span>
			<span class="flex-1"></span>
			<button type="button" class="lb-btn" onclick={() => (actual = !actual)} title={actual ? 'Fit to the window (Z)' : 'Actual size (Z)'} aria-pressed={actual}><Icon name={actual ? 'minimize' : 'maximize'} size={15} /><span class="sr-only">{actual ? 'Fit to the window' : 'Actual size'}</span></button>
			<a class="lb-btn" href={f.url} download={f.name} title="Download"><Icon name="download" size={15} /><span class="sr-only">Download</span></a>
			<a class="lb-btn" href={f.url} target="_blank" rel="noopener" title="Open in a new tab"><Icon name="external" size={15} /><span class="sr-only">Open in a new tab</span></a>
			<button type="button" class="lb-btn" onclick={() => dialog?.close()} title="Close (Esc)"><Icon name="x" size={16} /><span class="sr-only">Close</span></button>
		</header>
		<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
		<div class="lb-stage" class:actual onclick={(e) => e.target === e.currentTarget && dialog?.close()}>
			{#key f.id}<img src={f.url} alt={f.name} class:actual onclick={() => (actual = !actual)} />{/key}
		</div>
		{#if items.length > 1}
			<button type="button" class="lb-nav left" onclick={() => go(-1)} aria-label="Previous picture"><Icon name="chevronLeft" size={20} /></button>
			<button type="button" class="lb-nav right" onclick={() => go(1)} aria-label="Next picture"><Icon name="chevronRight" size={20} /></button>
		{/if}
	{/if}
</dialog>

<style>
	.lightbox {
		position: fixed;
		inset: 0;
		width: 100vw;
		height: 100dvh;
		max-width: none;
		max-height: none;
		margin: 0;
		padding: 0;
		border: 0;
		background: transparent;
		color: white;
		display: flex;
		flex-direction: column;
		outline: none;
	}
	.lightbox::backdrop {
		background: rgb(14 12 10 / 0.88);
		animation: fade 180ms ease-out both;
	}
	.lb-head {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.75rem 1rem;
		font-size: 13px;
	}
	.lb-btn {
		display: grid;
		place-items: center;
		width: 2.25rem;
		height: 2.25rem;
		border-radius: var(--radius-control);
		color: rgb(255 255 255 / 0.78);
	}
	.lb-btn:hover,
	.lb-btn:focus-visible,
	.lb-btn[aria-pressed='true'] {
		color: white;
		background: rgb(255 255 255 / 0.12);
	}
	.lb-stage {
		flex: 1;
		min-height: 0;
		display: grid;
		place-items: center;
		padding: 0 4rem 2rem;
		overflow: auto;
	}
	.lb-stage.actual {
		place-items: start center;
	}
	.lb-stage img {
		max-width: 100%;
		max-height: 100%;
		object-fit: contain;
		border-radius: var(--radius-control);
		box-shadow: 0 24px 60px -20px rgb(0 0 0 / 0.6);
		cursor: zoom-in;
		animation: lb-in 260ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	.lb-stage img.actual {
		max-width: none;
		max-height: none;
		cursor: zoom-out;
	}
	@keyframes lb-in {
		from {
			opacity: 0;
			transform: scale(0.97);
		}
	}
	.lb-nav {
		position: absolute;
		top: 50%;
		display: grid;
		place-items: center;
		width: 2.75rem;
		height: 2.75rem;
		margin-top: -1.375rem;
		border-radius: var(--radius-pill);
		background: rgb(255 255 255 / 0.1);
		color: white;
		transition: background-color 120ms;
	}
	.lb-nav:hover,
	.lb-nav:focus-visible {
		background: rgb(255 255 255 / 0.22);
	}
	.lb-nav.left {
		left: 0.75rem;
	}
	.lb-nav.right {
		right: 0.75rem;
	}
	@media (max-width: 40rem) {
		.lb-stage {
			padding: 0 0.5rem 1rem;
		}
		.lb-nav {
			top: auto;
			bottom: 1rem;
			margin-top: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.lb-stage img {
			animation: none;
		}
	}
</style>
