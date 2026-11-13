<script lang="ts">
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import { href } from '$lib/docs';
	import Inlines from '$lib/docs/Inlines.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const c = $derived(data.category);
	const minutes = (words: number) => Math.max(1, Math.round(words / 220));
</script>

<svelte:head>
	<title>{c.title} · RivetPanel documentation</title>
	<meta name="description" content={c.blurb} />
</svelte:head>

<div class="docs-page single">
	<div class="min-w-0">
		<nav class="docs-crumbs" aria-label="Breadcrumb"><ol><li><a href="/docs">Docs</a></li></ol></nav>
		<header class="docs-head">
			<h1 class="docs-title flex items-center gap-3"><span class="docs-cat-glyph"><Icon name={c.icon as IconName} size={20} /></span>{c.title}</h1>
			<p class="docs-lede">{c.blurb}</p>
		</header>

		<ol class="docs-index">
			{#each c.pages as p, i (p.slug)}
				<li>
					<a href={href(p.slug)}>
						<span class="docs-index-no">{i + 1}</span>
						<span class="min-w-0">
							<span class="flex flex-wrap items-baseline gap-x-2">
								<span class="docs-index-title">{p.title}</span>
								{#if p.audience === 'admin'}<span class="docs-admin-tag">Administrators</span>{/if}
							</span>
							<span class="mt-0.5 block text-muted"><Inlines c={p.ledeC} /></span>
							{#if p.headings.some((h) => h.d === 2)}
								<span class="docs-index-heads">{p.headings.filter((h) => h.d === 2).slice(0, 5).map((h) => h.text).join(' · ')}</span>
							{/if}
						</span>
						<span class="docs-index-time">{minutes(p.words)} min</span>
					</a>
				</li>
			{/each}
		</ol>
	</div>
</div>
