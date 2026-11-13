<script lang="ts">
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { afterNavigate } from '$app/navigation';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import { session } from '$lib/session.svelte';
	import { manifest, href } from '$lib/docs';
	import DocsSearch from '$lib/docs/DocsSearch.svelte';

	let { children }: { children: Snippet } = $props();

	const path = $derived(page.url.pathname.replace(/\/$/, ''));
	const current = $derived(path.split('/')[2] ?? '');
	// Categories the reader opened by hand; the current one is always open.
	let opened = $state<Record<string, boolean>>({});
	let sheet = $state<HTMLDialogElement>();

	afterNavigate(() => sheet?.open && sheet.close());

	const signedOut = $derived(session.loaded && !session.user);
</script>

{#snippet rail(inSheet: boolean)}
	<div class="px-1 pb-3"><DocsSearch compact /></div>
	<nav aria-label="Documentation" class="grid gap-0.5">
		<a href="/docs" class="docs-cat-link" data-active={path === '/docs'}><Icon name="home" size={15} class="text-muted" />Overview</a>
		{#each manifest.categories as c (c.slug)}
			{@const open = c.slug === current || opened[c.slug]}
			<div class="mt-0.5">
				<div class="flex items-center">
					<a href={href(c.slug)} class="docs-cat-link flex-1" data-active={path === `/docs/${c.slug}`} aria-current={path === `/docs/${c.slug}` ? 'page' : undefined}>
						<Icon name={c.icon as IconName} size={15} class="text-muted" />{c.title}
					</a>
					<button type="button" class="docs-cat-toggle" aria-expanded={open} aria-label={`${open ? 'Hide' : 'Show'} ${c.title} pages`} onclick={() => (opened[c.slug] = !open)}>
						<Icon name="chevronDown" size={14} class={open ? '' : '-rotate-90'} />
					</button>
				</div>
				{#if open}
					<ul class="docs-pages">
						{#each c.pages as p (p.slug)}
							{@const active = path === `/docs/${p.slug}`}
							<li><a href={href(p.slug)} class="docs-page-link" data-active={active} aria-current={active ? 'page' : undefined}>{p.title}{#if p.audience === 'admin'}<span class="docs-admin-dot" title="For administrators"></span>{/if}</a></li>
						{/each}
					</ul>
				{/if}
			</div>
		{/each}
	</nav>
	<div class="mt-5 grid gap-1 border-t border-rule-soft px-2 pt-4 text-small">
		<a class="docs-aux-link" href="/api/v1/automation/openapi.yaml" target="_blank" rel="noreferrer"><Icon name="code" size={13} />Automation OpenAPI</a>
		<a class="docs-aux-link" href="https://github.com/xenycx/rivetpanel/tree/main/docs/guide" target="_blank" rel="noreferrer"><Icon name="github" size={13} />Guide source on GitHub</a>
	</div>
	{#if inSheet}<span class="sr-only">End of contents</span>{/if}
{/snippet}

{#if signedOut}
	<header class="docs-public-bar">
		<a href="/" class="flex items-center gap-2 font-semibold"><img src="/favicon.svg" alt="" width="24" height="24" class="rounded-control" />RivetPanel</a>
		<span class="text-muted" aria-hidden="true">/</span>
		<a href="/docs" class="font-medium">Documentation</a>
		<a href="/login" class="btn btn-sm ml-auto">Sign in</a>
	</header>
{/if}

<div class="docs-shell" class:signed-out={signedOut}>
	<aside class="docs-rail scrollbar-thin" aria-label="Documentation contents">
		{@render rail(false)}
	</aside>

	<div class="min-w-0">
		<div class="docs-mobile-bar">
			<button type="button" class="btn btn-sm" aria-haspopup="dialog" onclick={() => sheet?.showModal()}><Icon name="menu" size={14} />Contents</button>
			<div class="min-w-0 flex-1"><DocsSearch compact /></div>
		</div>
		{@render children()}
	</div>
</div>

<dialog bind:this={sheet} class="docs-sheet" aria-label="Documentation contents" onclick={(e) => e.target === sheet && sheet?.close()}>
	<div class="flex items-center justify-between border-b border-rule-soft px-4 py-3">
		<p class="font-semibold">Contents</p>
		<button type="button" class="btn btn-quiet btn-icon btn-sm" aria-label="Close contents" onclick={() => sheet?.close()}><Icon name="x" size={16} /></button>
	</div>
	<div class="scrollbar-thin overflow-y-auto p-3">{@render rail(true)}</div>
</dialog>
