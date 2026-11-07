<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { session } from '$lib/session.svelte';
	import AppList from '$lib/components/apps/AppList.svelte';
	import GameServerList from '$lib/components/apps/GameServerList.svelte';
	import SiteList from '$lib/components/apps/SiteList.svelte';
	import StackList from '$lib/components/apps/StackList.svelte';
	import AppCatalog from '$lib/components/apps/AppCatalog.svelte';
	import NewMenu from '$lib/components/NewMenu.svelte';

	// Everything the account runs, in one place: bots, game servers,
	// containers (and their stacks) and websites. ?kind= narrows the list and
	// is what the former Bots, Game servers and Sites pages redirect to.
	type Kind = 'all' | 'bot' | 'game' | 'container' | 'web';
	const kinds = $derived(
		[
			{ id: 'all', label: 'All' },
			{ id: 'bot', label: 'Bots' },
			...(session.features.games ? [{ id: 'game', label: 'Game servers' }] : []),
			...(session.features.containers ? [{ id: 'container', label: 'Containers' }] : []),
			...(session.features.sites ? [{ id: 'web', label: 'Websites' }] : [])
		] as { id: Kind; label: string }[]
	);
	const kind = $derived.by((): Kind => {
		const k = page.url.searchParams.get('kind') as Kind | null;
		return k && kinds.some((x) => x.id === k) ? k : 'all';
	});
	const title = $derived(kind === 'all' ? 'Apps' : (kinds.find((k) => k.id === kind)?.label ?? 'Apps'));
	// Without game servers in their own view, a mixed list shows them as rows.
	const mixed = $derived<('bot' | 'game' | 'container')[]>(['bot', ...(session.features.games ? ['game' as const] : []), 'container']);

	function choose(k: Kind) {
		const u = new URL(page.url);
		// Filters belong to one view; switching kinds starts clean.
		for (const p of ['q', 'state', 'runtime', 'owner', 'tag', 'sort', 'new']) u.searchParams.delete(p);
		if (k === 'all') u.searchParams.delete('kind');
		else u.searchParams.set('kind', k);
		goto(u.pathname + u.search, { replaceState: true, keepFocus: true, noScroll: true });
	}
</script>

<svelte:head><title>{title} · RivetPanel</title></svelte:head>

<header class="page-head">
	<h1 class="font-semibold">Apps</h1>
	<div class="ml-auto"><NewMenu primary /></div>
</header>

<nav class="mt-3 flex gap-1 overflow-x-auto overflow-y-hidden border-b border-rule-soft [scrollbar-width:none]" aria-label="Kinds of apps">
	{#each kinds as k (k.id)}
		<a
			href={k.id === 'all' ? '/apps' : `/apps?kind=${k.id}`}
			class="-mb-px shrink-0 border-b-2 px-3 py-2 text-small font-medium {kind === k.id ? 'border-action text-ink' : 'border-transparent text-muted hover:text-ink'}"
			aria-current={kind === k.id ? 'page' : undefined}
			onclick={(e) => {
				if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
				e.preventDefault();
				choose(k.id);
			}}>{k.label}</a
		>
	{/each}
</nav>

{#key kind}
	{#if kind === 'all'}
		<AppList kinds={mixed} />
		{#if session.features.containers}<StackList />{/if}
		{#if session.features.sites}
			<section class="mt-8" aria-labelledby="web-h">
				<h2 id="web-h" class="text-section font-semibold">Websites</h2>
				<SiteList />
			</section>
		{/if}
	{:else if kind === 'bot'}
		<AppList kinds={['bot']} noun="bots" />
	{:else if kind === 'game'}
		<GameServerList />
	{:else if kind === 'container'}
		<AppList kinds={['container']} noun="containers" />
		<StackList />
		<AppCatalog limit={12} />
	{:else if kind === 'web'}
		<SiteList />
	{/if}
{/key}
