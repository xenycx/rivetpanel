<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtBytes } from '$lib/support/markdown';
	import type { Attachment } from '$lib/api/support';
	import Lightbox from './Lightbox.svelte';

	// The files a sent message carries. One picture shows large; several
	// form a strip of tiles; any of them opens the viewer. Other files are
	// cards that download.
	let { files }: { files: Attachment[] } = $props();
	const pics = $derived(files.filter((f) => f.image));
	const docs = $derived(files.filter((f) => !f.image));
	let viewing = $state<number | null>(null);
	const kind = (f: Attachment) =>
		f.content_type === 'application/pdf' ? 'PDF' : f.content_type.startsWith('video/') ? 'Video' : f.content_type === 'text/plain' ? 'Text' : f.content_type.includes('zip') ? 'Archive' : 'File';
</script>

{#if pics.length}
	<ul class="pics mt-2.5" class:single={pics.length === 1} aria-label="Pictures">
		{#each pics as f, i (f.id)}
			<li>
				<button type="button" class="pic" onclick={() => (viewing = i)} aria-label="View {f.name}" title="{f.name} · {fmtBytes(f.size)}">
					<img src={f.url} alt={f.name} loading="lazy" decoding="async" />
				</button>
			</li>
		{/each}
	</ul>
{/if}
{#if docs.length}
	<ul class="mt-2.5 flex flex-wrap gap-2" aria-label="Files">
		{#each docs as f (f.id)}
			<li>
				<a class="doc" href={f.url} download={f.name} title="Download {f.name}">
					<span class="doc-icon"><Icon name="file" size={15} /></span>
					<span class="min-w-0">
						<span class="block max-w-[18rem] truncate font-medium">{f.name}</span>
						<span class="block text-label text-muted">{kind(f)} · {fmtBytes(f.size)}</span>
					</span>
					<Icon name="download" size={14} class="doc-dl" />
				</a>
			</li>
		{/each}
	</ul>
{/if}

{#if viewing !== null}
	<Lightbox items={pics} bind:index={viewing} onclose={() => (viewing = null)} />
{/if}

<style>
	.pics {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.pic {
		display: block;
		overflow: hidden;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-control);
		background: var(--color-paper-2);
		cursor: zoom-in;
		transition:
			border-color 140ms,
			box-shadow 140ms;
	}
	.pic:hover,
	.pic:focus-visible {
		border-color: var(--color-rule);
		box-shadow: 0 6px 18px -10px rgb(20 18 16 / 0.35);
	}
	.pic img {
		display: block;
		width: 7.5rem;
		height: 7.5rem;
		object-fit: cover;
		transition: transform 260ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.pic:hover img {
		transform: scale(1.03);
	}
	/* A single picture shows at its own proportions. */
	.single li {
		max-width: min(100%, 26rem);
	}
	.single .pic img {
		width: auto;
		height: auto;
		max-width: 100%;
		max-height: 18rem;
		object-fit: contain;
	}
	.single .pic:hover img {
		transform: none;
	}
	.doc {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-control);
		background: var(--color-panel);
		padding: 0.45rem 0.65rem 0.45rem 0.45rem;
		font-size: 13px;
		line-height: 1.3;
		transition: border-color 140ms;
	}
	.doc:hover,
	.doc:focus-visible {
		border-color: var(--color-rule);
	}
	.doc-icon {
		display: grid;
		place-items: center;
		width: 2rem;
		height: 2rem;
		border-radius: var(--radius-inner);
		background: color-mix(in srgb, var(--color-ink) 6%, transparent);
		color: var(--color-muted);
	}
	.doc :global(.doc-dl) {
		color: var(--color-muted);
	}
	.doc:hover :global(.doc-dl) {
		color: var(--color-ink);
	}
	@media (prefers-reduced-motion: reduce) {
		.pic img {
			transition: none;
		}
	}
</style>
