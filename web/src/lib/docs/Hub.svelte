<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import releases from 'virtual:docs/changelog';
	import DocsSearch from './DocsSearch.svelte';
	import { manifest, href, resolveAlias, pageMeta } from './index';
	import type { Inline } from './types';
	// web/package.json tracks the root VERSION file (make version-check).
	import { version } from '../../../package.json';

	const pageCount = manifest.order.length;

	// Old single-page links (/docs#sites) open the page that took the section over.
	onMount(() => {
		const target = location.hash && resolveAlias(location.hash);
		if (target) goto(target, { replaceState: true });
	});

	const start = [
		{ slug: 'get-started/quick-start', verb: 'Run your first app' },
		{ slug: 'get-started/concepts', verb: 'Learn the moving parts' },
		{ slug: 'observe/health', verb: 'Know when it breaks' }
	].map((s) => ({ ...s, meta: pageMeta(s.slug) })).filter((s) => s.meta);

	// The bold lead-in of each entry in the latest release, as a short list.
	const latest = releases.find((r) => r.version !== 'Unreleased');
	const leadText = (c: Inline[]) => {
		const first = c[0];
		return first && typeof first === 'object' && first.t === 'b' ? first.c.map((x) => (typeof x === 'string' ? x : 'v' in x ? x.v : '')).join('').replace(/\.$/, '') : '';
	};
	const highlights = (latest?.blocks ?? [])
		.flatMap((b) => (b.t === 'ul' ? b.items : []))
		.map((item) => (item[0]?.t === 'p' ? leadText(item[0].c) : ''))
		.filter(Boolean)
		.slice(0, 5);
</script>

<svelte:head>
	<title>Documentation · RivetPanel</title>
	<meta name="description" content="How to run bots, game servers, containers and websites on RivetPanel, and how to install, secure and operate it." />
</svelte:head>

<div class="docs-hub">
	<header class="docs-hub-head">
		<h1 class="docs-title">Documentation</h1>
		<p class="docs-lede">How to run bots, game servers, containers and websites on RivetPanel, and how to install, secure and operate it. This guide describes version {version}.</p>
		<div class="mt-6 max-w-2xl"><DocsSearch /></div>
		<p class="mt-2 text-small text-muted">{pageCount} pages in {manifest.categories.length} sections. Press <kbd class="docs-kbd">/</kbd> to search from any page.</p>
	</header>

	{#if start.length}
		<section class="docs-start" aria-labelledby="start-title">
			<h2 id="start-title" class="docs-section-title">New here</h2>
			<ol class="docs-route">
				{#each start as s, i (s.slug)}
					<li>
						<a href={href(s.slug)}>
							<span class="docs-route-no">{i + 1}</span>
							<span class="docs-route-verb">{s.verb}</span>
							<span class="docs-route-page">{s.meta!.title}<Icon name="chevronRight" size={12} /></span>
						</a>
					</li>
				{/each}
			</ol>
		</section>
	{/if}

	<section aria-labelledby="contents-title" class="mt-12">
		<h2 id="contents-title" class="docs-section-title">Contents</h2>
		<div class="docs-contents">
			{#each manifest.categories as c (c.slug)}
				<section class="docs-contents-cat" aria-labelledby={'cat-' + c.slug}>
					<a href={href(c.slug)} class="docs-contents-head">
						<span class="docs-cat-glyph"><Icon name={c.icon as IconName} size={18} /></span>
						<span class="min-w-0"><span id={'cat-' + c.slug} class="block font-semibold text-title">{c.title}</span><span class="block text-small text-muted">{c.blurb}</span></span>
					</a>
					<ul class="docs-contents-pages">
						{#each c.pages as p (p.slug)}<li><a href={href(p.slug)}>{p.title}{#if p.audience === 'admin'}<span class="docs-admin-dot" title="For administrators"></span>{/if}</a></li>{/each}
					</ul>
				</section>
			{/each}
		</div>
	</section>

	<section class="docs-hub-foot" aria-label="Release notes and references">
		{#if latest}
			<div>
				<h2 class="docs-section-title">New in {latest.version}</h2>
				{#if highlights.length}<ul class="mt-3 grid gap-1.5">{#each highlights as h (h)}<li class="flex gap-2"><Icon name="check" size={14} class="mt-1 shrink-0 text-run" />{h}</li>{/each}</ul>{/if}
				<a class="link mt-3 inline-block text-small" href={href('get-started/whats-new')}>Read the release notes</a>
			</div>
		{/if}
		<div>
			<h2 class="docs-section-title">Reference</h2>
			<ul class="mt-3 grid gap-1.5 text-small">
				<li><a class="link" href={href('observe/widget-data')}>Widget data shapes</a></li>
				<li><a class="link" href={href('observe/telemetry')}>Telemetry limits</a></li>
				<li><a class="link" href={href('operate/configuration')}>Configuration variables</a></li>
				<li><a class="link" href="/api/v1/automation/openapi.yaml" target="_blank" rel="noreferrer">Automation OpenAPI specification</a></li>
				<li><a class="link" href="https://github.com/xenycx/rivetpanel/blob/main/CHANGELOG.md" target="_blank" rel="noreferrer">Full changelog</a></li>
			</ul>
		</div>
	</section>
</div>
