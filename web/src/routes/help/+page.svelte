<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { KBArticle, KBIndex } from '$lib/api/kb';
	import { visibilityLabel } from '$lib/api/kb';
	import { session } from '$lib/session.svelte';
	import PublicShell from '$lib/components/PublicShell.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import { fmtWhen } from '$lib/args';

	let index = $state<KBIndex | null>(null);
	let signInNeeded = $state(false);
	let error = $state('');
	let query = $state('');
	let results = $state<KBArticle[] | null>(null);

	async function load() {
		try {
			index = await api<KBIndex>('GET', '/kb');
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) signInNeeded = true;
			else error = e instanceof ApiError ? e.message : 'The help center could not be loaded.';
		}
	}
	$effect(() => {
		void session.user;
		load();
	});

	let timer: ReturnType<typeof setTimeout> | undefined;
	function search() {
		clearTimeout(timer);
		const q = query.trim();
		if (!q) {
			results = null;
			return;
		}
		timer = setTimeout(async () => {
			try {
				results = (await api<{ results: KBArticle[] }>('GET', `/kb/search?q=${encodeURIComponent(q)}`)).results;
			} catch (e) {
				error = e instanceof ApiError ? e.message : 'Search failed.';
			}
		}, 250);
	}

	const groups = $derived.by(() => {
		if (!index) return [];
		const out = index.categories.map((c) => ({ category: c, articles: index!.articles.filter((a) => a.category_id === c.id) }));
		const loose = index.articles.filter((a) => !a.category_id || !index!.categories.some((c) => c.id === a.category_id));
		if (loose.length) out.push({ category: { id: '', slug: '', name: index.categories.length ? 'Other articles' : 'Articles', description: '', position: 0 }, articles: loose });
		return out.filter((g) => g.articles.length);
	});
</script>

<svelte:head><title>Help center · RivetPanel</title></svelte:head>

<PublicShell>
	<header class="flex flex-wrap items-end gap-3 border-b border-rule-soft pb-5">
		<div class="min-w-0 flex-1">
			<h1 class="text-page">Help center<span class="text-action">.</span></h1>
			<p class="mt-0.5 text-muted">Answers written by the people who run this panel.</p>
		</div>
		{#if index?.manage}<a class="btn" href="/admin/kb"><Icon name="pencil" />Manage articles</a>{/if}
		{#if session.user}<a class="btn btn-quiet" href="/support"><Icon name="lifebuoy" />Contact support</a>{/if}
	</header>

	{#if signInNeeded}
		<div class="mt-6"><EmptyState title="Sign in to read the help center">This panel's help articles are for signed-in accounts.{#snippet actions()}<a class="btn btn-primary" href="/login">Sign in</a>{/snippet}</EmptyState></div>
	{:else if error}
		<Notice tone="fail" class="mt-6" live>{error}</Notice>
	{:else if index}
		<label class="relative mt-6 block max-w-xl">
			<span class="sr-only">Search help articles</span>
			<Icon name="search" size={15} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
			<input class="field pl-9" type="search" placeholder="Search articles" bind:value={query} oninput={search} maxlength="200" />
		</label>

		{#if results}
			<section class="mt-6" aria-live="polite">
				<h2 class="text-title font-semibold">{results.length ? `${results.length} result${results.length === 1 ? '' : 's'}` : 'No articles match'}</h2>
				<ul class="mt-3 grid gap-2">
					{#each results as a (a.id)}
						<li class="card p-4"><a class="font-medium link" href="/help/{a.slug}">{a.title}</a>{#if a.excerpt}<p class="mt-1 text-small text-muted">{a.excerpt}</p>{/if}</li>
					{/each}
				</ul>
			</section>
		{:else if !groups.length}
			<div class="mt-6"><EmptyState title="No articles yet">{#if index.manage}Write the first one under Administration → Knowledgebase.{:else}Nothing has been published here yet.{/if}</EmptyState></div>
		{:else}
			<div class="mt-6 grid gap-5 md:grid-cols-2">
				{#each groups as g (g.category.id)}
					<section class="card p-5">
						<h2 class="text-title font-semibold">{g.category.name}</h2>
						{#if g.category.description}<p class="mt-0.5 text-small text-muted">{g.category.description}</p>{/if}
						<ul class="mt-3 grid gap-1.5">
							{#each g.articles as a (a.id)}
								<li>
									<a class="link font-medium" href="/help/{a.slug}">{a.title}</a>
									{#if a.visibility !== 'public' && session.user}<span class="ml-1 text-[12px] text-muted">· {visibilityLabel[a.visibility]}</span>{/if}
									{#if a.summary}<p class="text-small text-muted">{a.summary}</p>{/if}
								</li>
							{/each}
						</ul>
						<p class="mt-3 text-[12px] text-muted">Updated {fmtWhen(Math.max(...g.articles.map((a) => a.updated_at_ms)))}</p>
					</section>
				{/each}
			</div>
		{/if}
	{/if}
</PublicShell>
