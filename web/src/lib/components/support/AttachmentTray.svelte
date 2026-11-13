<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtBytes } from '$lib/support/markdown';
	import type { Uploads } from '$lib/support/uploads.svelte';

	// The files a message being written will carry: pictures as thumbnails,
	// other files as chips, each with its upload progress and a remove
	// button. A failed file says why and stays until removed.
	let { uploads }: { uploads: Uploads } = $props();
	const pics = $derived(uploads.items.filter((i) => i.preview && i.state !== 'error'));
	const files = $derived(uploads.items.filter((i) => !i.preview || i.state === 'error'));
</script>

<div class="tray" aria-label="Attached files">
	{#if pics.length}
		<ul class="flex flex-wrap gap-2">
			{#each pics as it (it.key)}
				<li class="thumb" data-state={it.state} title="{it.name} · {fmtBytes(it.size)}">
					<img src={it.preview} alt={it.name} />
					{#if it.state === 'uploading'}
						<span class="thumb-veil" aria-hidden="true"></span>
						<span class="thumb-bar" style:--p={it.progress} role="progressbar" aria-label="Uploading {it.name}" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(it.progress * 100)}></span>
					{/if}
					<button type="button" class="thumb-x" onclick={() => uploads.remove(it.key)} aria-label="Remove {it.name}"><Icon name="x" size={11} /></button>
				</li>
			{/each}
		</ul>
	{/if}
	{#if files.length}
		<ul class="flex flex-wrap gap-1.5" class:mt-2={pics.length}>
			{#each files as it (it.key)}
				<li class="chip" data-state={it.state}>
					<Icon name={it.state === 'error' ? 'alert' : 'file'} size={13} class="shrink-0" />
					<span class="min-w-0">
						<span class="block max-w-[16rem] truncate font-medium" title={it.name}>{it.name}</span>
						<span class="block text-label {it.state === 'error' ? '' : 'text-muted'}">{it.state === 'error' ? it.error : it.state === 'uploading' ? `${Math.round(it.progress * 100)}% of ${fmtBytes(it.size)}` : fmtBytes(it.size)}</span>
					</span>
					<button type="button" class="chip-x" onclick={() => uploads.remove(it.key)} aria-label="Remove {it.name}"><Icon name="x" size={12} /></button>
					{#if it.state === 'uploading'}<span class="chip-bar" style:--p={it.progress} aria-hidden="true"></span>{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.tray {
		padding: 0 0.75rem 0.6rem;
	}
	.thumb {
		position: relative;
		width: 4.5rem;
		height: 4.5rem;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-control);
		overflow: hidden;
		background: var(--color-paper-2);
		animation: pop 220ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	.thumb img {
		width: 100%;
		height: 100%;
		object-fit: cover;
		transition: filter 200ms;
	}
	.thumb[data-state='uploading'] img {
		filter: saturate(0.4);
	}
	.thumb-veil {
		position: absolute;
		inset: 0;
		background: color-mix(in srgb, var(--color-panel) 35%, transparent);
	}
	.thumb-bar,
	.chip-bar {
		position: absolute;
		left: 0;
		bottom: 0;
		height: 3px;
		width: calc(var(--p, 0) * 100%);
		background: var(--color-action);
		transition: width 160ms linear;
	}
	.thumb-x {
		position: absolute;
		top: 0.2rem;
		right: 0.2rem;
		display: grid;
		place-items: center;
		width: 1.25rem;
		height: 1.25rem;
		border-radius: var(--radius-pill);
		background: rgb(20 18 16 / 0.62);
		color: white;
		opacity: 0;
		transition: opacity 120ms;
	}
	.thumb:hover .thumb-x,
	.thumb-x:focus-visible {
		opacity: 1;
	}
	@media (pointer: coarse) {
		.thumb-x {
			opacity: 1;
		}
	}
	.chip {
		position: relative;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		overflow: hidden;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-control);
		background: var(--color-raised);
		padding: 0.35rem 0.35rem 0.35rem 0.6rem;
		font-size: 12.5px;
		line-height: 1.3;
		animation: pop 220ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	.chip[data-state='error'] {
		border-color: color-mix(in srgb, var(--color-fail) 40%, transparent);
		background: color-mix(in srgb, var(--color-fail) 6%, var(--color-raised));
		color: var(--color-fail);
	}
	.chip-x {
		display: grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		border-radius: var(--radius-inner);
		color: var(--color-muted);
	}
	.chip-x:hover {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 7%, transparent);
	}
	@keyframes pop {
		from {
			opacity: 0;
			transform: scale(0.92);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.thumb,
		.chip {
			animation: none;
		}
	}
</style>
