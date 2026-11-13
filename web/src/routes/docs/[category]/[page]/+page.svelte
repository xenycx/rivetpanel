<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Blocks from '$lib/docs/Blocks.svelte';
	import Inlines from '$lib/docs/Inlines.svelte';
	import { href, pageMeta } from '$lib/docs';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const meta = $derived(data.meta);
	const toc = $derived(meta.headings.filter((h) => h.d === 2 || h.d === 3));
	const related = $derived(meta.related.map((s) => pageMeta(s)).filter((p) => !!p));
	const source = $derived(`https://github.com/xenycx/rivetpanel/blob/main/${meta.file}`);

	let article = $state<HTMLElement>();

	// A first load with #heading: the router looks for the heading before the
	// app shell has rendered the article, so jump to it once it exists.
	onMount(async () => {
		await tick();
		const id = decodeURIComponent(location.hash.slice(1));
		if (id) document.getElementById(id)?.scrollIntoView();
	});
	let activeId = $state('');

	// The heading the reader is in: the last one above a line just under the
	// sticky bars. Recomputed on scroll, at most once a frame.
	$effect(() => {
		void data.meta.slug;
		let frame = 0;
		let heads: HTMLElement[] = [];
		const measure = () => {
			frame = 0;
			const line = 120;
			let id = heads[0]?.id ?? '';
			for (const h of heads) {
				if (h.getBoundingClientRect().top - line > 0) break;
				id = h.id;
			}
			// At the very bottom the last section wins even if it is short.
			if (innerHeight + scrollY >= document.documentElement.scrollHeight - 4 && heads.length) id = heads[heads.length - 1].id;
			activeId = id;
		};
		const onscroll = () => (frame ||= requestAnimationFrame(measure));
		tick().then(() => {
			heads = [...(article?.querySelectorAll<HTMLElement>('h2[id], h3[id]') ?? [])];
			measure();
		});
		addEventListener('scroll', onscroll, { passive: true });
		addEventListener('resize', onscroll, { passive: true });
		return () => {
			removeEventListener('scroll', onscroll);
			removeEventListener('resize', onscroll);
			cancelAnimationFrame(frame);
		};
	});
</script>

<svelte:head>
	<title>{meta.title} · RivetPanel documentation</title>
	<meta name="description" content={meta.lede} />
</svelte:head>

<div class="docs-page">
	<article bind:this={article} class="min-w-0" aria-labelledby="doc-title">
		<nav class="docs-crumbs" aria-label="Breadcrumb">
			<ol>
				<li><a href="/docs">Docs</a></li>
				<li><Icon name="chevronRight" size={11} /><a href={href(data.category.slug)}>{data.category.title}</a></li>
			</ol>
			<a class="docs-crumbs-edit" href={source} target="_blank" rel="noreferrer"><Icon name="pencil" size={12} />Edit this page</a>
		</nav>

		<header class="docs-head">
			<h1 id="doc-title" class="docs-title">{meta.title}</h1>
			{#if meta.audience === 'admin'}<p class="docs-audience"><Icon name="key" size={12} />For administrators</p>{/if}
			<p class="docs-lede"><Inlines c={meta.ledeC} /></p>
			{#if meta.glance.length}
				<ul class="docs-glance" aria-label="At a glance">
					{#each meta.glanceC as fact, i (i)}<li><Inlines c={fact} /></li>{/each}
				</ul>
			{/if}
		</header>

		<div class="doc">
			<Blocks blocks={data.blocks} />
		</div>

		<footer class="docs-foot">
			{#if related.length}
				<section aria-labelledby="related-title">
					<h2 id="related-title" class="text-small font-semibold">Related</h2>
					<ul class="mt-2 grid gap-1 sm:grid-cols-2">
						{#each related as r (r!.slug)}<li><a class="docs-related" href={href(r!.slug)}><span class="font-medium text-ink">{r!.title}</span><span class="text-small text-muted">{r!.lede}</span></a></li>{/each}
					</ul>
				</section>
			{/if}
			<nav class="docs-pager" aria-label="Previous and next page">
				{#if data.prev}<a href={href(data.prev.slug)} rel="prev"><span class="text-small text-muted"><Icon name="chevronLeft" size={12} />Previous</span><span class="font-medium">{data.prev.title}</span></a>{:else}<span></span>{/if}
				{#if data.next}<a href={href(data.next.slug)} rel="next" class="text-right"><span class="justify-end text-small text-muted">Next<Icon name="chevronRight" size={12} /></span><span class="font-medium">{data.next.title}</span></a>{/if}
			</nav>
		</footer>
	</article>

	{#if toc.length > 1}
		<aside class="docs-toc" aria-label="On this page">
			<p class="docs-toc-title">On this page</p>
			<ul>
				{#each toc as h (h.id)}
					<li><a href={'#' + h.id} class:sub={h.d === 3} data-active={activeId === h.id} aria-current={activeId === h.id ? 'location' : undefined}>{h.text}</a></li>
				{/each}
			</ul>
			<a href="#top" class="docs-toc-top" onclick={(e) => { e.preventDefault(); scrollTo({ top: 0, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' }); }}><Icon name="chevronDown" size={12} class="rotate-180" />Back to top</a>
		</aside>
	{/if}
</div>
