<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { KBArticle, KBCategory } from '$lib/api/kb';
	import { visibilityLabel } from '$lib/api/kb';
	import { session } from '$lib/session.svelte';
	import PublicShell from '$lib/components/PublicShell.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { fmtWhen } from '$lib/args';

	let article = $state<KBArticle | null>(null);
	let category = $state<KBCategory | null>(null);
	let problem = $state<'' | 'signin' | 'missing' | 'error'>('');
	let message = $state('');

	async function load(slug: string) {
		problem = '';
		article = null;
		try {
			const r = await api<{ article: KBArticle; category?: KBCategory }>('GET', `/kb/articles/${encodeURIComponent(slug)}`);
			article = r.article;
			category = r.category ?? null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) problem = 'signin';
			else if (e instanceof ApiError && e.status === 404) problem = 'missing';
			else {
				problem = 'error';
				message = e instanceof ApiError ? e.message : 'The article could not be loaded.';
			}
		}
	}
	$effect(() => {
		void session.user;
		load(page.params.slug ?? '');
	});
</script>

<svelte:head><title>{article?.title ?? 'Help'} · RivetPanel</title></svelte:head>

<PublicShell>
	<nav class="text-small text-muted" aria-label="Breadcrumb"><a class="link" href="/help">Help center</a>{#if category}<span> / {category.name}</span>{/if}</nav>
	{#if problem === 'signin'}
		<div class="mt-6"><EmptyState title="Sign in to read this article">This panel's help articles are for signed-in accounts.{#snippet actions()}<a class="btn btn-primary" href="/login">Sign in</a>{/snippet}</EmptyState></div>
	{:else if problem === 'missing'}
		<div class="mt-6"><EmptyState title="Article not found">It may have been moved or unpublished, or it is not available to your account.{#snippet actions()}<a class="btn" href="/help">Back to the help center</a>{/snippet}</EmptyState></div>
	{:else if problem === 'error'}
		<Notice tone="fail" class="mt-6" live>{message}</Notice>
	{:else if article}
		<article class="mt-3 max-w-3xl">
			<h1 class="text-page font-semibold">{article.title}</h1>
			<p class="mt-1 text-small text-muted">
				Updated {fmtWhen(article.updated_at_ms)}
				{#if article.status === 'draft'}<span class="pill ml-2" data-tone="warn">Draft (not published)</span>{/if}
				{#if article.visibility !== 'public' && session.user}<span class="pill ml-2">{visibilityLabel[article.visibility]}</span>{/if}
			</p>
			{#if article.summary}<p class="mt-4 text-lg text-muted">{article.summary}</p>{/if}
			<div class="kb-body mt-5"><SafeMarkdown source={article.body ?? ''} relativeLinks /></div>
			<footer class="mt-10 flex flex-wrap gap-2 border-t border-rule-soft pt-5">
				<a class="btn btn-quiet" href="/help"><Icon name="chevronLeft" />All articles</a>
				{#if session.user}<a class="btn btn-quiet" href="/support"><Icon name="lifebuoy" />Still stuck? Open a ticket</a>{/if}
			</footer>
		</article>
	{/if}
</PublicShell>
